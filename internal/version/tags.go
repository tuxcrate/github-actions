package version

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	aliasRE         = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)
	numericSeriesRE = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
)

// ResolveTags expands explicit tag templates and identifies the single version
// tag from which immutable image and Git release revisions are derived.
func ResolveTags(appVersion string, templates []string, prerelease bool) (string, []string, error) {
	if err := Validate(appVersion); err != nil {
		return "", nil, err
	}
	parts := strings.SplitN(appVersion, ".", 3)
	replacer := strings.NewReplacer("${version}", appVersion, "${major}", parts[0], "${minor}", parts[1])
	tags := make([]string, len(templates))
	imageVersion := ""
	for i, template := range templates {
		tags[i] = replacer.Replace(template)
		if strings.Contains(template, "${version}") {
			if imageVersion != "" || strings.Count(template, "${version}") != 1 ||
				(template != "${version}" && !strings.HasPrefix(template, "${version}-")) {
				return "", nil, fmt.Errorf("tags require exactly one ${version} template, optionally followed by a suffix")
			}
			imageVersion = tags[i]
		}
	}
	if imageVersion == "" {
		return "", nil, fmt.Errorf("tags require exactly one template containing ${version}")
	}
	if err := ValidateImageVersion(appVersion, imageVersion); err != nil {
		return "", nil, err
	}
	if err := ValidateTags(imageVersion, tags, prerelease || strings.Contains(appVersion, "-")); err != nil {
		return "", nil, err
	}
	return imageVersion, tags, nil
}

// ValidateImageVersion keeps the installation version separate from its optional
// packaging suffix without allowing unrelated application versions to be tagged.
func ValidateImageVersion(appVersion, imageVersion string) error {
	if err := Validate(appVersion); err != nil {
		return err
	}
	if err := Validate(imageVersion); err != nil {
		return fmt.Errorf("invalid image version: %w", err)
	}
	if imageVersion != appVersion && !strings.HasPrefix(imageVersion, appVersion+"-") {
		return fmt.Errorf("image version must be the application version with an optional suffix")
	}
	return nil
}

// ValidImageTag reports whether tag is a valid container image tag.
func ValidImageTag(tag string) bool {
	return aliasRE.MatchString(tag)
}

// ValidateTags validates expanded tags, including the required image version.
// Full versions other than imageVersion and revision-shaped tags are reserved.
func ValidateTags(imageVersion string, tags []string, prerelease bool) error {
	if err := Validate(imageVersion); err != nil {
		return fmt.Errorf("invalid image version: %w", err)
	}
	prerelease = prerelease || strings.Contains(imageVersion, "-")
	if prerelease && !strings.Contains(imageVersion, "-") {
		return fmt.Errorf("prerelease image version must have a suffix")
	}
	seen := map[string]bool{}
	for _, tag := range tags {
		if !ValidImageTag(tag) || seen[tag] || strings.HasPrefix(tag, "pr-") || releaseRE.MatchString("v"+tag) ||
			(versionRE.MatchString(tag) && tag != imageVersion) {
			return fmt.Errorf("invalid, reserved, or duplicate tag %q", tag)
		}
		if prerelease && (tag == "latest" || tag == "stable" || numericSeriesRE.MatchString(tag)) {
			return fmt.Errorf("prerelease cannot own stable tag %q", tag)
		}
		seen[tag] = true
	}
	if !seen[imageVersion] {
		return fmt.Errorf("tags must include image version %q", imageVersion)
	}
	return nil
}
