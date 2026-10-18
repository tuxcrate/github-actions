package containerrelease

import (
	"strings"
	"testing"
)

func TestNumericPrereleaseAliases(t *testing.T) {
	a, env := testApp(t)
	env["APP_VERSION"] = "5.0.0.2668"
	env["PRERELEASE"] = "true"
	for _, aliases := range []string{`["latest"]`, `["stable"]`, `["5"]`, `["5.0"]`} {
		env["ALIASES"] = aliases
		if _, err := a.tags(env["APP_VERSION"], 0); err == nil {
			t.Fatalf("accepted %s", aliases)
		}
	}
	env["ALIASES"] = ""
	tags, err := a.tags(env["APP_VERSION"], 0)
	if err != nil || strings.Join(tags, ",") != "5.0.0.2668-r0,5.0.0.2668,preview" || a.trackName() != "preview" {
		t.Fatalf("%v: %v", tags, err)
	}
	env["ALIASES"] = `["5-prerelease","5.0-prerelease"]`
	tags, err = a.tags(env["APP_VERSION"], 0)
	if err != nil || strings.Join(tags, ",") != "5.0.0.2668-r0,5.0.0.2668,5-prerelease,5.0-prerelease" {
		t.Fatalf("%v: %v", tags, err)
	}
}
