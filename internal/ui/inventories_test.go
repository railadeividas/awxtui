package ui

import (
	"strings"
	"testing"
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

// "h" inside the details view expands its hosts inline — no separate page,
// no mode change, so there is nothing to navigate back out of but the
// expansion itself.
func TestInventoryDetailsHKeyExpandsHosts(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	m = step(t, m, key("h"))

	if m.mode != modeInventory || m.inventory.focus != focusHosts {
		t.Fatalf("h left mode %v focus %v, want modeInventory/focusHosts", m.mode, m.inventory.focus)
	}
	if m.inventory.inventory.Name != "production" || len(m.inventory.hosts.rows) == 0 {
		t.Fatalf("h did not expand production's hosts: inventory %q, rows %d",
			m.inventory.inventory.Name, len(m.inventory.hosts.rows))
	}
}

// Hosts and groups are fetched as soon as the details open, not only once h/g
// expands them, so both the inline preview and the expansion are ready
// immediately — no spinner the user has to wait through after pressing h/g.
func TestInventoryDetailsLoadsMembersUpFront(t *testing.T) {
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
	view := stripANSI(settled.View())
	if !strings.Contains(view, "web-01") {
		t.Errorf("details view does not preview host names once loaded:\n%s", view)
	}
}

// esc from an expanded hosts/groups list collapses back to the details
// fields — it takes a second esc to leave the details view entirely.
func TestMembersEscCollapsesBeforeLeavingDetails(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	m = step(t, m, key("h"))
	if m.inventory.focus != focusHosts {
		t.Fatalf("h left focus %v, want focusHosts", m.inventory.focus)
	}

	m = step(t, m, key("esc"))
	if m.mode != modeInventory || m.inventory.focus != focusFields {
		t.Fatalf("esc from hosts left mode %v focus %v, want details fields for production",
			m.mode, m.inventory.focus)
	}

	m = step(t, m, key("esc"))
	if m.mode != modeList {
		t.Fatalf("esc from details left mode %v, want the list", m.mode)
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
	if !strings.Contains(got, "scroll") {
		t.Errorf("details legend does not mention scrolling: %q", got)
	}
	if !strings.Contains(got, "hosts") {
		t.Errorf("details legend does not mention hosts: %q", got)
	}
}
