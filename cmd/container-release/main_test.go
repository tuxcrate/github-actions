package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateVersion(t *testing.T) {
	for _, version := range []string{"0.7.0", "1.0.0", "13.20.102", "0.7.0-rc1", "1.2.3-rc.1", "1.2.3-0"} {
		if err := validateVersion(version); err != nil {
			t.Error(err)
		}
	}
	for _, version := range []string{"v0.7.0", "0.7", "01.7.0", "0.7.0-rc.01", "1.2.3+build", "1.2.3-r0", "1.2.3-rc.1-r3", "1.2.3-", "1.2.3-a..b", "0.7.0\n", "$(id)", "1.2.3-" + strings.Repeat("a", 101)} {
		if validateVersion(version) == nil {
			t.Errorf("accepted invalid version %q", version)
		}
	}
}

func TestNextRevision(t *testing.T) {
	for _, tt := range []struct {
		name, version string
		tags          []string
		want          uint64
	}{
		{"first", "0.7.0", nil, 0},
		{"highest", "0.7.0", []string{"v0.7.0-r0", "v0.7.0-r12"}, 13},
		{"reset", "0.7.1", []string{"v0.7.0-r12"}, 0},
		{"unrelated", "0.7.0", []string{"v0.7.0-r01", "v0.7.0-r1-extra", "v0.7.0"}, 0},
		{"prerelease", "0.8.0-rc.1", []string{"v0.8.0-rc.1-r2", "v0.8.0-rc.2-r8", "v0.8.0-r9"}, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := nextRevision(tt.version, tt.tags)
			if err != nil || got != tt.want {
				t.Fatalf("got %d, %v; want %d", got, err, tt.want)
			}
		})
	}
	for _, tag := range []string{"v0.7.0-r18446744073709551615", "v0.7.0-r18446744073709551616"} {
		if _, err := nextRevision("0.7.0", []string{tag}); err == nil {
			t.Error("revision overflow accepted")
		}
	}
	if _, err := nextRevision("invalid", nil); err == nil {
		t.Error("invalid version accepted")
	}
}

func TestImageTags(t *testing.T) {
	want := []string{"0.7.0-r2", "0.7.0", "0.7", "latest"}
	if got := imageTags("0.7.0", 2); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

func TestImageName(t *testing.T) {
	if got, err := imageName("", "Tuxcrate/Zrepl"); err != nil || got != "ghcr.io/tuxcrate/zrepl" {
		t.Fatalf("%s: %v", got, err)
	}
	for _, image := range []string{"docker.io/tuxcrate/zrepl", "ghcr.io/tuxcrate/zrepl:latest", "ghcr.io/tuxcrate/zrepl\ninjected"} {
		if _, err := imageName(image, "tuxcrate/zrepl"); err == nil {
			t.Errorf("accepted %q", image)
		}
	}
}

func testApp(t *testing.T) (app, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	env := map[string]string{
		"APP_VERSION": "0.7.0", "IMAGE": "ghcr.io/tuxcrate/zrepl",
		"IMAGE_DIGEST":      "sha256:" + strings.Repeat("a", 64),
		"GITHUB_REPOSITORY": "tuxcrate/zrepl", "GITHUB_REF_NAME": "main",
		"GITHUB_SHA": strings.Repeat("b", 40), "GITHUB_SERVER_URL": "https://github.com",
		"GITHUB_EVENT_NAME": "schedule", "PLATFORMS": "linux/amd64,linux/arm64",
		"GITHUB_OUTPUT": filepath.Join(dir, "output"), "GITHUB_STEP_SUMMARY": filepath.Join(dir, "summary"),
	}
	return app{exec: execCommand, getenv: func(key string) string { return env[key] }}, env
}

func TestMetadata(t *testing.T) {
	a, env := testApp(t)
	env["IMAGE"] = ""
	if err := a.metadata(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(env["GITHUB_OUTPUT"])
	if err != nil || string(data) != "image<<TUXCRATE_OUTPUT\nghcr.io/tuxcrate/zrepl\nTUXCRATE_OUTPUT\n" {
		t.Fatalf("%s: %v", data, err)
	}
	env["APP_VERSION"] = "invalid"
	if a.metadata() == nil {
		t.Fatal("invalid metadata accepted")
	}
}

func TestCurrentCommit(t *testing.T) {
	for _, head := range []string{"old", strings.Repeat("b", 40)} {
		a, env := testApp(t)
		a.exec = func(_ string, args ...string) (commandResult, error) {
			want := []string{"gh", "api", "repos/tuxcrate/zrepl/git/ref/heads/main", "--jq", ".object.sha"}
			if !reflect.DeepEqual(args, want) {
				t.Fatal(args)
			}
			return commandResult{stdout: head}, nil
		}
		err := a.assertCurrentCommit()
		if (err == nil) != (head == env["GITHUB_SHA"]) {
			t.Fatalf("head %s: %v", head, err)
		}
	}
}

func TestTagAvailability(t *testing.T) {
	for _, tt := range []struct {
		name      string
		result    commandResult
		err       error
		wantError bool
	}{
		{"existing", commandResult{}, nil, true},
		{"missing", commandResult{code: 1, stderr: "ERROR: not found"}, nil, false},
		{"manifest unknown", commandResult{code: 1, stderr: "manifest unknown"}, nil, false},
		{"auth", commandResult{code: 1, stderr: "unauthorized"}, nil, true},
		{"network", commandResult{code: 1, stderr: "connection refused"}, nil, true},
		{"tls", commandResult{code: 1, stderr: "TLS certificate error"}, nil, true},
		{"missing executable", commandResult{}, errors.New("executable not found"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := testApp(t)
			a.exec = func(_ string, args ...string) (commandResult, error) { return tt.result, tt.err }
			if err := a.assertTagAvailable("ghcr.io/tuxcrate/zrepl", "0.7.0-r0"); (err != nil) != tt.wantError {
				t.Fatal(err)
			}
		})
	}
}

func TestPreviousRelease(t *testing.T) {
	releases := []release{
		{TagName: "v0.7.0-r0", PublishedAt: "2026-01-01"},
		{TagName: "v0.7.0-r1", PublishedAt: "2026-01-02"},
		{TagName: "v0.7.0-r2", Draft: true},
		{TagName: "v0.7.0-r3", Prerelease: true, PublishedAt: "2026-01-03"},
		{TagName: "unrelated", PublishedAt: "2026-01-04"},
	}
	a, _ := testApp(t)
	var checked []string
	a.exec = func(_ string, args ...string) (commandResult, error) {
		checked = append(checked, args[3])
		if args[3] == "v0.7.0-r1" {
			return commandResult{code: 1}, nil
		}
		return commandResult{}, nil
	}
	got, err := a.previousRelease(releases)
	if err != nil || got != "v0.7.0-r0" || !reflect.DeepEqual(checked, []string{"v0.7.0-r1", "v0.7.0-r0"}) {
		t.Fatalf("%s, %v, %v", got, err, checked)
	}
	a.exec = func(_ string, args ...string) (commandResult, error) {
		return commandResult{code: 128, stderr: "missing tag"}, nil
	}
	if _, err := a.previousRelease(releases); err == nil {
		t.Fatal("missing tag accepted")
	}
	a.exec = func(_ string, args ...string) (commandResult, error) { return commandResult{code: 1}, nil }
	if got, err := a.previousRelease(releases); got != "" || err != nil {
		t.Fatalf("%s: %v", got, err)
	}
}

func TestReservation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmtBool(fail), func(t *testing.T) {
			a, env := testApp(t)
			calls := 0
			a.exec = func(input string, args ...string) (commandResult, error) {
				calls++
				var want []string
				var stdout string
				switch calls {
				case 1:
					want = []string{"gh", "api", "repos/tuxcrate/zrepl/git/ref/heads/main", "--jq", ".object.sha"}
					stdout = env["GITHUB_SHA"]
				case 2:
					want = []string{"gh", "api", "--paginate", "--slurp", "repos/tuxcrate/zrepl/releases?per_page=100"}
					stdout = `[[{"tag_name":"v0.7.0-r3","draft":true}],[{"tag_name":"v0.7.0-r8","draft":true}]]`
				case 3:
					want = []string{"git", "tag", "--list"}
					stdout = "v0.7.0-r1"
				case 4:
					want = []string{"docker", "buildx", "imagetools", "inspect", "ghcr.io/tuxcrate/zrepl:0.7.0-r9"}
				case 5:
					want = []string{"git", "log", "--reverse", "--format=%H%x09%s", "HEAD"}
				case 6:
					want = []string{"gh", "api", "--method", "POST", "repos/tuxcrate/zrepl/releases", "--input", "-"}
					var payload map[string]any
					if err := json.Unmarshal([]byte(input), &payload); err != nil {
						t.Fatal(err)
					}
					if payload["tag_name"] != "v0.7.0-r9" || payload["draft"] != true || payload["prerelease"] != false || payload["target_commitish"] != env["GITHUB_SHA"] {
						t.Fatal(payload)
					}
					if !strings.Contains(payload["body"].(string), "Scheduled security rebuild") {
						t.Fatal(payload)
					}
					if _, err := os.Stat(env["GITHUB_OUTPUT"]); !os.IsNotExist(err) {
						t.Fatal("outputs written before reservation")
					}
				default:
					t.Fatalf("unexpected command: %v", args)
				}
				if !reflect.DeepEqual(args, want) {
					t.Fatalf("got %v; want %v", args, want)
				}
				if calls == 4 {
					return commandResult{code: 1, stderr: "not found"}, nil
				}
				if calls == 6 && fail {
					return commandResult{code: 1, stderr: "API unavailable"}, nil
				}
				return commandResult{stdout: stdout}, nil
			}
			err := a.reserve()
			if (err != nil) != fail || calls != 6 {
				t.Fatalf("calls %d: %v", calls, err)
			}
			if fail {
				if _, err := os.Stat(env["GITHUB_OUTPUT"]); !os.IsNotExist(err) {
					t.Fatal("failed reservation exposed outputs")
				}
				return
			}
			data, err := os.ReadFile(env["GITHUB_OUTPUT"])
			if err != nil || !strings.Contains(string(data), "tag<<TUXCRATE_OUTPUT\nv0.7.0-r9\n") || !strings.Contains(string(data), "0.7.0-r9\n0.7.0\n0.7\nlatest\n") {
				t.Fatalf("%s: %v", data, err)
			}
			data, err = os.ReadFile(env["GITHUB_STEP_SUMMARY"])
			if err != nil || !strings.HasPrefix(string(data), "# 0.7.0-r9\n") {
				t.Fatalf("%s: %v", data, err)
			}
		})
	}
}

func fmtBool(fail bool) string {
	if fail {
		return "failed POST"
	}
	return "successful reservation"
}

func TestReleaseNotesFormatting(t *testing.T) {
	a, env := testApp(t)
	a.exec = func(_ string, args ...string) (commandResult, error) {
		if !reflect.DeepEqual(args, []string{"git", "log", "--reverse", "--format=%H%x09%s", "HEAD"}) {
			t.Fatalf("unexpected command: %v", args)
		}
		return commandResult{}, nil
	}
	tags := []string{"0.7.0-r1", "latest", "0.7.0", "0", "stable", "0.7"}
	originalTags := append([]string(nil), tags...)
	notes, err := a.releaseNotes("0.7.0", tags, env["IMAGE"], env["IMAGE_DIGEST"], "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tags, originalTags) {
		t.Fatalf("release notes changed image tags: %v", tags)
	}
	for _, want := range []string{
		"- Immutable Image: `ghcr.io/tuxcrate/zrepl:0.7.0-r1@" + env["IMAGE_DIGEST"] + "`\n",
		"- Moving aliases:\n  - `ghcr.io/tuxcrate/zrepl:0.7.0`\n  - `ghcr.io/tuxcrate/zrepl:0.7`\n  - `ghcr.io/tuxcrate/zrepl:0`\n  - `ghcr.io/tuxcrate/zrepl:latest`\n  - `ghcr.io/tuxcrate/zrepl:stable`\n",
		"- Platforms: `linux/amd64`, `linux/arm64`\n",
		"\n\n## Packaging changes\n\n",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("missing %q in %s", want, notes)
		}
	}
	for _, absent := range []string{
		"- Digest pin:",
		"- Source:",
		"[Full diff]",
		"This rebuild uses the base image reference recorded in source and refreshes installed OS packages.",
		"A pinned base image changes only when its digest is updated.",
		"This does not guarantee that vulnerabilities in the upstream application binary are fixed.",
	} {
		if strings.Contains(notes, absent) {
			t.Errorf("unexpected %q in %s", absent, notes)
		}
	}
}

func TestChangelogIntegration(t *testing.T) {
	dir := t.TempDir()
	for key, value := range map[string]string{"GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.com", "GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.com"} {
		t.Setenv(key, value)
	}
	a, env := testApp(t)
	// Execute in a temporary source checkout without changing process cwd.
	a.exec = func(input string, args ...string) (commandResult, error) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(input)
		data, err := cmd.CombinedOutput()
		if err != nil {
			return commandResult{}, errors.New(string(data))
		}
		return commandResult{stdout: strings.TrimSpace(string(data))}, nil
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := a.run("", args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	run("git", "init", "--quiet")
	run("git", "commit", "--allow-empty", "-m", "Initial container")
	initialSHA := run("git", "rev-parse", "HEAD")
	run("git", "tag", "v0.7.0-r0")
	run("git", "commit", "--allow-empty", "-m", "Refresh dependencies\n\nDetailed body")
	env["GITHUB_SHA"] = run("git", "rev-parse", "HEAD")
	tags := imageTags("0.7.0", 1)
	notes := func(previous string) string {
		t.Helper()
		text, err := a.releaseNotes("0.7.0", tags, env["IMAGE"], env["IMAGE_DIGEST"], previous)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	initialEntry := "- Initial container ([" + initialSHA[:7] + "](https://github.com/tuxcrate/zrepl/commit/" + initialSHA + "))"
	refreshEntry := "- Refresh dependencies ([" + env["GITHUB_SHA"][:7] + "](https://github.com/tuxcrate/zrepl/commit/" + env["GITHUB_SHA"] + "))"
	if first := notes(""); !strings.Contains(first, initialEntry+"\n"+refreshEntry) {
		t.Fatal(first)
	}
	text := notes("v0.7.0-r0")
	for _, want := range []string{refreshEntry, env["IMAGE_DIGEST"]} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	for _, absent := range []string{"Initial container", "Detailed body", "$REPOSITORY_URL", "- Source:", "[Full diff]"} {
		if strings.Contains(text, absent) {
			t.Errorf("unexpected %q in %s", absent, text)
		}
	}
	run("git", "tag", "v0.7.0-r1")
	if text := notes("v0.7.0-r1"); !strings.Contains(text, "Scheduled security rebuild") || !strings.Contains(text, "Application version unchanged") {
		t.Fatal(text)
	}
	env["GITHUB_EVENT_NAME"] = "workflow_dispatch"
	if text := notes("v0.7.0-r1"); !strings.Contains(text, "Image rebuild") || strings.Contains(text, "Scheduled security rebuild") {
		t.Fatal(text)
	}
}

func TestExecCommand(t *testing.T) {
	result, err := execCommand("hello", "sh", "-c", "cat; echo diagnostic >&2; exit 3")
	if err != nil || result.code != 3 || result.stdout != "hello" || result.stderr != "diagnostic" {
		t.Fatalf("%+v: %v", result, err)
	}
	if _, err := execCommand("", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing executable accepted")
	}
}
