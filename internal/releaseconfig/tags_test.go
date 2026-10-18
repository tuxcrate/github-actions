package releaseconfig

import (
	"strings"
	"testing"
)

func TestImageVersionOwnership(t *testing.T) {
	// A numeric upstream can be installed in both channels without publishing
	// the prerelease image under the stable version or sharing its revision space.
	config := `tracks:
  - name: stable
    version: "1.2.3"
    tags: ["${version}", latest]
  - name: preview
    version: "1.2.3"
    prerelease: true
    tags: ["${version}-preview", preview]
`
	builds, err := Plan(strings.NewReader(config), "tuxcrate/app")
	if err != nil || len(builds) != 2 || builds[0].ImageVersion != "1.2.3" || builds[1].ImageVersion != "1.2.3-preview" || builds[1].BuildArgs != "APP_VERSION=1.2.3" {
		t.Fatalf("%v: %v", builds, err)
	}
	for _, bad := range []string{
		strings.Replace(config, "${version}-preview", "${version}", 1),
		strings.Replace(config, "preview]", "latest]", 1),
		strings.Replace(config, "prerelease: true", "prerelease: false", 1),
	} {
		got, err := Plan(strings.NewReader(bad), "tuxcrate/app")
		if strings.Contains(bad, "prerelease: false") {
			if err != nil || !got[1].Prerelease {
				t.Fatalf("image suffix must imply prerelease: %v: %v", got, err)
			}
		} else if err == nil {
			t.Fatalf("accepted conflicting tags: %s", bad)
		}
	}
}
