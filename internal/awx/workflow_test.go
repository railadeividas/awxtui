package awx

import (
	"encoding/json"
	"testing"
)

// A workflow job template's launch metadata is a strict subset of a job
// template's: no credential, execution environment, job type or verbosity
// prompt, because those belong to the workflow's nodes. Decoding it into the
// same LaunchConfig must leave those flags false rather than erroring.
func TestWorkflowLaunchConfigDecodesAsASubset(t *testing.T) {
	const body = `{
	  "can_start_without_user_input": true,
	  "ask_inventory_on_launch": false,
	  "ask_limit_on_launch": false,
	  "ask_scm_branch_on_launch": true,
	  "ask_labels_on_launch": false,
	  "ask_skip_tags_on_launch": false,
	  "ask_tags_on_launch": false,
	  "ask_variables_on_launch": false,
	  "survey_enabled": false,
	  "variables_needed_to_start": [],
	  "defaults": {
	    "limit": "", "scm_branch": "", "job_tags": null, "skip_tags": null, "extra_vars": "{}",
	    "inventory": {"id": 131, "name": "ansible-cdn"}
	  }
	}`
	var cfg LaunchConfig
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.AskSCMBranch {
		t.Error("ask_scm_branch_on_launch decoded as false")
	}
	if cfg.AskCredentials || cfg.AskExecutionEnvironment || cfg.AskJobType || cfg.AskVerbosity {
		t.Errorf("a workflow launch config must never set node-only flags: %+v", cfg)
	}
	if cfg.Defaults.Inventory.Name != "ansible-cdn" {
		t.Errorf("default inventory = %+v", cfg.Defaults.Inventory)
	}
}

// WorkflowJobTemplate.LastRunStatus must map AWX's "never updated" to empty,
// matching the other last-run status fields awxtui renders.
func TestWorkflowJobTemplateLastRunStatus(t *testing.T) {
	for _, c := range []struct{ status, want string }{
		{"never updated", ""},
		{"successful", "successful"},
		{"failed", "failed"},
		{"", ""},
	} {
		got := WorkflowJobTemplate{Status: c.status}.LastRunStatus()
		if got != c.want {
			t.Errorf("LastRunStatus(%q) = %q, want %q", c.status, got, c.want)
		}
	}
}

// A workflow job's Resource must resolve to its own collection, not the
// default /api/v2/jobs/ every unrecognised type falls back to — that default
// existed to keep a refreshed record pointing at its own output endpoints,
// and would otherwise send a workflow job's cancel to the wrong collection.
func TestWorkflowJobResourceAndKind(t *testing.T) {
	j := Job{Type: "workflow_job"}
	if got := j.Resource(); got != ResourceWorkflowJobs {
		t.Errorf("Resource() = %q, want %q", got, ResourceWorkflowJobs)
	}
	if !j.IsWorkflow() {
		t.Error("IsWorkflow() = false for a workflow_job record")
	}
	if j.IsSync() {
		t.Error("IsSync() = true for a workflow job, which is neither a project update nor an inventory sync")
	}
	if j.Supported() {
		// Supported gates whether output/stdout can be shown. A workflow
		// job has none of its own, so this must stay false; the UI uses it
		// to route a workflow job to launch details instead.
		t.Error("workflow_job unexpectedly reports Supported(); it has no output of its own")
	}
	if got := j.KindLabel(); got != "workflow" {
		t.Errorf("KindLabel() = %q, want workflow", got)
	}
}
