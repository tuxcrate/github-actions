// Package containerartifact keeps an OCI archive unchanged between testing and upload.
package containerartifact

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const usage = "usage: container-artifact test ARCHIVE PLATFORMS APP_VERSION | publish ARCHIVE DESTINATION"

type app struct {
	stdout, stderr io.Writer
}

// Run tests or publishes an archive from the caller repository's working directory.
func Run(ctx context.Context, args []string) error {
	return (app{os.Stdout, os.Stderr}).execute(ctx, args)
}

func (a app) execute(ctx context.Context, args []string) error {
	if len(args) < 2 || (args[0] != "test" && args[0] != "publish") ||
		(args[0] == "test" && len(args) != 4) || (args[0] == "publish" && len(args) != 3) {
		return errors.New(usage)
	}
	for _, arg := range args[1:] {
		if arg == "" {
			return errors.New(usage)
		}
	}
	archive, err := filepath.Abs(args[1])
	if err != nil {
		return err
	}
	archive, err = filepath.EvalSymlinks(archive)
	if err != nil {
		return err
	}
	if args[0] == "test" {
		return a.test(ctx, archive, args[2], args[3])
	}
	return a.publish(ctx, archive, args[2])
}

func archiveHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (a app) command(ctx context.Context, capture bool, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stderr = a.stderr
	var output strings.Builder
	cmd.Stdout = a.stdout
	if capture {
		cmd.Stdout = &output
	}
	if err := runProcess(ctx, cmd, 30*time.Second); err != nil {
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return []byte(output.String()), nil
}

type platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
}

var platformRE = regexp.MustCompile(`^linux/[a-z0-9_]+(/[a-zA-Z0-9_.-]+)?$`)

func validatePlatforms(raw []byte, requested string) ([]platform, error) {
	var index struct {
		Manifests []struct {
			Platform platform `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		return nil, fmt.Errorf("decode OCI index: %w", err)
	}
	names := strings.Split(requested, ",")
	runnable := 0
	for _, m := range index.Manifests {
		if m.Platform.OS != "unknown" && m.Platform.Architecture != "unknown" {
			runnable++
		}
	}
	if runnable != len(names) || requested == "" {
		return nil, errors.New("the requested test platforms must cover every runnable image in the archive")
	}
	seen := make(map[int]bool)
	var platforms []platform
	for _, name := range names {
		if !platformRE.MatchString(name) {
			return nil, fmt.Errorf("unsupported test platform: %s", name)
		}
		parts := strings.Split(name, "/")
		p := platform{OS: parts[0], Architecture: parts[1]}
		if len(parts) == 3 {
			p.Variant = parts[2]
		}
		match, count := -1, 0
		for i, m := range index.Manifests {
			if m.Platform.OS == p.OS && m.Platform.Architecture == p.Architecture && (p.Variant == "" || m.Platform.Variant == p.Variant) {
				match, count = i, count+1
			}
		}
		if count != 1 {
			return nil, fmt.Errorf("expected exactly one archive image for %s; found %d", name, count)
		}
		if seen[match] {
			return nil, fmt.Errorf("duplicate test platform: %s", name)
		}
		seen[match] = true
		platforms = append(platforms, p)
	}
	return platforms, nil
}

func (a app) test(ctx context.Context, archive, requested, version string) (err error) {
	receipt := archive + ".tested"
	if err := os.Remove(receipt); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	passed := false
	var images, testIDs []string
	defer func() {
		// Cleanup must still run after cancellation, without hanging indefinitely.
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, id := range testIDs {
			containers, e := a.command(cleanup, true, "docker", "ps", "-aq", "--filter", "label=io.tuxcrate.smoke-test="+id)
			if e != nil {
				fmt.Fprintln(a.stderr, e)
			} else if ids := strings.Fields(string(containers)); len(ids) > 0 {
				a.cleanupCommand(cleanup, append([]string{"rm", "--force", "--volumes"}, ids...)...)
			}
		}
		for i, image := range images {
			if !passed || i != 0 {
				a.cleanupCommand(cleanup, "image", "rm", image)
			}
		}
		if !passed {
			if e := os.Remove(receipt); e != nil && !errors.Is(e, os.ErrNotExist) {
				fmt.Fprintln(a.stderr, e)
			}
		}
	}()
	original, err := archiveHash(archive)
	if err != nil {
		return err
	}
	raw, err := a.command(ctx, true, "skopeo", "inspect", "--raw", "oci-archive:"+archive)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	hook, err := os.Stat("tests/smoke.sh")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hook == nil {
		if _, e := os.Lstat("tests/smoke.sh"); e == nil {
			return errors.New("tests/smoke.sh must be a regular file")
		}
	} else if !hook.Mode().IsRegular() {
		return errors.New("tests/smoke.sh must be a regular file")
	}
	platforms, err := validatePlatforms(raw, requested)
	if err != nil {
		return err
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	names := strings.Split(requested, ",")
	for i, p := range platforms {
		id := fmt.Sprintf("%x-%d", nonce, i)
		image := "tuxcrate-test:" + id
		images = append(images, image)
		options := []string{"--override-os", p.OS, "--override-arch", p.Architecture}
		if p.Variant != "" {
			options = append(options, "--override-variant", p.Variant)
		}
		options = append(options, "copy", "oci-archive:"+archive, "docker-daemon:"+image)
		if _, err := a.command(ctx, false, "skopeo", options...); err != nil {
			return err
		}
		actual, err := a.command(ctx, true, "docker", "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", image)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(actual)) != p.OS+"/"+p.Architecture {
			return fmt.Errorf("Loaded %s instead of %s", strings.TrimSpace(string(actual)), names[i])
		}
		if hook != nil {
			testIDs = append(testIDs, id)
			fmt.Fprintf(a.stdout, "Running tests/smoke.sh for %s (%s).\n", names[i], version)
			if err := a.runHook(ctx, image, version, names[i], id, 5*time.Minute, 30*time.Second); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(a.stdout, "No tests/smoke.sh; retaining Containerfile assertions and platform validation only.")
		}
	}
	current, err := archiveHash(archive)
	if err != nil {
		return err
	}
	if current != original {
		return errors.New("the OCI archive changed during testing; refusing publication")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Keep the two-line receipt compatible with already tested local archives.
	if err := os.WriteFile(receipt, []byte(original+"\n"+digest+"\n"), 0600); err != nil {
		return err
	}
	if output := os.Getenv("GITHUB_OUTPUT"); output != "" {
		f, err := os.OpenFile(output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := fmt.Fprintf(f, "digest=%s\nimage=%s\n", digest, images[0])
		if err := errors.Join(writeErr, f.Close()); err != nil {
			return err
		}
	}
	passed = true
	return nil
}

func (a app) cleanupCommand(ctx context.Context, args ...string) {
	if _, err := a.command(ctx, false, "docker", args...); err != nil {
		fmt.Fprintln(a.stderr, err)
	}
}

func (a app) runHook(ctx context.Context, image, version, platform, id string, timeout, grace time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command("bash", "tests/smoke.sh", image, version, platform)
	for _, variable := range os.Environ() {
		key, _, _ := strings.Cut(variable, "=")
		if key != "GH_TOKEN" && key != "GITHUB_TOKEN" && key != "TUXCRATE_TEST_ID" {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "TUXCRATE_TEST_ID="+id)
	cmd.Stdout, cmd.Stderr = a.stdout, a.stderr
	if err := runProcess(ctx, cmd, grace); err != nil {
		return fmt.Errorf("smoke test for %s: %w", platform, err)
	}
	return nil
}

func (a app) publish(ctx context.Context, archive, destination string) error {
	data, err := os.ReadFile(archive + ".tested")
	if err != nil {
		return fmt.Errorf("No successful test receipt; refusing to upload the image: %w", err)
	}
	// Permit a registry port, but never accept a tag, digest, or transport.
	if strings.ContainsAny(destination, "@ \t\r\n") || strings.Contains(destination, "://") || strings.Contains(filepath.Base(destination), ":") {
		return errors.New("an untagged destination image is required")
	}
	raw, err := a.command(ctx, true, "skopeo", "inspect", "--raw", "oci-archive:"+archive)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	hash, err := archiveHash(archive)
	if err != nil {
		return err
	}
	if string(data) != hash+"\n"+digest+"\n" {
		return errors.New("the OCI archive does not match the tested artifact; refusing publication")
	}
	options := []string{"copy", "--all", "--preserve-digests", "--retry-times", "3"}
	config := os.Getenv("DOCKER_CONFIG")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		config = filepath.Join(home, ".docker")
	}
	authfile := filepath.Join(config, "config.json")
	if info, err := os.Stat(authfile); err == nil && info.Mode().IsRegular() {
		options = append(options, "--authfile", authfile)
	}
	// Copy the complete index and attestations without re-encoding or tagging.
	options = append(options, "oci-archive:"+archive, "docker://"+destination+"@"+digest)
	_, err = a.command(ctx, false, "skopeo", options...)
	return err
}
