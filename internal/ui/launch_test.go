package ui

import (
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

	want := []string{"inventory", "job_type", "limit", "verbosity", "job_tags", "diff_mode", "timeout", "extra_vars"}
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
