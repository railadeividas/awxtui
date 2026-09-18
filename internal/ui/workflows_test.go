package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

func TestWorkflowsListRenders(t *testing.T) {
	m, _ := openTab(t, "6")
	view := stripANSI(m.View())
	show(t, "workflows list", m.View())
	for _, want := range []string{"Nightly pipeline", "Fleet rollout", "Default", "production"} {
		if !strings.Contains(view, want) {
			t.Errorf("workflows list is missing %q\n%s", want, view)
		}
	}
	// A workflow that has never run shows "never run", not AWX's literal
	// "never updated" status string, matching the templates tab.
	if !strings.Contains(view, "never run") {
		t.Errorf("workflows list should render an unrun workflow as never run\n%s", view)
	}
}

// TestWorkflowLaunchFormHasNoNodeFields checks that a workflow's launch form
// only ever offers what the workflow itself can prompt for: it must not grow
// a credential, execution environment, job type or verbosity field, which
// belong to the workflow's nodes and are never in its own launch config.
func TestWorkflowLaunchFormHasNoNodeFields(t *testing.T) {
	m, _ := openTab(t, "6")
	m = rowAt(t, m, "Nightly pipeline")
	m = step(t, m, key("enter"))

	if m.mode != modeLaunch {
		t.Fatalf("form did not open: mode %v err %v", m.mode, m.err)
	}
	if !m.form.isWorkflow {
		t.Fatal("form should be marked as a workflow launch")
	}
	keys := formKeys(&m)
	for _, unwanted := range []string{"credentials", "execution_environment", "job_type", "verbosity", "forks"} {
		for _, k := range keys {
			if k == unwanted {
				t.Errorf("workflow form has field %q, which belongs to a node, not the workflow", k)
			}
		}
	}
	var hasLimit bool
	for _, k := range keys {
		if k == "limit" {
			hasLimit = true
		}
	}
	if !hasLimit {
		t.Errorf("workflow form is missing limit, fields: %v", keys)
	}
}

// TestLaunchingAWorkflowOpensItsDetailsNotOutput checks that submitting a
// workflow's launch form lands on the launch-details view: a workflow job
// has no stdout of its own to open.
func TestLaunchingAWorkflowOpensItsDetailsNotOutput(t *testing.T) {
	m, srv := openTab(t, "6")
	m = rowAt(t, m, "Nightly pipeline")
	m = step(t, m, key("enter"))
	if m.mode != modeLaunch {
		t.Fatalf("form did not open: mode %v err %v", m.mode, m.err)
	}
	m = step(t, m, key("ctrl+s"))

	if m.err != nil {
		t.Fatalf("launching the workflow failed: %v", m.err)
	}
	if m.mode != modeJob {
		t.Fatalf("expected launch details after launching a workflow, got mode %v", m.mode)
	}
	if m.job.job.SummaryFields.WorkflowJobTemplate.Name != "Fleet rollout" {
		t.Errorf("job detail does not name the workflow template: %+v", m.job.job.SummaryFields)
	}
	if got := srv.postedTo(); len(got) != 1 || got[0] != "POST /api/v2/workflow_job_templates/20/launch/" {
		t.Fatalf("launch posted to %v, want one POST to workflow_job_templates/20/launch/", got)
	}
}

// TestWorkflowSurveyBecomesExtraVars checks a workflow's survey answer is
// sent the same way a job template's is: folded into extra_vars.
func TestWorkflowSurveyBecomesExtraVars(t *testing.T) {
	m, srv := openTab(t, "6")
	m = rowAt(t, m, "Fleet rollout")
	m = step(t, m, key("enter"))
	if m.mode != modeLaunch {
		t.Fatalf("form did not open: mode %v err %v", m.mode, m.err)
	}
	m = focusField(t, m, "wave")
	m = typeText(t, m, "canary")
	m = step(t, m, key("ctrl+s"))

	if m.err != nil {
		t.Fatalf("launching failed: %v", m.err)
	}
	got := srv.lastLaunch()
	vars, _ := got["extra_vars"].(map[string]any)
	if vars["wave"] != "canary" {
		t.Errorf("extra_vars = %v, want wave=canary", got["extra_vars"])
	}
}

// TestCancellingAWorkflowJobUsesItsOwnEndpoint checks that cancelling a
// running workflow job POSTs to /api/v2/workflow_jobs/, not /api/v2/jobs/ —
// the collection Job.Resource() would default to for any type it did not
// recognise.
func TestCancellingAWorkflowJobUsesItsOwnEndpoint(t *testing.T) {
	m, srv := openTab(t, "2")
	for i, r := range m.visible(tabJobs) {
		if r.id == 11 {
			m.cursor[tabJobs] = i
		}
	}
	j, ok := m.selectedJob()
	if !ok || !j.IsWorkflow() || !j.IsRunning() {
		t.Fatalf("expected the selected row to be a running workflow job, got %+v ok=%v", j, ok)
	}
	m = step(t, m, key("c"))
	if !strings.Contains(m.notice, "cancel requested") {
		t.Fatalf("expected cancel notice, got %q (err %v)", m.notice, m.err)
	}
	if got := srv.postedTo(); len(got) != 1 || got[0] != "POST /api/v2/workflow_jobs/11/cancel/" {
		t.Fatalf("cancel posted to %v, want one POST to workflow_jobs/11/cancel/", got)
	}
}

// Read-only mode must refuse to launch a workflow while still letting its
// form open, exactly as for a job template.
func TestReadOnlyModeBlocksWorkflowLaunch(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false).ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, m.connect())

	m = step(t, m, key("6"))
	m = rowAt(t, m, "Nightly pipeline")
	m = step(t, m, key("enter"))
	if m.mode != modeLaunch {
		t.Fatalf("read-only mode should still allow inspecting the form, mode = %v (err %v)", m.mode, m.err)
	}
	m = step(t, m, key("ctrl+s"))
	if m.mode != modeLaunch {
		t.Error("form should stay open after a refused submit")
	}
	if !strings.Contains(m.form.problem, "read-only") {
		t.Errorf("problem = %q, want a read-only message", m.form.problem)
	}
	if srv.launchCount() != 0 {
		t.Errorf("read-only mode launched something: %d", srv.launchCount())
	}
}

func TestPinningAWorkflowTemplate(t *testing.T) {
	m, _ := openTab(t, "6")
	m = rowAt(t, m, "Nightly pipeline")
	m = step(t, m, key("p"))
	if m.notice == "" || !strings.Contains(m.notice, "pinned") {
		t.Fatalf("expected a pinned notice, got %q (err %v)", m.notice, m.err)
	}
	if !m.pinned(tabWorkflows, 20) {
		t.Error("workflow template #20 should be pinned")
	}
}
