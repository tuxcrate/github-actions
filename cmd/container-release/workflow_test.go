package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestWorkflowRequiresRootMetadata(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	type step struct {
		Name             string            `yaml:"name"`
		WorkingDirectory string            `yaml:"working-directory"`
		Run              string            `yaml:"run"`
		Env              map[string]string `yaml:"env"`
	}
	var workflow struct {
		On struct {
			Call struct {
				Inputs map[string]any `yaml:"inputs"`
			} `yaml:"workflow_call"`
		} `yaml:"on"`
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for input := range workflow.On.Call.Inputs {
		if input != "shared-ref" && input != "website-repository" {
			t.Fatalf("workflow accepts image configuration input %s", input)
		}
	}
	var required, planned bool
	for _, s := range workflow.Jobs["plan"].Steps {
		switch s.Name {
		case "Require root meta.yaml":
			required = true
			if s.WorkingDirectory != "source" {
				t.Fatal("metadata preflight must run in the caller repository root")
			}
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "nested"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "nested", "meta.yaml"), []byte("tracks: []\n"), 0644); err != nil {
				t.Fatal(err)
			}
			check := func() error {
				cmd := exec.Command("bash", "-c", s.Run)
				cmd.Dir = dir
				return cmd.Run()
			}
			if err := check(); err == nil {
				t.Fatal("accepted missing root metadata with only a nested meta.yaml")
			}
			if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte("tracks: []\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := check(); err != nil {
				t.Fatalf("rejected root metadata: %v", err)
			}
		case "Validate root meta.yaml and alias ownership":
			planned = true
			if !required || s.WorkingDirectory != "source" || len(s.Env) != 0 {
				t.Fatal("planner must validate the fixed root metadata after the preflight")
			}
		}
	}
	if !required || !planned {
		t.Fatal("workflow must require and validate root metadata")
	}
}

func TestWorkflowPRImages(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	type step struct {
		Name string            `yaml:"name"`
		ID   string            `yaml:"id"`
		If   string            `yaml:"if"`
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
		With map[string]string `yaml:"with"`
	}
	var workflow struct {
		Jobs map[string]struct {
			If          string            `yaml:"if"`
			Permissions map[string]string `yaml:"permissions"`
			Env         map[string]string `yaml:"env"`
			Steps       []step            `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	pr := workflow.Jobs["pull-request"]
	if pr.If != "github.event_name == 'pull_request'" || pr.Permissions["contents"] != "read" || pr.Permissions["packages"] != "write" || pr.Permissions["pull-requests"] != "write" {
		t.Fatal("PR builds must use pull_request with read-only sources, package publishing, and comment permissions")
	}
	gate := "${{ github.event.pull_request.head.repo.full_name == github.repository }}"
	if pr.Env["PUBLISH_PR"] != gate {
		t.Fatal("publishing must exclude fork PRs")
	}
	var prepared, loggedIn, built, commented bool
	for _, s := range pr.Steps {
		switch {
		case s.ID == "preview":
			prepared = true
			if s.Env["IMAGE_REPOSITORY"] != "${{ github.repository }}" || s.Env["PR_NUMBER"] != "${{ github.event.pull_request.number }}" || s.Env["TRACK"] != "${{ matrix.track }}" || s.Env["COMMIT_SHA"] != "${{ github.sha }}" {
				t.Fatal("preview tags must use caller repository, PR number, track, and tested merge commit")
			}
			for _, track := range []string{"stable", "preview"} {
				t.Run(track, func(t *testing.T) {
					output := filepath.Join(t.TempDir(), "output")
					sha := strings.Repeat("a", 40)
					cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", s.Run)
					cmd.Env = append(os.Environ(), "IMAGE_REPOSITORY=Tuxcrate/Zrepl", "PR_NUMBER=12", "TRACK="+track, "COMMIT_SHA="+sha, "GITHUB_OUTPUT="+output)
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("prepare tags: %v: %s", err, out)
					}
					got, err := os.ReadFile(output)
					if err != nil {
						t.Fatal(err)
					}
					image := "ghcr.io/tuxcrate/zrepl-pr"
					tag := "pr-12-" + track
					want := "image=" + image + "\ntags<<PR_IMAGE_TAGS\n" + image + ":" + tag + "\n" + image + ":" + tag + "-" + sha + "\nPR_IMAGE_TAGS\n"
					if string(got) != want {
						t.Fatalf("unexpected preview tags: %s", got)
					}
				})
			}
		case strings.HasPrefix(s.Uses, "docker/login-action@"):
			loggedIn = true
			if !prepared || s.If != "env.PUBLISH_PR == 'true'" || s.With["password"] != "${{ secrets.GITHUB_TOKEN }}" {
				t.Fatal("registry login must be gated and use only GITHUB_TOKEN")
			}
		case strings.HasPrefix(s.Uses, "docker/build-push-action@"):
			built = true
			if !loggedIn || s.ID != "build" || s.With["push"] != "${{ env.PUBLISH_PR == 'true' }}" || s.With["tags"] != "${{ steps.preview.outputs.tags }}" {
				t.Fatal("only eligible PRs may push, and only to preview tags")
			}
			if s.With["no-cache"] != "true" || s.With["platforms"] != "${{ matrix.platforms }}" || s.With["provenance"] != "mode=max" || s.With["sbom"] != "true" {
				t.Fatal("PR builds must keep uncached multiarch validation and attestations")
			}
		case s.Name == "Comment PR image pull instructions":
			commented = true
			if !built || s.If != "env.PUBLISH_PR == 'true'" {
				t.Fatal("comments must follow successful eligible PR image builds")
			}
			for key, want := range map[string]string{
				"GH_TOKEN":     "${{ secrets.GITHUB_TOKEN }}",
				"PR_NUMBER":    "${{ github.event.pull_request.number }}",
				"TRACK":        "${{ matrix.track }}",
				"IMAGE":        "${{ steps.preview.outputs.image }}",
				"IMAGE_DIGEST": "${{ steps.build.outputs.digest }}",
				"COMMIT_SHA":   "${{ github.sha }}",
				"PLATFORMS":    "${{ matrix.platforms }}",
				"RUN_URL":      "${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}",
			} {
				if s.Env[key] != want {
					t.Fatalf("comment environment %s: got %q, want %q", key, s.Env[key], want)
				}
			}
			testPRImageComment(t, s.Run)
		}
	}
	if !prepared || !loggedIn || !built || !commented {
		t.Fatal("missing PR tag preparation, conditional login, build, or comment")
	}
	if !strings.Contains(workflow.Jobs["release"].If, "github.ref == format('refs/heads/{0}', github.event.repository.default_branch)") || strings.Contains(workflow.Jobs["release"].If, "pull_request") {
		t.Fatal("PRs must not enter the production release job")
	}
}

func testPRImageComment(t *testing.T, script string) {
	t.Helper()
	for _, track := range []string{"stable", "preview"} {
		for _, scenario := range []string{"create", "update", "lookup-fails"} {
			t.Run("comment/"+track+"/"+scenario, func(t *testing.T) {
				dir := t.TempDir()
				// Simulate GitHub without making requests or requiring gh in Go tests.
				stub := `#!/bin/bash
set -euo pipefail
if [[ "$2" == '--paginate' ]]; then
  printf '%s\n' "$@" > "$GH_LOOKUP"
  if [[ "$FAIL_LOOKUP" == true ]]; then exit 1; fi
  printf '%s' "$COMMENT_ID"
else
  printf '%s\n' "$@" > "$GH_WRITE"
fi
`
				if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(stub), 0755); err != nil {
					t.Fatal(err)
				}
				id, fail := "", "false"
				if scenario == "update" {
					id = "42"
				}
				if scenario == "lookup-fails" {
					fail = "true"
				}
				lookup, write := filepath.Join(dir, "lookup"), filepath.Join(dir, "write")
				sha := strings.Repeat("a", 40)
				digest := "sha256:" + strings.Repeat("b", 64)
				image := "ghcr.io/tuxcrate/zrepl-pr"
				runURL := "https://github.com/tuxcrate/zrepl/actions/runs/123"
				cmd := exec.Command("bash", "-c", script)
				cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"),
					"GH_LOOKUP="+lookup, "GH_WRITE="+write, "COMMENT_ID="+id, "FAIL_LOOKUP="+fail,
					"GITHUB_REPOSITORY=tuxcrate/zrepl", "PR_NUMBER=12", "TRACK="+track,
					"IMAGE="+image, "IMAGE_DIGEST="+digest, "COMMIT_SHA="+sha,
					"PLATFORMS=linux/amd64,linux/arm64", "RUN_URL="+runURL)
				out, err := cmd.CombinedOutput()
				if scenario == "lookup-fails" {
					if err == nil {
						t.Fatal("ignored failed comment lookup")
					}
					if _, err := os.Stat(write); !os.IsNotExist(err) {
						t.Fatal("must not post a duplicate comment when lookup fails")
					}
					return
				}
				if err != nil {
					t.Fatalf("comment script: %v: %s", err, out)
				}
				query, err := os.ReadFile(lookup)
				if err != nil {
					t.Fatal(err)
				}
				marker := "<!-- tuxcrate-pr-image:" + track + " -->"
				for _, want := range []string{"--paginate\n--slurp", "repos/tuxcrate/zrepl/issues/12/comments", `select(.user.login == "github-actions[bot]"`, `startswith("` + marker + `")`} {
					if !strings.Contains(string(query), want) {
						t.Fatalf("comment lookup missing %q: %s", want, query)
					}
				}
				request, err := os.ReadFile(write)
				if err != nil {
					t.Fatal(err)
				}
				endpoint := "POST\nrepos/tuxcrate/zrepl/issues/12/comments"
				if scenario == "update" {
					endpoint = "PATCH\nrepos/tuxcrate/zrepl/issues/comments/42"
				}
				tag := "pr-12-" + track
				for _, want := range []string{endpoint, "body=" + marker, "docker pull " + image + ":" + tag + "\n", "docker pull " + image + ":" + tag + "-" + sha, "docker pull " + image + "@" + digest, runURL, "linux/amd64,linux/arm64"} {
					if !strings.Contains(string(request), want) {
						t.Fatalf("comment request missing %q: %s", want, request)
					}
				}
			})
		}
	}
}
