package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// namedMock is an AWX whose template names identify which instance it is.
func namedMock(t *testing.T, label string, count int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("/api/v2/me/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 1, "results": []any{map[string]any{"username": label + "-user"}}})
	})
	mux.HandleFunc("/api/v2/job_templates/", func(w http.ResponseWriter, r *http.Request) {
		var items []any
		for i := 0; i < count; i++ {
			items = append(items, map[string]any{
				"id": i + 1, "name": fmt.Sprintf("%s-template-%d", label, i),
				"summary_fields": map[string]any{},
			})
		}
		write(w, map[string]any{"count": len(items), "next": nil, "results": items})
	})
	mux.HandleFunc("/api/v2/jobs/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 0, "next": nil, "results": []any{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// twoInstanceModel wires a model to two mock instances, starting on "prod".
func twoInstanceModel(t *testing.T, prod, staging *httptest.Server) Model {
	t.Helper()
	list := []InstanceInfo{
		{Name: "prod", URL: prod.URL, ReadOnly: true},
		{Name: "staging", URL: staging.URL},
	}
	connector := func(name string) (*awx.Client, error) {
		switch name {
		case "prod":
			return awx.New(prod.URL, "t", false).ReadOnly(), nil
		case "staging":
			return awx.New(staging.URL, "t", false), nil
		}
		return nil, fmt.Errorf("no instance named %q", name)
	}
	m := New(awx.New(prod.URL, "t", false).ReadOnly(),
		WithInstances(list, "prod"), WithConnector(connector))
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 26})
	return step(t, m, m.connect())
}

func TestInstanceSwitcherOpensAndSwitches(t *testing.T) {
	prod, staging := namedMock(t, "prod", 3), namedMock(t, "staging", 2)
	m := twoInstanceModel(t, prod, staging)

	if m.instance != "prod" || m.user != "prod-user" {
		t.Fatalf("started on %q as %q, want prod", m.instance, m.user)
	}
	if len(m.rows[tabTemplates]) != 3 {
		t.Fatalf("expected prod's 3 templates, got %d", len(m.rows[tabTemplates]))
	}

	m = step(t, m, key("i"))
	if m.mode != modeInstances {
		t.Fatalf("i should open the switcher, got mode %v (err %v)", m.mode, m.err)
	}
	show(t, "instance switcher", m.View())

	m = step(t, m, key("down"))
	m = step(t, m, key("enter"))

	if m.mode != modeList {
		t.Errorf("switching should return to the list, got %v", m.mode)
	}
	if m.instance != "staging" {
		t.Fatalf("connected instance = %q, want staging", m.instance)
	}
	if m.user != "staging-user" {
		t.Errorf("user = %q, want staging's", m.user)
	}
	if got := len(m.rows[tabTemplates]); got != 2 {
		t.Errorf("loaded %d templates, want staging's 2", got)
	}
	for _, r := range m.rows[tabTemplates] {
		if !strings.Contains(r.search, "staging-") {
			t.Errorf("row from the previous instance survived: %q", r.search)
		}
	}
	if !strings.Contains(stripANSI(m.View()), "staging") {
		t.Error("the header should name the connected instance")
	}
}

// Everything tied to the old instance must be cleared, not just the rows.
func TestSwitchingResetsSessionState(t *testing.T) {
	prod, staging := namedMock(t, "prod", 3), namedMock(t, "staging", 2)
	m := twoInstanceModel(t, prod, staging)

	// Leave some state behind: a filter, a moved cursor, another tab loaded.
	m = step(t, m, key("/"))
	m = typeText(t, m, "prod-template-2")
	m = step(t, m, key("enter"))
	m = step(t, m, key("2"))
	m = step(t, m, key("1"))
	m.cursor[tabTemplates] = 1

	m = step(t, m, key("i"))
	m = step(t, m, key("down"))
	m = step(t, m, key("enter"))

	if m.filters[tabTemplates] != "" || m.serverQuery[tabTemplates] != "" {
		t.Errorf("filter survived the switch: %q/%q",
			m.filters[tabTemplates], m.serverQuery[tabTemplates])
	}
	if m.cursor[tabTemplates] != 0 {
		t.Errorf("cursor = %d, want it reset", m.cursor[tabTemplates])
	}
	if m.loaded[tabJobs] {
		t.Error("the other tab should be reloaded from the new instance, not reused")
	}
	if m.client.IsReadOnly() {
		t.Error("staging is not read-only; the previous instance's client was kept")
	}
}

// A reply from the instance we just left must not be applied.
func TestStaleRepliesFromOldInstanceAreDropped(t *testing.T) {
	prod, staging := namedMock(t, "prod", 3), namedMock(t, "staging", 2)
	m := twoInstanceModel(t, prod, staging)
	oldGen := m.gen

	m = step(t, m, key("i"))
	m = step(t, m, key("down"))
	m = step(t, m, key("enter"))
	rows := len(m.rows[tabTemplates])

	// These were issued against prod and land after the switch.
	m = step(t, m, templatesMsg{
		pageMeta: pageMeta{count: 99, gen: oldGen, seq: m.searchSeq[tabTemplates]},
		items:    make([]awx.JobTemplate, 99),
	})
	if got := len(m.rows[tabTemplates]); got != rows {
		t.Errorf("stale list reply was applied: %d rows, want %d", got, rows)
	}
	m = step(t, m, errMsg{err: fmt.Errorf("prod exploded"), gen: oldGen})
	if m.err != nil {
		t.Errorf("stale error was shown: %v", m.err)
	}
	m = step(t, m, hostsMsg{pageMeta: pageMeta{gen: oldGen}, inventory: "old"})
	if m.mode == modeHosts {
		t.Error("a stale hosts reply hijacked the view")
	}
	m = step(t, m, connectedMsg{user: "prod-user", gen: oldGen})
	if m.user != "staging-user" {
		t.Errorf("stale connect reply changed the user to %q", m.user)
	}
}

// Failing to connect must report the problem and keep the current session.
func TestSwitchingToABrokenInstanceKeepsTheSession(t *testing.T) {
	prod := namedMock(t, "prod", 3)
	list := []InstanceInfo{{Name: "prod", URL: prod.URL}, {Name: "broken", URL: "https://nope"}}
	connector := func(name string) (*awx.Client, error) {
		if name == "prod" {
			return awx.New(prod.URL, "t", false), nil
		}
		return nil, fmt.Errorf("token_command for %q failed", name)
	}
	m := New(awx.New(prod.URL, "t", false), WithInstances(list, "prod"), WithConnector(connector))
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 26})
	m = step(t, m, m.connect())

	m = step(t, m, key("i"))
	m = step(t, m, key("down"))
	m = step(t, m, key("enter"))

	if m.instance != "prod" {
		t.Errorf("instance = %q, want to have stayed on prod", m.instance)
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "token_command") {
		t.Errorf("err = %v, want the connection failure reported", m.err)
	}
	if len(m.rows[tabTemplates]) != 3 {
		t.Errorf("the working session was discarded: %d rows", len(m.rows[tabTemplates]))
	}
}

// With nothing to switch to, say so instead of showing an empty picker.
func TestSwitcherNeedsMoreThanOneInstance(t *testing.T) {
	prod := namedMock(t, "prod", 1)
	m := New(awx.New(prod.URL, "t", false),
		WithInstances([]InstanceInfo{{Name: "prod", URL: prod.URL}}, "prod"),
		WithConnector(func(string) (*awx.Client, error) { return nil, nil }))
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 26})
	m = step(t, m, m.connect())

	m = step(t, m, key("i"))
	if m.mode == modeInstances {
		t.Error("the switcher opened with only one instance")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "one instance") {
		t.Errorf("err = %v, want an explanation", m.err)
	}
}

// Esc closes the switcher without changing anything.
func TestSwitcherCancels(t *testing.T) {
	prod, staging := namedMock(t, "prod", 3), namedMock(t, "staging", 2)
	m := twoInstanceModel(t, prod, staging)

	m = step(t, m, key("i"))
	m = step(t, m, key("down"))
	m = step(t, m, key("esc"))

	if m.mode != modeList {
		t.Errorf("esc should close the switcher, got %v", m.mode)
	}
	if m.instance != "prod" || len(m.rows[tabTemplates]) != 3 {
		t.Errorf("cancelling changed the session: %q with %d rows",
			m.instance, len(m.rows[tabTemplates]))
	}
}
