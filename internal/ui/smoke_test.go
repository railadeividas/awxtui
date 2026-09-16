package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// mockAWX serves just enough of the v2 API to drive the UI.
func mockAWX(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now().UTC()
	page := func(results ...any) map[string]any {
		return map[string]any{"count": len(results), "results": results}
	}
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/api/v2/me/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, `{"detail":"bad token"}`, http.StatusUnauthorized)
			return
		}
		write(w, page(map[string]any{"username": "admin"}))
	})
	mux.HandleFunc("/api/v2/job_templates/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(map[string]any{
			"id": 7, "name": "Deploy web app", "job_type": "run", "playbook": "deploy.yml",
			"last_job_run": now.Add(-90 * time.Minute),
			"summary_fields": map[string]any{
				"project":   map[string]any{"name": "infra"},
				"inventory": map[string]any{"name": "production"},
				"last_job":  map[string]any{"id": 42, "status": "successful"},
			},
		}, map[string]any{
			"id": 8, "name": "Rotate certificates", "job_type": "run", "playbook": "certs.yml",
			"summary_fields": map[string]any{
				"project":   map[string]any{"name": "security"},
				"inventory": map[string]any{"name": "all"},
				"last_job":  map[string]any{"id": 41, "status": "failed"},
			},
		}))
	})
	mux.HandleFunc("/api/v2/jobs/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(map[string]any{
			"id": 43, "name": "Deploy web app", "status": "running", "elapsed": 12.4,
			"started": now.Add(-12 * time.Second), "job_type": "run",
			"summary_fields": map[string]any{"created_by": map[string]any{"username": "admin"}},
		}, map[string]any{
			"id": 42, "name": "Deploy web app", "status": "successful", "elapsed": 96.2,
			"started": now.Add(-90 * time.Minute), "job_type": "run",
			"summary_fields": map[string]any{"created_by": map[string]any{"username": "admin"}},
		}))
	})
	mux.HandleFunc("/api/v2/jobs/43/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"id": 43, "name": "Deploy web app", "status": "running", "elapsed": 14.1})
	})
	mux.HandleFunc("/api/v2/jobs/43/stdout/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "PLAY [web] ****\n\nTASK [Gathering Facts] ****\nok: [web-01]\n")
	})
	mux.HandleFunc("/api/v2/jobs/43/cancel/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/api/v2/job_templates/7/launch/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		write(w, map[string]any{"id": 43, "name": "Deploy web app", "status": "pending"})
	})
	mux.HandleFunc("/api/v2/inventories/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(map[string]any{
			"id": 3, "name": "production", "total_hosts": 12, "total_groups": 4,
			"hosts_with_active_failures": 1,
			"summary_fields":             map[string]any{"organization": map[string]any{"name": "Default"}},
		}))
	})
	mux.HandleFunc("/api/v2/inventories/3/hosts/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(
			map[string]any{"id": 11, "name": "web-01", "enabled": true, "description": "frontend"},
			map[string]any{"id": 12, "name": "db-01", "enabled": true, "has_active_failures": true},
		))
	})
	mux.HandleFunc("/api/v2/projects/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(map[string]any{
			"id": 5, "name": "infra", "scm_type": "git", "scm_branch": "main",
			"status": "successful", "last_updated": now.Add(-3 * time.Hour),
		}))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// step applies a message and drains the returned command synchronously.
func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	return settle(t, m, msg, 0)
}

// settle applies msg and keeps applying the messages its commands produce,
// mimicking the Bubble Tea runtime closely enough to assert on end state.
func settle(t *testing.T, m Model, msg tea.Msg, depth int) Model {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(Model)
	if depth > 6 {
		return m
	}
	for _, out := range drain(cmd) {
		switch out.(type) {
		case tickMsg, tea.QuitMsg, nil:
			continue
		}
		m = settle(t, m, out, depth+1)
	}
	return m
}

// drain executes a command (flattening batches) and collects its messages.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drain(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestFlows(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, m.connect())

	if m.user != "admin" {
		t.Fatalf("expected connected user admin, got %q (err: %v)", m.user, m.err)
	}
	if got := len(m.rows[tabTemplates]); got != 2 {
		t.Fatalf("expected 2 templates, got %d (err: %v)", got, m.err)
	}
	show(t, "templates", m.View())

	// filter down to one template
	m = step(t, m, key("/"))
	for _, r := range "certs" {
		m = step(t, m, key(string(r)))
	}
	if got := len(m.visible(tabTemplates)); got != 0 {
		t.Fatalf("filter 'certs' should not match template names, got %d", got)
	}
	m = step(t, m, key("esc"))
	if m.filters[tabTemplates] != "" {
		t.Fatalf("esc should clear the filter, got %q", m.filters[tabTemplates])
	}

	// launch the selected template -> lands in the output view
	m = step(t, m, key("enter"))
	if m.mode != modeLaunch {
		t.Fatalf("enter on a template should open the launch modal, got mode %v", m.mode)
	}
	show(t, "launch modal", m.View())
	m = step(t, m, key("enter"))
	if m.mode != modeOutput || m.outputJob.ID != 43 {
		t.Fatalf("launch should open output for job 43, got mode %v job %d (err %v)", m.mode, m.outputJob.ID, m.err)
	}
	if !strings.Contains(m.outputText, "Gathering Facts") {
		t.Fatalf("expected stdout to be fetched, got %q", m.outputText)
	}
	show(t, "job output", m.View())

	// back to the list, then across every tab
	m = step(t, m, key("esc"))
	if m.mode != modeList {
		t.Fatalf("esc should return to the list, got %v", m.mode)
	}
	for _, tabKey := range []string{"2", "3", "4"} {
		m = step(t, m, key(tabKey))
		if m.err != nil {
			t.Fatalf("tab %s failed to load: %v", tabKey, m.err)
		}
		show(t, "tab "+tabKey, m.View())
	}

	// inventory drill-down into hosts
	m = step(t, m, key("3"))
	m = step(t, m, key("enter"))
	if m.mode != modeHosts || len(m.hostRows) != 2 {
		t.Fatalf("expected 2 hosts in drill-down, got mode %v rows %d (err %v)", m.mode, len(m.hostRows), m.err)
	}
	show(t, "hosts", m.View())

	// jobs tab: cancel a running job
	m = step(t, m, key("esc"))
	m = step(t, m, key("2"))
	m = step(t, m, key("c"))
	if !strings.Contains(m.notice, "cancel requested") {
		t.Fatalf("expected cancel notice, got %q (err %v)", m.notice, m.err)
	}
}

func TestBadTokenSurfacesError(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "nope", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = step(t, m, m.connect())
	if m.err == nil || !strings.Contains(m.err.Error(), "401") {
		t.Fatalf("expected a 401 error to surface, got %v", m.err)
	}
	show(t, "auth error", m.View())
}

func TestEveryViewRendersWithinTerminalBounds(t *testing.T) {
	srv := mockAWX(t)
	for _, size := range [][2]int{{80, 24}, {120, 40}, {200, 50}} {
		m := New(awx.New(srv.URL, "test-token", false))
		m = step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = step(t, m, m.connect())
		for _, k := range []string{"1", "2", "3", "4", "?"} {
			m = step(t, m, key(k))
			out := m.View()
			for i, line := range strings.Split(out, "\n") {
				if w := lineWidth(line); w > size[0] {
					t.Errorf("%dx%d key %q: line %d is %d cols wide", size[0], size[1], k, i, w)
				}
			}
			if lines := strings.Count(out, "\n") + 1; lines > size[1] {
				t.Errorf("%dx%d key %q: view is %d lines, terminal has %d", size[0], size[1], k, lines, size[1])
			}
		}
	}
}

func lineWidth(s string) int { return len([]rune(stripANSI(s))) }

// show prints a rendered view when AWXTUI_SHOW is set, for eyeballing layout.
func show(t *testing.T, label, view string) {
	if os.Getenv("AWXTUI_SHOW") == "" {
		return
	}
	fmt.Printf("\n=== %s ===\n%s\n", label, view)
}
