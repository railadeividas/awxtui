package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

type fieldKind int

const (
	fText fieldKind = iota
	fTextarea
	fPassword
	fChoice
	fMultiChoice
	fInt
	fFloat
)

// section groups fields under a heading in the rendered form.
type section string

const (
	secPrompts   section = "Prompted values"
	secSurvey    section = "Survey"
	secPasswords section = "Credential passwords"
)

// formField is one editable value in the launch form. Survey fields end up in
// extra_vars; the rest are top-level launch payload keys.
type formField struct {
	key      string // launch payload key, or survey variable name
	label    string
	help     string
	kind     fieldKind
	section  section
	required bool
	survey   bool

	choices []string
	chosen  []bool   // fMultiChoice selections, parallel to choices
	idx     int      // fChoice selection / fMultiChoice highlight
	values  []string // fChoice payload values, parallel to choices

	// usePicker offers the full-screen picker on enter for an fChoice field
	// whose catalogue is too long to cycle one at a time with ←→ — a
	// credential or a module name, the same reason instance groups and
	// labels get one as an fMultiChoice.
	usePicker bool

	input textinput.Model
	area  textarea.Model

	min, max *int
}

func (f *formField) textual() bool {
	switch f.kind {
	case fText, fPassword, fInt, fFloat:
		return true
	}
	return false
}

// value returns the field's current value as text.
func (f *formField) value() string {
	switch {
	case f.kind == fTextarea:
		return f.area.Value()
	case f.kind == fChoice:
		if f.idx < len(f.values) {
			return f.values[f.idx]
		}
		if f.idx < len(f.choices) {
			return f.choices[f.idx]
		}
		return ""
	case f.kind == fMultiChoice:
		return strings.Join(f.selections(), ",")
	default:
		return f.input.Value()
	}
}

func (f *formField) selections() []string {
	var out []string
	for i, on := range f.chosen {
		if on && i < len(f.choices) {
			out = append(out, f.choices[i])
		}
	}
	return out
}

// selectedIDs returns the payload values of a multi-select as numbers, for the
// id catalogues (credentials, instance groups, labels). Order follows the
// catalogue, which is the order AWX will apply them in.
func (f *formField) selectedIDs() []int {
	out := []int{}
	for i, on := range f.chosen {
		if !on || i >= len(f.values) {
			continue
		}
		if n, err := strconv.Atoi(f.values[i]); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (f *formField) focus() tea.Cmd {
	switch f.kind {
	case fTextarea:
		return f.area.Focus()
	default:
		if f.textual() {
			return f.input.Focus()
		}
	}
	return nil
}

func (f *formField) blur() {
	switch f.kind {
	case fTextarea:
		f.area.Blur()
	default:
		if f.textual() {
			f.input.Blur()
		}
	}
}

// form is the launch dialog for one job template or workflow job template.
// isWorkflow says which of template/workflowTemplate holds the record being
// launched — a workflow has no playbook, project or credentials of its own,
// so the rest of the form only ever offers the strict subset of prompts a
// workflow can ask for.
type form struct {
	isWorkflow       bool
	isAdHoc          bool
	template         awx.JobTemplate
	workflowTemplate awx.WorkflowJobTemplate
	adHocInventory   awx.Inventory
	config           awx.LaunchConfig
	survey           awx.SurveySpec
	fields           []formField
	cursor           int
	offset           int
	problem          string
	width            int

	// pickOffset scrolls the full-list picker opened on the focused
	// fMultiChoice field; it is meaningless otherwise.
	pickOffset int

	// submitting is set once launch is submitted and cleared only when
	// launchedMsg or errMsg answers it, so the modal stays up showing a
	// "submitting…" message rather than flashing back to the list.
	submitting bool
}

func newInput(value, placeholder string, width int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(value)
	ti.Placeholder = placeholder
	ti.TextStyle = inputStyle
	ti.Width = width
	// A static cursor keeps the focused field obvious without repainting the
	// screen twice a second, which matters over SSH.
	ti.Cursor.SetMode(cursor.CursorStatic)
	return ti
}

// idChoice is one entry of a pick-many-by-id catalogue: credentials, instance
// groups or labels.
type idChoice struct {
	id    int
	label string
}

// idListField builds the multi-select for one of those catalogues, with the
// template's own values pre-selected. It reports ok=false when there is
// nothing to choose from, so the caller can leave the prompt out entirely
// rather than offer an empty list that would submit "none".
//
// A default that is missing from the catalogue is appended rather than
// dropped: the catalogue is capped at maxPages, and silently deselecting a
// credential the template needs would launch a job that cannot authenticate.
func idListField(key, label string, items []idChoice, defaults []awx.NamedRef) (formField, bool) {
	fl := formField{key: key, label: label, kind: fMultiChoice}
	byID := make(map[int]bool, len(items))
	for _, it := range items {
		byID[it.id] = true
	}
	for _, def := range defaults {
		if !byID[def.ID] {
			byID[def.ID] = true
			items = append(items, idChoice{id: def.ID, label: firstNonEmpty(def.Name, fmt.Sprintf("id %d", def.ID))})
		}
	}
	if len(items) == 0 {
		return fl, false
	}
	selected := make(map[int]bool, len(defaults))
	for _, id := range awx.IDs(defaults) {
		selected[id] = true
	}
	for _, it := range items {
		fl.choices = append(fl.choices, it.label)
		fl.values = append(fl.values, strconv.Itoa(it.id))
		fl.chosen = append(fl.chosen, selected[it.id])
	}
	return fl, true
}

// name is the record being launched, for the form's title.
func (f form) name() string {
	if f.isWorkflow {
		return f.workflowTemplate.Name
	}
	return f.template.Name
}

// id is the template id to launch, for the form's title.
func (f form) id() int {
	if f.isWorkflow {
		return f.workflowTemplate.ID
	}
	return f.template.ID
}

// launchID is the same id, named for where it is used: the id to POST
// /launch/ to.
func (f form) launchID() int { return f.id() }

// newForm builds the fields a template needs: the ask_*_on_launch prompts, the
// survey questions, and any credential passwords. A workflow's config never
// sets the flags a job template's node-specific prompts depend on — job type,
// verbosity, credentials, execution environment, forks, slices, timeout — so
// those fields are simply never added.
func newForm(src launchFormMsg, width int) form {
	cfg, spec, inventories := src.config, src.survey, src.inventories
	f := form{
		isWorkflow:       src.isWorkflow,
		template:         src.template,
		workflowTemplate: src.workflowTemplate,
		config:           cfg,
		survey:           spec,
		width:            width,
	}
	d := cfg.Defaults
	inputWidth := min(max(width-34, 16), 60)

	add := func(fl formField) {
		if fl.section == "" {
			fl.section = secPrompts
		}
		f.fields = append(f.fields, fl)
	}
	text := func(key, label, value, placeholder string) formField {
		return formField{key: key, label: label, kind: fText, input: newInput(value, placeholder, inputWidth)}
	}
	number := func(key, label string, value int) formField {
		return formField{key: key, label: label, kind: fInt, input: newInput(strconv.Itoa(value), "0", 12)}
	}

	if cfg.AskInventory {
		fl := formField{key: "inventory", label: "Inventory", kind: fChoice, required: cfg.InventoryNeededToStart}
		for _, inv := range inventories {
			fl.choices = append(fl.choices, inv.Name)
			fl.values = append(fl.values, strconv.Itoa(inv.ID))
		}
		if len(fl.choices) == 0 {
			// No inventory list available: fall back to typing an id.
			fl = text("inventory", "Inventory id", strconv.Itoa(d.Inventory.ID), "id")
			fl.required = cfg.InventoryNeededToStart
		} else {
			for i, v := range fl.values {
				if v == strconv.Itoa(d.Inventory.ID) {
					fl.idx = i
				}
			}
		}
		add(fl)
	}
	if cfg.AskCredentials {
		items := make([]idChoice, 0, len(src.credentials))
		for _, c := range src.credentials {
			label := c.Name
			if ty := c.TypeName(); ty != "" {
				label += " (" + ty + ")"
			}
			items = append(items, idChoice{id: c.ID, label: label})
		}
		if fl, ok := idListField("credentials", "Credentials", items, d.Credentials); ok {
			// credential_needed_to_start means the job cannot run with none.
			fl.required = cfg.CredentialNeededToStart
			add(fl)
		}
	}
	if cfg.AskExecutionEnvironment && len(src.environments) > 0 {
		fl := formField{key: "execution_environment", label: "Execution env", kind: fChoice,
			// The first entry keeps the template's own image: AWX takes an id
			// here, so "no answer" has to be a real, unsendable choice.
			choices: []string{"template default"}, values: []string{""}}
		for _, ee := range src.environments {
			fl.choices = append(fl.choices, ee.Name)
			fl.values = append(fl.values, strconv.Itoa(ee.ID))
		}
		if def := d.ExecutionEnvironment.ID; def != 0 {
			for i, v := range fl.values {
				if v == strconv.Itoa(def) {
					fl.idx = i
				}
			}
		}
		add(fl)
	}
	if cfg.AskInstanceGroups {
		items := make([]idChoice, 0, len(src.instanceGroups))
		for _, g := range src.instanceGroups {
			label := g.Name
			if g.IsContainerGroup {
				label += " (container)"
			}
			items = append(items, idChoice{id: g.ID, label: label})
		}
		if fl, ok := idListField("instance_groups", "Instance groups", items, d.InstanceGroups); ok {
			add(fl)
		}
	}
	if cfg.AskLabels {
		items := make([]idChoice, 0, len(src.labels))
		for _, l := range src.labels {
			items = append(items, idChoice{id: l.ID, label: l.Name})
		}
		if fl, ok := idListField("labels", "Labels", items, d.Labels); ok {
			add(fl)
		}
	}
	if cfg.AskJobType {
		fl := formField{key: "job_type", label: "Job type", kind: fChoice,
			choices: []string{"run", "check"}, values: []string{"run", "check"}}
		if d.JobType == "check" {
			fl.idx = 1
		}
		add(fl)
	}
	if cfg.AskSCMBranch {
		add(text("scm_branch", "SCM branch", d.SCMBranch, "project default"))
	}
	if cfg.AskLimit {
		// The template's own default always wins: a host/group picked in the
		// members view only fills a blank limit, never overrides one the
		// template already set.
		def := d.Limit
		if def == "" {
			def = src.limit
		}
		add(text("limit", "Limit", def, "all hosts"))
	}
	if cfg.AskVerbosity {
		fl := formField{key: "verbosity", label: "Verbosity", kind: fChoice,
			choices: []string{"0 normal", "1 verbose", "2 more verbose", "3 debug", "4 connection debug"},
			values:  []string{"0", "1", "2", "3", "4"}}
		if d.Verbosity >= 0 && d.Verbosity < len(fl.values) {
			fl.idx = d.Verbosity
		}
		add(fl)
	}
	if cfg.AskTags {
		add(text("job_tags", "Job tags", d.JobTags, "none"))
	}
	if cfg.AskSkipTags {
		add(text("skip_tags", "Skip tags", d.SkipTags, "none"))
	}
	if cfg.AskDiffMode {
		fl := formField{key: "diff_mode", label: "Diff mode", kind: fChoice,
			choices: []string{"off", "on"}, values: []string{"false", "true"}}
		if d.DiffMode {
			fl.idx = 1
		}
		add(fl)
	}
	if cfg.AskForks {
		add(number("forks", "Forks", d.Forks))
	}
	if cfg.AskJobSliceCount {
		add(number("job_slice_count", "Job slices", d.JobSliceCount))
	}
	if cfg.AskTimeout {
		add(number("timeout", "Timeout (s)", d.Timeout))
	}
	if cfg.AskVariables {
		ta := textarea.New()
		ta.Placeholder = "key: value   (YAML or JSON)"
		ta.SetWidth(min(max(width-30, 24), 64))
		ta.SetHeight(5)
		ta.ShowLineNumbers = false
		ta.Cursor.SetMode(cursor.CursorStatic)
		if v := strings.TrimSpace(d.ExtraVars); v != "" && v != "{}" && v != "---" {
			ta.SetValue(v)
		}
		add(formField{key: "extra_vars", label: "Extra vars", kind: fTextarea, area: ta,
			help: "YAML or JSON mapping"})
	}

	for _, q := range spec.Spec {
		fl := formField{
			key:      q.Variable,
			label:    firstNonEmpty(q.QuestionName, q.Variable),
			help:     q.QuestionDescription,
			section:  secSurvey,
			required: q.Required,
			survey:   true,
			min:      q.Min,
			max:      q.Max,
		}
		switch q.Type {
		case "multiplechoice":
			fl.kind = fChoice
			fl.choices = q.ChoiceList()
			fl.values = fl.choices
			if !fl.required {
				// AWX allows leaving an optional choice unanswered.
				fl.choices = append([]string{"—"}, fl.choices...)
				fl.values = append([]string{""}, fl.values...)
			}
			for i, c := range fl.values {
				if c == q.DefaultString() && c != "" {
					fl.idx = i
				}
			}
		case "multiselect":
			fl.kind = fMultiChoice
			fl.choices = q.ChoiceList()
			fl.values = fl.choices
			fl.chosen = make([]bool, len(fl.choices))
			for _, def := range q.DefaultList() {
				for i, c := range fl.choices {
					if c == def {
						fl.chosen[i] = true
					}
				}
			}
		case "textarea":
			fl.kind = fTextarea
			ta := textarea.New()
			ta.SetWidth(min(max(width-30, 24), 64))
			ta.SetHeight(4)
			ta.ShowLineNumbers = false
			ta.Cursor.SetMode(cursor.CursorStatic)
			ta.SetValue(q.DefaultString())
			fl.area = ta
		case "password":
			fl.kind = fPassword
			fl.input = newInput(q.DefaultString(), "", inputWidth)
			fl.input.EchoMode = textinput.EchoPassword
		case "integer":
			fl.kind = fInt
			fl.input = newInput(q.DefaultString(), "", 14)
		case "float":
			fl.kind = fFloat
			fl.input = newInput(q.DefaultString(), "", 14)
		default: // "text" and anything unknown
			fl.kind = fText
			fl.input = newInput(q.DefaultString(), "", inputWidth)
		}
		f.fields = append(f.fields, fl)
	}

	for _, name := range cfg.PasswordsNeededToStart {
		in := newInput("", "", inputWidth)
		in.EchoMode = textinput.EchoPassword
		f.fields = append(f.fields, formField{
			key: name, label: prettyKey(name), kind: fPassword,
			section: secPasswords, required: true, input: in,
		})
	}

	if len(f.fields) > 0 {
		f.fields[0].focus()
	}
	return f
}

// newAdHocForm builds the fixed field set for running a module directly
// against an inventory. Unlike a template it has no ask_*_on_launch config
// and no survey — every field AWX's own ad hoc launch offers is always
// asked.
func newAdHocForm(src adHocFormMsg, width int) form {
	inputWidth := min(max(width-34, 16), 60)
	f := form{isAdHoc: true, adHocInventory: src.inventory, width: width}

	credFl := formField{key: "credential", label: "Credential", kind: fChoice, required: true, usePicker: true}
	for _, c := range src.credentials {
		label := c.Name
		if ty := c.TypeName(); ty != "" {
			label += " (" + ty + ")"
		}
		credFl.choices = append(credFl.choices, label)
		credFl.values = append(credFl.values, strconv.Itoa(c.ID))
	}
	if len(credFl.choices) == 0 {
		credFl = formField{key: "credential", label: "Credential id", kind: fText, required: true,
			input: newInput("", "id", inputWidth)}
	}
	f.fields = append(f.fields, credFl)

	moduleFl := formField{key: "module_name", label: "Module", kind: fChoice,
		choices: awx.AdHocModules, values: awx.AdHocModules, usePicker: true}
	f.fields = append(f.fields, moduleFl)

	f.fields = append(f.fields,
		formField{key: "module_args", label: "Arguments", kind: fText, input: newInput("", "e.g. name=nginx state=restarted", inputWidth)},
		formField{key: "limit", label: "Limit", kind: fText, input: newInput(src.limit, "all hosts", inputWidth)},
		formField{key: "verbosity", label: "Verbosity", kind: fChoice,
			choices: []string{"0 normal", "1 verbose", "2 more verbose", "3 debug", "4 connection debug"},
			values:  []string{"0", "1", "2", "3", "4"}},
		formField{key: "become_enabled", label: "Become", kind: fChoice,
			choices: []string{"off", "on"}, values: []string{"false", "true"}},
	)

	f.fields[0].focus()
	return f
}

// canStartImmediately reports whether there is nothing to fill in.
func (f form) canStartImmediately() bool { return len(f.fields) == 0 }

// focused returns the field the cursor is on, or nil once the form is empty.
func (f *form) focused() *formField {
	if f == nil || f.cursor < 0 || f.cursor >= len(f.fields) {
		return nil
	}
	return &f.fields[f.cursor]
}

func (f *form) moveCursor(delta int) {
	if len(f.fields) == 0 {
		return
	}
	f.fields[f.cursor].blur()
	f.cursor = clamp(f.cursor+delta, 0, len(f.fields)-1)
	f.fields[f.cursor].focus()
	f.problem = ""
}

// update routes a key to the focused field. It reports submit=true when the
// user asked to launch.
func (f *form) update(msg tea.KeyMsg) (submit bool, cmd tea.Cmd) {
	if len(f.fields) == 0 {
		return msg.String() == "enter" || msg.String() == "ctrl+s", nil
	}
	fl := &f.fields[f.cursor]
	switch msg.String() {
	case "ctrl+s":
		return true, nil
	case "enter":
		// Enter inserts a newline in a textarea; elsewhere it launches.
		if fl.kind == fTextarea {
			break
		}
		return true, nil
	case "tab", "down":
		f.moveCursor(1)
		return false, nil
	case "shift+tab", "up":
		f.moveCursor(-1)
		return false, nil
	case "left":
		if fl.kind == fChoice || fl.kind == fMultiChoice {
			if len(fl.choices) > 0 {
				fl.idx = (fl.idx + len(fl.choices) - 1) % len(fl.choices)
			}
			return false, nil
		}
	case "right":
		if fl.kind == fChoice || fl.kind == fMultiChoice {
			if len(fl.choices) > 0 {
				fl.idx = (fl.idx + 1) % len(fl.choices)
			}
			return false, nil
		}
	case " ":
		if fl.kind == fMultiChoice && fl.idx < len(fl.chosen) {
			fl.chosen[fl.idx] = !fl.chosen[fl.idx]
			return false, nil
		}
	}

	switch {
	case fl.kind == fTextarea:
		fl.area, cmd = fl.area.Update(msg)
	case fl.textual():
		fl.input, cmd = fl.input.Update(msg)
	}
	return false, cmd
}

// validate checks required values and types, moving the cursor to the first
// offending field.
func (f *form) validate() error {
	for i := range f.fields {
		fl := &f.fields[i]
		v := strings.TrimSpace(fl.value())
		fail := func(format string, args ...any) error {
			f.cursor = i
			for j := range f.fields {
				f.fields[j].blur()
			}
			f.fields[i].focus()
			return fmt.Errorf("%s: %s", fl.label, fmt.Sprintf(format, args...))
		}

		if fl.required && v == "" {
			return fail("required")
		}
		switch fl.kind {
		case fInt:
			if v == "" {
				continue
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return fail("must be a whole number")
			}
			if err := checkRange(fl, float64(n)); err != nil {
				return fail("%s", err)
			}
		case fFloat:
			if v == "" {
				continue
			}
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fail("must be a number")
			}
			if err := checkRange(fl, n); err != nil {
				return fail("%s", err)
			}
		case fText, fPassword, fTextarea:
			if fl.key == "extra_vars" {
				if _, err := awx.ParseVars(v); err != nil {
					return fail("%s", err)
				}
				continue
			}
			// For text answers AWX treats min/max as a length constraint.
			if v == "" {
				continue
			}
			if err := checkRange(fl, float64(len([]rune(v)))); err != nil {
				return fail("length %s", err)
			}
		}
	}
	return nil
}

func checkRange(fl *formField, n float64) error {
	if fl.min != nil && n < float64(*fl.min) {
		return fmt.Errorf("must be at least %d", *fl.min)
	}
	if fl.max != nil && n > float64(*fl.max) {
		return fmt.Errorf("must be at most %d", *fl.max)
	}
	return nil
}

// payload builds the POST body for /launch/, merging survey answers and any
// user-supplied extra vars.
func (f form) payload() (map[string]any, error) {
	out := map[string]any{}
	vars := map[string]any{}
	passwords := map[string]any{}

	for i := range f.fields {
		fl := &f.fields[i]
		v := fl.value()

		switch {
		case fl.section == secPasswords:
			if v != "" {
				passwords[fl.key] = v
			}

		case fl.survey:
			answer, ok, err := surveyAnswer(fl)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fl.label, err)
			}
			if ok {
				vars[fl.key] = answer
			}

		case fl.key == "extra_vars":
			base, err := awx.ParseVars(v)
			if err != nil {
				return nil, fmt.Errorf("extra vars: %w", err)
			}
			for k, val := range base {
				vars[k] = val
			}

		case fl.kind == fMultiChoice:
			// The only non-survey multi-selects are the id catalogues, and
			// AWX wants those as a list of numbers. An empty list is sent
			// deliberately: it is how the user drops the template's own
			// credentials, labels or instance groups.
			out[fl.key] = fl.selectedIDs()

		case fl.key == "execution_environment":
			// Empty means "keep the template's image": AWX rejects a null id.
			if strings.TrimSpace(v) == "" {
				continue
			}
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fl.label, err)
			}
			out[fl.key] = n

		case fl.kind == fInt:
			if strings.TrimSpace(v) == "" {
				continue
			}
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fl.label, err)
			}
			out[fl.key] = n

		case fl.key == "diff_mode", fl.key == "become_enabled":
			out[fl.key] = v == "true"

		case fl.key == "inventory", fl.key == "verbosity", fl.key == "credential":
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fl.label, err)
			}
			out[fl.key] = n

		default:
			out[fl.key] = v
		}
	}

	if len(vars) > 0 {
		out["extra_vars"] = vars
	}
	if len(passwords) > 0 {
		out["credential_passwords"] = passwords
	}
	return out, nil
}

// surveyAnswer converts a survey field to the type AWX expects, reporting
// ok=false when the question was left unanswered.
func surveyAnswer(fl *formField) (any, bool, error) {
	v := fl.value()
	switch fl.kind {
	case fMultiChoice:
		sel := fl.selections()
		if len(sel) == 0 && !fl.required {
			return nil, false, nil
		}
		return sel, true, nil
	case fInt:
		if strings.TrimSpace(v) == "" {
			return nil, false, nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, err == nil, err
	case fFloat:
		if strings.TrimSpace(v) == "" {
			return nil, false, nil
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return n, err == nil, err
	default:
		if v == "" {
			return nil, false, nil
		}
		return v, true, nil
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// prettyKey turns ssh_password into "Ssh password" for display.
func prettyKey(k string) string {
	s := strings.ReplaceAll(k, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
