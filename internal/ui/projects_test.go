package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// openProjects connects to the mock and lands on the Projects tab.
func openProjects(t *testing.T, width, height int) Model {
	t.Helper()
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	m = step(t, m, m.connect())
	return step(t, m, key("4"))
}

func TestProjectEnterOpensDetails(t *testing.T) {
	m := openProjects(t, 120, 40)
	m = step(t, m, key("enter"))
	if m.mode != modeProject {
		t.Fatalf("enter on a project left mode %v, want modeProject", m.mode)
	}
	view := stripANSI(m.View())
	show(t, "project details", m.View())
	for _, want := range []string{
		"Project #5", "infra", "fleet playbooks",
		"git@github.com:example/infra.git", "main",
		"3f6e384b8694bac33b6215675fca396f2831c2e1",
		"infra.git", "_5__infra",
		"update on launch, clean, allow branch override",
		"60s",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("details view is missing %q", want)
		}
	}
	// esc goes back to the list, and leaves no state behind for the next one.
	m = step(t, m, key("esc"))
	if m.mode != modeList || m.project.project.ID != 0 {
		t.Errorf("esc left mode %v, project %+v", m.mode, m.project.project)
	}
}

// A manual project has no branch, revision or update flags. The view must say
// so rather than render blank rows that look like a loading failure.
func TestProjectDetailsOfManualProject(t *testing.T) {
	m := openProjects(t, 120, 40)
	m = step(t, m, key("down"))
	m = step(t, m, key("enter"))
	if got := m.project.project.Name; got != "legacy" {
		t.Fatalf("opened project %q, want legacy", got)
	}
	view := stripANSI(m.View())
	show(t, "manual project", m.View())
	for _, want := range []string{"manual", "— never run"} {
		if !strings.Contains(view, want) {
			t.Errorf("manual project view is missing %q", want)
		}
	}
	if strings.Contains(view, "refspec") {
		t.Error("manual project shows a refspec row it has no value for")
	}
}

// The status bar promises what enter does; on Projects that promise is now
// kept by a real view.
func TestProjectLegendMatchesBehaviour(t *testing.T) {
	m := openProjects(t, 120, 40)
	if got := stripANSI(m.statusView()); !strings.Contains(got, "details") {
		t.Errorf("projects legend does not offer details: %q", got)
	}
	m = step(t, m, key("enter"))
	if m.mode != modeProject {
		t.Fatalf("enter did not open the details view, mode %v", m.mode)
	}
	if got := stripANSI(m.statusView()); !strings.Contains(got, "scroll") {
		t.Errorf("details legend does not mention scrolling: %q", got)
	}
}
