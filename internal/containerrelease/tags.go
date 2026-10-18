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
	if strings.Contains(a.getenv("APP_VERSION"), "-") {
		return "preview"
	}
	return "stable"
}

func (a app) tags(appVersion string, revision uint64) ([]string, error) {
	if a.getenv("ALIASES") == "" {
		return version.ImageTags(appVersion, revision), nil
	}
	var aliases []string
	if err := json.Unmarshal([]byte(a.getenv("ALIASES")), &aliases); err != nil {
		return nil, fmt.Errorf("decode aliases: %w", err)
	}
	if err := version.ValidateAliases(appVersion, aliases); err != nil {
		return nil, err
	}
	return append([]string{fmt.Sprintf("%s-r%d", appVersion, revision), appVersion}, aliases...), nil
}
