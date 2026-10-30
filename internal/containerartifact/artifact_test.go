package containerartifact

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const index = `{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"linux","architecture":"arm64"}},{"platform":{"os":"unknown","architecture":"unknown"}}]}`

type fixture struct {
	dir, script, archive string
	env                  []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	for _, command := range []string{"bash", "jq", "sha256sum"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("test dependency %s: %v", command, err)
		}
	}
	script, err := filepath.Abs("../../scripts/container-artifact.sh")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: t.TempDir(), script: script}
	f.archive = filepath.Join(f.dir, "image.tar")
	f.write(t, "image.tar", "archive fixture")
	f.write(t, "index.json", index)
	f.write(t, "bin/skopeo", `#!/usr/bin/env bash
set -eu
if [[ $* == *'inspect --raw'* ]]; then
    cat "$FIXTURE/index.json"
elif [[ ${!#} == docker-daemon:* ]]; then
    echo "load:${!#}" >> "$FIXTURE/events"
elif [[ ${!#} == docker://* ]]; then
    echo "publish:$*" >> "$FIXTURE/events"
else
    echo "Unexpected skopeo command: $*" >&2
    exit 1
fi
`)
	f.write(t, "bin/docker", `#!/usr/bin/env bash
set -eu
case "$1 $2" in
    'image inspect')
        if [[ ${WRONG_PLATFORM:-} == true ]]; then
            echo linux/386
        elif [[ ${!#} == *-1 ]]; then
            echo linux/arm64
        else
            echo linux/amd64
        fi
        ;;
    'image rm') echo "remove:${!#}" >> "$FIXTURE/events" ;;
    'ps -aq') echo leaked-test-container ;;
    'rm --force') echo "cleanup:$*" >> "$FIXTURE/events" ;;
    *) echo "Unexpected docker command: $*" >&2; exit 1 ;;
esac
`)
	f.write(t, "bin/timeout", `#!/usr/bin/env bash
set -eu
echo "timeout:$1 $2 $3" >> "$FIXTURE/events"
if [[ ${SIMULATE_TIMEOUT:-} == true ]]; then exit 124; fi
shift 3
exec "$@"
`)
	f.write(t, "tests/smoke.sh", `#!/usr/bin/env bash
set -eu
test -z "${GH_TOKEN:-}"
test -z "${GITHUB_TOKEN:-}"
test -n "$TUXCRATE_TEST_ID"
echo "hook:$3:$2" >> "$FIXTURE/events"
if [[ ${FAIL_PLATFORM:-} == "$3" ]]; then exit 23; fi
if [[ ${MUTATE_ARCHIVE:-} == true ]]; then echo mutation >> "$FIXTURE/image.tar"; fi
`)
	f.env = append(os.Environ(), "FIXTURE="+f.dir, "PATH="+filepath.Join(f.dir, "bin")+":"+os.Getenv("PATH"),
		"GITHUB_OUTPUT="+filepath.Join(f.dir, "output"), "GH_TOKEN=must-not-reach-hook", "GITHUB_TOKEN=must-not-reach-hook",
		"DOCKER_CONFIG="+filepath.Join(f.dir, "no-credentials"))
	return f
}

func (f *fixture) write(t *testing.T, name, data string) {
	t.Helper()
	path := filepath.Join(f.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0755); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{f.script}, args...)...)
	cmd.Dir, cmd.Env = f.dir, f.env
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (f *fixture) events(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "events"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestBuildTestPublishArtifact(t *testing.T) {
	f := newFixture(t)
	if output, err := f.run(t, "test", f.archive, "linux/amd64,linux/arm64", "1.2.3-4"); err != nil {
		t.Fatalf("test: %v\n%s", err, output)
	}
	events := f.events(t)
	first, second := strings.Index(events, "hook:linux/amd64:1.2.3-4"), strings.Index(events, "hook:linux/arm64:1.2.3-4")
	if first < 0 || second <= first || strings.Contains(events, "publish:") {
		t.Fatalf("unexpected test order: %s", events)
	}
	if !strings.Contains(events, "timeout:--signal=TERM --kill-after=30s 300s") || !strings.Contains(events, "cleanup:rm --force --volumes leaked-test-container") {
		t.Fatalf("missing bounded invocation or container cleanup: %s", events)
	}
	output, err := os.ReadFile(filepath.Join(f.dir, "output"))
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(index)))
	if !strings.Contains(string(output), "digest="+wantDigest) || !strings.Contains(string(output), "image=tuxcrate-test:") {
		t.Fatalf("unexpected outputs: %s", output)
	}
	if output, err := f.run(t, "publish", f.archive, "ghcr.io/example/image"); err != nil {
		t.Fatalf("publish: %v\n%s", err, output)
	}
	events = f.events(t)
	if !strings.Contains(events, "publish:copy --all --preserve-digests --retry-times 3") || !strings.Contains(events, "docker://ghcr.io/example/image@"+wantDigest) {
		t.Fatalf("upload must preserve attestations and digest, without a tag: %s", events)
	}
}

func TestFailedTestsNeverAuthorizeUpload(t *testing.T) {
	for _, tt := range []struct{ name, variable, want string }{
		{"first platform", "FAIL_PLATFORM=linux/amd64", "exit status 23"},
		{"second platform", "FAIL_PLATFORM=linux/arm64", "exit status 23"},
		{"timeout", "SIMULATE_TIMEOUT=true", "exit status 124"},
		{"wrong loaded architecture", "WRONG_PLATFORM=true", "Loaded linux/386"},
		{"modified during tests", "MUTATE_ARCHIVE=true", "changed during testing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.env = append(f.env, tt.variable)
			f.write(t, "image.tar.tested", "stale receipt")
			output, err := f.run(t, "test", f.archive, "linux/amd64,linux/arm64", "1.2.3")
			if err == nil || !strings.Contains(output+err.Error(), tt.want) {
				t.Fatalf("got %v\n%s; want %s", err, output, tt.want)
			}
			if _, err := os.Stat(f.archive + ".tested"); !os.IsNotExist(err) {
				t.Fatal("failed tests retained a publication receipt")
			}
			if output, err := f.run(t, "publish", f.archive, "ghcr.io/example/image"); err == nil || !strings.Contains(output, "No successful test receipt") {
				t.Fatalf("upload was not blocked: %v\n%s", err, output)
			}
			if strings.Contains(f.events(t), "publish:") {
				t.Fatal("failed tests reached the registry")
			}
		})
	}
}

func TestArchiveModificationAfterTestsBlocksUpload(t *testing.T) {
	f := newFixture(t)
	if output, err := f.run(t, "test", f.archive, "linux/amd64,linux/arm64", "1.2.3"); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	f.write(t, "image.tar", "different archive")
	if output, err := f.run(t, "publish", f.archive, "ghcr.io/example/image"); err == nil || !strings.Contains(output, "does not match the tested artifact") {
		t.Fatalf("upload was not blocked: %v\n%s", err, output)
	}
	if strings.Contains(f.events(t), "publish:") {
		t.Fatal("modified archive reached the registry")
	}
}

func TestMissingPlatformAndInvalidHook(t *testing.T) {
	for _, mode := range []string{"missing platform", "incomplete platforms", "duplicate platforms", "invalid hook"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			platform := "linux/386,linux/arm64"
			if mode == "incomplete platforms" {
				platform = "linux/amd64"
			}
			if mode == "duplicate platforms" {
				platform = "linux/amd64,linux/amd64"
			}
			if mode == "invalid hook" {
				platform = "linux/amd64"
				if err := os.Remove(filepath.Join(f.dir, "tests/smoke.sh")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(f.dir, "tests/smoke.sh"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if output, err := f.run(t, "test", f.archive, platform, "1.2.3"); err == nil {
				t.Fatalf("expected failure: %s", output)
			}
			if _, err := os.Stat(f.archive + ".tested"); !os.IsNotExist(err) {
				t.Fatal("invalid test input retained a publication receipt")
			}
		})
	}
}

func TestMissingHookCompatibility(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.dir, "tests/smoke.sh")); err != nil {
		t.Fatal(err)
	}
	if output, err := f.run(t, "test", f.archive, "linux/amd64,linux/arm64", "1.2.3"); err != nil || strings.Count(output, "No tests/smoke.sh") != 2 {
		t.Fatalf("%v\n%s", err, output)
	}
}

func TestWorkflowCannotUploadBeforeTestsAndMetadata(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string
				ID   string
				Uses string
				If   string
				Run  string
				With map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pull-request", "release"} {
		steps := workflow.Jobs[name].Steps
		build, test, metadata, login, upload := -1, -1, -1, -1, -1
		for i, step := range steps {
			if strings.HasPrefix(step.Uses, "docker/build-push-action@") {
				if build != -1 {
					t.Fatalf("%s rebuilds instead of publishing the tested archive", name)
				}
				build = i
				if step.With["push"] != "" || !strings.HasPrefix(step.With["outputs"], "type=oci,") || step.With["no-cache"] != "true" || step.With["provenance"] != "mode=max" || step.With["sbom"] != "true" {
					t.Fatalf("%s build must be local, uncached, and attested: %+v", name, step.With)
				}
			}
			if step.ID == "tested" {
				test = i
			}
			if strings.Contains(step.Run, " check-manifest") {
				metadata = i
			}
			if strings.HasPrefix(step.Uses, "docker/login-action@") {
				login = i
			}
			if strings.Contains(step.Run, "container-artifact.sh publish") {
				upload = i
				if name == "pull-request" && step.If != "env.PUBLISH_PR == 'true'" {
					t.Fatal("fork PRs must never upload")
				}
			}
		}
		if !(build >= 0 && build < test && test < metadata && metadata < login && login < upload) {
			t.Fatalf("%s must build -> test -> validate -> login -> upload; got %d %d %d %d %d", name, build, test, metadata, login, upload)
		}
	}
}
