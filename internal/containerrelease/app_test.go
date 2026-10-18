package containerrelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecute(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"no command", nil, "usage: container-release"},
		{"unknown command", []string{"unknown"}, "usage: container-release"},
		{"extra argument", []string{"metadata", "extra"}, "usage: container-release"},
		{"metadata", []string{"metadata"}, "app-version must be"},
		{"reserve", []string{"reserve"}, "invalid release track name"},
		{"plan", []string{"plan"}, "configuration requires 1–256 tracks"},
		{"manifest", []string{"manifest"}, "app-version must be"},
		{"check-manifest", []string{"check-manifest"}, "a local image is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, env := configApp(t, "tracks: []\n")
			env["APP_VERSION"] = "invalid"
			env["TRACK"] = "INVALID"
			a.exec = func(_ string, args ...string) (commandResult, error) {
				t.Fatalf("unexpected external command: %v", args)
				return commandResult{}, nil
			}
			if err := a.execute(tt.args); err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Fatalf("got %v; want error starting with %q", err, tt.want)
			}
		})
	}
}

func TestRunMetadata(t *testing.T) {
	output := filepath.Join(t.TempDir(), "output")
	for key, value := range map[string]string{
		"APP_VERSION":       "1.2.3",
		"IMAGE":             "",
		"GITHUB_REPOSITORY": "Tuxcrate/Zrepl",
		"GITHUB_OUTPUT":     output,
	} {
		t.Setenv(key, value)
	}
	if err := Run([]string{"metadata"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if want := "image<<TUXCRATE_OUTPUT\nghcr.io/tuxcrate/zrepl\nTUXCRATE_OUTPUT\n"; string(data) != want {
		t.Fatalf("got %q; want %q", data, want)
	}
}
