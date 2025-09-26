package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"devagent/internal/dsl"
	"devagent/internal/planner"
	"devagent/internal/runner"
	"devagent/internal/scheduler"
	"devagent/internal/store"
)

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "new":
		doNew(args)
	case "run":
		doRun(args)
	case "schedule":
		doSchedule(args)
	case "daemon":
		doDaemon()
	case "plan":
		doPlan(args)
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("Usage: devagent <command> [options]")
	fmt.Println("Commands: new, run, schedule, daemon, plan")
}

func doNew(args []string) {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	var (
		cronFlag    = fs.String("cron", "", "cron expression fallback")
		repoFlag    = fs.String("repo", "", "repository path")
		nameFlag    = fs.String("name", "", "workflow name")
		approveFlag = fs.Bool("approve", false, "approve writing the workflow")
		yesFlag     = fs.Bool("yes", false, "skip approval prompt")
		tzFlag      = fs.String("timezone", "", "timezone override")
		modelFlag   = fs.String("model", "", "planner model")
		baseURLFlag = fs.String("base-url", "", "planner base URL")
	)
	var steps stringList
	fs.Var(&steps, "step", "command step (repeatable)")
	var copies stringList
	fs.Var(&copies, "copy", "output file to copy (repeatable)")
	fs.Parse(args)

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Println("provide a natural language specification")
		os.Exit(1)
	}
	spec := remaining[0]

	apiKey := os.Getenv("OPENAI_API_KEY")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	plan, err := planner.PlanFromSpec(ctx, spec, planner.Options{
		Name:      *nameFlag,
		CronHint:  *cronFlag,
		RepoHint:  *repoFlag,
		StepHints: steps,
		Timezone:  *tzFlag,
		APIKey:    apiKey,
		Model:     *modelFlag,
		BaseURL:   *baseURLFlag,
	})
	if err != nil {
		fmt.Printf("planner error: %v\n", err)
		os.Exit(1)
	}

	if plan.Name == "" {
		plan.Name = "devagent-job"
	}

	if len(plan.Steps) == 0 {
		fmt.Println("no steps resolved")
		os.Exit(1)
	}

	workflow := &dsl.Workflow{
		Version: 1,
		Name:    plan.Name,
		Repo:    plan.Repo,
		Schedule: dsl.Schedule{
			Natural:  plan.Natural,
			Cron:     plan.Cron,
			Timezone: plan.Timezone,
		},
		Steps: make([]dsl.Step, 0, len(plan.Steps)),
	}
	for _, step := range plan.Steps {
		workflow.Steps = append(workflow.Steps, dsl.Step{Run: step})
	}
	if len(copies) > 0 {
		workflow.Outputs = &dsl.Outputs{CopyIfExists: copies}
	}

	yamlBytes, err := yaml.Marshal(workflow)
	if err != nil {
		fmt.Printf("failed to render YAML: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(yamlBytes))
	if !*approveFlag && !*yesFlag {
		fmt.Println("rerun with --approve to save this plan")
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Printf("cwd error: %v\n", err)
		os.Exit(1)
	}
	yamlPath := filepath.Join(cwd, ".devagent.yml")
	if err := dsl.Save(yamlPath, workflow); err != nil {
		fmt.Printf("failed to write workflow: %v\n", err)
		os.Exit(1)
	}

	st, err := store.Open()
	if err != nil {
		fmt.Printf("failed to open state store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	job := store.NewJob(workflow.Name, workflow.Repo, workflow.Schedule.Cron, workflow.Schedule.Natural, workflow.Schedule.Timezone, yamlPath)
	if err := st.UpsertJob(context.Background(), job); err != nil {
		fmt.Printf("failed to register job: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("workflow saved to %s and scheduled\n", yamlPath)
}

func doRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.Bool("once", false, "deprecated flag")
	fs.Parse(args)

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Printf("cwd error: %v\n", err)
		os.Exit(1)
	}
	yamlPath := filepath.Join(cwd, ".devagent.yml")
	workflow, err := dsl.Load(yamlPath)
	if err != nil {
		fmt.Printf("load error: %v\n", err)
		os.Exit(1)
	}

	summary, err := runner.Run(context.Background(), runner.Options{Workflow: workflow, Stdout: os.Stdout})
	if err != nil {
		fmt.Printf("run error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("run finished with status %s\n", summary.Status)

	st, err := store.Open()
	if err == nil {
		defer st.Close()
		_ = st.UpdateRunResult(context.Background(), workflow.Name, summary.Status, time.Now())
	}
}

func doSchedule(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: devagent schedule <list|remove>")
		os.Exit(1)
	}
	sub := args[0]
	st, err := store.Open()
	if err != nil {
		fmt.Printf("failed to open state: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	switch sub {
	case "list":
		jobs, err := st.ListJobs(context.Background())
		if err != nil {
			fmt.Printf("list error: %v\n", err)
			os.Exit(1)
		}
		if len(jobs) == 0 {
			fmt.Println("no jobs scheduled")
			return
		}
		for _, job := range jobs {
			last := "never"
			if job.LastRun.Valid {
				last = job.LastRun.Time.Format(time.RFC3339)
			}
			status := "unknown"
			if job.LastStatus.Valid {
				status = job.LastStatus.String
			}
			fmt.Printf("%s\t%s\tcron=%s\tlast=%s\n", job.Name, job.Repo, job.Cron(), fmt.Sprintf("%s (%s)", last, status))
		}
	case "remove":
		if len(args) < 2 {
			fmt.Println("provide a job name to remove")
			os.Exit(1)
		}
		name := args[1]
		if err := st.RemoveJob(context.Background(), name); err != nil {
			fmt.Printf("remove error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("removed", name)
	default:
		fmt.Println("Usage: devagent schedule <list|remove>")
		os.Exit(1)
	}
}

func doDaemon() {
	st, err := store.Open()
	if err != nil {
		log.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	daemon := scheduler.New(st, log.New(os.Stdout, "devagent ", log.LstdFlags))

	ctx, cancel := signalContext()
	defer cancel()

	if err := daemon.Run(ctx); err != nil {
		log.Fatalf("daemon error: %v", err)
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}

func doPlan(args []string) {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	var (
		cronFlag    = fs.String("cron", "", "cron expression fallback")
		repoFlag    = fs.String("repo", "", "repository path")
		nameFlag    = fs.String("name", "", "workflow name")
		tzFlag      = fs.String("timezone", "", "timezone override")
		modelFlag   = fs.String("model", "", "planner model")
		baseURLFlag = fs.String("base-url", "", "planner base URL")
	)
	var steps stringList
	fs.Var(&steps, "step", "command step (repeatable)")
	fs.Parse(args)

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Println("provide a natural language specification")
		os.Exit(1)
	}
	spec := remaining[0]

	apiKey := os.Getenv("OPENAI_API_KEY")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	plan, err := planner.PlanFromSpec(ctx, spec, planner.Options{
		Name:      *nameFlag,
		CronHint:  *cronFlag,
		RepoHint:  *repoFlag,
		StepHints: steps,
		Timezone:  *tzFlag,
		APIKey:    apiKey,
		Model:     *modelFlag,
		BaseURL:   *baseURLFlag,
	})
	if err != nil {
		fmt.Printf("planner error: %v\n", err)
		os.Exit(1)
	}

	workflow := &dsl.Workflow{
		Version: 1,
		Name:    plan.Name,
		Repo:    plan.Repo,
		Schedule: dsl.Schedule{
			Natural:  plan.Natural,
			Cron:     plan.Cron,
			Timezone: plan.Timezone,
		},
		Steps: make([]dsl.Step, 0, len(plan.Steps)),
	}
	for _, step := range plan.Steps {
		workflow.Steps = append(workflow.Steps, dsl.Step{Run: step})
	}

	out, err := yaml.Marshal(workflow)
	if err != nil {
		fmt.Printf("failed to marshal workflow: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(out))
}
