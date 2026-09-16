package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

func viewLines(m Model) []string { return strings.Split(m.View(), "\n") }

func lastLine(m Model) string {
	lines := viewLines(m)
	return stripANSI(lines[len(lines)-1])
}

// The legend is how you leave a screen, so a notice must sit beside it rather
// than take its place.
func TestFooterKeepsLegendBesideANotice(t *testing.T) {
	m := openOutputView(t, outputMock(t))
	// Jump past both failures so the view reports there is nothing after.
	for i := 0; i < 3; i++ {
		m = step(t, m, key("]"))
	}
	if m.notice == "" {
		t.Fatalf("expected a notice from jumping past the last failure (err %v)", m.err)
	}
	foot := lastLine(m)
	for _, want := range []string{"esc back", "]/[", "no later failure"} {
		if !strings.Contains(foot, want) {
			t.Errorf("status bar %q is missing %q", foot, want)
		}
	}
}

// An error is shown next to the legend too, cut to fit with the pointer to the
// detail view kept — that pointer is the only way to read the rest.
func TestFooterKeepsLegendBesideAnError(t *testing.T) {
	long := strings.Repeat("the upstream returned a long html error page. ", 12)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/me/" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": 1, "results": []any{map[string]any{"username": "admin"}}})
			return
		}
		http.Error(w, long, http.StatusBadRequest)
	}))
	defer srv.Close()

	m := New(awx.New(srv.URL, "t", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, m.connect())
	if m.err == nil {
		t.Fatal("expected the failing list request to surface an error")
	}
	foot := lastLine(m)
	for _, want := range []string{"q quit", "✗ ", "e for details"} {
		if !strings.Contains(foot, want) {
			t.Errorf("status bar %q is missing %q", foot, want)
		}
	}
	if w := lineWidth(foot); w > 120 {
		t.Errorf("status bar is %d cols wide on a 120-col terminal", w)
	}
}

// A terminal too narrow to hold both gives the line to the message: half an
// error says nothing, while the keys are also listed under ?.
func TestFooterGivesANarrowLineToTheError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/me/" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": 1, "results": []any{map[string]any{"username": "admin"}}})
			return
		}
		http.Error(w, "inventory sync is broken", http.StatusBadRequest)
	}))
	defer srv.Close()

	m := New(awx.New(srv.URL, "t", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 60, Height: 24})
	m = step(t, m, m.connect())
	foot := lastLine(m)
	if !strings.Contains(foot, "✗ ") || !strings.Contains(foot, "e for details") {
		t.Errorf("a narrow status bar must still show the error and its pointer: %q", foot)
	}
	if w := lineWidth(foot); w > 60 {
		t.Errorf("status bar is %d cols wide on a 60-col terminal", w)
	}
}
