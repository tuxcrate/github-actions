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
    aliases: ["{major}.{minor}", latest]
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
	if b.Image != "ghcr.io/tuxcrate/zrepl" || b.Version != "1.2.3" || b.Aliases != `["1.2","latest"]` || b.BuildArgs != "APP_VERSION=1.2.3\nA=first\nZ=last" {
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
