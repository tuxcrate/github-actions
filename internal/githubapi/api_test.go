package githubapi

import (
	"encoding/json"
	"errors"
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
			run := func(input string, args ...string) (string, error) {
				want := append([]string{"gh", "api"}, tt.args...)
				want = append(want, "--include")
				if input != "request payload" || !reflect.DeepEqual(args, want) {
					t.Fatalf("unexpected command: %q %v", input, args)
				}
				return tt.response, nil
			}
			got, err := Call(run, "request payload", tt.args...)
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
	run := func(_ string, _ ...string) (string, error) {
		return "HTTP/2.0 200 OK\n\n" + string(body), nil
	}
	got, err := Call(run, "", "repos/owner/repo/releases")
	if err != nil || got != string(body) {
		t.Fatalf("got %q, %v; want %q", got, err, body)
	}
}

func TestGitHubAPIFailure(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PATCH", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			failure := errors.New("HTTP/2.0 503 Service Unavailable\nX-Github-Request-Id: TEST\nRetry-After: 60\n\nupstream unavailable")
			run := func(input string, args ...string) (string, error) {
				calls++
				want := []string{"gh", "api", "--method", method, "repos/owner/repo/releases", "--include"}
				if input != "private payload" || !reflect.DeepEqual(args, want) {
					t.Fatal("unexpected command")
				}
				return "partial response", failure
			}
			got, err := Call(run, "private payload", "--method", method, "repos/owner/repo/releases")
			if got != "" || !errors.Is(err, failure) {
				t.Fatalf("got %q, %v; want original diagnostic error", got, err)
			}
			if calls != 1 || strings.Contains(err.Error(), "private payload") {
				t.Fatal("request retried or payload logged")
			}
		})
	}
}
