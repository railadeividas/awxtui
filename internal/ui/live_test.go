package ui

import (
	"os"
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

	m := New(awx.New(url, token, os.Getenv("AWX_INSECURE") != ""))
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 36})
	m = step(t, m, m.connect())
	if m.err != nil {
		t.Fatalf("connect failed: %v", m.err)
	}
	t.Logf("connected as %s, %d templates", m.user, len(m.rows[tabTemplates]))

	m = step(t, m, key("2"))
	if m.err != nil {
		t.Fatalf("loading jobs failed: %v", m.err)
	}
	if len(m.rows[tabJobs]) == 0 {
		t.Skip("no jobs on this instance to inspect")
	}

	m = step(t, m, key("enter"))
	if m.err != nil {
		t.Fatalf("opening job output failed: %v", m.err)
	}
	if strings.TrimSpace(m.outputText) == "" {
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
			t.Fatalf("finished job #%d produced no output", m.outputJob.ID)
		}
		t.Logf("finished job #%d %s: %d bytes in %s",
			m.outputJob.ID, m.outputJob.Status, len(m.outputText), time.Since(start).Round(time.Millisecond))
		break
	}
}
