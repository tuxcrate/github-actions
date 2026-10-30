package containerrelease

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tuxcrate/github-actions/internal/releaseconfig"
)

const changeConfig = `tracks:
  - name: stable
    version: "1.2.3"
    tags: ["${version}", latest]
  - name: preview
    version: "2.0.0-rc.1"
    tags: ["${version}", preview]
    context: preview
    containerfile: preview/Containerfile
`

func writeSource(t *testing.T, name, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func commitSource(t *testing.T, a app) {
	t.Helper()
	if _, err := a.run("", "git", "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := a.run("", "git", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "test: update source"); err != nil {
		t.Fatal(err)
	}
}

func changeApp(t *testing.T) (app, map[string]string, []releaseconfig.Build, []release) {
	t.Helper()
	a, env := configApp(t, changeConfig)
	env["GITHUB_EVENT_NAME"] = "push"
	for key, value := range map[string]string{
		"GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.com",
		"GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		t.Setenv(key, value)
	}
	if _, err := a.run("", "git", "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"Containerfile": "FROM scratch\nCOPY . /app\n",
		".dockerignore": ".git\n.github\nREADME.md\nLICENSE\nrenovate.json\nmeta.yaml\ntests\npreview\nignored/**\n!ignored/keep.txt\n",
		"README.md":     "Documentation\n", ".github/workflows/container.yml": "name: Container\n",
		"app.txt": "application\n", "requirements.txt": "dependency==1.0\n",
		"tests/smoke.sh": "#!/bin/sh\nexit 0\n", "ignored/drop.txt": "ignored\n", "ignored/keep.txt": "included\n",
		"preview/Containerfile": "FROM scratch\nCOPY . /app\n", "preview/.dockerignore": "*.md\n",
		"preview/app.txt": "preview\n", "preview/README.md": "preview documentation\n",
	} {
		writeSource(t, name, text)
	}
	commitSource(t, a)
	for _, tag := range []string{"v1.2.3-r0", "v2.0.0-rc.1-r0"} {
		if _, err := a.run("", "git", "tag", tag); err != nil {
			t.Fatal(err)
		}
	}
	builds, err := a.builds()
	if err != nil {
		t.Fatal(err)
	}
	return a, env, builds, []release{
		{TagName: "v1.2.3-r0", PublishedAt: "2026-01-01"}, // Legacy stable marker is optional.
		{TagName: "v2.0.0-rc.1-r0", PublishedAt: "2026-01-01", Prerelease: true, Body: "<!-- tuxcrate-track:preview -->"},
	}
}

func TestTrackChanged(t *testing.T) {
	for _, tt := range []struct {
		name, file, text string
		stable, preview  bool
	}{
		{name: "identical inputs"},
		{name: "README", file: "README.md", text: "New docs"},
		{name: "workflow", file: ".github/workflows/container.yml", text: "name: Updated"},
		{name: "ignored file", file: "ignored/drop.txt", text: "Updated"},
		{name: "negated ignore", file: "ignored/keep.txt", text: "Updated", stable: true},
		{name: "context file", file: "app.txt", text: "Updated", stable: true},
		{name: "dependency file", file: "requirements.txt", text: "dependency==2.0", stable: true},
		{name: "new context file", file: "new file\twith space.txt", text: "New", stable: true},
		{name: "Containerfile", file: "Containerfile", text: "FROM scratch", stable: true},
		{name: "dockerignore", file: ".dockerignore", text: "meta.yaml\nREADME.md\n", stable: true},
		{name: "excluded smoke hook", file: "tests/smoke.sh", text: "exit 1", stable: true, preview: true},
		{name: "preview context", file: "preview/app.txt", text: "Updated", preview: true},
		{name: "preview ignored docs", file: "preview/README.md", text: "Updated"},
		{name: "preview metadata only", file: "meta.yaml", text: strings.ReplaceAll(changeConfig, "2.0.0-rc.1", "2.0.0-rc.2"), preview: true},
		{name: "metadata formatting", file: "meta.yaml", text: "# Comment\n" + changeConfig},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, _, _, releases := changeApp(t)
			if tt.file != "" {
				writeSource(t, tt.file, tt.text)
			}
			commitSource(t, a)
			builds, err := a.builds()
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range []bool{tt.stable, tt.preview} {
				changed, reason := a.trackChanged(builds[i], releases)
				if changed != want {
					t.Fatalf("%s: changed=%v, reason=%s; want %v", builds[i].Track, changed, reason, want)
				}
			}
		})
	}
}

func TestChangedBuildSettings(t *testing.T) {
	for _, field := range []string{"version", "image version", "image", "tags", "platforms", "build args", "prerelease", "context", "containerfile"} {
		t.Run(field, func(t *testing.T) {
			a, _, builds, releases := changeApp(t)
			b := builds[0]
			switch field {
			case "version":
				b.Version = "1.2.4"
			case "image version":
				b.ImageVersion = "1.2.4"
			case "image":
				b.Image = "ghcr.io/tuxcrate/other"
			case "tags":
				b.Tags = `["1.2.3","latest","stable"]`
			case "platforms":
				b.Platforms = "linux/amd64"
			case "build args":
				b.BuildArgs += "\nBASE_IMAGE=alpine:3@sha256:" + strings.Repeat("a", 64)
			case "prerelease":
				b.Prerelease = true
			case "context":
				b.Context = "preview"
			case "containerfile":
				b.Containerfile = "preview/Containerfile"
			}
			changed, reason := a.trackChanged(b, releases)
			if !changed || reason != "build or publication settings changed" {
				t.Fatalf("%v, %s", changed, reason)
			}
		})
	}
}

func TestChangedFilesDeletedOrExecutable(t *testing.T) {
	for _, operation := range []string{"delete", "executable", "symlink"} {
		t.Run(operation, func(t *testing.T) {
			a, _, builds, releases := changeApp(t)
			var err error
			switch operation {
			case "delete":
				err = os.Remove("app.txt")
			case "executable":
				err = os.Chmod("app.txt", 0755)
			case "symlink":
				err = os.Symlink("app.txt", "link.txt")
			}
			if err != nil {
				t.Fatal(err)
			}
			commitSource(t, a)
			if changed, reason := a.trackChanged(builds[0], releases); !changed {
				t.Fatalf("changed=%v: %s", changed, reason)
			}
		})
	}
}

func TestContainerfileSpecificIgnore(t *testing.T) {
	a, _, builds, _ := changeApp(t)
	writeSource(t, "Containerfile.dockerignore", "meta.yaml\napp.txt\nContainerfile\nContainerfile.dockerignore\n")
	commitSource(t, a)
	before, err := a.inputFingerprint("HEAD", builds[0])
	if err != nil {
		t.Fatal(err)
	}
	writeSource(t, "app.txt", "ignored by Containerfile-specific rules")
	commitSource(t, a)
	after, err := a.inputFingerprint("HEAD", builds[0])
	if err != nil || before != after {
		t.Fatalf("specific ignore did not override root ignore: %v", err)
	}
	for _, name := range []string{"Containerfile", "Containerfile.dockerignore"} {
		writeSource(t, name, "# Changed even though ignored\n")
		commitSource(t, a)
		after, err = a.inputFingerprint("HEAD", builds[0])
		if err != nil || before == after {
			t.Fatalf("ignored control file %s was not included: %v", name, err)
		}
		before = after
	}
}

func TestFailedReleaseStillPending(t *testing.T) {
	a, _, builds, releases := changeApp(t)
	writeSource(t, "app.txt", "unreleased build change")
	commitSource(t, a)
	if _, err := a.run("", "git", "tag", "v1.2.3-r1"); err != nil {
		t.Fatal(err)
	}
	releases = append(releases, release{TagName: "v1.2.3-r1", Draft: true, PublishedAt: "2026-02-01"})
	writeSource(t, "README.md", "subsequent docs-only commit")
	commitSource(t, a)
	if changed, reason := a.trackChanged(builds[0], releases); !changed {
		t.Fatalf("pending build was lost: %s", reason)
	}
	// Once that same source is published, an empty/docs-only commit can skip.
	releases[len(releases)-1].Draft = false
	if changed, reason := a.trackChanged(builds[0], releases); changed {
		t.Fatalf("published build not recognized: %s", reason)
	}
}

func TestNoImplicitContextExclusions(t *testing.T) {
	a, _, builds, _ := changeApp(t)
	if err := os.Remove(".dockerignore"); err != nil {
		t.Fatal(err)
	}
	commitSource(t, a)
	before, err := a.inputFingerprint("HEAD", builds[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "meta.yaml", "preview/app.txt"} {
		writeSource(t, name, "# Tracked context files are build inputs unless explicitly excluded\n")
		commitSource(t, a)
		after, err := a.inputFingerprint("HEAD", builds[0])
		if err != nil || before == after {
			t.Fatalf("implicitly excluded %s: %v", name, err)
		}
		before = after
	}
}

func TestContainerfileOutsideContext(t *testing.T) {
	a, _, builds, _ := changeApp(t)
	b := builds[1]
	b.Containerfile = "Containerfile"
	before, err := a.inputFingerprint("HEAD", b)
	if err != nil {
		t.Fatal(err)
	}
	writeSource(t, "Containerfile", "FROM scratch\n# Changed outside preview context\n")
	commitSource(t, a)
	after, err := a.inputFingerprint("HEAD", b)
	if err != nil || before == after {
		t.Fatalf("Containerfile outside context not included: %v", err)
	}
}

func TestUnsafeFingerprintInputs(t *testing.T) {
	for _, scenario := range []string{"symlink ignore", "symlink Containerfile", "symlink smoke hook", "symlink context ancestor", "missing Containerfile", "invalid ignore"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, builds, _ := changeApp(t)
			b := builds[0]
			switch scenario {
			case "symlink ignore", "symlink Containerfile", "symlink smoke hook":
				name := ".dockerignore"
				if scenario == "symlink Containerfile" {
					name = "Containerfile"
				} else if scenario == "symlink smoke hook" {
					name = "tests/smoke.sh"
				}
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("app.txt", name); err != nil {
					t.Fatal(err)
				}
			case "symlink context ancestor":
				if err := os.Symlink("preview", "context-link"); err != nil {
					t.Fatal(err)
				}
				b.Context = "context-link/subdir"
			case "missing Containerfile":
				b.Containerfile = "missing"
			case "invalid ignore":
				writeSource(t, ".dockerignore", "[\n")
			}
			commitSource(t, a)
			if _, err := a.inputFingerprint("HEAD", b); err == nil {
				t.Fatal("unsafe input comparison accepted")
			}
			if changed, reason := a.trackChanged(b, []release{{TagName: "v1.2.3-r0"}}); !changed {
				t.Fatalf("unsafe inputs skipped a build: %s", reason)
			}
		})
	}
}

func TestConservativeReleaseFallbacks(t *testing.T) {
	for _, scenario := range []string{"no release", "missing tag", "old configuration", "new track"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, builds, releases := changeApp(t)
			switch scenario {
			case "no release":
				releases = nil
			case "missing tag":
				releases[0].TagName = "v1.2.3-r99"
			case "old configuration":
				writeSource(t, "meta.yaml", "legacy: unsupported")
				commitSource(t, a)
				if _, err := a.run("", "git", "tag", "v1.2.3-r1"); err != nil {
					t.Fatal(err)
				}
				releases[0].TagName = "v1.2.3-r1"
			case "new track":
				builds[0].Track = "new"
			}
			if changed, reason := a.trackChanged(builds[0], releases); !changed {
				t.Fatalf("unsafe skip for %s: %s", scenario, reason)
			}
		})
	}
}

func TestReleasePlan(t *testing.T) {
	for _, event := range []string{"push", "schedule", "workflow_dispatch"} {
		for _, changed := range []bool{false, true} {
			t.Run(event+"/"+strconv.FormatBool(changed), func(t *testing.T) {
				a, env, _, releases := changeApp(t)
				env["GITHUB_EVENT_NAME"] = event
				if changed {
					writeSource(t, "app.txt", "changed")
					commitSource(t, a)
				}
				gitRunner := a.exec
				a.exec = func(input string, args ...string) (commandResult, error) {
					if args[0] == "gh" {
						if event != "push" {
							t.Fatal("forced builds must not need release history")
						}
						// Simulate history spanning multiple GitHub API pages.
						data, _ := json.Marshal([][]release{{releases[0]}, {releases[1]}})
						return commandResult{stdout: string(data)}, nil
					}
					return gitRunner(input, args...)
				}
				if err := a.execute([]string{"release-plan"}); err != nil {
					t.Fatal(err)
				}
				output, err := os.ReadFile(env["GITHUB_OUTPUT"])
				if err != nil {
					t.Fatal(err)
				}
				want := 2
				if event == "push" {
					want = 0
					if changed {
						want = 1
					}
				}
				matrixJSON := strings.Split(strings.Split(string(output), "matrix<<TUXCRATE_OUTPUT\n")[1], "\nTUXCRATE_OUTPUT")[0]
				var matrix buildMatrix
				if err := json.Unmarshal([]byte(matrixJSON), &matrix); err != nil || len(matrix.Include) != want {
					t.Fatalf("unexpected matrix: %s (%v); want %d tracks", output, err, want)
				}
				if !strings.Contains(string(output), "changed<<TUXCRATE_OUTPUT\n"+strconv.FormatBool(want > 0)+"\n") {
					t.Fatal(string(output))
				}
				summary, err := os.ReadFile(env["GITHUB_STEP_SUMMARY"])
				if err != nil || !strings.Contains(string(summary), "Container release decisions") {
					t.Fatalf("missing decision summary: %s (%v)", summary, err)
				}
			})
		}
	}
}

func TestReleasePlanHistoryErrors(t *testing.T) {
	for _, response := range []string{"failure", "invalid JSON"} {
		t.Run(response, func(t *testing.T) {
			a, env, _, _ := changeApp(t)
			a.exec = func(_ string, args ...string) (commandResult, error) {
				if response == "failure" {
					return commandResult{}, errors.New("network failure")
				}
				return commandResult{stdout: "not JSON"}, nil
			}
			if err := a.releasePlan(); err == nil {
				t.Fatal("history lookup failure silently skipped releases")
			}
			if _, err := os.Stat(env["GITHUB_OUTPUT"]); !os.IsNotExist(err) {
				t.Fatal("failed decision emitted workflow outputs")
			}
		})
	}
}
