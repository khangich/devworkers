package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Result represents the normalized planning output.
type Result struct {
	Name     string
	Repo     string
	Cron     string
	Natural  string
	Timezone string
	Steps    []string
}

// Options configure the planner behaviour.
type Options struct {
	Name       string
	Timezone   string
	CronHint   string
	RepoHint   string
	StepHints  []string
	HTTPClient *http.Client
	Model      string
	BaseURL    string
	APIKey     string
}

// PlanFromSpec resolves a plan from natural language using an OpenAI-compatible API when available.
func PlanFromSpec(ctx context.Context, spec string, opts Options) (*Result, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, errors.New("spec is empty")
	}

	res := &Result{
		Name:     opts.Name,
		Repo:     opts.RepoHint,
		Cron:     opts.CronHint,
		Timezone: opts.Timezone,
		Natural:  spec,
		Steps:    append([]string{}, opts.StepHints...),
	}

	if res.Timezone == "" {
		res.Timezone = "Local"
	}

	if opts.APIKey != "" {
		plan, err := callLLM(ctx, spec, opts)
		if err == nil {
			if plan.Name != "" {
				res.Name = plan.Name
			}
			if plan.Repo != "" {
				res.Repo = plan.Repo
			}
			if plan.Cron != "" {
				res.Cron = plan.Cron
			}
			if len(plan.Steps) > 0 {
				res.Steps = plan.Steps
			}
			if plan.Timezone != "" {
				res.Timezone = plan.Timezone
			}
			return res, nil
		}
	}

	// fallback heuristics
	if res.Cron == "" {
		if cron, ok := parseCommonCron(spec); ok {
			res.Cron = cron
		} else {
			return nil, errors.New("unable to derive cron expression; provide --cron")
		}
	}

	if res.Repo == "" {
		if repo := extractRepoPath(spec); repo != "" {
			res.Repo = repo
		}
	}

	if len(res.Steps) == 0 {
		res.Steps = extractSteps(spec)
		if len(res.Steps) == 0 {
			return nil, errors.New("no steps resolved; provide --step")
		}
	}

	if res.Name == "" {
		res.Name = defaultNameFromSpec(spec)
	}

	return res, nil
}

type llmResult struct {
	Name     string   `json:"name"`
	Repo     string   `json:"repo"`
	Cron     string   `json:"cron"`
	Timezone string   `json:"timezone"`
	Steps    []string `json:"steps"`
}

func callLLM(ctx context.Context, spec string, opts Options) (*llmResult, error) {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
	}
	model := opts.Model
	if model == "" {
		model = "gpt-4.1-mini"
	}

	requestBody := map[string]interface{}{
		"model": model,
		"input": []map[string]interface{}{
			{
				"role":    "system",
				"content": []map[string]string{{"type": "text", "text": plannerSystemPrompt()}},
			},
			{
				"role": "user",
				"content": []map[string]string{{
					"type": "text",
					"text": spec,
				}},
			},
		},
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name": "devagent_plan",
				"schema": map[string]interface{}{
					"type":     "object",
					"required": []string{"repo", "cron", "steps"},
					"properties": map[string]interface{}{
						"name":     map[string]string{"type": "string"},
						"repo":     map[string]string{"type": "string"},
						"cron":     map[string]string{"type": "string"},
						"timezone": map[string]string{"type": "string"},
						"steps": map[string]interface{}{
							"type":  "array",
							"items": map[string]string{"type": "string"},
						},
					},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(baseURL, "/")+"/responses", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("planner API returned status %d", resp.StatusCode)
	}

	var payload struct {
		Output []struct {
			Content []struct {
				Type string          `json:"type"`
				Text string          `json:"text"`
				JSON json.RawMessage `json:"json"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	for _, item := range payload.Output {
		for _, content := range item.Content {
			if content.JSON != nil {
				var out llmResult
				if err := json.Unmarshal(content.JSON, &out); err != nil {
					return nil, err
				}
				return &out, nil
			}
			if content.Type == "output_text" && content.Text != "" {
				var out llmResult
				if err := json.Unmarshal([]byte(content.Text), &out); err == nil {
					return &out, nil
				}
			}
		}
	}

	return nil, errors.New("planner response missing JSON content")
}

func plannerSystemPrompt() string {
	return "You convert natural language repo automation specs into a strict JSON plan with fields: name, repo, cron, timezone, steps. Always output valid cron expressions with five fields."
}

type cronPattern struct {
	pattern  *regexp.Regexp
	weekdays bool
}

var commonCronPatterns = []cronPattern{
	{pattern: regexp.MustCompile(`(?i)every\s+day\s+at\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?`), weekdays: false},
	{pattern: regexp.MustCompile(`(?i)every\s+weekday\s+at\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?`), weekdays: true},
}

func parseCommonCron(spec string) (string, bool) {
	spec = strings.ToLower(spec)
	for _, item := range commonCronPatterns {
		matches := item.pattern.FindStringSubmatch(spec)
		if len(matches) == 0 {
			continue
		}
		hour := to24Hour(matches[1], matches[3])
		minute := 0
		if matches[2] != "" {
			if m, err := strconv.Atoi(matches[2]); err == nil {
				minute = m
			}
		}
		cron := fmt.Sprintf("%d %s * * *", minute, hour)
		if item.weekdays {
			cron = fmt.Sprintf("%d %s * * 1-5", minute, hour)
		}
		return cron, true
	}
	if strings.Contains(spec, "hour") {
		return "0 * * * *", true
	}
	return "", false
}

func to24Hour(val string, meridiem string) string {
	val = strings.TrimSpace(val)
	hour := 0
	if parsed, err := strconv.Atoi(val); err == nil {
		hour = parsed % 24
	}
	if meridiem != "" {
		meridiem = strings.ToLower(meridiem)
		if meridiem == "pm" && hour < 12 {
			hour += 12
		}
		if meridiem == "am" && hour == 12 {
			hour = 0
		}
	}
	return fmt.Sprintf("%d", hour)
}

var repoPattern = regexp.MustCompile(`(?i)repo\s+([~\./\w\-_/]+)`)

func extractRepoPath(spec string) string {
	matches := repoPattern.FindStringSubmatch(spec)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func extractSteps(spec string) []string {
	var steps []string
	parts := strings.Split(spec, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "run ") {
			cmd := strings.TrimSpace(part[4:])
			cmd = strings.Trim(cmd, "'")
			steps = append(steps, cmd)
		}
	}
	return steps
}

func defaultNameFromSpec(spec string) string {
	slug := strings.ToLower(spec)
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = fmt.Sprintf("devagent-%d", time.Now().Unix())
	}
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return slug
}
