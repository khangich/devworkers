package scheduler

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"devagent/internal/dsl"
	"devagent/internal/runner"
	"devagent/internal/store"
	"devagent/internal/util"
)

// Daemon coordinates scheduled workflow executions.
type Daemon struct {
	store  *store.Store
	cron   *cron.Cron
	logger *log.Logger
	jobs   map[string]cron.EntryID
	mu     sync.Mutex
	parser cron.Parser
}

// New creates a new daemon instance.
func New(st *store.Store, logger *log.Logger) *Daemon {
	if logger == nil {
		logger = log.New(os.Stdout, "devagent ", log.LstdFlags)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return &Daemon{
		store:  st,
		cron:   cron.New(),
		logger: logger,
		jobs:   make(map[string]cron.EntryID),
		parser: parser,
	}
}

// Run starts the daemon loop until the context is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if d.store == nil {
		return errors.New("scheduler store is nil")
	}
	d.logger.Println("daemon starting")
	d.cron.Start()
	defer d.cron.Stop()

	if err := d.reload(ctx); err != nil {
		d.logger.Printf("initial load error: %v", err)
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Println("daemon stopping")
			return nil
		case <-ticker.C:
			if err := d.reload(ctx); err != nil {
				d.logger.Printf("reload error: %v", err)
			}
		}
	}
}

func (d *Daemon) reload(ctx context.Context) error {
	jobs, err := d.store.JobsForSchedule(ctx)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	existing := make(map[string]struct{}, len(d.jobs))
	for name := range d.jobs {
		existing[name] = struct{}{}
	}

	for _, job := range jobs {
		delete(existing, job.Name)
		if _, ok := d.jobs[job.Name]; ok {
			continue
		}
		if err := d.scheduleJob(job); err != nil {
			d.logger.Printf("schedule job %s: %v", job.Name, err)
		}
	}

	for name := range existing {
		if entryID, ok := d.jobs[name]; ok {
			d.cron.Remove(entryID)
			delete(d.jobs, name)
		}
	}

	return nil
}

func (d *Daemon) scheduleJob(job store.Job) error {
	sched, err := d.parser.Parse(job.Cron())
	if err != nil {
		return err
	}
	loc := util.ResolveLocation(job.Timezone())
	entryID := d.cron.Schedule(sched, cron.FuncJob(func() { d.execute(job, loc) }))
	d.jobs[job.Name] = entryID
	d.logger.Printf("scheduled %s (%s)", job.Name, job.Cron())
	return nil
}

func (d *Daemon) execute(job store.Job, loc *time.Location) {
	lock, err := acquireLock(job.Name)
	if err != nil {
		if errors.Is(err, errAlreadyRunning) {
			d.logger.Printf("job %s already running", job.Name)
			return
		}
		d.logger.Printf("lock error for %s: %v", job.Name, err)
		return
	}
	defer releaseLock(lock)

	ctx := context.Background()
	wf, err := dsl.Load(job.YAMLPath())
	if err != nil {
		d.logger.Printf("load workflow %s: %v", job.Name, err)
		return
	}

	summary, err := runner.Run(ctx, runner.Options{Workflow: wf})
	if err != nil {
		d.logger.Printf("run %s error: %v", job.Name, err)
		_ = d.store.UpdateRunResult(context.Background(), job.Name, "failed", time.Now().In(loc))
		return
	}

	status := summary.Status
	_ = d.store.UpdateRunResult(context.Background(), job.Name, status, time.Now().In(loc))
	d.logger.Printf("job %s finished with %s", job.Name, status)
}

var errAlreadyRunning = errors.New("job already running")

func acquireLock(name string) (*os.File, error) {
	dir, err := store.LocksDir()
	if err != nil {
		return nil, err
	}
	fileName := sanitizeName(name) + ".lock"
	path := filepath.Join(dir, fileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errAlreadyRunning
		}
		return nil, err
	}
	return f, nil
}

func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

func sanitizeName(name string) string {
	name = strings.ToLower(name)
	replacer := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-", "..", "-")
	return replacer.Replace(name)
}
