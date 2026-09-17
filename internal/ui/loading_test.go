package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// pressed is a key press as a case's message, ignoring the model.
func pressed(k string) func(Model) tea.Msg {
	return func(Model) tea.Msg { return key(k) }
}

// probe applies one message without draining its commands: the frame it
// returns is what the user sees while the request is still on the wire.
func probe(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

// Every request has to raise the badge — the counter lives in one place
// exactly so that no path can talk to AWX silently.
func TestLoadingBadgeCoversEveryRequest(t *testing.T) {
	cases := []struct {
		name string
		tab  string
		// setup runs on the connected model before the probe.
		setup func(*testing.T, Model) Model
		// msg is built from the model so a case can name a sequence number
		// its setup has already moved on.
		msg func(Model) tea.Msg
	}{
		{name: "refresh a list", tab: "2", msg: pressed("r")},
		{name: "switch tab", tab: "2", msg: pressed("4")},
		{name: "open a launch form", tab: "1", msg: pressed("enter")},
		{name: "open job output", tab: "2", msg: pressed("enter")},
		{name: "reload job output", tab: "2", setup: func(t *testing.T, m Model) Model {
			return step(t, m, key("enter"))
		}, msg: pressed("r")},
		{name: "open an inventory's hosts", tab: "3", msg: pressed("enter")},
		{name: "sync a project", tab: "4", setup: func(t *testing.T, m Model) Model {
			return rowAt(t, m, "infra")
		}, msg: pressed("s")},
		{name: "search a list", tab: "2", setup: func(t *testing.T, m Model) Model {
			// probe, not step: draining would let the debounce fire and the
			// search would be over before the case starts.
			m = probe(t, m, key("/"))
			for _, r := range "no-such-job" {
				m = probe(t, m, key(string(r)))
			}
			return m
		}, msg: func(m Model) tea.Msg {
			return searchTickMsg{tab: tabJobs, seq: m.searchSeq[tabJobs]}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := openTab(t, tc.tab)
			if tc.setup != nil {
				m = tc.setup(t, m)
			}
			if m.inflight != 0 {
				t.Fatalf("setup left %d requests in flight", m.inflight)
			}

			msg := tc.msg(m)
			busy := probe(t, m, msg)
			if busy.inflight == 0 {
				t.Fatalf("no request counted, so the badge still reads connected")
			}
			if v := busy.View(); !strings.Contains(v, "loading") {
				t.Errorf("badge does not say loading:\n%s", v)
			}

			// And it has to come down again: a request counted but never
			// released would leave the badge spinning for the rest of the
			// session.
			settled := step(t, m, msg)
			if settled.inflight != 0 {
				t.Errorf("%d requests still counted after settling", settled.inflight)
			}
		})
	}
}

// Before AWX has answered /api/v2/me/ there is no user to name, and the
// corner used to be blank — the one moment the app is certainly busy.
func TestBadgeSaysConnectingUntilConnected(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})

	if v := m.View(); !strings.Contains(v, "connecting") {
		t.Errorf("startup frame says nothing about connecting:\n%s", v)
	}
	m = step(t, m, m.connect())
	if v := m.View(); !strings.Contains(v, "connected") || strings.Contains(v, "connecting") {
		t.Errorf("connected frame still claims to be connecting:\n%s", v)
	}
}
