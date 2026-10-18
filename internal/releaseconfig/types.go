package releaseconfig

type trackConfig struct {
	Image  string  `yaml:"image"`
	Tracks []track `yaml:"tracks"`
}

type track struct {
	Name          string            `yaml:"name"`
	Upstream      *upstream         `yaml:"upstream,omitempty"`
	Version       string            `yaml:"version"`
	BaseImage     string            `yaml:"baseImage,omitempty"`
	Aliases       []string          `yaml:"aliases"`
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
	Image         string `json:"image"`
	Aliases       string `json:"aliases"` // JSON array, including an explicit empty array.
	Context       string `json:"context"`
	Containerfile string `json:"containerfile"`
	Platforms     string `json:"platforms"`
	BuildArgs     string `json:"build-args"`
}
