package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// mock is a fake AWX instance that also records what was launched.
type mock struct {
	*httptest.Server
	mu       sync.Mutex
	launches []map[string]any
}

func (m *mock) lastLaunch() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.launches) == 0 {
		return nil
	}
	return m.launches[len(m.launches)-1]
}

func (m *mock) launchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.launches)
}

// mockAWX serves just enough of the v2 API to drive the UI.
func mockAWX(t *testing.T) *mock {
	t.Helper()
	mk := &mock{}
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
			"survey_enabled": true,
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
	mux.HandleFunc("/api/v2/jobs/42/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"id": 42, "name": "Deploy web app", "status": "successful", "elapsed": 96.2})
	})
	// Real AWX rejects format=ansi when the client asks for JSON, and serves
	// nothing useful while a job is still running.
	mux.HandleFunc("/api/v2/jobs/42/stdout/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			http.Error(w, "Not acceptable", http.StatusNotAcceptable)
			return
		}
		fmt.Fprint(w, "PLAY [web] ****\nok: [web-01]\narchived stdout\n")
	})
	mux.HandleFunc("/api/v2/jobs/43/stdout/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	// job_events, paginated by counter__gt like the real API.
	mux.HandleFunc("/api/v2/jobs/43/job_events/", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.Atoi(r.URL.Query().Get("counter__gt"))
		all := []struct {
			counter int
			stdout  string
		}{
			{1, "PLAY [web] ****"},
			{2, ""},
			{3, "TASK [Gathering Facts] ****"},
			{4, "ok: [web-01]"},
			{5, "TASK [Deploy] ****"},
			{6, "changed: [web-01]"},
		}
		var results []any
		for _, e := range all {
			if e.counter > after {
				results = append(results, map[string]any{
					"counter": e.counter, "stdout": e.stdout,
					"start_line": e.counter, "end_line": e.counter,
				})
			}
		}
		write(w, page(results...))
	})
	mux.HandleFunc("/api/v2/jobs/42/job_events/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page())
	})
	mux.HandleFunc("/api/v2/jobs/43/cancel/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	// Template 7 prompts for most ask_* values; template 8 has a survey.
	launchConfig := func(id int, survey bool) map[string]any {
		return map[string]any{
			"can_start_without_user_input": false,
			"passwords_needed_to_start":    []string{},
			"variables_needed_to_start":    []string{},
			"survey_enabled":               survey,
			"inventory_needed_to_start":    false,
			"ask_inventory_on_launch":      id == 7,
			"ask_limit_on_launch":          true,
			"ask_tags_on_launch":           id == 7,
			"ask_variables_on_launch":      true,
			"ask_job_type_on_launch":       id == 7,
			"ask_verbosity_on_launch":      id == 7,
			"ask_timeout_on_launch":        id == 7,
			"ask_diff_mode_on_launch":      id == 7,
			"defaults": map[string]any{
				"limit": "", "job_tags": "", "skip_tags": "", "scm_branch": "",
				"extra_vars": "{}", "job_type": "run", "verbosity": 0,
				"diff_mode": false, "timeout": 0, "forks": 5,
				"inventory": map[string]any{"id": 3, "name": "production"},
			},
		}
	}
	launch := func(id int, survey bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				write(w, launchConfig(id, survey))
				return
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"detail":"bad payload"}`, http.StatusBadRequest)
				return
			}
			mk.mu.Lock()
			mk.launches = append(mk.launches, body)
			mk.mu.Unlock()
			write(w, map[string]any{"id": 43, "name": "Deploy web app", "status": "pending"})
		}
	}
	mux.HandleFunc("/api/v2/job_templates/7/launch/", launch(7, false))
	mux.HandleFunc("/api/v2/job_templates/8/launch/", launch(8, true))
	mux.HandleFunc("/api/v2/job_templates/8/survey_spec/", func(w http.ResponseWriter, r *http.Request) {
		one, ten := 1, 10
		write(w, map[string]any{"name": "Deploy options", "spec": []any{
			map[string]any{"type": "text", "variable": "release", "question_name": "Release tag",
				"question_description": "git tag to deploy", "required": true, "default": ""},
			map[string]any{"type": "integer", "variable": "batch", "question_name": "Batch size",
				"required": false, "default": 2, "min": one, "max": ten},
			map[string]any{"type": "float", "variable": "ratio", "question_name": "Ratio",
				"required": false, "default": 0.5},
			map[string]any{"type": "multiplechoice", "variable": "env", "question_name": "Environment",
				"required": true, "default": "staging", "choices": "staging\nproduction"},
			map[string]any{"type": "multiselect", "variable": "steps", "question_name": "Steps",
				"required": false, "default": []string{"build"}, "choices": []string{"build", "migrate", "smoke"}},
			map[string]any{"type": "password", "variable": "api_key", "question_name": "API key",
				"required": false, "default": ""},
			map[string]any{"type": "textarea", "variable": "notes", "question_name": "Notes",
				"required": false, "default": ""},
		}})
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
	mk.Server = httptest.NewServer(mux)
	t.Cleanup(mk.Close)
	return mk
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

// drainTimeout bounds how long drain waits for one command. Against the mock
// server real work is local HTTP (sub-millisecond), so anything slower is a UI
// timer such as a poll tick, which the tests do not care about. Live tests talk
// to a real instance over the network and need a real timeout.
var drainTimeout = func() time.Duration {
	if os.Getenv("AWXTUI_LIVE") != "" {
		return 60 * time.Second
	}
	return 400 * time.Millisecond
}()

// drain executes a command (flattening batches) and collects its messages,
// skipping commands that are really just timers.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(drainTimeout):
		return nil
	}
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
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// typeText sends each rune of s as a key press.
func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = step(t, m, key(string(r)))
	}
	return m
}

// fieldByKey finds a form field, failing the test when it is missing.
func fieldByKey(t *testing.T, m *Model, key string) *formField {
	t.Helper()
	for i := range m.form.fields {
		if m.form.fields[i].key == key {
			return &m.form.fields[i]
		}
	}
	t.Fatalf("form has no %q field; has %v", key, formKeys(m))
	return nil
}

func formKeys(m *Model) []string {
	var out []string
	for i := range m.form.fields {
		out = append(out, m.form.fields[i].key)
	}
	return out
}

// focusField moves the form cursor onto a named field.
func focusField(t *testing.T, m Model, key string) Model {
	t.Helper()
	for i := range m.form.fields {
		if m.form.fields[i].key == key {
			for m.form.cursor < i {
				m = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
			}
			for m.form.cursor > i {
				m = step(t, m, tea.KeyMsg{Type: tea.KeyUp})
			}
			return m
		}
	}
	t.Fatalf("cannot focus %q; form has %v", key, formKeys(&m))
	return m
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
	if m.err != nil {
		t.Fatalf("launch failed: %v", m.err)
	}
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

// A running job has no /stdout/ content but does have job_events; this is the
// case that used to show a permanent "waiting for output…".
func TestRunningJobOutputComesFromEvents(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, m.connect())
	m = step(t, m, key("2")) // jobs tab, running job 43 is first
	m = step(t, m, key("enter"))

	if m.err != nil {
		t.Fatalf("opening output errored: %v", m.err)
	}
	if !m.outputJob.IsRunning() {
		t.Fatalf("expected to open the running job, got %q", m.outputJob.Status)
	}
	for _, want := range []string{"PLAY [web]", "Gathering Facts", "changed: [web-01]"} {
		if !strings.Contains(m.outputText, want) {
			t.Errorf("output missing %q; got:\n%s", want, m.outputText)
		}
	}
	if m.outputCounter != 6 {
		t.Errorf("expected to tail up to counter 6, got %d", m.outputCounter)
	}
	// A poll tick must not duplicate the lines already shown.
	before := m.outputText
	m = step(t, m, tickMsg(time.Now()))
	if m.outputText != before {
		t.Errorf("poll duplicated output:\nbefore:\n%s\nafter:\n%s", before, m.outputText)
	}
}

// Finished jobs whose events were pruned still fall back to /stdout/.
func TestFinishedJobFallsBackToStdout(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, m.connect())
	m = step(t, m, key("2"))
	m = step(t, m, key("down"))
	m = step(t, m, key("enter"))

	if m.outputJob.ID != 42 {
		t.Fatalf("expected job 42, got %d", m.outputJob.ID)
	}
	if !strings.Contains(m.outputText, "archived stdout") {
		t.Fatalf("expected stdout fallback, got %q (err %v)", m.outputText, m.err)
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
