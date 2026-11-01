package main

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestWorkflowPackageVersionAndRevisionLabels(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				ID   string            `yaml:"id"`
				Run  string            `yaml:"run"`
				Uses string            `yaml:"uses"`
				Env  map[string]string `yaml:"env"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, job := range []string{"pull-request", "release"} {
		reserve, build, test, upload, finalize, manifest, tags := -1, -1, -1, -1, -1, -1, -1
		packageVersion := "${{ steps.release.outputs.package-version }}"
		if job == "pull-request" {
			packageVersion = "${{ steps.preview.outputs.package-version }}"
		}
		for i, s := range workflow.Jobs[job].Steps {
			if strings.Contains(s.Run, `container-release" reserve`) {
				reserve = i
				if s.ID != "release" || s.Env["IMAGE_DIGEST"] != "" {
					t.Fatal("reservation must run before the build has a digest")
				}
			}
			if strings.HasPrefix(s.Uses, "docker/build-push-action@") {
				build = i
				if !strings.Contains(s.With["build-args"], "PACKAGE_VERSION="+packageVersion) {
					t.Fatalf("%s is missing the package version build argument", job)
				}
				for _, label := range []string{"org.opencontainers.image.version=" + packageVersion, "org.opencontainers.image.revision=${{ github.sha }}"} {
					if !strings.Contains(s.With["labels"], label+"\n") {
						t.Fatalf("%s is missing label %s", job, label)
					}
				}
			}
			if s.ID == "tested" {
				test = i
				if s.Env["PACKAGE_VERSION"] != packageVersion {
					t.Fatalf("%s must give smoke tests the exact built package version", job)
				}
			}
			if strings.Contains(s.Run, `container-artifact" publish`) {
				upload = i
			}
			if strings.Contains(s.Run, `container-release" finalize`) {
				finalize = i
				if s.Env["RELEASE_TAG"] != "${{ steps.release.outputs.tag }}" || s.Env["IMAGE_DIGEST"] != "${{ steps.tested.outputs.digest }}" {
					t.Fatal("finalization must use the reserved tag and tested digest")
				}
			}
			if strings.Contains(s.Run, `container-release" manifest`) {
				manifest = i
			}
			if s.Env["IMAGE_TAGS"] == "${{ steps.release.outputs.tags }}" {
				tags = i
			}
		}
		if build < 0 || test < 0 {
			t.Fatalf("%s is missing builds or tests", job)
		}
		if job == "pull-request" {
			if reserve != -1 || finalize != -1 {
				t.Fatal("PRs must never reserve or finalize production revisions")
			}
		} else if !(reserve >= 0 && reserve < build && build < test && test < upload && upload < finalize && finalize < manifest && manifest < tags) {
			t.Fatalf("production must reserve -> build -> test -> upload -> finalize -> manifest -> tags; got %d %d %d %d %d %d %d", reserve, build, test, upload, finalize, manifest, tags)
		}
	}
}
