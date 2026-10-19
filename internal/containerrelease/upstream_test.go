package containerrelease

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/tuxcrate/github-actions/internal/releaseconfig"
)

const upstreamConfig = `tracks:
  - name: stable
    upstream: {datasource: github-releases, depName: Example/App, version: "1.2.3"}
    githubRelease:
      tag: "v${version}"
      assets:
        amd64: "app.${version}.x64.tar.gz"
        arm64: "app.${version}.arm64.tar.gz"
    tags: ["${version}"]
`

func upstreamResponse() string {
	return `{"tag_name":"v1.2.3","draft":false,"prerelease":false,"assets":[
{"name":"app.1.2.3.x64.tar.gz","browser_download_url":"https://github.com/Example/App/releases/download/v1.2.3/app.1.2.3.x64.tar.gz","digest":"sha256:` + strings.Repeat("a", 64) + `"},
{"name":"app.1.2.3.arm64.tar.gz","browser_download_url":"https://github.com/Example/App/releases/download/v1.2.3/app.1.2.3.arm64.tar.gz","digest":"sha256:` + strings.Repeat("b", 64) + `"}]}`
}

func TestUpstreamRelease(t *testing.T) {
	for _, tt := range []struct {
		name, want                   string
		missingToken, fail, execFail bool
	}{
		{name: "success"},
		{name: "no token", missingToken: true, want: "requires GH_TOKEN"},
		{name: "HTTP failure", fail: true, want: "fetch upstream release metadata failed (HTTP 403)"},
		{name: "network error", execFail: true, want: "fetch upstream release metadata failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, env := configApp(t, upstreamConfig)
			env["TRACK"], env["GH_TOKEN"], env["RUNNER_TEMP"] = "stable", "test-token", t.TempDir()
			if tt.missingToken {
				delete(env, "GH_TOKEN")
			}
			calls := 0
			a.exec = func(input string, args ...string) (commandResult, error) {
				calls++
				want := []string{"gh", "api", "--hostname", "github.com", "repos/Example/App/releases/tags/v1.2.3"}
				if calls != 1 || input != "" || !reflect.DeepEqual(args, want) {
					t.Fatalf("unexpected authenticated request: %v", args)
				}
				if tt.execFail {
					return commandResult{}, errors.New("test-token")
				}
				if tt.fail {
					return commandResult{code: 1, stderr: "test-token (HTTP 403)"}, nil
				}
				return commandResult{stdout: upstreamResponse()}, nil
			}
			err := a.upstreamRelease()
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "test-token") {
					t.Fatalf("got %v, want %q without token output", err, tt.want)
				}
				if tt.missingToken && calls != 0 {
					t.Fatal("missing token must fail before making a request")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			output, err := os.ReadFile(env["GITHUB_OUTPUT"])
			if err != nil {
				t.Fatal(err)
			}
			want := "build-args<<TUXCRATE_OUTPUT\nUPSTREAM_PRERELEASE=false\n" +
				"UPSTREAM_URL_AMD64=https://github.com/Example/App/releases/download/v1.2.3/app.1.2.3.x64.tar.gz\nUPSTREAM_SHA256_AMD64=" + strings.Repeat("a", 64) + "\n" +
				"UPSTREAM_URL_ARM64=https://github.com/Example/App/releases/download/v1.2.3/app.1.2.3.arm64.tar.gz\nUPSTREAM_SHA256_ARM64=" + strings.Repeat("b", 64) + "\nTUXCRATE_OUTPUT\n"
			if string(output) != want {
				t.Fatalf("unexpected build arguments: %s", output)
			}
			files, err := os.ReadDir(env["RUNNER_TEMP"])
			if err != nil || len(files) != 0 {
				t.Fatal("asset resolution must not create secret files")
			}
		})
	}
}

func TestGitHubBuildArgsValidation(t *testing.T) {
	for _, tt := range []struct{ name, old, replacement, want string }{
		{"wrong tag", "v1.2.3", "v9.9.9", "invalid upstream"},
		{"draft", `"draft":false`, `"draft":true`, "invalid upstream"},
		{"missing flag", `"prerelease":false,`, "", "invalid upstream"},
		{"channel mismatch", `"prerelease":false`, `"prerelease":true`, "channel does not match"},
		{"missing asset", "app.1.2.3.arm64.tar.gz", "other.tar.gz", "expected exactly one"},
		{"bad digest", "sha256:", "sha512:", "URL or SHA-256"},
		{"short digest", "sha256:" + strings.Repeat("a", 64), "sha256:abc", "URL or SHA-256"},
		{"insecure URL", "https://github.com", "http://github.com", "URL or SHA-256"},
		{"wrong host", "https://github.com", "https://example.com", "URL or SHA-256"},
		{"credentials", "https://github.com", "https://token@github.com", "URL or SHA-256"},
		{"URL query", "https://github.com/Example/App/releases/download/v1.2.3/app.1.2.3.x64.tar.gz", "https://github.com/Example/App/releases/download/v1.2.3/app.1.2.3.x64.tar.gz?token=secret", "URL or SHA-256"},
		{"argument injection", "https://github.com", `https://github.com\nTOKEN=secret`, "URL or SHA-256"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := &releaseconfig.GitHubSource{Repository: "Example/App", Tag: "v1.2.3", Assets: map[string]string{"amd64": "app.1.2.3.x64.tar.gz", "arm64": "app.1.2.3.arm64.tar.gz"}}
			_, err := githubBuildArgs(source, strings.ReplaceAll(upstreamResponse(), tt.old, tt.replacement))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
		})
	}
	t.Run("duplicate assets", func(t *testing.T) {
		var response map[string]any
		if err := json.Unmarshal([]byte(upstreamResponse()), &response); err != nil {
			t.Fatal(err)
		}
		assets := response["assets"].([]any)
		response["assets"] = append(assets, assets[0])
		data, _ := json.Marshal(response)
		source := &releaseconfig.GitHubSource{Repository: "Example/App", Tag: "v1.2.3", Assets: map[string]string{"amd64": "app.1.2.3.x64.tar.gz"}}
		if _, err := githubBuildArgs(source, string(data)); err == nil || !strings.Contains(err.Error(), "expected exactly one") {
			t.Fatalf("duplicate assets must fail: %v", err)
		}
	})
}

func TestUpstreamReleaseSkipsOtherSources(t *testing.T) {
	for _, source := range []string{
		`version: "1.2.3"`,
		`upstream: {datasource: pypi, depName: example, version: "1.2.3"}`,
		`upstream: {datasource: deb, depName: example, version: "1.2.3-1", registryUrl: "https://example.com/?suite=stable&components=main&binaryArch=amd64"}`,
		`upstream: {datasource: github-releases, depName: Example/App, version: "1.2.3"}`,
	} {
		t.Run(source, func(t *testing.T) {
			a, env := configApp(t, "tracks:\n  - name: stable\n    "+source+"\n    tags: [\"${version}\"]\n")
			env["TRACK"] = "stable"
			a.exec = func(_ string, args ...string) (commandResult, error) {
				t.Fatalf("must not request release metadata: %v", args)
				return commandResult{}, nil
			}
			if err := a.upstreamRelease(); err != nil {
				t.Fatal(err)
			}
			output, err := os.ReadFile(env["GITHUB_OUTPUT"])
			if err != nil || string(output) != "build-args<<TUXCRATE_OUTPUT\n\nTUXCRATE_OUTPUT\n" {
				t.Fatalf("skipped sources must emit no extra build args: %s, %v", output, err)
			}
		})
	}
}
