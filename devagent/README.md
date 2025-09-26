# DevAgent

DevAgent is a lightweight, headless automation runner for macOS that lets you describe repo tasks in natural language, store the workflow as YAML, and schedule executions locally. It is written in Go and ships as a single binary suitable for Apple Silicon and Intel Macs.

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/khangich/devworkers/main/devagent/scripts/install.sh | bash
```

> Requires Go 1.22+ at install time so the script can build the binary locally. Override the source repo by exporting `DEVAGENT_REPO` if using a fork.

The install script builds the `devagent` binary (or downloads a cached build when available), installs it into `/usr/local/bin` (falling back to `/opt/homebrew/bin` on Apple Silicon), writes the LaunchAgent file `~/Library/LaunchAgents/com.llmlab.devagent.plist`, and loads it via `launchctl`. After the script completes you can inspect logs with:

```bash
log stream --predicate 'process == "devagent"'
```

## Quick start

```bash
export OPENAI_API_KEY=sk-...
devagent new "repo ~/code/app; checkout feature/x; run 'pytest -q'; every weekday at 9am"
devagent run
devagent schedule list
```

If you do not provide an API key, supply `--cron`, `--repo`, and one or more `--step` flags when running `devagent new`.

## Troubleshooting

- Check the daemon: `launchctl list | grep devagent`
- Inspect daemon logs: `log stream --predicate 'process == "devagent"'`
- Remove a job: `devagent schedule remove <name>`

## Development

Run tests and build the binary locally:

```bash
cd devagent
go test ./...
go build ./cmd/devagent
```

To uninstall
```bash
bash scripts/uninstall.sh
```

The SQLite state file lives at `~/.devagent/state.db` and run artifacts are stored under `devagent_runs/` inside the configured repo.
