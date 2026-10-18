package containerrelease

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	for _, tt := range []struct {
		name, data, flavor, version string
	}{
		{"alpine", "NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.24.0\n", "alpine", "3.24.0"},
		{"debian", "ID=debian\nVERSION_ID=\"13\"\nVERSION=\"13 (trixie)\"\n", "debian", "13"},
		{"other", "# comment\nID='xy'\nVERSION_ID='1.2'\n", "xy", "1.2"},
		{"missing ID", "VERSION_ID=13", "", ""},
		{"missing version", "ID=debian", "", ""},
		{"empty version", "ID=debian\nVERSION_ID=\"\"", "", ""},
		{"empty ID", "ID=\"\"\nVERSION_ID=13", "", ""},
		{"unclosed quote", "ID=debian\nVERSION_ID=\"13", "", ""},
		{"shell expression", "ID=debian\nVERSION_ID=$(touch /tmp/not-executed)", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flavor, version, err := parseOSRelease(tt.data)
			if tt.flavor == "" {
				if err == nil {
					t.Fatal("accepted missing or invalid OS identifiers")
				}
				return
			}
			if err != nil || flavor != tt.flavor || version != tt.version {
				t.Fatalf("got %q, %q, %v; want %q, %q", flavor, version, err, tt.flavor, tt.version)
			}
		})
	}
}

// Stub Docker's stopped-container file access while exercising the real parser.
func imageOSRunner(t *testing.T, image, platform string, files map[string]string, fail string, calls *[]string) commandRunner {
	t.Helper()
	return func(_ string, args ...string) (commandResult, error) {
		*calls = append(*calls, args[1])
		if args[1] == fail {
			return commandResult{code: 1, stderr: "simulated failure"}, nil
		}
		switch args[1] {
		case "create":
			want := []string{"docker", "create", "--pull=missing", "--platform", platform, "--entrypoint", "/bin/true", image}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("got %v, want %v", args, want)
			}
			return commandResult{stdout: "test-container"}, nil
		case "cp":
			if len(args) != 5 || args[2] != "--follow-link" || !strings.HasPrefix(args[3], "test-container:") {
				t.Fatal(args)
			}
			path := strings.TrimPrefix(args[3], "test-container:")
			data, ok := files[path]
			if !ok {
				return commandResult{code: 1, stderr: "file not found"}, nil
			}
			if err := os.WriteFile(args[4], []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			return commandResult{}, nil
		case "rm":
			if !reflect.DeepEqual(args, []string{"docker", "rm", "test-container"}) {
				t.Fatal(args)
			}
			return commandResult{}, nil
		default:
			t.Fatalf("unexpected command %v", args)
			return commandResult{}, nil
		}
	}
}

func TestImageOS(t *testing.T) {
	for _, tt := range []struct {
		name, data, pointVersion, flavor, version, fail string
		fallback                                        bool
	}{
		{name: "alpine", data: "ID=alpine\nVERSION_ID=3.24.0", flavor: "alpine", version: "3.24.0"},
		{name: "debian point release", data: "ID=debian\nVERSION_ID=13", pointVersion: "13.7\n", flavor: "debian", version: "13.7"},
		{name: "debian without point release", data: "ID=debian\nVERSION_ID=13", flavor: "debian", version: "13"},
		{name: "other distro", data: "ID=xy\nVERSION_ID=1.2", flavor: "xy", version: "1.2"},
		{name: "usr lib fallback", data: "ID=alpine\nVERSION_ID=3.24.0", flavor: "alpine", version: "3.24.0", fallback: true},
		{name: "missing os-release"},
		{name: "invalid os-release", data: "NAME=Unknown"},
		{name: "create failure", fail: "create"},
		{name: "copy failure", fail: "cp"},
		{name: "cleanup failure", data: "ID=alpine\nVERSION_ID=3.24.0", fail: "rm"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, env := testApp(t)
			image := env["IMAGE"] + "@" + env["IMAGE_DIGEST"]
			files := map[string]string{}
			path := "/etc/os-release"
			if tt.fallback {
				path = "/usr/lib/os-release"
			}
			if tt.data != "" {
				files[path] = tt.data
			}
			if tt.pointVersion != "" {
				files["/etc/debian_version"] = tt.pointVersion
			}
			var calls []string
			a.exec = imageOSRunner(t, image, "linux/arm64", files, tt.fail, &calls)
			flavor, version, err := a.imageOS(image, "linux/arm64")
			if tt.flavor == "" {
				if err == nil {
					t.Fatal("ignored OS metadata failure")
				}
			} else if err != nil || flavor != tt.flavor || version != tt.version {
				t.Fatalf("got %q, %q, %v; want %q, %q", flavor, version, err, tt.flavor, tt.version)
			}
			if tt.fail != "create" && calls[len(calls)-1] != "rm" {
				t.Fatal("stopped container was not cleaned up")
			}
		})
	}
}
