package containerrelease

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
	"github.com/tuxcrate/github-actions/internal/githubapi"
	"github.com/tuxcrate/github-actions/internal/releaseconfig"
	releaseversion "github.com/tuxcrate/github-actions/internal/version"
)

// releasePlan is separate from plan so local metadata validation and PRs never
// need release-history access. Only pushes may skip tracks; schedules and manual
// runs deliberately refresh dependencies even when the source has not changed.
func (a app) releasePlan() error {
	builds, err := a.builds()
	if err != nil {
		return err
	}
	var releases []release
	if a.getenv("GITHUB_EVENT_NAME") == "push" {
		data, err := githubapi.Call(a.run, "", "--paginate", "--slurp", "repos/"+a.getenv("GITHUB_REPOSITORY")+"/releases?per_page=100")
		if err != nil {
			return err
		}
		var pages [][]release
		if err := json.Unmarshal([]byte(data), &pages); err != nil {
			return fmt.Errorf("decode release history: %w", err)
		}
		for _, page := range pages {
			releases = append(releases, page...)
		}
	}
	selected := make([]releaseconfig.Build, 0, len(builds))
	var summary strings.Builder
	summary.WriteString("## Container release decisions\n\n")
	for _, b := range builds {
		changed, reason := true, "forced rebuild (scheduled or manual run)"
		if a.getenv("GITHUB_EVENT_NAME") == "push" {
			changed, reason = a.trackChanged(b, releases)
		}
		action := "skip"
		if changed {
			selected = append(selected, b)
			action = "build and release"
		}
		fmt.Fprintf(&summary, "- `%s`: %s — %s.\n", b.Track, action, reason)
	}
	fmt.Print(summary.String())
	if file := a.getenv("GITHUB_STEP_SUMMARY"); file != "" {
		if err := appendFile(file, summary.String()); err != nil {
			return err
		}
	}
	data, err := json.Marshal(buildMatrix{Include: selected})
	if err != nil {
		return err
	}
	if err := a.output("matrix", string(data)); err != nil {
		return err
	}
	return a.output("changed", fmt.Sprint(len(selected) > 0))
}

func publishedForTrack(r release, track string) bool {
	marker := "<!-- tuxcrate-track:" + track + " -->"
	return !r.Draft && releaseversion.IsReleaseTag(r.TagName) &&
		(strings.HasSuffix(strings.TrimSpace(r.Body), marker) ||
			(track == "stable" && !r.Prerelease && !strings.Contains(r.Body, "<!-- tuxcrate-track:")))
}

func (a app) trackChanged(b releaseconfig.Build, releases []release) (bool, string) {
	var previous release
	for _, r := range releases {
		if publishedForTrack(r, b.Track) && (previous.TagName == "" || r.PublishedAt > previous.PublishedAt) {
			previous = r
		}
	}
	if previous.TagName == "" {
		return true, "no published release for this track"
	}
	// Do not use the preceding commit or a draft: failed releases must leave the
	// outstanding source changes eligible for the next push. Read old metadata,
	// never check it out or execute code from a historical release.
	metadata, err := a.run("", "git", "show", previous.TagName+":meta.yaml")
	if err != nil {
		return true, "previous release metadata unavailable"
	}
	oldBuilds, err := releaseconfig.Plan(strings.NewReader(metadata), a.getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return true, "previous release metadata uses an unsupported configuration"
	}
	for _, old := range oldBuilds {
		if old.Track != b.Track {
			continue
		}
		if old != b {
			return true, "build or publication settings changed"
		}
		current, err := a.inputFingerprint("HEAD", b)
		if err != nil {
			// Unusual layouts (for example symlinked control files) must not
			// turn into false no-ops or remove support for existing builds.
			return true, "current build inputs cannot be compared safely"
		}
		prior, err := a.inputFingerprint(previous.TagName, old)
		if err != nil {
			return true, "previous release build inputs unavailable"
		}
		if current != prior {
			return true, "build context or smoke tests changed"
		}
		return false, "inputs match the last published release " + previous.TagName
	}
	return true, "track absent from previous release configuration"
}

type sourceEntry struct {
	name, mode, object string
}

// inputFingerprint uses Git object IDs and modes, not image digests (which also
// contain revision labels, build timestamps and attestations). Include every
// tracked context file unless Docker excludes it: COPY . must remain safe.
func (a app) inputFingerprint(ref string, b releaseconfig.Build) (string, error) {
	tree, err := a.run("", "git", "ls-tree", "-r", "-z", "--full-tree", ref)
	if err != nil {
		return "", err
	}
	var entries []sourceEntry
	byName := map[string]sourceEntry{}
	for _, record := range strings.Split(tree, "\x00") {
		if record == "" {
			continue
		}
		header, name, ok := strings.Cut(record, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 {
			return "", fmt.Errorf("invalid Git tree entry")
		}
		entry := sourceEntry{name: name, mode: fields[0], object: fields[2]}
		entries = append(entries, entry)
		byName[name] = entry
	}
	containerfile := path.Clean(b.Containerfile)
	if entry, ok := byName[containerfile]; !ok || (entry.mode != "100644" && entry.mode != "100755") {
		return "", fmt.Errorf("Containerfile %s must be a tracked regular file", containerfile)
	}
	context := path.Clean(b.Context)
	for dir := context; dir != "."; dir = path.Dir(dir) {
		if _, exists := byName[dir]; exists {
			return "", fmt.Errorf("build context %s must not traverse a file or symlink", context)
		}
	}
	ignore := path.Join(context, ".dockerignore")
	if _, exists := byName[containerfile+".dockerignore"]; exists {
		ignore = containerfile + ".dockerignore"
	}
	var patterns []string
	if entry, exists := byName[ignore]; exists {
		// BuildKit reads ignore files from disk. Refuse symlinks rather than
		// mistakenly treating their link targets as ignore patterns.
		if entry.mode != "100644" && entry.mode != "100755" {
			return "", fmt.Errorf("ignore file %s must be a regular file", ignore)
		}
		text, err := a.run("", "git", "show", ref+":"+ignore)
		if err != nil {
			return "", err
		}
		patterns, err = ignorefile.ReadAll(strings.NewReader(text))
		if err != nil {
			return "", err
		}
	}
	matcher, err := patternmatcher.New(patterns)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	config, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(hash, "%s\x00", config)
	for _, entry := range entries {
		// Host-side tests are validation inputs even when excluded from COPY.
		testInput := entry.name == "tests" || strings.HasPrefix(entry.name, "tests/")
		if testInput && entry.mode == "120000" {
			return "", fmt.Errorf("smoke test symlink %s may depend on files outside tests", entry.name)
		}
		required := entry.name == containerfile || entry.name == ignore || testInput
		relative := entry.name
		inContext := context == "." || strings.HasPrefix(entry.name, context+"/")
		if context != "." {
			relative = strings.TrimPrefix(entry.name, context+"/")
		}
		if !required {
			if !inContext {
				continue
			}
			ignored, err := matcher.MatchesOrParentMatches(relative)
			if err != nil {
				return "", err
			}
			if ignored {
				continue
			}
		}
		fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", entry.name, entry.mode, entry.object)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
