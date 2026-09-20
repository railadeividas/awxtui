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

// "h" inside the details view is the only way left to reach an inventory's
// hosts, now that enter opens details instead.
func TestInventoryDetailsHKeyOpensHosts(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	m = step(t, m, key("h"))

	if m.mode != modeMembers {
		t.Fatalf("h in inventory details left mode %v, want modeMembers", m.mode)
	}
	if m.members.invName != "production" || len(m.members.rows) == 0 {
		t.Fatalf("h did not open production's hosts: title %q, rows %d", m.members.invName, len(m.members.rows))
	}
	if m.inventory.inventory.ID != 0 {
		t.Errorf("h left inventory detail state behind: %+v", m.inventory)
	}
}

// While the hosts request is still on the wire, the view must show an empty,
// spinning hosts table rather than lingering on a half-cleared details modal.
func TestInventoryHKeyShowsHostsLoadingImmediately(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))

	busy := probe(t, m, key("h"))
	if busy.mode != modeMembers {
		t.Fatalf("h left mode %v before the response landed, want modeMembers", busy.mode)
	}
	if len(busy.members.rows) != 0 || !busy.members.loading {
		t.Fatalf("expected an empty, loading hosts view, got %d rows loading=%v", len(busy.members.rows), busy.members.loading)
	}
	view := stripANSI(busy.View())
	if !strings.Contains(view, "fetching hosts") {
		t.Errorf("hosts view does not show a loading spinner while fetching:\n%s", view)
	}

	settled := step(t, m, key("h"))
	if settled.members.loading || len(settled.members.rows) == 0 {
		t.Errorf("hosts never settled: loading=%v rows=%d", settled.members.loading, len(settled.members.rows))
	}
}

// esc from the hosts view returns to the inventory's details, since that is
// where hosts are reached from — not all the way out to the list.
func TestHostsEscReturnsToInventoryDetails(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	m = step(t, m, key("h"))
	if m.mode != modeMembers {
		t.Fatalf("h left mode %v, want modeMembers", m.mode)
	}

	m = step(t, m, key("esc"))
	if m.mode != modeInventory || m.inventory.inventory.Name != "production" {
		t.Fatalf("esc from hosts left mode %v inventory %+v, want details for production",
			m.mode, m.inventory.inventory)
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
