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

The top-right corner reads `● connected` only while nothing is outstanding.
Any request to AWX — the first page of a list, a refresh, a page loading as
you scroll, a search, a launch form, output being tailed, a sync — replaces it
with a spinner and `loading`, and `connecting` covers the round trip before
AWX has said who you are.

## Keys

| Key | Action |
| --- | --- |
| `1`–`6`, `tab` | switch between Templates, Jobs, Inventories, Projects, Schedules, Workflows |
| `↑` `↓` / `j` `k` | move; `ctrl+d` / `ctrl+u` half page, `g` / `G` top / bottom |
| `/` | search the current view (`esc` clears) |
| `i` | switch to another configured instance |
| `enter` | launch a template or workflow · open job output · open project, inventory or schedule details · for a workflow job: its launch details |
| `s` | sync: SCM update a project · update an inventory's sources |
| `t` | on the Schedules tab or a schedule's details: enable or disable it |
| `h` / `g` | inside inventory details: jump the cursor to its first host / group (both are always listed) — `space` toggles a row into the live limit |
| `x` | inside inventory details: clear every checked host/group |
| `a` | inside inventory details: launch an ad hoc command against it |
| `p` | pin the highlighted record, or the run whose output is open |
| `f` | narrow the view: pinned only, and on Jobs also owner, status and kind |
| `↑↓` / `tab` | move between fields in the launch form; `←→` pick a choice, `space` toggles a multiselect, `enter` on a multiselect opens its full list, `ctrl+s` submits |
| `c` | cancel a running job |
| `d` | on the Jobs tab or its output: show what a job was launched with |
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
with a count of what is scrolled out of view. `enter` on one opens a full-screen
picker instead, listing every entry vertically and scrolling like any other
list (`↑↓` move, `space` toggle, `a` select all, `c` clear, `enter`/`esc`
closes back to the form); this also applies to a survey `multiselect` question.
The template's own values start selected, and one that is past the page cap is
added to the list rather than quietly dropped: deselecting a credential a
template needs would launch a job that cannot authenticate. The execution
environment keeps a "template default" entry, which sends no key at all.

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

## Ad hoc commands

`a` inside an inventory's details opens a fixed form for running a module
directly against it, without a job template: credential, module (`command`,
`shell`, `ping`, `systemd`, and the rest of the 18 AWX permits), module
arguments, limit, verbosity and become. There is no `ask_*_on_launch` config
or survey to read first — every field is always asked, and the inventory is
whichever one's details are open, not typed in.

Credential and module each open the same full-screen picker as instance
groups on a template — `enter` lists the whole catalogue vertically instead of
cycling one at a time with `←→` — minus the checkboxes: there is exactly one
answer, so moving the highlight has already chosen it, and `enter` or `esc`
both just close the list.

The credential list is narrowed to machine credentials, the same as AWX's own
ad hoc launch: `kind` itself is not a filterable field, but
`?credential_type__kind=ssh` is (confirmed against a live instance), and
that's the terse value the API uses for what its UI calls "Machine" — nothing
else can authenticate to a host, so a vault or cloud credential is excluded
server-side rather than fetched and left unusable in the list.

`POST /api/v2/ad_hoc_commands/` answers with the ad hoc command it started,
which opens in the same output view as any other run: it is its own
collection, like a project update or inventory sync, with its own `/stdout/`,
`/events/` and `/cancel/` endpoints. AWX names the record after the module
itself (`systemd`, `shell`) and leaves `extra_vars` as `"---"` when none is
given.

`esc` from the form returns to the same inventory's details, not all the way
out to the inventories list — ad hoc is only ever reached from there.

## Picking hosts and groups for a limit

Before AWX groups had any representation here, the only way to target a
`limit` was to already know a host or group's exact name and type it in
blind. Opening an inventory's details (`enter`) now fetches its hosts and
groups (`/api/v2/inventories/{id}/hosts/` and
`/api/v2/inventories/{id}/groups/`) alongside its sources, and renders both
as tables right there in the same view — no separate page, nothing to expand.

A single cursor moves across groups, then hosts (`↑↓`, `h`/`g` to jump
straight to the first row of either); `space` toggles the row it is on.
There is no separate "confirm" step — the `limit` line above updates live as
soon as a row is checked, `:`-joined the way AWX's own limit syntax ORs
alternatives together, combining whatever is checked across both tables.
`x` clears every checkbox in both.

That live selection is not itself a `limit` field — it only offers a default
the next time one is asked for. It fills the ad hoc form's `limit` (opened
with `a`), or a job template's, but only when that template's own default is
empty and its inventory is the one the selection was made against; a
template that already sets its own default limit always keeps it.

## Schedules

The Schedules tab reads `/api/v2/schedules/`, the one global list in AWX: every
recurrence rule across every job template, project and inventory, plus the
system jobs AWX schedules for itself (cleanup, activity stream). `enter` opens
a schedule's details — its rrule, timezone, next run and what it launches; `t`
enables or disables it, on the list or from the details view.

AWX tells a schedule's target apart by `summary_fields.unified_job_template
.unified_job_type` (`job`, `project_update`, `inventory_update`, `system_job`,
`workflow_job`), not by which collection it came from — there is no
`/api/v2/job_templates/{id}/schedules/`-style split the way inventory sources
split by inventory. The "type" column shows this, so a system cleanup schedule
doesn't get mistaken for one that runs a real playbook.

Toggling is the only write this tab makes: `PATCH /api/v2/schedules/{id}/`
with `{"enabled": ...}`, touching nothing else about the schedule. Like
syncing, it is refused outright on a read-only client and a held-down `t`
cannot fire the same PATCH twice.

## Workflow job templates

The Workflows tab reads `/api/v2/workflow_job_templates/` and launches them
the same way the Templates tab launches a job template — `enter` reads
`/launch/` and `/survey_spec/` and builds a form — but a workflow's own form
is smaller: it can only ever ask for inventory, limit, SCM branch, tags,
labels and its survey, never a credential, execution environment, job type
or verbosity, since those belong to the job templates inside the workflow,
not the workflow itself. `LaunchConfig` is shared with job templates, so a
flag a workflow never sets just stays at its zero value rather than needing a
type of its own.

Launching answers with a workflow job, listed on the Jobs tab beside every
other kind of run. A workflow job has no stdout of its own, so `enter` and
`d` both open its launch details (status, launch type, inventory, limit,
tags, extra vars) instead of trying to open output that does not exist —
and, reading `/api/v2/workflow_jobs/{id}/workflow_nodes/`, every node's name
and status underneath: successful, running, failed, `pending` for one the
workflow has not reached yet, `skipped` for one on the untaken branch of a
success/failure edge. The nodes themselves are listed flat, in the order AWX
created them, not as the graph they form — no edges, no approvals, no
retrying a single node. `r` reloads both the job and its nodes; `c` still
cancels the workflow job itself, through its own
`/api/v2/workflow_jobs/{id}/cancel/` rather than `/api/v2/jobs/`.

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
| Jobs | started by anyone / me, or a typed username · status: any of running / failed / successful, several at once · kind: any / jobs / project updates / inventory syncs, several at once · everything / pinned only |
| Templates, Inventories, Projects | everything / pinned only |

`↑↓` picks a row. Status and Kind are sets, not one choice each — a run
worth a look is usually failed or still running, both at once, and wanting
jobs and project updates together while excluding inventory syncs is one
narrowing, not two — so on those rows `←→` moves a highlight across the
options and `space` toggles the highlighted one in or out. Kind leads with
an "any" entry (Status has none — an empty set already means any, and there
is no shorter way to say it that still fits the row) which is not a member
to toggle in; selecting it just clears whatever else is selected. On every
other row `←→` cycles through its options; `space` does nothing there, so it
means one thing everywhere it does something. "Started by" is one row that
does two jobs: empty, `←→` toggles anyone/me exactly like Pinned does, but
the moment a character is typed it becomes a username fragment instead (a
deploy bot, a colleague), matched case-insensitively — "mine" can only ever
mean the connected user, so finding anyone else means typing their name.
Backspacing that text back to empty returns the row to the anyone/me toggle.
`c` clears everything (typed off the "Started by" row, where a letter is
just as likely to be someone's username); backspace does the same, and also
doubles as the clear-all once "Started by" is empty. `enter` applies and
`esc` leaves without changing anything. The active narrowing shows in the
count line — `mine · failed/running · jobs/project updates · 46` — so a
short list is never mistaken for a small instance.

**A narrowed view is remembered**, in the same file as the pins and keyed the
same way, per instance and per tab: leave the Jobs tab showing your own failed
runs and that is how it opens tomorrow, while Templates stays as you left it
and another instance is unaffected. It is restored before the first request
goes out, so a saved filter never costs an unnarrowed fetch that is thrown
away. `c` then `enter` clears it, on disk as well as on screen. Choices are
saved as plain names, so adding one to the panel later cannot strand what is
already saved.

Every choice is sent to AWX, not applied to the rows that happen to be
loaded: `created_by`, `created_by__username__icontains`, `status__in` and
`type__in` (comma lists, for several statuses or kinds at once) as query
parameters, and a pinned-only view as one `?id__in=` request. Lists are
paged, so filtering what is on screen would hide everything that is not.
`/` still searches, inside whatever the view is narrowed to.

The key line marks what is already in force: `p` reads **unpin** on a pinned
row, `f` **show** on a narrowed list, and in the output view `f` **follow**
while the tail is being followed — each in amber, the same colour as the `★`
on the rows, so the state of the view can be read off the legend that is
always on screen.

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
| `d` | what this job was launched with |
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

## Job launch details

`d` on the Jobs tab, or from a job's output view, opens what it was launched
with: status, launch type (manual, scheduled, relaunch, a workflow
dependency, ...), when it started and finished, and — for a playbook job —
its job template, inventory, limit, job and skip tags, credentials,
execution environment and extra vars. A project update shows the project it
updated and its SCM credential instead, since its record has no inventory,
tags or extra vars at all; an inventory sync shows its inventory the same way
a playbook job does.

The jobs list itself never carries this — `extra_vars`, `limit` and the rest
are missing from `/api/v2/unified_jobs/` — so opening the view always makes
one more request to the job's own collection (`/api/v2/jobs/`,
`/api/v2/project_updates/` or `/api/v2/inventory_updates/`, matching what the
run actually is) to read the full record.

`enter` jumps straight from the details view to that job's output; `d` from
the output view comes back to the details. `esc` from either always drops
back to the jobs list, no matter how many times you have hopped between them.

`p` pins or unpins the run right from its details, the same as on the jobs
list or from its output.

## Project details

`enter` on a project opens what AWX knows about it: status and when it last
updated, organization, SCM type, URL, branch, refspec and the checked-out
revision, its SCM credential, local path, the update flags that are actually
set (update on launch, clean, delete on update, track submodules, allow branch
override), and the cache and job timeouts. AWX's project list already returns
the whole record, so opening the details costs no extra request.

The view was originally going to list the playbooks AWX finds in the
checkout too, but `/api/v2/projects/{id}/playbooks/` walks the whole
repository for any YAML file that merely parses as a top-level list — it
isn't scoped to a `playbooks/` directory — so a project that stores
non-playbook YAML (e.g. list-shaped inventory files) elsewhere in the repo
gets those listed as "playbooks" too. That's a real-AWX quirk, not something
awxtui can filter reliably, so the playbook list was dropped.

A project's details can be longer than the screen, so the view scrolls with
`↑↓`, `ctrl+d` / `ctrl+u` and `g` / `G`, and says which lines are shown. A
manual project (no SCM type) says `manual` rather than leaving blank rows
that look like a failed load.

## Layout

| Path | What |
| --- | --- |
| `main.go` | flags and program start-up |
| `internal/config` | config file, instance selection, token resolution |
| `internal/ui/instances.go` | in-app instance switcher |
| `internal/awx` | minimal AWX v2 API client |
| `internal/awx/launch.go` | launch metadata, survey specs, YAML/JSON extra vars |
| `internal/awx/sync.go` | project updates, inventory sources, inventory sync |
| `internal/awx/adhoc.go` | ad hoc commands: module list, machine-credential lookup, launch |
| `internal/ui` | Bubble Tea model, key handling, rendering |
| `internal/ui/form.go` | launch form: fields, validation, payload building |
| `internal/ui/output.go` | job output view: find, highlight, failure/task jumps |
| `internal/ui/jobs.go` | job launch details: what a run was started with |
| `internal/ui/projects.go` | project details: SCM settings, update flags |
| `internal/ui/inventories.go` | inventory details: each source's real sync status, its hosts/groups tables and the combined cursor over them |
| `internal/ui/members.go` | a hosts/groups list: fetch, paging, the row shape the limit is built from |
| `internal/ui/schedules.go` | schedule details: rrule, next run, enable/disable |
| `internal/ui/table.go` | responsive column layout (columns shrink, then drop) |
| `internal/ui/pins.go` | pinning, on every tab |
| `internal/ui/show.go` | the f panel: what a view is narrowed to |
| `internal/state` | pins and saved views, kept across restarts |
| `internal/ui/theme.go` | colours and status badges |

## Tests

`go test ./...` drives the whole model against a mock AWX API: connect, filter,
launch, follow output, drill into an inventory's hosts and groups and pick a
limit from them, read project, inventory and schedule details, sync a project
and an inventory's sources, toggle a schedule's enabled flag, cancel a job,
plus a check that every view fits inside 80×24, 120×40 and 200×50 terminals.

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
how large the instance is. While a page is on its way, a spinner and `loading
more jobs…` sit below the list with a blank line either side, where you are
already looking.

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

- A workflow's own graph: a workflow job's details list every node's name
  and status, but not the edges between them, approval nodes, or retrying
  one node on its own. Look at the AWX UI for the graph itself.
- Creating, editing or deleting a workflow job template, or a schedule: both
  tabs read (and a schedule toggles), but building either is not done.
