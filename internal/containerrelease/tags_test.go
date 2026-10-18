package containerrelease

import (
	"strings"
	"testing"
)

func TestNumericPrereleaseTags(t *testing.T) {
	a, env := testApp(t)
	env["APP_VERSION"] = "5.0.0.2668"
	env["IMAGE_VERSION"] = "5.0.0.2668-prerelease"
	env["PRERELEASE"] = "true"
	for _, tags := range []string{"", `null`, `[]`, `["5.0.0.2668-prerelease","latest"]`, `["5.0.0.2668-prerelease","stable"]`, `["5.0.0.2668-prerelease","5"]`, `["5.0.0.2668-prerelease","5.0.0.2668"]`} {
		env["TAGS"] = tags
		if _, err := a.tags(env["IMAGE_VERSION"], 0); err == nil {
			t.Fatalf("accepted %s", tags)
		}
	}
	env["TAGS"] = `["5.0.0.2668-prerelease","5-prerelease","5.0-prerelease"]`
	tags, err := a.tags(env["IMAGE_VERSION"], 0)
	if err != nil || strings.Join(tags, ",") != "5.0.0.2668-prerelease-r0,5.0.0.2668-prerelease,5-prerelease,5.0-prerelease" || a.trackName() != "preview" {
		t.Fatalf("%v: %v", tags, err)
	}
	if _, err := a.tags(env["APP_VERSION"], 0); err == nil {
		t.Fatal("accepted unsuffixed numeric prerelease")
	}
}
