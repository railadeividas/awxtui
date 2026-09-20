package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// openAdHocForm connects, selects an inventory by name, opens its details and
// opens the ad hoc launch form from there — 'a' is a detail-view action, not
// a list one, since it needs one inventory already chosen.
func openAdHocForm(t *testing.T, srv *mock, inventory string) Model {
	t.Helper()
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, m.connect())
	m = step(t, m, key("3")) // Inventories tab
	for i, inv := range m.inventories {
		if inv.Name == inventory {
			m.cursor[tabInventories] = i
		}
	}
	m = step(t, m, key("enter"))
	if m.mode != modeInventory {
		t.Fatalf("expected inventory details, got mode %v (err %v)", m.mode, m.err)
	}
	m = step(t, m, key("a"))
	if m.mode != modeLaunch {
		t.Fatalf("expected the ad hoc launch form, got mode %v (err %v)", m.mode, m.err)
	}
	return m
}

// Pressing 'a' starts a request for the credential catalogue that takes a
// moment to land; until it does, the details view must keep showing the real
// inventory rather than snap to a cleared, zero-value one (bug: it briefly
// rendered "Inventory #0", no organization, 0 hosts — because 'a' cleared
// m.inventory immediately but only adHocFormMsg actually leaves this view).
func TestAdHocKeyDoesNotClearInventoryWhileLoading(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, m.connect())
	m = step(t, m, key("3"))
	for i, inv := range m.inventories {
		if inv.Name == "production" {
			m.cursor[tabInventories] = i
		}
	}
	m = step(t, m, key("enter"))
	if m.mode != modeInventory {
		t.Fatalf("expected inventory details, got mode %v (err %v)", m.mode, m.err)
	}

	// A single, undrained Update call: the credential request is still in
	// flight, the way it is for the whole span the status bar reads
	// "reading credentials…" in the real app.
	next, _ := m.Update(key("a"))
	m = next.(Model)

	if m.mode != modeInventory {
		t.Fatalf("expected to still be on the details view mid-request, got mode %v", m.mode)
	}
	if m.inventory.inventory.ID == 0 || m.inventory.inventory.Name == "" {
		t.Fatalf("inventory was cleared while its own request was in flight: %+v", m.inventory.inventory)
	}
}

// Launching an ad hoc command must offer the fixed field set AWX's own
// ad_hoc_commands POST accepts, send inventory as an int the user never
// typed, and open the run's output once AWX accepts it.
func TestAdHocLaunch(t *testing.T) {
	srv := mockAWX(t)
	m := openAdHocForm(t, srv, "production")

	want := []string{"credential", "module_name", "module_args", "limit", "verbosity", "become_enabled"}
	if got := formKeys(&m); !equalStrings(got, want) {
		t.Fatalf("form fields = %v, want %v", got, want)
	}
	show(t, "ad hoc launch form", m.View())

	// Only machine credentials can authenticate to a host; AWX's own ad hoc
	// launch filters the same way, server-side (credential_type__kind=ssh),
	// so galaxy.token — a real credential on the instance, just not one an
	// ad hoc command could ever use — must not be offered here.
	credFl := m.form.fields[0]
	wantCreds := []string{"web_prod.ssh (Machine)", "db_prod.ssh (Machine)"}
	if !equalStrings(credFl.choices, wantCreds) {
		t.Fatalf("credential choices = %v, want %v", credFl.choices, wantCreds)
	}

	m = focusField(t, m, "credential")
	m = step(t, m, key("enter"))
	if m.mode != modePick {
		t.Fatalf("enter on credential should open the picker, got mode %v", m.mode)
	}
	m = step(t, m, key("down")) // web_prod.ssh -> db_prod.ssh
	m = step(t, m, key("enter"))
	if m.mode != modeLaunch {
		t.Fatalf("enter in the picker should close it, got mode %v", m.mode)
	}

	m = focusField(t, m, "module_name")
	m = step(t, m, key("enter"))
	if m.mode != modePick {
		t.Fatalf("enter on module should open the picker, got mode %v", m.mode)
	}
	m = step(t, m, key("down")) // command -> shell
	m = step(t, m, key("esc"))  // esc also just closes, keeping the highlight
	if m.mode != modeLaunch {
		t.Fatalf("esc in the picker should close it, got mode %v", m.mode)
	}
	m = focusField(t, m, "module_args")
	m = typeText(t, m, "systemctl restart nginx")
	m = focusField(t, m, "limit")
	m = typeText(t, m, "web-01")
	m = focusField(t, m, "become_enabled")
	m = step(t, m, key("right")) // off -> on
	m = step(t, m, key("ctrl+s"))

	if m.err != nil {
		t.Fatalf("launch errored: %v", m.err)
	}
	if m.mode != modeOutput {
		t.Fatalf("expected output view after launch, got mode %v", m.mode)
	}

	got := srv.lastLaunch()
	if got["module_name"] != "shell" {
		t.Errorf("module_name = %v, want shell", got["module_name"])
	}
	if got["module_args"] != "systemctl restart nginx" {
		t.Errorf("module_args = %v", got["module_args"])
	}
	if got["limit"] != "web-01" {
		t.Errorf("limit = %v", got["limit"])
	}
	if got["become_enabled"] != true {
		t.Errorf("become_enabled = %v, want true", got["become_enabled"])
	}
	// inventory was never a field the user typed into: the tab selection
	// alone must be what AWX receives.
	if inv, ok := got["inventory"].(float64); !ok || inv != 3 {
		t.Errorf("inventory = %v, want 3", got["inventory"])
	}
	if cred, ok := got["credential"].(float64); !ok || cred != 40 {
		t.Errorf("credential = %v, want 40 (db_prod.ssh)", got["credential"])
	}
}

// esc from the ad hoc form must return to the inventory's details, since that
// is where ad hoc is reached from — not all the way out to the inventories
// list, the same as the members view.
func TestAdHocEscReturnsToInventoryDetails(t *testing.T) {
	srv := mockAWX(t)
	m := openAdHocForm(t, srv, "production")

	m = step(t, m, key("esc"))
	if m.mode != modeInventory || m.inventory.inventory.Name != "production" {
		t.Fatalf("esc from ad hoc left mode %v inventory %+v, want details for production",
			m.mode, m.inventory.inventory)
	}

	m = step(t, m, key("esc"))
	if m.mode != modeList {
		t.Fatalf("esc from details left mode %v, want the list", m.mode)
	}
}

// A read-only client must refuse to submit the ad hoc form, the same way it
// refuses a template launch.
func TestAdHocLaunchReadOnly(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false).ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, m.connect())
	m = step(t, m, key("3"))
	m = step(t, m, key("enter"))
	if m.mode != modeInventory {
		t.Fatalf("expected inventory details, got mode %v (err %v)", m.mode, m.err)
	}
	m = step(t, m, key("a"))
	if m.mode != modeLaunch {
		t.Fatalf("expected the ad hoc launch form, got mode %v (err %v)", m.mode, m.err)
	}
	m = step(t, m, key("ctrl+s"))
	if m.mode != modeLaunch || m.form.problem == "" {
		t.Fatalf("expected a read-only refusal, got mode %v problem %q", m.mode, m.form.problem)
	}
	if srv.launchCount() != 0 {
		t.Fatalf("read-only client still launched: %v", srv.lastLaunch())
	}
}
