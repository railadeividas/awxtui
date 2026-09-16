package awx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// LaunchConfig is the GET /job_templates/{id}/launch/ metadata describing what
// a template needs from the user before it can start.
type LaunchConfig struct {
	CanStartWithoutUserInput bool     `json:"can_start_without_user_input"`
	PasswordsNeededToStart   []string `json:"passwords_needed_to_start"`
	VariablesNeededToStart   []string `json:"variables_needed_to_start"`
	SurveyEnabled            bool     `json:"survey_enabled"`
	InventoryNeededToStart   bool     `json:"inventory_needed_to_start"`
	CredentialNeededToStart  bool     `json:"credential_needed_to_start"`

	AskInventory            bool `json:"ask_inventory_on_launch"`
	AskLimit                bool `json:"ask_limit_on_launch"`
	AskTags                 bool `json:"ask_tags_on_launch"`
	AskSkipTags             bool `json:"ask_skip_tags_on_launch"`
	AskVariables            bool `json:"ask_variables_on_launch"`
	AskJobType              bool `json:"ask_job_type_on_launch"`
	AskVerbosity            bool `json:"ask_verbosity_on_launch"`
	AskSCMBranch            bool `json:"ask_scm_branch_on_launch"`
	AskDiffMode             bool `json:"ask_diff_mode_on_launch"`
	AskTimeout              bool `json:"ask_timeout_on_launch"`
	AskForks                bool `json:"ask_forks_on_launch"`
	AskJobSliceCount        bool `json:"ask_job_slice_count_on_launch"`
	AskCredentials          bool `json:"ask_credential_on_launch"`
	AskExecutionEnvironment bool `json:"ask_execution_environment_on_launch"`
	AskLabels               bool `json:"ask_labels_on_launch"`
	AskInstanceGroups       bool `json:"ask_instance_groups_on_launch"`

	Defaults LaunchDefaults `json:"defaults"`
}

// NamedRef is an {id, name} reference to another record. AWX is inconsistent
// about these inside the launch defaults: some builds send the object, others
// send the bare id, and an unset reference is null — so accept all three.
type NamedRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func (r *NamedRef) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if id, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
		r.ID = id
		return nil
	}
	var obj struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	r.ID, r.Name = obj.ID, obj.Name
	return nil
}

// LaunchDefaults are the values AWX will use for anything left untouched.
type LaunchDefaults struct {
	Limit                string     `json:"limit"`
	SCMBranch            string     `json:"scm_branch"`
	JobTags              string     `json:"job_tags"`
	SkipTags             string     `json:"skip_tags"`
	ExtraVars            string     `json:"extra_vars"`
	JobType              string     `json:"job_type"`
	Verbosity            int        `json:"verbosity"`
	DiffMode             bool       `json:"diff_mode"`
	Timeout              int        `json:"timeout"`
	Forks                int        `json:"forks"`
	JobSliceCount        int        `json:"job_slice_count"`
	Inventory            NamedRef   `json:"inventory"`
	Credentials          []NamedRef `json:"credentials"`
	ExecutionEnvironment NamedRef   `json:"execution_environment"`
	Labels               []NamedRef `json:"labels"`
	InstanceGroups       []NamedRef `json:"instance_groups"`
}

// IDs pulls the ids out of a reference list, for pre-selecting the template's
// own values in the launch form.
func IDs(refs []NamedRef) []int {
	out := make([]int, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.ID)
	}
	return out
}

func (c *Client) LaunchConfig(ctx context.Context, templateID int) (LaunchConfig, error) {
	var cfg LaunchConfig
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v2/job_templates/%d/launch/", templateID), nil, &cfg)
	return cfg, err
}

// SurveySpec is a template's survey questionnaire.
type SurveySpec struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Spec        []SurveyQuestion `json:"spec"`
}

// SurveyQuestion is one survey field. AWX is loose about types here: choices
// arrive either as a newline-separated string or a list, and default can be a
// string, number, bool or list.
type SurveyQuestion struct {
	Type                string `json:"type"`
	QuestionName        string `json:"question_name"`
	QuestionDescription string `json:"question_description"`
	Variable            string `json:"variable"`
	Required            bool   `json:"required"`
	Default             any    `json:"default"`
	Choices             any    `json:"choices"`
	Min                 *int   `json:"min"`
	Max                 *int   `json:"max"`
}

// ChoiceList normalises the choices field to a slice.
func (q SurveyQuestion) ChoiceList() []string {
	switch v := q.Choices.(type) {
	case string:
		var out []string
		for _, line := range strings.Split(v, "\n") {
			if s := strings.TrimSpace(line); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		var out []string
		for _, c := range v {
			out = append(out, scalarString(c))
		}
		return out
	}
	return nil
}

// DefaultString renders the question's default as editable text.
func (q SurveyQuestion) DefaultString() string {
	if list, ok := q.Default.([]any); ok {
		parts := make([]string, 0, len(list))
		for _, v := range list {
			parts = append(parts, scalarString(v))
		}
		return strings.Join(parts, "\n")
	}
	return scalarString(q.Default)
}

// DefaultList renders the default of a multiselect question.
func (q SurveyQuestion) DefaultList() []string {
	switch v := q.Default.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, scalarString(item))
		}
		return out
	case string:
		var out []string
		for _, line := range strings.Split(v, "\n") {
			if s := strings.TrimSpace(line); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	}
	return fmt.Sprint(v)
}

func (c *Client) SurveySpec(ctx context.Context, templateID int) (SurveySpec, error) {
	var spec SurveySpec
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v2/job_templates/%d/survey_spec/", templateID), nil, &spec)
	return spec, err
}

// Launch starts a job template with an explicit payload of prompted values.
// Only keys the template actually asks for should be included.
func (c *Client) Launch(ctx context.Context, templateID int, payload map[string]any) (Job, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Job{}, err
	}
	var j Job
	err = c.do(ctx, http.MethodPost,
		fmt.Sprintf("/api/v2/job_templates/%d/launch/", templateID),
		strings.NewReader(string(raw)), &j)
	return j, err
}

// ParseVars reads extra variables written as either JSON or YAML and requires
// the result to be a mapping, which is what AWX expects for extra_vars.
func ParseVars(s string) (map[string]any, error) {
	if strings.TrimSpace(s) == "" {
		return map[string]any{}, nil
	}
	var v any
	// YAML is a superset of JSON, so this accepts both forms.
	if err := yaml.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("not valid YAML or JSON: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("extra vars must be a mapping of names to values, got %T", v)
	}
	return m, nil
}
