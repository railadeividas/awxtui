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

// A job can emit far more output than is worth holding; keep the tail.
func TestHugeOutputIsTrimmedToTheTail(t *testing.T) {
	var body strings.Builder
	for i := 0; body.Len() < 3*maxOutputBytes/2; i++ {
		fmt.Fprintf(&body, "ok: [host-%06d] some reasonably long line of play output\n", i)
	}
	full := body.String()
	lastLine := strings.TrimSpace(full[strings.LastIndex(strings.TrimRight(full, "\n"), "\n"):])

	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("/api/v2/me/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 1, "results": []any{map[string]any{"username": "admin"}}})
	})
	mux.HandleFunc("/api/v2/job_templates/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 0, "results": []any{}})
	})
	job := map[string]any{"id": 5, "name": "verbose", "status": "successful", "elapsed": 1.0}
	jobList := func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 1, "results": []any{job}})
	}
	mux.HandleFunc("/api/v2/jobs/", jobList)
	mux.HandleFunc("/api/v2/unified_jobs/", jobList)
	mux.HandleFunc("/api/v2/jobs/5/", func(w http.ResponseWriter, r *http.Request) { write(w, job) })
	mux.HandleFunc("/api/v2/jobs/5/stdout/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, full)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	m := New(awx.New(srv.URL, "t", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = step(t, m, m.connect())
	m = step(t, m, key("2"))
	m = step(t, m, key("enter"))

	if len(m.outputText) > maxOutputBytes+len(outputTrimmedNotice) {
		t.Errorf("kept %d bytes, cap is %d", len(m.outputText), maxOutputBytes)
	}
	if !strings.HasPrefix(m.outputText, outputTrimmedNotice) {
		t.Error("the user should be told the output was trimmed")
	}
	// The end of the run is what matters, so it must survive.
	if !strings.Contains(m.outputText, lastLine) {
		t.Error("the tail of the output was trimmed away")
	}
	// Trimming must cut on a line boundary, not mid-line.
	first := strings.SplitN(strings.TrimPrefix(m.outputText, outputTrimmedNotice), "\n", 2)[0]
	if first != "" && !strings.HasPrefix(first, "ok: [host-") {
		t.Errorf("trimmed mid-line: %q", first)
	}
}

func TestTrimOutputLeavesShortOutputAlone(t *testing.T) {
	const short = "PLAY [web]\nok: [web-01]\n"
	if got := trimOutput(short); got != short {
		t.Errorf("short output was altered: %q", got)
	}
}

// The status bar can only show one line; the full text must be reachable.
func TestErrorDetailView(t *testing.T) {
	// A 400 is not retried, so the error surfaces at once; retry behaviour
	// itself is covered in internal/awx.
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
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = step(t, m, m.connect())
	if m.err == nil {
		t.Fatal("expected the failing list request to surface an error")
	}

	// The status bar advertises where the rest of it is.
	if !strings.Contains(m.View(), "e for details") {
		t.Error("status bar should point at the detail view")
	}

	m = step(t, m, key("e"))
	if m.mode != modeError {
		t.Fatalf("e should open the error view, got mode %v", m.mode)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "400") {
		t.Error("the error view should show the status")
	}
	// The detail view has room for text the status line had to cut.
	if len(stripANSI(m.errorModal())) <= 120 {
		t.Errorf("error view looks truncated: %q", stripANSI(m.errorModal()))
	}
	if strings.Contains(stripANSI(m.errorModal()), "message truncated") {
		t.Error("this message fits; it should not be marked truncated")
	}
	// On a short terminal it cannot fit, and that must be said rather than
	// silently cut off.
	small := step(t, m, tea.WindowSizeMsg{Width: 100, Height: 14})
	if !strings.Contains(stripANSI(small.errorModal()), "message truncated") {
		t.Error("a message too long for the view should be marked as truncated")
	}
	for i, line := range strings.Split(small.View(), "\n") {
		if w := lineWidth(line); w > 100 {
			t.Errorf("error view line %d is %d cols wide on a small terminal", i, w)
		}
	}
	if lines := strings.Count(small.View(), "\n") + 1; lines > 14 {
		t.Errorf("error view is %d lines on a 14-line terminal", lines)
	}
	// The view must stay inside the terminal.
	for i, line := range strings.Split(m.View(), "\n") {
		if w := lineWidth(line); w > 100 {
			t.Errorf("error view line %d is %d cols wide", i, w)
		}
	}
	show(t, "error detail", m.View())

	m = step(t, m, key("x")) // any key closes
	if m.mode != modeList {
		t.Errorf("any key should close the error view, got %v", m.mode)
	}
}

// Pressing e with nothing wrong should do nothing at all.
func TestErrorViewNeedsAnError(t *testing.T) {
	m := openOutputView(t, outputMock(t))
	m = step(t, m, key("esc"))
	m = step(t, m, key("e"))
	if m.mode == modeError {
		t.Error("e opened an empty error view")
	}
}
