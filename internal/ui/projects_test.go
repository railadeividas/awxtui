package ui

import (
	"net/http"
	"net/http/httptest"
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
	if m.project.loading {
		t.Error("playbooks still marked as loading after the reply settled")
	}
	view := stripANSI(m.View())
	show(t, "project details", m.View())
	for _, want := range []string{
		"Project #5", "infra", "fleet playbooks",
		"git@github.com:example/infra.git", "main",
		"3f6e384b8694bac33b6215675fca396f2831c2e1",
		"infra.git", "_5__infra",
		"update on launch, clean, allow branch override",
		"60s", "playbooks (40)", "plays/site-01.yml",
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
	for _, want := range []string{"manual", "— never run", "none reported"} {
		if !strings.Contains(view, want) {
			t.Errorf("manual project view is missing %q", want)
		}
	}
	if strings.Contains(view, "refspec") {
		t.Error("manual project shows a refspec row it has no value for")
	}
}

// The fixture lists 40 playbooks, more than any of these terminals can show,
// so the window really has to move.
func TestProjectDetailsScrolls(t *testing.T) {
	m := openProjects(t, 100, 30)
	m = step(t, m, key("enter"))

	first := stripANSI(m.View())
	if !strings.Contains(first, "git@github.com:example/infra.git") {
		t.Fatal("the top of the details view does not show the SCM url")
	}
	if strings.Contains(first, "plays/site-40.yml") {
		t.Fatal("the whole list fits on screen; the fixture no longer tests scrolling")
	}

	m = step(t, m, key("G"))
	last := stripANSI(m.View())
	if !strings.Contains(last, "plays/site-40.yml") {
		t.Error("G did not scroll to the last playbook")
	}
	if m.project.offset == 0 {
		t.Error("G left the offset at the top")
	}

	m = step(t, m, key("g"))
	if m.project.offset != 0 {
		t.Errorf("g left the offset at %d, want 0", m.project.offset)
	}
	// Scrolling cannot run past either end.
	m = step(t, m, key("up"))
	if m.project.offset != 0 {
		t.Errorf("scrolling up from the top moved to %d", m.project.offset)
	}
	m = step(t, m, key("G"))
	bottom := m.project.offset
	m = step(t, m, key("pgdown"))
	if m.project.offset != bottom {
		t.Errorf("paging down from the bottom moved from %d to %d", bottom, m.project.offset)
	}
}

// A failing /playbooks/ call must surface as an error and still show every
// field the list already gave us, rather than an endless "loading…".
func TestProjectPlaybooksErrorSurfaces(t *testing.T) {
	projects := `{"count":1,"results":[{"id":5,"name":"infra","scm_type":"git",
		"scm_branch":"main","scm_url":"git@example.com:infra.git","status":"successful"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/me/":
			w.Write([]byte(`{"count":1,"results":[{"username":"admin"}]}`))
		case "/api/v2/projects/":
			w.Write([]byte(projects))
		case "/api/v2/projects/5/playbooks/":
			http.Error(w, `{"detail":"project has never updated"}`, http.StatusBadRequest)
		default:
			w.Write([]byte(`{"count":0,"results":[]}`))
		}
	}))
	t.Cleanup(srv.Close)

	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, m.connect())
	m = step(t, m, key("4"))
	m = step(t, m, key("enter"))

	if m.err == nil {
		t.Fatal("a failing playbooks call did not surface an error")
	}
	if m.project.loading {
		t.Error("the details view is still loading after the call failed")
	}
	view := stripANSI(m.View())
	if strings.Contains(view, "loading…") {
		t.Error("the details view still says loading after the call failed")
	}
	if !strings.Contains(view, "git@example.com:infra.git") {
		t.Error("the fields that did load are no longer shown")
	}
}

// Playbooks arriving for a project whose details view has moved on must not
// be rendered against the project now on screen.
func TestStalePlaybooksReplyIsDropped(t *testing.T) {
	m := openProjects(t, 120, 40)
	m = step(t, m, key("enter"))
	stale := playbooksMsg{gen: m.gen, projectID: m.project.project.ID + 1, names: []string{"wrong.yml"}}
	m = step(t, m, stale)
	if strings.Contains(stripANSI(m.View()), "wrong.yml") {
		t.Error("playbooks of another project were rendered")
	}

	older := playbooksMsg{gen: m.gen - 1, projectID: m.project.project.ID, names: []string{"previous.yml"}}
	m = step(t, m, older)
	if strings.Contains(stripANSI(m.View()), "previous.yml") {
		t.Error("playbooks from a previous instance generation were rendered")
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
