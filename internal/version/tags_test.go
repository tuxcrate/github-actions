package version

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolveTags(t *testing.T) {
	for _, tt := range []struct {
		app, image      string
		templates, want []string
		prerelease      bool
	}{
		{"12.34.56", "12.34.56", []string{"${version}", "${major}", "${major}.${minor}", "latest"}, []string{"12.34.56", "12", "12.34", "latest"}, false},
		{"5.0.0.2668", "5.0.0.2668-prerelease", []string{"${major}-prerelease", "${version}-prerelease", "${major}.${minor}-prerelease"}, []string{"5-prerelease", "5.0.0.2668-prerelease", "5.0-prerelease"}, true},
		{"2.0.0-rc.1", "2.0.0-rc.1", []string{"${version}", "preview"}, []string{"2.0.0-rc.1", "preview"}, false},
	} {
		original := append([]string(nil), tt.templates...)
		image, tags, err := ResolveTags(tt.app, tt.templates, tt.prerelease)
		if err != nil || image != tt.image || !reflect.DeepEqual(tags, tt.want) {
			t.Fatalf("%s: %s, %v: %v", tt.app, image, tags, err)
		}
		if !reflect.DeepEqual(original, tt.templates) {
			t.Fatal("changed input templates")
		}
	}
}

func TestRejectInvalidTagTemplates(t *testing.T) {
	for _, tags := range [][]string{
		nil, {}, {"latest"}, {"1.2.3"}, {"${version}", "${version}-preview"},
		{"${version}-${version}"}, {"v${version}"}, {"${version}", "${patch}"},
		{"${version}", "{major}"}, {"${version}", "${revision}"},
		{"${version}-r0"}, {"${version}+preview"}, {"${version}-01"},
		{"${version}-" + strings.Repeat("a", 101)},
		{"${version}", "${major}.${minor}", "1.2"},
		{"${version}", "2.0.0"}, {"${version}", "1.2.3-r0"},
		{"${version}", "latest", "latest"}, {"${version}", ""},
		{"${version}", "bad tag"},
	} {
		if _, _, err := ResolveTags("1.2.3", tags, false); err == nil {
			t.Errorf("accepted %v", tags)
		}
	}
	for _, tag := range []string{"latest", "stable", "5", "5.0", "5.0.0.2668"} {
		if _, _, err := ResolveTags("5.0.0.2668", []string{"${version}-prerelease", tag}, true); err == nil {
			t.Errorf("accepted prerelease tag %s", tag)
		}
	}
	if _, _, err := ResolveTags("5.0.0.2668", []string{"${version}"}, true); err == nil {
		t.Fatal("accepted numeric prerelease image version")
	}
	if err := ValidateTags("1.2.3", []string{"latest"}, false); err == nil {
		t.Fatal("accepted missing version tag")
	}
}
