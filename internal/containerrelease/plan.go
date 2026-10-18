package containerrelease

import (
	"encoding/json"
	"os"

	"github.com/tuxcrate/github-actions/internal/releaseconfig"
)

func (a app) builds() ([]releaseconfig.Build, error) {
	// Validate the repository before opening the fixed root metadata file.
	if _, err := releaseconfig.ImageName("", a.getenv("GITHUB_REPOSITORY")); err != nil {
		return nil, err
	}
	f, err := os.Open("meta.yaml")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return releaseconfig.Plan(f, a.getenv("GITHUB_REPOSITORY"))
}

func (a app) plan() error {
	builds, err := a.builds()
	if err != nil {
		return err
	}
	data, err := json.Marshal(buildMatrix{Include: builds})
	if err != nil {
		return err
	}
	return a.output("matrix", string(data))
}
