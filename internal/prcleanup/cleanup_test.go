package prcleanup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func digest(n int) string { return fmt.Sprintf("sha256:%064x", n) }

func image(id int, tags ...string) version {
	v := version{ID: int64(id), Name: digest(id)}
	v.Metadata.Container.Tags = tags
	return v
}

type fixture struct {
	versions    []version
	children    map[int][]int
	annotations map[int]map[string]string
	states      map[string]string
	deleted     []int
	prReads     map[string]int
	pages       []int
	accountType string
	missing     bool
	failPath    string
	reopen      bool
	mutate      bool
	listReads   int
}

func (f *fixture) run(t *testing.T, dryRun bool) (string, error) {
	t.Helper()
	f.prReads = map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == f.failPath {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		write := func(value any) { _ = json.NewEncoder(w).Encode(value) }
		switch {
		case r.URL.Path == "/users/tuxcrate":
			accountType := f.accountType
			if accountType == "" {
				accountType = "Organization"
			}
			write(map[string]string{"type": accountType})
		case strings.HasSuffix(r.URL.Path, "/versions"):
			if strings.Contains(r.URL.Path, "/demo/") || !strings.Contains(r.URL.Path, "/demo-pr/") {
				t.Errorf("unexpected package path: %s", r.URL.Path)
			}
			if f.missing {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			f.pages = append(f.pages, page)
			if page == 1 {
				f.listReads++
			}
			start := (page - 1) * 100
			end := min(start+100, len(f.versions))
			batch := append([]version{}, f.versions[start:end]...)
			if f.mutate && f.listReads > 1 && len(batch) > 0 {
				batch[0].ID += 1000
			}
			write(batch)
		case strings.HasPrefix(r.URL.Path, "/repos/tuxcrate/demo/pulls/"):
			pr := strings.TrimPrefix(r.URL.Path, "/repos/tuxcrate/demo/pulls/")
			f.prReads[pr]++
			state := f.states[pr]
			if f.reopen && f.prReads[pr] > 1 {
				state = "open"
			}
			write(map[string]string{"state": state})
		case r.URL.Path == "/token":
			_, password, ok := r.BasicAuth()
			if !ok || password != "github-token" || r.URL.Query().Get("scope") != "repository:tuxcrate/demo-pr:pull" {
				t.Error("incorrect registry token request")
			}
			write(map[string]string{"token": "registry-token"})
		case strings.HasPrefix(r.URL.Path, "/v2/tuxcrate/demo-pr/manifests/"):
			if r.Header.Get("Authorization") != "Bearer registry-token" {
				t.Error("registry manifest request must use registry token")
			}
			var n int
			_, _ = fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/v2/tuxcrate/demo-pr/manifests/"), "sha256:%064x", &n)
			manifests := []map[string]string{}
			for _, child := range f.children[n] {
				manifests = append(manifests, map[string]string{"digest": digest(child)})
			}
			write(map[string]any{"schemaVersion": 2, "manifests": manifests, "annotations": f.annotations[n]})
		case r.Method == "DELETE":
			if r.Header.Get("Authorization") != "Bearer github-token" {
				t.Error("delete must use GitHub token")
			}
			prefix := "/orgs/tuxcrate/packages/container/demo-pr/versions/"
			if f.accountType == "User" {
				prefix = "/users/tuxcrate/packages/container/demo-pr/versions/"
			}
			if !strings.HasPrefix(r.URL.Path, prefix) {
				t.Errorf("unsafe deletion path %s", r.URL.Path)
			}
			id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, prefix))
			f.deleted = append(f.deleted, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	c := &client{http: server.Client(), api: server.URL, registry: server.URL,
		token: "github-token", repository: "tuxcrate/demo", imagePath: "tuxcrate/demo-pr", output: &output}
	err := c.run(dryRun)
	return output.String(), err
}

func TestCleanup(t *testing.T) {
	tests := []struct {
		name string
		f    fixture
		want []int
	}{
		{
			name: "all closed PR tracks and commits including platforms and attestations",
			f: fixture{
				versions: []version{image(1, "pr-12-stable", "pr-12-stable-"+strings.Repeat("a", 40)), image(2), image(3), image(4), image(5, "pr-12-preview"), image(6, "pr-12-stable-"+strings.Repeat("b", 40))},
				children: map[int][]int{1: {2, 3, 4}}, states: map[string]string{"12": "closed"},
			},
			want: []int{2, 3, 4, 1, 5, 6},
		},
		{
			name: "open PR and shared child retained",
			f: fixture{
				versions: []version{image(1, "pr-12-stable"), image(2), image(3), image(4, "pr-123-stable")},
				children: map[int][]int{1: {2, 3}, 4: {3}}, states: map[string]string{"12": "closed", "123": "open"},
			},
			want: []int{2, 1},
		},
		{
			name: "unknown tags and unidentifiable orphans retained",
			f: fixture{
				versions: []version{image(1, "pr-12-stable", "keep"), image(2), image(3), image(4, "pr-12-preview")},
				children: map[int][]int{1: {2}}, states: map[string]string{"12": "closed"},
			},
			want: []int{4},
		},
		{
			name: "version with tags for both closed and open PR retained",
			f:    fixture{versions: []version{image(1, "pr-12-stable", "pr-13-stable")}, states: map[string]string{"12": "closed", "13": "open"}},
		},
		{
			name: "annotated untagged rerun removed with descendants",
			f: fixture{
				versions: []version{image(1), image(2)}, children: map[int][]int{1: {2}}, states: map[string]string{"12": "closed"},
				annotations: map[int]map[string]string{1: {"io.tuxcrate.repository": "tuxcrate/demo", "io.tuxcrate.pull-request": "12"}},
			},
			want: []int{2, 1},
		},
		{
			name: "foreign annotation and legacy untagged index retained",
			f: fixture{
				versions: []version{image(1), image(2), image(3, "pr-12-stable")}, children: map[int][]int{1: {2}, 3: {2}}, states: map[string]string{"12": "closed"},
				annotations: map[int]map[string]string{1: {"io.tuxcrate.repository": "someone/else", "io.tuxcrate.pull-request": "12"}},
			},
			want: []int{3},
		},
		{
			name: "partially deleted children do not prevent retry",
			f:    fixture{versions: []version{image(1, "pr-12-stable"), image(3)}, children: map[int][]int{1: {2, 3}}, states: map[string]string{"12": "closed"}},
			want: []int{3, 1},
		},
		{
			name: "user owned repository",
			f:    fixture{versions: []version{image(1, "pr-12-stable")}, states: map[string]string{"12": "closed"}, accountType: "User"},
			want: []int{1},
		},
		{name: "absent package", f: fixture{missing: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.f.run(t, false); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tt.f.deleted, tt.want) {
				t.Fatalf("deleted %v, want %v", tt.f.deleted, tt.want)
			}
		})
	}
}

func TestDryRun(t *testing.T) {
	f := fixture{versions: []version{image(1, "pr-12-stable")}, states: map[string]string{"12": "closed"}}
	output, err := f.run(t, true)
	if err != nil || len(f.deleted) != 0 || !strings.Contains(output, "dry-run=true") {
		t.Fatalf("output=%s deleted=%v err=%v", output, f.deleted, err)
	}
}

func TestFailsClosed(t *testing.T) {
	for _, scenario := range []string{"PR API failure", "registry failure", "package API failure", "reopened", "changed snapshot", "unknown PR state", "delete denied"} {
		t.Run(scenario, func(t *testing.T) {
			f := fixture{versions: []version{image(1, "pr-12-stable"), image(2)}, children: map[int][]int{1: {2}}, states: map[string]string{"12": "closed"}}
			switch scenario {
			case "PR API failure":
				f.failPath = "/repos/tuxcrate/demo/pulls/12"
			case "registry failure":
				f.failPath = "/v2/tuxcrate/demo-pr/manifests/" + digest(2)
			case "package API failure":
				f.failPath = "/orgs/tuxcrate/packages/container/demo-pr/versions"
			case "reopened":
				f.reopen = true
			case "changed snapshot":
				f.mutate = true
			case "unknown PR state":
				f.states["12"] = ""
			case "delete denied":
				f.failPath = "/orgs/tuxcrate/packages/container/demo-pr/versions/2"
			}
			if _, err := f.run(t, false); err == nil || len(f.deleted) != 0 {
				t.Fatalf("deleted=%v err=%v", f.deleted, err)
			}
		})
	}
}

func TestPagination(t *testing.T) {
	f := fixture{states: map[string]string{"12": "closed"}}
	for i := 1; i <= 101; i++ {
		f.versions = append(f.versions, image(i, "pr-12-stable-"+strconv.Itoa(i)))
	}
	if _, err := f.run(t, false); err != nil {
		t.Fatal(err)
	}
	if len(f.deleted) != 101 || !reflect.DeepEqual(f.pages, []int{1, 2, 1, 2}) {
		t.Fatalf("deleted=%d pages=%v", len(f.deleted), f.pages)
	}
}

func TestRunRequiresRepositoryAndToken(t *testing.T) {
	for _, repo := range []string{"", "owner", "../other/repo"} {
		if err := Run(repo, "token", true, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted invalid repository %q", repo)
		}
	}
	if err := Run("tuxcrate/demo", "", true, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted missing token")
	}
}
