package containerrelease

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestUpstreamRelease(t *testing.T) {
	const config = `tracks:
  - name: stable
    upstream:
      datasource: github-releases
      depName: Example/App
      version: "1.2.3"
    tags: ["${version}"]
`
	for _, tt := range []struct {
		name, response, stderr, want string
		code                         int
		bare, missingToken, execFail bool
	}{
		{name: "v tag", response: `{"tag_name":"v1.2.3","draft":false,"prerelease":false,"assets":[{"digest":"sha256:abc"}]}`},
		{name: "bare tag", bare: true, response: `{"tag_name":"1.2.3","draft":false,"prerelease":true}`},
		{name: "unusual tag fallback", code: 1, stderr: "Not Found (HTTP 404)"},
		{name: "rate limit", code: 1, stderr: "rate limited (HTTP 403)", want: "failed (exit 1)"},
		{name: "unauthorized", code: 1, stderr: "bad credentials: test-token (HTTP 401)", want: "failed (exit 1)"},
		{name: "network error", execFail: true, want: "fetch upstream release metadata"},
		{name: "no token", missingToken: true, want: "requires GH_TOKEN"},
		{name: "invalid json", response: "oops", want: "invalid upstream"},
		{name: "wrong tag", response: `{"tag_name":"v9.9.9","draft":false,"prerelease":false}`, want: "invalid upstream"},
		{name: "draft", response: `{"tag_name":"v1.2.3","draft":true,"prerelease":false}`, want: "invalid upstream"},
		{name: "missing flags", response: `{"tag_name":"v1.2.3"}`, want: "invalid upstream"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, env := configApp(t, config)
			env["TRACK"], env["GH_TOKEN"], env["RUNNER_TEMP"] = "stable", "test-token", t.TempDir()
			if tt.missingToken {
				delete(env, "GH_TOKEN")
			}
			calls := 0
			a.exec = func(input string, args ...string) (commandResult, error) {
				calls++
				tag := "v1.2.3"
				if calls == 2 {
					tag = "1.2.3"
				}
				want := []string{"gh", "api", "--hostname", "github.com", "repos/Example/App/releases/tags/" + tag}
				if calls > 2 || input != "" || !reflect.DeepEqual(args, want) {
					t.Fatalf("unexpected authenticated request: %v", args)
				}
				if tt.execFail {
					return commandResult{}, errors.New("network error")
				}
				if tt.bare && calls == 1 {
					return commandResult{code: 1, stderr: "Not Found (HTTP 404)"}, nil
				}
				return commandResult{stdout: tt.response, stderr: tt.stderr, code: tt.code}, nil
			}
			err := a.upstreamRelease()
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("got %v, want %q", err, tt.want)
				}
				if strings.Contains(err.Error(), "test-token") {
					t.Fatal("authenticated errors must not expose token output")
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
			files, err := os.ReadDir(env["RUNNER_TEMP"])
			if err != nil {
				t.Fatal(err)
			}
			if tt.code != 0 {
				if len(files) != 0 || string(output) != "secret-files<<TUXCRATE_OUTPUT\n\nTUXCRATE_OUTPUT\n" {
					t.Fatalf("fallback must not pass a secret file: %s", output)
				}
				return
			}
			if len(files) != 1 {
				t.Fatalf("expected one metadata file, got %d", len(files))
			}
			path := env["RUNNER_TEMP"] + "/" + files[0].Name()
			data, err := os.ReadFile(path)
			if err != nil || string(data) != tt.response {
				t.Fatalf("metadata must retain asset digests: %s, %v", data, err)
			}
			if string(output) != "secret-files<<TUXCRATE_OUTPUT\ngithub_release_json="+path+"\nTUXCRATE_OUTPUT\n" {
				t.Fatalf("unexpected secret-file output: %s", output)
			}
		})
	}
}

func TestUpstreamReleaseSkipsOtherSources(t *testing.T) {
	for _, source := range []string{
		`version: "1.2.3"`,
		`upstream: {datasource: pypi, depName: example, version: "1.2.3"}`,
		`upstream: {datasource: deb, depName: example, version: "1.2.3-1", registryUrl: "https://example.com/?suite=stable&components=main&binaryArch=amd64"}`,
		`upstream: {datasource: github-releases, depName: Example/App, version: "1.2.3", registryUrl: "https://github.example.com"}`,
	} {
		t.Run(source, func(t *testing.T) {
			a, env := configApp(t, "tracks:\n  - name: stable\n    "+source+"\n    tags: [\"${version}\"]\n")
			env["TRACK"], env["RUNNER_TEMP"] = "stable", t.TempDir()
			a.exec = func(_ string, args ...string) (commandResult, error) {
				t.Fatalf("must not request release metadata for this source: %v", args)
				return commandResult{}, nil
			}
			if err := a.upstreamRelease(); err != nil {
				t.Fatal(err)
			}
			files, err := os.ReadDir(env["RUNNER_TEMP"])
			if err != nil || len(files) != 0 {
				t.Fatalf("skipped sources must not create metadata files: %v, %v", files, err)
			}
		})
	}
}
