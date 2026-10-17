package main

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
	if len(builds) != 2 || builds[0].Image != "ghcr.io/tuxcrate/zrepl" || builds[1].BuildArgs != "\nREV=2" || builds[1].Containerfile != "preview/Containerfile" {
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

func TestUpstreamMetadata(t *testing.T) {
	registry := "https://zrepl.cschwarz.com/apt/debian?suite=trixie&components=main&binaryArch=amd64"
	config := `{"tracks":[{"name":"stable","upstream":{"datasource":"deb","depName":"zrepl","registryUrl":"` + registry + `","version":"0.7.0-1"},"aliases":["{major}.{minor}","latest"]}]}`
	a, _ := configApp(t, config)
	builds, err := a.builds()
	if err != nil || builds[0].Version != "0.7.0" || builds[0].BuildArgs != "\nUPSTREAM_VERSION=0.7.0-1" || builds[0].Aliases != `["0.7","latest"]` || builds[0].Platforms != "linux/amd64,linux/arm64" {
		t.Fatalf("%v: %v", builds, err)
	}
	valid := upstream{Datasource: "deb", DepName: "zrepl", RegistryURL: registry, Version: "0.7.0-1"}
	for _, mutate := range []func(*upstream){
		func(u *upstream) { u.Datasource = "github-releases" },
		func(u *upstream) { u.DepName = "bad package" },
		func(u *upstream) { u.RegistryURL = "file:///etc/passwd" },
		func(u *upstream) { u.RegistryURL = "https://example.com/apt" },
		func(u *upstream) { u.RegistryURL = strings.ReplaceAll(registry, "binaryArch=amd64", "binaryArch=") },
		func(u *upstream) { u.Version = "" },
		func(u *upstream) { u.Version = "0.8.0-rc.1-1" },
		func(u *upstream) { u.Version = "1:0.8.0-1" },
		func(u *upstream) { u.Version = "0.8.0-1~deb13" },
	} {
		u := valid
		mutate(&u)
		if u.validate() == nil {
			t.Errorf("accepted invalid upstream: %+v", u)
		}
	}
	for _, conflicting := range []string{
		strings.Replace(config, `"name":"stable"`, `"name":"stable","version":"0.7.0"`, 1),
		strings.Replace(config, `"name":"stable"`, `"name":"stable","buildArgs":{"UPSTREAM_VERSION":"other"}`, 1),
	} {
		a, _ := configApp(t, conflicting)
		if _, err := a.builds(); err == nil {
			t.Fatal("accepted competing track package pin")
		}
	}
	for _, version := range []string{"0.7.0-2", "0.8.0-1"} {
		a, _ := configApp(t, strings.ReplaceAll(config, "0.7.0-1", version))
		builds, err := a.builds()
		appVersion, _, _ := strings.Cut(version, "-")
		if err != nil || builds[0].Version != appVersion || !strings.Contains(builds[0].BuildArgs, "="+version) {
			t.Fatalf("%s: %v: %v", version, builds, err)
		}
	}
	if _, err := decodeConfig(strings.NewReader(`{"tracks":[{"upstream":{"datasource":"deb","typo":true}}]}`)); err == nil {
		t.Fatal("accepted unknown upstream field")
	}
	if _, err := decodeConfig(strings.NewReader(strings.Replace(config, `"version":"0.7.0-1"`, `"version":"0.7.0-1","buildArg":"CUSTOM_VERSION"`, 1))); err == nil {
		t.Fatal("accepted removed upstream buildArg field")
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
		`{"tracks":[{"name":"stable","version":"1.2.3","aliases":[],"buildArgs":{"UPSTREAM_VERSION":"other"}}]}`,
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
	if err != nil || builds[0].Image != "ghcr.io/tuxcrate/custom" || builds[0].Context != "app" || builds[0].Containerfile != "Containerfile" || builds[0].Platforms != "linux/amd64" || builds[0].BuildArgs != "\nFOO=bar" || builds[0].Version != "1.2.3" {
		t.Fatalf("%v: %v", builds, err)
	}
}

func TestManifest(t *testing.T) {
	a, env := testApp(t)
	env["RELEASE_TAG"] = "v0.7.0-r3"
	env["ALIASES"] = `["stable","latest"]`
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
	env["RELEASE_TAG"] = "v0.8.0-r3"
	if a.manifest() == nil {
		t.Fatal("accepted mismatched tag")
	}
}

func TestValidateBaseImage(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("a", 64)
	for _, image := range []string{"", "debian:13-slim" + digest, "docker.io/library/debian:13.7-slim" + digest, "registry.example.com:5000/team/base:1.2.3" + digest} {
		if err := validateBaseImage(image); err != nil {
			t.Errorf("rejected %q: %v", image, err)
		}
	}
	for _, image := range []string{"13.7-slim" + digest, "debian:13-slim", "debian" + digest, "debian:13-slim@sha256:bad", "debian:13-slim@sha256:" + strings.Repeat("A", 64), "https://docker.io/debian:13" + digest, "debian:13-slim" + digest + "\n", "debian:13 slim" + digest, ":13" + digest, "debian:" + digest} {
		if validateBaseImage(image) == nil {
			t.Errorf("accepted invalid base image %q", image)
		}
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
	if builds[0].BuildArgs != "\nREV=1\nBASE_IMAGE="+base || builds[1].BuildArgs != "\nBASE_IMAGE="+override {
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
	if err != nil || builds[0].BuildArgs != "\nBASE_IMAGE="+base {
		t.Fatalf("%v: %v", builds, err)
	}
}

func TestStrictYAMLConfiguration(t *testing.T) {
	for _, config := range []string{
		"baseImage: debian:13-slim\ntracks: []\n",
		"tracks: []\ntracks: []\n",
		"tracks: []\n---\ntracks: []\n",
		"tracks:\n  - name: stable\n    version: '1.2.3'\n    aliases: []\n    unknownField: true\n",
	} {
		if _, err := decodeConfig(strings.NewReader(config)); err == nil {
			t.Errorf("accepted invalid YAML configuration %q", config)
		}
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
