package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
	"github.com/railadeividas/awxtui/internal/state"
)

// onTab connects a model and leaves it on one tab.
func onTab(t *testing.T, srv *mock, want tab, opts ...Option) Model {
	t.Helper()
	m := New(awx.New(srv.URL, "test-token", false), opts...)
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, m.connect())
	m = step(t, m, key(string(rune('1'+want))))
	if m.active != want {
		t.Fatalf("expected tab %v, got %v", want, m.active)
	}
	return m
}

func rowIDs(m Model, t tab) []int {
	out := make([]int, 0, len(m.rows[t]))
	for _, r := range m.visible(t) {
		out = append(out, r.id)
	}
	return out
}

// setShow opens the panel, sets one field and applies it, the way the keys do.
func setShow(t *testing.T, m Model, field, value string) Model {
	t.Helper()
	m = step(t, m, key("f"))
	if m.mode != modeShow {
		t.Fatalf("f should open the show panel, got mode %v", m.mode)
	}
	rows := choicesFor(m.panel.tab)
	// startedBy shares its row with "mine": both are the owner row, one a
	// toggle while it holds no text, the other free text once it does.
	lookup := field
	if field == "startedBy" {
		lookup = "mine"
	}
	at := -1
	for i, c := range rows {
		if c.field == lookup {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("tab %v has no %q choice; it offers %v", m.panel.tab, field, rows)
	}
	for m.panel.cursor < at {
		m = step(t, m, key("down"))
	}
	switch {
	case field == "startedBy":
		for m.panel.startedByInput.Value() != "" {
			m = step(t, m, key("backspace"))
		}
		if value != "" {
			m = typeText(t, m, value)
		}
	case rows[at].kind == kindMulti:
		want := map[string]bool{}
		for _, v := range strings.Split(value, ",") {
			if v != "" {
				want[v] = true
			}
		}
		for oi, o := range rows[at].options {
			for m.panel.optCursor != oi {
				m = step(t, m, key("right"))
			}
			if slices.Contains(*m.panel.draft.multi(field), o.value) != want[o.value] {
				m = step(t, m, key(" "))
			}
		}
	default:
		for i := 0; i < len(rows[at].options)*2; i++ {
			if m.panel.draft.get(field) == value {
				break
			}
			m = step(t, m, key("right"))
		}
	}
	if got := m.panel.draft.get(field); got != value {
		t.Fatalf("could not set %s to %q, draft holds %q", field, value, got)
	}
	return step(t, m, key("enter"))
}

// Every tab can pin, and each keeps its own list: ids only identify a record
// inside its own collection.
func TestPinningWorksOnEveryTab(t *testing.T) {
	srv := mockAWX(t)
	for _, tc := range []struct {
		tab  tab
		want int
		name string
	}{
		{tabTemplates, 7, "Deploy web app"},
		{tabJobs, 43, "Deploy web app"},
		{tabInventories, 3, "production"},
		{tabProjects, 5, "infra"},
	} {
		m := onTab(t, srv, tc.tab)
		if len(m.rows[tc.tab]) == 0 {
			t.Fatalf("%v: nothing loaded (err %v)", tc.tab, m.err)
		}
		m = step(t, m, key("p"))
		if !m.pinned(tc.tab, tc.want) {
			t.Errorf("%v: p did not pin #%d (notice %q, err %v)", tc.tab, tc.want, m.notice, m.err)
		}
		if got := stripANSI(m.rows[tc.tab][0].cells[0]); !strings.HasPrefix(got, "★") {
			t.Errorf("%v: pinned row is not marked, first cell = %q", tc.tab, got)
		}
		if m.pinLabel() != "unpin" {
			t.Errorf("%v: the key legend still offers to pin an already pinned row", tc.tab)
		}
		// The name is kept so a pin can be listed before AWX answers, and
		// still be named after AWX has deleted the record. It must be the
		// record's name — not the row's first cell, which is the id on the
		// Jobs tab and already carries the pin marker everywhere else.
		pins := m.store.Pins(m.instance, pinGroup(tc.tab))
		if len(pins) != 1 {
			t.Fatalf("%v: store holds %d pins, want 1", tc.tab, len(pins))
		}
		if got := pins[0].Name; got != tc.name {
			t.Errorf("%v: pinned name = %q, want %q", tc.tab, got, tc.name)
		}
		if tc.tab == tabJobs && pins[0].Kind != "job" {
			t.Errorf("jobs: pinned kind = %q, want job; it says which collection the output comes from", pins[0].Kind)
		}

		m = step(t, m, key("p"))
		if m.pinned(tc.tab, tc.want) {
			t.Errorf("%v: p did not unpin #%d", tc.tab, tc.want)
		}
	}
}

func TestPinnedOnlyShowsJustThePins(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabTemplates)
	// #8 is the second template; pin it and nothing else.
	m = step(t, m, key("down"))
	m = step(t, m, key("p"))

	m = setShow(t, m, "pinned", "yes")
	if got := rowIDs(m, tabTemplates); len(got) != 1 || got[0] != 8 {
		t.Fatalf("pinned-only shows %v, want just #8 (err %v)", got, m.err)
	}
	if label := m.countLabel(tabTemplates); !strings.Contains(label, "pinned") {
		t.Errorf("count label = %q, should say the view is narrowed", label)
	}

	// Unpinning the last pin empties the view rather than leaving a stale row.
	m = step(t, m, key("p"))
	if got := rowIDs(m, tabTemplates); len(got) != 0 {
		t.Errorf("after unpinning, pinned-only shows %v", got)
	}
}

func TestShowFilterNarrowsJobsServerSide(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)
	if got := len(rowIDs(m, tabJobs)); got != 6 {
		t.Fatalf("the jobs tab should list every kind of run, got %d rows (err %v)", got, m.err)
	}

	m = setShow(t, m, "mine", "yes")
	for _, id := range rowIDs(m, tabJobs) {
		if id == 40 {
			t.Errorf("mine shows #40, which colleague started")
		}
	}
	queries := srv.unified()
	if len(queries) == 0 || !strings.Contains(queries[len(queries)-1], "created_by=1") {
		t.Fatalf("the owner filter did not reach AWX, queries: %v", queries)
	}

	m = setShow(t, m, "status", "failed")
	if got := rowIDs(m, tabJobs); len(got) != 0 {
		t.Errorf("none of admin's runs failed, got %v", got)
	}
	if q := srv.unified(); !strings.Contains(q[len(q)-1], "status__in=failed") {
		t.Errorf("the status filter did not reach AWX: %q", q[len(q)-1])
	}

	// Clearing the owner leaves colleague's failed run.
	m = setShow(t, m, "mine", "")
	if got := rowIDs(m, tabJobs); len(got) != 1 || got[0] != 40 {
		t.Errorf("failed runs = %v, want just #40", got)
	}
}

func TestShowFilterNarrowsJobsByKind(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)

	m = setShow(t, m, "kind", "project_update")
	if got := rowIDs(m, tabJobs); len(got) != 1 || got[0] != 12 {
		t.Fatalf("project updates = %v, want just #12 (err %v)", got, m.err)
	}
	if q := srv.unified(); !strings.Contains(q[len(q)-1], "type__in=project_update") {
		t.Errorf("the kind filter did not reach AWX: %q", q[len(q)-1])
	}
	if label := m.countLabel(tabJobs); !strings.Contains(label, "project updates") {
		t.Errorf("count label = %q, should name the kind", label)
	}
}

// A run worth a look is usually failed or still running — two statuses at
// once, not one, which is why status is a set rather than a single choice.
func TestShowFilterCanSelectMultipleStatuses(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)

	m = setShow(t, m, "status", "failed,running")
	got := rowIDs(m, tabJobs)
	want := map[int]bool{43: true, 40: true, 11: true}
	if len(got) != len(want) {
		t.Fatalf("failed+running = %v, want the 3 runs in either status", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("row #%d is neither failed nor running", id)
		}
	}
	if q := srv.unified(); !strings.Contains(q[len(q)-1], "status__in=failed%2Crunning") &&
		!strings.Contains(q[len(q)-1], "status__in=failed,running") {
		t.Errorf("the multi-status filter did not reach AWX as one comma list: %q", q[len(q)-1])
	}

	// Toggling failed back off narrows to running alone.
	m = setShow(t, m, "status", "running")
	got = rowIDs(m, tabJobs)
	want = map[int]bool{43: true, 11: true}
	if len(got) != len(want) {
		t.Fatalf("running alone = %v, want just #43 and #11", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("row #%d is not running", id)
		}
	}
}

func TestShowFilterOffersPendingAndAnyStatus(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)

	// Pending is sent to AWX like every other status, even if this small mock
	// does not happen to contain a pending run.
	m = setShow(t, m, "status", "pending")
	if got := m.show[tabJobs].status; len(got) != 1 || got[0] != "pending" {
		t.Fatalf("pending status = %q, want pending", got)
	}
	if q := srv.unified(); !strings.Contains(q[len(q)-1], "status__in=pending") {
		t.Errorf("the pending status filter did not reach AWX: %q", q[len(q)-1])
	}

	// The visible "any" choice clears the status set, so all runs are shown.
	m = setShow(t, m, "status", "")
	if got := m.show[tabJobs].status; len(got) != 0 {
		t.Errorf("any status should clear the selection, got %q", got)
	}
	if got := len(rowIDs(m, tabJobs)); got != 6 {
		t.Errorf("any status returned %d runs, want all 6", got)
	}
	if q := srv.unified(); strings.Contains(q[len(q)-1], "status__in=") {
		t.Errorf("any status should omit status__in, got %q", q[len(q)-1])
	}
}

// Kind is a set too, for the same reason status is: wanting jobs and project
// updates together, but not inventory syncs, is one narrowing, not two.
func TestShowFilterCanSelectMultipleKinds(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)

	m = setShow(t, m, "kind", "job,project_update")
	got := rowIDs(m, tabJobs)
	want := map[int]bool{43: true, 42: true, 40: true, 12: true}
	if len(got) != len(want) {
		t.Fatalf("job+project_update = %v, want the 4 runs of either kind", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("row #%d is neither a job nor a project update", id)
		}
	}
	if q := srv.unified(); !strings.Contains(q[len(q)-1], "type__in=job%2Cproject_update") &&
		!strings.Contains(q[len(q)-1], "type__in=job,project_update") {
		t.Errorf("the multi-kind filter did not reach AWX as one comma list: %q", q[len(q)-1])
	}

	// Kind's own "any" is not a member alongside job/project_update/
	// inventory_update - selecting it resets the whole row to unfiltered,
	// the same blank-means-any value every other row uses.
	m = step(t, m, key("f"))
	m = step(t, m, key("down"))
	m = step(t, m, key("down")) // owner -> status -> kind, landing on "any"
	m = step(t, m, key(" "))
	if got := m.panel.draft.kind; len(got) != 0 {
		t.Fatalf("selecting any should clear kind, draft holds %v", got)
	}
	m = step(t, m, key("enter"))
	if len(rowIDs(m, tabJobs)) != 6 {
		t.Errorf("kind cleared to any should show every run, got %v", rowIDs(m, tabJobs))
	}
}

// "mine" only ever means the connected user; finding a deploy bot's or a
// colleague's runs needs their name typed in instead.
func TestShowFilterNarrowsByStartedByText(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)

	m = setShow(t, m, "startedBy", "colleague")
	if got := rowIDs(m, tabJobs); len(got) != 1 || got[0] != 40 {
		t.Fatalf("started-by 'colleague' = %v, want just #40", got)
	}
	if q := srv.unified(); !strings.Contains(q[len(q)-1], "created_by__username__icontains=colleague") {
		t.Errorf("the started-by filter did not reach AWX: %q", q[len(q)-1])
	}
	if label := m.countLabel(tabJobs); !strings.Contains(label, "by colleague") {
		t.Errorf("count label = %q, should name who it is narrowed to", label)
	}

	// A partial, differently-cased fragment still matches, the way AWX's
	// icontains lookup does.
	m = setShow(t, m, "startedBy", "COLL")
	if got := rowIDs(m, tabJobs); len(got) != 1 || got[0] != 40 {
		t.Errorf("case-insensitive fragment 'COLL' = %v, want just #40", got)
	}
}

// esc must leave the view exactly as it was, or a panel is a trap.
func TestEscapingTheShowPanelChangesNothing(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)
	before := rowIDs(m, tabJobs)

	m = step(t, m, key("f"))
	m = step(t, m, key("right")) // started by: me
	m = step(t, m, key("esc"))
	if m.mode != modeShow && m.show[tabJobs].active() {
		t.Errorf("esc applied the draft: %+v", m.show[tabJobs])
	}
	if got := rowIDs(m, tabJobs); len(got) != len(before) {
		t.Errorf("esc changed the rows: %v, was %v", got, before)
	}

	// c clears every choice at once. It is pressed off the owner row, where
	// a letter is just as likely to be the start of a typed username.
	m = setShow(t, m, "mine", "yes")
	m = step(t, m, key("f"))
	m = step(t, m, key("down"))
	m = step(t, m, key("c"))
	m = step(t, m, key("enter"))
	if m.show[tabJobs].active() {
		t.Errorf("c should clear the whole filter, left %+v", m.show[tabJobs])
	}

	// On the owner row, backspace erases its text like any other field; only
	// once that text is empty does backspace fall back to clearing the whole
	// filter, rather than doing nothing.
	m = setShow(t, m, "startedBy", "colleague")
	m = setShow(t, m, "status", "failed")
	m = step(t, m, key("f"))
	for range []rune("colleague") {
		m = step(t, m, key("backspace"))
	}
	if got := m.panel.draft.startedBy; got != "" {
		t.Fatalf("backspace did not erase the owner row's text, left %q", got)
	}
	if len(m.panel.draft.status) == 0 {
		t.Fatalf("erasing the owner row's text should not have touched status")
	}
	m = step(t, m, key("backspace"))
	m = step(t, m, key("enter"))
	if m.show[tabJobs].active() {
		t.Errorf("backspace on the now-empty owner row should clear the whole filter, left %+v", m.show[tabJobs])
	}
}

func TestPinsSurviveARestartAndRefreshFromAWX(t *testing.T) {
	srv := mockAWX(t)
	path := filepath.Join(t.TempDir(), "pins.json")
	store, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	m := onTab(t, srv, tabJobs, WithStore(store))
	m = step(t, m, key("down")) // #42
	m = step(t, m, key("p"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the pin was not written: %v (err %v)", err, m.err)
	}

	// A second process reading the same file.
	reopened, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m2 := onTab(t, srv, tabJobs, WithStore(reopened))
	m2 = setShow(t, m2, "pinned", "yes")
	if got := rowIDs(m2, tabJobs); len(got) != 1 || got[0] != 42 {
		t.Fatalf("the pin did not survive the restart, pinned view = %v (err %v)", got, m2.err)
	}
	// The row is AWX's live record, not the name saved beside the id.
	if j, ok := m2.selectedJob(); !ok || j.Status != "successful" {
		t.Errorf("pinned rows should carry AWX's current status, got %+v", j)
	}
}

// A pin whose record AWX has deleted must be admitted in the count, not
// quietly dropped: "1 of 2" is the difference between gone and not looked up.
func TestPinnedCountShowsRecordsAWXNoLongerHas(t *testing.T) {
	srv := mockAWX(t)
	store := state.Memory()
	for _, id := range []int{42, 777} {
		if _, err := store.Toggle("", state.GroupRuns, state.Pin{ID: id}); err != nil {
			t.Fatal(err)
		}
	}

	m := onTab(t, srv, tabJobs, WithStore(store))
	m = setShow(t, m, "pinned", "yes")
	if got := rowIDs(m, tabJobs); len(got) != 1 || got[0] != 42 {
		t.Fatalf("only the surviving pin should be listed, got %v (err %v)", got, m.err)
	}
	if label := m.countLabel(tabJobs); !strings.Contains(label, "1 of 2") {
		t.Errorf("count label = %q, want it to admit one pin is gone", label)
	}
}

func TestPinFromTheOutputView(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)
	m = step(t, m, key("enter"))
	if m.mode != modeOutput {
		t.Fatalf("expected the output view, got %v (err: %v)", m.mode, m.err)
	}
	if got := m.outputPinLabel(); got != "pin" {
		t.Fatalf("pin label before pinning = %q", got)
	}
	m = step(t, m, key("p"))
	if !m.pinned(tabJobs, 43) {
		t.Errorf("p in the output view did not pin #43 (notice %q, err %v)", m.notice, m.err)
	}
	if got := m.outputPinLabel(); got != "unpin" {
		t.Errorf("pin label after pinning = %q", got)
	}
}

// /api/v2/unified_jobs/ serves kinds awxtui has no output endpoint for.
// TestOpeningAWorkflowJobShowsItsDetails checks that a workflow job in the
// unified Jobs list opens its launch details rather than output: a workflow
// job has no stdout of its own — its output lives per-node — so fetching
// /api/v2/workflow_jobs/11/stdout/ would find nothing there to show.
func TestOpeningAWorkflowJobShowsItsDetails(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)
	for i, r := range m.visible(tabJobs) {
		if r.id == 11 {
			m.cursor[tabJobs] = i
		}
	}
	m = step(t, m, key("enter"))
	if m.err != nil {
		t.Fatalf("opening workflow job details failed: %v", m.err)
	}
	if m.mode != modeJob {
		t.Fatalf("expected launch details for a workflow job, got mode %v", m.mode)
	}
	if m.job.job.SummaryFields.WorkflowJobTemplate.Name != "Nightly pipeline" {
		t.Errorf("expected the workflow template name, got %q", m.job.job.SummaryFields.WorkflowJobTemplate.Name)
	}
}

func TestOwnRunsAreMarkedInTheUnfilteredList(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)

	var mine, theirs string
	for _, r := range m.visible(tabJobs) {
		switch r.id {
		case 42:
			mine = r.cells[len(r.cells)-1]
		case 40:
			theirs = r.cells[len(r.cells)-1]
		}
	}
	if mine == "" || theirs == "" {
		t.Fatalf("expected both #42 and #40 on screen, got %v", rowIDs(m, tabJobs))
	}
	// Colour alone would not do: lipgloss drops it when the output is not a
	// terminal, and so does a monochrome one.
	if got := stripANSI(mine); got != "you" {
		t.Errorf("your own run should be marked as yours, by-column = %q", got)
	}
	if got := stripANSI(theirs); got != "colleague" {
		t.Errorf("someone else's run should name them, by-column = %q", got)
	}
}

func TestSyncsAreLabelledInTheJobsList(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)

	var line string
	for _, r := range m.visible(tabJobs) {
		if r.id == 12 {
			line = stripANSI(strings.Join(r.cells, " "))
		}
	}
	if line == "" {
		t.Fatalf("project update #12 is not in the jobs list: %v", rowIDs(m, tabJobs))
	}
	// A project update and a playbook job would otherwise read identically.
	if !strings.Contains(line, "project update") {
		t.Errorf("row for #12 should say what kind of run it is, got %q", line)
	}
}

// on reports which legend entries are marked as in force. The mark itself is
// a colour, which lipgloss drops when the output is not a terminal, so the
// legend is checked as data rather than as a rendered string.
func on(items []legend) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		if it.on {
			out[it.key] = true
		}
	}
	return out
}

// The key line is the only thing on screen that is always there. An entry
// that describes the state of the view — unpin, show, search — is marked, so
// a narrowed or pinned view can be recognised without reading the rows.
func TestTheLegendMarksWhatIsInForce(t *testing.T) {
	srv := mockAWX(t)
	m := onTab(t, srv, tabJobs)
	if got := on(m.listKeys()); len(got) != 0 {
		t.Fatalf("nothing is in force yet, but the legend marks %v", got)
	}

	m = step(t, m, key("p"))
	marked := on(m.listKeys())
	if !marked["p"] {
		t.Errorf("the row is pinned; p reads %q and should be marked", m.pinLabel())
	}
	if marked["f"] {
		t.Errorf("the list is not narrowed, but f is marked")
	}

	m = setShow(t, m, "status", "successful")
	if !on(m.listKeys())["f"] {
		t.Errorf("the list is narrowed to %q, but f is not marked", m.show[tabJobs].summary())
	}
	// Moving off the pinned row unmarks p: the mark describes the row under
	// the cursor, not the tab.
	m = step(t, m, key("down"))
	if on(m.listKeys())["p"] {
		t.Errorf("p is marked on an unpinned row (label %q)", m.pinLabel())
	}

	// The same rule in the output view, where follow is the state you are in.
	m = step(t, m, key("enter"))
	if m.mode != modeOutput {
		t.Fatalf("expected the output view, got %v (err %v)", m.mode, m.err)
	}
	if !m.follow {
		t.Fatal("output opens in follow mode")
	}
	if !on(m.outputKeys())["f"] {
		t.Errorf("follow is on, but f is not marked")
	}
	m = step(t, m, key("f"))
	if on(m.outputKeys())["f"] {
		t.Errorf("follow is off, but f is still marked")
	}
	if on(m.outputKeys())["p"] {
		t.Errorf("this run is not pinned, but p is marked")
	}
	m = step(t, m, key("p"))
	if !on(m.outputKeys())["p"] {
		t.Errorf("this run is pinned now; p reads %q and should be marked", m.outputPinLabel())
	}
}

// A filter is saved where the pins are, and is in force before the first
// request goes out — restoring it after the list loaded would fetch the
// unnarrowed list first and then throw it away.
func TestNarrowingSurvivesARestart(t *testing.T) {
	srv := mockAWX(t)
	path := filepath.Join(t.TempDir(), "pins.json")
	store, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	m := onTab(t, srv, tabJobs, WithStore(store))
	m = setShow(t, m, "mine", "yes")
	m = setShow(t, m, "status", "successful")

	reopened, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.View("", state.GroupRuns); got["mine"] != "yes" || got["status"] != "successful" {
		t.Fatalf("the filter was not written: %v", got)
	}

	fresh := New(awx.New(srv.URL, "test-token", false), WithStore(reopened))
	if got := fresh.show[tabJobs]; !got.mine || len(got.status) != 1 || got.status[0] != "successful" {
		t.Fatalf("a new model did not restore the filter, got %+v", got)
	}
	before := len(srv.unified())
	fresh = step(t, fresh, tea.WindowSizeMsg{Width: 120, Height: 30})
	fresh = step(t, fresh, fresh.connect())
	fresh = step(t, fresh, key("2"))
	for _, q := range srv.unified()[before:] {
		if !strings.Contains(q, "created_by=1") || !strings.Contains(q, "status__in=successful") {
			t.Errorf("a request went out unnarrowed after restart: %q", q)
		}
	}
	if got := rowIDs(fresh, tabJobs); len(got) == 0 {
		t.Errorf("restored filter matched nothing (err %v)", fresh.err)
	}
	if label := fresh.countLabel(tabJobs); !strings.Contains(label, "mine · successful") {
		t.Errorf("count label = %q; a restored filter has to be visible, or an empty view looks like an empty instance", label)
	}

	// Clearing it is saved too, rather than coming back on the next start.
	// c is pressed off the owner row, where a letter is typed text instead.
	fresh = step(t, fresh, key("f"))
	fresh = step(t, fresh, key("down"))
	fresh = step(t, fresh, key("c"))
	fresh = step(t, fresh, key("enter"))
	again, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.View("", state.GroupRuns); len(got) != 0 {
		t.Errorf("a cleared filter is still on disk: %v", got)
	}
}

// Each instance keeps its own narrowing, the way it keeps its own pins: the
// two AWXes have nothing to do with each other.
func TestNarrowingIsPerInstanceAndPerTab(t *testing.T) {
	prod, staging := namedMock(t, "prod", 3), namedMock(t, "staging", 2)
	path := filepath.Join(t.TempDir(), "pins.json")
	store, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	m := twoInstanceModel(t, prod, staging, WithStore(store))
	m = setShow(t, m, "pinned", "yes")
	if !m.show[tabTemplates].pinnedOnly {
		t.Fatalf("templates should be narrowed to pins")
	}
	if m.show[tabJobs].active() {
		t.Errorf("narrowing the templates tab also narrowed jobs: %+v", m.show[tabJobs])
	}

	m = step(t, m, key("i"))
	m = step(t, m, key("down"))
	m = step(t, m, key("enter"))
	if m.instance != "staging" {
		t.Fatalf("expected staging, got %q (err %v)", m.instance, m.err)
	}
	if m.show[tabTemplates].active() {
		t.Errorf("prod's filter followed the switch to staging: %+v", m.show[tabTemplates])
	}

	// Going back restores it.
	m = step(t, m, key("i"))
	m = step(t, m, key("up"))
	m = step(t, m, key("enter"))
	if m.instance != "prod" {
		t.Fatalf("expected prod, got %q", m.instance)
	}
	if !m.show[tabTemplates].pinnedOnly {
		t.Errorf("prod's filter was not restored on the way back: %+v", m.show[tabTemplates])
	}
}

// Switching instances rebuilds the model. The store has to come along, or
// pins quietly stop being written for the rest of the session.
func TestSwitchingInstanceKeepsThePersistentStore(t *testing.T) {
	prod, staging := namedMock(t, "prod", 3), namedMock(t, "staging", 2)
	path := filepath.Join(t.TempDir(), "pins.json")
	store, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Toggle("prod", state.GroupTemplates, state.Pin{ID: 1, Name: "kept"}); err != nil {
		t.Fatal(err)
	}

	m := twoInstanceModel(t, prod, staging, WithStore(store))
	m = step(t, m, key("i"))
	m = step(t, m, key("down"))
	m = step(t, m, key("enter"))
	if m.instance != "staging" {
		t.Fatalf("expected to be on staging, got %q (err %v)", m.instance, m.err)
	}
	m = step(t, m, key("p"))

	reloaded, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Any("staging", state.GroupTemplates) {
		t.Errorf("a pin made after switching instance was not written to disk")
	}
	if !reloaded.Pinned("prod", state.GroupTemplates, 1) {
		t.Errorf("switching instance lost the previous instance's pins")
	}
}
