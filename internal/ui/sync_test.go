package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// openTab connects to a mock AWX and lands on one tab, returning both so the
// test can assert on what was written as well as on what is on screen.
func openTab(t *testing.T, tabKey string) (Model, *mock) {
	t.Helper()
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m = step(t, m, m.connect())
	return step(t, m, key(tabKey)), srv
}

// rowAt moves the cursor to the row whose first cell holds name.
func rowAt(t *testing.T, m Model, name string) Model {
	t.Helper()
	for i, r := range m.visible(m.active) {
		if strings.Contains(stripANSI(r.cells[0]), name) {
			m.cursor[m.active] = i
			return m
		}
	}
	t.Fatalf("no row named %q on tab %v", name, m.active)
	return m
}

func TestSyncProjectFollowsItsOwnUpdate(t *testing.T) {
	m, srv := openTab(t, "4")
	m = rowAt(t, m, "infra")
	m = step(t, m, key("s"))

	if m.err != nil {
		t.Fatalf("syncing a project failed: %v", m.err)
	}
	if got := srv.postedTo(); len(got) != 1 || got[0] != "POST /api/v2/projects/5/update/" {
		t.Fatalf("sync wrote %v, want one POST to /api/v2/projects/5/update/", got)
	}
	// The update AWX started is what we follow, and it is not a job: its id
	// belongs to /api/v2/project_updates/.
	if m.mode != modeOutput {
		t.Fatalf("sync left mode %v, want the output view", m.mode)
	}
	if m.outputJob.ID != 12 || m.outputJob.Resource() != awx.ResourceProjectUpdates {
		t.Fatalf("following %#v, want project update #12", m.outputJob)
	}
	// Output can only have come from /api/v2/project_updates/12/events/; the
	// job_events path the playbook viewer uses does not serve this record.
	if !strings.Contains(m.outputText, "Update project using git") {
		t.Errorf("no update output was read; got %q", m.outputText)
	}
	view := stripANSI(m.View())
	show(t, "project update output", m.View())
	for _, want := range []string{"#12", "infra", "project update"} {
		if !strings.Contains(view, want) {
			t.Errorf("output view is missing %q", want)
		}
	}

	// Cancelling must reach the update's own endpoint, not /api/v2/jobs/.
	m = step(t, m, key("c"))
	posts := srv.postedTo()
	if last := posts[len(posts)-1]; last != "POST /api/v2/project_updates/12/cancel/" {
		t.Errorf("cancel went to %q, want the project update's cancel endpoint", last)
	}

	// Leaving refreshes the list the sync came from: a sync never appears in
	// the Jobs tab, so refreshing that one would show nothing new.
	m = step(t, m, key("esc"))
	if m.mode != modeList || m.active != tabProjects {
		t.Errorf("esc left mode %v on tab %v", m.mode, m.active)
	}
}

func TestSyncProjectSurfacesARefusal(t *testing.T) {
	m, srv := openTab(t, "4")
	// A manual project has no SCM source, and AWX refuses to update it.
	m = rowAt(t, m, "legacy")
	m = step(t, m, key("s"))

	if m.err == nil {
		t.Fatal("syncing a manual project reported no error")
	}
	if !strings.Contains(m.err.Error(), "legacy") || !strings.Contains(m.err.Error(), "405") {
		t.Errorf("error does not name the project and the refusal: %v", m.err)
	}
	if m.mode != modeList {
		t.Errorf("a refused sync left mode %v, want the list", m.mode)
	}
	if !strings.Contains(stripANSI(m.View()), "✗") {
		t.Error("the refusal never reached the status bar")
	}
	if got := srv.postedTo(); len(got) != 1 {
		t.Errorf("wrote %v, want the one POST that was refused", got)
	}
	// The sync guard must not stay latched after a failure.
	if m.syncing {
		t.Error("still marked as syncing after the sync failed")
	}
}

func TestSyncInventoryUpdatesEverySource(t *testing.T) {
	m, srv := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("s"))

	if m.err != nil {
		t.Fatalf("syncing an inventory failed: %v", m.err)
	}
	// An inventory has no update endpoint of its own: each of its sources is
	// updated, and both must be started, not just the first.
	want := []string{
		"POST /api/v2/inventory_sources/10/update/",
		"POST /api/v2/inventory_sources/11/update/",
	}
	got := srv.postedTo()
	if len(got) != len(want) {
		t.Fatalf("sync wrote %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("write %d was %q, want %q", i, got[i], want[i])
		}
	}
	if m.mode != modeOutput || m.outputJob.Resource() != awx.ResourceInventoryUpdates {
		t.Fatalf("sync left mode %v following %#v", m.mode, m.outputJob)
	}
	if !strings.Contains(m.outputText, "Updating inventory") {
		t.Errorf("no sync output was read; got %q", m.outputText)
	}
	// Only one output fits on screen, so the notice has to account for both.
	if !strings.Contains(m.notice, "2 sources") {
		t.Errorf("notice %q does not say how many sources are syncing", m.notice)
	}
	show(t, "inventory sync output", m.View())
}

func TestSyncInventoryWithoutSources(t *testing.T) {
	m, srv := openTab(t, "3")
	m = rowAt(t, m, "handmade")
	m = step(t, m, key("s"))

	if m.err == nil {
		t.Fatal("syncing an inventory with no sources reported no error")
	}
	if !strings.Contains(m.err.Error(), "no sources") {
		t.Errorf("error does not say what is missing: %v", m.err)
	}
	if got := srv.postedTo(); len(got) != 0 {
		t.Errorf("an inventory with nothing to sync still wrote %v", got)
	}
	// The row says as much before the key is ever pressed.
	show(t, "inventories with a sources column", m.View())
	view := stripANSI(m.View())
	if !strings.Contains(view, "SOURCES") {
		t.Error("the inventory list has no sources column")
	}
	// "—" for an inventory with none, a count for one with sources.
	if !strings.Contains(view, "handmade") || !strings.Contains(view, "—") {
		t.Errorf("the sources column does not distinguish the two inventories:\n%s", view)
	}
}

func TestSyncIsRefusedByAReadOnlyClient(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false).ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m = step(t, m, m.connect())

	for _, c := range []struct{ tabKey, row string }{{"4", "infra"}, {"3", "production"}} {
		m = step(t, m, key(c.tabKey))
		m = rowAt(t, m, c.row)
		m = step(t, m, key("s"))
		if m.err == nil || !strings.Contains(m.err.Error(), "read-only") {
			t.Errorf("%s: read-only mode reported %v", c.row, m.err)
		}
		if m.mode != modeList {
			t.Errorf("%s: a refused sync left mode %v", c.row, m.mode)
		}
	}
	if got := srv.postedTo(); len(got) != 0 {
		t.Errorf("read-only client still wrote %v", got)
	}
}

// The details view is where a project's SCM state is read, so it is also
// where a sync is most likely to be wanted.
func TestSyncFromTheProjectDetails(t *testing.T) {
	m, srv := openTab(t, "4")
	m = rowAt(t, m, "infra")
	m = step(t, m, key("enter"))
	if m.mode != modeProject {
		t.Fatalf("details did not open: mode %v", m.mode)
	}
	if !strings.Contains(stripANSI(m.View()), "sync") {
		t.Error("the details view does not offer sync")
	}
	m = step(t, m, key("s"))
	if got := srv.postedTo(); len(got) != 1 || got[0] != "POST /api/v2/projects/5/update/" {
		t.Fatalf("sync from the details wrote %v", got)
	}
	if m.mode != modeOutput {
		t.Errorf("sync from the details left mode %v, want the output view", m.mode)
	}
	if m.project.project.ID != 0 {
		t.Error("the closed details view left its project behind")
	}
}

func TestSyncKeyIsAdvertised(t *testing.T) {
	for _, c := range []struct{ tabKey, name string }{{"4", "projects"}, {"3", "inventories"}} {
		m, _ := openTab(t, c.tabKey)
		if !strings.Contains(stripANSI(m.View()), "sync") {
			t.Errorf("the %s status bar does not mention sync", c.name)
		}
	}
	// And the tabs that have nothing to sync do not claim otherwise.
	for _, c := range []struct{ tabKey, name string }{{"1", "templates"}, {"2", "jobs"}} {
		m, srv := openTab(t, c.tabKey)
		if strings.Contains(stripANSI(m.View()), "s sync") {
			t.Errorf("the %s status bar offers sync", c.name)
		}
		m = step(t, m, key("s"))
		if got := srv.postedTo(); len(got) != 0 {
			t.Errorf("s on %s wrote %v", c.name, got)
		}
	}
	m, _ := openTab(t, "4")
	m = step(t, m, key("?"))
	if !strings.Contains(stripANSI(m.View()), "sync") {
		t.Error("the help modal does not list the sync key")
	}
}

// A held-down key must not start the same update twice: AWX would queue two.
func TestSyncDoesNotStartTwice(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m = step(t, m, m.connect())
	m = step(t, m, key("4"))
	m = rowAt(t, m, "infra")

	// Both presses land before either reply is drained.
	next, first := m.Update(key("s"))
	m = next.(Model)
	next, second := m.Update(key("s"))
	m = next.(Model)
	if second != nil {
		t.Error("a second sync was dispatched while the first was in flight")
	}
	for _, msg := range drain(first) {
		m = step(t, m, msg)
	}
	if got := srv.postedTo(); len(got) != 1 {
		t.Fatalf("two presses wrote %v, want one POST", got)
	}
	// And the guard clears, so the next sync still works.
	if m.syncing {
		t.Error("still marked as syncing after the update started")
	}
}
