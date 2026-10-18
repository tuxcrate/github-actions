// Package version validates application versions and generates release tags.
package version

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// CorePattern matches the X.Y.Z portion of an application version.
const CorePattern = `(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)`
const appVersionPattern = CorePattern + `(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?`

var (
	versionRE = regexp.MustCompile(`^` + appVersionPattern + `$`)
	releaseRE = regexp.MustCompile(`^v(` + appVersionPattern + `)-r(0|[1-9][0-9]*)$`)
)

// Validate checks the application version and reserved revision namespace.
func Validate(version string) error {
	if !versionRE.MatchString(version) || len(version) > 106 {
		return errors.New("app-version must be X.Y.Z with an optional SemVer prerelease, no v prefix or build metadata, at most 106 characters")
	}
	if releaseRE.MatchString("v" + version) {
		return errors.New("app-version must not end in -rN: that namespace is reserved for image revisions")
	}
	if _, suffix, ok := strings.Cut(version, "-"); ok {
		for _, id := range strings.Split(suffix, ".") {
			if len(id) > 1 && id[0] == '0' && strings.Trim(id, "0123456789") == "" {
				return errors.New("numeric prerelease identifiers must not have leading zeros")
			}
		}
	}
	return nil
}

// NextRevision returns the first unused revision after all matching release tags.
func NextRevision(version string, tags []string) (uint64, error) {
	if err := Validate(version); err != nil {
		return 0, err
	}
	var next uint64
	for _, tag := range tags {
		match := releaseRE.FindStringSubmatch(tag)
		if match == nil || match[1] != version {
			continue
		}
		revision, err := strconv.ParseUint(match[len(match)-1], 10, 64)
		if err != nil || revision == ^uint64(0) {
			return 0, fmt.Errorf("release revision out of range: %s", tag)
		}
		if revision >= next {
			next = revision + 1
		}
	}
	return next, nil
}

// IsReleaseTag reports whether tag has the release-tag syntax.
func IsReleaseTag(tag string) bool {
	return releaseRE.MatchString(tag)
}

// ParseReleaseTag extracts the application version and numeric revision.
func ParseReleaseTag(tag string) (string, uint64, error) {
	match := releaseRE.FindStringSubmatch(tag)
	if match == nil {
		return "", 0, errors.New("invalid release tag")
	}
	revision, err := strconv.ParseUint(match[len(match)-1], 10, 64)
	return match[1], revision, err
}
