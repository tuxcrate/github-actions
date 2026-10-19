package releaseconfig

import (
	"strings"
	"testing"
)

func TestGitHubReleaseSource(t *testing.T) {
	for _, tt := range []struct {
		name, dependency, track, want string
	}{
		{"valid", "Example/App", "stable", ""},
		{"path traversal", "Example/../App", "stable", "requires owner/repo"},
		{"query", "Example/App?x=y", "stable", "requires owner/repo"},
		{"unknown track", "Example/App", "missing", "unknown release track"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := `tracks:
  - name: stable
    upstream: {datasource: github-releases, depName: "` + tt.dependency + `", version: "1.2.3"}
    tags: ["${version}"]
`
			repo, version, err := GitHubReleaseSource(strings.NewReader(config), "tuxcrate/example", tt.track)
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("got %v, want %s", err, tt.want)
				}
				return
			}
			if err != nil || repo != "Example/App" || version != "1.2.3" {
				t.Fatalf("got %q, %q, %v", repo, version, err)
			}
		})
	}
}
