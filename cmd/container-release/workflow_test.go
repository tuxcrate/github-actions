package main

import (
	"fmt"
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
		if input != "shared-ref" && input != "docs-repository" {
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
		case "Validate root meta.yaml and tag ownership":
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

func TestWorkflowPRAssignment(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			If          string            `yaml:"if"`
			Needs       any               `yaml:"needs"`
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	job := workflow.Jobs["assign-pull-request"]
	wantGate := "github.event_name == 'pull_request' && github.event.pull_request.user.login != 'renovate[bot]' && github.event.pull_request.head.repo.full_name == github.repository"
	if strings.TrimSpace(job.If) != wantGate {
		t.Fatal("assignment must use the PR author to exclude Renovate and skip forks and non-PR events")
	}
	if job.Needs != nil || len(job.Permissions) != 1 || job.Permissions["pull-requests"] != "write" {
		t.Fatal("assignment must run independently of builds with only PR write permission")
	}
	if len(job.Steps) != 1 || job.Steps[0].Uses != "" {
		t.Fatal("assignment must not check out or execute PR code")
	}
	step := job.Steps[0]
	if step.Env["GH_TOKEN"] != "${{ secrets.GITHUB_TOKEN }}" || step.Env["PR_NUMBER"] != "${{ github.event.pull_request.number }}" {
		t.Fatal("assignment must use GITHUB_TOKEN and the event PR number")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/bash\nprintf '%s\\n' \"$@\" > \"$GH_REQUEST\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	request := filepath.Join(dir, "request")
	cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", step.Run)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "GH_REQUEST="+request,
		"GITHUB_REPOSITORY=tuxcrate/zrepl", "PR_NUMBER=12")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("assignment script: %v: %s", err, out)
	}
	got, err := os.ReadFile(request)
	if err != nil {
		t.Fatal(err)
	}
	want := "api\n--method\nPOST\nrepos/tuxcrate/zrepl/issues/12/assignees\n-f\nassignees[]=Skaronator\n--silent\n"
	if string(got) != want {
		t.Fatalf("assignment must add Skaronator without replacing existing assignees: got %q, want %q", got, want)
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
	var prepared, loggedIn, built, loaded, tooling, validated, commented bool
	for _, s := range pr.Steps {
		switch {
		case s.ID == "preview":
			prepared = true
			if s.Env["IMAGE_REPOSITORY"] != "${{ github.repository }}" || s.Env["PR_NUMBER"] != "${{ github.event.pull_request.number }}" || s.Env["TRACK"] != "${{ matrix.track }}" || s.Env["COMMIT_SHA"] != "${{ github.sha }}" || s.Env["PLATFORMS"] != "${{ matrix.platforms }}" {
				t.Fatal("preview tags must use caller repository, PR number, track, and tested merge commit")
			}
			for _, track := range []string{"stable", "preview"} {
				t.Run(track, func(t *testing.T) {
					output := filepath.Join(t.TempDir(), "output")
					sha := strings.Repeat("a", 40)
					cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", s.Run)
					cmd.Env = append(os.Environ(), "IMAGE_REPOSITORY=Tuxcrate/Zrepl", "PR_NUMBER=12", "TRACK="+track, "COMMIT_SHA="+sha, "GITHUB_OUTPUT="+output, "PLATFORMS=linux/arm64,linux/amd64")
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("prepare tags: %v: %s", err, out)
					}
					got, err := os.ReadFile(output)
					if err != nil {
						t.Fatal(err)
					}
					image := "ghcr.io/tuxcrate/zrepl-pr"
					tag := "pr-12-" + track
					want := "image=" + image + "\nplatform=linux/arm64\ntags<<PR_IMAGE_TAGS\n" + image + ":" + tag + "\n" + image + ":" + tag + "-" + sha + "\nPR_IMAGE_TAGS\n"
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
		case s.Name == "Load first platform for OS metadata validation":
			loaded = true
			if !built || s.If != "" || !strings.HasPrefix(s.Uses, "docker/build-push-action@") || s.With["load"] != "true" || s.With["platforms"] != "${{ steps.preview.outputs.platform }}" || s.With["tags"] != "tuxcrate-pr-os-check:${{ matrix.track }}" {
				t.Fatal("all PRs, including forks, must load the first platform for validation")
			}
			if s.With["context"] != "${{ matrix.context }}" || s.With["file"] != "${{ matrix.containerfile }}" || !strings.Contains(s.With["build-args"], "${{ matrix.build-args }}") || s.With["push"] != "" {
				t.Fatal("validation must build the same source and arguments without pushing")
			}
		case s.Name == "Check out shared build tooling":
			tooling = true
			if built || s.If != "" || s.With["repository"] != "tuxcrate/github-actions" || s.With["ref"] != "${{ inputs.shared-ref }}" || s.With["persist-credentials"] != "false" {
				t.Fatal("trusted tooling must be checked out before the image build without credentials")
			}
		case s.Name == "Generate and validate synthetic release metadata":
			validated = true
			if !tooling || s.If != "" || !strings.Contains(s.Run, `"$RUNNER_TEMP/container-release" check-manifest`) || !strings.Contains(s.Run, `cd "$RUNNER_TEMP/pr-release-metadata"`) {
				t.Fatal("all PRs must generate and validate a temporary release.json")
			}
			for key, want := range map[string]string{
				"APP_VERSION":   "${{ matrix.version }}",
				"TRACK":         "${{ matrix.track }}",
				"IMAGE_VERSION": "${{ matrix.image-version }}",
				"TAGS":          "${{ matrix.tags }}",
				"IMAGE":         "${{ matrix.image }}",
				"IMAGE_DIGEST":  "${{ steps.local-image.outputs.imageid }}",
				"PLATFORMS":     "${{ matrix.platforms }}",
				"LOCAL_IMAGE":   "tuxcrate-pr-os-check:${{ matrix.track }}",
			} {
				if s.Env[key] != want {
					t.Fatalf("validation environment %s: got %q, want %q", key, s.Env[key], want)
				}
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
			if !built || !validated || s.If != "env.PUBLISH_PR == 'true'" {
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
	if !prepared || !loggedIn || !built || !loaded || !tooling || !validated || !commented {
		t.Fatal("missing PR tag preparation, conditional login, build, OS validation, or comment")
	}
	if !strings.Contains(workflow.Jobs["release"].If, "github.ref == format('refs/heads/{0}', github.event.repository.default_branch)") || strings.Contains(workflow.Jobs["release"].If, "pull_request") {
		t.Fatal("PRs must not enter the production release job")
	}
}

func TestWorkflowUpstreamMetadata(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				ID   string            `yaml:"id"`
				If   string            `yaml:"if"`
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				Env  map[string]string `yaml:"env"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, job := range []string{"pull-request", "release"} {
		prefetched, builds := false, 0
		for _, step := range workflow.Jobs[job].Steps {
			if step.ID == "upstream" {
				prefetched = true
				if step.If != "" || step.Env["GH_TOKEN"] != "${{ secrets.GITHUB_TOKEN }}" || step.Env["TRACK"] != "${{ matrix.track }}" || !strings.Contains(step.Run, "upstream-release") {
					t.Fatalf("%s must prefetch metadata using the job token, including fork PRs", job)
				}
			}
			if strings.HasPrefix(step.Uses, "docker/build-push-action@") {
				builds++
				if !prefetched || !strings.Contains(step.With["build-args"], "${{ steps.upstream.outputs.build-args }}") || step.With["secret-files"] != "" {
					t.Fatalf("%s build must receive resolved public build arguments, not JSON secrets", job)
				}
				for key, value := range step.With {
					if strings.Contains(value, "secrets.") || (key == "secrets" && value != "") {
						t.Fatalf("%s must not pass tokens to the container build", job)
					}
				}
			}
		}
		if !prefetched || builds == 0 {
			t.Fatalf("%s missing prefetch or builds", job)
		}
	}
}

func testPRImageComment(t *testing.T, script string) {
	t.Helper()
	for _, track := range []string{"stable", "preview"} {
		for _, scenario := range []string{"create", "update", "update-later-page", "multiple-matches", "lookup-fails", "invalid-json", "missing-manifest"} {
			t.Run("comment/"+track+"/"+scenario, func(t *testing.T) {
				dir := t.TempDir()
				// Simulate GitHub without making requests or requiring gh in Go tests.
				stub := `#!/bin/bash
set -euo pipefail
if [[ "$2" == '--paginate' ]]; then
  printf '%s\n' "$@" > "$GH_LOOKUP"
  for arg in "$@"; do
    if [[ "$arg" == '--jq' || "$arg" == '--template' ]]; then exit 1; fi
  done
  if [[ "$FAIL_LOOKUP" == true ]]; then exit 1; fi
  printf '%s' "$COMMENT_PAGES"
else
  printf '%s\n' "$@" > "$GH_WRITE"
fi
`
				if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(stub), 0755); err != nil {
					t.Fatal(err)
				}
				marker := "<!-- tuxcrate-pr-image:" + track + " -->"
				comment := func(id int, login, body string) string {
					return fmt.Sprintf(`{"id":%d,"user":{"login":%q},"body":%q}`, id, login, body)
				}
				// Neither another user's matching marker nor another track's bot
				// comment should be updated. Exercise the real jq filter.
				unrelated := comment(1, "contributor", marker) + "," + comment(2, "github-actions[bot]", "<!-- tuxcrate-pr-image:other -->")
				match := comment(42, "github-actions[bot]", marker+"\nPrevious preview")
				pages, fail := "[["+unrelated+"],[]]", "false"
				switch scenario {
				case "update":
					pages = "[[" + unrelated + "," + match + "]]"
				case "update-later-page":
					pages = "[[" + unrelated + "],[" + match + "]]"
				case "multiple-matches":
					pages = "[[" + match + "],[" + comment(43, "github-actions[bot]", marker) + "]]"
				case "invalid-json":
					pages = "invalid JSON"
				}
				if scenario == "lookup-fails" {
					fail = "true"
				}
				lookup, write := filepath.Join(dir, "lookup"), filepath.Join(dir, "write")
				sha := strings.Repeat("a", 40)
				digest := "sha256:" + strings.Repeat("b", 64)
				image := "ghcr.io/tuxcrate/zrepl-pr"
				runURL := "https://github.com/tuxcrate/zrepl/actions/runs/123"
				manifest := fmt.Sprintf("{\n  \"track\": %q,\n  \"os_flavor\": \"debian\",\n  \"os_version\": \"13.7\"\n}", track)
				if scenario != "missing-manifest" {
					metadataDir := filepath.Join(dir, "pr-release-metadata")
					if err := os.Mkdir(metadataDir, 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(metadataDir, "release.json"), []byte(manifest+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				cmd := exec.Command("bash", "-c", script)
				cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"),
					"RUNNER_TEMP="+dir,
					"GH_LOOKUP="+lookup, "GH_WRITE="+write, "COMMENT_PAGES="+pages, "FAIL_LOOKUP="+fail,
					"GITHUB_REPOSITORY=tuxcrate/zrepl", "PR_NUMBER=12", "TRACK="+track,
					"IMAGE="+image, "IMAGE_DIGEST="+digest, "COMMIT_SHA="+sha,
					"PLATFORMS=linux/amd64,linux/arm64", "RUN_URL="+runURL)
				out, err := cmd.CombinedOutput()
				if scenario == "lookup-fails" || scenario == "invalid-json" || scenario == "missing-manifest" {
					if err == nil {
						t.Fatal("ignored failed comment lookup or missing release manifest")
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
				if want := "api\n--paginate\n--slurp\nrepos/tuxcrate/zrepl/issues/12/comments\n"; string(query) != want {
					t.Fatalf("comment lookup: got %q, want %q", query, want)
				}
				request, err := os.ReadFile(write)
				if err != nil {
					t.Fatal(err)
				}
				endpoint := "POST\nrepos/tuxcrate/zrepl/issues/12/comments"
				if scenario == "update" || scenario == "update-later-page" || scenario == "multiple-matches" {
					endpoint = "PATCH\nrepos/tuxcrate/zrepl/issues/comments/42"
				}
				tag := "pr-12-" + track
				for _, want := range []string{endpoint, "body=" + marker, "docker pull " + image + ":" + tag + "\n", "docker pull " + image + ":" + tag + "-" + sha, "docker pull " + image + "@" + digest, runURL, "linux/amd64,linux/arm64"} {
					if !strings.Contains(string(request), want) {
						t.Fatalf("comment request missing %q: %s", want, request)
					}
				}
				spoiler := "<details>\n<summary>Synthetic release.json (validation only)</summary>"
				if !strings.Contains(string(request), spoiler) || !strings.Contains(string(request), "```json\n"+manifest+"\n```\n\n</details>") || !strings.Contains(string(request), "Revision `r0` and the local image ID are placeholders") {
					t.Fatalf("comment must include the generated manifest in a collapsed, clearly labeled section: %s", request)
				}
			})
		}
	}
}
