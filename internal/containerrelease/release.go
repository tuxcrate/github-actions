package containerrelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tuxcrate/github-actions/internal/githubapi"
	"github.com/tuxcrate/github-actions/internal/releaseconfig"
	releaseversion "github.com/tuxcrate/github-actions/internal/version"
)

var missingRE = regexp.MustCompile(`(?i)not found|manifest unknown|name_unknown`)

func (a app) previousRelease(releases []release) (string, error) {
	var candidates []release
	for _, r := range releases {
		if publishedForTrack(r, a.trackName()) {
			candidates = append(candidates, r)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].PublishedAt > candidates[j].PublishedAt })
	for _, r := range candidates {
		result, err := a.exec("", "git", "merge-base", "--is-ancestor", r.TagName, "HEAD")
		if err != nil {
			return "", err
		}
		switch result.code {
		case 0:
			return r.TagName, nil
		case 1:
			continue
		default:
			return "", fmt.Errorf("cannot resolve release tag %s: %s", r.TagName, result.stderr)
		}
	}
	return "", nil
}

func (a app) assertTagAvailable(image, tag string) error {
	result, err := a.exec("", "docker", "buildx", "imagetools", "inspect", image+":"+tag)
	if err != nil {
		return err
	}
	if result.code == 0 {
		return fmt.Errorf("refusing to overwrite existing image %s:%s", image, tag)
	}
	// Fail closed on authentication, networking, and other registry errors.
	if !missingRE.MatchString(result.stderr) {
		return fmt.Errorf("cannot check whether image tag exists: %s", result.stderr)
	}
	return nil
}

func (a app) assertCurrentCommit() error {
	head, err := githubapi.Call(a.run, "", "repos/"+a.getenv("GITHUB_REPOSITORY")+"/git/ref/heads/"+a.getenv("GITHUB_REF_NAME"), "--jq", ".object.sha")
	if err != nil {
		return err
	}
	if head != a.getenv("GITHUB_SHA") {
		return errors.New("refusing to publish a stale default-branch commit; rerun on the current branch HEAD")
	}
	return nil
}

func (a app) releaseNotes(version string, tags []string, image, digest, previous string) (string, error) {
	url := a.getenv("GITHUB_SERVER_URL") + "/" + a.getenv("GITHUB_REPOSITORY")
	commitRange := "HEAD"
	if previous != "" {
		commitRange = previous + "..HEAD"
	}
	commits, err := a.run("", "git", "log", "--reverse", "--format=%H%x09%s", commitRange)
	if err != nil {
		return "", err
	}
	var changes string
	if commits != "" {
		var lines []string
		for _, commit := range strings.Split(commits, "\n") {
			sha, subject, ok := strings.Cut(commit, "\t")
			if !ok || len(sha) < 7 {
				return "", fmt.Errorf("invalid git log entry: %q", commit)
			}
			lines = append(lines, fmt.Sprintf("- %s ([%s](%s/commit/%s))", subject, sha[:7], url, sha))
		}
		changes = strings.Join(lines, "\n")
	} else {
		reason := "Image rebuild"
		if a.getenv("GITHUB_EVENT_NAME") == "schedule" {
			reason = "Scheduled security rebuild"
		}
		changes = reason + "; no packaging commits since the previous release. Application version unchanged."
	}
	aliasTags := append([]string(nil), tags[1:]...)
	sort.Slice(aliasTags, func(i, j int) bool {
		left, right := aliasTags[i], aliasTags[j]
		leftVersion := left == version || strings.HasPrefix(version, left+".")
		rightVersion := right == version || strings.HasPrefix(version, right+".")
		if leftVersion != rightVersion {
			return leftVersion
		}
		if leftVersion {
			return len(left) > len(right)
		}
		return left < right
	})
	aliases := make([]string, 0, len(aliasTags))
	for _, tag := range aliasTags {
		aliases = append(aliases, "  - `"+image+":"+tag+"`")
	}
	platforms := strings.Split(a.getenv("PLATFORMS"), ",")
	flavor, osVersion, err := a.imageOS(image+"@"+digest, strings.TrimSpace(platforms[0]))
	if err != nil {
		return "", err
	}
	for i, platform := range platforms {
		platforms[i] = "`" + strings.TrimSpace(platform) + "`"
	}
	notes := fmt.Sprintf("## Container image\n\n- Application version: `%s`\n- OS: `%s/%s`\n- Immutable Image: `%s:%s@%s`\n- Moving aliases:\n%s\n- Platforms: %s\n\n## Packaging changes\n\n%s\n",
		version, strings.ToUpper(flavor[:1])+flavor[1:], osVersion, image, tags[0], digest, strings.Join(aliases, "\n"), strings.Join(platforms, ", "), changes)
	return notes, nil
}

func (a app) metadata() error {
	if err := releaseversion.Validate(a.getenv("APP_VERSION")); err != nil {
		return err
	}
	image, err := releaseconfig.ImageName(a.getenv("IMAGE"), a.getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return err
	}
	return a.output("image", image)
}

func (a app) reserve() error {
	if !releaseconfig.ValidTrackName(a.trackName()) {
		return errors.New("invalid release track name")
	}
	// Concurrency does not guarantee queue ordering. Old reruns must not
	// roll moving aliases back after a newer default-branch commit released.
	if err := a.assertCurrentCommit(); err != nil {
		return err
	}
	version := a.getenv("APP_VERSION")
	if err := releaseversion.Validate(version); err != nil {
		return err
	}
	imageVersion := a.getenv("IMAGE_VERSION")
	if err := releaseversion.ValidateImageVersion(version, imageVersion); err != nil {
		return err
	}
	// Validate all publication tags before consulting history or reserving a draft.
	if _, err := a.tags(imageVersion, 0); err != nil {
		return err
	}
	image, err := releaseconfig.ImageName(a.getenv("IMAGE"), a.getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return err
	}
	repository := a.getenv("GITHUB_REPOSITORY")
	data, err := githubapi.Call(a.run, "", "--paginate", "--slurp", "repos/"+repository+"/releases?per_page=100")
	if err != nil {
		return err
	}
	var pages [][]release
	if err := json.Unmarshal([]byte(data), &pages); err != nil {
		return fmt.Errorf("decode release history: %w", err)
	}
	gitTags, err := a.run("", "git", "tag", "--list")
	if err != nil {
		return err
	}
	tags := strings.Split(gitTags, "\n")
	for _, page := range pages {
		for _, r := range page {
			// Drafts durably reserve revisions even after failed publication.
			tags = append(tags, r.TagName)
		}
	}
	revision, err := releaseversion.NextRevision(imageVersion, tags)
	if err != nil {
		return err
	}
	tags, err = a.tags(imageVersion, revision)
	if err != nil {
		return err
	}
	tag := "v" + tags[0]
	if err := a.assertTagAvailable(image, tags[0]); err != nil {
		return err
	}
	// The digest and final OS identity do not exist until the build is tested.
	notes := fmt.Sprintf("Revision reserved for application version `%s` at source commit `%s`. Build and publication pending.\n", version, a.getenv("GITHUB_SHA"))
	track := a.trackName()
	notes += "\n<!-- tuxcrate-track:" + track + " -->\n"
	payload, err := json.Marshal(releaseRequest{
		TagName:         tag,
		TargetCommitish: a.getenv("GITHUB_SHA"),
		Name:            tags[0],
		Draft:           true,
		Prerelease:      a.isPrerelease(),
		Body:            notes,
	})
	if err != nil {
		return err
	}
	// Reserve before exposing named tags. Retain failed drafts, never reuse them.
	data, err = githubapi.Call(a.run, string(payload), "--method", "POST", "repos/"+repository+"/releases", "--input", "-")
	if err != nil {
		return fmt.Errorf("reserve draft release %s: %w\nThe POST may have created a draft despite the failure. Inspect releases before rerunning; retain any reserved drafts", tag, err)
	}
	var draft release
	if err := json.Unmarshal([]byte(data), &draft); err != nil {
		return fmt.Errorf("decode reserved draft %s: %w; retain any reserved drafts and inspect releases before rerunning", tag, err)
	}
	if draft.ID <= 0 || draft.TagName != tag || !draft.Draft || draft.TargetCommitish != a.getenv("GITHUB_SHA") || draft.Prerelease != a.isPrerelease() || !strings.Contains(draft.Body, "<!-- tuxcrate-track:"+track+" -->") {
		return fmt.Errorf("invalid reservation response for %s; retain any reserved drafts and inspect releases before rerunning", tag)
	}
	if err := a.output("release-id", strconv.FormatInt(draft.ID, 10)); err != nil {
		return err
	}
	if err := a.output("tag", tag); err != nil {
		return err
	}
	if err := a.output("package-version", tags[0]); err != nil {
		return err
	}
	if err := a.output("tags", strings.Join(tags, "\n")); err != nil {
		return err
	}
	if err := a.output("latest", strconv.FormatBool(!a.isPrerelease() && contains(tags[1:], "latest"))); err != nil {
		return err
	}
	return appendFile(a.getenv("GITHUB_STEP_SUMMARY"), "# "+tags[0]+"\n\n"+notes)
}

// finalize fills the reserved draft with metadata from the tested artifact.
// It never allocates another revision or publishes a release or image tag.
func (a app) finalize() error {
	if err := a.assertCurrentCommit(); err != nil {
		return err
	}
	version := a.getenv("APP_VERSION")
	if err := releaseversion.Validate(version); err != nil {
		return err
	}
	imageVersion := a.getenv("IMAGE_VERSION")
	if err := releaseversion.ValidateImageVersion(version, imageVersion); err != nil {
		return err
	}
	tag := a.getenv("RELEASE_TAG")
	tagVersion, revision, err := releaseversion.ParseReleaseTag(tag)
	if err != nil {
		return err
	}
	if tagVersion != imageVersion || !releaseconfig.ValidTrackName(a.trackName()) {
		return errors.New("reserved release does not match image version or track")
	}
	tags, err := a.tags(imageVersion, revision)
	if err != nil {
		return err
	}
	image, err := releaseconfig.ImageName(a.getenv("IMAGE"), a.getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return err
	}
	digest := a.getenv("IMAGE_DIGEST")
	if !releaseconfig.ValidDigest(digest) {
		return errors.New("build did not return a valid image digest")
	}
	releaseID := a.getenv("RELEASE_ID")
	id, err := strconv.ParseInt(releaseID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != releaseID {
		return errors.New("invalid or missing reserved release ID")
	}
	repository := a.getenv("GITHUB_REPOSITORY")
	// GitHub's get-by-tag endpoint excludes drafts. Use the ID returned by POST.
	data, err := githubapi.Call(a.run, "", "repos/"+repository+"/releases/"+releaseID)
	if err != nil {
		return err
	}
	var draft release
	if err := json.Unmarshal([]byte(data), &draft); err != nil {
		return fmt.Errorf("decode reserved release: %w", err)
	}
	marker := "<!-- tuxcrate-track:" + a.trackName() + " -->"
	if draft.ID != id || !draft.Draft || draft.TagName != tag || draft.TargetCommitish != a.getenv("GITHUB_SHA") || draft.Prerelease != a.isPrerelease() || !strings.Contains(draft.Body, marker) {
		return errors.New("refusing to finalize a release that is not this build's reserved draft")
	}
	// Recheck after the build; never overwrite a revision published elsewhere.
	if err := a.assertTagAvailable(image, tags[0]); err != nil {
		return err
	}
	data, err = githubapi.Call(a.run, "", "--paginate", "--slurp", "repos/"+repository+"/releases?per_page=100")
	if err != nil {
		return err
	}
	var pages [][]release
	if err := json.Unmarshal([]byte(data), &pages); err != nil {
		return fmt.Errorf("decode release history: %w", err)
	}
	var releases []release
	for _, page := range pages {
		releases = append(releases, page...)
	}
	previous, err := a.previousRelease(releases)
	if err != nil {
		return err
	}
	notes, err := a.releaseNotes(version, tags, image, digest, previous)
	if err != nil {
		return err
	}
	notes += "\n" + marker + "\n"
	payload, err := json.Marshal(struct {
		Body string `json:"body"`
	}{notes})
	if err != nil {
		return err
	}
	if _, err := githubapi.Call(a.run, string(payload), "--method", "PATCH", "repos/"+repository+"/releases/"+strconv.FormatInt(draft.ID, 10), "--input", "-"); err != nil {
		return fmt.Errorf("finalize reserved release %s: %w", tag, err)
	}
	return appendFile(a.getenv("GITHUB_STEP_SUMMARY"), "# "+tags[0]+"\n\n"+notes)
}
