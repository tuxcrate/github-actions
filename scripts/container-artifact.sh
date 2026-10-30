#!/usr/bin/env bash
set -euo pipefail

# Keep the OCI archive (including its index and attestations) unchanged between
# building, testing each runnable platform, and uploading it to the registry.
command=${1:?Usage: container-artifact.sh test|publish ARCHIVE ...}
archive=$(realpath "${2:?An OCI archive is required}")
receipt="$archive.tested"
archive_hash() { sha256sum "$archive" | cut -d ' ' -f 1; }
image_digest() { skopeo inspect --raw "oci-archive:$archive" | sha256sum | cut -d ' ' -f 1; }

case "$command" in
    test)
        platforms=${3:?A comma-separated platform list is required}
        version=${4:?The expected application version is required}
        rm -f "$receipt"
        original_hash=$(archive_hash)
        digest="sha256:$(image_digest)"
        scratch=$(mktemp -d)
        images=()
        test_ids=()
        passed=false
        cleanup() {
            status=$?
            for test_id in "${test_ids[@]}"; do
                # The shared timeout can interrupt a hook before its own trap
                # finishes. Labels let us clean up only this invocation's work.
                mapfile -t containers < <(docker ps -aq --filter "label=io.tuxcrate.smoke-test=$test_id")
                if ((${#containers[@]})); then
                    docker rm --force --volumes "${containers[@]}" || true
                fi
            done
            for image in "${images[@]}"; do
                # Retain the first tested image for local OS metadata validation.
                if [[ $passed == true && $image == "${images[0]}" ]]; then
                    continue
                fi
                docker image rm "$image" || true
            done
            rm -rf "$scratch"
            exit "$status"
        }
        trap cleanup EXIT
        trap 'exit 130' INT
        trap 'exit 143' TERM
        skopeo inspect --raw "oci-archive:$archive" > "$scratch/index.json"
        if [[ ( -e tests/smoke.sh || -L tests/smoke.sh ) && ! -f tests/smoke.sh ]]; then
            echo 'tests/smoke.sh must be a regular file.' >&2
            exit 1
        fi
        IFS=, read -ra platform_list <<< "$platforms"
        runnable=$(jq '[.manifests[] | select(.platform.os != "unknown" and .platform.architecture != "unknown")] | length' "$scratch/index.json")
        if ((${#platform_list[@]} == 0 || runnable != ${#platform_list[@]})); then
            echo 'The requested test platforms must cover every runnable image in the archive.' >&2
            exit 1
        fi
        declare -A seen_platforms=()
        for platform in "${platform_list[@]}"; do
            if [[ ! $platform =~ ^linux/[a-z0-9_]+(/[a-zA-Z0-9_.-]+)?$ ]]; then
                printf 'Unsupported test platform: %s\n' "$platform" >&2
                exit 1
            fi
            if [[ -n ${seen_platforms[$platform]:-} ]]; then
                printf 'Duplicate test platform: %s\n' "$platform" >&2
                exit 1
            fi
            seen_platforms[$platform]=true
            IFS=/ read -r os arch variant <<< "$platform"
            # Do not let a platform selector silently fall back to another image.
            jq -e --arg os "$os" --arg arch "$arch" --arg variant "${variant:-}" '
                [.manifests[] | select(.platform.os == $os and .platform.architecture == $arch
                    and ($variant == "" or (.platform.variant // "") == $variant))]
                | length == 1' "$scratch/index.json" > /dev/null
            image="tuxcrate-test:$(basename "$scratch")-${#images[@]}"
            images+=("$image")
            options=(--override-os "$os" --override-arch "$arch")
            if [[ -n ${variant:-} ]]; then
                options+=(--override-variant "$variant")
            fi
            skopeo "${options[@]}" copy "oci-archive:$archive" "docker-daemon:$image"
            actual=$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")
            if [[ $actual != "$os/$arch" ]]; then
                printf 'Loaded %s instead of %s.\n' "$actual" "$platform" >&2
                exit 1
            fi
            if [[ -f tests/smoke.sh ]]; then
                test_id="$(basename "$scratch")-${#test_ids[@]}"
                test_ids+=("$test_id")
                printf 'Running tests/smoke.sh for %s (%s).\n' "$platform" "$version"
                TUXCRATE_TEST_ID="$test_id" timeout --signal=TERM --kill-after=30s 300s \
                    env -u GH_TOKEN -u GITHUB_TOKEN bash tests/smoke.sh "$image" "$version" "$platform"
            else
                echo 'No tests/smoke.sh; retaining Containerfile assertions and platform validation only.'
            fi
        done
        if [[ $(archive_hash) != "$original_hash" ]]; then
            echo 'The OCI archive changed during testing; refusing publication.' >&2
            exit 1
        fi
        printf '%s\n%s\n' "$original_hash" "$digest" > "$receipt"
        if [[ -n ${GITHUB_OUTPUT:-} ]]; then
            printf 'digest=%s\nimage=%s\n' "$digest" "${images[0]}" >> "$GITHUB_OUTPUT"
        fi
        passed=true
        ;;
    publish)
        destination=${3:?An untagged destination image is required}
        if [[ ! -f $receipt ]]; then
            echo 'No successful test receipt; refusing to upload the image.' >&2
            exit 1
        fi
        mapfile -t tested < "$receipt"
        digest="sha256:$(image_digest)"
        if [[ ${tested[0]:-} != "$(archive_hash)" || ${tested[1]:-} != "$digest" ]]; then
            echo 'The OCI archive does not match the tested artifact; refusing publication.' >&2
            exit 1
        fi
        options=()
        authfile="${DOCKER_CONFIG:-$HOME/.docker}/config.json"
        if [[ -f $authfile ]]; then
            options+=(--authfile "$authfile")
        fi
        # A digest-only destination creates no moving or revision tags. Copy all
        # manifests, including provenance/SBOMs, without re-encoding their digests.
        skopeo copy --all --preserve-digests --retry-times 3 "${options[@]}" \
            "oci-archive:$archive" "docker://$destination@$digest"
        ;;
    *)
        echo 'Usage: container-artifact.sh test|publish ARCHIVE ...' >&2
        exit 1
        ;;
esac
