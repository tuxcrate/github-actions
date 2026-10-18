// Package releaseconfig decodes track configuration and produces build plans.
package releaseconfig

import (
	"errors"
	"io"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

func sourcePath(path string) bool {
	clean := filepath.Clean(path)
	return path != "" && !filepath.IsAbs(path) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) && !strings.ContainsAny(path, "\r\n")
}

func decodeConfig(r io.Reader) (trackConfig, error) {
	var config trackConfig
	d := yaml.NewDecoder(r)
	d.KnownFields(true)
	if err := d.Decode(&config); err != nil {
		return config, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return config, errors.New("expected exactly one YAML configuration document")
	}
	return config, nil
}
