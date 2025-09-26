package planner

import (
	"context"
	"testing"
)

func TestParseCommonCron(t *testing.T) {
	plan, err := PlanFromSpec(context.Background(), "repo ~/code/app; run pytest; every weekday at 9am", Options{StepHints: []string{"pytest"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := plan.Cron, "0 9 * * 1-5"; got != want {
		t.Fatalf("cron mismatch: got %s want %s", got, want)
	}
}
