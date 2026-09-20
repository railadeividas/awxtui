package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// mock is a fake AWX instance that also records what was launched and every
// path it was asked to change.
type mock struct {
	*httptest.Server
	mu       sync.Mutex
	launches []map[string]any
	posts    []string
	// unifiedQueries records the query string of every /api/v2/unified_jobs/
	// request, so a test can assert the filter actually went to AWX rather
	// than being applied to rows already on screen.
	unifiedQueries []string
}

func (m *mock) unified() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.unifiedQueries...)
}

// postedTo reports the paths the model POSTed to, in order.
func (m *mock) postedTo() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.posts...)
}

func (m *mock) lastLaunch() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.launches) == 0 {
		return nil
	}
	return m.launches[len(m.launches)-1]
}

func (m *mock) launchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.launches)
}

// mockAWX serves just enough of the v2 API to drive the UI.
func mockAWX(t *testing.T) *mock {
	t.Helper()
	mk := &mock{}
	now := time.Now().UTC()
	page := func(results ...any) map[string]any {
		return map[string]any{"count": len(results), "results": results}
	}
	mux := http.NewServeMux()
	// searched mirrors AWX's ?search=: a case-insensitive match over the
	// record's name and description.
	searched := func(r *http.Request, items []any) []any {
		q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
		if q == "" {
			return items
		}
		var hits []any
		for _, it := range items {
			m, _ := it.(map[string]any)
			name, _ := m["name"].(string)
			desc, _ := m["description"].(string)
			if strings.Contains(strings.ToLower(name+" "+desc), q) {
				hits = append(hits, it)
			}
		}
		return hits
	}
	// filtered mirrors the query filters real AWX applies to a list, on top
	// of ?search=: id__in on every collection, and created_by/status/type on
	// the job lists. A mock that ignored them would let a filter that never
	// reached AWX still look like it worked.
	filtered := func(r *http.Request, items []any) []any {
		hits := searched(r, items)
		q := r.URL.Query()
		match := func(rec map[string]any, param string, of func(map[string]any) string) bool {
			want := q.Get(param)
			return want == "" || of(rec) == want
		}
		// matchIn mirrors AWX's __in lookup: a comma list where any member
		// matching is enough. Used for id__in, status__in and type__in alike.
		matchIn := func(param, got string) bool {
			list := q.Get(param)
			if list == "" {
				return true
			}
			for _, want := range strings.Split(list, ",") {
				if want == got {
					return true
				}
			}
			return false
		}
		var kept []any
		for _, it := range hits {
			rec, _ := it.(map[string]any)
			if !matchIn("id__in", fmt.Sprint(rec["id"])) {
				continue
			}
			createdBy := func(rec map[string]any) string {
				sf, _ := rec["summary_fields"].(map[string]any)
				by, _ := sf["created_by"].(map[string]any)
				return fmt.Sprint(by["id"])
			}
			createdByUsername := func(rec map[string]any) string {
				sf, _ := rec["summary_fields"].(map[string]any)
				by, _ := sf["created_by"].(map[string]any)
				return fmt.Sprint(by["username"])
			}
			if !match(rec, "created_by", createdBy) {
				continue
			}
			if !matchIn("status__in", fmt.Sprint(rec["status"])) ||
				!matchIn("type__in", fmt.Sprint(rec["type"])) {
				continue
			}
			if frag := strings.ToLower(q.Get("created_by__username__icontains")); frag != "" {
				if !strings.Contains(strings.ToLower(createdByUsername(rec)), frag) {
					continue
				}
			}
			kept = append(kept, it)
		}
		return kept
	}
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/api/v2/me/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, `{"detail":"bad token"}`, http.StatusUnauthorized)
			return
		}
		write(w, page(map[string]any{"id": 1, "username": "admin"}))
	})
	// /api/v2/unified_jobs/ is the only list that holds all three kinds of
	// run. Real AWX filters it by created_by (the numeric user id), by
	// id__in, and by ?search=, and composes all three; so does this. The
	// extra records exist only here, exactly as on a real instance: a sync
	// and a workflow job never appear in /api/v2/jobs/.
	unified := []any{
		map[string]any{
			"id": 43, "type": "job", "name": "Deploy web app", "status": "running", "elapsed": 12.4,
			"started": now.Add(-12 * time.Second), "job_type": "run",
			"summary_fields": map[string]any{"created_by": map[string]any{"id": 1, "username": "admin"}},
		},
		map[string]any{
			"id": 42, "type": "job", "name": "Deploy web app", "status": "successful", "elapsed": 96.2,
			"started": now.Add(-90 * time.Minute), "job_type": "run",
			"summary_fields": map[string]any{"created_by": map[string]any{"id": 1, "username": "admin"}},
		},
		map[string]any{
			"id": 40, "type": "job", "name": "Someone else's run", "status": "failed", "elapsed": 3.0,
			"started": now.Add(-4 * time.Hour), "job_type": "run",
			"summary_fields": map[string]any{"created_by": map[string]any{"id": 2, "username": "colleague"}},
		},
		map[string]any{
			"id": 21, "type": "inventory_update", "name": "staging - git", "status": "successful",
			"elapsed": 4.0, "started": now.Add(-30 * time.Minute),
			"summary_fields": map[string]any{"created_by": map[string]any{"id": 1, "username": "admin"}},
		},
		map[string]any{
			"id": 12, "type": "project_update", "name": "infra", "status": "successful",
			"elapsed": 6.0, "started": now.Add(-40 * time.Minute),
			"summary_fields": map[string]any{"created_by": map[string]any{"id": 1, "username": "admin"}},
		},
		map[string]any{
			// A workflow job has no output endpoint of its own — its output
			// lives per-node — but it does have a detail endpoint, and can
			// be cancelled just like a playbook job.
			"id": 11, "type": "workflow_job", "name": "Nightly pipeline", "status": "running",
			"elapsed": 300.0, "started": now.Add(-3 * time.Hour),
			"summary_fields": map[string]any{"created_by": map[string]any{"id": 1, "username": "admin"}},
		},
	}
	mux.HandleFunc("/api/v2/unified_jobs/", func(w http.ResponseWriter, r *http.Request) {
		mk.mu.Lock()
		mk.unifiedQueries = append(mk.unifiedQueries, r.URL.RawQuery)
		mk.mu.Unlock()
		write(w, page(filtered(r, unified)...))
	})
	mux.HandleFunc("/api/v2/job_templates/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(filtered(r, []any{map[string]any{
			"id": 7, "name": "Deploy web app", "job_type": "run", "playbook": "deploy.yml",
			"last_job_run": now.Add(-90 * time.Minute),
			"summary_fields": map[string]any{
				"project":   map[string]any{"name": "infra"},
				"inventory": map[string]any{"name": "production"},
				"last_job":  map[string]any{"id": 42, "status": "successful"},
			},
		}, map[string]any{
			"id": 8, "name": "Rotate certificates", "job_type": "run", "playbook": "certs.yml",
			"survey_enabled": true,
			"summary_fields": map[string]any{
				"project":   map[string]any{"name": "security"},
				"inventory": map[string]any{"name": "all"},
				"last_job":  map[string]any{"id": 41, "status": "failed"},
			},
		}})...))
	})
	mux.HandleFunc("/api/v2/jobs/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(searched(r, []any{map[string]any{
			"id": 43, "name": "Deploy web app", "status": "running", "elapsed": 12.4,
			"started": now.Add(-12 * time.Second), "job_type": "run",
			"summary_fields": map[string]any{"created_by": map[string]any{"id": 1, "username": "admin"}},
		}, map[string]any{
			"id": 42, "name": "Deploy web app", "status": "successful", "elapsed": 96.2,
			"started": now.Add(-90 * time.Minute), "job_type": "run",
			"summary_fields": map[string]any{"created_by": map[string]any{"id": 1, "username": "admin"}},
		}, map[string]any{
			// AWX's job list is everyone's, which is the whole difficulty:
			// your own runs are a minority of it.
			"id": 40, "name": "Someone else's run", "status": "failed", "elapsed": 3.0,
			"started": now.Add(-4 * time.Hour), "job_type": "run",
			"summary_fields": map[string]any{"created_by": map[string]any{"id": 2, "username": "colleague"}},
		}})...))
	})
	mux.HandleFunc("/api/v2/jobs/43/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"id": 43, "name": "Deploy web app", "status": "running", "elapsed": 14.1})
	})
	mux.HandleFunc("/api/v2/jobs/42/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 42, "name": "Deploy web app", "status": "successful", "elapsed": 96.2,
			"launch_type": "manual", "extra_vars": "release: v1.4.2\n", "limit": "web",
			"job_tags": "deploy", "skip_tags": "slow",
			"summary_fields": map[string]any{
				"inventory":    map[string]any{"id": 1, "name": "all"},
				"job_template": map[string]any{"id": 8, "name": "Deploy web app"},
				"credentials": []any{
					map[string]any{"id": 5, "name": "prod ssh", "kind": "ssh"},
				},
			},
		})
	})
	// Real AWX rejects format=ansi when the client asks for JSON, and serves
	// nothing useful while a job is still running.
	mux.HandleFunc("/api/v2/jobs/42/stdout/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			http.Error(w, "Not acceptable", http.StatusNotAcceptable)
			return
		}
		fmt.Fprint(w, "PLAY [web] ****\nok: [web-01]\narchived stdout\n")
	})
	mux.HandleFunc("/api/v2/jobs/43/stdout/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	// job_events, paginated by counter__gt like the real API.
	mux.HandleFunc("/api/v2/jobs/43/job_events/", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.Atoi(r.URL.Query().Get("counter__gt"))
		all := []struct {
			counter int
			stdout  string
		}{
			{1, "PLAY [web] ****"},
			{2, ""},
			{3, "TASK [Gathering Facts] ****"},
			{4, "ok: [web-01]"},
			{5, "TASK [Deploy] ****"},
			{6, "changed: [web-01]"},
		}
		var results []any
		for _, e := range all {
			if e.counter > after {
				results = append(results, map[string]any{
					"counter": e.counter, "stdout": e.stdout,
					"start_line": e.counter, "end_line": e.counter,
				})
			}
		}
		write(w, page(results...))
	})
	mux.HandleFunc("/api/v2/jobs/42/job_events/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page())
	})
	mux.HandleFunc("/api/v2/jobs/43/cancel/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	// Template 7 prompts for most ask_* values; template 8 has a survey.
	launchConfig := func(id int, survey bool) map[string]any {
		return map[string]any{
			"can_start_without_user_input": false,
			"passwords_needed_to_start":    []string{},
			"variables_needed_to_start":    []string{},
			"survey_enabled":               survey,
			"inventory_needed_to_start":    false,
			"ask_inventory_on_launch":      id == 7,
			"ask_limit_on_launch":          true,
			"ask_tags_on_launch":           id == 7,
			"ask_variables_on_launch":      true,
			"ask_job_type_on_launch":       id == 7,
			"ask_verbosity_on_launch":      id == 7,
			"ask_timeout_on_launch":        id == 7,
			"ask_diff_mode_on_launch":      id == 7,
			// The catalogue prompts: the template picks from a list of ids.
			"ask_credential_on_launch":            id == 7,
			"ask_execution_environment_on_launch": id == 7,
			"ask_instance_groups_on_launch":       id == 7,
			"ask_labels_on_launch":                id == 7,
			"defaults": map[string]any{
				"limit": "", "job_tags": "", "skip_tags": "", "scm_branch": "",
				"extra_vars": "{}", "job_type": "run", "verbosity": 0,
				"diff_mode": false, "timeout": 0, "forks": 5,
				"inventory": map[string]any{"id": 3, "name": "production"},
				// Real AWX lists the template's own credentials and image
				// here, and an empty list when no instance group is pinned.
				"credentials": []any{
					map[string]any{"id": 39, "name": "web_prod.ssh", "credential_type": 1, "passwords_needed": []string{}},
					// Credential 99 is deliberately absent from the
					// catalogue below, the way a credential past the page cap
					// would be: it must still be kept, not silently dropped.
					map[string]any{"id": 99, "name": "vault.approle", "credential_type": 31, "passwords_needed": []string{}},
				},
				"execution_environment": map[string]any{"id": 14, "name": "ansible-web"},
				"labels":                []any{map[string]any{"id": 1, "name": "nightly"}},
				"instance_groups":       []any{},
			},
		}
	}
	launch := func(id int, survey bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				write(w, launchConfig(id, survey))
				return
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"detail":"bad payload"}`, http.StatusBadRequest)
				return
			}
			mk.mu.Lock()
			mk.launches = append(mk.launches, body)
			mk.mu.Unlock()
			write(w, map[string]any{"id": 43, "name": "Deploy web app", "status": "pending"})
		}
	}
	mux.HandleFunc("/api/v2/job_templates/7/launch/", launch(7, false))
	mux.HandleFunc("/api/v2/job_templates/8/launch/", launch(8, true))
	mux.HandleFunc("/api/v2/job_templates/8/survey_spec/", func(w http.ResponseWriter, r *http.Request) {
		one, ten := 1, 10
		write(w, map[string]any{"name": "Deploy options", "spec": []any{
			map[string]any{"type": "text", "variable": "release", "question_name": "Release tag",
				"question_description": "git tag to deploy", "required": true, "default": ""},
			map[string]any{"type": "integer", "variable": "batch", "question_name": "Batch size",
				"required": false, "default": 2, "min": one, "max": ten},
			map[string]any{"type": "float", "variable": "ratio", "question_name": "Ratio",
				"required": false, "default": 0.5},
			map[string]any{"type": "multiplechoice", "variable": "env", "question_name": "Environment",
				"required": true, "default": "staging", "choices": "staging\nproduction"},
			map[string]any{"type": "multiselect", "variable": "steps", "question_name": "Steps",
				"required": false, "default": []string{"build"}, "choices": []string{"build", "migrate", "smoke"}},
			map[string]any{"type": "password", "variable": "api_key", "question_name": "API key",
				"required": false, "default": ""},
			map[string]any{"type": "textarea", "variable": "notes", "question_name": "Notes",
				"required": false, "default": ""},
		}})
	})
	// Credentials arrive over two pages, so the form only holds the whole
	// catalogue if the client actually follows `next`. kind is the real
	// API's terse value ("ssh" for what the web UI calls "Machine"); the
	// display name lives in summary_fields.credential_type.name instead.
	mux.HandleFunc("/api/v2/credentials/", func(w http.ResponseWriter, r *http.Request) {
		cred := func(id int, name, kind, typeName string) any {
			return map[string]any{"id": id, "name": name, "kind": kind,
				"summary_fields": map[string]any{"credential_type": map[string]any{"name": typeName}}}
		}
		// AWX rejects a filter on kind itself, only credential_type__kind, and
		// an ad hoc command asks for exactly that — machine credentials only.
		if r.URL.Query().Get("credential_type__kind") == "ssh" {
			write(w, map[string]any{"count": 2, "results": []any{
				cred(39, "web_prod.ssh", "ssh", "Machine"), cred(40, "db_prod.ssh", "ssh", "Machine"),
			}})
			return
		}
		if r.URL.Query().Get("page") == "2" {
			write(w, map[string]any{"count": 3, "results": []any{cred(41, "galaxy.token", "galaxy_api_token", "Ansible Galaxy")}})
			return
		}
		write(w, map[string]any{"count": 3, "next": "/api/v2/credentials/?page=2&page_size=200",
			"results": []any{cred(39, "web_prod.ssh", "ssh", "Machine"), cred(40, "db_prod.ssh", "ssh", "Machine")}})
	})
	mux.HandleFunc("/api/v2/execution_environments/", func(w http.ResponseWriter, r *http.Request) {
		write(w,
			page(map[string]any{"id": 14, "name": "ansible-web", "image": "registry/ansible-web:1"},
				map[string]any{"id": 15, "name": "ansible-db", "image": "registry/ansible-db:1"}))
	})
	mux.HandleFunc("/api/v2/instance_groups/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(
			map[string]any{"id": 2, "name": "default"},
			map[string]any{"id": 12, "name": "tower-srv"},
			map[string]any{"id": 13, "name": "tower-k8s", "is_container_group": true},
		))
	})
	mux.HandleFunc("/api/v2/labels/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(
			map[string]any{"id": 1, "name": "nightly"},
			map[string]any{"id": 2, "name": "hotfix"},
		))
	})
	mux.HandleFunc("/api/v2/inventories/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(filtered(r, []any{map[string]any{
			"id": 3, "name": "production", "total_hosts": 12, "total_groups": 4,
			"hosts_with_active_failures": 1,
			// Two sources, so a sync has to start both and not just the first.
			"total_inventory_sources": 2, "has_inventory_sources": true,
			"summary_fields": map[string]any{"organization": map[string]any{"name": "Default"}},
		}, map[string]any{
			// Hosts entered by hand: there is nothing here to sync.
			"id": 4, "name": "handmade", "total_hosts": 2, "total_groups": 0,
			"total_inventory_sources": 0, "has_inventory_sources": false,
			"summary_fields": map[string]any{"organization": map[string]any{"name": "Default"}},
		}})...))
	})
	mux.HandleFunc("/api/v2/inventories/3/hosts/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(
			map[string]any{"id": 11, "name": "web-01", "enabled": true, "description": "frontend"},
			map[string]any{"id": 12, "name": "db-01", "enabled": true, "has_active_failures": true},
		))
	})
	mux.HandleFunc("/api/v2/projects/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(filtered(r, []any{map[string]any{
			"id": 5, "name": "infra", "description": "fleet playbooks",
			"scm_type": "git", "scm_branch": "main",
			"scm_url":              "git@github.com:example/infra.git",
			"scm_refspec":          "+refs/heads/*:refs/remotes/origin/*",
			"scm_revision":         "3f6e384b8694bac33b6215675fca396f2831c2e1",
			"scm_update_on_launch": true, "scm_clean": true, "allow_override": true,
			"scm_update_cache_timeout": 60, "timeout": 0,
			"local_path": "_5__infra",
			"status":     "successful", "last_updated": now.Add(-3 * time.Hour),
			"created": now.Add(-720 * time.Hour),
			"summary_fields": map[string]any{
				"organization": map[string]any{"name": "Default"},
				"credential":   map[string]any{"id": 4, "name": "infra.git", "kind": "scm"},
			},
		}, map[string]any{
			// A manual project: no SCM type, never updated. Its details view
			// must not claim a branch or a revision it does not have.
			"id": 6, "name": "legacy", "status": "",
			"summary_fields": map[string]any{"organization": map[string]any{"name": "Default"}},
		}})...))
	})

	mux.HandleFunc("/api/v2/schedules/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(filtered(r, []any{map[string]any{
			"id": 7, "name": "nightly backup", "description": "Nightly infra backup run",
			"rrule":   "DTSTART:20260101T020000Z RRULE:FREQ=DAILY;INTERVAL=1",
			"enabled": true, "next_run": now.Add(10 * time.Hour), "timezone": "UTC",
			"unified_job_template": 1,
			"created":              now.Add(-720 * time.Hour), "modified": now.Add(-24 * time.Hour),
			"summary_fields": map[string]any{
				"unified_job_template": map[string]any{"id": 1, "name": "Deploy fleet", "unified_job_type": "job"},
			},
		}, map[string]any{
			"id": 8, "name": "Cleanup Job Schedule", "description": "Automatically Generated Schedule",
			"rrule":   "DTSTART:20260101T020000Z RRULE:FREQ=WEEKLY;INTERVAL=1;BYDAY=SU",
			"enabled": false, "next_run": nil, "timezone": "UTC",
			"unified_job_template": 2,
			"created":              now.Add(-720 * time.Hour), "modified": now.Add(-720 * time.Hour),
			"summary_fields": map[string]any{
				"unified_job_template": map[string]any{"id": 2, "name": "Cleanup Job Details", "unified_job_type": "system_job"},
			},
		}})...))
	})
	mux.HandleFunc("/api/v2/schedules/7/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"id": 7, "enabled": true})
	})
	mux.HandleFunc("/api/v2/schedules/8/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"id": 8, "enabled": false})
	})

	// Workflow job templates: 20 has plain prompts, 21 has a survey. Neither
	// asks for a credential, execution environment, job type or verbosity —
	// real AWX never offers those on a workflow's own launch form.
	mux.HandleFunc("/api/v2/workflow_job_templates/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(filtered(r, []any{map[string]any{
			"id": 20, "name": "Nightly pipeline", "description": "Backup, then rotate certs",
			"status": "never updated",
			"summary_fields": map[string]any{
				"organization": map[string]any{"name": "Default"},
				"inventory":    map[string]any{"name": "production"},
			},
		}, map[string]any{
			"id": 21, "name": "Fleet rollout", "description": "Staged fleet-wide rollout",
			"survey_enabled": true, "status": "successful", "last_job_run": now.Add(-3 * time.Hour),
			"summary_fields": map[string]any{
				"organization": map[string]any{"name": "Default"},
				"inventory":    map[string]any{"name": "production"},
			},
		}})...))
	})
	workflowLaunch := func(id int, survey bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				write(w, map[string]any{
					"can_start_without_user_input": true,
					"variables_needed_to_start":    []string{},
					"survey_enabled":               survey,
					"ask_inventory_on_launch":      true,
					"ask_limit_on_launch":          true,
					"ask_labels_on_launch":         id == 21,
					"defaults": map[string]any{
						"limit": "", "scm_branch": "", "extra_vars": "{}",
						"inventory": map[string]any{"id": 3, "name": "production"},
						"labels":    []any{},
					},
				})
				return
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"detail":"bad payload"}`, http.StatusBadRequest)
				return
			}
			mk.mu.Lock()
			mk.launches = append(mk.launches, body)
			mk.mu.Unlock()
			write(w, map[string]any{"id": 44, "type": "workflow_job", "name": "Fleet rollout", "status": "pending"})
		}
	}
	mux.HandleFunc("/api/v2/workflow_job_templates/20/launch/", workflowLaunch(20, false))
	mux.HandleFunc("/api/v2/workflow_job_templates/21/launch/", workflowLaunch(21, true))
	mux.HandleFunc("/api/v2/ad_hoc_commands/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"detail":"bad payload"}`, http.StatusBadRequest)
			return
		}
		mk.mu.Lock()
		mk.launches = append(mk.launches, body)
		mk.mu.Unlock()
		write(w, map[string]any{"id": 45, "type": "ad_hoc_command", "name": "shell", "status": "pending"})
	})
	mux.HandleFunc("/api/v2/ad_hoc_commands/45/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"id": 45, "type": "ad_hoc_command", "name": "shell", "status": "successful"})
	})
	mux.HandleFunc("/api/v2/ad_hoc_commands/45/stdout/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "application/json" {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		fmt.Fprint(w, "PLAY [ad hoc]\n")
	})
	mux.HandleFunc("/api/v2/workflow_job_templates/21/survey_spec/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"name": "Rollout options", "spec": []any{
			map[string]any{"type": "text", "variable": "wave", "question_name": "Rollout wave",
				"required": true, "default": ""},
		}})
	})
	// The workflow job the unified list already carries (#11) and the one a
	// launch creates (#44) both need their own detail endpoint: a workflow
	// job has no stdout, so awxtui reads it through the same detail view as
	// a job template's launch details rather than /stdout/.
	mux.HandleFunc("/api/v2/workflow_jobs/11/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 11, "type": "workflow_job", "name": "Nightly pipeline", "status": "running",
			"elapsed": 300.0, "started": now.Add(-3 * time.Hour), "extra_vars": "{}",
			"summary_fields": map[string]any{
				"inventory":             map[string]any{"id": 3, "name": "production"},
				"workflow_job_template": map[string]any{"id": 20, "name": "Nightly pipeline"},
			},
		})
	})
	mux.HandleFunc("/api/v2/workflow_jobs/11/cancel/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/api/v2/workflow_jobs/44/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 44, "type": "workflow_job", "name": "Fleet rollout", "status": "pending",
			"summary_fields": map[string]any{
				"workflow_job_template": map[string]any{"id": 21, "name": "Fleet rollout"},
			},
		})
	})
	// A workflow job's nodes: one already finished, one still running, one
	// AWX has not reached yet (job: null), and one on the untaken branch of
	// a success/failure edge (do_not_run: true) — real AWX creates every
	// node up front and fills job/summary_fields.job in only once it starts.
	node := func(id int, name string, jobID int, status string, doNotRun bool) any {
		n := map[string]any{
			"id": id, "job": jobID, "do_not_run": doNotRun,
			"summary_fields": map[string]any{
				"unified_job_template": map[string]any{"name": name, "unified_job_type": "job"},
			},
		}
		if jobID != 0 {
			n["summary_fields"].(map[string]any)["job"] = map[string]any{
				"id": jobID, "name": name, "status": status,
			}
		}
		return n
	}
	mux.HandleFunc("/api/v2/workflow_jobs/11/workflow_nodes/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(
			node(101, "backup fleet", 91, "successful", false),
			node(102, "rotate certs", 92, "running", false),
			node(103, "notify on failure", 0, "", true),
			node(104, "smoke test", 0, "", false),
		))
	})
	mux.HandleFunc("/api/v2/workflow_jobs/44/workflow_nodes/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page(node(201, "rollout wave 1", 0, "", false)))
	})

	// ---- syncing ----
	//
	// A project update and an inventory sync are separate collections from
	// /api/v2/jobs/, with their own detail, stdout, events and cancel
	// endpoints. Their events live under /events/, not /job_events/.
	update := func(id int, kind string, name string) map[string]any {
		return map[string]any{
			"id": id, "type": kind, "name": name, "status": "running",
			"started": now.Add(-2 * time.Second), "elapsed": 2.0,
			"summary_fields": map[string]any{"created_by": map[string]any{"id": 1, "username": "admin"}},
		}
	}
	mux.HandleFunc("/api/v2/projects/5/update/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			write(w, map[string]any{"can_update": true})
			return
		}
		write(w, update(12, "project_update", "infra"))
	})
	// A manual project cannot be updated, and AWX refuses the POST outright
	// rather than answering with an update that will never run.
	mux.HandleFunc("/api/v2/projects/6/update/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			write(w, map[string]any{"can_update": false})
			return
		}
		http.Error(w, `{"detail":"Method \"POST\" not allowed."}`, http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/api/v2/project_updates/12/", func(w http.ResponseWriter, r *http.Request) {
		write(w, update(12, "project_update", "infra"))
	})
	mux.HandleFunc("/api/v2/project_updates/12/stdout/", func(w http.ResponseWriter, r *http.Request) {
		// Still running, so there is no collated stdout yet.
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/api/v2/project_updates/12/events/", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.Atoi(r.URL.Query().Get("counter__gt"))
		var results []any
		for i, line := range []string{
			"PLAY [Update source tree if necessary] ****",
			"TASK [Update project using git] ****",
			"changed: [localhost]",
		} {
			if counter := i + 1; counter > after {
				results = append(results, map[string]any{
					"counter": counter, "stdout": line,
					"start_line": counter, "end_line": counter,
				})
			}
		}
		write(w, page(results...))
	})
	mux.HandleFunc("/api/v2/project_updates/12/cancel/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	source := func(id int, name, path, status string) any {
		return map[string]any{
			"id": id, "name": name, "source": "scm", "source_path": path,
			"status": status, "last_updated": now.Add(-90 * time.Minute), "update_on_launch": true,
			"summary_fields": map[string]any{"source_project": map[string]any{"name": "infra"}},
		}
	}
	mux.HandleFunc("/api/v2/inventories/3/inventory_sources/", func(w http.ResponseWriter, r *http.Request) {
		// One source healthy, one failed: the detail view has to tell them
		// apart, which the list's aggregate "sources" column cannot.
		write(w, page(
			source(10, "git", "inventories/prod.yml", "successful"),
			source(11, "git", "inventories/edge.yml", "failed"),
		))
	})
	mux.HandleFunc("/api/v2/inventories/4/inventory_sources/", func(w http.ResponseWriter, r *http.Request) {
		write(w, page())
	})
	for sourceID, updateID := range map[int]int{10: 21, 11: 22} {
		id := updateID
		mux.HandleFunc(fmt.Sprintf("/api/v2/inventory_sources/%d/update/", sourceID),
			func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					write(w, map[string]any{"can_update": true})
					return
				}
				write(w, update(id, "inventory_update", "production - git"))
			})
	}
	mux.HandleFunc("/api/v2/inventory_updates/21/", func(w http.ResponseWriter, r *http.Request) {
		write(w, update(21, "inventory_update", "production - git"))
	})
	mux.HandleFunc("/api/v2/inventory_updates/21/stdout/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/api/v2/inventory_updates/21/events/", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.Atoi(r.URL.Query().Get("counter__gt"))
		var results []any
		if after < 1 {
			results = append(results, map[string]any{
				"counter": 1, "stdout": "Updating inventory 0: production",
				"start_line": 0, "end_line": 1,
			})
		}
		write(w, page(results...))
	})
	// Every write is recorded, so a test can assert both what was changed and
	// that nothing was.
	mk.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mk.mu.Lock()
			mk.posts = append(mk.posts, r.Method+" "+r.URL.Path)
			mk.mu.Unlock()
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(mk.Close)
	return mk
}

// step applies a message and drains the returned command synchronously.
func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	return settle(t, m, msg, 0)
}

// settle applies msg and keeps applying the messages its commands produce,
// mimicking the Bubble Tea runtime closely enough to assert on end state.
func settle(t *testing.T, m Model, msg tea.Msg, depth int) Model {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(Model)
	// Deep enough for a full pagination chain (bounded by maxPages in the
	// model), while still catching a genuinely unbounded message loop.
	if depth > maxPages+10 {
		t.Fatalf("message loop did not settle after %d rounds", depth)
	}
	for _, out := range drain(cmd) {
		switch out.(type) {
		case tickMsg, spinner.TickMsg, tea.QuitMsg, nil:
			continue // timers, not state the tests care about
		}
		m = settle(t, m, out, depth+1)
	}
	return m
}

// Tests override the real, user-tunable searchDelay so the mock-server suite
// stays fast regardless of how long a real user's typing pause is set to.
// Live tests exercise the real value against real AWX timing.
func init() {
	if os.Getenv("AWXTUI_LIVE") == "" {
		searchDelay = 20 * time.Millisecond
	}
}

// drainTimeout bounds how long drain waits for one command. Against the mock
// server real work is local HTTP (sub-millisecond), so anything slower is a UI
// timer such as a poll tick, which the tests do not care about. Live tests talk
// to a real instance over the network and need a real timeout.
var drainTimeout = func() time.Duration {
	if os.Getenv("AWXTUI_LIVE") != "" {
		return 60 * time.Second
	}
	return 400 * time.Millisecond
}()

// drain executes a command (flattening batches) and collects its messages,
// skipping commands that are really just timers.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(drainTimeout):
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drain(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// typeText sends each rune of s as a key press. Only the last keystroke's
// commands are run, which is what happens in practice too: the debounced
// search from an earlier keystroke is superseded before it fires.
func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	runes := []rune(s)
	for _, r := range runes[:max(len(runes)-1, 0)] {
		next, _ := m.Update(key(string(r)))
		m = next.(Model)
	}
	if len(runes) > 0 {
		m = step(t, m, key(string(runes[len(runes)-1])))
	}
	return m
}

// fieldByKey finds a form field, failing the test when it is missing.
func fieldByKey(t *testing.T, m *Model, key string) *formField {
	t.Helper()
	for i := range m.form.fields {
		if m.form.fields[i].key == key {
			return &m.form.fields[i]
		}
	}
	t.Fatalf("form has no %q field; has %v", key, formKeys(m))
	return nil
}

func formKeys(m *Model) []string {
	var out []string
	for i := range m.form.fields {
		out = append(out, m.form.fields[i].key)
	}
	return out
}

// focusField moves the form cursor onto a named field.
func focusField(t *testing.T, m Model, key string) Model {
	t.Helper()
	for i := range m.form.fields {
		if m.form.fields[i].key == key {
			for m.form.cursor < i {
				m = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
			}
			for m.form.cursor > i {
				m = step(t, m, tea.KeyMsg{Type: tea.KeyUp})
			}
			return m
		}
	}
	t.Fatalf("cannot focus %q; form has %v", key, formKeys(&m))
	return m
}

func TestFlows(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, m.connect())

	if m.user != "admin" {
		t.Fatalf("expected connected user admin, got %q (err: %v)", m.user, m.err)
	}
	if got := len(m.rows[tabTemplates]); got != 2 {
		t.Fatalf("expected 2 templates, got %d (err: %v)", got, m.err)
	}
	show(t, "templates", m.View())

	// filter down to one template
	m = step(t, m, key("/"))
	for _, r := range "certs" {
		m = step(t, m, key(string(r)))
	}
	if got := len(m.visible(tabTemplates)); got != 0 {
		t.Fatalf("filter 'certs' should not match template names, got %d", got)
	}
	m = step(t, m, key("esc"))
	if m.filters[tabTemplates] != "" {
		t.Fatalf("esc should clear the filter, got %q", m.filters[tabTemplates])
	}

	// launch the selected template -> lands in the output view
	m = step(t, m, key("enter"))
	if m.mode != modeLaunch {
		t.Fatalf("enter on a template should open the launch modal, got mode %v", m.mode)
	}
	show(t, "launch modal", m.View())
	m = step(t, m, key("enter"))
	if m.err != nil {
		t.Fatalf("launch failed: %v", m.err)
	}
	if m.mode != modeOutput || m.outputJob.ID != 43 {
		t.Fatalf("launch should open output for job 43, got mode %v job %d (err %v)", m.mode, m.outputJob.ID, m.err)
	}
	if !strings.Contains(m.outputText, "Gathering Facts") {
		t.Fatalf("expected stdout to be fetched, got %q", m.outputText)
	}
	show(t, "job output", m.View())

	// back to the list, then across every tab
	m = step(t, m, key("esc"))
	if m.mode != modeList {
		t.Fatalf("esc should return to the list, got %v", m.mode)
	}
	for _, tabKey := range []string{"2", "3", "4"} {
		m = step(t, m, key(tabKey))
		if m.err != nil {
			t.Fatalf("tab %s failed to load: %v", tabKey, m.err)
		}
		show(t, "tab "+tabKey, m.View())
	}

	// inventory drill-down: enter opens details, h opens its hosts
	m = step(t, m, key("3"))
	m = step(t, m, key("enter"))
	if m.mode != modeInventory {
		t.Fatalf("expected inventory details, got mode %v (err %v)", m.mode, m.err)
	}
	show(t, "inventory details", m.View())
	m = step(t, m, key("h"))
	if m.mode != modeHosts || len(m.hostRows) != 2 {
		t.Fatalf("expected 2 hosts in drill-down, got mode %v rows %d (err %v)", m.mode, len(m.hostRows), m.err)
	}
	show(t, "hosts", m.View())

	// jobs tab: cancel a running job
	m = step(t, m, key("esc")) // hosts -> inventory details
	m = step(t, m, key("esc")) // details -> list
	m = step(t, m, key("2"))
	m = step(t, m, key("c"))
	if !strings.Contains(m.notice, "cancel requested") {
		t.Fatalf("expected cancel notice, got %q (err %v)", m.notice, m.err)
	}
}

// A running job has no /stdout/ content but does have job_events; this is the
// case that used to show a permanent "waiting for output…".
func TestRunningJobOutputComesFromEvents(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, m.connect())
	m = step(t, m, key("2")) // jobs tab, running job 43 is first
	m = step(t, m, key("enter"))

	if m.err != nil {
		t.Fatalf("opening output errored: %v", m.err)
	}
	if !m.outputJob.IsRunning() {
		t.Fatalf("expected to open the running job, got %q", m.outputJob.Status)
	}
	for _, want := range []string{"PLAY [web]", "Gathering Facts", "changed: [web-01]"} {
		if !strings.Contains(m.outputText, want) {
			t.Errorf("output missing %q; got:\n%s", want, m.outputText)
		}
	}
	if m.outputCounter != 6 {
		t.Errorf("expected to tail up to counter 6, got %d", m.outputCounter)
	}
	// A poll tick must not duplicate the lines already shown.
	before := m.outputText
	m = step(t, m, tickMsg(time.Now()))
	if m.outputText != before {
		t.Errorf("poll duplicated output:\nbefore:\n%s\nafter:\n%s", before, m.outputText)
	}
}

// Finished jobs whose events were pruned still fall back to /stdout/.
func TestFinishedJobFallsBackToStdout(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "test-token", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, m.connect())
	m = step(t, m, key("2"))
	m = step(t, m, key("down"))
	m = step(t, m, key("enter"))

	if m.outputJob.ID != 42 {
		t.Fatalf("expected job 42, got %d", m.outputJob.ID)
	}
	if !strings.Contains(m.outputText, "archived stdout") {
		t.Fatalf("expected stdout fallback, got %q (err %v)", m.outputText, m.err)
	}
}

func TestBadTokenSurfacesError(t *testing.T) {
	srv := mockAWX(t)
	m := New(awx.New(srv.URL, "nope", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = step(t, m, m.connect())
	if m.err == nil || !strings.Contains(m.err.Error(), "401") {
		t.Fatalf("expected a 401 error to surface, got %v", m.err)
	}
	show(t, "auth error", m.View())
}

func TestEveryViewRendersWithinTerminalBounds(t *testing.T) {
	srv := mockAWX(t)
	for _, size := range [][2]int{{80, 24}, {120, 40}, {200, 50}} {
		m := New(awx.New(srv.URL, "test-token", false))
		m = step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = step(t, m, m.connect())
		for _, k := range []string{"1", "2", "d", "esc", "m", "p", "m", "3", "enter", "h", "esc", "3", "enter", "a", "esc", "4", "?", "4", "enter", "G", "esc", "5", "enter", "t", "esc", "6", "enter", "G", "esc", "2", "f", "down", "right", "right", "space", "down", "enter", "2", "f", "d", "e", "p", "l", "o", "y", "esc", "G", "enter", "esc"} {
			m = step(t, m, key(k))
			out := m.View()
			for i, line := range strings.Split(out, "\n") {
				if w := lineWidth(line); w > size[0] {
					t.Errorf("%dx%d key %q: line %d is %d cols wide", size[0], size[1], k, i, w)
				}
			}
			if lines := strings.Count(out, "\n") + 1; lines > size[1] {
				t.Errorf("%dx%d key %q: view is %d lines, terminal has %d", size[0], size[1], k, lines, size[1])
			}
		}
	}
}

func lineWidth(s string) int { return len([]rune(stripANSI(s))) }

// show prints a rendered view when AWXTUI_SHOW is set, for eyeballing layout.
func show(t *testing.T, label, view string) {
	if os.Getenv("AWXTUI_SHOW") == "" {
		return
	}
	fmt.Printf("\n=== %s ===\n%s\n", label, view)
}
