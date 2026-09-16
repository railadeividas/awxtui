package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ids(pins []Pin) []int {
	out := make([]int, len(pins))
	for i, p := range pins {
		out[i] = p.ID
	}
	return out
}

func equal(a []int, b ...int) bool {
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

func TestMissingFileIsAnEmptyStoreAndCorruptOneIsAnError(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(filepath.Join(dir, "absent.json"))
	if err != nil {
		t.Fatalf("a first run has no state file; that is not an error: %v", err)
	}
	if len(s.Pins("prod", GroupRuns)) != 0 {
		t.Errorf("expected an empty store, got %v", s.Pins("prod", GroupRuns))
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Starting over silently would throw away every pin without saying so.
	if _, err := Load(bad); err == nil {
		t.Errorf("a corrupt state file should be reported, not ignored")
	}
}

func TestTogglePinsAndUnpins(t *testing.T) {
	s := Memory()
	pinned, err := s.Toggle("prod", GroupRuns, Pin{ID: 42, Name: "Deploy"})
	if err != nil || !pinned {
		t.Fatalf("first toggle should pin, got %v %v", pinned, err)
	}
	if !s.Pinned("prod", GroupRuns, 42) || !s.Any("prod", GroupRuns) {
		t.Errorf("#42 should be pinned")
	}
	pinned, err = s.Toggle("prod", GroupRuns, Pin{ID: 42})
	if err != nil || pinned {
		t.Fatalf("second toggle should unpin, got %v %v", pinned, err)
	}
	if s.Pinned("prod", GroupRuns, 42) || s.Any("prod", GroupRuns) {
		t.Errorf("#42 should be gone")
	}
}

// Ids only identify a record inside its own collection: project #12 and
// project update #12 are different things.
func TestGroupsAndInstancesAreSeparate(t *testing.T) {
	s := Memory()
	for _, g := range []string{GroupRuns, GroupTemplates, GroupInventorys, GroupProjects} {
		if _, err := s.Toggle("prod", g, Pin{ID: 12}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Toggle("prod", GroupRuns, Pin{ID: 12}); err != nil { // unpin the run
		t.Fatal(err)
	}
	if s.Pinned("prod", GroupRuns, 12) {
		t.Errorf("run #12 should be unpinned")
	}
	if !s.Pinned("prod", GroupProjects, 12) {
		t.Errorf("unpinning run #12 also unpinned project #12")
	}
	if s.Pinned("staging", GroupProjects, 12) {
		t.Errorf("prod's pin leaked into staging; job ids are not comparable across AWXes")
	}
}

func TestPinsAreListedMostRecentFirst(t *testing.T) {
	s := Memory()
	base := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	for i, id := range []int{1, 2, 3} {
		if _, err := s.Toggle("prod", GroupTemplates, Pin{ID: id, At: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := ids(s.Pins("prod", GroupTemplates)); !equal(got, 3, 2, 1) {
		t.Errorf("order = %v, want newest pin first", got)
	}
	if got := s.IDs("prod", GroupTemplates); !equal(got, 3, 2, 1) {
		t.Errorf("IDs = %v", got)
	}
}

// The cap drops the oldest pin rather than refusing to pin at a limit nobody
// was told about.
func TestPinsAreCapped(t *testing.T) {
	s := Memory()
	base := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < maxPins+10; i++ {
		if _, err := s.Toggle("prod", GroupRuns, Pin{ID: i + 1, At: base.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	pins := s.Pins("prod", GroupRuns)
	if len(pins) != maxPins {
		t.Fatalf("kept %d pins, want %d", len(pins), maxPins)
	}
	if pins[0].ID != maxPins+10 {
		t.Errorf("newest pin = #%d, want #%d", pins[0].ID, maxPins+10)
	}
	if s.Pinned("prod", GroupRuns, 1) {
		t.Errorf("the oldest pin should have been dropped")
	}
}

func TestViewsRoundTripAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.View("prod", GroupRuns); got != nil {
		t.Errorf("a group that was never narrowed should read as unnarrowed, got %v", got)
	}
	// An empty value is not a choice; it is the absence of one.
	if err := s.SetView("prod", GroupRuns, map[string]string{"mine": "yes", "status": "failed", "kind": ""}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetView("staging", GroupRuns, map[string]string{"pinned": "yes"}); err != nil {
		t.Fatal(err)
	}

	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := again.View("prod", GroupRuns)
	if got["mine"] != "yes" || got["status"] != "failed" {
		t.Errorf("reloaded view = %v", got)
	}
	if _, ok := got["kind"]; ok {
		t.Errorf("an empty choice was saved: %v", got)
	}
	if again.View("staging", GroupRuns)["pinned"] != "yes" {
		t.Errorf("staging's view did not survive")
	}
	if again.View("prod", GroupTemplates) != nil {
		t.Errorf("narrowing the runs list also narrowed the templates list")
	}

	// A caller cannot reach into the store through what View handed back.
	got["mine"] = "tampered"
	if again.View("prod", GroupRuns)["mine"] != "yes" {
		t.Errorf("View returned the store's own map")
	}

	if err := again.SetView("prod", GroupRuns, nil); err != nil {
		t.Fatal(err)
	}
	third, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := third.View("prod", GroupRuns); v != nil {
		t.Errorf("a cleared view came back as %v", v)
	}
	if third.View("staging", GroupRuns)["pinned"] != "yes" {
		t.Errorf("clearing prod's view cleared staging's too")
	}
}

func TestRoundTripThroughTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "pins.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Toggle("prod", GroupRuns, Pin{ID: 7, Kind: "project_update", Name: "infra"}); err != nil {
		t.Fatal(err)
	}

	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	pins := again.Pins("prod", GroupRuns)
	if len(pins) != 1 || pins[0].ID != 7 {
		t.Fatalf("reloaded %+v, want the pinned #7", pins)
	}
	// The kind is what says which collection the run's output comes from.
	if pins[0].Kind != "project_update" || pins[0].Name != "infra" {
		t.Errorf("reloaded pin lost its kind or name: %+v", pins[0])
	}

	// The file names records on a production AWX.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("state file mode = %o, want 600", perm)
	}
}
