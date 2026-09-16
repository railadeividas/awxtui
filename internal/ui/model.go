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
	"github.com/railadeividas/awxtui/internal/state"
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
	modeError
	modeInstances
	modeProject
	modeShow
)

const (
	pollInterval    = 2 * time.Second
	jobsAutoRefresh = 5 * time.Second
	// maxOutputPages bounds how many event pages one refresh burst pulls.
	maxOutputPages = 50
	// maxOutputRetries re-reads the output of a finished job that still looks
	// empty, since AWX can lag behind the job finishing.
	maxOutputRetries = 10
	// maxOutputBytes bounds how much job output is kept in memory. A verbose
	// play across thousands of hosts can run to hundreds of megabytes, and
	// every poll re-wraps whatever is held.
	maxOutputBytes = 2 << 20
	// maxPages bounds how many pages of any list are pulled, so a huge
	// instance cannot be walked forever.
	maxPages = 25
	// loadMoreWithin triggers the next page once the cursor comes this close
	// to the end of what is loaded.
	loadMoreWithin = 10
	// searchDelay is how long typing must settle before AWX is queried.
	searchDelay = 250 * time.Millisecond
)

// Model is the whole application state.
type Model struct {
	client *awx.Client
	user   string
	// userID is what a "mine" filter is built from: AWX takes the numeric id
	// on created_by, and a rename would silently empty a filter on the name.
	userID   int
	instance string

	// show is what each tab is narrowed to, and panel is that choice being
	// edited; store holds the pins, which no AWX endpoint knows about.
	show  [tabCount]showFilter
	panel showPanel
	store *state.Store

	// instance switching
	instances      []InstanceInfo
	instanceCursor int
	connector      Connector
	// gen increments on every switch; replies tagged with an older
	// generation belong to the previous instance and are discarded.
	gen int

	width, height int
	ready         bool

	active tab
	mode   mode

	rows    [tabCount][]row
	cursor  [tabCount]int
	offset  [tabCount]int
	filters [tabCount]string
	loaded  [tabCount]bool

	// paging state per tab: the next page URL AWX gave us, the total it
	// reports, how many pages we pulled, and whether one is in flight.
	next     [tabCount]string
	count    [tabCount]int
	pages    [tabCount]int
	fetching [tabCount]bool

	// server-side search state: the query the rows currently reflect, a
	// sequence number so stale replies can be dropped, and whether a search
	// request is in flight.
	serverQuery [tabCount]string
	searchSeq   [tabCount]int
	searching   [tabCount]bool

	templates   []awx.JobTemplate
	jobs        []awx.Job
	inventories []awx.Inventory
	projects    []awx.Project

	filterInput textinput.Model
	spin        spinner.Model
	inflight    int

	// job output view
	vp            viewport.Model
	outputJob     awx.Job
	outputText    string
	outputLines   []string
	outputCursor  int
	osearch       outputSearch
	outputCounter int
	outputPages   int
	outputRetries int
	follow        bool

	// inventory drill-down
	hostTitle     string
	hostRows      []row
	hostCursor    int
	hostOffset    int
	hostInventory int
	hostNext      string
	hostCount     int
	hostPages     int

	// launch form for the selected template
	form form

	// details of the selected project
	project projectDetail

	// syncing is set while a sync POST is in flight, so a held-down key
	// cannot start the same update twice.
	syncing bool

	notice       string
	err          error
	lastJobsPull time.Time
}

// Option configures the model at construction.
type Option func(*Model)

// New builds the initial model.
func New(c *awx.Client, opts ...Option) Model {
	fi := textinput.New()
	fi.Prompt = "search "
	fi.Placeholder = "type to filter…"
	fi.PromptStyle = helpKeyStyle
	fi.TextStyle = inputStyle
	fi.Cursor.SetMode(cursor.CursorStatic)

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = helpKeyStyle

	m := Model{
		client:      c,
		mode:        modeList,
		filterInput: fi,
		spin:        sp,
		follow:      true,
		osearch:     newOutputSearch(),
		store:       state.Memory(),
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.connect, m.spin.Tick, tick(pollInterval))
}

// visible returns the rows of tab t after applying its filter. When the rows
// came from AWX for this exact query they are already the answer — filtering
// them again locally would hide matches found on fields we do not render.
func (m Model) visible(t tab) []row {
	f := strings.ToLower(strings.TrimSpace(m.filters[t]))
	if f == "" || strings.EqualFold(m.serverQuery[t], strings.TrimSpace(m.filters[t])) {
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

func (m *Model) moveCursor(delta int) tea.Cmd {
	if m.mode == modeHosts {
		n := len(m.hostRows)
		m.hostCursor = clamp(m.hostCursor+delta, 0, n-1)
		m.hostOffset = clampOffset(m.hostCursor, m.hostOffset, m.tableHeight(), n)
		return m.loadMoreHosts()
	}
	n := len(m.visible(m.active))
	m.cursor[m.active] = clamp(m.cursor[m.active]+delta, 0, n-1)
	m.offset[m.active] = clampOffset(m.cursor[m.active], m.offset[m.active], m.tableHeight(), n)
	return m.loadMore(m.active)
}

// loadMore pulls the next page once the cursor approaches the end of the rows
// already loaded.
func (m *Model) loadMore(t tab) tea.Cmd {
	if len(m.visible(t))-m.cursor[t] > loadMoreWithin {
		return nil
	}
	return m.nextPage(t)
}

// nextPage requests the following page of tab t, if there is one and we have
// not hit the page cap.
func (m *Model) nextPage(t tab) tea.Cmd {
	if m.next[t] == "" || m.fetching[t] || m.pages[t] >= maxPages {
		return nil
	}
	m.fetching[t] = true
	m.pages[t]++
	return m.fetch(t, m.next[t], m.serverQuery[t], true)
}

// continueLoad pulls another page when what is loaded would not even fill the
// screen. Everything else is paged in on demand as the user scrolls.
func (m *Model) continueLoad(t tab) tea.Cmd {
	if len(m.visible(t)) >= m.tableHeight() {
		return nil
	}
	return m.nextPage(t)
}

func (m *Model) loadMoreHosts() tea.Cmd {
	if m.hostNext == "" || m.hostPages >= maxPages ||
		len(m.hostRows)-m.hostCursor > loadMoreWithin {
		return nil
	}
	m.hostPages++
	next := m.hostNext
	m.hostNext = ""
	return m.fetchHosts(m.hostInventory, m.hostTitle, next, true)
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
	m.pages[t] = 1
	m.next[t] = ""
	return m.fetch(t, "", m.filters[t], false)
}

// runSearch asks AWX for the current filter text. Lists are paged in lazily,
// so searching locally would only ever see the pages already loaded.
func (m *Model) runSearch(t tab) tea.Cmd {
	query := strings.TrimSpace(m.filters[t])
	if query == m.serverQuery[t] {
		return nil
	}
	m.searching[t] = true
	m.pages[t] = 1
	m.next[t] = ""
	return m.fetch(t, "", query, false)
}

// searchable reports whether a filter needs the server: either the list is
// incomplete, or we are already showing server-filtered rows. A complete list
// with local matches is filtered in memory, which keeps typing instant.
func (m *Model) searchable(t tab) bool {
	return m.next[t] != "" || m.serverQuery[t] != ""
}

// selectedInventory resolves the highlighted row back to its inventory record.
func (m *Model) selectedInventory() (awx.Inventory, bool) {
	r, ok := m.selected()
	if !ok {
		return awx.Inventory{}, false
	}
	for _, inv := range m.inventories {
		if inv.ID == r.id {
			return inv, true
		}
	}
	return awx.Inventory{}, false
}

// startSync begins a project update or an inventory sync. Both are writes, so
// both are refused outright by a read-only client rather than sent and
// rejected by AWX.
func (m *Model) startSync(what string, cmd tea.Cmd) tea.Cmd {
	if m.client.IsReadOnly() {
		m.err = fmt.Errorf("read-only mode: syncing is disabled")
		return nil
	}
	if m.syncing {
		return nil
	}
	m.err = nil
	m.notice = "syncing " + what + "…"
	m.syncing = true
	m.inflight++
	return cmd
}

func (m *Model) openOutput(job awx.Job) tea.Cmd {
	m.mode = modeOutput
	m.outputJob = job
	m.outputText = ""
	m.outputCounter = 0
	m.outputPages = 0
	m.outputRetries = 0
	m.follow = true
	m.outputCursor = 0
	m.osearch.query = ""
	m.osearch.editing = false
	m.osearch.matches = nil
	m.osearch.input.SetValue("")
	m.vp = viewport.New(m.width, m.outputHeight())
	return m.fetchOutput(job.Resource(), job.ID, 0)
}

func (m Model) outputHeight() int {
	// title line, rule, rule, footer
	h := m.height - 4
	if h < 3 {
		h = 3
	}
	return h
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
			cmds = append(cmds, m.fetchOutput(m.outputJob.Resource(), m.outputJob.ID, m.outputCounter))
		} else if m.mode == modeOutput && m.outputText == "" && m.outputRetries < maxOutputRetries {
			// A job that has just finished can report no output for a few
			// seconds while AWX finishes processing its events.
			m.outputRetries++
			cmds = append(cmds, m.fetchOutput(m.outputJob.Resource(), m.outputJob.ID, 0))
		}
		if m.mode == modeList && m.active == tabJobs && time.Since(m.lastJobsPull) >= jobsAutoRefresh {
			m.lastJobsPull = time.Now()
			cmds = append(cmds, m.fetch(tabJobs, "", m.serverQuery[tabJobs], false))
		}
		return m, tea.Batch(cmds...)

	case searchTickMsg:
		if msg.seq != m.searchSeq[msg.tab] {
			return m, nil // the user kept typing
		}
		return m, m.runSearch(msg.tab)

	case connectedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.user, m.userID = msg.user.Username, msg.user.ID
		return m, m.load(tabTemplates, true)

	case templatesMsg:
		m.inflight = max(0, m.inflight-1)
		if msg.gen != m.gen || msg.seq != m.searchSeq[tabTemplates] {
			return m, nil // a newer search, or another instance, has replaced it
		}
		m.fetching[tabTemplates], m.searching[tabTemplates] = false, false
		if msg.cont {
			m.templates = append(m.templates, msg.items...)
		} else {
			m.templates = msg.items
			m.serverQuery[tabTemplates] = msg.query
		}
		m.rows[tabTemplates] = m.templateRows(m.templates)
		m.next[tabTemplates] = msg.next
		m.count[tabTemplates] = msg.count
		m.loaded[tabTemplates] = true
		m.clampAll()
		return m, m.continueLoad(tabTemplates)

	case jobsMsg:
		m.inflight = max(0, m.inflight-1)
		if msg.gen != m.gen || msg.seq != m.searchSeq[tabJobs] {
			return m, nil // a newer search, or another instance, has replaced it
		}
		m.fetching[tabJobs], m.searching[tabJobs] = false, false
		// A background refresh re-reads page one; merge it so the pages the
		// user already scrolled through are not thrown away.
		merged := false
		switch {
		case msg.cont:
			m.jobs = append(m.jobs, msg.items...)
		case msg.query != m.serverQuery[tabJobs]:
			m.jobs = msg.items
			m.serverQuery[tabJobs] = msg.query
		case m.pages[tabJobs] > 1 && len(m.jobs) > 0:
			m.jobs = mergeJobs(m.jobs, msg.items)
			merged = true
		default:
			m.jobs = msg.items
		}
		m.rows[tabJobs] = m.jobRows(m.jobs)
		if !merged {
			m.next[tabJobs] = msg.next
		}
		m.count[tabJobs] = msg.count
		m.loaded[tabJobs] = true
		m.lastJobsPull = time.Now()
		m.clampAll()
		return m, m.continueLoad(tabJobs)

	case inventoriesMsg:
		m.inflight = max(0, m.inflight-1)
		if msg.gen != m.gen || msg.seq != m.searchSeq[tabInventories] {
			return m, nil // a newer search, or another instance, has replaced it
		}
		m.fetching[tabInventories], m.searching[tabInventories] = false, false
		if msg.cont {
			m.inventories = append(m.inventories, msg.items...)
		} else {
			m.inventories = msg.items
			m.serverQuery[tabInventories] = msg.query
		}
		m.rows[tabInventories] = m.inventoryRows(m.inventories)
		m.next[tabInventories] = msg.next
		m.count[tabInventories] = msg.count
		m.loaded[tabInventories] = true
		m.clampAll()
		return m, m.continueLoad(tabInventories)

	case projectsMsg:
		m.inflight = max(0, m.inflight-1)
		if msg.gen != m.gen || msg.seq != m.searchSeq[tabProjects] {
			return m, nil // a newer search, or another instance, has replaced it
		}
		m.fetching[tabProjects], m.searching[tabProjects] = false, false
		if msg.cont {
			m.projects = append(m.projects, msg.items...)
		} else {
			m.projects = msg.items
			m.serverQuery[tabProjects] = msg.query
		}
		m.rows[tabProjects] = m.projectRows(m.projects)
		m.next[tabProjects] = msg.next
		m.count[tabProjects] = msg.count
		m.loaded[tabProjects] = true
		m.clampAll()
		return m, m.continueLoad(tabProjects)

	case playbooksMsg:
		m.inflight = max(0, m.inflight-1)
		// A reply for a project no longer on screen belongs to a details view
		// that has since been closed or replaced.
		if msg.gen != m.gen || m.mode != modeProject || msg.projectID != m.project.project.ID {
			return m, nil
		}
		m.project.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.project.playbooks = msg.names
		return m, nil

	case hostsMsg:
		m.inflight = max(0, m.inflight-1)
		if msg.gen != m.gen {
			return m, nil
		}
		m.mode = modeHosts
		m.hostTitle = msg.inventory
		m.hostInventory = msg.inventoryID
		m.hostNext = msg.next
		m.hostCount = msg.count
		if msg.cont {
			m.hostRows = append(m.hostRows, hostRows(msg.hosts)...)
		} else {
			m.hostRows = hostRows(msg.hosts)
			m.hostCursor, m.hostOffset = 0, 0
			m.hostPages = 1
		}
		return m, nil

	case outputMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.outputJob = msg.job
		text := m.outputText + msg.chunk
		if msg.reset {
			text = msg.chunk
			m.outputPages = 0
		}
		m.outputCounter = msg.counter
		m.setOutput(trimOutput(text))
		// Long jobs span many event pages; keep pulling until caught up.
		if msg.more && m.mode == modeOutput && m.outputPages < maxOutputPages {
			m.outputPages++
			return m, m.fetchOutput(m.outputJob.Resource(), m.outputJob.ID, m.outputCounter)
		}
		return m, nil

	case launchFormMsg:
		m.inflight = max(0, m.inflight-1)
		if msg.gen != m.gen {
			return m, nil
		}
		m.notice = ""
		m.form = newForm(msg, m.width)
		m.mode = modeLaunch
		return m, textinput.Blink

	case launchedMsg:
		m.inflight = max(0, m.inflight-1)
		if msg.gen != m.gen {
			return m, nil
		}
		m.notice = fmt.Sprintf("launched job #%d", msg.job.ID)
		m.loaded[tabJobs] = false
		cmd := m.openOutput(msg.job)
		return m, tea.Batch(cmd, m.fetch(tabJobs, "", m.serverQuery[tabJobs], false))

	case canceledMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.notice = fmt.Sprintf("cancel requested for job #%d", msg.id)
		return m, m.fetch(tabJobs, "", m.serverQuery[tabJobs], false)

	case syncedMsg:
		m.inflight = max(0, m.inflight-1)
		m.syncing = false
		if msg.gen != m.gen || len(msg.started) == 0 {
			return m, nil
		}
		job := msg.started[0]
		m.notice = fmt.Sprintf("syncing %s (#%d)", msg.what, job.ID)
		if len(msg.started) > 1 {
			// Every source was started; only one output can be on screen.
			m.notice = fmt.Sprintf("syncing %s: %d sources, showing #%d",
				msg.what, len(msg.started), job.ID)
		}
		// The list the sync came from now has a running update on it.
		return m, tea.Batch(m.openOutput(job), m.fetch(m.active, "", m.serverQuery[m.active], false))

	case errMsg:
		m.inflight = max(0, m.inflight-1)
		if msg.gen != m.gen {
			return m, nil
		}
		m.syncing = false
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
			if m.serverQuery[m.active] != "" {
				m.searchSeq[m.active]++
				return m, m.runSearch(m.active)
			}
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
		// Ask AWX when the list is incomplete, and also when a complete list
		// matches nothing locally: the server searches fields we do not show,
		// such as descriptions.
		if m.searchable(m.active) || len(m.visible(m.active)) == 0 {
			m.searchSeq[m.active]++
			return m, tea.Batch(cmd, searchDebounce(m.active, m.searchSeq[m.active]))
		}
		return m, tea.Batch(cmd, m.continueLoad(m.active))

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

	case modeHelp, modeError:
		m.mode = modeList
		return m, nil

	case modeInstances:
		return m.handleInstancesKey(msg)

	case modeProject:
		return m.handleProjectKey(msg)

	case modeShow:
		return m.handleShowKey(msg)

	case modeOutput:
		return m.handleOutputKey(msg)

	case modeHosts:
		switch key {
		case "q", "esc":
			m.mode = modeList
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			return m, m.moveCursor(-1)
		case "down", "j":
			return m, m.moveCursor(1)
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
	case "e":
		if m.err != nil {
			m.mode = modeError
		}
		return m, nil
	case "i":
		m.openInstances()
		return m, nil
	case "up", "k":
		return m, m.moveCursor(-1)
	case "down", "j":
		return m, m.moveCursor(1)
	case "pgup", "ctrl+u":
		return m, m.moveCursor(-m.tableHeight() / 2)
	case "pgdown", "ctrl+d":
		return m, m.moveCursor(m.tableHeight() / 2)
	case "home", "g":
		return m, m.moveCursor(-1 << 30)
	case "end", "G":
		return m, m.moveCursor(1 << 30)
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
			if m.serverQuery[m.active] != "" {
				m.err, m.notice = nil, ""
				m.searchSeq[m.active]++
				return m, m.runSearch(m.active)
			}
		}
		m.err, m.notice = nil, ""
	case "r":
		m.err = nil
		m.notice = "refreshing…"
		return m, m.load(m.active, true)
	case "enter":
		return m.activate()
	case "s":
		return m.sync()
	case "f":
		m.openShowPanel()
		return m, nil
	case "p":
		return m, m.togglePinSelected()
	case "c":
		if m.active == tabJobs {
			if j, ok := m.selectedJob(); ok && j.IsRunning() {
				if m.client.IsReadOnly() {
					m.err = fmt.Errorf("read-only mode: cancelling is disabled")
					return m, nil
				}
				return m, m.cancelJob(j.Resource(), j.ID)
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
		// The unified list can hold kinds awxtui has no output endpoint for.
		// Saying so beats fetching /api/v2/jobs/<id>/stdout/ for a workflow
		// job and reporting whatever 404 comes back.
		if !j.Supported() {
			m.err = fmt.Errorf("#%d is a %s; awxtui cannot show its output yet",
				j.ID, strings.ReplaceAll(j.Type, "_", " "))
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
		return m, m.fetchHosts(r.id, name, "", false)

	case tabProjects:
		p, ok := m.selectedProject()
		if !ok {
			return m, nil
		}
		return m, m.openProject(p)
	}
	return m, nil
}

// sync handles the sync key for the current tab: an SCM update for the
// selected project, a source sync for the selected inventory. The other tabs
// have nothing to sync.
func (m Model) sync() (tea.Model, tea.Cmd) {
	switch m.active {
	case tabProjects:
		p, ok := m.selectedProject()
		if !ok {
			return m, nil
		}
		return m, m.startSync("project "+p.Name, m.syncProject(p))

	case tabInventories:
		inv, ok := m.selectedInventory()
		if !ok {
			return m, nil
		}
		// AWX would answer the same way, but saying it here saves a request
		// and names the reason the row shows no sources.
		if inv.TotalInventorySources == 0 {
			m.err = fmt.Errorf("inventory %s has no sources to sync", inv.Name)
			return m, nil
		}
		return m, m.startSync("inventory "+inv.Name, m.syncInventory(inv))
	}
	return m, nil
}

// trimOutput keeps the tail of very long output, so that memory and the cost
// of re-wrapping stay bounded no matter how verbose a job is.
func trimOutput(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	cut := len(s) - maxOutputBytes
	if i := strings.IndexByte(s[cut:], '\n'); i >= 0 {
		cut += i + 1
	}
	return outputTrimmedNotice + s[cut:]
}

const outputTrimmedNotice = "… earlier output trimmed; press r to reload from the start …\n"

// mergeJobs folds a freshly fetched first page into an already-paged list:
// existing entries are updated in place and genuinely new jobs are prepended.
func mergeJobs(existing, fresh []awx.Job) []awx.Job {
	byID := make(map[int]int, len(existing))
	for i, j := range existing {
		byID[j.ID] = i
	}
	var added []awx.Job
	for _, j := range fresh {
		if i, ok := byID[j.ID]; ok {
			existing[i] = j
			continue
		}
		added = append(added, j)
	}
	return append(added, existing...)
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
