package containerrelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/tuxcrate/github-actions/internal/releaseconfig"
	releaseversion "github.com/tuxcrate/github-actions/internal/version"
)

var (
	platformRE = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9_]+(?:/[a-zA-Z0-9._-]+)?$`)
	commitRE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func (a app) manifest() error {
	return a.writeManifest("")
}

func (a app) checkManifest() error {
	localImage := a.getenv("LOCAL_IMAGE")
	if localImage == "" {
		return errors.New("a local image is required to validate PR release metadata")
	}
	getenv := a.getenv
	a.getenv = func(key string) string {
		if key == "RELEASE_TAG" {
			return "v" + getenv("IMAGE_VERSION") + "-r0"
		}
		return getenv(key)
	}
	// PR validation uses a local image and a synthetic revision. It never
	// reserves a revision, creates a GitHub release, or publishes release tags.
	return a.writeManifest(localImage)
}

func (a app) writeManifest(localImage string) error {
	version := a.getenv("APP_VERSION")
	if err := releaseversion.Validate(version); err != nil {
		return err
	}
	tag := a.getenv("RELEASE_TAG")
	tagVersion, revision, err := releaseversion.ParseReleaseTag(tag)
	if err != nil {
		return err
	}
	imageVersion := a.getenv("IMAGE_VERSION")
	if err := releaseversion.ValidateImageVersion(version, imageVersion); err != nil {
		return err
	}
	if tagVersion != imageVersion {
		return errors.New("release tag does not match image version")
	}
	tags, err := a.tags(imageVersion, revision)
	if err != nil {
		return err
	}
	image, err := releaseconfig.ImageName(a.getenv("IMAGE"), a.getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return err
	}
	if !releaseconfig.ValidDigest(a.getenv("IMAGE_DIGEST")) {
		return errors.New("invalid image digest")
	}
	platforms := strings.Split(a.getenv("PLATFORMS"), ",")
	for i, platform := range platforms {
		platform = strings.TrimSpace(platform)
		if !platformRE.MatchString(platform) {
			return fmt.Errorf("invalid or empty release platform %q", platform)
		}
		platforms[i] = platform
	}
	track := a.trackName()
	if !releaseconfig.ValidTrackName(track) {
		return errors.New("invalid release track")
	}
	if !commitRE.MatchString(a.getenv("GITHUB_SHA")) {
		return errors.New("invalid or empty source commit")
	}
	imageRef := image + "@" + a.getenv("IMAGE_DIGEST")
	if localImage != "" {
		imageRef = localImage
	}
	// A release has one OS identity; inspect the first declared platform.
	flavor, osVersion, err := a.imageOS(imageRef, platforms[0])
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(releaseManifest{
		SchemaVersion: 2,
		Image:         image,
		Track:         track,
		Version:       version,
		ImageVersion:  imageVersion,
		Revision:      revision,
		ImmutableTag:  tags[0],
		Digest:        a.getenv("IMAGE_DIGEST"),
		Aliases:       tags[1:],
		Platforms:     platforms,
		SourceCommit:  a.getenv("GITHUB_SHA"),
		OSFlavor:      flavor,
		OSVersion:     osVersion,
	}, "", "  ")
	if err != nil {
		return err
	}
	// Publication time comes from the published GitHub release, not the draft.
	return os.WriteFile("release.json", append(data, '\n'), 0600)
}
