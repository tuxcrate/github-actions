package containerrelease

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestGitHubAPISuccess(t *testing.T) {
	const headers = "HTTP/2.0 200 OK\r\nContent-Type: application/json\r\nX-Github-Request-Id: TEST\r\n\r\n"
	for _, tt := range []struct {
		name     string
		args     []string
		response string
		want     string
	}{
		{"GET", []string{"repos/owner/repo"}, headers + `{"id":1}`, `{"id":1}`},
		{"jq", []string{"repos/owner/repo/git/ref/heads/main", "--jq", ".object.sha"}, headers + "abc123\n", "abc123"},
		{"slurp", []string{"--paginate", "--slurp", "repos/owner/repo/releases"}, "[" + headers + `[{"id":1}],` + headers + `[{"id":2}]]`, `[[{"id":1}],[{"id":2}]]`},
		{"pagination", []string{"--paginate", "repos/owner/repo/releases"}, headers + `[{"id":1}]` + "\n" + headers + `[{"id":2}]`, "[{\"id\":1}]\n[{\"id\":2}]"},
		{"POST", []string{"--method", "POST", "repos/owner/repo/releases", "--input", "-"}, "HTTP/1.1 201 Created\nLocation: https://api.github.com/releases/1\n\n{\"id\":1}", `{"id":1}`},
		{"PATCH", []string{"--method", "PATCH", "repos/owner/repo/releases/1", "--input", "-"}, headers + `{"draft":false}`, `{"draft":false}`},
		{"DELETE", []string{"--method", "DELETE", "repos/owner/repo/releases/1"}, "HTTP/2.0 204 No Content\r\nX-Github-Request-Id: TEST", ""},
		{"no headers", []string{"repos/owner/repo"}, `{"id":1}`, `{"id":1}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := app{exec: func(input string, args ...string) (commandResult, error) {
				want := append([]string{"gh", "api"}, tt.args...)
				want = append(want, "--include")
				if input != "request payload" || !reflect.DeepEqual(args, want) {
					t.Fatalf("unexpected command: %q %v", input, args)
				}
				return commandResult{stdout: tt.response}, nil
			}}
			got, err := a.githubAPI("request payload", tt.args...)
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestGitHubAPIPreservesJSONStrings(t *testing.T) {
	// A release body can contain examples of HTTP responses. JSON encoding must
	// keep those from being mistaken for actual response headers.
	body, err := json.Marshal(map[string]string{"body": "HTTP/2.0 200 OK\r\nX-Test: example\r\n\r\nresponse"})
	if err != nil {
		t.Fatal(err)
	}
	a := app{exec: func(_ string, _ ...string) (commandResult, error) {
		return commandResult{stdout: "HTTP/2.0 200 OK\n\n" + string(body)}, nil
	}}
	got, err := a.githubAPI("", "repos/owner/repo/releases")
	if err != nil || got != string(body) {
		t.Fatalf("got %q, %v; want %q", got, err, body)
	}
}

func TestGitHubAPIFailure(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PATCH", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			a := app{exec: func(_ string, _ ...string) (commandResult, error) {
				calls++
				return commandResult{code: 1, stderr: "gh: API unavailable", stdout: "HTTP/2.0 503 Service Unavailable\nX-Github-Request-Id: TEST\nRetry-After: 60\n\nupstream unavailable"}, nil
			}}
			_, err := a.githubAPI("private payload", "--method", method, "repos/owner/repo/releases")
			if err == nil {
				t.Fatal("expected failure")
			}
			for _, want := range []string{"--method " + method, "HTTP/2.0 503 Service Unavailable", "X-Github-Request-Id: TEST", "Retry-After: 60", "upstream unavailable", "gh: API unavailable"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in %v", want, err)
				}
			}
			if calls != 1 || strings.Contains(err.Error(), "private payload") {
				t.Fatal("request retried or payload logged")
			}
		})
	}
}

func TestGitHubAPIPaginatedFailure(t *testing.T) {
	a := app{exec: func(_ string, _ ...string) (commandResult, error) {
		return commandResult{code: 1, stderr: "gh: API unavailable", stdout: "[HTTP/2.0 200 OK\nX-Github-Request-Id: PAGE1\n\n" + strings.Repeat("x", 10000) + ",HTTP/2.0 503 Service Unavailable\nX-Github-Request-Id: PAGE2\n\nupstream unavailable"}, nil
	}}
	_, err := a.githubAPI("", "--paginate", "--slurp", "repos/owner/repo/releases")
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, want := range []string{"X-Github-Request-Id: PAGE1", "[truncated]", "HTTP/2.0 503 Service Unavailable", "X-Github-Request-Id: PAGE2", "upstream unavailable"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in diagnostic", want)
		}
	}
}
