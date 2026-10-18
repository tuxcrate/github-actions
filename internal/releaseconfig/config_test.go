package releaseconfig

import (
	"strings"
	"testing"
)

func TestUpstreamMetadata(t *testing.T) {
	registry := "https://zrepl.cschwarz.com/apt/debian?suite=trixie&components=main&binaryArch=amd64"
	config := `{"tracks":[{"name":"stable","upstream":{"datasource":"deb","depName":"zrepl","registryUrl":"` + registry + `","version":"0.7.0-1"},"tags":["${version}","${major}.${minor}","latest"]}]}`
	builds, err := Plan(strings.NewReader(config), "tuxcrate/zrepl")
	if err != nil || builds[0].Version != "0.7.0" || builds[0].ImageVersion != "0.7.0" || builds[0].BuildArgs != "APP_VERSION=0.7.0-1" || builds[0].Tags != `["0.7.0","0.7","latest"]` || builds[0].Platforms != "linux/amd64,linux/arm64" {
		t.Fatalf("%v: %v", builds, err)
	}
	valid := upstream{Datasource: "deb", DepName: "zrepl", RegistryURL: registry, Version: "0.7.0-1"}
	for _, mutate := range []func(*upstream){
		func(u *upstream) { u.Datasource = "" },
		func(u *upstream) { u.DepName = "bad package" },
		func(u *upstream) { u.RegistryURL = "file:///etc/passwd" },
		func(u *upstream) { u.RegistryURL = "https://example.com/apt" },
		func(u *upstream) { u.RegistryURL = strings.ReplaceAll(registry, "binaryArch=amd64", "binaryArch=") },
		func(u *upstream) { u.Version = "" },
		func(u *upstream) { u.Version = "0.8.0-rc.1-1" },
		func(u *upstream) { u.Version = "1:0.8.0-1" },
		func(u *upstream) { u.Version = "0.8.0-1~deb13" },
	} {
		u := valid
		mutate(&u)
		if u.validate() == nil {
			t.Errorf("accepted invalid upstream: %+v", u)
		}
	}
	for _, conflicting := range []string{
		strings.Replace(config, `"name":"stable"`, `"name":"stable","version":"0.7.0"`, 1),
		strings.Replace(config, `"name":"stable"`, `"name":"stable","buildArgs":{"APP_VERSION":"other"}`, 1),
	} {
		if _, err := Plan(strings.NewReader(conflicting), "tuxcrate/zrepl"); err == nil {
			t.Fatal("accepted competing track package pin")
		}
	}
	for _, version := range []string{"0.7.0-2", "0.8.0-1"} {
		builds, err := Plan(strings.NewReader(strings.ReplaceAll(config, "0.7.0-1", version)), "tuxcrate/zrepl")
		appVersion, _, _ := strings.Cut(version, "-")
		if err != nil || builds[0].Version != appVersion || builds[0].BuildArgs != "APP_VERSION="+version {
			t.Fatalf("%s: %v: %v", version, builds, err)
		}
	}
	if _, err := decodeConfig(strings.NewReader(`{"tracks":[{"upstream":{"datasource":"deb","typo":true}}]}`)); err == nil {
		t.Fatal("accepted unknown upstream field")
	}
	if _, err := decodeConfig(strings.NewReader(strings.Replace(config, `"version":"0.7.0-1"`, `"version":"0.7.0-1","buildArg":"CUSTOM_VERSION"`, 1))); err == nil {
		t.Fatal("accepted removed upstream buildArg field")
	}
}

func TestPyPIUpstreamMetadata(t *testing.T) {
	config := `{"tracks":[{"name":"stable","upstream":{"datasource":"pypi","depName":"gallery-dl","version":"1.32.16"},"tags":["${version}","${major}","${major}.${minor}","latest"]}]}`
	builds, err := Plan(strings.NewReader(config), "tuxcrate/zrepl")
	if err != nil || len(builds) != 1 || builds[0].Version != "1.32.16" || builds[0].BuildArgs != "APP_VERSION=1.32.16" || builds[0].Tags != `["1.32.16","1","1.32","latest"]` {
		t.Fatalf("%v: %v", builds, err)
	}
	valid := upstream{Datasource: "pypi", DepName: "gallery-dl", Version: "1.32.16"}
	for _, name := range []string{"gallery-dl", "Some_Package.name", "a"} {
		u := valid
		u.DepName = name
		if err := u.validate(); err != nil {
			t.Fatalf("%+v: %v", u, err)
		}
	}
	for _, registry := range []string{"", "https://pypi.org/simple", "https://example.com/simple/"} {
		u := valid
		u.RegistryURL = registry
		if err := u.validate(); err != nil {
			t.Fatalf("%+v: %v", u, err)
		}
	}
	for _, mutate := range []func(*upstream){
		func(u *upstream) { u.DepName = "" },
		func(u *upstream) { u.DepName = "bad package" },
		func(u *upstream) { u.DepName = "-package" },
		func(u *upstream) { u.DepName = "package." },
		func(u *upstream) { u.DepName = "package[extra]" },
		func(u *upstream) { u.Version = "" },
		func(u *upstream) { u.Version = "1.32" },
		func(u *upstream) { u.Version = "01.32.16" },
		func(u *upstream) { u.Version = "1.32.16rc1" },
		func(u *upstream) { u.Version = "1.32.16-rc.1" },
		func(u *upstream) { u.Version = "1.32.16.post1" },
		func(u *upstream) { u.Version = "1!1.32.16" },
		func(u *upstream) { u.Version = "1.32.16+local" },
		func(u *upstream) { u.RegistryURL = "file:///etc/passwd" },
		func(u *upstream) { u.RegistryURL = "https:///simple" },
		func(u *upstream) { u.RegistryURL = "https://user:secret@example.com/simple" },
		func(u *upstream) { u.RegistryURL = "https://example.com/simple#fragment" },
	} {
		u := valid
		mutate(&u)
		if u.validate() == nil {
			t.Errorf("accepted invalid PyPI upstream: %+v", u)
		}
	}
	for _, conflicting := range []string{
		strings.Replace(config, `"name":"stable"`, `"name":"stable","version":"1.32.16"`, 1),
		strings.Replace(config, `"name":"stable"`, `"name":"stable","buildArgs":{"APP_VERSION":"other"}`, 1),
	} {
		if _, err := Plan(strings.NewReader(conflicting), "tuxcrate/zrepl"); err == nil {
			t.Fatal("accepted competing PyPI package pin")
		}
	}
	builds, err = Plan(strings.NewReader(strings.ReplaceAll(config, "1.32.16", "1.33.0")), "tuxcrate/zrepl")
	if err != nil || builds[0].Version != "1.33.0" || builds[0].BuildArgs != "APP_VERSION=1.33.0" || builds[0].Tags != `["1.33.0","1","1.33","latest"]` {
		t.Fatalf("%v: %v", builds, err)
	}
}

func TestDynamicUpstreamMetadata(t *testing.T) {
	for _, source := range []struct{ datasource, depName string }{
		{"github-releases", "example/app"},
		{"npm", "@example/app"},
		{"crate", "example-app"},
		{"future-datasource", "example/app"},
	} {
		for _, version := range []string{"1.2.3", "2.0.0-rc.1"} {
			t.Run(source.datasource+"/"+version, func(t *testing.T) {
				config := `{"tracks":[{"name":"test","upstream":{"datasource":"` + source.datasource + `","depName":"` + source.depName + `","version":"` + version + `"},"tags":["${version}"]}]}`
				builds, err := Plan(strings.NewReader(config), "tuxcrate/zrepl")
				if err != nil || len(builds) != 1 || builds[0].Version != version || builds[0].BuildArgs != "APP_VERSION="+version {
					t.Fatalf("%v: %v", builds, err)
				}
			})
		}
	}
	valid := upstream{Datasource: "github-releases", DepName: "example/app", Version: "1.2.3"}
	for _, registry := range []string{"", "https://github.example.com", "https://example.com/releases?channel=stable"} {
		u := valid
		u.RegistryURL = registry
		if err := u.validate(); err != nil {
			t.Fatalf("%+v: %v", u, err)
		}
	}
	for _, mutate := range []func(*upstream){
		func(u *upstream) { u.Datasource = "" },
		func(u *upstream) { u.Datasource = "bad source" },
		func(u *upstream) { u.DepName = "" },
		func(u *upstream) { u.DepName = "example/app\n" },
		func(u *upstream) { u.Version = "" },
		func(u *upstream) { u.Version = "1.2" },
		func(u *upstream) { u.Version = "1.2.3\nARG=other" },
		func(u *upstream) { u.RegistryURL = "file:///etc/passwd" },
		func(u *upstream) { u.RegistryURL = "https:///releases" },
		func(u *upstream) { u.RegistryURL = "https://user:secret@example.com" },
		func(u *upstream) { u.RegistryURL = "https://example.com/#fragment" },
	} {
		u := valid
		mutate(&u)
		if err := u.validate(); err == nil {
			t.Errorf("accepted invalid upstream: %+v", u)
		}
	}
}

func TestValidateBaseImage(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("a", 64)
	for _, image := range []string{"", "debian:13-slim" + digest, "docker.io/library/debian:13.7-slim" + digest, "registry.example.com:5000/team/base:1.2.3" + digest} {
		if err := validateBaseImage(image); err != nil {
			t.Errorf("rejected %q: %v", image, err)
		}
	}
	for _, image := range []string{"13.7-slim" + digest, "debian:13-slim", "debian" + digest, "debian:13-slim@sha256:bad", "debian:13-slim@sha256:" + strings.Repeat("A", 64), "https://docker.io/debian:13" + digest, "debian:13-slim" + digest + "\n", "debian:13 slim" + digest, ":13" + digest, "debian:" + digest} {
		if validateBaseImage(image) == nil {
			t.Errorf("accepted invalid base image %q", image)
		}
	}
}

func TestStrictYAMLConfiguration(t *testing.T) {
	for _, config := range []string{
		"baseImage: debian:13-slim\ntracks: []\n",
		"tracks: []\ntracks: []\n",
		"tracks: []\n---\ntracks: []\n",
		"tracks:\n  - name: stable\n    version: '1.2.3'\n    tags: ['${version}']\n    unknownField: true\n",
		"tracks:\n  - name: stable\n    version: '1.2.3'\n    aliases: []\n",
	} {
		if _, err := decodeConfig(strings.NewReader(config)); err == nil {
			t.Errorf("accepted invalid YAML configuration %q", config)
		}
	}
}
