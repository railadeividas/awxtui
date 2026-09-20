package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/railadeividas/awxtui/internal/awx"
)

// The "health" column on the list is about host failures, not sync status —
// AWX has no such field on the inventory itself. Enter opens a details view
// that fetches the real per-source status.
func TestInventoryEnterOpensDetails(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))

	if m.mode != modeInventory {
		t.Fatalf("enter on an inventory left mode %v, want modeInventory", m.mode)
	}
	view := stripANSI(m.View())
	show(t, "inventory details", m.View())
	for _, want := range []string{
		"Inventory #3", "production", "Default",
		"inventories/prod.yml", "inventories/edge.yml",
		"successful", "failed",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("details view is missing %q", want)
		}
	}
	// esc goes back to the list and leaves no state behind for the next one.
	m = step(t, m, key("esc"))
	if m.mode != modeList || m.inventory.inventory.ID != 0 {
		t.Errorf("esc left mode %v, inventory %+v", m.mode, m.inventory.inventory)
	}
}

// An inventory whose hosts were entered by hand has no sources: the view must
// say so rather than render an empty sources section that looks unloaded.
func TestInventoryDetailsWithoutSources(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "handmade")
	m = step(t, m, key("enter"))

	if got := m.inventory.inventory.Name; got != "handmade" {
		t.Fatalf("opened inventory %q, want handmade", got)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "no sources") {
		t.Errorf("handmade inventory view does not say it has no sources: %q", view)
	}
}

// Hosts and groups are fetched as soon as the details open and rendered
// right there, both at once — no separate page or expand step needed to see
// or select either one.
func TestInventoryDetailsShowsHostsAndGroupsInline(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")

	busy := probe(t, m, key("enter"))
	if busy.mode != modeInventory {
		t.Fatalf("enter left mode %v before the response landed, want modeInventory", busy.mode)
	}
	if !busy.inventory.hosts.loading || !busy.inventory.groups.loading {
		t.Fatalf("expected hosts and groups to start loading immediately, got hosts.loading=%v groups.loading=%v",
			busy.inventory.hosts.loading, busy.inventory.groups.loading)
	}

	settled := step(t, m, key("enter"))
	if settled.inventory.hosts.loading || len(settled.inventory.hosts.rows) == 0 {
		t.Errorf("hosts never settled: loading=%v rows=%d", settled.inventory.hosts.loading, len(settled.inventory.hosts.rows))
	}
	if settled.inventory.groups.loading || len(settled.inventory.groups.rows) == 0 {
		t.Errorf("groups never settled: loading=%v rows=%d", settled.inventory.groups.loading, len(settled.inventory.groups.rows))
	}
	view := stripANSI(settled.View())
	for _, want := range []string{"web-01", "db-01", "web", "db"} {
		if !strings.Contains(view, want) {
			t.Errorf("details view does not show %q inline:\n%s", want, view)
		}
	}
	show(t, "inventory details with hosts and groups", settled.View())
}

// The cursor moves across groups first, then hosts, as one combined list —
// "h" jumps straight to the first host, skipping however many groups there
// are.
func TestInventoryCursorMovesAcrossGroupsThenHosts(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))

	if m.inventory.cursor != 0 {
		t.Fatalf("cursor should start at the first group, got %d", m.inventory.cursor)
	}
	nGroups := len(m.inventory.groups.rows)
	m = step(t, m, key("h"))
	if m.inventory.cursor != nGroups {
		t.Fatalf("h should jump the cursor to the first host (index %d), got %d", nGroups, m.inventory.cursor)
	}
}

// Scrolling deep into a long list must keep the cursor's row on screen even
// once its table's own header has to be pinned back at the top — the pinned
// header eats into the same window the cursor is clamped against, and a
// stale clamp against the wrong (larger) window let the cursor scroll
// behind it, invisible.
func TestCursorStaysVisibleBehindAPinnedSectionHeader(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))

	// Give the inventory far more hosts than fit on screen, so scrolling
	// through them pins the "N hosts" header the way 47 real hosts did.
	hosts := make([]awx.Host, 60)
	for i := range hosts {
		hosts[i] = awx.Host{ID: 1000 + i, Name: fmt.Sprintf("host-%02d", i), Enabled: true}
	}
	rows, names := memberRows(memberHosts, hosts, nil)
	m.inventory.hosts.rows, m.inventory.hosts.names = rows, names
	m.inventory.hosts.count = len(hosts)

	m = step(t, m, key("h")) // jump the cursor to the first host
	for i := 0; i < 40; i++ {
		m = step(t, m, key("down"))
		want := m.inventory.hosts.names[m.inventory.cursor-len(m.inventory.groups.rows)]
		view := stripANSI(m.View())
		if !strings.Contains(view, want) {
			t.Fatalf("after %d downs, cursor is on %q but it is not visible:\n%s", i+1, want, view)
		}
	}
}

// A limit built from many selected names is easily wider than the modal —
// wrapping it, the way every other field wraps, would turn one line into a
// screenful and push the sources and tables clean out of the header's fixed
// space. It must stay a single line, however many names are behind it.
func TestLimitLineStaysOneLineHoweverManyAreSelected(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))

	hosts := make([]awx.Host, 60)
	for i := range hosts {
		hosts[i] = awx.Host{ID: 1000 + i, Name: fmt.Sprintf("host-%02d", i), Enabled: true}
	}
	rows, names := memberRows(memberHosts, hosts, nil)
	m.inventory.hosts.rows, m.inventory.hosts.names = rows, names
	m.inventory.hosts.count = len(hosts)

	m = step(t, m, key("h")) // jump to the first host
	for i := 0; i < len(hosts); i++ {
		m = step(t, m, key(" "))
		m = step(t, m, key("down"))
	}
	if len(m.limitSel.names) != len(hosts) {
		t.Fatalf("expected all %d hosts selected, got %d", len(hosts), len(m.limitSel.names))
	}

	header, _, _, _ := m.inventoryBody(m.inventoryWidth())
	limitLines := 0
	for _, l := range header {
		if strings.Contains(stripANSI(l), "selected") {
			limitLines++
		}
	}
	if limitLines != 1 {
		t.Fatalf("limit line count = %d, want exactly 1 — it must not wrap", limitLines)
	}
	// The sources section this pushed below it must still be reachable
	// within the header budget the fields view assumes.
	view := stripANSI(m.View())
	if !strings.Contains(view, "sources") {
		t.Errorf("sources section got pushed out of view by a wrapped limit line:\n%s", view)
	}
}

// Space toggles the row under the cursor and immediately updates the live
// limit selection — there is no separate confirm step.
func TestSpaceTogglesRowAndUpdatesLimitLive(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	firstGroupName := m.inventory.groups.names[0]

	m = step(t, m, key(" "))
	if m.limitSel.inventoryID != 3 || m.limitSel.limit() != firstGroupName {
		t.Fatalf("space did not select the first group live: %+v", m.limitSel)
	}

	m = step(t, m, key(" ")) // toggle back off
	if m.limitSel.inventoryID != 0 {
		t.Fatalf("second space should have cleared the selection, got %+v", m.limitSel)
	}
}

// "x" clears every checkbox across both lists, not just the derived limit.
func TestXClearsBothLists(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	m = step(t, m, key(" "))
	m = step(t, m, key("h"))
	m = step(t, m, key(" "))
	if len(m.inventory.groups.chosen) == 0 || len(m.inventory.hosts.chosen) == 0 {
		t.Fatalf("expected a selection in both lists before clearing: groups=%v hosts=%v",
			m.inventory.groups.chosen, m.inventory.hosts.chosen)
	}

	m = step(t, m, key("x"))
	if len(m.inventory.groups.chosen) != 0 || len(m.inventory.hosts.chosen) != 0 || m.limitSel.inventoryID != 0 {
		t.Fatalf("x did not clear both lists: groups=%v hosts=%v limitSel=%+v",
			m.inventory.groups.chosen, m.inventory.hosts.chosen, m.limitSel)
	}
}

// The status bar promises what enter and h do, and it stays accurate once the
// details view is open.
func TestInventoryLegendMatchesBehaviour(t *testing.T) {
	m, _ := openTab(t, "3")
	if got := stripANSI(m.statusView()); !strings.Contains(got, "details") {
		t.Errorf("inventories legend does not offer details: %q", got)
	}
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	if m.mode != modeInventory {
		t.Fatalf("enter did not open the details view, mode %v", m.mode)
	}
	got := stripANSI(m.statusView())
	if !strings.Contains(got, "move") {
		t.Errorf("details legend does not mention moving the cursor: %q", got)
	}
	if !strings.Contains(got, "hosts") {
		t.Errorf("details legend does not mention hosts: %q", got)
	}
}
