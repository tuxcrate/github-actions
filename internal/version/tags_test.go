package version

import (
	"reflect"
	"testing"
)

func TestExpandAliases(t *testing.T) {
	aliases := []string{"{major}", "{major}.{minor}", "latest"}
	want := []string{"12", "12.34", "latest"}
	if got := ExpandAliases("12.34.56", aliases); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
	if aliases[0] != "{major}" {
		t.Fatal("changed input aliases")
	}
}

func TestValidateAliases(t *testing.T) {
	for _, tt := range []struct {
		version string
		aliases []string
		valid   bool
	}{
		{"1.2.3", []string{"1", "1.2", "latest", "stable"}, true},
		{"2.0.0-rc.1", []string{"preview"}, true},
		{"1.2.3", nil, true},
		{"1.2.3", []string{"latest", "latest"}, false},
		{"1.2.3", []string{"1.2.3"}, false},
		{"1.2.3", []string{"2.0.0"}, false},
		{"1.2.3", []string{"1.2.3-r0"}, false},
		{"1.2.3", []string{""}, false},
		{"1.2.3", []string{"bad tag"}, false},
		{"2.0.0-rc.1", []string{"latest"}, false},
		{"2.0.0-rc.1", []string{"stable"}, false},
		{"2.0.0-rc.1", []string{"2.0"}, false},
	} {
		if err := ValidateAliases(tt.version, tt.aliases); (err == nil) != tt.valid {
			t.Errorf("ValidateAliases(%q, %v) = %v; valid=%v", tt.version, tt.aliases, err, tt.valid)
		}
	}
}

func TestPrereleaseImageTags(t *testing.T) {
	want := []string{"2.0.0-rc.1-r3", "2.0.0-rc.1", "preview"}
	if got := ImageTags("2.0.0-rc.1", 3); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}
