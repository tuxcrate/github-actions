package releaseconfig

import (
	"errors"
	"regexp"
	"strings"

	"github.com/tuxcrate/github-actions/internal/version"
)

var (
	imageRE          = regexp.MustCompile(`^ghcr\.io/[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)+$`)
	digestRE         = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	baseRepositoryRE = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?::[0-9]+)?(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
	trackRE          = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
)

// ImageName validates and normalizes an image, defaulting to the GHCR repository.
func ImageName(image, repository string) (string, error) {
	if image == "" {
		image = "ghcr.io/" + repository
	}
	image = strings.ToLower(image)
	if !imageRE.MatchString(image) {
		return "", errors.New("image must be an untagged GHCR image name")
	}
	return image, nil
}

func validateBaseImage(image string) error {
	if image == "" {
		return nil // Optional for existing callers with a literal FROM reference.
	}
	reference, digest, ok := strings.Cut(image, "@")
	colon := strings.LastIndex(reference, ":")
	if !ok || !digestRE.MatchString(digest) || colon <= strings.LastIndex(reference, "/") || colon < 1 ||
		!baseRepositoryRE.MatchString(reference[:colon]) || !version.ValidImageTag(reference[colon+1:]) {
		return errors.New("baseImage must be a complete image:tag@sha256:<64 lowercase hex characters> reference")
	}
	return nil
}

// ValidDigest reports whether digest is a lowercase SHA-256 image digest.
func ValidDigest(digest string) bool {
	return digestRE.MatchString(digest)
}

// ValidTrackName reports whether name is a valid release track identifier.
func ValidTrackName(name string) bool {
	return trackRE.MatchString(name)
}
