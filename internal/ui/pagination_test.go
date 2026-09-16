package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// pagedMock serves lists of a configurable size, honouring page and page_size
// the way AWX does, and counts the requests it answers.
type pagedMock struct {
	*httptest.Server
	mu       sync.Mutex
	requests map[string]int
	jobs     []any // mutable so a refresh can return something new
}

func (p *pagedMock) count(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[path]
}

// paginate replies like an AWX list endpoint.
func (p *pagedMock) paginate(w http.ResponseWriter, r *http.Request, items []any) {
	p.mu.Lock()
	p.requests[r.URL.Path]++
	p.mu.Unlock()

	// AWX filters server-side before paging; match on name like it does.
	if q := r.URL.Query().Get("search"); q != "" {
		var hits []any
		for _, it := range items {
			m, _ := it.(map[string]any)
			name, _ := m["name"].(string)
			desc, _ := m["description"].(string)
			hay := strings.ToLower(name + " " + desc)
			if strings.Contains(hay, strings.ToLower(q)) {
				hits = append(hits, it)
			}
		}
		items = hits
	}

	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if size <= 0 {
		size = 25
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	start := min((page-1)*size, len(items))
	end := min(start+size, len(items))

	var next any // AWX sends null on the last page
	if end < len(items) {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(page+1))
		next = r.URL.Path + "?" + q.Encode()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"count": len(items), "next": next, "results": items[start:end],
	})
}

func newPagedMock(t *testing.T, templates, jobs, hosts int) *pagedMock {
	t.Helper()
	p := &pagedMock{requests: map[string]int{}}

	tpls := make([]any, 0, templates)
	for i := 0; i < templates; i++ {
		tpls = append(tpls, map[string]any{
			"id": 1000 + i, "name": fmt.Sprintf("template-%03d", i),
			// Only AWX can see this; the TUI never renders descriptions.
			"description":    fmt.Sprintf("handles widget-%03d rollout", i),
			"summary_fields": map[string]any{"project": map[string]any{"name": "infra"}},
		})
	}
	for i := 0; i < jobs; i++ {
		p.jobs = append(p.jobs, map[string]any{
			"id": 9000 - i, "name": fmt.Sprintf("job-%03d", i),
			"status": "successful", "elapsed": 1.0,
		})
	}
	hostList := make([]any, 0, hosts)
	for i := 0; i < hosts; i++ {
		hostList = append(hostList, map[string]any{
			"id": 500 + i, "name": fmt.Sprintf("host-%03d", i), "enabled": true,
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/me/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 1, "results": []any{map[string]any{"username": "admin"}}})
	})
	mux.HandleFunc("/api/v2/job_templates/", func(w http.ResponseWriter, r *http.Request) {
		p.paginate(w, r, tpls)
	})
	mux.HandleFunc("/api/v2/jobs/", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		current := append([]any{}, p.jobs...)
		p.mu.Unlock()
		p.paginate(w, r, current)
	})
	mux.HandleFunc("/api/v2/inventories/", func(w http.ResponseWriter, r *http.Request) {
		p.paginate(w, r, []any{map[string]any{
			"id": 3, "name": "production", "total_hosts": hosts,
			"summary_fields": map[string]any{"organization": map[string]any{"name": "Default"}},
		}})
	})
	mux.HandleFunc("/api/v2/inventories/3/hosts/", func(w http.ResponseWriter, r *http.Request) {
		p.paginate(w, r, hostList)
	})
	mux.HandleFunc("/api/v2/projects/", func(w http.ResponseWriter, r *http.Request) {
		p.paginate(w, r, []any{})
	})

	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

func pagedModel(t *testing.T, p *pagedMock, w, h int) Model {
	t.Helper()
	m := New(awx.New(p.URL, "t", false))
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return step(t, m, m.connect())
}

// Lists load one page at a time; search goes to AWX so it still covers every
// record, not just the pages already pulled.
func TestTemplatesSearchServerSide(t *testing.T) {
	p := newPagedMock(t, 450, 0, 0)
	m := pagedModel(t, p, 120, 30)

	if got := len(m.rows[tabTemplates]); got != 200 {
		t.Fatalf("loaded %d templates, want just the first page of 200", got)
	}
	if m.count[tabTemplates] != 450 {
		t.Errorf("count = %d, want 450", m.count[tabTemplates])
	}
	if got := p.count("/api/v2/job_templates/"); got != 1 {
		t.Errorf("made %d requests at startup, want 1", got)
	}

	// template-449 only exists on the last page, which was never fetched.
	m = step(t, m, key("/"))
	m = typeText(t, m, "template-449")
	if got := len(m.visible(tabTemplates)); got != 1 {
		t.Fatalf("search matched %d rows, want the 1 on the unfetched page", got)
	}
	if m.serverQuery[tabTemplates] != "template-449" {
		t.Errorf("serverQuery = %q, want the search to have gone to AWX",
			m.serverQuery[tabTemplates])
	}
	if got := m.countLabel(tabTemplates); got != "1 matching" {
		t.Errorf("count label = %q, want %q", got, "1 matching")
	}

	// Clearing the search restores the unfiltered first page.
	m = step(t, m, key("esc"))
	if got := len(m.rows[tabTemplates]); got != 200 {
		t.Errorf("after clearing the search, %d rows, want the first page back", got)
	}
	if m.serverQuery[tabTemplates] != "" {
		t.Errorf("serverQuery = %q, want it cleared", m.serverQuery[tabTemplates])
	}
}

// A search that matches more than a page still pages in on scroll.
func TestSearchResultsPageIn(t *testing.T) {
	p := newPagedMock(t, 450, 0, 0)
	m := pagedModel(t, p, 120, 30)
	m = step(t, m, key("/"))
	m = typeText(t, m, "template-1") // 100 matches: template-100..199
	m = step(t, m, key("enter"))     // leave the search box

	if got := len(m.rows[tabTemplates]); got != 100 {
		t.Fatalf("search loaded %d rows, want all 100 matches", got)
	}
	if got := m.countLabel(tabTemplates); got != "100 matching" {
		t.Errorf("count label = %q", got)
	}
}

// Replies to a search the user has already typed past must be ignored.
func TestStaleSearchRepliesAreDropped(t *testing.T) {
	p := newPagedMock(t, 450, 0, 0)
	m := pagedModel(t, p, 120, 30)
	m = step(t, m, key("/"))
	m = typeText(t, m, "template-449")

	rowsBefore := len(m.rows[tabTemplates])
	// A late reply from an earlier keystroke arrives.
	stale := templatesMsg{
		pageMeta: pageMeta{query: "templ", count: 450, seq: m.searchSeq[tabTemplates] - 1},
		items:    make([]awx.JobTemplate, 17),
	}
	m = step(t, m, stale)

	if got := len(m.rows[tabTemplates]); got != rowsBefore {
		t.Errorf("stale reply overwrote the rows: %d rows, want %d", got, rowsBefore)
	}
	if m.serverQuery[tabTemplates] != "template-449" {
		t.Errorf("stale reply changed the active query to %q", m.serverQuery[tabTemplates])
	}
}

// A complete list whose local filter matches nothing still asks AWX, which
// searches fields the TUI never displays.
func TestCompleteListFallsBackToServerSearch(t *testing.T) {
	p := newPagedMock(t, 5, 0, 0)
	m := pagedModel(t, p, 120, 30)
	if m.next[tabTemplates] != "" {
		t.Fatal("5 templates should arrive in a single complete page")
	}

	m = step(t, m, key("/"))
	m = typeText(t, m, "widget-003") // only in the description

	if got := len(m.visible(tabTemplates)); got != 1 {
		t.Fatalf("matched %d rows, want the 1 whose description matches", got)
	}
	if m.serverQuery[tabTemplates] != "widget-003" {
		t.Errorf("expected a fallback server search, serverQuery = %q",
			m.serverQuery[tabTemplates])
	}
}

// A list that is already complete is filtered locally, without a round trip.
func TestCompleteListFiltersLocally(t *testing.T) {
	p := newPagedMock(t, 5, 0, 0)
	m := pagedModel(t, p, 120, 30)
	if m.next[tabTemplates] != "" {
		t.Fatal("5 templates should arrive in a single complete page")
	}
	before := p.count("/api/v2/job_templates/")

	m = step(t, m, key("/"))
	m = typeText(t, m, "template-003")
	if got := len(m.visible(tabTemplates)); got != 1 {
		t.Errorf("local filter matched %d rows, want 1", got)
	}
	if got := p.count("/api/v2/job_templates/"); got != before {
		t.Errorf("made %d extra requests filtering a complete list", got-before)
	}
}

// Job lists are unbounded, so they page in as the cursor approaches the end.
func TestJobsPageInOnDemand(t *testing.T) {
	p := newPagedMock(t, 1, 250, 0)
	m := pagedModel(t, p, 120, 30)
	m = step(t, m, key("2"))

	if got := len(m.rows[tabJobs]); got != 100 {
		t.Fatalf("first jobs page loaded %d rows, want 100", got)
	}
	if m.count[tabJobs] != 250 {
		t.Errorf("count = %d, want 250", m.count[tabJobs])
	}
	if label := m.countLabel(tabJobs); label != "100 of 250" {
		t.Errorf("count label = %q, want %q", label, "100 of 250")
	}

	m = step(t, m, key("G")) // jump to the end
	if got := len(m.rows[tabJobs]); got != 200 {
		t.Fatalf("after scrolling to the end, %d rows loaded, want 200", got)
	}
	m = step(t, m, key("G"))
	if got := len(m.rows[tabJobs]); got != 250 {
		t.Fatalf("second scroll loaded %d rows, want all 250", got)
	}
	if m.next[tabJobs] != "" {
		t.Errorf("next = %q, want empty at the end of the list", m.next[tabJobs])
	}
	// Paging must not have re-read the same page over and over.
	if got := p.count("/api/v2/jobs/"); got > 4 {
		t.Errorf("made %d job requests for 3 pages", got)
	}
}

// The background refresh re-reads page one; it must not discard later pages.
func TestJobsRefreshKeepsPagedRows(t *testing.T) {
	p := newPagedMock(t, 1, 250, 0)
	m := pagedModel(t, p, 120, 30)
	m = step(t, m, key("2"))
	m = step(t, m, key("G")) // load a second page
	if got := len(m.rows[tabJobs]); got != 200 {
		t.Fatalf("expected 200 rows loaded, got %d", got)
	}

	// A new job appears and an old one changes state.
	p.mu.Lock()
	p.jobs[0].(map[string]any)["status"] = "failed"
	p.jobs = append([]any{map[string]any{
		"id": 9999, "name": "brand-new", "status": "running", "elapsed": 2.0,
	}}, p.jobs...)
	p.mu.Unlock()

	m.lastJobsPull = time.Time{} // force the refresh on the next tick
	m = step(t, m, tickMsg(time.Now()))

	if got := len(m.rows[tabJobs]); got != 201 {
		t.Fatalf("after refresh %d rows, want 201 (200 paged + 1 new)", got)
	}
	if m.jobs[0].ID != 9999 {
		t.Errorf("newest job is #%d, want the new #9999 first", m.jobs[0].ID)
	}
	if m.jobs[1].Status != "failed" {
		t.Errorf("refreshed status = %q, want failed", m.jobs[1].Status)
	}
}

// An instance with far more pages than we will ever read must not be walked
// forever, however hard the user scrolls.
func TestPageCapStopsRunawayPagination(t *testing.T) {
	var mu sync.Mutex
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/me/" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": 1, "results": []any{map[string]any{"username": "admin"}}})
			return
		}
		mu.Lock()
		requests++
		mu.Unlock()
		// Always claims there is another page.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 1000000,
			"next":  "/api/v2/job_templates/?page=99999&page_size=200",
			"results": []any{map[string]any{
				"id": 1, "name": "endless", "summary_fields": map[string]any{}}},
		})
	}))
	defer srv.Close()

	m := New(awx.New(srv.URL, "t", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = step(t, m, m.connect())

	// Scroll to the end far more times than the cap allows.
	for i := 0; i < maxPages*2; i++ {
		m = step(t, m, key("G"))
	}

	if m.pages[tabTemplates] != maxPages {
		t.Errorf("stopped at %d pages, want the cap of %d", m.pages[tabTemplates], maxPages)
	}
	mu.Lock()
	got := requests
	mu.Unlock()
	if got > maxPages {
		t.Errorf("made %d requests, cap is %d", got, maxPages)
	}
	// The user needs to know the list is incomplete.
	if label := m.countLabel(tabTemplates); !strings.Contains(label, "page limit") {
		t.Errorf("count label = %q, want it to mention the page limit", label)
	}
}

// Drilling into a large inventory pages its hosts in as well.
func TestHostsPageInOnDemand(t *testing.T) {
	p := newPagedMock(t, 1, 1, 300)
	m := pagedModel(t, p, 120, 30)
	m = step(t, m, key("3"))
	m = step(t, m, key("enter"))

	if m.mode != modeHosts {
		t.Fatalf("expected the hosts view, got %v (err %v)", m.mode, m.err)
	}
	if got := len(m.hostRows); got != 200 {
		t.Fatalf("first hosts page loaded %d rows, want 200", got)
	}
	if m.hostCount != 300 {
		t.Errorf("host count = %d, want 300", m.hostCount)
	}
	for i := 0; i < 200; i++ {
		m = step(t, m, key("j"))
	}
	if got := len(m.hostRows); got != 300 {
		t.Errorf("after scrolling, %d host rows, want all 300", got)
	}
}

// Filtering jobs asks AWX, so a match on a page we never fetched still shows.
func TestJobsFilterSearchesServerSide(t *testing.T) {
	p := newPagedMock(t, 1, 250, 0)
	m := pagedModel(t, p, 120, 30)
	m = step(t, m, key("2"))
	m = step(t, m, key("/"))
	m = typeText(t, m, "job-249") // last job, on the third page

	if got := len(m.visible(tabJobs)); got != 1 {
		t.Fatalf("search matched %d rows, want 1 (loaded %d of %d)",
			got, len(m.rows[tabJobs]), m.count[tabJobs])
	}
	if m.serverQuery[tabJobs] != "job-249" {
		t.Errorf("serverQuery = %q", m.serverQuery[tabJobs])
	}
}

// The count label tells the user how much of a list is actually loaded.
func TestCountLabelReflectsPagingState(t *testing.T) {
	p := newPagedMock(t, 450, 250, 0)
	m := pagedModel(t, p, 120, 30)

	if got := m.countLabel(tabTemplates); got != "200 of 450" {
		t.Errorf("partly loaded label = %q, want %q", got, "200 of 450")
	}
	m = step(t, m, key("2"))
	if got := m.countLabel(tabJobs); got != "100 of 250" {
		t.Errorf("partly loaded label = %q, want %q", got, "100 of 250")
	}
	m = step(t, m, key("1"))
	m = step(t, m, key("/"))
	m = typeText(t, m, "template-1")
	// Names are zero-padded, so "template-1" matches the 100 in template-1xx.
	if got := m.countLabel(tabTemplates); got != "100 matching" {
		t.Errorf("searched label = %q, want %q", got, "100 matching")
	}
}
