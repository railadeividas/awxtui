package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Ansible marks failures and task boundaries in predictable ways; these let
// the reader jump straight to what went wrong.
var (
	failureLine = regexp.MustCompile(`(?i)^(fatal|failed|unreachable|error):|^ERROR!|FAILED!`)
	taskLine    = regexp.MustCompile(`^(TASK|PLAY|PLAY RECAP|RUNNING HANDLER) \[?`)
)

// outputSearch is the find-in-output state of the job output view. Matches are
// indices into the wrapped output lines, so they map straight onto scrolling.
type outputSearch struct {
	input   textinput.Model
	query   string
	editing bool
	matches []int
	current int
}

func newOutputSearch() outputSearch {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "find in output"
	ti.PromptStyle = helpKeyStyle
	ti.TextStyle = inputStyle
	ti.Cursor.SetMode(cursor.CursorStatic)
	return outputSearch{input: ti}
}

func (s outputSearch) active() bool { return s.query != "" || s.editing }

// setOutput replaces the output text, re-wraps it and refreshes search state.
func (m *Model) setOutput(text string) {
	m.outputText = text
	m.vp.Width = m.width
	m.vp.Height = m.outputHeight()
	// AWX pads stdout with blank lines; rendering them leaves a dead band
	// above the footer and makes "100%" stop short of the last real line.
	// Only the display copy is trimmed: outputText keeps every byte so the
	// next streamed chunk still joins at the right place.
	m.outputLines = trimTrailingBlank(strings.Split(wrapANSI(text, m.width), "\n"))
	m.findMatches()
	m.vp.SetContent(m.outputContent())
	if m.follow {
		m.vp.GotoBottom()
	}
}

// findMatches records which wrapped lines contain the query, keeping the
// cursor on the nearest match so incoming output does not move the selection.
func (m *Model) findMatches() {
	prev := m.currentMatchLine()
	m.osearch.matches = nil
	q := strings.ToLower(strings.TrimSpace(m.osearch.query))
	if q == "" {
		m.osearch.current = 0
		return
	}
	for i, line := range m.outputLines {
		if strings.Contains(strings.ToLower(ansi.Strip(line)), q) {
			m.osearch.matches = append(m.osearch.matches, i)
		}
	}
	m.osearch.current = 0
	for i, line := range m.osearch.matches {
		if line >= prev {
			m.osearch.current = i
			break
		}
	}
}

// trimTrailingBlank drops empty lines from the end, keeping at least one.
func trimTrailingBlank(lines []string) []string {
	end := len(lines)
	for end > 1 && strings.TrimSpace(ansi.Strip(lines[end-1])) == "" {
		end--
	}
	return lines[:end]
}

func (m Model) currentMatchLine() int {
	if len(m.osearch.matches) == 0 || m.osearch.current >= len(m.osearch.matches) {
		return 0
	}
	return m.osearch.matches[m.osearch.current]
}

// outputContent renders the viewport body, highlighting search matches.
func (m Model) outputContent() string {
	if m.osearch.query == "" || len(m.osearch.matches) == 0 {
		return strings.Join(m.outputLines, "\n")
	}
	current := m.currentMatchLine()
	lines := make([]string, len(m.outputLines))
	copy(lines, m.outputLines)
	for _, i := range m.osearch.matches {
		lines[i] = highlightMatches(lines[i], m.osearch.query, i == current)
	}
	return strings.Join(lines, "\n")
}

// highlightMatches styles every occurrence of query in an ANSI-coloured line,
// leaving the rest of the line's own colours intact.
func highlightMatches(line, query string, current bool) string {
	plain := ansi.Strip(line)
	lower, q := strings.ToLower(plain), strings.ToLower(query)
	if q == "" || !strings.Contains(lower, q) {
		return line
	}
	style := matchStyle
	if current {
		style = matchCurrentStyle
	}

	var b strings.Builder
	cut := 0 // display cells already written
	for pos := 0; ; {
		i := strings.Index(lower[pos:], q)
		if i < 0 {
			break
		}
		start, end := pos+i, pos+i+len(q)
		// ansi.Cut works in display cells, so convert byte offsets first.
		startCell, endCell := ansi.StringWidth(plain[:start]), ansi.StringWidth(plain[:end])
		b.WriteString(ansi.Cut(line, cut, startCell))
		b.WriteString(style.Render(plain[start:end]))
		cut = endCell
		pos = end
	}
	b.WriteString(ansi.Cut(line, cut, ansi.StringWidth(plain)))
	return b.String()
}

// scrollTo positions a line about a third of the way down the viewport and
// remembers it, so the next jump starts from the line we landed on rather than
// from the top of the window.
func (m *Model) scrollTo(line int) {
	m.follow = false
	m.outputCursor = line
	m.vp.SetYOffset(clamp(line-m.vp.Height/3, 0, max(len(m.outputLines)-m.vp.Height, 0)))
}

// jumpFrom is the line jumps count from: wherever we last landed, unless the
// user has since scrolled it off screen.
func (m Model) jumpFrom() int {
	if m.outputCursor < m.vp.YOffset || m.outputCursor >= m.vp.YOffset+m.vp.Height {
		return m.vp.YOffset
	}
	return m.outputCursor
}

// jumpMatch moves to the next or previous search hit, wrapping around.
func (m *Model) jumpMatch(delta int) {
	if len(m.osearch.matches) == 0 {
		return
	}
	n := len(m.osearch.matches)
	m.osearch.current = ((m.osearch.current+delta)%n + n) % n
	m.scrollTo(m.currentMatchLine())
	m.vp.SetContent(m.outputContent())
}

// jumpLine moves to the next or previous line satisfying pred, searching from
// what is currently on screen.
func (m *Model) jumpLine(delta int, pred func(string) bool) bool {
	for i := m.jumpFrom() + delta; i >= 0 && i < len(m.outputLines); i += delta {
		if pred(ansi.Strip(m.outputLines[i])) {
			m.scrollTo(i)
			return true
		}
	}
	return false
}

func isFailure(plain string) bool { return failureLine.MatchString(strings.TrimSpace(plain)) }
func isTask(plain string) bool    { return taskLine.MatchString(strings.TrimSpace(plain)) }

// handleOutputKey drives the job output view.
func (m Model) handleOutputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.osearch.editing {
		switch key {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.osearch.editing = false
			m.osearch.query = ""
			m.osearch.input.SetValue("")
			m.osearch.input.Blur()
			m.findMatches()
			m.vp.SetContent(m.outputContent())
			return m, nil
		case "enter":
			m.osearch.editing = false
			m.osearch.input.Blur()
			if len(m.osearch.matches) > 0 {
				m.scrollTo(m.currentMatchLine())
				m.vp.SetContent(m.outputContent())
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.osearch.input, cmd = m.osearch.input.Update(msg)
		if q := m.osearch.input.Value(); q != m.osearch.query {
			m.osearch.query = q
			m.findMatches()
			if len(m.osearch.matches) > 0 {
				m.scrollTo(m.currentMatchLine())
			}
			m.vp.SetContent(m.outputContent())
		}
		return m, cmd
	}

	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc":
		// Clear a search before leaving the view.
		if m.osearch.active() {
			m.osearch.query = ""
			m.osearch.input.SetValue("")
			m.findMatches()
			m.vp.SetContent(m.outputContent())
			return m, nil
		}
		m.mode = modeList
		// A sync never shows up in the Jobs tab; what its status changed is
		// the list it was started from.
		if m.outputJob.IsSync() {
			return m, m.fetch(m.active, "", m.serverQuery[m.active], false)
		}
		return m, tea.Batch(m.fetch(tabJobs, "", m.serverQuery[tabJobs], false))
	case "/":
		m.osearch.editing = true
		m.osearch.input.SetValue(m.osearch.query)
		m.osearch.input.CursorEnd()
		m.osearch.input.Focus()
		return m, nil
	case "n":
		m.jumpMatch(1)
		return m, nil
	case "N":
		m.jumpMatch(-1)
		return m, nil
	case "]":
		if !m.jumpLine(1, isFailure) {
			m.notice = "no later failure"
		}
		return m, nil
	case "[":
		if !m.jumpLine(-1, isFailure) {
			m.notice = "no earlier failure"
		}
		return m, nil
	case "t":
		m.jumpLine(1, isTask)
		return m, nil
	case "T":
		m.jumpLine(-1, isTask)
		return m, nil
	case "f":
		m.follow = !m.follow
		if m.follow {
			m.vp.GotoBottom()
		}
		return m, nil
	case "p":
		// Pinning is most useful exactly here: watching a run is when you
		// decide it is one you will want to find again.
		return m, m.togglePin(tabJobs, m.outputJob.ID, m.outputJob.Name, m.outputJob.Type)
	case "r":
		m.err = nil
		return m, m.fetchOutput(m.outputJob.Resource(), m.outputJob.ID, 0)
	case "d":
		return m, m.openJobDetail(m.outputJob)
	case "c":
		if m.outputJob.IsRunning() {
			if m.client.IsReadOnly() {
				m.err = fmt.Errorf("read-only mode: cancelling is disabled")
				return m, nil
			}
			return m, m.cancelJob(m.outputJob.Resource(), m.outputJob.ID)
		}
		return m, nil
	case "g":
		m.follow = false
		m.vp.GotoTop()
		return m, nil
	case "G":
		m.follow = true
		m.vp.GotoBottom()
		return m, nil
	}

	// Manual scrolling drops follow mode.
	switch key {
	case "up", "k", "pgup", "ctrl+u", "ctrl+b":
		m.follow = false
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// outputView renders the job output screen.
func (m Model) outputView() string {
	j := m.outputJob
	head := titleStyle.Render(fmt.Sprintf("#%d", j.ID)) + "  " + rowStyle.Render(j.Name)
	// A sync's id belongs to its own collection, so #12 can be both a job and
	// a project update; say which this is.
	if j.IsSync() {
		head += "  " + metaStyle.Render(j.KindLabel())
	}
	// This view has its own header, so it needs its own copy of the badge the
	// list header carries: tailing a run is exactly where a request is most
	// often in flight.
	right := ""
	if m.inflight > 0 {
		right = m.spin.View() + metaStyle.Render(" loading") + "  "
	}
	right += statusBadge(j.Status) + metaStyle.Render("  "+duration(j.Elapsed))
	if j.IsRunning() {
		if m.follow {
			right += okStyle.Render("  ⟳ follow")
		} else {
			right += dimStyle.Render("  ⏸ paused")
		}
	}

	var b strings.Builder
	b.WriteString(m.spread(head, right))
	b.WriteString("\n")
	b.WriteString(m.rule())
	b.WriteString("\n")
	if strings.TrimSpace(m.outputText) == "" {
		placeholder := "  waiting for output…"
		if !j.IsRunning() && m.outputRetries >= maxOutputRetries {
			placeholder = "  no output recorded for this job"
		}
		b.WriteString(dimStyle.Render(placeholder))
		b.WriteString(strings.Repeat("\n", max(0, m.outputHeight()-1)))
	} else {
		b.WriteString(m.vp.View())
	}
	b.WriteString("\n")
	b.WriteString(m.rule())
	b.WriteString("\n")
	b.WriteString(m.outputFooter())
	return b.String()
}

// outputKeys is the legend of the output view. find, follow and unpin
// describe the state this view is already in rather than something waiting to
// be done, so they are marked as in force.
func (m Model) outputKeys() []legend {
	keys := []legend{{key: "/", desc: "find", on: m.osearch.query != ""}}
	if m.osearch.query != "" {
		keys = append(keys, legend{key: "n/N", desc: "next/prev"})
	}
	pin := m.outputPinLabel()
	keys = append(keys,
		legend{key: "]/[", desc: "fail"},
		legend{key: "t/T", desc: "task"},
		legend{key: "f", desc: "follow", on: m.follow},
		legend{key: "p", desc: pin, on: pin == "unpin"},
		legend{key: "d", desc: "details"},
	)
	if m.outputJob.IsRunning() {
		keys = append(keys, legend{key: "c", desc: "cancel"})
	}
	return append(keys, legend{key: "esc", desc: "back"})
}

// outputFooter shows the search box when searching, otherwise the keys.
func (m Model) outputFooter() string {
	if m.osearch.editing {
		return m.spread(m.osearch.input.View(), dimStyle.Render(m.matchLabel()))
	}
	right := dimStyle.Render(fmt.Sprintf("%3.0f%%", m.vp.ScrollPercent()*100))
	if m.osearch.query != "" {
		right = dimStyle.Render(m.matchLabel()+"  ") + right
	}
	return m.statusOrKeys(keyLegend(m.outputKeys()), right)
}

// matchLabel summarises the search, e.g. "3/17 for failed".
func (m Model) matchLabel() string {
	if strings.TrimSpace(m.osearch.query) == "" {
		return ""
	}
	if len(m.osearch.matches) == 0 {
		return "no matches"
	}
	return fmt.Sprintf("%d/%d", m.osearch.current+1, len(m.osearch.matches))
}
