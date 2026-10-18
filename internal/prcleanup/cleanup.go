// Package prcleanup deletes only preview manifests attributable to closed PRs.
package prcleanup

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var previewTag = regexp.MustCompile(`^pr-([1-9][0-9]*)-[a-z][a-z0-9-]*$`)
var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var manifestDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var pullNumber = regexp.MustCompile(`^[1-9][0-9]*$`)

type version struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Metadata struct {
		Container struct {
			Tags []string `json:"tags"`
		} `json:"container"`
	} `json:"metadata"`
}

type client struct {
	http                  *http.Client
	api, registry, token  string
	registryToken         string
	repository, imagePath string
	packagePath           string
	output                io.Writer
	known                 map[string]bool
	annotations           map[string]map[string]string
}

// Run discovers all tracks and historical commits from GHCR, not current meta.yaml.
func Run(repository, token string, dryRun bool, output io.Writer) error {
	if !repositoryName.MatchString(repository) || token == "" {
		return fmt.Errorf("GITHUB_REPOSITORY (owner/repo) and GH_TOKEN are required")
	}
	c := &client{
		http: &http.Client{Timeout: 30 * time.Second},
		api:  "https://api.github.com", registry: "https://ghcr.io", token: token,
		repository: repository, imagePath: strings.ToLower(repository) + "-pr", output: output,
	}
	return c.run(dryRun)
}

func (c *client) request(method, endpoint, token string, result any) (int, error) {
	req, err := http.NewRequest(method, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if strings.HasPrefix(endpoint, c.registry+"/token?") {
		owner, _, _ := strings.Cut(c.repository, "/")
		req.SetBasicAuth(owner, token)
	}
	req.Header.Set("Accept", "application/vnd.github+json, application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s %s: HTTP %d", method, endpoint, resp.StatusCode)
	}
	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s: %w", endpoint, err)
		}
	}
	return resp.StatusCode, nil
}

func (c *client) run(dryRun bool) error {
	owner, name, _ := strings.Cut(c.repository, "/")
	var account struct{ Type string }
	if _, err := c.request("GET", c.api+"/users/"+owner, c.token, &account); err != nil {
		return err
	}
	prefix := "/users/"
	if account.Type == "Organization" {
		prefix = "/orgs/"
	}
	c.packagePath = c.api + prefix + owner + "/packages/container/" + url.PathEscape(strings.ToLower(name)+"-pr")
	versions, err := c.listVersions()
	if err != nil {
		return err
	}
	c.known = map[string]bool{}
	for _, v := range versions {
		if !manifestDigest.MatchString(v.Name) {
			return fmt.Errorf("invalid package version digest %q", v.Name)
		}
		c.known[v.Name] = true
	}
	if len(versions) == 0 {
		fmt.Fprintln(c.output, "No preview images found.")
		return nil
	}
	// Registry manifests identify untagged platforms and attestations precisely.
	// Never infer ownership of an untagged version from its age or list position.
	var auth struct{ Token string }
	if _, err := c.request("GET", c.registry+"/token?service=ghcr.io&scope="+url.QueryEscape("repository:"+c.imagePath+":pull"), c.token, &auth); err != nil {
		return err
	}
	if auth.Token == "" {
		return fmt.Errorf("GHCR returned an empty registry token")
	}
	c.registryToken = auth.Token
	graph := map[string][]string{}
	c.annotations = map[string]map[string]string{}
	for _, v := range versions {
		if err := c.loadGraph(v.Name, graph); err != nil {
			return err // Complete discovery before any destructive request.
		}
	}
	closed := map[string]bool{}
	candidates := map[string]bool{}
	roots := map[string]bool{}
	referenced := map[string]bool{}
	for _, children := range graph {
		for _, child := range children {
			referenced[child] = true
		}
	}
	for _, v := range versions {
		tags := v.Metadata.Container.Tags
		// Index annotations retain PR identity when reruns replace all its tags.
		// Older, unidentifiable untagged images are deliberately preserved.
		annotation := c.annotations[v.Name]
		if len(tags) == 0 && len(graph[v.Name]) > 0 && annotation["io.tuxcrate.repository"] == c.repository {
			pr := annotation["io.tuxcrate.pull-request"]
			if pullNumber.MatchString(pr) {
				tags = []string{"pr-" + pr + "-annotated"}
			}
		}
		roots[v.Name] = len(tags) > 0 || (len(graph[v.Name]) > 0 && !referenced[v.Name])
		eligible := len(tags) > 0
		for _, tag := range tags {
			match := previewTag.FindStringSubmatch(tag)
			if match == nil {
				eligible = false
				continue
			}
			pr := match[1]
			if _, ok := closed[pr]; !ok {
				var pull struct{ State string }
				if _, err := c.request("GET", c.api+"/repos/"+c.repository+"/pulls/"+pr, c.token, &pull); err != nil {
					return err // Unknown state must never authorize deletion.
				}
				if pull.State != "open" && pull.State != "closed" {
					return fmt.Errorf("unexpected state for PR %s: %q", pr, pull.State)
				}
				closed[pr] = pull.State == "closed"
			}
			eligible = eligible && closed[pr]
		}
		if eligible {
			candidates[v.Name] = true
		}
	}
	if len(candidates) == 0 {
		fmt.Fprintln(c.output, "No closed-PR preview images found.")
		return nil
	}
	remove, keep := map[string]bool{}, map[string]bool{}
	var order []string
	var visit func(string, map[string]bool)
	visit = func(digest string, set map[string]bool) {
		if set[digest] {
			return
		}
		set[digest] = true
		for _, child := range graph[digest] {
			visit(child, set)
		}
	}
	for _, v := range versions {
		if candidates[v.Name] {
			visit(v.Name, remove)
		} else if roots[v.Name] {
			visit(v.Name, keep)
		}
	}
	byDigest := map[string]version{}
	for _, v := range versions {
		byDigest[v.Name] = v
	}
	seen := map[string]bool{}
	var postorder func(string)
	postorder = func(digest string) {
		if seen[digest] {
			return
		}
		seen[digest] = true
		for _, child := range graph[digest] {
			postorder(child)
		}
		if remove[digest] && !keep[digest] {
			if _, ok := byDigest[digest]; ok {
				order = append(order, digest)
			}
		}
	}
	for _, v := range versions {
		if candidates[v.Name] {
			postorder(v.Name)
		}
	}
	// A reopened PR must be retained, even if it was closed during discovery.
	for pr, wasClosed := range closed {
		if !wasClosed {
			continue
		}
		var pull struct{ State string }
		if _, err := c.request("GET", c.api+"/repos/"+c.repository+"/pulls/"+pr, c.token, &pull); err != nil {
			return err
		}
		if pull.State != "closed" {
			return fmt.Errorf("PR %s is no longer closed; retry cleanup with fresh state", pr)
		}
	}
	// Publishing may have moved tags or introduced new shared manifests while
	// discovery ran. Abort instead of deleting from a known-stale snapshot.
	fresh, err := c.listVersions()
	if err != nil {
		return err
	}
	if len(fresh) != len(versions) {
		return fmt.Errorf("preview package changed during discovery; retry cleanup")
	}
	for _, v := range fresh {
		old, ok := byDigest[v.Name]
		if !ok || old.ID != v.ID || !slices.Equal(old.Metadata.Container.Tags, v.Metadata.Container.Tags) {
			return fmt.Errorf("preview package changed during discovery; retry cleanup")
		}
	}
	for _, digest := range order {
		v := byDigest[digest]
		fmt.Fprintf(c.output, "Delete ghcr.io/%s@%s (version %d, tags %v, dry-run=%t)\n", c.imagePath, digest, v.ID, v.Metadata.Container.Tags, dryRun)
		if !dryRun {
			// Delete children before parents so failed sweeps remain discoverable.
			if _, err := c.request("DELETE", c.packagePath+"/versions/"+strconv.FormatInt(v.ID, 10), c.token, nil); err != nil {
				return fmt.Errorf("delete preview version (the repository needs package admin access): %w", err)
			}
		}
	}
	return nil
}

func (c *client) listVersions() ([]version, error) {
	var versions []version
	for page := 1; ; page++ {
		var batch []version
		status, err := c.request("GET", c.packagePath+"/versions?per_page=100&page="+strconv.Itoa(page), c.token, &batch)
		if page == 1 && status == http.StatusNotFound {
			fmt.Fprintln(c.output, "Preview package absent or inaccessible; nothing deleted.")
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		versions = append(versions, batch...)
		if len(batch) < 100 {
			return versions, nil
		}
	}
}

func (c *client) loadGraph(digest string, graph map[string][]string) error {
	if _, ok := graph[digest]; ok {
		return nil
	}
	var manifest struct {
		SchemaVersion int               `json:"schemaVersion"`
		Annotations   map[string]string `json:"annotations"`
		Manifests     []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if _, err := c.request("GET", c.registry+"/v2/"+c.imagePath+"/manifests/"+url.PathEscape(digest), c.registryToken, &manifest); err != nil {
		return err
	}
	if manifest.SchemaVersion != 2 {
		return fmt.Errorf("unsupported manifest schema for %s", digest)
	}
	graph[digest] = nil
	c.annotations[digest] = manifest.Annotations
	for _, child := range manifest.Manifests {
		if !manifestDigest.MatchString(child.Digest) {
			return fmt.Errorf("invalid child digest %q", child.Digest)
		}
		graph[digest] = append(graph[digest], child.Digest)
		// Previously deleted children may still be referenced after a partial
		// cleanup. Only package versions present in this snapshot need reading.
		if c.known[child.Digest] {
			if err := c.loadGraph(child.Digest, graph); err != nil {
				return err
			}
		}
	}
	return nil
}
