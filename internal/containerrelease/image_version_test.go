package containerrelease

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSuffixedManifest(t *testing.T) {
	for _, synthetic := range []bool{false, true} {
		a, env := configApp(t, "tracks: []")
		env["APP_VERSION"] = "5.0.0.2668"
		env["IMAGE_VERSION"] = "5.0.0.2668-prerelease"
		env["TRACK"] = "prerelease"
		env["PRERELEASE"] = "true"
		env["TAGS"] = `["5-prerelease","5.0.0.2668-prerelease"]`
		env["RELEASE_TAG"] = "v5.0.0.2668-prerelease-r4"
		image := env["IMAGE"] + "@" + env["IMAGE_DIGEST"]
		if synthetic {
			env["LOCAL_IMAGE"] = "local:prerelease"
			image = env["LOCAL_IMAGE"]
		}
		var calls []string
		a.exec = imageOSRunner(t, image, "linux/amd64", map[string]string{"/etc/os-release": "ID=alpine\nVERSION_ID=3.24.2"}, "", &calls)
		check := a.manifest
		wantTag := "5.0.0.2668-prerelease-r4"
		if synthetic {
			check = a.checkManifest
			wantTag = "5.0.0.2668-prerelease-r0"
		}
		if err := check(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile("release.json")
		if err != nil {
			t.Fatal(err)
		}
		var record releaseManifest
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if record.SchemaVersion != 2 || record.Version != env["APP_VERSION"] || record.ImageVersion != env["IMAGE_VERSION"] || record.ImmutableTag != wantTag || contains(record.Aliases, env["APP_VERSION"]) {
			t.Fatalf("%s", data)
		}
	}
}

func TestRejectUnrelatedImageVersion(t *testing.T) {
	a, env := testApp(t)
	env["IMAGE_VERSION"] = "9.9.9-prerelease"
	env["TAGS"] = `["9.9.9-prerelease"]`
	env["RELEASE_TAG"] = "v9.9.9-prerelease-r0"
	a.exec = func(_ string, args ...string) (commandResult, error) {
		if args[0] == "gh" && strings.Contains(strings.Join(args, " "), "/git/ref/heads/") {
			return commandResult{stdout: env["GITHUB_SHA"]}, nil
		}
		t.Fatalf("invalid version reached external command: %v", args)
		return commandResult{}, nil
	}
	if err := a.reserve(); err == nil {
		t.Fatal("reserved unrelated image version")
	}
	if err := a.manifest(); err == nil {
		t.Fatal("generated unrelated image metadata")
	}
}
