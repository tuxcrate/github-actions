package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	osFlavorRE  = regexp.MustCompile(`^[a-z0-9._-]+$`)
	osVersionRE = regexp.MustCompile(`^[a-zA-Z0-9._+-]+$`)
)

func parseOSRelease(data string) (flavor, version string, err error) {
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || (key != "ID" && key != "VERSION_ID") {
			continue
		}
		// Read these identifiers as data, never source os-release as shell code.
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if key == "ID" {
			flavor = value
		} else {
			version = value
		}
	}
	if !osFlavorRE.MatchString(flavor) || !osVersionRE.MatchString(version) {
		return "", "", errors.New("os-release requires valid ID and VERSION_ID identifiers")
	}
	return flavor, version, nil
}

func (a app) imageOS(image, platform string) (flavor, version string, err error) {
	if platform == "" {
		return "", "", errors.New("an image platform is required to read OS metadata")
	}
	dir, err := os.MkdirTemp("", "container-release-os-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(dir)
	// The digest identifies the final image, not its configured base. Creating
	// without starting also works for images without a shell or cat binary.
	container, err := a.run("", "docker", "create", "--pull=missing", "--platform", platform, "--entrypoint", "/bin/true", image)
	if err != nil {
		return "", "", err
	}
	defer func() {
		_, cleanupErr := a.run("", "docker", "rm", container)
		err = errors.Join(err, cleanupErr)
	}()
	read := func(path string) (string, error) {
		dest := filepath.Join(dir, filepath.Base(path))
		// /etc/os-release may be a symlink to /usr/lib/os-release.
		if _, err := a.run("", "docker", "cp", "--follow-link", container+":"+path, dest); err != nil {
			return "", err
		}
		data, err := os.ReadFile(dest)
		return strings.TrimSpace(string(data)), err
	}
	data, err := read("/etc/os-release")
	if err != nil {
		data, err = read("/usr/lib/os-release")
	}
	if err != nil {
		return "", "", fmt.Errorf("read image os-release: %w", err)
	}
	flavor, version, err = parseOSRelease(data)
	if err != nil {
		return "", "", err
	}
	// Debian's VERSION_ID contains only the major version. The installed
	// base-files package records the point release in /etc/debian_version.
	if flavor == "debian" {
		if pointVersion, readErr := read("/etc/debian_version"); readErr == nil && osVersionRE.MatchString(pointVersion) {
			version = pointVersion
		}
	}
	return flavor, version, nil
}
