package awx

import (
	"encoding/json"
	"testing"
)

// The launch endpoint's ask_* flags and defaults must survive decoding. This
// body is trimmed from a real GET /job_templates/272/launch/: that template is
// the one that exposed the bug, prompting for instance groups while awxtui
// silently ignored the flag.
func TestLaunchConfigDecodesEveryAskFlag(t *testing.T) {
	const body = `{
	  "can_start_without_user_input": false,
	  "ask_inventory_on_launch": false,
	  "ask_limit_on_launch": true,
	  "ask_tags_on_launch": true,
	  "ask_skip_tags_on_launch": true,
	  "ask_variables_on_launch": false,
	  "ask_job_type_on_launch": true,
	  "ask_verbosity_on_launch": true,
	  "ask_scm_branch_on_launch": true,
	  "ask_diff_mode_on_launch": false,
	  "ask_timeout_on_launch": true,
	  "ask_forks_on_launch": false,
	  "ask_job_slice_count_on_launch": false,
	  "ask_credential_on_launch": true,
	  "ask_execution_environment_on_launch": true,
	  "ask_labels_on_launch": true,
	  "ask_instance_groups_on_launch": true,
	  "defaults": {
	    "limit": "",
	    "job_type": "run",
	    "verbosity": 0,
	    "timeout": 43200,
	    "inventory": {"id": 79, "name": "ansible-dns_dnstools"},
	    "credentials": [
	      {"id": 39, "name": "dns_prod.ssh", "credential_type": 1, "passwords_needed": []},
	      {"id": 7, "name": "vault.approle", "credential_type": 31, "passwords_needed": []}
	    ],
	    "execution_environment": {"id": 14, "name": "ansible-dns"},
	    "labels": [{"id": 1, "name": "nightly"}],
	    "instance_groups": []
	  }
	}`
	var cfg LaunchConfig
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for name, got := range map[string]bool{
		"ask_credential_on_launch":            cfg.AskCredentials,
		"ask_execution_environment_on_launch": cfg.AskExecutionEnvironment,
		"ask_labels_on_launch":                cfg.AskLabels,
		"ask_instance_groups_on_launch":       cfg.AskInstanceGroups,
		"ask_limit_on_launch":                 cfg.AskLimit,
		"ask_scm_branch_on_launch":            cfg.AskSCMBranch,
	} {
		if !got {
			t.Errorf("%s decoded as false", name)
		}
	}
	if ids := IDs(cfg.Defaults.Credentials); len(ids) != 2 || ids[0] != 39 || ids[1] != 7 {
		t.Errorf("default credential ids = %v, want [39 7]", ids)
	}
	if ee := cfg.Defaults.ExecutionEnvironment; ee.ID != 14 || ee.Name != "ansible-dns" {
		t.Errorf("default execution environment = %+v", ee)
	}
	if ids := IDs(cfg.Defaults.Labels); len(ids) != 1 || ids[0] != 1 {
		t.Errorf("default label ids = %v, want [1]", ids)
	}
	if got := cfg.Defaults.InstanceGroups; len(got) != 0 {
		t.Errorf("default instance groups = %+v, want none", got)
	}
}

// AWX is not consistent about references: some builds send the {id, name}
// object, others the bare id, and an unset one is null.
func TestNamedRefAcceptsObjectIDAndNull(t *testing.T) {
	for _, c := range []struct {
		raw    string
		wantID int
	}{
		{`{"id": 14, "name": "ansible-dns"}`, 14},
		{`14`, 14},
		{`null`, 0},
	} {
		var ref NamedRef
		if err := json.Unmarshal([]byte(c.raw), &ref); err != nil {
			t.Errorf("%s: %v", c.raw, err)
			continue
		}
		if ref.ID != c.wantID {
			t.Errorf("%s decoded to id %d, want %d", c.raw, ref.ID, c.wantID)
		}
	}
	var ref NamedRef
	if err := json.Unmarshal([]byte(`"not a reference"`), &ref); err == nil {
		t.Error("a string should not decode as a reference")
	}
}
