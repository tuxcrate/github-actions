package releaseconfig

type trackConfig struct {
	Image  string  `yaml:"image"`
	Tracks []track `yaml:"tracks"`
}

type track struct {
	Name          string            `yaml:"name"`
	Upstream      *upstream         `yaml:"upstream,omitempty"`
	Version       string            `yaml:"version"`
	Prerelease    bool              `yaml:"prerelease,omitempty"`
	BaseImage     string            `yaml:"baseImage,omitempty"`
	Tags          []string          `yaml:"tags"`
	Context       string            `yaml:"context,omitempty"`
	Containerfile string            `yaml:"containerfile,omitempty"`
	Platforms     string            `yaml:"platforms,omitempty"`
	BuildArgs     map[string]string `yaml:"buildArgs,omitempty"`
}

// upstream is discovery metadata for Renovate, not a runtime download source.
type upstream struct {
	Datasource  string `yaml:"datasource"`
	DepName     string `yaml:"depName"`
	RegistryURL string `yaml:"registryUrl"`
	Version     string `yaml:"version"`
}

// Build is a validated track's build-matrix entry.
type Build struct {
	Track         string `json:"track"`
	Version       string `json:"version"`
	ImageVersion  string `json:"image-version"`
	Prerelease    bool   `json:"prerelease"`
	Image         string `json:"image"`
	Tags          string `json:"tags"` // JSON array of all expanded moving tags.
	Context       string `json:"context"`
	Containerfile string `json:"containerfile"`
	Platforms     string `json:"platforms"`
	BuildArgs     string `json:"build-args"`
}
