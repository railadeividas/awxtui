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

The quickest way is the environment:

```sh
export AWX_URL=https://awx.example.com   # base URL, no /api/v2
export AWX_TOKEN=<personal access token> # AWX: Users -> Tokens -> Add
export AWX_INSECURE=1                    # optional, skip TLS verification
export AWXTUI_READONLY=1                 # optional, refuse all writes
```

For more than one AWX, put them in `~/.config/awxtui/config.yml`
(`$XDG_CONFIG_HOME` is honoured):

```yaml
default: prod

instances:
  prod:
    url: https://awx.example.com
    token_command: pass show awx/prod   # keeps the secret off disk
    read_only: true                     # refuse launches and cancels
  staging:
    url: https://awx-staging.example.com
    token: <personal access token>
    insecure: true                      # skip TLS verification
```

```sh
awxtui                      # the default instance
awxtui -instance staging    # or $AWXTUI_INSTANCE=staging
awxtui -list                # what is configured
awxtui -read-only           # refuse writes whatever the config says
```

Press `i` in the app to switch instance without restarting: pick from the list,
and everything — templates, jobs, filters, the connected user, the read-only
badge — is reloaded from the new one. Replies still in flight from the previous
instance are discarded rather than mixed in. The header names the instance you
are on, with a `▾` when there are others to switch to.

An instance is chosen in this order: `-instance`, `$AWXTUI_INSTANCE`,
`$AWX_URL`+`$AWX_TOKEN`, the file's `default`, or the only one configured. An
instance with no token falls back to `$AWX_TOKEN`, so the file can hold URLs
while secrets stay elsewhere. Tokens are unquoted before use, since a value
copied out of a shell env file usually arrives wrapped in quotes, and awxtui
warns if a file holding a literal token is readable by anyone else.

`AWXTUI_READONLY=1`, `-read-only` or `read_only: true` pin the API client to
GET requests, so nothing can be launched or cancelled by accident. The header
shows a `read-only` badge, launch forms still open for inspection, and
submitting one is refused.

Create tokens in the AWX UI under **Users → your user → Tokens**, scope
`write` to launch and cancel jobs (`read` is enough for browsing).

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
| `i` | switch to another configured instance |
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

## Reading job output

`enter` on a job opens its output, which is where most of the time goes on a
failed run:

| Key | Action |
| --- | --- |
| `/` | find in output — matches are highlighted as you type |
| `n` / `N` | next / previous hit, wrapping around; the position shows as `3/17` |
| `]` / `[` | next / previous **failure** (`fatal:`, `failed:`, `unreachable:`, `ERROR!`) |
| `t` / `T` | next / previous task boundary (`TASK`, `PLAY`, `RUNNING HANDLER`, `PLAY RECAP`) |
| `f` | follow the tail of a running job |
| `g` / `G` | top / bottom |
| `esc` | clear the search, or leave the view |

Searching and jumping work on the text as displayed, so Ansible's colour codes
never interfere: highlighting a match keeps the line's own colour. Jumping
anywhere turns follow mode off, so a running job cannot yank the view back to
the bottom while you are reading.

Output for a running job is tailed from `job_events` (the same stream the web UI
renders, available while the job runs); a finished job is fetched in one request
from `/stdout/`, falling back to events if AWX has no stored stdout.

## Layout

| Path | What |
| --- | --- |
| `main.go` | flags and program start-up |
| `internal/config` | config file, instance selection, token resolution |
| `internal/ui/instances.go` | in-app instance switcher |
| `internal/awx` | minimal AWX v2 API client |
| `internal/awx/launch.go` | launch metadata, survey specs, YAML/JSON extra vars |
| `internal/ui` | Bubble Tea model, key handling, rendering |
| `internal/ui/form.go` | launch form: fields, validation, payload building |
| `internal/ui/output.go` | job output view: find, highlight, failure/task jumps |
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

## Pagination and search

Every list is read one page at a time (200 records, 100 for jobs) and pages in
as you scroll toward the end, so startup costs one request per view no matter
how large the instance is.

Because a lazily loaded list is incomplete, `/` searches **AWX**, not just the
rows on screen: the query is debounced, sent as `?search=`, and matches
whatever that endpoint considers searchable — including descriptions the TUI
never shows. Results page in the same way. A list that is already complete is
filtered in memory so typing stays instant, and falls back to a server search
if nothing matches locally.

The count on the right of the search line shows the state: `200 of 209`,
`1 matching`, `100 of 97928`, or `searching…`. Replies to a query you have
already typed past are discarded, so results never flicker backwards.

Every list stops after 25 pages and says `(page limit)` rather than walking an
instance forever. The Jobs list refreshes in the background by re-reading only
the first page and merging it, so the pages you scrolled through stay put.

## Behaviour under stress

- **Transient failures are retried** with growing backoff, honouring
  `Retry-After`. Only GETs are retried on network errors and 5xx; a launch is
  retried solely on 429, which AWX returns before doing any work — repeating an
  ambiguous POST could run a playbook twice.
- **Tokens never reach an error message**, even if a server echoes one back.
- **Very long output is trimmed to its tail** (2 MiB) on a line boundary, with
  a note saying so, so a verbose play across thousands of hosts cannot grow
  without bound or make every poll re-wrap hundreds of megabytes.
- **Errors get a real view**: the status bar shows one line and `e` opens the
  full text, itself marked as truncated if it still does not fit.
- **The key legend never disappears**: errors and notices sit beside it, cut to
  the room that is left, so the keys that get you out of a screen stay on
  screen. Only a terminal too narrow to show both gives the line to the
  message.

## Not done yet

- Workflow job templates, schedules and ad-hoc commands.
- Prompting for instance groups, labels, execution environments, credentials.
