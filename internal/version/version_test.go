package version

import (
	"strings"
	"testing"
)

func TestValidateVersion(t *testing.T) {
	for _, version := range []string{"0.7.0", "1.0.0", "13.20.102", "0.7.0-rc1", "1.2.3-rc.1", "1.2.3-0", "4.0.20.3014", "5.0.0.2668", "1.2.3.0", "1.2.3.4-rc.1"} {
		if err := Validate(version); err != nil {
			t.Error(err)
		}
	}
	for _, version := range []string{"v0.7.0", "0.7", "01.7.0", "0.7.0-rc.01", "1.2.3+build", "1.2.3-r0", "1.2.3-rc.1-r3", "1.2.3-", "1.2.3-a..b", "0.7.0\n", "$(id)", "1.2.3-" + strings.Repeat("a", 101), "1.2.3.04", "1.2.3.4.5", "1.2.3.4-r0", "1.2.3.4-rc.01", "1.2.3.4+build"} {
		if Validate(version) == nil {
			t.Errorf("accepted invalid version %q", version)
		}
	}
}

func TestNextRevision(t *testing.T) {
	for _, tt := range []struct {
		name, version string
		tags          []string
		want          uint64
	}{
		{"first", "0.7.0", nil, 0},
		{"highest", "0.7.0", []string{"v0.7.0-r0", "v0.7.0-r12"}, 13},
		{"reset", "0.7.1", []string{"v0.7.0-r12"}, 0},
		{"four-part", "4.0.20.3014", []string{"v4.0.20.3014-r0", "v4.0.20.3014-r12", "v4.0.20.3013-r20"}, 13},
		{"unrelated", "0.7.0", []string{"v0.7.0-r01", "v0.7.0-r1-extra", "v0.7.0"}, 0},
		{"prerelease", "0.8.0-rc.1", []string{"v0.8.0-rc.1-r2", "v0.8.0-rc.2-r8", "v0.8.0-r9"}, 3},
		{"suffixed namespace starts fresh", "5.0.0.2668-prerelease", []string{"v5.0.0.2668-r9"}, 0},
		{"suffixed namespace includes migrated tags", "5.0.0.2668-prerelease", []string{"v5.0.0.2668-r99", "v5.0.0.2668-prerelease-r2"}, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextRevision(tt.version, tt.tags)
			if err != nil || got != tt.want {
				t.Fatalf("got %d, %v; want %d", got, err, tt.want)
			}
		})
	}
	for _, tag := range []string{"v0.7.0-r18446744073709551615", "v0.7.0-r18446744073709551616"} {
		if _, err := NextRevision("0.7.0", []string{tag}); err == nil {
			t.Error("revision overflow accepted")
		}
	}
	if _, err := NextRevision("invalid", nil); err == nil {
		t.Error("invalid version accepted")
	}
}

func TestParseReleaseTag(t *testing.T) {
	for _, tt := range []struct {
		tag, version string
		revision     uint64
		validSyntax  bool
		wantError    bool
	}{
		{"v1.2.3-r0", "1.2.3", 0, true, false},
		{"v4.0.20.3014-r7", "4.0.20.3014", 7, true, false},
		{"v5.0.0.2668-rc.1-r3", "5.0.0.2668-rc.1", 3, true, false},
		{"v2.0.0-rc.1-r12", "2.0.0-rc.1", 12, true, false},
		{"v1.2.3-r18446744073709551615", "1.2.3", ^uint64(0), true, false},
		{"v1.2.3-r18446744073709551616", "1.2.3", ^uint64(0), true, true},
		{"v1.2.3-r01", "", 0, false, true},
		{"1.2.3-r0", "", 0, false, true},
		{"v1.2.3", "", 0, false, true},
		{"", "", 0, false, true},
	} {
		t.Run(tt.tag, func(t *testing.T) {
			if got := IsReleaseTag(tt.tag); got != tt.validSyntax {
				t.Fatalf("IsReleaseTag = %v; want %v", got, tt.validSyntax)
			}
			version, revision, err := ParseReleaseTag(tt.tag)
			if (err != nil) != tt.wantError || version != tt.version || revision != tt.revision {
				t.Fatalf("got %q, %d, %v; want %q, %d, error=%v", version, revision, err, tt.version, tt.revision, tt.wantError)
			}
		})
	}
}
