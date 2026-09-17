package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/railadeividas/awxtui/internal/awx"
)

// playOutput is realistic play output, including the ANSI colour AWX serves
// and enough host lines that the result cannot fit on one screen — otherwise
// the scrolling assertions below would pass trivially.
var playOutput = buildPlayOutput()

func buildPlayOutput() string {
	var b strings.Builder
	hosts := func(n int, format string) {
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, format, i)
		}
	}
	b.WriteString("PLAY [web] *********************************************************************\n\n")
	b.WriteString("TASK [Gathering Facts] *********************************************************\n")
	hosts(30, "\x1b[0;32mok: [web-%02d]\x1b[0m\n")
	b.WriteString("\nTASK [deploy : Copy release] ***************************************************\n")
	hosts(30, "\x1b[0;33mchanged: [web-%02d]\x1b[0m\n")
	b.WriteString("\x1b[0;31mfatal: [web-99]: FAILED! => {\"changed\": false, \"msg\": \"disk full\"}\x1b[0m\n")
	b.WriteString("\nTASK [deploy : Restart service] ************************************************\n")
	hosts(30, "\x1b[0;36mskipping: [web-%02d]\x1b[0m\n")
	b.WriteString("\nTASK [smoke : Check health] ****************************************************\n")
	hosts(30, "\x1b[0;32mok: [web-%02d]\x1b[0m\n")
	b.WriteString("\x1b[0;31mfatal: [web-98]: FAILED! => {\"changed\": false, \"msg\": \"connection refused\"}\x1b[0m\n")
	b.WriteString("\nPLAY RECAP *********************************************************************\n")
	b.WriteString("\x1b[0;31mweb-01\x1b[0m : ok=3 changed=1 unreachable=0 failed=1\n")
	return b.String()
}

// outputMock serves one finished job whose stdout is the play above.
func outputMock(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/api/v2/me/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 1, "results": []any{map[string]any{"username": "admin"}}})
	})
	mux.HandleFunc("/api/v2/job_templates/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 0, "results": []any{}})
	})
	job := map[string]any{"id": 77, "name": "Deploy web app", "status": "failed", "elapsed": 12.0}
	jobList := func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"count": 1, "results": []any{job}})
	}
	mux.HandleFunc("/api/v2/jobs/", jobList)
	mux.HandleFunc("/api/v2/unified_jobs/", jobList)
	mux.HandleFunc("/api/v2/jobs/77/", func(w http.ResponseWriter, r *http.Request) {
		write(w, job)
	})
	mux.HandleFunc("/api/v2/jobs/77/stdout/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, playOutput)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// openOutputView connects and opens the output of the one job.
func openOutputView(t *testing.T, srv *httptest.Server) Model {
	t.Helper()
	m := New(awx.New(srv.URL, "t", false))
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = step(t, m, m.connect())
	m = step(t, m, key("2"))
	m = step(t, m, key("enter"))
	if m.mode != modeOutput {
		t.Fatalf("expected the output view, got %v (err %v)", m.mode, m.err)
	}
	if !strings.Contains(m.outputText, "PLAY RECAP") {
		t.Fatalf("output not loaded: %q", m.outputText)
	}
	return m
}

// lineOf reports the wrapped-line index of the first line containing want.
func lineOf(t *testing.T, m Model, want string) int {
	t.Helper()
	for i, l := range m.outputLines {
		if strings.Contains(ansi.Strip(l), want) {
			return i
		}
	}
	t.Fatalf("output has no line containing %q", want)
	return -1
}

func TestOutputSearchFindsAndCyclesMatches(t *testing.T) {
	m := openOutputView(t, outputMock(t))

	m = step(t, m, key("/"))
	if !m.osearch.editing {
		t.Fatal("/ should open the find box")
	}
	m = typeText(t, m, "fatal")

	if got := len(m.osearch.matches); got != 2 {
		t.Fatalf("found %d matches for 'fatal', want 2", got)
	}
	if got := m.matchLabel(); got != "1/2" {
		t.Errorf("match label = %q, want 1/2", got)
	}
	// Searching must not be fooled by the colour codes around the text.
	if want := lineOf(t, m, "disk full"); m.currentMatchLine() != want {
		t.Errorf("first match on line %d, want %d", m.currentMatchLine(), want)
	}

	m = step(t, m, key("enter")) // accept the search
	if m.osearch.editing {
		t.Error("enter should close the find box")
	}
	m = step(t, m, key("n"))
	if want := lineOf(t, m, "connection refused"); m.currentMatchLine() != want {
		t.Errorf("after n, match line %d, want %d", m.currentMatchLine(), want)
	}
	if got := m.matchLabel(); got != "2/2" {
		t.Errorf("match label = %q, want 2/2", got)
	}
	// n wraps around at the end.
	m = step(t, m, key("n"))
	if got := m.matchLabel(); got != "1/2" {
		t.Errorf("after wrapping, label = %q, want 1/2", got)
	}
	m = step(t, m, key("N"))
	if got := m.matchLabel(); got != "2/2" {
		t.Errorf("after N, label = %q, want 2/2", got)
	}
}

func TestOutputSearchHighlightsWithoutLosingColour(t *testing.T) {
	m := openOutputView(t, outputMock(t))
	m = step(t, m, key("/"))
	m = typeText(t, m, "disk full")
	m = step(t, m, key("enter"))

	line := m.outputLines[lineOf(t, m, "disk full")]
	highlighted := highlightMatches(line, "disk full", true)

	// The text is unchanged once styling is stripped...
	if got, want := ansi.Strip(highlighted), ansi.Strip(line); got != want {
		t.Errorf("highlighting altered the text:\n got %q\nwant %q", got, want)
	}
	// ...the match is styled...
	if !strings.Contains(highlighted, matchCurrentStyle.Render("disk full")) {
		t.Error("the matched text is not highlighted")
	}
	// ...and the line keeps its original red.
	if !strings.Contains(highlighted, "\x1b[0;31m") {
		t.Errorf("original colour lost: %q", highlighted)
	}
}

// TestWrapANSICarriesColourAcrossContinuationLines guards against the bug
// where a long fatal message wrapped across several rows only carried colour
// on its first row: ansi.Wrap opens the SGR code once and never reopens it,
// so a viewport window scrolled to a later row rendered it in plain white.
func TestWrapANSICarriesColourAcrossContinuationLines(t *testing.T) {
	long := "\x1b[0;31mfatal: [web-99]: FAILED! this message is long enough to wrap across several rows of narrow terminal output\x1b[0m"
	wrapped := strings.Split(wrapANSI(long, 20), "\n")
	if len(wrapped) < 3 {
		t.Fatalf("expected the fixture to wrap across several lines, got %d: %q", len(wrapped), wrapped)
	}
	for i, line := range wrapped {
		if strings.TrimSpace(ansi.Strip(line)) == "" {
			continue
		}
		if !strings.Contains(line, "\x1b[0;31m") {
			t.Errorf("line %d lost its colour after wrapping: %q", i, line)
		}
	}
}

func TestOutputSearchReportsNoMatches(t *testing.T) {
	m := openOutputView(t, outputMock(t))
	m = step(t, m, key("/"))
	m = typeText(t, m, "nothing-like-this")

	if len(m.osearch.matches) != 0 {
		t.Fatalf("expected no matches, got %d", len(m.osearch.matches))
	}
	if got := m.matchLabel(); got != "no matches" {
		t.Errorf("label = %q, want %q", got, "no matches")
	}
	if !strings.Contains(m.View(), "no matches") {
		t.Error("the view should tell the user there are no matches")
	}
}

func TestOutputSearchEscapeClearsThenLeaves(t *testing.T) {
	m := openOutputView(t, outputMock(t))
	m = step(t, m, key("/"))
	m = typeText(t, m, "fatal")
	m = step(t, m, key("enter"))

	m = step(t, m, key("esc")) // clears the search
	if m.mode != modeOutput {
		t.Fatal("the first esc should clear the search, not leave the view")
	}
	if m.osearch.query != "" || len(m.osearch.matches) != 0 {
		t.Errorf("search not cleared: query=%q matches=%d", m.osearch.query, len(m.osearch.matches))
	}
	m = step(t, m, key("esc")) // now leaves
	if m.mode != modeList {
		t.Errorf("the second esc should return to the list, got %v", m.mode)
	}
}

func TestOutputJumpsBetweenFailures(t *testing.T) {
	m := openOutputView(t, outputMock(t))
	m = step(t, m, key("g")) // start at the top

	first, second := lineOf(t, m, "disk full"), lineOf(t, m, "connection refused")
	if first < m.vp.Height || second < m.vp.Height {
		t.Fatalf("test output is too short to exercise scrolling (failures on lines %d and %d, viewport %d)",
			first, second, m.vp.Height)
	}

	m = step(t, m, key("]"))
	if !visibleAt(m, first) {
		t.Errorf("] did not reach the failure on line %d (offset %d)", first, m.vp.YOffset)
	}
	m = step(t, m, key("]"))
	if !visibleAt(m, second) {
		t.Errorf("second ] did not reach the failure on line %d (offset %d)", second, m.vp.YOffset)
	}
	if visibleAt(m, first) {
		t.Errorf("the second jump should have moved past the first failure (offset %d)", m.vp.YOffset)
	}
	m = step(t, m, key("["))
	if !visibleAt(m, first) {
		t.Errorf("[ did not go back to the failure on line %d (offset %d)", first, m.vp.YOffset)
	}
	// There is nothing after the last failure; the view should say so.
	m = step(t, m, key("]"))
	m = step(t, m, key("]"))
	if m.notice != "no later failure" {
		t.Errorf("notice = %q, want it to report no further failure", m.notice)
	}
	// Following a failure jump must stop tailing, or the view would snap back.
	if m.follow {
		t.Error("jumping should turn follow mode off")
	}
}

func TestOutputJumpsBetweenTasks(t *testing.T) {
	m := openOutputView(t, outputMock(t))
	m = step(t, m, key("g"))

	for _, want := range []string{"TASK [Gathering Facts]", "TASK [deploy : Copy release]", "TASK [deploy : Restart service]"} {
		before := m.vp.YOffset
		m = step(t, m, key("t"))
		if !visibleAt(m, lineOf(t, m, want)) {
			t.Errorf("t did not reach %q (offset %d)", want, m.vp.YOffset)
		}
		if m.vp.YOffset == before && before != 0 {
			t.Errorf("t did not move the view when jumping to %q", want)
		}
	}
	m = step(t, m, key("T"))
	if !visibleAt(m, lineOf(t, m, "TASK [deploy : Copy release]")) {
		t.Errorf("T did not go back a task (offset %d)", m.vp.YOffset)
	}
}

// Recognising Ansible's failure and task markers is what the jumps rely on.
func TestFailureAndTaskLineDetection(t *testing.T) {
	failures := []string{
		`fatal: [web-02]: FAILED! => {"msg": "disk full"}`,
		`failed: [web-01] (item=a) => {"msg": "nope"}`,
		`unreachable: [db-01]`,
		`ERROR! the playbook could not be found`,
	}
	for _, line := range failures {
		if !isFailure(line) {
			t.Errorf("should be seen as a failure: %q", line)
		}
	}
	notFailures := []string{
		`ok: [web-01]`,
		`changed: [web-01]`,
		`skipping: [web-02]`,
		`web-01 : ok=3 changed=1 unreachable=0 failed=1`, // recap, not the failure itself
	}
	for _, line := range notFailures {
		if isFailure(line) {
			t.Errorf("should not be seen as a failure: %q", line)
		}
	}
	for _, line := range []string{"TASK [x] ***", "PLAY [web] ***", "PLAY RECAP ***", "RUNNING HANDLER [restart] ***"} {
		if !isTask(line) {
			t.Errorf("should be seen as a task boundary: %q", line)
		}
	}
	if isTask("ok: [web-01]") {
		t.Error("a host result is not a task boundary")
	}
}

// visibleAt reports whether a line is within the viewport window.
func visibleAt(m Model, line int) bool {
	return line >= m.vp.YOffset && line < m.vp.YOffset+m.vp.Height
}

// A visual check of the output view while searching.
func TestOutputSearchViewRenders(t *testing.T) {
	m := openOutputView(t, outputMock(t))
	m = step(t, m, key("/"))
	m = typeText(t, m, "fatal")
	show(t, "output search (editing)", m.View())
	m = step(t, m, key("enter"))
	show(t, "output search (accepted)", m.View())
}

// The output view used to leave two unused lines under the footer.
func TestOutputViewFillsTheTerminal(t *testing.T) {
	srv := outputMock(t)
	for _, size := range [][2]int{{100, 24}, {120, 40}, {80, 30}} {
		m := openOutputView(t, srv)
		m = step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if got := len(viewLines(m)); got != size[1] {
			t.Errorf("%dx%d: output view is %d lines, terminal has %d",
				size[0], size[1], got, size[1])
		}
		for i, l := range viewLines(m) {
			if w := lineWidth(l); w > size[0] {
				t.Errorf("%dx%d: line %d is %d cols wide", size[0], size[1], i, w)
			}
		}
	}
}

// Trailing blank lines in AWX stdout left a dead band above the footer and
// made a full scroll stop short of the last real line.
func TestOutputDropsTrailingBlankLines(t *testing.T) {
	m := openOutputView(t, outputMock(t))
	m.setOutput(playOutput + "\n\n\n\n")
	if last := m.outputLines[len(m.outputLines)-1]; strings.TrimSpace(stripANSI(last)) == "" {
		t.Error("the last displayed line is blank")
	}
	// The raw text is untouched so a following chunk still joins correctly.
	if !strings.HasSuffix(m.outputText, "\n\n\n\n") {
		t.Error("outputText should keep every byte AWX sent")
	}
	m.vp.GotoBottom()
	body := stripANSI(m.vp.View())
	if !strings.Contains(body, "ok=3 changed=1") {
		t.Errorf("a full scroll should end on the last real line, got:\n%s", body)
	}
	if strings.TrimSpace(body[strings.LastIndex(body, "\n"):]) == "" {
		t.Error("the viewport still ends on a blank line")
	}
}
