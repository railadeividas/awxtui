package ui

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// TestLive runs against a real AWX instance. It is skipped unless AWXTUI_LIVE
// is set along with AWX_URL and AWX_TOKEN:
//
//	AWXTUI_LIVE=1 go test -v ./internal/ui -run TestLive
func TestLive(t *testing.T) {
	if os.Getenv("AWXTUI_LIVE") == "" {
		t.Skip("set AWXTUI_LIVE=1 to run against a real AWX instance")
	}
	url, token := os.Getenv("AWX_URL"), os.Getenv("AWX_TOKEN")
	if url == "" || token == "" {
		t.Skip("AWX_URL and AWX_TOKEN must be set")
	}

	// The instance behind AWX_URL is assumed to be production: pin the client
	// read-only so this test can never launch or cancel anything.
	m := New(awx.New(url, token, os.Getenv("AWX_INSECURE") != "").ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 36})
	m = step(t, m, m.connect())
	if m.err != nil {
		t.Fatalf("connect failed: %v", m.err)
	}
	t.Logf("connected as %s", m.user)
	for _, view := range []struct {
		tab  tab
		name string
	}{{tabTemplates, "templates"}, {tabInventories, "inventories"}, {tabProjects, "projects"}} {
		start := time.Now()
		m = step(t, m, key(strconv.Itoa(int(view.tab)+1)))
		if m.err != nil {
			t.Fatalf("loading %s failed: %v", view.name, m.err)
		}
		t.Logf("%s: %d loaded of %d reported in %s, label %q",
			view.name, len(m.rows[view.tab]), m.count[view.tab],
			time.Since(start).Round(time.Millisecond), m.countLabel(view.tab))
		if len(m.rows[view.tab]) == 0 && m.count[view.tab] > 0 {
			t.Errorf("%s: reported %d records but loaded none", view.name, m.count[view.tab])
		}
	}

	// Server-side search must find a record regardless of which page it is on.
	m = step(t, m, key("1"))
	m = step(t, m, key("/"))
	m = typeText(t, m, "dnstools")
	t.Logf("search 'dnstools': %d rows, label %q, serverQuery %q",
		len(m.visible(tabTemplates)), m.countLabel(tabTemplates), m.serverQuery[tabTemplates])
	if len(m.visible(tabTemplates)) == 0 {
		t.Errorf("server-side search for dnstools found nothing")
	}
	if m.serverQuery[tabTemplates] != "dnstools" {
		t.Errorf("search did not reach AWX: serverQuery = %q", m.serverQuery[tabTemplates])
	}
	m = step(t, m, key("esc"))

	m = step(t, m, key("1"))

	m = step(t, m, key("2"))
	if m.err != nil {
		t.Fatalf("loading jobs failed: %v", m.err)
	}
	t.Logf("jobs: %d loaded, label %q", len(m.rows[tabJobs]), m.countLabel(tabJobs))
	if len(m.rows[tabJobs]) == 0 {
		t.Skip("no jobs on this instance to inspect")
	}
	// Scrolling to the end must page in more rather than stopping at page one.
	before := len(m.rows[tabJobs])
	m = step(t, m, key("G"))
	t.Logf("jobs after scrolling to the end: %d loaded (was %d)", len(m.rows[tabJobs]), before)
	if m.count[tabJobs] > before && len(m.rows[tabJobs]) <= before {
		t.Errorf("scrolling to the end loaded nothing new (%d rows, %d reported)",
			len(m.rows[tabJobs]), m.count[tabJobs])
	}
	m = step(t, m, key("g"))

	m = step(t, m, key("enter"))
	if m.err != nil {
		t.Fatalf("opening job output failed: %v", m.err)
	}
	// A job that has not started yet legitimately has nothing to show.
	queued := m.outputJob.Status == "pending" || m.outputJob.Status == "waiting" ||
		m.outputJob.Status == "new"
	if strings.TrimSpace(m.outputText) == "" && !queued {
		t.Fatalf("job #%d (%s) produced no output", m.outputJob.ID, m.outputJob.Status)
	}
	t.Logf("job #%d %s: %d bytes of output, up to counter %d",
		m.outputJob.ID, m.outputJob.Status, len(m.outputText), m.outputCounter)
	show(t, "live job output", m.View())

	// Also exercise the finished-job path, which reads /stdout/ in one request.
	m = step(t, m, key("esc"))
	for i, j := range m.jobs {
		if j.IsRunning() {
			continue
		}
		m.cursor[tabJobs] = i
		start := time.Now()
		m = step(t, m, key("enter"))
		if m.err != nil {
			t.Fatalf("opening finished job failed: %v", m.err)
		}
		if strings.TrimSpace(m.outputText) == "" {
			// AWX can lag for a moment after a job finishes; only treat an
			// older job with no output as a real failure.
			if m.outputJob.Finished != nil && time.Since(*m.outputJob.Finished) < 2*time.Minute {
				t.Logf("finished job #%d has no output yet (finished %s ago)",
					m.outputJob.ID, time.Since(*m.outputJob.Finished).Round(time.Second))
				break
			}
			t.Fatalf("finished job #%d produced no output", m.outputJob.ID)
		}
		t.Logf("finished job #%d %s: %d bytes, %d wrapped lines, in %s",
			m.outputJob.ID, m.outputJob.Status, len(m.outputText), len(m.outputLines),
			time.Since(start).Round(time.Millisecond))

		// Find-in-output over real, ANSI-coloured Ansible output.
		m = step(t, m, key("/"))
		m = typeText(t, m, "TASK")
		t.Logf("search 'TASK': %s", m.matchLabel())
		if len(m.osearch.matches) == 0 {
			t.Errorf("no TASK lines found in %d lines of real output", len(m.outputLines))
		}
		m = step(t, m, key("enter"))
		m = step(t, m, key("n"))
		m = step(t, m, key("g"))
		if m.jumpLine(1, isTask) {
			t.Logf("jumped to task line %d of %d", m.outputCursor, len(m.outputLines))
		} else {
			t.Errorf("task jump found nothing in real output")
		}
		break
	}
}

// TestLiveLaunchForm builds the launch form for a real template. The client is
// read-only, so this only ever issues GETs: it can never launch anything.
//
//	AWXTUI_LIVE=1 AWXTUI_SHOW=1 go test -v ./internal/ui -run TestLiveLaunchForm
func TestLiveLaunchForm(t *testing.T) {
	if os.Getenv("AWXTUI_LIVE") == "" {
		t.Skip("set AWXTUI_LIVE=1 to run against a real AWX instance")
	}
	url, token := os.Getenv("AWX_URL"), os.Getenv("AWX_TOKEN")
	if url == "" || token == "" {
		t.Skip("AWX_URL and AWX_TOKEN must be set")
	}
	name := os.Getenv("AWXTUI_LIVE_TEMPLATE")

	client := awx.New(url, token, os.Getenv("AWX_INSECURE") != "").ReadOnly()
	m := New(client)
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 44})
	m = step(t, m, m.connect())
	if m.err != nil {
		t.Fatalf("connect failed: %v", m.err)
	}

	picked := -1
	for i, tpl := range m.templates {
		if name == "" || strings.Contains(tpl.Name, name) {
			picked = i
			break
		}
	}
	if picked < 0 {
		t.Skipf("no template matching %q", name)
	}
	m.cursor[tabTemplates] = picked
	tpl := m.templates[picked]

	m = step(t, m, key("enter"))
	if m.mode != modeLaunch {
		t.Fatalf("form did not open for %q: mode %v err %v", tpl.Name, m.mode, m.err)
	}
	t.Logf("%s (#%d): survey=%v fields=%v",
		tpl.Name, tpl.ID, m.form.config.SurveyEnabled, formKeys(&m))
	show(t, "live launch form: "+tpl.Name, m.View())

	// Every prompt the template asks for must have become a field. This is the
	// check that was missing: ansible-dns_dnstools_main sets
	// ask_instance_groups_on_launch, and the form used to ignore it, so the
	// job silently ran on the template's own instance groups.
	cfg := m.form.config
	for _, p := range []struct {
		asked bool
		key   string
	}{
		{cfg.AskInventory, "inventory"},
		{cfg.AskCredentials, "credentials"},
		{cfg.AskExecutionEnvironment, "execution_environment"},
		{cfg.AskInstanceGroups, "instance_groups"},
		{cfg.AskLabels, "labels"},
		{cfg.AskJobType, "job_type"},
		{cfg.AskSCMBranch, "scm_branch"},
		{cfg.AskLimit, "limit"},
		{cfg.AskVerbosity, "verbosity"},
		{cfg.AskTags, "job_tags"},
		{cfg.AskSkipTags, "skip_tags"},
		{cfg.AskDiffMode, "diff_mode"},
		{cfg.AskForks, "forks"},
		{cfg.AskJobSliceCount, "job_slice_count"},
		{cfg.AskTimeout, "timeout"},
		{cfg.AskVariables, "extra_vars"},
	} {
		if !p.asked {
			continue
		}
		var fl *formField
		for i := range m.form.fields {
			if m.form.fields[i].key == p.key {
				fl = &m.form.fields[i]
			}
		}
		if fl == nil {
			t.Errorf("template prompts for %s but the form has no such field (%v)", p.key, formKeys(&m))
			continue
		}
		switch fl.kind {
		case fMultiChoice:
			t.Logf("  %s: %d choices, pre-selected %v (ids %v)",
				p.key, len(fl.choices), fl.selections(), fl.selectedIDs())
		case fChoice:
			t.Logf("  %s: %d choices, value %q", p.key, len(fl.choices), fl.value())
		}
	}

	// Submitting must be refused by the read-only client.
	m = step(t, m, key("ctrl+s"))
	if m.form.problem != "" && !strings.Contains(m.form.problem, "read-only") {
		t.Logf("form reported: %s", m.form.problem)
	}
	if m.mode != modeLaunch {
		t.Fatal("read-only client must not submit the form")
	}
}

// TestLiveSyncTargets checks what a sync would act on, without starting one.
// The client is read-only, so every call here is a GET: the project update
// endpoint answers can_update to a GET and only starts an update on a POST,
// and an inventory's sources are read before any of them is touched.
//
//	AWXTUI_LIVE=1 go test -v ./internal/ui -run TestLiveSyncTargets
//
// TestLiveMyRuns checks the "started by me" filter against a real instance,
// read-only. It is the whole reason the filter exists: on the AWX this was
// built against the unfiltered list holds 170290 runs and the filtered one
// 310.
func TestLiveMyRuns(t *testing.T) {
	if os.Getenv("AWXTUI_LIVE") == "" {
		t.Skip("set AWXTUI_LIVE=1 to run against a real AWX instance")
	}
	url, token := os.Getenv("AWX_URL"), os.Getenv("AWX_TOKEN")
	if url == "" || token == "" {
		t.Skip("AWX_URL and AWX_TOKEN must be set")
	}

	m := New(awx.New(url, token, os.Getenv("AWX_INSECURE") != "").ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 36})
	m = step(t, m, m.connect())
	if m.err != nil {
		t.Fatalf("connect failed: %v", m.err)
	}
	if m.userID == 0 {
		t.Fatalf("/api/v2/me/ gave no user id for %s; the owner filter has nothing to filter by", m.user)
	}

	m = step(t, m, key("2"))
	all := m.count[tabJobs]
	// f, then right on the first row ("Started by": anyone -> me), then enter.
	m = step(t, m, key("f"))
	m = step(t, m, key("right"))
	m = step(t, m, key("enter"))
	if m.err != nil {
		t.Fatalf("the owner filter failed: %v", m.err)
	}
	mine := m.count[tabJobs]
	t.Logf("%s: %d runs of their own out of %d on the instance", m.user, mine, all)
	if mine == 0 {
		t.Skip("this account has started nothing on this instance")
	}
	if mine >= all {
		t.Errorf("mine (%d) is not narrower than the full list (%d); the filter did not reach AWX", mine, all)
	}
	kinds := map[string]int{}
	for _, j := range m.jobs {
		if j.SummaryFields.CreatedBy.Username != m.user {
			t.Errorf("#%d was created by %q, not %q", j.ID, j.SummaryFields.CreatedBy.Username, m.user)
		}
		kinds[j.KindLabel()]++
	}
	t.Logf("kinds in the filtered list: %v", kinds)
	show(t, "started by me", m.View())

	// Narrowing further: only the failures, still server-side.
	m = step(t, m, key("f"))
	m = step(t, m, key("down")) // started by -> status
	m = step(t, m, key("right"))
	m = step(t, m, key("right")) // running -> failed
	m = step(t, m, key(" "))     // toggle failed on
	if got := m.panel.draft.status; len(got) != 1 || got[0] != "failed" {
		t.Fatalf("expected the status choice to land on failed, got %q", got)
	}
	m = step(t, m, key("enter"))
	if m.err != nil {
		t.Fatalf("the status filter failed: %v", m.err)
	}
	t.Logf("failed runs of %s: %d, label %q", m.user, m.count[tabJobs], m.countLabel(tabJobs))
	if m.count[tabJobs] > mine {
		t.Errorf("failed (%d) cannot exceed all of this user's runs (%d)", m.count[tabJobs], mine)
	}
	for _, j := range m.jobs {
		if j.Status != "failed" {
			t.Errorf("#%d has status %q in a failed-only view", j.ID, j.Status)
		}
	}
	show(t, "started by me, failed", m.View())
}

func TestLiveSyncTargets(t *testing.T) {
	if os.Getenv("AWXTUI_LIVE") == "" {
		t.Skip("set AWXTUI_LIVE=1 to run against a real AWX instance")
	}
	url, token := os.Getenv("AWX_URL"), os.Getenv("AWX_TOKEN")
	if url == "" || token == "" {
		t.Skip("AWX_URL and AWX_TOKEN must be set")
	}

	client := awx.New(url, token, os.Getenv("AWX_INSECURE") != "").ReadOnly()
	m := New(client)
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, m.connect())
	if m.err != nil {
		t.Fatalf("connect failed: %v", m.err)
	}
	ctx, cancel := cmdCtx()
	defer cancel()

	m = step(t, m, key("4"))
	for _, p := range m.projects {
		can, err := client.CanUpdate(ctx, p.ID)
		if err != nil {
			t.Errorf("%s (#%d): can_update failed: %v", p.Name, p.ID, err)
			continue
		}
		t.Logf("project %s (#%d): %s, can_update=%v", p.Name, p.ID, p.SCMTypeLabel(), can)
		// A project with no source control has nothing to pull, and AWX
		// refuses to update it; anything with an SCM type should be updatable
		// unless one of its updates is already running.
		if p.SCMType == "" && can {
			t.Errorf("%s has no SCM type but AWX says it can be updated", p.Name)
		}
	}

	m = step(t, m, key("3"))
	for _, inv := range m.inventories {
		page, err := client.InventorySources(ctx, inv.ID, "", "")
		if err != nil {
			t.Errorf("%s (#%d): listing sources failed: %v", inv.Name, inv.ID, err)
			continue
		}
		// total_inventory_sources on the list row is what the sources column
		// shows and what the sync key checks before it writes anything, so it
		// has to agree with the sources themselves.
		if page.Count != inv.TotalInventorySources {
			t.Errorf("%s: row claims %d sources, the endpoint reports %d",
				inv.Name, inv.TotalInventorySources, page.Count)
		}
		for _, src := range page.Results {
			t.Logf("inventory %s (#%d) source #%d: %s, status %s",
				inv.Name, inv.ID, src.ID, src.Label(), src.Status)
		}
	}
	show(t, "live inventories with sources", m.View())
}

// TestLiveProjectDetails opens the details of every real project. The client
// is read-only, so this only ever issues GETs.
//
//	AWXTUI_LIVE=1 AWXTUI_SHOW=1 go test -v ./internal/ui -run TestLiveProjectDetails
func TestLiveProjectDetails(t *testing.T) {
	if os.Getenv("AWXTUI_LIVE") == "" {
		t.Skip("set AWXTUI_LIVE=1 to run against a real AWX instance")
	}
	url, token := os.Getenv("AWX_URL"), os.Getenv("AWX_TOKEN")
	if url == "" || token == "" {
		t.Skip("AWX_URL and AWX_TOKEN must be set")
	}

	m := New(awx.New(url, token, os.Getenv("AWX_INSECURE") != "").ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, m.connect())
	if m.err != nil {
		t.Fatalf("connect failed: %v", m.err)
	}
	m = step(t, m, key("4"))
	if len(m.projects) == 0 {
		t.Skip("no projects on this instance")
	}

	for i, p := range m.projects {
		m.cursor[tabProjects] = i
		m = step(t, m, key("enter"))
		if m.mode != modeProject {
			t.Fatalf("details did not open for %q: mode %v err %v", p.Name, m.mode, m.err)
		}
		t.Logf("%s (#%d): %s %s@%.7s, status %s, %d body lines",
			p.Name, p.ID, p.SCMTypeLabel(), p.SCMBranch, p.SCMRevision,
			p.Status, len(m.projectBody(m.projectWidth())))
		show(t, "live project details: "+p.Name, m.View())
		m = step(t, m, key("G"))
		m = step(t, m, key("esc"))
	}
}

// TestLiveWorkflowTemplates builds the launch form for every real workflow
// job template. The client is read-only, so this only ever issues GETs.
//
//	AWXTUI_LIVE=1 AWXTUI_SHOW=1 go test -v ./internal/ui -run TestLiveWorkflowTemplates
func TestLiveWorkflowTemplates(t *testing.T) {
	if os.Getenv("AWXTUI_LIVE") == "" {
		t.Skip("set AWXTUI_LIVE=1 to run against a real AWX instance")
	}
	url, token := os.Getenv("AWX_URL"), os.Getenv("AWX_TOKEN")
	if url == "" || token == "" {
		t.Skip("AWX_URL and AWX_TOKEN must be set")
	}

	m := New(awx.New(url, token, os.Getenv("AWX_INSECURE") != "").ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, m.connect())
	if m.err != nil {
		t.Fatalf("connect failed: %v", m.err)
	}
	m = step(t, m, key("6"))
	if m.err != nil {
		t.Fatalf("loading workflow job templates failed: %v", m.err)
	}
	if len(m.workflows) == 0 {
		t.Skip("no workflow job templates on this instance")
	}

	for i, wf := range m.workflows {
		m.cursor[tabWorkflows] = i
		m = step(t, m, key("enter"))
		if m.mode != modeLaunch {
			t.Fatalf("form did not open for %q: mode %v err %v", wf.Name, m.mode, m.err)
		}
		if !m.form.isWorkflow {
			t.Errorf("%s (#%d): form was not marked as a workflow launch", wf.Name, wf.ID)
		}
		t.Logf("%s (#%d): survey=%v fields=%v", wf.Name, wf.ID, m.form.config.SurveyEnabled, formKeys(&m))
		show(t, "live workflow launch form: "+wf.Name, m.View())

		// A workflow must never grow a node-only field: those belong to the
		// job templates inside it, and its own launch config never sets
		// the flags that create them.
		cfg := m.form.config
		if cfg.AskCredentials || cfg.AskExecutionEnvironment || cfg.AskJobType || cfg.AskVerbosity ||
			cfg.AskForks || cfg.AskJobSliceCount || cfg.AskInstanceGroups {
			t.Errorf("%s (#%d): workflow launch config set a node-only ask_* flag: %+v", wf.Name, wf.ID, cfg)
		}
		for _, k := range formKeys(&m) {
			switch k {
			case "credentials", "execution_environment", "job_type", "verbosity", "forks", "job_slice_count", "instance_groups":
				t.Errorf("%s (#%d): form grew node-only field %q", wf.Name, wf.ID, k)
			}
		}

		// Submitting must be refused by the read-only client.
		m = step(t, m, key("ctrl+s"))
		if m.mode != modeLaunch {
			t.Fatalf("read-only client must not submit a workflow's form (%s)", wf.Name)
		}
		m = step(t, m, key("esc"))
	}
}

// TestLiveWorkflowJobNodes opens the launch details of a real workflow job
// — found through /api/v2/unified_jobs/?type=workflow_job rather than a
// hardcoded id, since which ones exist drifts over time — and checks its
// nodes render. The client is read-only, so this only ever issues GETs.
//
//	AWXTUI_LIVE=1 AWXTUI_SHOW=1 go test -v ./internal/ui -run TestLiveWorkflowJobNodes
func TestLiveWorkflowJobNodes(t *testing.T) {
	if os.Getenv("AWXTUI_LIVE") == "" {
		t.Skip("set AWXTUI_LIVE=1 to run against a real AWX instance")
	}
	url, token := os.Getenv("AWX_URL"), os.Getenv("AWX_TOKEN")
	if url == "" || token == "" {
		t.Skip("AWX_URL and AWX_TOKEN must be set")
	}

	client := awx.New(url, token, os.Getenv("AWX_INSECURE") != "").ReadOnly()
	m := New(client)
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, m.connect())
	if m.err != nil {
		t.Fatalf("connect failed: %v", m.err)
	}

	ctx, cancel := cmdCtx()
	defer cancel()
	page, err := client.UnifiedJobs(ctx, "", "", awx.JobFilter{Type: []string{"workflow_job"}})
	if err != nil {
		t.Fatalf("listing workflow jobs failed: %v", err)
	}
	if len(page.Results) == 0 {
		t.Skip("no workflow jobs on this instance")
	}
	j := page.Results[0]
	if !j.IsWorkflow() {
		t.Fatalf("?type=workflow_job returned #%d with type %q", j.ID, j.Type)
	}

	cmd := m.openJobDetail(j)
	for _, out := range drain(cmd) {
		m = step(t, m, out)
	}
	if m.mode != modeJob {
		t.Fatalf("details did not open for #%d: mode %v err %v", j.ID, m.mode, m.err)
	}
	if m.job.nodesLoading {
		t.Fatalf("#%d: nodes never finished loading", j.ID)
	}
	if len(m.job.nodes) == 0 && m.job.nodesTotal > 0 {
		t.Errorf("#%d: reported %d nodes but loaded none", j.ID, m.job.nodesTotal)
	}
	t.Logf("workflow job #%d (%s): %d nodes loaded of %d reported", j.ID, j.Name, len(m.job.nodes), m.job.nodesTotal)
	for _, n := range m.job.nodes {
		t.Logf("  node #%d %q: %s", n.ID, n.Name(), n.Status())
	}
	show(t, "live workflow job details", m.View())
}

// TestLiveSchedules opens the details of every real schedule. The client is
// read-only, so this only ever issues GETs — it never calls
// SetScheduleEnabled.
//
//	AWXTUI_LIVE=1 AWXTUI_SHOW=1 go test -v ./internal/ui -run TestLiveSchedules
func TestLiveSchedules(t *testing.T) {
	if os.Getenv("AWXTUI_LIVE") == "" {
		t.Skip("set AWXTUI_LIVE=1 to run against a real AWX instance")
	}
	url, token := os.Getenv("AWX_URL"), os.Getenv("AWX_TOKEN")
	if url == "" || token == "" {
		t.Skip("AWX_URL and AWX_TOKEN must be set")
	}

	m := New(awx.New(url, token, os.Getenv("AWX_INSECURE") != "").ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, m.connect())
	if m.err != nil {
		t.Fatalf("connect failed: %v", m.err)
	}
	m = step(t, m, key("5"))
	if len(m.schedules) == 0 {
		t.Skip("no schedules on this instance")
	}

	for i, s := range m.schedules {
		m.cursor[tabSchedules] = i
		m = step(t, m, key("enter"))
		if m.mode != modeSchedule {
			t.Fatalf("details did not open for %q: mode %v err %v", s.Name, m.mode, m.err)
		}
		t.Logf("%s (#%d): runs %s %q, enabled=%v, next run %v",
			s.Name, s.ID, s.SummaryFields.UnifiedJobTemplate.UnifiedJobType,
			s.SummaryFields.UnifiedJobTemplate.Name, s.Enabled, s.NextRun)
		show(t, "live schedule details: "+s.Name, m.View())
		m = step(t, m, key("G"))
		m = step(t, m, key("esc"))
	}
}

// TestLiveInventoryGroups opens an inventory with real groups and confirms
// the members view and the details preview both read them correctly. It only
// ever issues GETs: no group or host is selected past the point of reading
// m.limitSel back, and neither the ad hoc form nor a template launch form is
// ever opened, so nothing can be submitted.
//
//	AWXTUI_LIVE=1 AWXTUI_SHOW=1 go test -v ./internal/ui -run TestLiveInventoryGroups
func TestLiveInventoryGroups(t *testing.T) {
	if os.Getenv("AWXTUI_LIVE") == "" {
		t.Skip("set AWXTUI_LIVE=1 to run against a real AWX instance")
	}
	url, token := os.Getenv("AWX_URL"), os.Getenv("AWX_TOKEN")
	if url == "" || token == "" {
		t.Skip("AWX_URL and AWX_TOKEN must be set")
	}

	m := New(awx.New(url, token, os.Getenv("AWX_INSECURE") != "").ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, m.connect())
	if m.err != nil {
		t.Fatalf("connect failed: %v", m.err)
	}

	m = step(t, m, key("3"))
	var target awx.Inventory
	for _, inv := range m.inventories {
		if inv.TotalGroups > 0 {
			target = inv
			break
		}
	}
	if target.ID == 0 {
		t.Skip("no inventory on this instance reports any groups")
	}
	m = rowAt(t, m, target.Name)
	m = step(t, m, key("enter"))
	if m.mode != modeInventory {
		t.Fatalf("expected inventory details, got mode %v (err %v)", m.mode, m.err)
	}
	preview := stripANSI(m.View())
	t.Logf("%s: %d hosts, %d groups reported; preview:\n%s",
		target.Name, target.TotalHosts, target.TotalGroups, preview)
	show(t, "live inventory details with preview", m.View())

	m = step(t, m, key("g"))
	if m.mode != modeMembers || m.members.kind != memberGroups {
		t.Fatalf("g did not open the groups view: mode %v (err %v)", m.mode, m.err)
	}
	if m.members.count != target.TotalGroups {
		t.Errorf("groups view reports %d, the inventory row claims %d", m.members.count, target.TotalGroups)
	}
	if len(m.members.rows) == 0 {
		t.Fatalf("groups view loaded nothing for an inventory reporting %d", target.TotalGroups)
	}
	t.Logf("%s: first group loaded is %q", target.Name, m.members.names[0])
	show(t, "live groups view: "+target.Name, m.View())

	// Select the first two loaded groups (or just the one, if that's all
	// there is) and confirm — GETs only, nothing launched.
	m = step(t, m, key(" "))
	if len(m.members.rows) > 1 {
		m = step(t, m, key("down"))
		m = step(t, m, key(" "))
	}
	m = step(t, m, key("enter"))
	if m.mode != modeInventory {
		t.Fatalf("enter after selecting should return to the inventory details, got mode %v", m.mode)
	}
	if m.limitSel.inventoryID != target.ID || len(m.limitSel.names) == 0 {
		t.Fatalf("selecting groups did not set a limit for %s: %+v", target.Name, m.limitSel)
	}
	t.Logf("%s: limit selection is %q", target.Name, m.limitSel.limit())
	if !strings.Contains(m.limitSel.limit(), m.members.names[0]) {
		t.Errorf("limit %q does not contain the first selected group %q", m.limitSel.limit(), m.members.names[0])
	}
}
