package releaseconfig

import (
	"strings"
	"testing"
)

func TestPackageVersionBuildArgumentIsReserved(t *testing.T) {
	_, err := Plan(strings.NewReader(`tracks:
  - name: stable
    version: "1.2.3"
    tags: ["${version}"]
    buildArgs:
      PACKAGE_VERSION: "1.2.3-r99"
`), "tuxcrate/test")
	if err == nil || !strings.Contains(err.Error(), `invalid build argument "PACKAGE_VERSION"`) {
		t.Fatalf("caller must not override the workflow's reserved package version: %v", err)
	}
}
