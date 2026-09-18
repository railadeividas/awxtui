// Package state keeps the one thing awxtui knows that AWX does not: which
// records this operator pinned.
//
// AWX has no notion of a bookmark. A template you launch every week is one
// row among 209, and a job you want to come back to is one among 170290 on
// the instance this was built against, so finding either again meant
// remembering its name or its number.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Groups are the kinds of thing that can be pinned. Ids only identify a
// record within its own collection, so #12 the project and #12 the project
// update must not share a list.
const (
	GroupRuns       = "runs"
	GroupTemplates  = "templates"
	GroupInventorys = "inventories"
	GroupProjects   = "projects"
	GroupSchedules  = "schedules"
)

// maxPins bounds one group of one instance. A pin is deliberate, so the cap
// is generous; it exists so a forgotten list cannot grow without limit.
const maxPins = 100

// Pin is one pinned record. The name is kept beside the id so a pinned row
// can be named before AWX has answered — and still be named after AWX has
// deleted the record.
type Pin struct {
	ID int `json:"id"`
	// Kind is the AWX record type of a pinned run: job, project_update or
	// inventory_update. It says which collection the run's output comes
	// from. Empty for the other groups.
	Kind string    `json:"kind,omitempty"`
	Name string    `json:"name"`
	At   time.Time `json:"at"`
}

// Store is the pins of every instance, backed by one JSON file. A Store with
// an empty path keeps everything in memory and never writes, which is what
// tests and an unwritable home directory get.
type Store struct {
	path string
	data fileData
}

type fileData struct {
	// Instances is keyed by the configured instance name and then by group,
	// so switching instances switches pin lists rather than mixing two
	// AWXes together.
	Instances map[string]map[string][]Pin `json:"instances"`
	// Views is what each group's list is narrowed to, keyed the same way:
	// instance, then group, then the name of the choice. It is kept as plain
	// strings rather than a struct so that adding a choice to the panel does
	// not change the file format or strand what is already saved.
	Views map[string]map[string]map[string]string `json:"views,omitempty"`
}

// DefaultPath is ~/.local/state/awxtui/pins.json, honouring XDG_STATE_HOME.
// State, not config: it is written by the program, not edited by hand.
func DefaultPath() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "awxtui", "pins.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "awxtui", "pins.json")
}

// Memory returns a Store that is never written to disk.
func Memory() *Store { return &Store{data: fileData{Instances: map[string]map[string][]Pin{}}} }

// Load reads the store at path. A missing file is not an error — it is the
// normal state on first run. A corrupt one is: silently starting over would
// throw away every pin without saying so.
func Load(path string) (*Store, error) {
	s := &Store{path: path, data: fileData{Instances: map[string]map[string][]Pin{}}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return s, fmt.Errorf("reading %s: %w", path, err)
	}
	if s.data.Instances == nil {
		s.data.Instances = map[string]map[string][]Pin{}
	}
	return s, nil
}

// Pins returns one group's pins, most recently pinned first.
func (s *Store) Pins(instance, group string) []Pin {
	src := s.data.Instances[instance][group]
	out := make([]Pin, len(src))
	copy(out, src)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// IDs returns the pinned ids of one group, in display order.
func (s *Store) IDs(instance, group string) []int {
	pins := s.Pins(instance, group)
	ids := make([]int, len(pins))
	for i, p := range pins {
		ids[i] = p.ID
	}
	return ids
}

// Pinned reports whether a record is pinned.
func (s *Store) Pinned(instance, group string, id int) bool {
	for _, p := range s.data.Instances[instance][group] {
		if p.ID == id {
			return true
		}
	}
	return false
}

// Any reports whether a group has any pins, which is what decides whether a
// "pinned only" filter can show anything.
func (s *Store) Any(instance, group string) bool {
	return len(s.data.Instances[instance][group]) > 0
}

// Toggle pins or unpins a record and reports the state it ended in.
func (s *Store) Toggle(instance, group string, p Pin) (bool, error) {
	pins := s.data.Instances[instance][group]
	for i, cur := range pins {
		if cur.ID != p.ID {
			continue
		}
		s.set(instance, group, append(pins[:i:i], pins[i+1:]...))
		return false, s.save()
	}
	if p.At.IsZero() {
		p.At = time.Now().UTC()
	}
	pins = append(pins, p)
	// The cap drops the oldest pin, so pinning always succeeds rather than
	// failing at a limit nobody was told about.
	if len(pins) > maxPins {
		sort.SliceStable(pins, func(i, j int) bool { return pins[i].At.After(pins[j].At) })
		pins = pins[:maxPins]
	}
	s.set(instance, group, pins)
	return true, s.save()
}

func (s *Store) set(instance, group string, pins []Pin) {
	if s.data.Instances == nil {
		s.data.Instances = map[string]map[string][]Pin{}
	}
	if s.data.Instances[instance] == nil {
		s.data.Instances[instance] = map[string][]Pin{}
	}
	if len(pins) == 0 {
		delete(s.data.Instances[instance], group)
		return
	}
	s.data.Instances[instance][group] = pins
}

// View returns what one group's list was last narrowed to. A group that was
// never narrowed returns nil, which is the unnarrowed view.
func (s *Store) View(instance, group string) map[string]string {
	src := s.data.Views[instance][group]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SetView records what a group's list is narrowed to. An empty set clears it,
// so a cleared filter does not linger in the file as an empty object.
func (s *Store) SetView(instance, group string, fields map[string]string) error {
	kept := map[string]string{}
	for k, v := range fields {
		if v != "" {
			kept[k] = v
		}
	}
	if s.data.Views == nil {
		s.data.Views = map[string]map[string]map[string]string{}
	}
	if s.data.Views[instance] == nil {
		s.data.Views[instance] = map[string]map[string]string{}
	}
	if len(kept) == 0 {
		delete(s.data.Views[instance], group)
		if len(s.data.Views[instance]) == 0 {
			delete(s.data.Views, instance)
		}
	} else {
		s.data.Views[instance][group] = kept
	}
	return s.save()
}

// save writes the whole file atomically, so an interrupted write cannot leave
// a half-written list behind. The file names records on a production AWX, so
// it is owner-readable only.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	return nil
}
