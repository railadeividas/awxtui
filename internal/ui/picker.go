package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The launch form's multi-selects — credentials, instance groups, labels and
// survey multiselects — hold whole catalogues that never fit the field's
// one-line, horizontally-scrolling window (multiChoiceWindow in view.go): 19
// instance groups or 52 credentials cannot be read a handful at a time. This
// file adds a full-screen picker, opened from that field with enter, that
// lists every entry vertically and scrolls like any other list here.
//
// A single-choice field can face the same problem — an ad hoc command's
// credential or module — without being a multi-select at all, so an fChoice
// field can opt in with usePicker and gets the same list, minus the
// checkboxes: moving the highlight is already choosing, so enter and esc
// both just close it.

// openPicker enters the picker for the focused field. The caller has already
// checked the field is an fMultiChoice, or an fChoice marked usePicker, with
// something to choose from.
func (m *Model) openPicker() {
	m.mode = modePick
	m.form.pickOffset = 0
}

// pickHeight is how many catalogue rows fit inside the picker modal.
func (m Model) pickHeight() int {
	return max(m.tableHeight()-6, 5)
}

func (m Model) handlePickKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fl := m.form.focused()
	if fl == nil || (fl.kind != fMultiChoice && !(fl.kind == fChoice && fl.usePicker)) {
		m.mode = modeLaunch
		return m, nil
	}
	single := fl.kind == fChoice
	n := len(fl.choices)
	h := m.pickHeight()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeLaunch
		return m, nil
	case "enter":
		// idx is the field's own value for a single choice, so moving the
		// highlight has already chosen it; enter and esc both just close.
		m.mode = modeLaunch
		return m, nil
	case "up", "k":
		fl.idx = clamp(fl.idx-1, 0, n-1)
	case "down", "j":
		fl.idx = clamp(fl.idx+1, 0, n-1)
	case "pgup", "ctrl+u":
		fl.idx = clamp(fl.idx-h, 0, n-1)
	case "pgdown", "ctrl+d":
		fl.idx = clamp(fl.idx+h, 0, n-1)
	case "home", "g":
		fl.idx = 0
	case "end", "G":
		fl.idx = n - 1
	case " ":
		if !single && fl.idx < len(fl.chosen) {
			fl.chosen[fl.idx] = !fl.chosen[fl.idx]
		}
	case "a":
		if !single {
			for i := range fl.chosen {
				fl.chosen[i] = true
			}
		}
	case "c":
		if !single {
			for i := range fl.chosen {
				fl.chosen[i] = false
			}
		}
	}
	m.form.pickOffset = clampOffset(fl.idx, m.form.pickOffset, h, n)
	return m, nil
}

// pickerModal renders the whole catalogue of the field being picked, one
// entry per line, scrolled to keep the highlighted one visible.
func (m Model) pickerModal() string {
	fl := m.form.focused()
	if fl == nil {
		return ""
	}
	single := fl.kind == fChoice
	width := min(m.width-8, 70)
	h := m.pickHeight()
	n := len(fl.choices)
	offset := clampOffset(fl.idx, m.form.pickOffset, h, n)

	var b strings.Builder
	b.WriteString(titleStyle.Render(fl.label))
	if single {
		b.WriteString("  " + dimStyle.Render("choose one"))
	} else {
		b.WriteString("  " + dimStyle.Render(fmt.Sprintf("%d of %d selected", len(fl.selections()), n)))
	}
	b.WriteString("\n\n")

	end := min(offset+h, n)
	for i := offset; i < end; i++ {
		line := fl.choices[i]
		if !single {
			box := "[ ]"
			if i < len(fl.chosen) && fl.chosen[i] {
				box = "[x]"
			}
			line = box + " " + line
		}
		switch {
		case i == fl.idx:
			b.WriteString(rowSelStyle.Render("▌" + line))
		case !single && i < len(fl.chosen) && fl.chosen[i]:
			b.WriteString(rowStyle.Render(" " + line))
		default:
			b.WriteString(dimStyle.Render(" " + line))
		}
		b.WriteString("\n")
	}
	if offset > 0 || end < n {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  %d-%d of %d", offset+1, end, n)) + "\n")
	}
	return modalStyle.Width(width).Render(b.String())
}
