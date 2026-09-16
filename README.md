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
export AWXTUI_READONLY=1                 # optional, refuse all writes
```

`AWXTUI_READONLY=1` pins the API client to GET requests, so nothing can be
launched or cancelled by accident — useful when pointing at production. The
header shows a `read-only` badge, launch forms still open for inspection, and
submitting one is refused.

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
| `↑↓` / `tab` | move between fields in the launch form; `←→` pick a choice, `space` toggles a multiselect, `ctrl+s` submits |
| `c` | cancel a running job |
| `f` | toggle follow mode in the job output view |
| `r` | refresh |
| `?` | key help |
| `q` / `esc` | back, or quit from the list |

## Launching

`enter` on a template reads `/launch/` and `/survey_spec/` and builds a form
with exactly what that template asks for:

- **Prompted values** — any `ask_*_on_launch` field: inventory (picked from a
  list), job type, SCM branch, limit, verbosity, job and skip tags, diff mode,
  forks, job slices, timeout, and extra vars as YAML or JSON.
- **Survey questions** — text, textarea, password, integer, float,
  multiple choice and multiselect, with their defaults pre-filled and `required`
  answers enforced before anything is sent.
- **Credential passwords** — whatever `passwords_needed_to_start` lists.

Values are validated locally first (required answers, number parsing, survey
`min`/`max`, and extra vars being a mapping), so a bad form is reported inline
instead of coming back as an API error. Survey answers are merged into
`extra_vars` with their declared types, and only the keys the template actually
prompted for are sent.

Instance groups, labels, execution environments and credential selection are not
promptable yet — the template's defaults apply.

Job output follows live while the job runs, and the Jobs list refreshes itself
every few seconds.

Output for a running job is tailed from `job_events` (the same stream the web UI
renders, available while the job runs); a finished job is fetched in one request
from `/stdout/`, falling back to events if AWX has no stored stdout.

## Layout

| Path | What |
| --- | --- |
| `main.go` | env config and program start-up |
| `internal/awx` | minimal AWX v2 API client |
| `internal/awx/launch.go` | launch metadata, survey specs, YAML/JSON extra vars |
| `internal/ui` | Bubble Tea model, key handling, rendering |
| `internal/ui/form.go` | launch form: fields, validation, payload building |
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

There is also a live test against a real instance, skipped by default:

```sh
AWXTUI_LIVE=1 go test -v ./internal/ui -run TestLive
```

## Pagination

Templates, inventories and projects are read in full (200 per request), so
search covers everything on the instance rather than the first page. Jobs are
different — an instance can hold a hundred thousand of them — so they page in as
you scroll, and keep paging while a search has too little to fill the screen.
The count on the right of the search line shows what is loaded against what
AWX reports, for example `100 of 97928`.

Every list stops after 25 pages and says `(page limit)` rather than walking an
instance forever. The Jobs list refreshes in the background by re-reading only
the first page and merging it, so the pages you scrolled through stay put.

## Not done yet

- Search within job output, and jumping between failed tasks.
- A config file with several named instances, instead of env vars.
- Workflow job templates, schedules and ad-hoc commands.
- Prompting for instance groups, labels, execution environments, credentials.
