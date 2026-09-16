# awxtui

A small, modern terminal UI for AWX / Ansible Automation Platform. Browse job
templates, launch them, and follow job output live — without leaving the shell
and without `ansible-navigator`'s weight.

Proof of concept: read-only browsing plus launch, cancel and sync.

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
| `enter` | launch a template · open job output · list inventory hosts · open project details |
| `s` | sync: SCM update a project · update an inventory's sources |
| `p` | pin the highlighted record, or the run whose output is open |
| `f` | narrow the view: pinned only, and on Jobs also owner, status and kind |
| `↑↓` / `tab` | move between fields in the launch form; `←→` pick a choice, `space` toggles a multiselect, `ctrl+s` submits |
| `c` | cancel a running job |
| `f` | in the job output view: toggle follow mode |
| `r` | refresh |
| `?` | key help |
| `q` / `esc` | back, or quit from the list |

## Launching

`enter` on a template reads `/launch/` and `/survey_spec/` and builds a form
with exactly what that template asks for:

- **Prompted values** — every `ask_*_on_launch` field: inventory, credentials,
  execution environment, instance groups and labels (each picked from the
  instance's own list), job type, SCM branch, limit, verbosity, job and skip
  tags, diff mode, forks, job slices, timeout, and extra vars as YAML or JSON.
- **Survey questions** — text, textarea, password, integer, float,
  multiple choice and multiselect, with their defaults pre-filled and `required`
  answers enforced before anything is sent.
- **Credential passwords** — whatever `passwords_needed_to_start` lists.

Values are validated locally first (required answers, number parsing, survey
`min`/`max`, and extra vars being a mapping), so a bad form is reported inline
instead of coming back as an API error. Survey answers are merged into
`extra_vars` with their declared types, and only the keys the template actually
prompted for are sent.

Credentials, instance groups and labels are multi-selects (`←→` to move,
`space` to toggle) holding the whole catalogue — 19 instance groups and 52
credentials on the instance this was built against — so they render as a window
with a count of what is scrolled out of view. The template's own values start
selected, and one that is past the page cap is added to the list rather than
quietly dropped: deselecting a credential a template needs would launch a job
that cannot authenticate. The execution environment keeps a "template default"
entry, which sends no key at all.

Job output follows live while the job runs, and the Jobs list refreshes itself
every few seconds.

## Syncing projects and inventories

`s` starts a sync and opens its output, so a project that has drifted from its
branch or an inventory that has not picked up a new host is one keystroke away
on the list it is already shown on — also from inside a project's details.

The two are different endpoints wearing one name:

- **A project** is updated in place with `POST /api/v2/projects/{id}/update/`,
  which answers with the `project_update` it started. A project with no SCM
  type has nothing to pull and AWX refuses the POST with a 405; the refusal is
  shown rather than swallowed.
- **An inventory** has no update endpoint of its own. Its hosts come from
  inventory sources, and each is synced with
  `POST /api/v2/inventory_sources/{id}/update/`, so syncing an inventory means
  updating every source it has — what the web UI calls "sync all". The
  `SOURCES` column says how many there are, so an inventory whose hosts were
  typed in by hand reads as `—`: nothing to sync, and `s` says so without
  issuing a request.

`/api/v2/inventories/{id}/update_inventory_sources/` would start every source
in one request, but it answers with a bespoke per-source status list instead of
the update records, and an update you cannot address is an update you cannot
follow. Sources are therefore updated one at a time; if one fails to start, the
error names it and says how many were already started, and nothing is retried.

**Neither record appears in `/api/v2/jobs/`** — it holds playbook jobs alone,
which is why the Jobs tab reads `/api/v2/unified_jobs/` instead, where a sync
you started is listed beside the playbook jobs and labelled as what it is.
A project update and an inventory sync each live in their own
collection, with their own detail, `/stdout/`, events and `/cancel/` endpoints,
and their events are served from `/events/` rather than `/job_events/`. The
output view follows whichever collection the run came from, names it in the
header (`#12  infra  project update`), and `c` cancels it through its own
endpoint. Leaving refreshes the list the sync came from, since that is where
its new status shows.

Syncing is a write: a read-only client refuses it before anything reaches the
network, and a held-down `s` cannot start the same update twice.

## Pinning and narrowing a view

On a shared AWX every list is everyone's: 209 job templates and 98079 jobs on
the instance this was built against, of which 310 runs belong to the account
using awxtui. Two keys deal with that.

**`p` pins the highlighted record** — a template, a job, an inventory, a
project, or the run whose output is open, which is usually when you decide a
run is worth finding again. AWX has no bookmark of any kind, so pins are kept
locally in `~/.local/state/awxtui/pins.json` (`$XDG_STATE_HOME` honoured,
`-state` overrides), per instance and per kind of record — job ids mean
nothing on another AWX, and project #12 is not project update #12. A pinned
row is marked `★`, and the file is written atomically, mode 600.

**`f` narrows what a view shows.** The panel offers what that tab can be
narrowed by:

| Tab | Choices |
| --- | --- |
| Jobs | started by anyone / me · status any / running / failed / successful · kind anything / jobs / project updates / inventory syncs · everything / pinned only |
| Templates, Inventories, Projects | everything / pinned only |

`↑↓` picks a row, `←→` or `space` sets it, `c` clears everything, `enter`
applies and `esc` leaves without changing anything. The active narrowing shows
in the count line — `mine · failed · 46` — so a short list is never mistaken
for a small instance.

**A narrowed view is remembered**, in the same file as the pins and keyed the
same way, per instance and per tab: leave the Jobs tab showing your own failed
runs and that is how it opens tomorrow, while Templates stays as you left it
and another instance is unaffected. It is restored before the first request
goes out, so a saved filter never costs an unnarrowed fetch that is thrown
away. `c` then `enter` clears it, on disk as well as on screen. Choices are
saved as plain names, so adding one to the panel later cannot strand what is
already saved.

Every choice is sent to AWX, not applied to the rows that happen to be
loaded: `created_by`, `status` and `type` as query parameters, and a
pinned-only view as one `?id__in=` request. Lists are paged, so filtering what
is on screen would hide everything that is not. `/` still searches, inside
whatever the view is narrowed to.

The key line marks what is already in force: `p` reads **unpin** on a pinned
row, `f` **show** on a narrowed list, and in the output view `f` **follow**
while the tail is being followed — each in the accent colour rather than the
muted one, so the state of the view can be read off the legend that is always
on screen.

Two details follow from where the data comes from:

- The Jobs tab reads **`/api/v2/unified_jobs/`**, not `/api/v2/jobs/`. The
  latter holds playbook jobs alone, so a project update or an inventory sync
  started from awxtui would be missing from it entirely. Runs that are not
  playbook jobs are labelled in the list (`ansible-dns · project update`), and
  the *kind* choice narrows to one of them. The unified list also serves kinds
  awxtui has no output endpoint for — workflow jobs, ad-hoc commands, system
  jobs — and opening one says so by name instead of asking `/api/v2/jobs/` for
  output that was never there.
- Your own runs say **`you`** in the `BY` column — a word, not just a colour,
  so it survives a monochrome terminal.

A pinned record AWX has since deleted is not silently dropped: the count reads
`1 of 2`, which is the difference between a job that is gone and one awxtui
failed to look up.

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

Output for a running job is tailed from its events (the same stream the web UI
renders, available while the job runs); a finished job is fetched in one request
from `/stdout/`, falling back to events if AWX has no stored stdout. This works
the same for a project update or an inventory sync, against that collection's
own endpoints.

## Project details

`enter` on a project opens what AWX knows about it: status and when it last
updated, organization, SCM type, URL, branch, refspec and the checked-out
revision, its SCM credential, local path, the update flags that are actually
set (update on launch, clean, delete on update, track submodules, allow branch
override), the cache and job timeouts, and the playbooks found in the
checkout. Nothing but the playbook list costs a request: AWX's project list
returns the whole record.

A project's details can be longer than the screen — the firewall project on
the instance this was built against renders 22 lines — so the view scrolls
with `↑↓`, `ctrl+d` / `ctrl+u` and `g` / `G`, and says which lines are shown.
A manual project (no SCM type) says `manual` and `none reported` rather than
leaving blank rows that look like a failed load, and `r` re-reads the
playbooks, which a project that has never updated does not have.

## Layout

| Path | What |
| --- | --- |
| `main.go` | flags and program start-up |
| `internal/config` | config file, instance selection, token resolution |
| `internal/ui/instances.go` | in-app instance switcher |
| `internal/awx` | minimal AWX v2 API client |
| `internal/awx/launch.go` | launch metadata, survey specs, YAML/JSON extra vars |
| `internal/awx/sync.go` | project updates, inventory sources, inventory sync |
| `internal/ui` | Bubble Tea model, key handling, rendering |
| `internal/ui/form.go` | launch form: fields, validation, payload building |
| `internal/ui/output.go` | job output view: find, highlight, failure/task jumps |
| `internal/ui/projects.go` | project details: SCM settings, update flags, playbooks |
| `internal/ui/table.go` | responsive column layout (columns shrink, then drop) |
| `internal/ui/pins.go` | pinning, on every tab |
| `internal/ui/show.go` | the f panel: what a view is narrowed to |
| `internal/state` | pins and saved views, kept across restarts |
| `internal/ui/theme.go` | colours and status badges |

## Tests

`go test ./...` drives the whole model against a mock AWX API: connect, filter,
launch, follow output, drill into inventory hosts, read project details, sync a
project and an inventory's sources, cancel a job, plus a check
that every view fits inside 80×24, 120×40 and 200×50 terminals.

Set `AWXTUI_SHOW=1` to print the rendered views while testing:

```sh
AWXTUI_SHOW=1 go test -v ./internal/ui -run TestFlows
```

There is also a live test against a real instance, skipped by default:

```sh
AWXTUI_LIVE=1 go test -v ./internal/ui -run TestLive
```

The live tests pin the client to `ReadOnly()`, so they only ever issue GETs
against a production instance: `TestLiveSyncTargets` checks what a sync *would*
act on — `can_update` per project, and that each inventory's row agrees with
its sources endpoint — without starting one, and `TestLiveMyRuns` checks that
"started by me" and "failed" are genuinely narrower than the full list and
that every row they return matches.

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
