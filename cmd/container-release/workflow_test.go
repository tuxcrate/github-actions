package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestWorkflowRequiresRootMetadata(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/container.yml")
	if err != nil {
		t.Fatal(err)
	}
	type step struct {
		Name             string            `yaml:"name"`
		WorkingDirectory string            `yaml:"working-directory"`
		Run              string            `yaml:"run"`
		Env              map[string]string `yaml:"env"`
	}
	var workflow struct {
		On struct {
			Call struct {
				Inputs map[string]any `yaml:"inputs"`
			} `yaml:"workflow_call"`
		} `yaml:"on"`
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for input := range workflow.On.Call.Inputs {
		if input != "shared-ref" && input != "website-repository" {
			t.Fatalf("workflow accepts image configuration input %s", input)
		}
	}
	var required, planned bool
	for _, s := range workflow.Jobs["plan"].Steps {
		switch s.Name {
		case "Require root meta.yaml":
			required = true
			if s.WorkingDirectory != "source" {
				t.Fatal("metadata preflight must run in the caller repository root")
			}
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "nested"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "nested", "meta.yaml"), []byte("tracks: []\n"), 0644); err != nil {
				t.Fatal(err)
			}
			check := func() error {
				cmd := exec.Command("bash", "-c", s.Run)
				cmd.Dir = dir
				return cmd.Run()
			}
			if err := check(); err == nil {
				t.Fatal("accepted missing root metadata with only a nested meta.yaml")
			}
			if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte("tracks: []\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := check(); err != nil {
				t.Fatalf("rejected root metadata: %v", err)
			}
		case "Validate root meta.yaml and alias ownership":
			planned = true
			if !required || s.WorkingDirectory != "source" || len(s.Env) != 0 {
				t.Fatal("planner must validate the fixed root metadata after the preflight")
			}
		}
	}
	if !required || !planned {
		t.Fatal("workflow must require and validate root metadata")
	}
}
