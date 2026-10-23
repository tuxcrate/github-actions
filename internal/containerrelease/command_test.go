package containerrelease

import (
	"errors"
	"strings"
	"testing"

	"github.com/tuxcrate/github-actions/internal/githubapi"
)

func TestRunDiagnostics(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result commandResult
		err    error
		want   []string
	}{
		{"stderr only", commandResult{code: 1, stderr: "unexpected end of JSON input"}, nil, []string{"command failed (1): gh api", "stderr:\nunexpected end of JSON input"}},
		{"HTTP response", commandResult{code: 1, stdout: "HTTP/2.0 403 Forbidden\nX-Ratelimit-Remaining: 0\n\n{\"message\":\"rate limit exceeded\"}", stderr: "gh: forbidden"}, nil, []string{"stderr:\ngh: forbidden", "stdout:\nHTTP/2.0 403 Forbidden", "X-Ratelimit-Remaining: 0", "rate limit exceeded"}},
		{"empty response", commandResult{code: 1}, nil, []string{"stderr:\n[empty]"}},
		{"large response", commandResult{code: 1, stdout: strings.Repeat("x", 9000), stderr: strings.Repeat("y", 9000)}, nil, []string{"[truncated]"}},
		{"execution failure", commandResult{}, errors.New("executable not found"), []string{"run gh api: executable not found"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := app{exec: func(input string, args ...string) (commandResult, error) {
				if input != "private request payload" {
					t.Fatal("request payload not passed to command")
				}
				return tt.result, tt.err
			}}
			_, err := a.run("private request payload", "gh", "api")
			if err == nil {
				t.Fatal("expected failure")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in %v", want, err)
				}
			}
			for _, absent := range []string{"private request payload", strings.Repeat("x", 8193), strings.Repeat("y", 8193)} {
				if strings.Contains(err.Error(), absent) {
					t.Error("diagnostics included request payload or excessive output")
				}
			}
		})
	}
}

func TestRunSuccess(t *testing.T) {
	a := app{exec: func(_ string, _ ...string) (commandResult, error) {
		return commandResult{stdout: "response", stderr: "warning"}, nil
	}}
	got, err := a.run("", "gh", "api")
	if err != nil || got != "response" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestGitHubAPIPaginatedFailure(t *testing.T) {
	a := app{exec: func(_ string, _ ...string) (commandResult, error) {
		return commandResult{code: 1, stderr: "gh: API unavailable", stdout: "[HTTP/2.0 200 OK\nX-Github-Request-Id: PAGE1\n\n" + strings.Repeat("x", 10000) + ",HTTP/2.0 503 Service Unavailable\nX-Github-Request-Id: PAGE2\n\nupstream unavailable"}, nil
	}}
	_, err := githubapi.Call(a.run, "", "--paginate", "--slurp", "repos/owner/repo/releases")
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, want := range []string{"X-Github-Request-Id: PAGE1", "[truncated]", "HTTP/2.0 503 Service Unavailable", "X-Github-Request-Id: PAGE2", "upstream unavailable"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in diagnostic", want)
		}
	}
}
