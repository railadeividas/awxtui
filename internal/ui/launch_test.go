package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// openTemplateForm connects, selects a template by name and opens its launch form.
func openTemplateForm(t *testing.T, srv *mock, name string) Model {
	t.Helper()
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, m.connect())
	for i, tpl := range m.templates {
		if tpl.Name == name {
			m.cursor[tabTemplates] = i
		}
	}
	m = step(t, m, key("enter"))
	if m.mode != modeLaunch {
		t.Fatalf("expected the launch form, got mode %v (err %v)", m.mode, m.err)
	}
	return m
}

// A template that prompts for values must offer them as fields, and the
// payload must carry AWX's expected types.
func TestLaunchFormPrompts(t *testing.T) {
	srv := mockAWX(t)
	m := openTemplateForm(t, srv, "Deploy web app")

	want := []string{"inventory", "credentials", "execution_environment", "instance_groups", "labels",
		"job_type", "limit", "verbosity", "job_tags", "diff_mode", "timeout", "extra_vars"}
	if got := formKeys(&m); !equalStrings(got, want) {
		t.Fatalf("form fields = %v, want %v", got, want)
	}
	show(t, "launch form with prompts", m.View())

	m = focusField(t, m, "limit")
	m = typeText(t, m, "web-01:web-02")
	m = focusField(t, m, "job_type")
	m = step(t, m, key("right")) // run -> check
	m = focusField(t, m, "verbosity")
	m = step(t, m, key("right"))
	m = step(t, m, key("right")) // 0 -> 2
	m = focusField(t, m, "timeout")
	m = typeText(t, m, "30")
	m = focusField(t, m, "diff_mode")
	m = step(t, m, key("right")) // off -> on
	m = step(t, m, key("ctrl+s"))

	if m.err != nil {
		t.Fatalf("launch errored: %v", m.err)
	}
	p := srv.lastLaunch()
	if p == nil {
		t.Fatal("nothing was launched")
	}
	if p["limit"] != "web-01:web-02" {
		t.Errorf("limit = %#v", p["limit"])
	}
	if p["job_type"] != "check" {
		t.Errorf("job_type = %#v", p["job_type"])
	}
	// Numbers must be JSON numbers, not strings.
	if v, ok := p["verbosity"].(float64); !ok || v != 2 {
		t.Errorf("verbosity = %#v, want number 2", p["verbosity"])
	}
	if v, ok := p["timeout"].(float64); !ok || v != 30 {
		t.Errorf("timeout = %#v, want number 30", p["timeout"])
	}
	if v, ok := p["inventory"].(float64); !ok || v != 3 {
		t.Errorf("inventory = %#v, want id 3", p["inventory"])
	}
	if p["diff_mode"] != true {
		t.Errorf("diff_mode = %#v, want true", p["diff_mode"])
	}
	// extra_vars was left empty, so it must not be sent at all.
	if _, ok := p["extra_vars"]; ok {
		t.Errorf("empty extra_vars should be omitted, got %#v", p["extra_vars"])
	}
}

// The catalogue prompts — credentials, execution environment, instance groups
// and labels — offer the instance's records, pre-select the template's own
// values, and send ids.
func TestLaunchCataloguePrompts(t *testing.T) {
	srv := mockAWX(t)
	m := openTemplateForm(t, srv, "Deploy web app")

	// Credentials span two pages, and credential 99 is not in the catalogue
	// at all; all four must be offered.
	creds := fieldByKey(t, &m, "credentials")
	if creds.kind != fMultiChoice {
		t.Fatalf("credentials kind = %v, want multi-select", creds.kind)
	}
	wantChoices := []string{"web_prod.ssh (Machine)", "db_prod.ssh (Machine)", "galaxy.token (Ansible Galaxy)", "vault.approle"}
	if !equalStrings(creds.choices, wantChoices) {
		t.Errorf("credential choices = %v, want %v", creds.choices, wantChoices)
	}
	if want := []string{"web_prod.ssh (Machine)", "vault.approle"}; !equalStrings(creds.selections(), want) {
		t.Errorf("pre-selected credentials = %v, want %v", creds.selections(), want)
	}
	if groups := fieldByKey(t, &m, "instance_groups"); !equalStrings(groups.choices, []string{"default", "tower-srv", "tower-k8s (container)"}) {
		t.Errorf("instance group choices = %v", groups.choices)
	} else if len(groups.selections()) != 0 {
		t.Errorf("the template pins no instance group, got %v", groups.selections())
	}
	if labels := fieldByKey(t, &m, "labels"); !equalStrings(labels.selections(), []string{"nightly"}) {
		t.Errorf("pre-selected labels = %v, want [nightly]", labels.selections())
	}
	// The execution environment is a single choice whose first entry keeps
	// the template's own image.
	ee := fieldByKey(t, &m, "execution_environment")
	if !equalStrings(ee.choices, []string{"template default", "ansible-web", "ansible-db"}) {
		t.Errorf("execution environment choices = %v", ee.choices)
	}
	if ee.value() != "14" {
		t.Errorf("execution environment should default to the template's image 14, got %q", ee.value())
	}
	show(t, "launch form with catalogue prompts", m.View())

	// Pin tower-srv, swap the label, leave credentials and the image alone.
	m = focusField(t, m, "instance_groups")
	m = step(t, m, key("right"))
	m = step(t, m, key(" "))
	m = focusField(t, m, "labels")
	m = step(t, m, key(" ")) // nightly off
	m = step(t, m, key("right"))
	m = step(t, m, key(" ")) // hotfix on
	show(t, "instance group selected", m.View())
	m = step(t, m, key("ctrl+s"))
	if m.err != nil {
		t.Fatalf("launch errored: %v", m.err)
	}

	p := srv.lastLaunch()
	if p == nil {
		t.Fatal("nothing was launched")
	}
	for _, c := range []struct {
		key  string
		want []float64
	}{
		{"instance_groups", []float64{12}},
		{"labels", []float64{2}},
		{"credentials", []float64{39, 99}},
	} {
		got, ok := p[c.key].([]any)
		if !ok {
			t.Errorf("%s = %#v, want a list of ids", c.key, p[c.key])
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%s = %#v, want %v", c.key, got, c.want)
			continue
		}
		for i, want := range c.want {
			if n, ok := got[i].(float64); !ok || n != want {
				t.Errorf("%s[%d] = %#v, want number %v", c.key, i, got[i], want)
			}
		}
	}
	if v, ok := p["execution_environment"].(float64); !ok || v != 14 {
		t.Errorf("execution_environment = %#v, want number 14", p["execution_environment"])
	}
}

// Choosing "template default" must leave execution_environment out of the
// payload: AWX rejects a null id, and an absent key means "keep the image".
func TestLaunchKeepsTemplateExecutionEnvironment(t *testing.T) {
	srv := mockAWX(t)
	m := openTemplateForm(t, srv, "Deploy web app")
	m = focusField(t, m, "execution_environment")
	m = step(t, m, key("left")) // ansible-web -> template default
	if v := fieldByKey(t, &m, "execution_environment").value(); v != "" {
		t.Fatalf("expected the template default to be selected, got %q", v)
	}
	m = step(t, m, key("ctrl+s"))
	if m.err != nil {
		t.Fatalf("launch errored: %v", m.err)
	}
	if v, ok := srv.lastLaunch()["execution_environment"]; ok {
		t.Errorf("execution_environment should have been omitted, got %#v", v)
	}
}

// hiddenCount matches the "41›" a windowed multi-select adds when entries
// are scrolled out of view.
var hiddenCount = regexp.MustCompile(`[0-9]›`)

// A real catalogue is far bigger than the screen: 19 instance groups and 52
// credentials on the instance this was built against, with names long enough
// that three of them fill a line. The field has to window them rather than
// render the list inline.
func TestLaunchFormFitsWithALargeCatalogue(t *testing.T) {
	srv := mockAWX(t)
	base := openTemplateForm(t, srv, "Deploy web app")
	groups := make([]awx.InstanceGroup, 60)
	for i := range groups {
		groups[i] = awx.InstanceGroup{ID: i + 1, Name: fmt.Sprintf("tower-region-%02d-executors", i)}
	}
	msg := launchFormMsg{
		gen: base.gen, template: base.form.template, config: base.form.config,
		inventories: []awx.Inventory{{ID: 3, Name: "production"}}, instanceGroups: groups,
	}

	for _, size := range [][2]int{{80, 24}, {120, 40}, {200, 50}} {
		m := New(awx.New(srv.URL, "test-token", false))
		m = step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = step(t, m, m.connect())
		m = step(t, m, msg)
		if m.mode != modeLaunch {
			t.Fatalf("expected the launch form, got mode %v", m.mode)
		}
		windowed := false
		// Walk every field: each one is rendered differently when focused.
		for i := 0; i < len(m.form.fields); i++ {
			m = step(t, m, key("down"))
			out := m.View()
			if m.form.fields[m.form.cursor].key == "instance_groups" {
				plain := stripANSI(out)
				boxes := strings.Count(plain, "[ ]") + strings.Count(plain, "[x]")
				// A window shows a handful of entries and says how many are
				// hidden ("‹12 … 41›"), so the digit-arrow pair is the tell.
				windowed = boxes < len(groups) && hiddenCount.MatchString(plain)
			}
			for n, line := range strings.Split(out, "\n") {
				if w := lineWidth(line); w > size[0] {
					t.Errorf("%dx%d field %q: line %d is %d cols wide",
						size[0], size[1], m.form.fields[m.form.cursor].key, n, w)
				}
			}
			if lines := strings.Count(out, "\n") + 1; lines > size[1] {
				t.Errorf("%dx%d field %q: view is %d lines, terminal has %d",
					size[0], size[1], m.form.fields[m.form.cursor].key, lines, size[1])
			}
		}
		if !windowed {
			t.Errorf("%dx%d: 60 instance groups should render as a window with a hidden count", size[0], size[1])
		}
	}
	show(t, "60 instance groups at 80 columns", func() string {
		m := New(awx.New(srv.URL, "test-token", false))
		m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
		m = step(t, m, m.connect())
		m = step(t, m, msg)
		m = focusField(t, m, "instance_groups")
		return m.View()
	}())
}

// Without a catalogue there is nothing to choose from, and offering an empty
// multi-select would submit "none" — dropping the template's own credentials.
func TestIDListFieldSkipsEmptyCatalogue(t *testing.T) {
	if _, ok := idListField("credentials", "Credentials", nil, nil); ok {
		t.Error("an empty catalogue with no defaults should produce no field")
	}
	fl, ok := idListField("credentials", "Credentials", nil, []awx.NamedRef{{ID: 7, Name: "vault"}})
	if !ok {
		t.Fatal("a template default must survive an unavailable catalogue")
	}
	if !equalStrings(fl.choices, []string{"vault"}) || !equalStrings(fl.selections(), []string{"vault"}) {
		t.Errorf("choices = %v, selected = %v, want the default pre-selected", fl.choices, fl.selections())
	}
	if ids := fl.selectedIDs(); len(ids) != 1 || ids[0] != 7 {
		t.Errorf("selectedIDs = %v, want [7]", ids)
	}
}

// Survey questions become fields with their defaults, types and choices.
func TestSurveyFormFieldsAndDefaults(t *testing.T) {
	srv := mockAWX(t)
	m := openTemplateForm(t, srv, "Rotate certificates")

	for _, k := range []string{"release", "batch", "ratio", "env", "steps", "api_key", "notes"} {
		fl := fieldByKey(t, &m, k)
		if !fl.survey {
			t.Errorf("%s should be a survey field", k)
		}
	}
	if fl := fieldByKey(t, &m, "release"); !fl.required {
		t.Error("release is required in the survey spec")
	}
	if fl := fieldByKey(t, &m, "batch"); fl.kind != fInt || fl.input.Value() != "2" {
		t.Errorf("batch: kind=%v value=%q, want integer defaulting to 2", fl.kind, fl.input.Value())
	}
	// choices arrive as a newline-separated string on real AWX
	if fl := fieldByKey(t, &m, "env"); !equalStrings(fl.choices, []string{"staging", "production"}) {
		t.Errorf("env choices = %v", fl.choices)
	}
	if fl := fieldByKey(t, &m, "env"); fl.value() != "staging" {
		t.Errorf("env should default to staging, got %q", fl.value())
	}
	// multiselect defaults are pre-ticked
	if fl := fieldByKey(t, &m, "steps"); !equalStrings(fl.selections(), []string{"build"}) {
		t.Errorf("steps selections = %v, want [build]", fl.selections())
	}
	if fl := fieldByKey(t, &m, "api_key"); fl.kind != fPassword {
		t.Errorf("api_key kind = %v, want password", fl.kind)
	}
	if fl := fieldByKey(t, &m, "notes"); fl.kind != fTextarea {
		t.Errorf("notes kind = %v, want textarea", fl.kind)
	}
	show(t, "survey form", m.View())
}

// A required survey answer must block the launch instead of failing at the API.
func TestSurveyRequiredAnswerBlocksLaunch(t *testing.T) {
	srv := mockAWX(t)
	m := openTemplateForm(t, srv, "Rotate certificates")

	m = step(t, m, key("ctrl+s"))
	if m.mode != modeLaunch {
		t.Fatal("form should stay open when a required answer is missing")
	}
	if !strings.Contains(strings.ToLower(m.form.problem), "required") {
		t.Errorf("problem = %q, want it to mention the required field", m.form.problem)
	}
	if srv.launchCount() != 0 {
		t.Errorf("nothing should have been launched, got %d launches", srv.launchCount())
	}
	show(t, "survey validation error", m.View())

	// The cursor must land on the offending field so it can be fixed.
	if m.form.fields[m.form.cursor].key != "release" {
		t.Errorf("cursor on %q, want release", m.form.fields[m.form.cursor].key)
	}
}

// Out-of-range integers are caught before the request.
func TestSurveyRangeValidation(t *testing.T) {
	srv := mockAWX(t)
	m := openTemplateForm(t, srv, "Rotate certificates")
	m = focusField(t, m, "release")
	m = typeText(t, m, "v1.2.3")
	m = focusField(t, m, "batch")
	m = step(t, m, key("backspace"))
	m = typeText(t, m, "99")
	m = step(t, m, key("ctrl+s"))

	if srv.launchCount() != 0 {
		t.Fatalf("99 is above max 10 but was launched anyway")
	}
	if !strings.Contains(m.form.problem, "at most 10") {
		t.Errorf("problem = %q, want an at-most-10 message", m.form.problem)
	}
}

// Survey answers are merged into extra_vars with their declared types.
func TestSurveyPayloadTypesAndExtraVarsMerge(t *testing.T) {
	srv := mockAWX(t)
	m := openTemplateForm(t, srv, "Rotate certificates")

	m = focusField(t, m, "release")
	m = typeText(t, m, "v1.2.3")
	m = focusField(t, m, "env")
	m = step(t, m, key("right")) // staging -> production
	m = focusField(t, m, "steps")
	m = step(t, m, key("right")) // highlight migrate
	m = step(t, m, key("space")) // tick it
	m = focusField(t, m, "extra_vars")
	m = typeText(t, m, "region: eu-central")
	m = step(t, m, key("ctrl+s"))

	if m.err != nil {
		t.Fatalf("launch errored: %v", m.err)
	}
	p := srv.lastLaunch()
	if p == nil {
		t.Fatal("nothing was launched")
	}
	vars, ok := p["extra_vars"].(map[string]any)
	if !ok {
		t.Fatalf("extra_vars = %#v, want a mapping", p["extra_vars"])
	}
	if vars["release"] != "v1.2.3" {
		t.Errorf("release = %#v", vars["release"])
	}
	if vars["env"] != "production" {
		t.Errorf("env = %#v", vars["env"])
	}
	if v, ok := vars["batch"].(float64); !ok || v != 2 {
		t.Errorf("batch = %#v, want number 2 from the default", vars["batch"])
	}
	if v, ok := vars["ratio"].(float64); !ok || v != 0.5 {
		t.Errorf("ratio = %#v, want number 0.5", vars["ratio"])
	}
	steps, ok := vars["steps"].([]any)
	if !ok || len(steps) != 2 || steps[0] != "build" || steps[1] != "migrate" {
		t.Errorf("steps = %#v, want [build migrate]", vars["steps"])
	}
	// User-supplied YAML extra vars merge alongside the survey answers.
	if vars["region"] != "eu-central" {
		t.Errorf("region = %#v, want eu-central from the extra vars field", vars["region"])
	}
	// An unanswered optional password must not be sent as an empty string.
	if _, ok := vars["api_key"]; ok {
		t.Errorf("unanswered api_key should be omitted, got %#v", vars["api_key"])
	}
}

// Malformed extra vars are reported in the form, not sent to AWX.
func TestExtraVarsMustBeMapping(t *testing.T) {
	srv := mockAWX(t)
	m := openTemplateForm(t, srv, "Deploy web app")
	m = focusField(t, m, "extra_vars")
	m = typeText(t, m, "just a string")
	m = step(t, m, key("ctrl+s"))

	if srv.launchCount() != 0 {
		t.Fatal("invalid extra vars were sent to AWX")
	}
	if !strings.Contains(m.form.problem, "mapping") {
		t.Errorf("problem = %q, want a mapping complaint", m.form.problem)
	}
}

// Read-only mode must refuse to even open the launch form.
func TestReadOnlyModeBlocksLaunchAndCancel(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false).ReadOnly())
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, m.connect())

	// The form itself is read-only API traffic, so it may open...
	m = step(t, m, key("enter"))
	if m.mode != modeLaunch {
		t.Fatalf("read-only mode should still allow inspecting the form, mode = %v (err %v)", m.mode, m.err)
	}
	// ...but submitting must be refused.
	m = focusField(t, m, "limit")
	m = typeText(t, m, "web-01")
	m = step(t, m, key("ctrl+s"))
	if m.mode != modeLaunch {
		t.Error("form should stay open after a refused submit")
	}
	if !strings.Contains(m.form.problem, "read-only") {
		t.Errorf("problem = %q, want a read-only message", m.form.problem)
	}
	m = step(t, m, key("esc"))

	m = step(t, m, key("2")) // jobs, first one is running
	m = step(t, m, key("c"))
	if m.err == nil || !strings.Contains(m.err.Error(), "read-only") {
		t.Errorf("cancel err = %v, want a read-only message", m.err)
	}
	if srv.launchCount() != 0 {
		t.Errorf("read-only mode launched something: %d", srv.launchCount())
	}
	if m.client.IsReadOnly() != true {
		t.Error("client should report read-only")
	}
	show(t, "read-only refusal", m.View())
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
