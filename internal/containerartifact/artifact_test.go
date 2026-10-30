package containerartifact

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

const index = `{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"linux","architecture":"arm64"}},{"platform":{"os":"unknown","architecture":"unknown"}}]}`

type fixture struct {
	dir, script, archive string
	env                  []string
}

var artifactBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "container-artifact-tests-")
	if err != nil {
		panic(err)
	}
	artifactBinary = filepath.Join(dir, "container-artifact")
	cmd := exec.Command("go", "build", "-o", artifactBinary, "../../cmd/container-artifact")
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic(fmt.Sprintf("build artifact command: %v\n%s", err, output))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	for _, command := range []string{"bash"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("test dependency %s: %v", command, err)
		}
	}
	f := &fixture{dir: t.TempDir(), script: artifactBinary}
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
	cmd := exec.Command(f.script, args...)
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
	if !strings.Contains(events, "cleanup:rm --force --volumes leaked-test-container") {
		t.Fatalf("missing container cleanup: %s", events)
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
			if strings.Contains(step.Run, `container-artifact" publish`) {
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

func TestProcessTimeoutAndCancellation(t *testing.T) {
	for _, mode := range []string{"deadline", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if mode == "cancellation" {
				time.AfterFunc(30*time.Millisecond, cancel)
			}
			cmd := exec.Command("bash", "-c", `trap '' TERM; sleep 60 & wait`)
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
			start := time.Now()
			if err := runProcess(ctx, cmd, 50*time.Millisecond); err == nil || !strings.Contains(err.Error(), "context") {
				t.Fatalf("expected context failure: %v", err)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("process group did not terminate within the grace period")
			}
		})
	}
}

func TestPlatformVariantsCannotCoverSameManifestTwice(t *testing.T) {
	raw := []byte(`{"manifests":[{"platform":{"os":"linux","architecture":"arm64","variant":"v8"}},{"platform":{"os":"linux","architecture":"amd64"}}]}`)
	if _, err := validatePlatforms(raw, "linux/arm64,linux/arm64/v8"); err == nil {
		t.Fatal("two selectors for one image must not authorize an untested image")
	}
	if _, err := validatePlatforms(raw, "linux/amd64,linux/arm64/v8"); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptedHookCannotAuthorizeUpload(t *testing.T) {
	f := newFixture(t)
	f.write(t, "image.tar.tested", "stale receipt")
	f.write(t, "tests/smoke.sh", `#!/usr/bin/env bash
trap 'exit 143' TERM
echo hook-ready
sleep 60 &
wait
`)
	cmd := exec.Command(f.script, "test", f.archive, "linux/amd64,linux/arm64", "1.2.3")
	cmd.Dir, cmd.Env = f.dir, f.env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "hook-ready" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("hook exited before readiness")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hook did not start")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted tests succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted hook was not cleaned up")
	}
	if _, err := os.Stat(f.archive + ".tested"); !os.IsNotExist(err) {
		t.Fatal("interrupted tests retained a publication receipt")
	}
	events := f.events(t)
	if !strings.Contains(events, "cleanup:rm --force --volumes leaked-test-container") || !strings.Contains(events, "remove:tuxcrate-test:") {
		t.Fatalf("interrupted test did not clean up: %s", events)
	}
	if output, err := f.run(t, "publish", f.archive, "ghcr.io/example/image"); err == nil || !strings.Contains(output, "No successful test receipt") {
		t.Fatalf("interrupted test authorized upload: %v\n%s", err, output)
	}
}

func TestInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"test", "archive"}, {"publish", "archive"}, {"test", "archive", "", "1.2.3"}, {"publish", "archive", "image", "extra"}} {
		if err := (app{io.Discard, io.Discard}).execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("args %v: got %v", args, err)
		}
	}
}

func TestOutputFailureInvalidatesReceipt(t *testing.T) {
	f := newFixture(t)
	f.env = append(f.env, "GITHUB_OUTPUT="+f.dir)
	if output, err := f.run(t, "test", f.archive, "linux/amd64,linux/arm64", "1.2.3"); err == nil {
		t.Fatalf("expected output failure: %s", output)
	}
	if _, err := os.Stat(f.archive + ".tested"); !os.IsNotExist(err) {
		t.Fatal("output failure retained a publication receipt")
	}
}
