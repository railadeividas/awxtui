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
		t.Logf("finished job #%d %s: %d bytes in %s",
			m.outputJob.ID, m.outputJob.Status, len(m.outputText), time.Since(start).Round(time.Millisecond))
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

	// Submitting must be refused by the read-only client.
	m = step(t, m, key("ctrl+s"))
	if m.form.problem != "" && !strings.Contains(m.form.problem, "read-only") {
		t.Logf("form reported: %s", m.form.problem)
	}
	if m.mode != modeLaunch {
		t.Fatal("read-only client must not submit the form")
	}
}
