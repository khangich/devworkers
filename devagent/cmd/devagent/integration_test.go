package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"devagent/internal/dsl"
)

func TestDevAgentNewAndRunHappyPath(t *testing.T) {
	tempHome := t.TempDir()
	gomodCache := filepath.Join(tempHome, "go", "pkg", "mod")
	t.Cleanup(func() {
		if _, err := os.Stat(gomodCache); os.IsNotExist(err) {
			return
		}
		// The Go toolchain marks module cache contents read-only, which breaks TempDir cleanup.
		filepath.WalkDir(gomodCache, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			mode := os.FileMode(0o644)
			if d.IsDir() {
				mode = 0o755
			}
			if err := os.Chmod(path, mode); err != nil && !os.IsNotExist(err) {
				t.Logf("chmod failed for %s: %v", path, err)
			}
			return nil
		})
	})
	t.Setenv("HOME", tempHome)
	t.Setenv("OPENAI_API_KEY", "test-key")

	repoDir := filepath.Join(tempHome, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	readmePath := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("DevAgent Integration Test Repo\nSecond line"), 0o644); err != nil {
		t.Fatalf("failed to write README: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	binPath := filepath.Join(tempHome, "devagent-bin")

	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/devagent")
	buildCmd.Dir = moduleRoot
	buildCmd.Env = append(os.Environ(), "HOME="+tempHome)
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}

	plan := map[string]interface{}{
		"name":     "repo-summary",
		"repo":     repoDir,
		"cron":     "0 7 * * *",
		"timezone": "Local",
		"steps": []string{
			"echo Repository files: > agent_summary.txt",
			"ls >> agent_summary.txt",
			"head -n 1 README.md >> agent_summary.txt",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]interface{}{
			"output": []map[string]interface{}{
				{
					"content": []map[string]interface{}{
						{
							"type": "json",
							"json": plan,
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	spec := "Summarize this repository and record the output"

	newCmd := exec.Command(binPath, "new", "--base-url", server.URL, spec)
	newCmd.Dir = repoDir
	newCmd.Env = append(os.Environ(), "HOME="+tempHome, "OPENAI_API_KEY=test-key")
	var newOut bytes.Buffer
	newCmd.Stdout = &newOut
	newCmd.Stderr = &newOut
	if err := newCmd.Run(); err != nil {
		t.Fatalf("devagent new failed: %v\n%s", err, newOut.String())
	}

	workflowPath := filepath.Join(repoDir, ".devagent.yml")
	workflow, err := dsl.Load(workflowPath)
	if err != nil {
		t.Fatalf("failed to load workflow: %v", err)
	}
	if workflow.Name != "repo-summary" {
		t.Fatalf("unexpected workflow name: %s", workflow.Name)
	}

	runCmd := exec.Command(binPath, "run")
	runCmd.Dir = repoDir
	runCmd.Env = append(os.Environ(), "HOME="+tempHome)
	var runOut bytes.Buffer
	runCmd.Stdout = &runOut
	runCmd.Stderr = &runOut
	if err := runCmd.Run(); err != nil {
		t.Fatalf("devagent run failed: %v\n%s", err, runOut.String())
	}

	summaryPath := filepath.Join(repoDir, "agent_summary.txt")
	summaryContent, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("failed to read agent summary: %v", err)
	}
	if !strings.Contains(string(summaryContent), "DevAgent Integration Test Repo") {
		t.Fatalf("summary does not contain repo heading: %s", string(summaryContent))
	}

	runsDir := filepath.Join(repoDir, "devagent_runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("failed to read runs dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 run directory, got %d", len(entries))
	}
	runDir := filepath.Join(runsDir, entries[0].Name())
	summaryFile := filepath.Join(runDir, "summary.json")
	data, err := os.ReadFile(summaryFile)
	if err != nil {
		t.Fatalf("failed to read summary.json: %v", err)
	}
	if !strings.Contains(string(data), "\"status\": \"success\"") {
		t.Fatalf("run summary does not indicate success: %s", string(data))
	}
}
