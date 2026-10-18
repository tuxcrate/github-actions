package releaseconfig

import (
	"strings"
	"testing"
)

func TestPlanUsesExplicitInputs(t *testing.T) {
	// Planning must not read workflow environment or require a source checkout.
	t.Setenv("GITHUB_REPOSITORY", "invalid")
	t.Setenv("IMAGE", "ghcr.io/other/image")
	t.Setenv("APP_VERSION", "9.9.9")
	config := `tracks:
  - name: stable
    version: "1.2.3"
    tags: ["${version}", "${major}.${minor}", latest]
    buildArgs:
      Z: last
      A: first
`
	builds, err := Plan(strings.NewReader(config), "Tuxcrate/Zrepl")
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 1 {
		t.Fatalf("got %d builds; want 1", len(builds))
	}
	b := builds[0]
	if b.Image != "ghcr.io/tuxcrate/zrepl" || b.Version != "1.2.3" || b.ImageVersion != "1.2.3" || b.Tags != `["1.2.3","1.2","latest"]` || b.BuildArgs != "APP_VERSION=1.2.3\nA=first\nZ=last" {
		t.Fatal(b)
	}
	if b.Context != "." || b.Containerfile != "Containerfile" || b.Platforms != "linux/amd64,linux/arm64" {
		t.Fatal(b)
	}
}

func TestPlanRejectsInvalidRepository(t *testing.T) {
	if _, err := Plan(strings.NewReader("tracks: []\n"), "invalid"); err == nil {
		t.Fatal("accepted an invalid repository")
	}
}

func TestFourPartPrereleaseTracks(t *testing.T) {
	config := `tracks:
  - name: stable
    upstream:
      datasource: github-releases
      depName: Sonarr/Sonarr
      version: "4.0.20.3014"
    tags: ["${version}", "${major}", "${major}.${minor}", latest]
  - name: develop
    upstream:
      datasource: github-releases
      depName: Sonarr/Sonarr
      version: "5.0.0.2668"
    prerelease: true
    tags: ["${version}-prerelease", "${major}-prerelease", "${major}.${minor}-prerelease"]
    buildArgs:
      SONARR_BRANCH: develop
`
	builds, err := Plan(strings.NewReader(config), "tuxcrate/sonarr")
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 2 || builds[0].Prerelease || !builds[1].Prerelease || builds[0].Tags != `["4.0.20.3014","4","4.0","latest"]` || builds[1].Tags != `["5.0.0.2668-prerelease","5-prerelease","5.0-prerelease"]` || builds[1].ImageVersion != "5.0.0.2668-prerelease" || builds[1].Version != "5.0.0.2668" || builds[1].BuildArgs != "APP_VERSION=5.0.0.2668\nSONARR_BRANCH=develop" {
		t.Fatal(builds)
	}
	for _, alias := range []string{"latest", "stable", "5", "5.0", "5.0.0", "5.0.0.2668"} {
		bad := strings.Replace(config, `["${version}-prerelease", "${major}-prerelease", "${major}.${minor}-prerelease"]`, `["${version}-prerelease", "`+alias+`"]`, 1)
		if _, err := Plan(strings.NewReader(bad), "tuxcrate/sonarr"); err == nil {
			t.Errorf("accepted prerelease alias %q", alias)
		}
	}
}

func TestPrereleaseSuffixCannotBeDisabled(t *testing.T) {
	builds, err := Plan(strings.NewReader(`tracks:
  - name: preview
    version: "1.2.3-rc.1"
    prerelease: false
    tags: ["${version}", preview]
`), "tuxcrate/test")
	if err != nil || !builds[0].Prerelease {
		t.Fatalf("%v: %v", builds, err)
	}
}
