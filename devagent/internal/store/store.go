package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database used by the daemon.
type Store struct {
	db *sql.DB
}

// Job represents a scheduled workflow.
type Job struct {
	Name       string
	Repo       string
	cron       string
	natural    string
	timezone   string
	yamlPath   string
	LastStatus sql.NullString
	LastRun    sql.NullTime
	UpdatedAt  time.Time
}

// NewJob constructs a Job instance.
func NewJob(name, repo, cron, natural, timezone, yamlPath string) Job {
	return Job{
		Name:     name,
		Repo:     repo,
		cron:     cron,
		natural:  natural,
		timezone: timezone,
		yamlPath: yamlPath,
	}
}

// Cron returns the cron specification.
func (j Job) Cron() string { return j.cron }

// Natural returns the natural language description.
func (j Job) Natural() string { return j.natural }

// Timezone returns the timezone string.
func (j Job) Timezone() string { return j.timezone }

// YAMLPath returns the workflow file path.
func (j Job) YAMLPath() string { return j.yamlPath }

// Open initialises the database at the default path.
func Open() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".devagent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.ensureSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the underlying database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) ensureSchema() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS jobs (
name TEXT PRIMARY KEY,
repo TEXT NOT NULL,
cron TEXT NOT NULL,
natural TEXT,
timezone TEXT,
yaml_path TEXT NOT NULL,
last_status TEXT,
last_run TIMESTAMP,
updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`)
	return err
}

// UpsertJob stores or updates a job definition.
func (s *Store) UpsertJob(ctx context.Context, job Job) error {
	if s == nil {
		return errors.New("store is nil")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO jobs(name, repo, cron, natural, timezone, yaml_path, updated_at)
VALUES(?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(name) DO UPDATE SET
repo=excluded.repo,
cron=excluded.cron,
natural=excluded.natural,
timezone=excluded.timezone,
yaml_path=excluded.yaml_path,
updated_at=CURRENT_TIMESTAMP;
`, job.Name, job.Repo, job.cron, job.natural, job.timezone, job.yamlPath)
	return err
}

// ListJobs returns all jobs.
func (s *Store) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT name, repo, cron, natural, timezone, yaml_path, last_status, last_run, updated_at
FROM jobs
ORDER BY name
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.Name, &job.Repo, &job.cron, &job.natural, &job.timezone, &job.yamlPath, &job.LastStatus, &job.LastRun, &job.UpdatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// RemoveJob deletes a job by name.
func (s *Store) RemoveJob(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE name = ?`, name)
	return err
}

// UpdateRunResult stores the outcome of a job run.
func (s *Store) UpdateRunResult(ctx context.Context, name, status string, runAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE jobs SET last_status = ?, last_run = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?
`, status, runAt.UTC(), name)
	return err
}

// GetJob fetches a job by name.
func (s *Store) GetJob(ctx context.Context, name string) (*Job, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT name, repo, cron, natural, timezone, yaml_path, last_status, last_run, updated_at
FROM jobs
WHERE name = ?
`, name)
	var job Job
	if err := row.Scan(&job.Name, &job.Repo, &job.cron, &job.natural, &job.timezone, &job.yamlPath, &job.LastStatus, &job.LastRun, &job.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

// JobsForSchedule returns all jobs without ordering constraints.
func (s *Store) JobsForSchedule(ctx context.Context) ([]Job, error) {
	return s.ListJobs(ctx)
}

// LocksDir returns the directory used for lock files.
func LocksDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".devagent", "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// StatePath returns the database path for documentation.
func StatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".devagent", "state.db"), nil
}
