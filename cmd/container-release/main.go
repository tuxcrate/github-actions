// Command container-release orchestrates releases for the shared container workflow.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const versionPattern = `(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)`
const appVersionPattern = versionPattern + `(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?`

var (
	versionRE = regexp.MustCompile(`^` + appVersionPattern + `$`)
	releaseRE = regexp.MustCompile(`^v(` + appVersionPattern + `)-r(0|[1-9][0-9]*)$`)
	imageRE   = regexp.MustCompile(`^ghcr\.io/[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)+$`)
	digestRE  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	missingRE = regexp.MustCompile(`(?i)not found|manifest unknown|name_unknown`)
)

func validateVersion(version string) error {
	if !versionRE.MatchString(version) || len(version) > 106 {
		return errors.New("app-version must be X.Y.Z with an optional SemVer prerelease, no v prefix or build metadata, at most 106 characters")
	}
	if releaseRE.MatchString("v" + version) {
		return errors.New("app-version must not end in -rN: that namespace is reserved for image revisions")
	}
	if _, suffix, ok := strings.Cut(version, "-"); ok {
		for _, id := range strings.Split(suffix, ".") {
			if len(id) > 1 && id[0] == '0' && strings.Trim(id, "0123456789") == "" {
				return errors.New("numeric prerelease identifiers must not have leading zeros")
			}
		}
	}
	return nil
}

func imageName(image, repository string) (string, error) {
	if image == "" {
		image = "ghcr.io/" + repository
	}
	image = strings.ToLower(image)
	if !imageRE.MatchString(image) {
		return "", errors.New("image must be an untagged GHCR image name")
	}
	return image, nil
}

func nextRevision(version string, tags []string) (uint64, error) {
	if err := validateVersion(version); err != nil {
		return 0, err
	}
	var next uint64
	for _, tag := range tags {
		match := releaseRE.FindStringSubmatch(tag)
		if match == nil || match[1] != version {
			continue
		}
		revision, err := strconv.ParseUint(match[len(match)-1], 10, 64)
		if err != nil || revision == ^uint64(0) {
			return 0, fmt.Errorf("release revision out of range: %s", tag)
		}
		if revision >= next {
			next = revision + 1
		}
	}
	return next, nil
}

func imageTags(version string, revision uint64) []string {
	if strings.Contains(version, "-") {
		return []string{fmt.Sprintf("%s-r%d", version, revision), version, "preview"}
	}
	parts := strings.Split(version, ".")
	return []string{fmt.Sprintf("%s-r%d", version, revision), version, parts[0] + "." + parts[1], "latest"}
}

type commandResult struct {
	stdout, stderr string
	code           int
}

type commandRunner func(input string, args ...string) (commandResult, error)

func execCommand(input string, args ...string) (commandResult, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := commandResult{stdout: strings.TrimSpace(stdout.String()), stderr: strings.TrimSpace(stderr.String())}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.code = exit.ExitCode()
		return result, nil
	}
	return result, err
}

type release struct {
	TagName     string `json:"tag_name"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
}

type app struct {
	exec   commandRunner
	getenv func(string) string
}

func (a app) run(input string, args ...string) (string, error) {
	result, err := a.exec(input, args...)
	if err != nil {
		return "", fmt.Errorf("run %s: %w", strings.Join(args, " "), err)
	}
	if result.code != 0 {
		return "", fmt.Errorf("command failed (%d): %s\n%s", result.code, strings.Join(args, " "), result.stderr)
	}
	return result.stdout, nil
}

func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(text)
	return errors.Join(writeErr, f.Close())
}

func (a app) output(name, value string) error {
	// Only validated metadata is written here, never arbitrary commit messages.
	return appendFile(a.getenv("GITHUB_OUTPUT"), name+"<<TUXCRATE_OUTPUT\n"+value+"\nTUXCRATE_OUTPUT\n")
}

func (a app) previousRelease(releases []release) (string, error) {
	var candidates []release
	for _, r := range releases {
		track := a.trackName()
		marker := "<!-- tuxcrate-track:" + track + " -->"
		matchesTrack := strings.HasSuffix(strings.TrimSpace(r.Body), marker) || (track == "stable" && !r.Prerelease && !strings.Contains(r.Body, "<!-- tuxcrate-track:"))
		if !r.Draft && matchesTrack && releaseRE.MatchString(r.TagName) {
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
	head, err := a.run("", "gh", "api", "repos/"+a.getenv("GITHUB_REPOSITORY")+"/git/ref/heads/"+a.getenv("GITHUB_REF_NAME"), "--jq", ".object.sha")
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
	for i, platform := range platforms {
		platforms[i] = "`" + strings.TrimSpace(platform) + "`"
	}
	notes := fmt.Sprintf("## Container image\n\n- Application version: `%s`\n- Immutable image: `%s:%s`\n- Digest pin: `%s@%s`\n- Moving aliases:\n%s\n- Platforms: %s\n\n## Packaging changes\n\n%s\n",
		version, image, tags[0], image, digest, strings.Join(aliases, "\n"), strings.Join(platforms, ", "), changes)
	return notes, nil
}

func (a app) metadata() error {
	if err := validateVersion(a.getenv("APP_VERSION")); err != nil {
		return err
	}
	image, err := imageName(a.getenv("IMAGE"), a.getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return err
	}
	return a.output("image", image)
}

func (a app) reserve() error {
	if !trackRE.MatchString(a.trackName()) {
		return errors.New("invalid release track name")
	}
	// Concurrency does not guarantee queue ordering. Old reruns must not
	// roll moving aliases back after a newer default-branch commit released.
	if err := a.assertCurrentCommit(); err != nil {
		return err
	}
	version := a.getenv("APP_VERSION")
	if err := validateVersion(version); err != nil {
		return err
	}
	image, err := imageName(a.getenv("IMAGE"), a.getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return err
	}
	digest := a.getenv("IMAGE_DIGEST")
	if !digestRE.MatchString(digest) {
		return errors.New("build did not return a valid image digest")
	}
	repository := a.getenv("GITHUB_REPOSITORY")
	data, err := a.run("", "gh", "api", "--paginate", "--slurp", "repos/"+repository+"/releases?per_page=100")
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
	var releases []release
	for _, page := range pages {
		for _, r := range page {
			releases = append(releases, r)
			// Drafts durably reserve revisions even after failed publication.
			tags = append(tags, r.TagName)
		}
	}
	revision, err := nextRevision(version, tags)
	if err != nil {
		return err
	}
	tags, err = a.tags(version, revision)
	if err != nil {
		return err
	}
	tag := "v" + tags[0]
	if err := a.assertTagAvailable(image, tags[0]); err != nil {
		return err
	}
	previous, err := a.previousRelease(releases)
	if err != nil {
		return err
	}
	notes, err := a.releaseNotes(version, tags, image, digest, previous)
	if err != nil {
		return err
	}
	track := a.trackName()
	notes += "\n<!-- tuxcrate-track:" + track + " -->\n"
	payload, err := json.Marshal(struct {
		TagName         string `json:"tag_name"`
		TargetCommitish string `json:"target_commitish"`
		Name            string `json:"name"`
		Draft           bool   `json:"draft"`
		Prerelease      bool   `json:"prerelease"`
		Body            string `json:"body"`
	}{tag, a.getenv("GITHUB_SHA"), tags[0], true, strings.Contains(version, "-"), notes})
	if err != nil {
		return err
	}
	// Reserve before exposing named tags. Retain failed drafts, never reuse them.
	if _, err := a.run(string(payload), "gh", "api", "--method", "POST", "repos/"+repository+"/releases", "--input", "-"); err != nil {
		return err
	}
	if err := a.output("tag", tag); err != nil {
		return err
	}
	if err := a.output("tags", strings.Join(tags, "\n")); err != nil {
		return err
	}
	if err := a.output("latest", strconv.FormatBool(!strings.Contains(version, "-") && contains(tags[1:], "latest"))); err != nil {
		return err
	}
	return appendFile(a.getenv("GITHUB_STEP_SUMMARY"), "# "+tags[0]+"\n\n"+notes)
}

func main() {
	flag.Parse()
	a := app{exec: execCommand, getenv: os.Getenv}
	var err error
	switch {
	case flag.NArg() == 1 && flag.Arg(0) == "metadata":
		err = a.metadata()
	case flag.NArg() == 1 && flag.Arg(0) == "reserve":
		err = a.reserve()
	case flag.NArg() == 1 && flag.Arg(0) == "plan":
		err = a.plan()
	case flag.NArg() == 1 && flag.Arg(0) == "manifest":
		err = a.manifest()
	default:
		err = errors.New("usage: container-release {plan|metadata|reserve|manifest}")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
