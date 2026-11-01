package containerrelease

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestFinalizeReservedDraft(t *testing.T) {
	for _, scenario := range []string{"success", "published", "wrong tag", "wrong commit", "wrong track", "wrong channel", "missing ID", "wrong ID", "invalid JSON", "lookup failure", "occupied tag", "registry failure", "history failure", "OS failure", "PATCH failure", "stale commit", "invalid digest", "wrong image version"} {
		t.Run(scenario, func(t *testing.T) {
			a, env := testApp(t)
			env["RELEASE_TAG"] = "v0.7.0-r9"
			env["RELEASE_ID"] = "42"
			draft := release{ID: 42, TagName: env["RELEASE_TAG"], TargetCommitish: env["GITHUB_SHA"], Draft: true, Body: "Build pending.\n<!-- tuxcrate-track:stable -->\n"}
			switch scenario {
			case "published":
				draft.Draft = false
			case "wrong tag":
				draft.TagName = "v0.7.0-r8"
			case "wrong commit":
				draft.TargetCommitish = strings.Repeat("c", 40)
			case "wrong track":
				draft.Body = "<!-- tuxcrate-track:preview -->"
			case "wrong channel":
				draft.Prerelease = true
			case "missing ID":
				draft.ID = 0
			case "wrong ID":
				draft.ID = 43
			case "invalid digest":
				env["IMAGE_DIGEST"] = "invalid"
			case "wrong image version":
				env["IMAGE_VERSION"] = "0.8.0"
			}
			patched := false
			a.exec = func(input string, args ...string) (commandResult, error) {
				switch {
				case args[0] == "gh" && strings.Contains(strings.Join(args, " "), "/git/ref/heads/"):
					head := env["GITHUB_SHA"]
					if scenario == "stale commit" {
						head = "old"
					}
					return commandResult{stdout: head}, nil
				case args[0] == "gh" && args[2] == "repos/tuxcrate/zrepl/releases/42":
					if scenario == "lookup failure" {
						return commandResult{}, errors.New("lookup failed")
					}
					if scenario == "invalid JSON" {
						return commandResult{stdout: "invalid"}, nil
					}
					data, _ := json.Marshal(draft)
					return commandResult{stdout: string(data)}, nil
				case args[0] == "docker":
					if !reflect.DeepEqual(args, []string{"docker", "buildx", "imagetools", "inspect", "ghcr.io/tuxcrate/zrepl:0.7.0-r9"}) {
						t.Fatal(args)
					}
					if scenario == "occupied tag" {
						return commandResult{}, nil
					}
					if scenario == "registry failure" {
						return commandResult{code: 1, stderr: "unauthorized"}, nil
					}
					return commandResult{code: 1, stderr: "not found"}, nil
				case args[0] == "gh" && args[2] == "--paginate":
					if scenario == "history failure" {
						return commandResult{stdout: "invalid"}, nil
					}
					return commandResult{stdout: `[[{"tag_name":"v0.7.0-r8","published_at":"2026-01-01"}]]`}, nil
				case args[0] == "git" && args[1] == "merge-base":
					return commandResult{}, nil
				case args[0] == "git" && args[1] == "log":
					if args[len(args)-1] != "v0.7.0-r8..HEAD" {
						t.Fatal(args)
					}
					return commandResult{}, nil
				case args[0] == "gh" && args[2] == "--method":
					if !reflect.DeepEqual(args, []string{"gh", "api", "--method", "PATCH", "repos/tuxcrate/zrepl/releases/42", "--input", "-", "--include"}) {
						t.Fatal(args)
					}
					var payload map[string]string
					if err := json.Unmarshal([]byte(input), &payload); err != nil {
						t.Fatal(err)
					}
					if len(payload) != 2 || payload["tag_name"] != env["RELEASE_TAG"] || !strings.Contains(payload["body"], "0.7.0-r9@"+env["IMAGE_DIGEST"]) || !strings.Contains(payload["body"], "- OS: `Debian/13.7`") || !strings.Contains(payload["body"], "<!-- tuxcrate-track:stable -->") {
						t.Fatal(payload)
					}
					if scenario == "PATCH failure" {
						return commandResult{}, errors.New("PATCH failed")
					}
					patched = true
					return commandResult{}, nil
				default:
					t.Fatalf("unexpected command %v", args)
					return commandResult{}, nil
				}
			}
			stubReleaseOS(t, &a, env)
			if scenario == "OS failure" {
				runner := a.exec
				a.exec = func(input string, args ...string) (commandResult, error) {
					if args[0] == "docker" && args[1] == "create" {
						return commandResult{}, errors.New("OS inspection failed")
					}
					return runner(input, args...)
				}
			}
			err := a.finalize()
			if (err == nil) != (scenario == "success") || patched != (scenario == "success") {
				t.Fatalf("patched=%t, error=%v", patched, err)
			}
			if _, err := os.Stat(env["GITHUB_OUTPUT"]); !os.IsNotExist(err) {
				t.Fatal("finalization must not allocate or expose a different revision")
			}
		})
	}
}

func TestFinalizePrereleaseDraft(t *testing.T) {
	for _, version := range []string{"2.0.0-rc.1", "5.0.0.2870"} {
		t.Run(version, func(t *testing.T) {
			a, env := testApp(t)
			env["APP_VERSION"] = version
			env["IMAGE_VERSION"] = version
			if version == "5.0.0.2870" {
				env["IMAGE_VERSION"] += "-prerelease"
			}
			env["TRACK"] = "prerelease"
			env["PRERELEASE"] = "true"
			env["TAGS"] = `["` + env["IMAGE_VERSION"] + `","preview"]`
			env["RELEASE_TAG"] = "v" + env["IMAGE_VERSION"] + "-r3"
			env["RELEASE_ID"] = "42"
			draft := release{ID: 42, TagName: env["RELEASE_TAG"], TargetCommitish: env["GITHUB_SHA"], Draft: true, Prerelease: true, Body: "<!-- tuxcrate-track:prerelease -->"}
			patched := false
			a.exec = func(input string, args ...string) (commandResult, error) {
				switch {
				case args[0] == "gh" && strings.Contains(strings.Join(args, " "), "/git/ref/heads/"):
					return commandResult{stdout: env["GITHUB_SHA"]}, nil
				case args[0] == "gh" && args[2] == "repos/tuxcrate/zrepl/releases/42":
					data, _ := json.Marshal(draft)
					return commandResult{stdout: string(data)}, nil
				case args[0] == "gh" && args[2] == "--paginate":
					return commandResult{stdout: "[[]]"}, nil
				case args[0] == "docker":
					if args[len(args)-1] != env["IMAGE"]+":"+env["IMAGE_VERSION"]+"-r3" {
						t.Fatal(args)
					}
					return commandResult{code: 1, stderr: "not found"}, nil
				case args[0] == "git" && args[1] == "log":
					return commandResult{}, nil
				case args[0] == "gh" && args[2] == "--method":
					var payload map[string]string
					if err := json.Unmarshal([]byte(input), &payload); err != nil {
						t.Fatal(err)
					}
					if len(payload) != 2 || payload["tag_name"] != env["RELEASE_TAG"] || !strings.Contains(payload["body"], "Application version: `"+version+"`") || !strings.Contains(payload["body"], env["IMAGE_VERSION"]+"-r3@"+env["IMAGE_DIGEST"]) {
						t.Fatal(payload)
					}
					patched = true
					return commandResult{}, nil
				default:
					t.Fatalf("unexpected command %v", args)
					return commandResult{}, nil
				}
			}
			stubReleaseOS(t, &a, env)
			if err := a.finalize(); err != nil || !patched {
				t.Fatalf("patched=%t: %v", patched, err)
			}
		})
	}
}
