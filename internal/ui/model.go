package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

type tab int

const (
	tabTemplates tab = iota
	tabJobs
	tabInventories
	tabProjects
	tabCount
)

var tabNames = [tabCount]string{"Templates", "Jobs", "Inventories", "Projects"}

type mode int

const (
	modeList mode = iota
	modeFilter
	modeOutput
	modeLaunch
	modeHosts
	modeHelp
)

const (
	pollInterval    = 2 * time.Second
	jobsAutoRefresh = 5 * time.Second
	// maxOutputPages bounds how many event pages one refresh burst pulls.
	maxOutputPages = 50
)

// Model is the whole application state.
type Model struct {
	client *awx.Client
	user   string

	width, height int
	ready         bool

	active tab
	mode   mode

	rows    [tabCount][]row
	cursor  [tabCount]int
	offset  [tabCount]int
	filters [tabCount]string
	loaded  [tabCount]bool

	templates []awx.JobTemplate
	jobs      []awx.Job

	filterInput textinput.Model
	spin        spinner.Model
	inflight    int

	// job output view
	vp            viewport.Model
	outputJob     awx.Job
	outputText    string
	outputCounter int
	outputPages   int
	follow        bool

	// inventory drill-down
	hostTitle  string
	hostRows   []row
	hostCursor int
	hostOffset int

	// launch form for the selected template
	form form

	notice       string
	err          error
	lastJobsPull time.Time
}

// New builds the initial model.
func New(c *awx.Client) Model {
	fi := textinput.New()
	fi.Prompt = "search "
	fi.Placeholder = "type to filter…"
	fi.PromptStyle = helpKeyStyle
	fi.TextStyle = inputStyle
	fi.Cursor.SetMode(cursor.CursorStatic)

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = helpKeyStyle

	return Model{
		client:      c,
		mode:        modeList,
		filterInput: fi,
		spin:        sp,
		follow:      true,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.connect, m.spin.Tick, tick(pollInterval))
}

// visible returns the rows of tab t after applying its filter.
func (m Model) visible(t tab) []row {
	f := strings.ToLower(strings.TrimSpace(m.filters[t]))
	if f == "" {
		return m.rows[t]
	}
	out := make([]row, 0, len(m.rows[t]))
	for _, r := range m.rows[t] {
		if strings.Contains(r.search, f) {
			out = append(out, r)
		}
	}
	return out
}

func (m Model) tableHeight() int {
	// header line, tab line, rule, table header, rule, status line, filter line
	chrome := 7
	h := m.height - chrome
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) moveCursor(delta int) {
	if m.mode == modeHosts {
		n := len(m.hostRows)
		m.hostCursor = clamp(m.hostCursor+delta, 0, n-1)
		m.hostOffset = clampOffset(m.hostCursor, m.hostOffset, m.tableHeight(), n)
		return
	}
	n := len(m.visible(m.active))
	m.cursor[m.active] = clamp(m.cursor[m.active]+delta, 0, n-1)
	m.offset[m.active] = clampOffset(m.cursor[m.active], m.offset[m.active], m.tableHeight(), n)
}

func (m *Model) selected() (row, bool) {
	rows := m.visible(m.active)
	i := m.cursor[m.active]
	if i < 0 || i >= len(rows) {
		return row{}, false
	}
	return rows[i], true
}

func (m *Model) selectedJob() (awx.Job, bool) {
	r, ok := m.selected()
	if !ok {
		return awx.Job{}, false
	}
	for _, j := range m.jobs {
		if j.ID == r.id {
			return j, true
		}
	}
	return awx.Job{}, false
}

func (m *Model) selectedTemplate() (awx.JobTemplate, bool) {
	r, ok := m.selected()
	if !ok {
		return awx.JobTemplate{}, false
	}
	for _, t := range m.templates {
		if t.ID == r.id {
			return t, true
		}
	}
	return awx.JobTemplate{}, false
}

func (m *Model) load(t tab, force bool) tea.Cmd {
	if m.loaded[t] && !force {
		return nil
	}
	m.inflight++
	return m.fetch(t)
}

func (m *Model) openOutput(job awx.Job) tea.Cmd {
	m.mode = modeOutput
	m.outputJob = job
	m.outputText = ""
	m.outputCounter = 0
	m.outputPages = 0
	m.follow = true
	m.vp = viewport.New(m.width, m.outputHeight())
	return m.fetchOutput(job.ID, 0)
}

func (m Model) outputHeight() int {
	h := m.height - 6
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) setOutput(text string) {
	m.outputText = text
	m.vp.Width = m.width
	m.vp.Height = m.outputHeight()
	m.vp.SetContent(wrapANSI(text, m.width))
	if m.follow {
		m.vp.GotoBottom()
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.filterInput.Width = max(10, m.width-12)
		if m.mode == modeOutput {
			m.setOutput(m.outputText)
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tickMsg:
		cmds := []tea.Cmd{tick(pollInterval)}
		if m.mode == modeOutput && m.outputJob.IsRunning() {
			cmds = append(cmds, m.fetchOutput(m.outputJob.ID, m.outputCounter))
		}
		if m.mode == modeList && m.active == tabJobs && time.Since(m.lastJobsPull) >= jobsAutoRefresh {
			m.lastJobsPull = time.Now()
			cmds = append(cmds, m.fetch(tabJobs))
		}
		return m, tea.Batch(cmds...)

	case connectedMsg:
		m.user = msg.user
		return m, m.load(tabTemplates, true)

	case templatesMsg:
		m.inflight = max(0, m.inflight-1)
		m.templates = msg
		m.rows[tabTemplates] = templateRows(msg)
		m.loaded[tabTemplates] = true
		m.clampAll()
		return m, nil

	case jobsMsg:
		m.inflight = max(0, m.inflight-1)
		m.jobs = msg
		m.rows[tabJobs] = jobRows(msg)
		m.loaded[tabJobs] = true
		m.lastJobsPull = time.Now()
		m.clampAll()
		return m, nil

	case inventoriesMsg:
		m.inflight = max(0, m.inflight-1)
		m.rows[tabInventories] = inventoryRows(msg)
		m.loaded[tabInventories] = true
		m.clampAll()
		return m, nil

	case projectsMsg:
		m.inflight = max(0, m.inflight-1)
		m.rows[tabProjects] = projectRows(msg)
		m.loaded[tabProjects] = true
		m.clampAll()
		return m, nil

	case hostsMsg:
		m.inflight = max(0, m.inflight-1)
		m.mode = modeHosts
		m.hostTitle = msg.inventory
		m.hostRows = hostRows(msg.hosts)
		m.hostCursor, m.hostOffset = 0, 0
		return m, nil

	case outputMsg:
		m.outputJob = msg.job
		text := m.outputText + msg.chunk
		if msg.reset {
			text = msg.chunk
			m.outputPages = 0
		}
		m.outputCounter = msg.counter
		m.setOutput(text)
		// Long jobs span many event pages; keep pulling until caught up.
		if msg.more && m.mode == modeOutput && m.outputPages < maxOutputPages {
			m.outputPages++
			return m, m.fetchOutput(m.outputJob.ID, m.outputCounter)
		}
		return m, nil

	case launchFormMsg:
		m.inflight = max(0, m.inflight-1)
		m.notice = ""
		m.form = newForm(msg.template, msg.config, msg.survey, msg.inventories, m.width)
		m.mode = modeLaunch
		return m, textinput.Blink

	case launchedMsg:
		m.inflight = max(0, m.inflight-1)
		m.notice = fmt.Sprintf("launched job #%d", msg.job.ID)
		m.loaded[tabJobs] = false
		cmd := m.openOutput(msg.job)
		return m, tea.Batch(cmd, m.fetch(tabJobs))

	case canceledMsg:
		m.notice = fmt.Sprintf("cancel requested for job #%d", msg.id)
		return m, m.fetch(tabJobs)

	case errMsg:
		m.inflight = max(0, m.inflight-1)
		m.err = msg.err
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) clampAll() {
	for t := tab(0); t < tabCount; t++ {
		n := len(m.visible(t))
		m.cursor[t] = clamp(m.cursor[t], 0, n-1)
		m.offset[t] = clampOffset(m.cursor[t], m.offset[t], m.tableHeight(), n)
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Modal-ish modes get first refusal on keys.
	switch m.mode {
	case modeFilter:
		switch key {
		case "esc":
			m.filters[m.active] = ""
			m.filterInput.SetValue("")
			m.filterInput.Blur()
			m.mode = modeList
			m.clampAll()
			return m, nil
		case "enter":
			m.filterInput.Blur()
			m.mode = modeList
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.filters[m.active] = m.filterInput.Value()
		m.cursor[m.active], m.offset[m.active] = 0, 0
		return m, cmd

	case modeLaunch:
		switch key {
		case "esc":
			m.mode = modeList
			m.form = form{}
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
		submit, cmd := m.form.update(msg)
		if !submit {
			return m, cmd
		}
		if err := m.form.validate(); err != nil {
			m.form.problem = err.Error()
			return m, nil
		}
		payload, err := m.form.payload()
		if err != nil {
			m.form.problem = err.Error()
			return m, nil
		}
		// The form is all GETs, so it opens in read-only mode; launching is
		// where we stop.
		if m.client.IsReadOnly() {
			m.form.problem = "read-only mode: launching is disabled"
			return m, nil
		}
		id := m.form.template.ID
		m.mode = modeList
		m.form = form{}
		m.err = nil
		m.inflight++
		return m, m.launch(id, payload)

	case modeHelp:
		m.mode = modeList
		return m, nil

	case modeOutput:
		switch key {
		case "q", "esc":
			m.mode = modeList
			return m, tea.Batch(m.fetch(tabJobs))
		case "ctrl+c":
			return m, tea.Quit
		case "f":
			m.follow = !m.follow
			if m.follow {
				m.vp.GotoBottom()
			}
			return m, nil
		case "r":
			m.err = nil
			return m, m.fetchOutput(m.outputJob.ID, 0)
		case "c":
			if m.outputJob.IsRunning() {
				if m.client.IsReadOnly() {
					m.err = fmt.Errorf("read-only mode: cancelling is disabled")
					return m, nil
				}
				return m, m.cancelJob(m.outputJob.ID)
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

	case modeHosts:
		switch key {
		case "q", "esc":
			m.mode = modeList
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.moveCursor(-1)
			return m, nil
		case "down", "j":
			m.moveCursor(1)
			return m, nil
		case "?":
			m.mode = modeHelp
			return m, nil
		}
		return m, nil
	}

	// modeList
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.mode = modeHelp
		return m, nil
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "pgup", "ctrl+u":
		m.moveCursor(-m.tableHeight() / 2)
	case "pgdown", "ctrl+d":
		m.moveCursor(m.tableHeight() / 2)
	case "home", "g":
		m.moveCursor(-1 << 30)
	case "end", "G":
		m.moveCursor(1 << 30)
	case "tab", "right", "l":
		m.active = (m.active + 1) % tabCount
		return m, m.afterTabSwitch()
	case "shift+tab", "left", "h":
		m.active = (m.active + tabCount - 1) % tabCount
		return m, m.afterTabSwitch()
	case "1", "2", "3", "4":
		m.active = tab(key[0] - '1')
		return m, m.afterTabSwitch()
	case "/":
		m.mode = modeFilter
		m.filterInput.SetValue(m.filters[m.active])
		m.filterInput.CursorEnd()
		m.filterInput.Focus()
		return m, textinput.Blink
	case "esc":
		if m.filters[m.active] != "" {
			m.filters[m.active] = ""
			m.clampAll()
		}
		m.err, m.notice = nil, ""
	case "r":
		m.err = nil
		m.notice = "refreshing…"
		return m, m.load(m.active, true)
	case "enter":
		return m.activate()
	case "c":
		if m.active == tabJobs {
			if j, ok := m.selectedJob(); ok && j.IsRunning() {
				if m.client.IsReadOnly() {
					m.err = fmt.Errorf("read-only mode: cancelling is disabled")
					return m, nil
				}
				return m, m.cancelJob(j.ID)
			}
		}
	}
	return m, nil
}

func (m *Model) afterTabSwitch() tea.Cmd {
	m.err, m.notice = nil, ""
	m.filterInput.SetValue(m.filters[m.active])
	return m.load(m.active, false)
}

// activate handles Enter for the current tab.
func (m Model) activate() (tea.Model, tea.Cmd) {
	switch m.active {
	case tabTemplates:
		t, ok := m.selectedTemplate()
		if !ok {
			return m, nil
		}
		m.err, m.notice = nil, "reading launch options…"
		m.inflight++
		return m, m.fetchLaunchForm(t)

	case tabJobs:
		j, ok := m.selectedJob()
		if !ok {
			return m, nil
		}
		cmd := m.openOutput(j)
		return m, cmd

	case tabInventories:
		r, ok := m.selected()
		if !ok {
			return m, nil
		}
		m.inflight++
		name := stripANSI(r.cells[0])
		return m, m.fetchHosts(r.id, name)

	case tabProjects:
		r, ok := m.selected()
		if ok {
			m.notice = fmt.Sprintf("project #%d — %s", r.id, stripANSI(r.cells[0]))
		}
	}
	return m, nil
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
