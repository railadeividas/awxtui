package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

func TestSchedulesListRenders(t *testing.T) {
	m, _ := openTab(t, "5")
	view := stripANSI(m.View())
	show(t, "schedules list", m.View())
	for _, want := range []string{"nightly backup", "Cleanup Job Schedule", "template", "system"} {
		if !strings.Contains(view, want) {
			t.Errorf("schedules list is missing %q\n%s", want, view)
		}
	}
}

func TestScheduleDetailOpensAndCloses(t *testing.T) {
	m, _ := openTab(t, "5")
	m = rowAt(t, m, "nightly backup")
	m = step(t, m, key("enter"))

	if m.mode != modeSchedule {
		t.Fatalf("enter left mode %v, want modeSchedule", m.mode)
	}
	view := stripANSI(m.View())
	show(t, "schedule detail", m.View())
	for _, want := range []string{"Schedule #7", "nightly backup", "Deploy fleet", "FREQ=DAILY"} {
		if !strings.Contains(view, want) {
			t.Errorf("schedule detail is missing %q\n%s", want, view)
		}
	}

	m = step(t, m, key("esc"))
	if m.mode != modeList || m.active != tabSchedules {
		t.Errorf("esc left mode %v on tab %v", m.mode, m.active)
	}
}

func TestToggleScheduleEnabledFromList(t *testing.T) {
	m, srv := openTab(t, "5")
	m = rowAt(t, m, "Cleanup Job Schedule")
	m = step(t, m, key("t"))

	if m.err != nil {
		t.Fatalf("toggling a schedule failed: %v", m.err)
	}
	if got := srv.postedTo(); len(got) != 1 || got[0] != "PATCH /api/v2/schedules/8/" {
		t.Fatalf("toggle wrote %v, want one PATCH to /api/v2/schedules/8/", got)
	}
	m = rowAt(t, m, "Cleanup Job Schedule")
	if !strings.Contains(stripANSI(m.rows[tabSchedules][m.cursor[tabSchedules]].cells[4]), "enabled") {
		t.Errorf("row does not reflect the schedule being enabled: %v", m.rows[tabSchedules])
	}
	if m.toggling {
		t.Error("still marked as toggling after the toggle settled")
	}
}

func TestToggleScheduleEnabledFromDetail(t *testing.T) {
	m, srv := openTab(t, "5")
	m = rowAt(t, m, "nightly backup")
	m = step(t, m, key("enter"))
	m = step(t, m, key("t"))

	if m.err != nil {
		t.Fatalf("toggling a schedule failed: %v", m.err)
	}
	if got := srv.postedTo(); len(got) != 1 || got[0] != "PATCH /api/v2/schedules/7/" {
		t.Fatalf("toggle wrote %v, want one PATCH to /api/v2/schedules/7/", got)
	}
	if m.schedule.schedule.Enabled {
		t.Error("detail still shows the schedule as enabled after disabling it")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "enable") {
		t.Errorf("legend does not offer to re-enable: %s", view)
	}
}

func TestToggleScheduleEnabledRefusedReadOnly(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false).ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m = step(t, m, m.connect())
	m = step(t, m, key("5"))
	m = rowAt(t, m, "nightly backup")
	m = step(t, m, key("t"))

	if m.err == nil || !strings.Contains(m.err.Error(), "read-only") {
		t.Fatalf("expected a read-only refusal, got %v", m.err)
	}
	if got := srv.postedTo(); len(got) != 0 {
		t.Errorf("read-only toggle still wrote %v", got)
	}
}

func TestSchedulePinning(t *testing.T) {
	m, _ := openTab(t, "5")
	m = rowAt(t, m, "nightly backup")
	m = step(t, m, key("p"))

	if !m.pinned(tabSchedules, 7) {
		t.Fatal("schedule was not pinned")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "★") {
		t.Errorf("pinned row has no marker: %s", view)
	}
}
