package containerrelease

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tuxcrate/github-actions/internal/version"
)

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func (a app) trackName() string {
	if name := a.getenv("TRACK"); name != "" {
		return name
	}
	if a.isPrerelease() {
		return "preview"
	}
	return "stable"
}

func (a app) isPrerelease() bool {
	return a.getenv("PRERELEASE") == "true" || strings.Contains(a.getenv("APP_VERSION"), "-") || strings.Contains(a.getenv("IMAGE_VERSION"), "-")
}

func (a app) tags(imageVersion string, revision uint64) ([]string, error) {
	var tags []string
	if err := json.Unmarshal([]byte(a.getenv("TAGS")), &tags); err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}
	if err := version.ValidateTags(imageVersion, tags, a.isPrerelease()); err != nil {
		return nil, err
	}
	return append([]string{fmt.Sprintf("%s-r%d", imageVersion, revision)}, tags...), nil
}
