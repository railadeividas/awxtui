package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// "g" in an inventory's details expands its groups inline, the same way "h"
// expands its hosts — AWX groups were entirely unrepresented in the UI
// before this, and neither one is a separate page to navigate to.
func TestInventoryDetailsGKeyExpandsGroups(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	m = step(t, m, key("g"))

	if m.mode != modeInventory || m.inventory.focus != focusGroups {
		t.Fatalf("g in inventory details left mode %v focus %v, want modeInventory/focusGroups", m.mode, m.inventory.focus)
	}
	if m.inventory.inventory.Name != "production" || len(m.inventory.groups.rows) == 0 {
		t.Fatalf("g did not expand production's groups: inventory %q, rows %d",
			m.inventory.inventory.Name, len(m.inventory.groups.rows))
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "web") || !strings.Contains(view, "db") {
		t.Errorf("groups view is missing the mock's group names:\n%s", view)
	}
	show(t, "groups expanded in inventory details", m.View())
}

// The details view should name a few hosts and groups inline, not just count
// them — the whole point being that a quick check does not always need to
// expand the full list.
func TestInventoryDetailsPreviewsNames(t *testing.T) {
	m, _ := openTab(t, "3")
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))

	view := stripANSI(m.View())
	for _, want := range []string{"web-01", "db-01", "web", "db"} {
		if !strings.Contains(view, want) {
			t.Errorf("inventory details preview is missing %q:\n%s", want, view)
		}
	}
}

// Selecting groups inline and confirming with enter must offer their names,
// ':'-joined, as the ad hoc form's limit default — the whole point being
// that a user no longer has to already know a group's name to type it blind.
func TestGroupSelectionPrefillsAdHocLimit(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, m.connect())
	m = step(t, m, key("3"))
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	m = step(t, m, key("g"))
	if m.mode != modeInventory || m.inventory.focus != focusGroups {
		t.Fatalf("expected groups expanded, got mode %v focus %v (err %v)", m.mode, m.inventory.focus, m.err)
	}
	m = step(t, m, key("a")) // select every loaded group
	m = step(t, m, key("enter"))

	if m.mode != modeInventory || m.inventory.focus != focusFields {
		t.Fatalf("enter after selecting should collapse back to the details fields, got mode %v focus %v", m.mode, m.inventory.focus)
	}
	if m.limitSel.inventoryID != 3 || len(m.limitSel.names) != 2 {
		t.Fatalf("limit selection = %+v, want both groups on inventory 3", m.limitSel)
	}

	m = step(t, m, key("a")) // now open the ad hoc form
	if m.mode != modeLaunch {
		t.Fatalf("expected the ad hoc launch form, got mode %v (err %v)", m.mode, m.err)
	}
	got := m.form.fields[3] // credential, module_name, module_args, limit
	if got.key != "limit" {
		t.Fatalf("form.fields[3] = %q, want limit", got.key)
	}
	if want := m.limitSel.limit(); got.value() != want {
		t.Errorf("limit default = %q, want %q", got.value(), want)
	}
}

// A template with its own non-empty default limit must keep it: a
// details-view selection only fills a blank limit, never overrides one AWX
// already set for the template.
func TestLimitSelectionNeverOverwritesTemplateDefault(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, m.connect())
	m = step(t, m, key("3"))
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	m = step(t, m, key("h"))
	m = step(t, m, key("a"))
	m = step(t, m, key("enter"))
	if m.limitSel.inventoryID != 3 {
		t.Fatalf("expected a limit selection on inventory 3, got %+v", m.limitSel)
	}

	m = step(t, m, key("esc")) // back to the list
	m = step(t, m, key("1"))   // templates tab
	for i, tpl := range m.templates {
		if tpl.Name == "Restart nginx" {
			m.cursor[tabTemplates] = i
		}
	}
	m = step(t, m, key("enter"))
	if m.mode != modeLaunch {
		t.Fatalf("expected the launch form, got mode %v (err %v)", m.mode, m.err)
	}
	fl := m.form.focused()
	for i := range m.form.fields {
		if m.form.fields[i].key == "limit" {
			fl = &m.form.fields[i]
		}
	}
	if fl == nil || fl.key != "limit" {
		t.Fatalf("template has no limit field")
	}
	if fl.value() != "custom-limit" {
		t.Errorf("limit = %q, want the template's own default of custom-limit, unaffected by the pending selection", fl.value())
	}
}

// A selection made against one inventory must not leak into a launch against
// a different one.
func TestLimitSelectionScopedToItsInventory(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, m.connect())
	m = step(t, m, key("3"))
	m = rowAt(t, m, "production")
	m = step(t, m, key("enter"))
	m = step(t, m, key("h"))
	m = step(t, m, key("a"))
	m = step(t, m, key("enter"))
	if m.limitSel.inventoryID != 3 {
		t.Fatalf("expected a limit selection on inventory 3, got %+v", m.limitSel)
	}

	m = step(t, m, key("esc")) // back to the list
	m = rowAt(t, m, "handmade")
	m = step(t, m, key("enter"))
	m = step(t, m, key("a")) // ad hoc for a different inventory
	if m.mode != modeLaunch {
		t.Fatalf("expected the ad hoc launch form, got mode %v (err %v)", m.mode, m.err)
	}
	for _, fl := range m.form.fields {
		if fl.key == "limit" && fl.value() != "" {
			t.Errorf("limit = %q, want empty: the selection was made against a different inventory", fl.value())
		}
	}
}
