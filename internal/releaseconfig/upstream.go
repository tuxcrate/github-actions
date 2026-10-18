package releaseconfig

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/tuxcrate/github-actions/internal/version"
)

var (
	datasourceRE  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	debPackageRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)
	debVersionRE  = regexp.MustCompile(`^` + version.CorePattern + `-[0-9]+$`)
	pypiPackageRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	pypiVersionRE = regexp.MustCompile(`^` + version.CorePattern + `$`)
)

func (u upstream) validate() error {
	if u.Datasource != "deb" && u.Datasource != "pypi" {
		// Renovate owns datasource discovery; do not maintain an allowlist here.
		if !datasourceRE.MatchString(u.Datasource) {
			return errors.New("upstream requires a datasource identifier")
		}
		if fields := strings.Fields(u.DepName); len(fields) != 1 || fields[0] != u.DepName {
			return errors.New("upstream requires a nonempty depName without whitespace")
		}
		if u.RegistryURL != "" {
			registry, err := url.Parse(u.RegistryURL)
			if err != nil || registry.Host == "" || (registry.Scheme != "https" && registry.Scheme != "http") || registry.User != nil || registry.Fragment != "" {
				return errors.New("upstream registryUrl must be an HTTP(S) URL")
			}
		}
		return version.Validate(u.Version)
	}
	if u.Datasource == "pypi" {
		if !pypiPackageRE.MatchString(u.DepName) {
			return errors.New("upstream requires a PyPI package depName")
		}
		if !pypiVersionRE.MatchString(u.Version) {
			return errors.New("upstream version must be a stable X.Y.Z PyPI package version")
		}
		if u.RegistryURL != "" {
			registry, err := url.Parse(u.RegistryURL)
			if err != nil || registry.Host == "" || (registry.Scheme != "https" && registry.Scheme != "http") || registry.User != nil || registry.Fragment != "" {
				return errors.New("upstream registryUrl must be an HTTP(S) PyPI index URL")
			}
		}
		return nil
	}
	if !debPackageRE.MatchString(u.DepName) {
		return errors.New("upstream requires a Debian package depName")
	}
	if !debVersionRE.MatchString(u.Version) {
		return errors.New("upstream version must be a stable X.Y.Z-N Debian package version")
	}
	registry, err := url.Parse(u.RegistryURL)
	if err != nil || registry.Host == "" || (registry.Scheme != "https" && registry.Scheme != "http") || registry.User != nil || registry.Fragment != "" {
		return errors.New("upstream registryUrl must be an HTTP(S) APT repository URL")
	}
	for _, key := range []string{"suite", "components", "binaryArch"} {
		if registry.Query().Get(key) == "" {
			return fmt.Errorf("upstream registryUrl requires %s", key)
		}
	}
	return nil
}
