package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// InstanceInfo describes a configured AWX, for the instance switcher.
type InstanceInfo struct {
	Name     string
	URL      string
	ReadOnly bool
}

// Connector builds a client for a named instance. It may fail, for example
// when a token_command does not work.
type Connector func(name string) (*awx.Client, error)

// WithVersion supplies the application version shown in the UI header.
func WithVersion(version string) Option {
	return func(m *Model) {
		if version != "" {
			m.version = version
		}
	}
}

// WithInstances supplies the instances that can be switched to and the one
// currently connected.
func WithInstances(list []InstanceInfo, current string) Option {
	return func(m *Model) {
		m.instances = list
		m.instance = current
		for i, inst := range list {
			if inst.Name == current {
				m.instanceCursor = i
			}
		}
	}
}

// WithConnector lets the UI reconnect to a different instance.
func WithConnector(c Connector) Option {
	return func(m *Model) { m.connector = c }
}

// openInstances shows the switcher, if there is anything to switch to.
func (m *Model) openInstances() {
	if m.connector == nil || len(m.instances) < 2 {
		m.err = fmt.Errorf("only one instance is configured; add more to the config file")
		return
	}
	m.err, m.notice = nil, ""
	for i, inst := range m.instances {
		if inst.Name == m.instance {
			m.instanceCursor = i
		}
	}
	m.mode = modeInstances
}

// switchInstance reconnects to another AWX and clears everything belonging to
// the previous one. Replies already in flight are ignored: every request
// carries the generation it was issued in.
func (m *Model) switchInstance(name string) tea.Cmd {
	client, err := m.connector(name)
	if err != nil {
		m.mode = modeList
		m.err = err
		return nil
	}

	// The store outlives the switch. It is keyed by instance name, so one
	// store holds both histories; building a fresh model without it would
	// quietly demote pins to memory-only for the rest of the session.
	fresh := New(client,
		WithVersion(m.version),
		WithInstances(m.instances, name),
		WithConnector(m.connector),
		WithStore(m.store),
	)
	fresh.width, fresh.height, fresh.ready = m.width, m.height, m.ready
	fresh.spin = m.spin
	fresh.gen = m.gen + 1
	fresh.notice = "connected to " + name

	*m = fresh
	// The spinner's ticker is still running from Init; starting another here
	// would leave two of them racing.
	return m.connect
}

func (m Model) handleInstancesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q", "i":
		m.mode = modeList
		return m, nil
	case "up", "k":
		m.instanceCursor = clamp(m.instanceCursor-1, 0, len(m.instances)-1)
		return m, nil
	case "down", "j":
		m.instanceCursor = clamp(m.instanceCursor+1, 0, len(m.instances)-1)
		return m, nil
	case "enter":
		if m.instanceCursor < 0 || m.instanceCursor >= len(m.instances) {
			return m, nil
		}
		name := m.instances[m.instanceCursor].Name
		if name == m.instance {
			m.mode = modeList
			return m, nil
		}
		cmd := m.switchInstance(name)
		return m, cmd
	}
	// Number keys pick an instance directly.
	if n := msg.String(); len(n) == 1 && n[0] >= '1' && n[0] <= '9' {
		if i := int(n[0] - '1'); i < len(m.instances) {
			m.instanceCursor = i
		}
	}
	return m, nil
}

// instancesModal renders the switcher.
func (m Model) instancesModal() string {
	width := min(m.width-8, 72)
	var b strings.Builder
	b.WriteString(titleStyle.Render("Switch instance"))
	b.WriteString("\n\n")

	nameW := 0
	for _, inst := range m.instances {
		if len(inst.Name) > nameW {
			nameW = len(inst.Name)
		}
	}
	for i, inst := range m.instances {
		prefix, name := " ", dimStyle.Render(cell(inst.Name, nameW))
		if i == m.instanceCursor {
			prefix = helpKeyStyle.Render("▌")
			name = rowStyle.Bold(true).Render(cell(inst.Name, nameW))
		}
		host := strings.TrimPrefix(strings.TrimPrefix(inst.URL, "https://"), "http://")
		line := fmt.Sprintf("%s%s %s  %s", prefix, dimStyle.Render(fmt.Sprintf("%d", i+1)),
			name, metaStyle.Render(host))
		if inst.ReadOnly {
			line += warnStyle.Render("  read-only")
		}
		if inst.Name == m.instance {
			line += okStyle.Render("  ● connected")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return modalStyle.Width(width).Render(b.String())
}
