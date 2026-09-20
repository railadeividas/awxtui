package ui

import (
	"fmt"
	"strings"
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

// A limit built from many selected hosts is easily wider than the form. The
// bug this guards: newAdHocForm prefills the field's textinput via SetValue
// *before* Width is set (see newInput), so bubbles computes its horizontal
// scroll window as if the field were unbounded — the value then renders
// wrapped across dozens of lines instead of scrolling within one, pushing
// every field below it off screen. Typing the value in character by
// character wouldn't reproduce this: each keystroke re-triggers bubbles'
// own recompute with Width already set correctly by then. Only a value
// that arrives prefilled — exactly how a members-view selection reaches
// this field — hits the bug, so that's what this test drives.
func TestLongLimitFromSelectionStaysOneLine(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, m.connect())
	m = step(t, m, key("3"))
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))

	// production's mock only has 2 hosts; inject enough to force real
	// wrapping if the field is left unbounded.
	hosts := make([]awx.Host, 60)
	for i := range hosts {
		hosts[i] = awx.Host{ID: 5000 + i, Name: fmt.Sprintf("host-%02d.example.com", i), Enabled: true}
	}
	rows, names := memberRows(memberHosts, hosts, nil)
	m.inventory.hosts.rows, m.inventory.hosts.names = rows, names
	m.inventory.hosts.count = len(hosts)

	m = step(t, m, key("h")) // jump to the first host
	for range hosts {
		m = step(t, m, key(" "))
		m = step(t, m, key("down"))
	}
	if len(m.limitSel.names) != len(hosts) {
		t.Fatalf("expected all %d hosts selected, got %d", len(hosts), len(m.limitSel.names))
	}

	m = step(t, m, key("a")) // open ad hoc — limit is prefilled from the selection
	if m.mode != modeLaunch {
		t.Fatalf("expected the ad hoc launch form, got mode %v (err %v)", m.mode, m.err)
	}

	// Limit and Verbosity must land on adjacent lines: a wrapped Limit
	// leaves the word "Limit" only on its first physical line, so counting
	// lines that mention it can't tell a one-line field from a many-line
	// one — but a wrapped Limit pushes Verbosity dozens of lines further
	// down, which this catches directly.
	linesApart := func(m Model) int {
		t.Helper()
		lines := strings.Split(stripANSI(m.View()), "\n")
		limitAt, verbosityAt := -1, -1
		for i, l := range lines {
			if strings.Contains(l, "Limit") {
				limitAt = i
			}
			if strings.Contains(l, "Verbosity") {
				verbosityAt = i
			}
		}
		if limitAt == -1 || verbosityAt == -1 {
			t.Fatalf("Limit or Verbosity missing from the view entirely:\n%s", strings.Join(lines, "\n"))
		}
		return verbosityAt - limitAt
	}

	if got := linesApart(m); got != 1 {
		t.Fatalf("Limit and Verbosity are %d lines apart unfocused, want 1 (Limit must not wrap):\n%s", got, stripANSI(m.View()))
	}

	m = focusField(t, m, "limit")
	if got := linesApart(m); got != 1 {
		t.Fatalf("Limit and Verbosity are %d lines apart while focused, want 1 (Limit must not wrap):\n%s", got, stripANSI(m.View()))
	}
}
