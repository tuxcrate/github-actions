package releaseconfig

import (
	"strings"
	"testing"
)

const githubConfig = `tracks:
  - name: stable
    upstream: {datasource: github-releases, depName: "Example/App", version: "1.2.3"}
    githubRelease:
      tag: "v${version}"
      assets:
        amd64: "app.${version}.x64.tar.gz"
        arm64: "app.${version}.arm64.tar.gz"
    tags: ["${version}"]
`

func TestGitHubReleaseSource(t *testing.T) {
	for _, tt := range []struct {
		name, old, replacement, track, want string
	}{
		{name: "valid", track: "stable"},
		{name: "bare tag", old: `tag: "v${version}"`, replacement: `tag: "${version}"`, track: "stable"},
		{name: "path traversal", old: "Example/App", replacement: "Example/../App", track: "stable", want: "owner/repo"},
		{name: "query", old: "Example/App", replacement: "Example/App?x=y", track: "stable", want: "owner/repo"},
		{name: "unknown track", track: "missing", want: "unknown release track"},
		{name: "unknown placeholder", old: "v${version}", replacement: "v${latest}", track: "stable", want: "tag template"},
		{name: "empty tag", old: `tag: "v${version}"`, replacement: `tag: ""`, track: "stable", want: "tag template"},
		{name: "moving tag", old: `tag: "v${version}"`, replacement: `tag: latest`, track: "stable", want: "tag template"},
		{name: "asset path", old: "app.${version}.x64.tar.gz", replacement: "../app.tar.gz", track: "stable", want: "invalid or duplicate"},
		{name: "asset injection", old: "app.${version}.x64.tar.gz", replacement: `app\nTOKEN=value`, track: "stable", want: "invalid or duplicate"},
		{name: "duplicate asset", old: "app.${version}.arm64.tar.gz", replacement: "app.${version}.x64.tar.gz", track: "stable", want: "invalid or duplicate"},
		{name: "missing architecture", old: "arm64:", replacement: "arm:", track: "stable", want: "no asset for platform"},
		{name: "invalid architecture", old: "arm64:", replacement: "arm_64:", track: "stable", want: "invalid or duplicate"},
		{name: "other datasource", old: "datasource: github-releases", replacement: "datasource: custom", track: "stable", want: "public github-releases"},
		{name: "custom registry", old: `depName: "Example/App",`, replacement: `registryUrl: https://github.example.com, depName: "Example/App",`, track: "stable", want: "public github-releases"},
		{name: "reserved build arg", old: `    tags:`, replacement: "    buildArgs: {UPSTREAM_URL_AMD64: override}\n    tags:", track: "stable", want: "invalid build argument"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := githubConfig
			if tt.old != "" {
				config = strings.ReplaceAll(config, tt.old, tt.replacement)
			}
			source, err := GitHubReleaseSource(strings.NewReader(config), "tuxcrate/example", tt.track)
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("got %v, want %s", err, tt.want)
				}
				return
			}
			if err != nil || source.Repository != "Example/App" || source.Assets["amd64"] != "app.1.2.3.x64.tar.gz" || source.Prerelease {
				t.Fatalf("got %+v, %v", source, err)
			}
			wantTag := "v1.2.3"
			if tt.name == "bare tag" {
				wantTag = "1.2.3"
			}
			if source.Tag != wantTag {
				t.Fatalf("tag = %q, want %q", source.Tag, wantTag)
			}
		})
	}
}

func TestGitHubReleasePrereleaseSource(t *testing.T) {
	for _, config := range []string{
		strings.Replace(githubConfig, `    tags: ["${version}"]`, "    prerelease: true\n    tags: [\"${version}-prerelease\"]", 1),
		strings.ReplaceAll(githubConfig, `tags: ["${version}"]`, `tags: ["${version}-preview"]`),
		strings.ReplaceAll(githubConfig, `"1.2.3"`, `"1.2.3-rc.1"`),
	} {
		source, err := GitHubReleaseSource(strings.NewReader(config), "tuxcrate/example", "stable")
		if err != nil || !source.Prerelease {
			t.Fatalf("prerelease track must require matching upstream channel: %+v, %v", source, err)
		}
	}
}
