package version

import (
	"fmt"
	"regexp"
	"strings"
)

var aliasRE = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)

// ImageTags returns the default immutable tag and moving aliases.
// The application version must have passed Validate.
func ImageTags(version string, revision uint64) []string {
	if strings.Contains(version, "-") {
		return []string{fmt.Sprintf("%s-r%d", version, revision), version, "preview"}
	}
	parts := strings.Split(version, ".")
	return []string{fmt.Sprintf("%s-r%d", version, revision), version, parts[0] + "." + parts[1], "latest"}
}

// ExpandAliases substitutes major and minor placeholders in configured aliases.
// The application version must have passed Validate.
func ExpandAliases(version string, aliases []string) []string {
	parts := strings.SplitN(version, ".", 3)
	replacer := strings.NewReplacer("{major}", parts[0], "{minor}", parts[1])
	expanded := make([]string, len(aliases))
	for i, alias := range aliases {
		expanded[i] = replacer.Replace(alias)
	}
	return expanded
}

// ValidImageTag reports whether tag is a valid container image tag.
func ValidImageTag(tag string) bool {
	return aliasRE.MatchString(tag)
}

// ValidateAliases rejects reserved, duplicate, or unsafe moving aliases.
func ValidateAliases(version string, aliases []string) error {
	seen := map[string]bool{version: true}
	for _, alias := range aliases {
		if !aliasRE.MatchString(alias) || versionRE.MatchString(alias) || releaseRE.MatchString("v"+alias) || seen[alias] {
			return fmt.Errorf("invalid, reserved, or duplicate alias %q", alias)
		}
		seen[alias] = true
	}
	if strings.Contains(version, "-") {
		return ValidatePrereleaseAliases(aliases)
	}
	return nil
}

// ValidatePrereleaseAliases prevents a prerelease track from owning stable aliases,
// including when upstream versions have no prerelease suffix.
func ValidatePrereleaseAliases(aliases []string) error {
	for _, alias := range aliases {
		if alias == "latest" || alias == "stable" || (len(alias) > 0 && alias[0] >= '0' && alias[0] <= '9') {
			return fmt.Errorf("prerelease cannot own stable alias %q", alias)
		}
	}
	return nil
}
