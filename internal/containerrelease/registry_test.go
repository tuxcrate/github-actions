package containerrelease

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type registryTransport func(*http.Request) (*http.Response, error)

func (f registryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestAuthenticatedGHCRTagAvailability(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		tokenCode int
		tokenBody string
		manifest  int
		wantError bool
	}{
		{"new image", 200, `{"token":"pull-token"}`, 404, false},
		{"existing tag", 200, `{"token":"pull-token"}`, 200, true},
		{"access token", 200, `{"access_token":"pull-token"}`, 404, false},
		{"token unauthorized", 401, "secret", 0, true},
		{"token forbidden", 403, "secret", 0, true},
		{"token not found", 404, "secret", 0, true},
		{"invalid token JSON", 200, "secret", 0, true},
		{"empty token", 200, `{}`, 0, true},
		{"manifest unauthorized", 200, `{"token":"pull-token"}`, 401, true},
		{"manifest forbidden", 200, `{"token":"pull-token"}`, 403, true},
		{"registry failure", 200, `{"token":"pull-token"}`, 500, true},
		{"redirect", 302, "secret", 0, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			calls := 0
			transport := registryTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.Scheme != "https" || request.URL.Host != "ghcr.io" {
					t.Fatal(request.URL)
				}
				code, body := scenario.tokenCode, scenario.tokenBody
				if calls == 1 {
					actor, password, ok := request.BasicAuth()
					if !ok || actor != "actor" || password != "secret" || request.Method != http.MethodGet || request.URL.Path != "/token" || request.URL.Query().Get("scope") != "repository:tuxcrate/lidarr:pull" || request.URL.Query().Get("service") != "ghcr.io" {
						t.Fatal("incorrect authenticated pull-only token request")
					}
				} else {
					if calls != 2 || request.Method != http.MethodHead || request.URL.Path != "/v2/tuxcrate/lidarr/manifests/3.2.0-r0" || request.Header.Get("Authorization") != "Bearer pull-token" || request.Header.Get("Accept") == "" {
						t.Fatal("incorrect manifest request")
					}
					code, body = scenario.manifest, ""
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Location": {"https://example.invalid/"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			err := ghcrTagAvailable("ghcr.io/tuxcrate/lidarr", "3.2.0-r0", "actor", "secret", transport)
			if (err != nil) != scenario.wantError {
				t.Fatal(err)
			}
			if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "pull-token")) {
				t.Fatal("credentials leaked into diagnostics")
			}
		})
	}
}

func TestGHCRTagCheckTransportFailure(t *testing.T) {
	transport := registryTransport(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})
	if err := ghcrTagAvailable("ghcr.io/tuxcrate/lidarr", "3.2.0-r0", "actor", "secret", transport); err == nil {
		t.Fatal("network failure accepted")
	}
}

func TestGHCRTagCheckRequiresActor(t *testing.T) {
	a, env := testApp(t)
	env["GH_TOKEN"] = "secret"
	a.exec = func(_ string, _ ...string) (commandResult, error) {
		t.Fatal("authenticated GHCR checks must not invoke Docker")
		return commandResult{}, nil
	}
	if err := a.assertTagAvailable("ghcr.io/tuxcrate/lidarr", "3.2.0-r0"); err == nil {
		t.Fatal("missing actor accepted")
	}
}
