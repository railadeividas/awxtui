package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// jobDetailMock serves three runs on the jobs tab: a finished playbook job
// with a full launch record, a still-running one with nothing set yet, and a
// project update, which has no playbook-only fields at all.
func jobDetailMock(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/api/v2/me/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 1, "results": []any{map[string]any{"username": "admin"}}})
	})
	mux.HandleFunc("/api/v2/job_templates/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 0, "results": []any{}})
	})
	mux.HandleFunc("/api/v2/unified_jobs/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 4, "results": []any{
			map[string]any{"id": 42, "type": "job", "name": "Deploy web app", "status": "successful", "elapsed": 96.2, "job_type": "run"},
			map[string]any{"id": 43, "type": "job", "name": "Deploy web app", "status": "running", "elapsed": 12.4, "job_type": "run"},
			map[string]any{"id": 50, "type": "project_update", "name": "security", "status": "successful", "elapsed": 4.0},
			map[string]any{"id": 60, "type": "system_job", "name": "Cleanup Activity Stream", "status": "successful", "elapsed": 2.0},
		}})
	})

	var job42Requests int32
	extraVars := strings.Repeat("release: v1.4.2\nverbose_note: line\n", 20)
	mux.HandleFunc("/api/v2/jobs/42/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&job42Requests, 1)
		write(w, map[string]any{
			"id": 42, "type": "job", "name": "Deploy web app", "status": "successful", "elapsed": 96.2,
			"launch_type": "manual", "extra_vars": extraVars, "limit": "web",
			"job_tags": "deploy", "skip_tags": "slow",
			"summary_fields": map[string]any{
				"inventory":    map[string]any{"id": 1, "name": "all"},
				"job_template": map[string]any{"id": 8, "name": "Deploy web app"},
				"credentials": []any{
					map[string]any{"id": 5, "name": "prod ssh", "kind": "ssh"},
					map[string]any{"id": 6, "name": "vault", "kind": "vault"},
				},
			},
		})
	})
	mux.HandleFunc("/api/v2/jobs/43/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"id": 43, "type": "job", "name": "Deploy web app", "status": "running", "elapsed": 14.1})
	})
	mux.HandleFunc("/api/v2/jobs/42/stdout/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "PLAY [web] ****\nok: [web-01]\n")
	})
	mux.HandleFunc("/api/v2/project_updates/50/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 50, "type": "project_update", "name": "security", "status": "successful", "elapsed": 4.0,
			"launch_type": "manual",
			"summary_fields": map[string]any{
				// A project update's record has no inventory: it updates a
				// project, and names it under "project" instead.
				"project":    map[string]any{"id": 9, "name": "security"},
				"credential": map[string]any{"id": 4, "name": "infra.git", "kind": "scm"},
			},
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &job42Requests
}

func openJobsTab(t *testing.T, srv *httptest.Server) Model {
	t.Helper()
	m := New(awx.New(srv.URL, "t", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = step(t, m, m.connect())
	m = step(t, m, key("2"))
	return m
}

func TestJobDetailShowsLaunchInfo(t *testing.T) {
	srv, _ := jobDetailMock(t)
	m := openJobsTab(t, srv)
	m = step(t, m, key("d"))
	if m.mode != modeJob {
		t.Fatalf("expected modeJob, got %v (err %v)", m.mode, m.err)
	}
	body := stripANSI(m.View())
	for _, want := range []string{"v1.4.2", "web", "deploy", "slow", "prod ssh", "vault", "manual", "Deploy web app"} {
		if !strings.Contains(body, want) {
			t.Errorf("job detail view missing %q:\n%s", want, body)
		}
	}
}

func TestJobDetailEmptyFieldsShowDash(t *testing.T) {
	srv, _ := jobDetailMock(t)
	m := openJobsTab(t, srv)
	m = step(t, m, key("down")) // job 43: still running, nothing set
	m = step(t, m, key("d"))
	if m.mode != modeJob {
		t.Fatalf("expected modeJob, got %v (err %v)", m.mode, m.err)
	}
	if !strings.Contains(stripANSI(m.View()), "—") {
		t.Errorf("expected empty launch fields to render as a dash:\n%s", stripANSI(m.View()))
	}
}

func TestJobDetailHidesPlaybookFieldsForSync(t *testing.T) {
	srv, _ := jobDetailMock(t)
	m := openJobsTab(t, srv)
	m = step(t, m, key("down"))
	m = step(t, m, key("down")) // job 50: a project update
	m = step(t, m, key("d"))
	if m.mode != modeJob {
		t.Fatalf("expected modeJob, got %v (err %v)", m.mode, m.err)
	}
	body := stripANSI(m.View())
	for _, want := range []string{"manual", "project", "security", "infra.git"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q for a project update:\n%s", want, body)
		}
	}
	for _, absent := range []string{"job template", "job tags", "skip tags", "extra vars", "inventory"} {
		if strings.Contains(body, absent) {
			t.Errorf("did not expect %q for a project update:\n%s", absent, body)
		}
	}
}

func TestJobDetailFromOutputViewReturnsToJobsList(t *testing.T) {
	srv, _ := jobDetailMock(t)
	m := openJobsTab(t, srv)
	m = step(t, m, key("enter")) // opens output for job 42
	if m.mode != modeOutput {
		t.Fatalf("expected modeOutput, got %v (err %v)", m.mode, m.err)
	}
	m = step(t, m, key("d"))
	if m.mode != modeJob {
		t.Fatalf("expected modeJob, got %v (err %v)", m.mode, m.err)
	}
	m = step(t, m, key("esc"))
	if m.mode != modeList {
		t.Errorf("expected esc to go straight to the jobs list, got %v", m.mode)
	}
}

func TestJobDetailJumpsToOutput(t *testing.T) {
	srv, _ := jobDetailMock(t)
	m := openJobsTab(t, srv)
	m = step(t, m, key("d")) // details for job 42, opened straight from the list
	if m.mode != modeJob {
		t.Fatalf("expected modeJob, got %v (err %v)", m.mode, m.err)
	}
	m = step(t, m, key("enter"))
	if m.mode != modeOutput {
		t.Fatalf("expected enter to open output, got %v (err %v)", m.mode, m.err)
	}
	if m.outputJob.ID != 42 {
		t.Errorf("expected output for job 42, got %d", m.outputJob.ID)
	}
	if !strings.Contains(m.outputText, "PLAY") {
		t.Errorf("output not loaded: %q", m.outputText)
	}
}

// esc always goes straight to the jobs list from either the details view or
// the output view it can jump to, regardless of how the current view was
// reached — no back-history to retrace step by step.
func TestJobDetailAndOutputEscAlwaysReachJobsList(t *testing.T) {
	srv, _ := jobDetailMock(t)
	m := openJobsTab(t, srv)
	m = step(t, m, key("d"))     // details for job 42, opened straight from the list
	m = step(t, m, key("enter")) // -> output
	m = step(t, m, key("d"))     // -> details again
	m = step(t, m, key("enter")) // -> output again
	if m.mode != modeOutput {
		t.Fatalf("expected modeOutput, got %v (err %v)", m.mode, m.err)
	}
	m = step(t, m, key("esc"))
	if m.mode != modeList {
		t.Fatalf("expected esc from output to go straight to modeList, got %v", m.mode)
	}
	m = step(t, m, key("d"))
	if m.mode != modeJob {
		t.Fatalf("expected modeJob, got %v (err %v)", m.mode, m.err)
	}
	m = step(t, m, key("esc"))
	if m.mode != modeList {
		t.Errorf("expected esc from details to go straight to modeList, got %v", m.mode)
	}
}

func TestJobDetailScrolls(t *testing.T) {
	srv, _ := jobDetailMock(t)
	m := openJobsTab(t, srv)
	m = step(t, m, key("d"))
	if m.job.offset != 0 {
		t.Fatalf("expected to open at the top, got offset %d", m.job.offset)
	}
	m = step(t, m, key("G"))
	if m.job.offset == 0 {
		t.Fatalf("expected G to move past the top")
	}
	end := m.job.offset
	m = step(t, m, key("g"))
	if m.job.offset != 0 {
		t.Fatalf("expected g to return to the top, got %d", m.job.offset)
	}
	m = step(t, m, key("down"))
	if m.job.offset != 1 {
		t.Fatalf("expected down to move by one line, got %d", m.job.offset)
	}
	m.job.offset = end
	m = step(t, m, key("up"))
	if m.job.offset != end-1 {
		t.Fatalf("expected up to move back by one line, got %d", m.job.offset)
	}
}

func TestJobDetailUnsupportedKindErrors(t *testing.T) {
	srv, _ := jobDetailMock(t)
	m := openJobsTab(t, srv)
	m = step(t, m, key("down"))
	m = step(t, m, key("down"))
	m = step(t, m, key("down")) // job 60: a system job, no detail endpoint
	m = step(t, m, key("d"))
	if m.mode == modeJob {
		t.Fatalf("expected an unsupported-kind error, not the details view")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "system job") {
		t.Errorf("expected an error naming the unsupported kind, got %v", m.err)
	}
}

func TestJobDetailPin(t *testing.T) {
	srv, _ := jobDetailMock(t)
	m := openJobsTab(t, srv)
	m = step(t, m, key("d")) // details for job 42
	if got := m.jobPinLabel(); got != "pin" {
		t.Fatalf("pin label before pinning = %q", got)
	}
	m = step(t, m, key("p"))
	if !m.pinned(tabJobs, 42) {
		t.Errorf("p in job details did not pin #42 (notice %q, err %v)", m.notice, m.err)
	}
	if got := m.jobPinLabel(); got != "unpin" {
		t.Errorf("pin label after pinning = %q", got)
	}
	body := stripANSI(m.View())
	if !strings.Contains(body, "unpin") {
		t.Errorf("expected the modal footer to show unpin:\n%s", body)
	}
	m = step(t, m, key("p"))
	if m.pinned(tabJobs, 42) {
		t.Errorf("second p did not unpin #42")
	}
}

func TestJobDetailReload(t *testing.T) {
	srv, requests := jobDetailMock(t)
	m := openJobsTab(t, srv)
	m = step(t, m, key("d"))
	before := atomic.LoadInt32(requests)
	if before == 0 {
		t.Fatalf("expected opening details to fetch the job record")
	}
	m = step(t, m, key("r"))
	after := atomic.LoadInt32(requests)
	if after != before+1 {
		t.Errorf("expected reload to issue one more request, got %d -> %d", before, after)
	}
}
