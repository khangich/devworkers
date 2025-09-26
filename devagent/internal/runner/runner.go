package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"devagent/internal/dsl"
	"devagent/internal/util"
)

// Summary represents the run summary stored on disk.
type Summary struct {
	Name      string        `json:"name"`
	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
	Status    string        `json:"status"`
	Steps     []StepSummary `json:"steps"`
	Repo      string        `json:"repo"`
}

// StepSummary captures details about an executed step.
type StepSummary struct {
	Cmd         string  `json:"cmd"`
	ExitCode    int     `json:"exit_code"`
	DurationSec float64 `json:"duration_sec"`
}

// Options controls run behaviour.
type Options struct {
	Workflow *dsl.Workflow
	Stdout   io.Writer
}

// Run executes the workflow steps sequentially and records output files.
func Run(ctx context.Context, opts Options) (*Summary, error) {
	if opts.Workflow == nil {
		return nil, errors.New("workflow is required")
	}
	repo, err := opts.Workflow.ExpandRepo()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(repo); err != nil {
		return nil, fmt.Errorf("repo path %s not accessible: %w", repo, err)
	}

	runDir := filepath.Join(repo, "devagent_runs", util.Timestamp())
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, err
	}

	logPath := filepath.Join(runDir, "run.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	var outputWriter io.Writer = logFile
	if opts.Stdout != nil {
		outputWriter = io.MultiWriter(logFile, opts.Stdout)
	}

	summary := &Summary{
		Name:  opts.Workflow.Name,
		Repo:  repo,
		Steps: make([]StepSummary, 0, len(opts.Workflow.Steps)),
	}
	summary.StartedAt = time.Now().UTC()

	status := "success"

	for _, step := range opts.Workflow.Steps {
		cmdText := strings.TrimSpace(step.Run)
		if cmdText == "" {
			continue
		}
		fmt.Fprintf(outputWriter, "$ %s\n", redact(cmdText))

		cmd := exec.CommandContext(ctx, "bash", "-lc", cmdText)
		cmd.Dir = repo
		cmd.Env = sanitizedEnv()

		logOut := newRedactingWriter(outputWriter)
		cmd.Stdout = logOut
		cmd.Stderr = logOut

		stepStart := time.Now()
		err := cmd.Run()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				return nil, err
			}
		}
		if flushErr := logOut.Flush(); flushErr != nil {
			return nil, flushErr
		}

		summary.Steps = append(summary.Steps, StepSummary{
			Cmd:         cmdText,
			ExitCode:    exitCode,
			DurationSec: time.Since(stepStart).Seconds(),
		})

		if err != nil {
			status = "failed"
			break
		}
	}

	summary.EndedAt = time.Now().UTC()
	summary.Status = status

	summaryPath := filepath.Join(runDir, "summary.json")
	if err := writeSummary(summaryPath, summary); err != nil {
		return nil, err
	}

	if opts.Workflow.Outputs != nil {
		for _, candidate := range opts.Workflow.Outputs.CopyIfExists {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			src := filepath.Join(repo, candidate)
			if _, err := os.Stat(src); err == nil {
				dst := filepath.Join(runDir, filepath.Base(candidate))
				_ = copyFile(src, dst)
			}
		}
	}

	return summary, nil
}

func writeSummary(path string, summary *Summary) error {
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0o644)
}

var redactionPattern = regexp.MustCompile(`(?i)(api[_-]?key|token|secret)=\S+`)

func redact(s string) string {
	return redactionPattern.ReplaceAllString(s, "$1=<redacted>")
}

func sanitizedEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		key := strings.ToUpper(parts[0])
		if strings.Contains(key, "SECRET") || strings.Contains(key, "TOKEN") || strings.Contains(key, "KEY") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

type redactingWriter struct {
	mu  sync.Mutex
	w   io.Writer
	buf bytes.Buffer
}

func newRedactingWriter(w io.Writer) *redactingWriter {
	return &redactingWriter{w: w}
}

func (rw *redactingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	n, err := rw.buf.Write(p)
	if err != nil {
		return n, err
	}
	return len(p), rw.flushLines()
}

func (rw *redactingWriter) flushLines() error {
	for {
		data := rw.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			return nil
		}
		line := string(data[:idx])
		if _, err := fmt.Fprintf(rw.w, "%s\n", redact(line)); err != nil {
			return err
		}
		rw.buf.Next(idx + 1)
	}
}

func (rw *redactingWriter) Flush() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.buf.Len() == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(rw.w, "%s\n", redact(strings.TrimRight(rw.buf.String(), "\n"))); err != nil {
		return err
	}
	rw.buf.Reset()
	return nil
}
