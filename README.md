# awxtui

A small, modern terminal UI for AWX / Ansible Automation Platform. Browse job
templates, launch them, and follow job output live — without leaving the shell
and without `ansible-navigator`'s weight.

Proof of concept: read-only browsing plus launch and cancel.

## Install

```sh
go build -o awxtui .
```

## Configure

```sh
export AWX_URL=https://awx.example.com   # base URL, no /api/v2
export AWX_TOKEN=<personal access token> # AWX: Users -> Tokens -> Add
export AWX_INSECURE=1                    # optional, skip TLS verification
```

Create the token in the AWX UI under **Users → your user → Tokens**, scope
`write` if you want to launch and cancel jobs (`read` is enough for browsing).

## Run

```sh
./awxtui
```

```
awxtui  admin@awx.example.com                                    ● connected
 1 Templates  2 Jobs  3 Inventories  4 Projects
──────────────────────────────────────────────────────────────────────────────
press / to search                                                         5/5
NAME                     PROJECT      INVENTORY     LAST RUN       WHEN
▌Deploy web app           infra        production    ✓ successful   1h ago
 Rotate certificates      security     all           ✗ failed       2h ago
──────────────────────────────────────────────────────────────────────────────
↑↓ move · enter launch · / search · r refresh · ? help · q quit
```

## Keys

| Key | Action |
| --- | --- |
| `1`–`4`, `tab` | switch between Templates, Jobs, Inventories, Projects |
| `↑` `↓` / `j` `k` | move; `ctrl+d` / `ctrl+u` half page, `g` / `G` top / bottom |
| `/` | search the current view (`esc` clears) |
| `enter` | launch a template · open job output · list inventory hosts |
| `c` | cancel a running job |
| `f` | toggle follow mode in the job output view |
| `r` | refresh |
| `?` | key help |
| `q` / `esc` | back, or quit from the list |

Launching prompts for optional `extra_vars` as a JSON object; leave it empty to
launch with the template's defaults. Job output follows live while the job runs,
and the Jobs list refreshes itself every few seconds.

## Layout

| Path | What |
| --- | --- |
| `main.go` | env config and program start-up |
| `internal/awx` | minimal AWX v2 API client |
| `internal/ui` | Bubble Tea model, key handling, rendering |
| `internal/ui/table.go` | responsive column layout (columns shrink, then drop) |
| `internal/ui/theme.go` | colours and status badges |

## Tests

`go test ./...` drives the whole model against a mock AWX API: connect, filter,
launch, follow output, drill into inventory hosts, cancel a job, plus a check
that every view fits inside 80×24, 120×40 and 200×50 terminals.

Set `AWXTUI_SHOW=1` to print the rendered views while testing:

```sh
AWXTUI_SHOW=1 go test -v ./internal/ui -run TestFlows
```

## Not in the POC

Surveys and launch prompts (inventory/credential/limit overrides), workflow job
templates, schedules, ad-hoc commands, pagination beyond the first page, host
detail and recent-job drill-down, and writing config to a file instead of env
vars.
