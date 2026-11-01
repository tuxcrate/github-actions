package containerrelease

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// PRs run this single release rehearsal against their tested local image.
// The API fixture mirrors draft lookup semantics; no GitHub writes are possible.
func TestReleaseLifecycle(t *testing.T) {
	a, env := configApp(t, "tracks: []")
	localImage := os.Getenv("LOCAL_IMAGE")
	sourceDir := os.Getenv("GITHUB_WORKSPACE")
	if localImage != "" {
		for _, key := range []string{"APP_VERSION", "IMAGE_VERSION", "TRACK", "TAGS", "PRERELEASE", "IMAGE", "IMAGE_DIGEST", "PLATFORMS", "GITHUB_SHA", "GITHUB_REPOSITORY", "GITHUB_SERVER_URL"} {
			env[key] = os.Getenv(key)
		}
		env["GITHUB_REF_NAME"] = "main"
	}
	var draft release
	patched := false
	repository := "repos/" + env["GITHUB_REPOSITORY"]
	a.exec = func(input string, args ...string) (commandResult, error) {
		switch {
		case args[0] == "gh" && strings.Contains(args[2], "/git/ref/heads/"):
			return commandResult{stdout: env["GITHUB_SHA"]}, nil
		case args[0] == "gh" && args[2] == "--paginate":
			releases := []release{}
			if draft.ID != 0 {
				releases = append(releases, draft)
			}
			data, _ := json.Marshal([][]release{releases})
			return commandResult{stdout: string(data)}, nil
		case args[0] == "gh" && args[2] == "--method" && args[3] == "POST":
			if args[4] != repository+"/releases" || draft.ID != 0 {
				t.Fatalf("unexpected reservation: %v", args)
			}
			if err := json.Unmarshal([]byte(input), &draft); err != nil {
				t.Fatal(err)
			}
			draft.ID = 42
			data, _ := json.Marshal(draft)
			return commandResult{stdout: string(data)}, nil
		case args[0] == "gh" && strings.Contains(args[2], "/releases/tags/"):
			return commandResult{code: 1, stderr: "Not Found (HTTP 404): get-by-tag excludes drafts"}, nil
		case args[0] == "gh" && args[2] == repository+"/releases/42" && draft.ID == 42:
			data, _ := json.Marshal(draft)
			return commandResult{stdout: string(data)}, nil
		case args[0] == "gh" && args[2] == "--method" && args[3] == "PATCH":
			if args[4] != repository+"/releases/42" {
				t.Fatalf("finalized the wrong draft: %v", args)
			}
			var payload map[string]string
			if err := json.Unmarshal([]byte(input), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 1 || payload["body"] == "" {
				t.Fatalf("finalization must only update draft notes: %v", payload)
			}
			draft.Body, patched = payload["body"], true
			return commandResult{}, nil
		case args[0] == "docker" && args[1] == "buildx":
			return commandResult{code: 1, stderr: "not found"}, nil
		case args[0] == "git" && args[1] == "tag":
			return commandResult{}, nil
		case localImage != "" && args[0] == "docker" && args[1] == "create":
			args = append([]string(nil), args...)
			args[2], args[len(args)-1] = "--pull=never", localImage
			return execCommand(input, args...)
		case localImage != "" && args[0] == "docker" && (args[1] == "cp" || args[1] == "rm"):
			return execCommand(input, args...)
		case args[0] == "git" && args[1] == "log":
			if localImage != "" {
				return execCommand(input, append([]string{"git", "-C", sourceDir}, args[1:]...)...)
			}
			return commandResult{stdout: env["GITHUB_SHA"] + "\tfeat: test image"}, nil
		default:
			t.Fatalf("release rehearsal refused external command: %v", args)
			return commandResult{}, nil
		}
	}
	if localImage == "" {
		stubReleaseOS(t, &a, env)
	}
	if err := a.reserve(); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(env["GITHUB_OUTPUT"])
	if err != nil {
		t.Fatal(err)
	}
	for key, name := range map[string]string{"RELEASE_ID": "release-id", "RELEASE_TAG": "tag"} {
		_, value, ok := strings.Cut(string(output), name+"<<TUXCRATE_OUTPUT\n")
		if !ok {
			t.Fatalf("reservation did not expose %s", name)
		}
		env[key], _, _ = strings.Cut(value, "\n")
	}
	if err := a.finalize(); err != nil {
		t.Fatal(err)
	}
	if !patched || !draft.Draft || !strings.Contains(draft.Body, env["IMAGE_DIGEST"]) {
		t.Fatalf("draft finalization was incomplete: %+v", draft)
	}
	if err := a.manifest(); err != nil {
		t.Fatal(err)
	}
}
