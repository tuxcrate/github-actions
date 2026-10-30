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

func TestWorkflowReleaseChangeDetection(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			If       string            `yaml:"if"`
			Needs    any               `yaml:"needs"`
			Outputs  map[string]string `yaml:"outputs"`
			Strategy struct {
				Matrix string `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []struct {
				ID               string            `yaml:"id"`
				Uses             string            `yaml:"uses"`
				Run              string            `yaml:"run"`
				WorkingDirectory string            `yaml:"working-directory"`
				With             map[string]string `yaml:"with"`
				Env              map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	plan, release := workflow.Jobs["release-plan"], workflow.Jobs["release"]
	if plan.Needs != "plan" || release.Needs != "release-plan" {
		t.Fatal("production builds must wait for metadata validation and release change detection")
	}
	for _, gate := range []string{
		"github.ref == format('refs/heads/{0}', github.event.repository.default_branch)",
		"github.event_name == 'push'", "github.event_name == 'schedule'", "github.event_name == 'workflow_dispatch'",
	} {
		if !strings.Contains(plan.If, gate) || !strings.Contains(release.If, gate) {
			t.Fatalf("release planning and publishing must retain gate %s", gate)
		}
	}
	if !strings.Contains(release.If, "needs.release-plan.outputs.changed == 'true'") ||
		release.Strategy.Matrix != "${{ fromJSON(needs.release-plan.outputs.matrix) }}" {
		t.Fatal("an empty release plan must skip the job before expanding its matrix")
	}
	if plan.Outputs["matrix"] != "${{ steps.plan.outputs.matrix }}" || plan.Outputs["changed"] != "${{ steps.plan.outputs.changed }}" {
		t.Fatal("release plan must expose its filtered matrix and nonempty flag")
	}
	var checkedOut, planned bool
	for _, step := range plan.Steps {
		if step.With["path"] == "source" {
			checkedOut = true
			if step.With["fetch-depth"] != "0" || step.With["persist-credentials"] != "false" {
				t.Fatal("change detection needs complete caller history without persisted credentials")
			}
		}
		if step.ID == "plan" {
			planned = true
			if !checkedOut || step.WorkingDirectory != "source" || step.Env["GH_TOKEN"] != "${{ secrets.GITHUB_TOKEN }}" || step.Run != `"$RUNNER_TEMP/container-release" release-plan` {
				t.Fatal("release planning must use trusted tooling, caller sources, and the caller token")
			}
		}
	}
	if !planned {
		t.Fatal("missing release planning command")
	}
	pr := workflow.Jobs["pull-request"]
	if pr.Needs != "plan" || pr.Strategy.Matrix != "${{ fromJSON(needs.plan.outputs.matrix) }}" {
		t.Fatal("PRs must keep the unfiltered metadata matrix, not release change detection")
	}
	if notify := workflow.Jobs["notify-docs"]; notify.Needs != "release" || !strings.Contains(notify.If, "needs.release.result == 'success'") {
		t.Fatal("no-op pushes must not notify docs when the release job is skipped")
	}
}

func TestWorkflowDocsNotification(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On struct {
			Call struct {
				Inputs map[string]struct {
					Default string `yaml:"default"`
				} `yaml:"inputs"`
				Secrets map[string]struct {
					Required bool `yaml:"required"`
				} `yaml:"secrets"`
			} `yaml:"workflow_call"`
		} `yaml:"on"`
		Jobs map[string]struct {
			If          string            `yaml:"if"`
			Needs       any               `yaml:"needs"`
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				ContinueOnError bool              `yaml:"continue-on-error"`
				Env             map[string]string `yaml:"env"`
				Run             string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	if workflow.On.Call.Inputs["shared-ref"].Default != "main" || workflow.On.Call.Inputs["docs-repository"].Default != "tuxcrate/docs" {
		t.Fatal("callers using main must not need explicit shared-ref or docs-repository inputs")
	}
	for _, name := range []string{"DOCS_TOKEN", "docs-token"} {
		secret, ok := workflow.On.Call.Secrets[name]
		if !ok || secret.Required {
			t.Fatalf("%s must remain optional for inherited and legacy callers", name)
		}
	}
	job := workflow.Jobs["notify-docs"]
	if job.Needs != "release" || job.If != "needs.release.result == 'success' && inputs.docs-repository != ''" || len(job.Permissions) != 0 {
		t.Fatal("docs notifications must run only after successful releases, allow opting out, and need no GITHUB_TOKEN permissions")
	}
	if len(job.Steps) != 1 || !job.Steps[0].ContinueOnError {
		t.Fatal("notification failure must not fail a completed release")
	}
	step := job.Steps[0]
	if step.Env["GH_TOKEN"] != "${{ secrets.docs-token || secrets.DOCS_TOKEN }}" || step.Env["DOCS_REPOSITORY"] != "${{ inputs.docs-repository }}" {
		t.Fatal("notification must support the explicit legacy token and inherited DOCS_TOKEN")
	}
	for _, token := range []string{"", "test-token"} {
		t.Run(fmt.Sprintf("token-present=%t", token != ""), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/bash\nprintf '%s\\n' \"$@\" > \"$GH_REQUEST\"\n"), 0755); err != nil {
				t.Fatal(err)
			}
			request := filepath.Join(dir, "request")
			cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", step.Run)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "GH_REQUEST="+request,
				"GH_TOKEN="+token, "DOCS_REPOSITORY=tuxcrate/docs")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("notification failed: %v\n%s", err, output)
			}
			args, err := os.ReadFile(request)
			if token == "" {
				if !os.IsNotExist(err) || !strings.Contains(string(output), "::warning::Docs refresh skipped:") {
					t.Fatal("missing token must warn without sending a dispatch")
				}
			} else if err != nil || string(args) != "api\n--method\nPOST\nrepos/tuxcrate/docs/dispatches\n-f\nevent_type=image-published\n" {
				t.Fatalf("unexpected docs dispatch: %q, %v", args, err)
			}
		})
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

func TestWorkflowPRAggregateCheck(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	// Other jobs use scalar needs; decode only the aggregate job below.
	var document struct {
		Jobs map[string]yaml.Node `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var job struct {
		Name        string            `yaml:"name"`
		If          string            `yaml:"if"`
		Needs       []string          `yaml:"needs"`
		Permissions map[string]string `yaml:"permissions"`
		Strategy    any               `yaml:"strategy"`
		Steps       []struct {
			If   string            `yaml:"if"`
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	}
	node := document.Jobs["pull-request-check"]
	if err := node.Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.Name != "" || job.Strategy != nil {
		t.Fatal("aggregate must retain its fixed job name without a matrix")
	}
	if strings.Join(job.Needs, ",") != "plan,pull-request" || job.If != "always() && github.event_name == 'pull_request'" {
		t.Fatal("aggregate must run on every PR even when planning or matrix builds fail or are skipped")
	}
	if job.Permissions == nil || len(job.Permissions) != 0 || len(job.Steps) != 1 {
		t.Fatal("aggregate must use no token permissions and a single validation step")
	}
	step := job.Steps[0]
	if step.If != "" || step.Uses != "" || step.Env["PLAN_RESULT"] != "${{ needs.plan.result }}" || step.Env["BUILD_RESULT"] != "${{ needs.pull-request.result }}" {
		t.Fatal("aggregate must check both dependency results without checking out PR code")
	}
	for _, plan := range []string{"success", "failure", "cancelled", "skipped", ""} {
		for _, build := range []string{"success", "failure", "cancelled", "skipped", ""} {
			t.Run("plan="+plan+"/build="+build, func(t *testing.T) {
				cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", step.Run)
				cmd.Env = append(os.Environ(), "PLAN_RESULT="+plan, "BUILD_RESULT="+build)
				out, err := cmd.CombinedOutput()
				wantSuccess := plan == "success" && build == "success"
				if (err == nil) != wantSuccess {
					t.Fatalf("aggregate success = %v, want %v: %s", err == nil, wantSuccess, out)
				}
			})
		}
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
	var prepared, loggedIn, built, tested, tooling, validated, uploaded, commented bool
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
			if !prepared || !tested || !validated || s.If != "env.PUBLISH_PR == 'true'" || s.With["password"] != "${{ secrets.GITHUB_TOKEN }}" {
				t.Fatal("registry login must follow tests and metadata validation, be gated, and use only GITHUB_TOKEN")
			}
		case s.ID == "tested":
			tested = true
			if !built || !tooling || loggedIn || s.If != "" || !strings.Contains(s.Run, `container-artifact" test`) || s.Env["PLATFORMS"] != "${{ matrix.platforms }}" || s.Env["APP_VERSION"] != "${{ matrix.version }}" {
				t.Fatal("all PRs, including forks, must test every built platform before registry login")
			}
		case s.Name == "Check out shared test and validation tooling":
			tooling = true
			if s.If != "" || s.With["repository"] != "tuxcrate/github-actions" || s.With["ref"] != "${{ inputs.shared-ref }}" || s.With["persist-credentials"] != "false" {
				t.Fatal("test and validation tooling must be checked out without credentials")
			}
		case s.Name == "Generate and validate synthetic release metadata":
			validated = true
			if !tooling || !tested || s.If != "" || !strings.Contains(s.Run, `"$RUNNER_TEMP/container-release" check-manifest`) || !strings.Contains(s.Run, `cd "$RUNNER_TEMP/pr-release-metadata"`) {
				t.Fatal("all PRs must generate and validate a temporary release.json")
			}
			for key, want := range map[string]string{
				"APP_VERSION":   "${{ matrix.version }}",
				"TRACK":         "${{ matrix.track }}",
				"IMAGE_VERSION": "${{ matrix.image-version }}",
				"TAGS":          "${{ matrix.tags }}",
				"IMAGE":         "${{ matrix.image }}",
				"IMAGE_DIGEST":  "${{ steps.tested.outputs.digest }}",
				"PLATFORMS":     "${{ matrix.platforms }}",
				"LOCAL_IMAGE":   "${{ steps.tested.outputs.image }}",
			} {
				if s.Env[key] != want {
					t.Fatalf("validation environment %s: got %q, want %q", key, s.Env[key], want)
				}
			}
		case strings.HasPrefix(s.Uses, "docker/build-push-action@"):
			built = true
			if loggedIn || s.ID != "build" || s.With["push"] != "" || s.With["tags"] != "" || !strings.HasPrefix(s.With["outputs"], "type=oci,") {
				t.Fatal("PR builds must export a local OCI archive without registry login or upload")
			}
			if s.With["no-cache"] != "true" || s.With["platforms"] != "${{ matrix.platforms }}" || s.With["provenance"] != "mode=max" || s.With["sbom"] != "true" {
				t.Fatal("PR builds must keep uncached multiarch validation and attestations")
			}
		case s.Name == "Upload the tested artifact and publish PR tags":
			uploaded = true
			if !loggedIn || !validated || !tested || s.If != "env.PUBLISH_PR == 'true'" || s.Env["IMAGE"] != "${{ steps.preview.outputs.image }}" || s.Env["IMAGE_TAGS"] != "${{ steps.preview.outputs.tags }}" || s.Env["IMAGE_DIGEST"] != "${{ steps.tested.outputs.digest }}" {
				t.Fatal("only tested, validated same-repository artifacts may publish preview tags")
			}
			if !strings.Contains(s.Run, `container-artifact" publish`) || !strings.Contains(s.Run, `docker buildx imagetools create --tag "$tag" "$IMAGE@$IMAGE_DIGEST"`) {
				t.Fatal("preview tags must refer to the unchanged tested archive")
			}
		case s.Name == "Comment PR image pull instructions":
			commented = true
			if !built || !validated || !uploaded || s.If != "env.PUBLISH_PR == 'true'" {
				t.Fatal("comments must follow successful eligible PR image builds")
			}
			for key, want := range map[string]string{
				"GH_TOKEN":     "${{ secrets.GITHUB_TOKEN }}",
				"PR_NUMBER":    "${{ github.event.pull_request.number }}",
				"TRACK":        "${{ matrix.track }}",
				"IMAGE":        "${{ steps.preview.outputs.image }}",
				"IMAGE_DIGEST": "${{ steps.tested.outputs.digest }}",
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
	if !prepared || !loggedIn || !built || !tested || !tooling || !validated || !uploaded || !commented {
		t.Fatal("missing PR tag preparation, build, tests, validation, conditional login, upload, or comment")
	}
	if !strings.Contains(workflow.Jobs["release"].If, "github.ref == format('refs/heads/{0}', github.event.repository.default_branch)") || strings.Contains(workflow.Jobs["release"].If, "pull_request") {
		t.Fatal("PRs must not enter the production release job")
	}
}

func TestWorkflowGitHubBuildSecret(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, job := range []string{"pull-request", "release"} {
		builds := 0
		want := "github_token=${{ secrets.GITHUB_TOKEN }}"
		if job == "pull-request" {
			want = "github_token=${{ github.event.pull_request.head.repo.full_name == github.repository && secrets.GITHUB_TOKEN || '' }}"
		}
		for _, step := range workflow.Jobs[job].Steps {
			if !strings.HasPrefix(step.Uses, "docker/build-push-action@") {
				continue
			}
			builds++
			if strings.TrimSpace(step.With["secrets"]) != want {
				t.Fatalf("%s must use a BuildKit secret and exclude fork PRs", job)
			}
			if strings.Contains(step.With["build-args"], "secrets.") {
				t.Fatalf("%s must not pass credentials as build arguments", job)
			}
		}
		if builds == 0 {
			t.Fatalf("missing %s builds", job)
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
				if !strings.Contains(string(request), spoiler) || !strings.Contains(string(request), "```json\n"+manifest+"\n```\n\n</details>") || !strings.Contains(string(request), "Revision `r0` is a placeholder") {
					t.Fatalf("comment must include the generated manifest in a collapsed, clearly labeled section: %s", request)
				}
			})
		}
	}
}
