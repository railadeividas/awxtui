# awxtui

A terminal UI for AWX / Ansible Automation Platform, in Go with Bubble Tea.
~6800 lines, 63 tests. `go test ./...` runs in under 3 seconds — keep it that way.

## Safety: the AWX behind AWX_URL is production

Real playbooks run against real fleet hosts. When working against a live
instance:

- **Issue GETs only.** Never launch, cancel, or modify anything.
- Exercise write paths against the mock servers in `internal/ui/*_test.go`.
- Live tests must construct the client with `awx.New(...).ReadOnly()`, which
  refuses non-GET requests before they reach the network
  (`internal/awx/client.go`). `internal/awx/readonly_test.go` guards this.
- Never interpolate `$AWX_TOKEN` into a shell command, a file, or output. Use
  single-quoted heredocs (`<<'EOF'`). A token has been leaked this way once.

## Layout

| Path | What |
| --- | --- |
| `main.go` | flags, config resolution, program start-up |
| `internal/config` | config file, instance selection, token resolution |
| `internal/awx` | API client: lists, paging, launch metadata, retry, redaction |
| `internal/ui/model.go` | state, key handling, the `Update` switch |
| `internal/ui/data.go` | `tea.Cmd` constructors and the messages they return |
| `internal/ui/view.go` | rendering, help, status bar, modals |
| `internal/ui/form.go` | launch form: fields, validation, payload building |
| `internal/ui/output.go` | job output: find, highlight, failure/task jumps |
| `internal/ui/instances.go` | in-app instance switcher |
| `internal/ui/table.go` | responsive columns (shrink, then drop) |
| `internal/ui/theme.go` | every colour and status badge |

## How to add a feature or fix a bug

1. **Check the real API first.** AWX's behaviour repeatedly contradicts the
   obvious reading of its docs. `curl` the endpoint (GET only) and look at what
   comes back before writing code. Past examples: `/stdout/?format=ansi`
   answers 406 when `Accept: application/json` is sent, and 202 with an empty
   body while a job runs; survey `choices` arrive as a newline-separated
   string, not a list.
2. **Client layer first** (`internal/awx`): a typed method returning typed
   results. Paged endpoints take `(ctx, pageURL, search)` and return
   `Page[T]`.
3. **Then the model**: a `tea.Cmd` in `data.go` returning a message; a case in
   the `Update` switch in `model.go`.
4. **Then the view**: rendering in `view.go` (or the feature's own file), keys
   in the status bar and in `helpModal`.
5. **Tests against the mock**, covering the failure the change is about.
6. **Verify against the live instance, read-only**, via `internal/ui/live_test.go`.
7. **Commit** (see below).

Put a feature with more than ~150 lines of logic in its own file under
`internal/ui`, with its state as a struct on `Model` (`form`, `outputSearch`),
not as loose fields.

## Testing

Tests drive the real `Model` through its `Update` loop against an
`httptest` AWX. No UI logic is tested through fakes of itself.

- `step(t, m, msg)` applies a message and recursively drains the commands it
  returns, so one call settles the whole chain. It fails if the loop does not
  settle — that has caught real feedback loops.
- `key("enter")`, `typeText(t, m, "text")`, `focusField(t, m, "limit")` drive input.
- `show(t, label, m.View())` prints a rendered frame when `AWXTUI_SHOW=1` is set.
- **Mocks must mirror real AWX**, including its awkward parts: 406 on a
  JSON-`Accept` stdout call, 202 while running, `?search=` filtering on name
  and description, `count`/`next` paging.
- Assert that every view fits its terminal (`TestEveryViewRendersWithinTerminalBounds`).
- Live tests are skipped unless `AWXTUI_LIVE=1`:
  `AWXTUI_LIVE=1 AWXTUI_SHOW=1 go test -v ./internal/ui -run TestLive`.

**A test that passes the first time has proved nothing yet.** Check it can
fail: break the code and watch it go red, or make the fixture realistic enough
to matter. Two bugs hid behind tests that passed — output short enough to fit
one screen made every scroll assertion trivially true, and a page-cap test
stopped early because lazy loading filled the screen first.

To watch the real binary, use tmux — feeding keys through `script` does not work:

```sh
tmux new-session -d -s awx -x 100 -y 30 "./awxtui -config /path/config.yml"
tmux send-keys -t awx i; tmux capture-pane -p -t awx
```

## Conventions that already hold

- `gofmt` and `go vet` clean. Comments say *why*, never restate the code.
- **Lists are lazy**: one page, more on scroll, capped by `maxPages`. Search
  goes to AWX (`?search=`, debounced) because loaded rows are not the whole
  list. A complete list filters in memory for instant feedback.
- **Stale replies are dropped, never rendered.** Every message carries `gen`
  (bumped on instance switch) and list messages carry `seq` (bumped per
  keystroke). Any new async work must carry them too.
- **Errors surface, never vanish.** An error goes to `m.err`, shows one line in
  the status bar, and opens in full with `e`. Do not swallow an error into an
  empty result — a swallowed 406 is what made job output look permanently empty.
- **Writes are never repeated on an ambiguous failure.** GETs retry on 5xx and
  network errors; a launch retries only on 429.
- Colours come from `theme.go`. No literal escape codes or ad-hoc styles.
- Text inputs use `cursor.CursorStatic`: no blink timer for tests to wait on.
- Anything unbounded gets a cap with a visible label: `maxPages`,
  `maxOutputBytes`, `maxOutputPages`, `maxOutputRetries`.
- Bound every request with a context timeout (`cmdCtx`).

## Commits

- **Never add `Co-Authored-By: Claude`** or any AI attribution.
- Subject in the imperative, under ~55 characters.
- The body explains *why the change was needed* and what the code now does —
  including the real numbers that justified it ("209 templates against a page
  size of 200", "97928 jobs") and any bug found while building it. Someone
  reading `git log` should learn the reasoning, not just the diff.
- One logical change per commit; README updated in the same commit.

## Still open

Workflow job templates, schedules, ad-hoc commands, and launch prompts for
instance groups, labels, execution environments and credentials (those fall
back to template defaults today). See the "Not done yet" section of README.md.
