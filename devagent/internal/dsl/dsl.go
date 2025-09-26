package dsl

import (
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Workflow represents the persisted YAML specification for a DevAgent job.
type Workflow struct {
	Version  int      `yaml:"version"`
	Name     string   `yaml:"name"`
	Repo     string   `yaml:"repo"`
	Schedule Schedule `yaml:"schedule"`
	Steps    []Step   `yaml:"steps"`
	Outputs  *Outputs `yaml:"outputs,omitempty"`
}

// Schedule describes when a job should run.
type Schedule struct {
	Natural  string `yaml:"natural,omitempty"`
	Cron     string `yaml:"cron"`
	Timezone string `yaml:"timezone,omitempty"`
}

// Step represents a shell command step.
type Step struct {
	Run string `yaml:"run"`
}

// Outputs configures optional output copying.
type Outputs struct {
	CopyIfExists []string `yaml:"copy_if_exists,omitempty"`
}

// Load reads a workflow from disk.
func Load(path string) (*Workflow, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var wf Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, err
	}
	if wf.Name == "" {
		return nil, errors.New("workflow name is required")
	}
	if wf.Repo == "" {
		return nil, errors.New("workflow repo is required")
	}
	if wf.Schedule.Cron == "" {
		return nil, errors.New("workflow schedule cron is required")
	}
	return &wf, nil
}

// Save writes the workflow to disk with standard permissions.
func Save(path string, wf *Workflow) error {
	if wf == nil {
		return errors.New("workflow is nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(wf)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0o644)
}

// ExpandRepo resolves the workflow repo path, expanding the tilde when present.
func (wf *Workflow) ExpandRepo() (string, error) {
	if wf == nil {
		return "", errors.New("workflow is nil")
	}
	repo := strings.TrimSpace(wf.Repo)
	if strings.HasPrefix(repo, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		repo = filepath.Join(home, strings.TrimPrefix(repo, "~"))
	}
	return filepath.Clean(os.ExpandEnv(repo)), nil
}
