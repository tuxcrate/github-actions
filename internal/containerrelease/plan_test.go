package containerrelease

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func configApp(t *testing.T, config string) (app, map[string]string) {
	t.Helper()
	a, env := testApp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.WriteFile("meta.yaml", []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	return a, env
}

func TestBuildPlan(t *testing.T) {
	a, env := configApp(t, `{"tracks":[
 {"name":"stable","version":"1.2.3","aliases":["latest","stable","1.2"]},
 {"name":"preview","version":"2.0.0-rc.1","aliases":["preview"],"containerfile":"preview/Containerfile","buildArgs":{"REV":"2"}}
]}`)
	builds, err := a.builds()
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 2 || builds[0].Image != "ghcr.io/tuxcrate/zrepl" || builds[0].BuildArgs != "APP_VERSION=1.2.3" || builds[1].BuildArgs != "APP_VERSION=2.0.0-rc.1\nREV=2" || builds[1].Containerfile != "preview/Containerfile" {
		t.Fatal(builds)
	}
	if err := a.plan(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(env["GITHUB_OUTPUT"])
	if !strings.Contains(string(data), `"include":[`) {
		t.Fatal(string(data))
	}
}

func TestDerivedAliases(t *testing.T) {
	for _, version := range []string{"0.7.0", "0.8.0", "12.34.56"} {
		a, _ := configApp(t, `{"tracks":[{"name":"stable","version":"`+version+`","aliases":["{major}.{minor}","latest"]}]}`)
		builds, err := a.builds()
		parts := strings.Split(version, ".")
		if err != nil || builds[0].Aliases != `["`+parts[0]+`.`+parts[1]+`","latest"]` {
			t.Fatalf("%s: %v: %v", version, builds, err)
		}
	}
	for _, config := range []string{
		`{"tracks":[{"name":"preview","version":"1.2.3-rc.1","aliases":["{major}.{minor}"]}]}`,
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":["{major}.{minor}","1.2"]}]}`,
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":["{patch}"]}]}`,
		`{"tracks":[{"name":"a","version":"1.2.3","aliases":["{major}.{minor}"]},{"name":"b","version":"1.2.4","aliases":["1.2"]}]}`,
	} {
		a, _ := configApp(t, config)
		if _, err := a.builds(); err == nil {
			t.Fatalf("accepted invalid expanded aliases: %s", config)
		}
	}
}

func TestInvalidConfigurations(t *testing.T) {
	for _, config := range []string{
		`{"tracks":[]}`,
		`{"typo":true,"tracks":[]}`,
		`{"tracks":[]} {}`,
		`{"tracks":[{"name":"stable","version":"1.2.3"}]}`,
		`{"tracks":[{"name":"Stable","version":"1.2.3","aliases":[]}]}`,
		`{"tracks":[{"name":"preview","version":"1.2.3-rc.1","aliases":["latest"]}]}`,
		`{"tracks":[{"name":"preview","version":"1.2.3-rc.1","aliases":["stable"]}]}`,
		`{"tracks":[{"name":"preview","version":"1.2.3-rc.1","aliases":["1.2"]}]}`,
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":["1.2.3-r0"]}]}`,
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":["2.0.0"]}]}`,
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":["stable","stable"]}]}`,
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":[],"context":"../other"}]}`,
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":[],"buildArgs":{"APP_VERSION":"other"}}]}`,
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":[],"buildArgs":{"ARG":"x\ny"}}]}`,
		`{"tracks":[{"name":"a","version":"1.2.3","aliases":["latest"]},{"name":"b","version":"2.0.0","aliases":["latest"]}]}`,
		`{"tracks":[{"name":"a","version":"1.2.3","aliases":[]},{"name":"b","version":"1.2.3","aliases":[]}]}`,
	} {
		t.Run(config, func(t *testing.T) {
			a, _ := configApp(t, config)
			if _, err := a.builds(); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestPlanRequiresRootMetadata(t *testing.T) {
	a, env := configApp(t, "tracks: []\n")
	if err := os.Rename("meta.yaml", "other.yaml"); err != nil {
		t.Fatal(err)
	}
	env["TRACKS_FILE"] = "other.yaml"
	env["APP_VERSION"] = "1.2.3"
	if _, err := a.builds(); err == nil {
		t.Fatal("accepted missing root metadata with legacy configuration")
	}
}

func TestExplicitTags(t *testing.T) {
	a, env := testApp(t)
	env["ALIASES"] = `["stable","latest"]`
	got, err := a.tags("1.2.3", 4)
	if err != nil || !reflect.DeepEqual(got, []string{"1.2.3-r4", "1.2.3", "stable", "latest"}) {
		t.Fatalf("%v: %v", got, err)
	}
	env["ALIASES"] = `[]`
	got, err = a.tags("2.0.0-rc.1", 0)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v: %v", got, err)
	}
	env["ALIASES"] = ""
	got, err = a.tags("2.0.0-rc.1", 0)
	if err != nil || contains(got, "latest") || !contains(got, "preview") {
		t.Fatalf("%v: %v", got, err)
	}
	env["ALIASES"] = `["latest"]`
	if _, err := a.tags("2.0.0-rc.1", 0); err == nil {
		t.Fatal("prerelease stole latest")
	}
}

func TestPreviousReleaseTrack(t *testing.T) {
	a, env := testApp(t)
	env["TRACK"] = "preview"
	a.exec = func(_ string, args ...string) (commandResult, error) { return commandResult{}, nil }
	got, err := a.previousRelease([]release{
		{TagName: "v1.0.0-r0", PublishedAt: "2026-02-01"},
		{TagName: "v2.0.0-rc.1-r0", Prerelease: true, PublishedAt: "2026-01-01", Body: "<!-- tuxcrate-track:preview -->"},
	})
	if err != nil || got != "v2.0.0-rc.1-r0" {
		t.Fatalf("%s: %v", got, err)
	}
}

func TestPrereleaseReservation(t *testing.T) {
	a, env := testApp(t)
	env["APP_VERSION"] = "2.0.0-rc.1"
	env["TRACK"] = "preview"
	env["ALIASES"] = `["preview"]`
	a.exec = func(input string, args ...string) (commandResult, error) {
		switch {
		case args[0] == "gh" && args[1] == "api" && strings.HasPrefix(args[2], "repos/"):
			return commandResult{stdout: env["GITHUB_SHA"]}, nil
		case args[0] == "gh" && args[2] == "--paginate":
			return commandResult{stdout: `[[{"tag_name":"v2.0.0-rc.1-r2","draft":true}]]`}, nil
		case args[0] == "git" && args[1] == "tag":
			return commandResult{}, nil
		case args[0] == "docker":
			if args[len(args)-1] != "ghcr.io/tuxcrate/zrepl:2.0.0-rc.1-r3" {
				t.Fatal(args)
			}
			return commandResult{code: 1, stderr: "not found"}, nil
		case args[0] == "git" && args[1] == "log":
			return commandResult{}, nil
		case args[0] == "gh" && args[2] == "--method":
			var payload map[string]any
			if err := json.Unmarshal([]byte(input), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["prerelease"] != true || !strings.Contains(payload["body"].(string), "<!-- tuxcrate-track:preview -->") {
				t.Fatal(payload)
			}
			return commandResult{}, nil
		default:
			t.Fatalf("unexpected command %v", args)
			return commandResult{}, nil
		}
	}
	if err := a.reserve(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(env["GITHUB_OUTPUT"])
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	if strings.Contains(output, "\nlatest\n") || strings.Contains(output, "\nstable\n") || !strings.Contains(output, "latest<<TUXCRATE_OUTPUT\nfalse\n") || !strings.Contains(output, "2.0.0-rc.1-r3\n2.0.0-rc.1\npreview\n") {
		t.Fatal(output)
	}
}

func TestPlanMetadataOverrides(t *testing.T) {
	config := `{"image":"ghcr.io/tuxcrate/custom","tracks":[{"name":"stable","version":"1.2.3","aliases":[],"context":"app","platforms":"linux/amd64","buildArgs":{"FOO":"bar"}}]}`
	a, env := configApp(t, config)
	// Workflow environment cannot compete with the metadata source of truth.
	env["IMAGE"] = "ghcr.io/tuxcrate/other"
	env["CONTEXT"] = "other"
	env["CONTAINERFILE"] = "Otherfile"
	env["PLATFORMS"] = "linux/arm64"
	env["BUILD_ARGS"] = "FOO=other"
	env["APP_VERSION"] = "9.9.9"
	builds, err := a.builds()
	if err != nil || builds[0].Image != "ghcr.io/tuxcrate/custom" || builds[0].Context != "app" || builds[0].Containerfile != "Containerfile" || builds[0].Platforms != "linux/amd64" || builds[0].BuildArgs != "APP_VERSION=1.2.3\nFOO=bar" || builds[0].Version != "1.2.3" {
		t.Fatalf("%v: %v", builds, err)
	}
}

func TestManifest(t *testing.T) {
	a, env := testApp(t)
	env["RELEASE_TAG"] = "v0.7.0-r3"
	env["ALIASES"] = `["stable","latest"]`
	env["PLATFORMS"] = " linux/amd64 , linux/arm64 "
	var calls []string
	a.exec = imageOSRunner(t, env["IMAGE"]+"@"+env["IMAGE_DIGEST"], "linux/amd64", map[string]string{
		"/etc/os-release":     "ID=debian\nVERSION_ID=\"13\"\n",
		"/etc/debian_version": "13.7\n",
	}, "", &calls)
	// manifest writes in the source checkout, as the workflow does.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := a.manifest(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(".", "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["schemaVersion"] != float64(1) || m["revision"] != float64(3) || m["immutableTag"] != "0.7.0-r3" || m["track"] != "stable" || m["digest"] != env["IMAGE_DIGEST"] {
		t.Fatal(m)
	}
	if m["os_flavor"] != "debian" || m["os_version"] != "13.7" {
		t.Fatal(m)
	}
	if !reflect.DeepEqual(m["platforms"], []any{"linux/amd64", "linux/arm64"}) {
		t.Fatal(m)
	}
	if !reflect.DeepEqual(calls, []string{"create", "cp", "cp", "rm"}) {
		t.Fatal(calls)
	}
	env["PLATFORMS"] = ""
	if a.manifest() == nil {
		t.Fatal("accepted missing image platform")
	}
	env["PLATFORMS"] = "linux/amd64,linux/arm64"
	env["RELEASE_TAG"] = "v0.8.0-r3"
	if a.manifest() == nil {
		t.Fatal("accepted mismatched tag")
	}
}

func TestCheckManifest(t *testing.T) {
	for _, tt := range []struct {
		name, key, value, osRelease string
		valid                       bool
	}{
		{name: "complete manifest", valid: true},
		{name: "platform whitespace", key: "PLATFORMS", value: " linux/amd64 , linux/arm64 ", valid: true},
		{name: "empty OS flavor", osRelease: "ID=\nVERSION_ID=3.24.2"},
		{name: "empty OS version", osRelease: "ID=alpine\nVERSION_ID="},
		{name: "missing version", key: "APP_VERSION"},
		{name: "missing digest", key: "IMAGE_DIGEST"},
		{name: "invalid image", key: "IMAGE", value: "invalid"},
		{name: "invalid track", key: "TRACK", value: "invalid track"},
		{name: "invalid aliases", key: "ALIASES", value: `[""]`},
		{name: "missing platforms", key: "PLATFORMS"},
		{name: "empty second platform", key: "PLATFORMS", value: "linux/amd64,"},
		{name: "whitespace-only platform", key: "PLATFORMS", value: "linux/amd64, "},
		{name: "invalid platform", key: "PLATFORMS", value: "amd64"},
		{name: "missing source commit", key: "GITHUB_SHA"},
		{name: "invalid source commit", key: "GITHUB_SHA", value: "not-a-commit"},
		{name: "missing local image", key: "LOCAL_IMAGE"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, env := testApp(t)
			env["LOCAL_IMAGE"] = "tuxcrate-pr-os-check:stable"
			env["TRACK"] = "stable"
			env["ALIASES"] = `["stable","latest"]`
			// PR checks synthesize their own tag instead of reserving a revision.
			env["RELEASE_TAG"] = "not-a-real-release"
			if tt.key != "" {
				env[tt.key] = tt.value
			}
			data := "ID=alpine\nVERSION_ID=3.24.2"
			if tt.osRelease != "" {
				data = tt.osRelease
			}
			var calls []string
			a.exec = imageOSRunner(t, env["LOCAL_IMAGE"], "linux/amd64", map[string]string{"/etc/os-release": data}, "", &calls)
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chdir(cwd) })
			err = a.checkManifest()
			if !tt.valid {
				if err == nil {
					t.Fatal("accepted missing or invalid required metadata")
				}
				if _, statErr := os.Stat("release.json"); !os.IsNotExist(statErr) {
					t.Fatal("created a manifest with invalid metadata")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := os.ReadFile("release.json")
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(manifest, &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"schemaVersion", "image", "track", "version", "revision", "immutableTag", "digest", "aliasesAtPublication", "platforms", "sourceCommit", "os_flavor", "os_version"} {
				if value, ok := fields[key]; !ok || value == nil || value == "" {
					t.Fatalf("missing or empty %s in %s", key, manifest)
				}
			}
			if fields["revision"] != float64(0) || fields["immutableTag"] != "0.7.0-r0" || fields["image"] != env["IMAGE"] {
				t.Fatal(fields)
			}
			if !reflect.DeepEqual(fields["platforms"], []any{"linux/amd64", "linux/arm64"}) {
				t.Fatal(fields)
			}
			if _, err := os.Stat(env["GITHUB_OUTPUT"]); !os.IsNotExist(err) {
				t.Fatal("validation must not emit publication outputs")
			}
		})
	}
}

func TestPerTrackBaseImages(t *testing.T) {
	base := "debian:13-slim@sha256:" + strings.Repeat("a", 64)
	override := "debian:14-slim@sha256:" + strings.Repeat("b", 64)
	config := `tracks:
  - name: stable
    version: "1.2.3"
    baseImage: ` + base + `
    aliases: [latest]
    buildArgs:
      REV: "1"
  - name: preview
    version: "2.0.0-rc.1"
    baseImage: "` + override + `"
    aliases: [preview]
`
	a, env := configApp(t, config)
	builds, err := a.builds()
	if err != nil {
		t.Fatal(err)
	}
	if builds[0].BuildArgs != "APP_VERSION=1.2.3\nREV=1\nBASE_IMAGE="+base || builds[1].BuildArgs != "APP_VERSION=2.0.0-rc.1\nBASE_IMAGE="+override {
		t.Fatal(builds)
	}
	if err := a.plan(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(env["GITHUB_OUTPUT"])
	if err != nil || !strings.Contains(string(data), "BASE_IMAGE="+base) || !strings.Contains(string(data), "BASE_IMAGE="+override) {
		t.Fatalf("base images missing from build matrix: %s, %v", data, err)
	}
}

func TestTrackBaseImage(t *testing.T) {
	base := "debian:13-slim@sha256:" + strings.Repeat("a", 64)
	a, _ := configApp(t, `{"tracks":[{"name":"stable","version":"1.2.3","aliases":[],"baseImage":"`+base+`"}]}`)
	builds, err := a.builds()
	if err != nil || builds[0].BuildArgs != "APP_VERSION=1.2.3\nBASE_IMAGE="+base {
		t.Fatalf("%v: %v", builds, err)
	}
}

func TestInvalidBaseImageConfiguration(t *testing.T) {
	for _, config := range []string{
		`{"baseImage":"debian:13-slim","tracks":[{"name":"stable","version":"1.2.3","aliases":[]}]}`,
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":[],"baseImage":"debian:13-slim"}]}`,
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":[],"buildArgs":{"BASE_IMAGE":"debian:13-slim"}}]}`,
	} {
		a, _ := configApp(t, config)
		if _, err := a.builds(); err == nil {
			t.Errorf("accepted invalid base-image configuration %s", config)
		}
	}
}
