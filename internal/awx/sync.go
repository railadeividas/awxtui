package awx

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Syncing in AWX is two unrelated endpoints wearing one name. A project is
// updated in place — POST /api/v2/projects/{id}/update/ — and answers with the
// project_update it started. An inventory has no update endpoint of its own:
// its hosts come from inventory sources, and each source is updated through
// POST /api/v2/inventory_sources/{id}/update/, so syncing an inventory means
// updating every source it has. Both answer with a record in the same shape as
// a launched job, but neither record appears in /api/v2/jobs/.

// KindLabel names what kind of run this is, for a view that would otherwise
// show a project update and a playbook job identically.
func (j Job) KindLabel() string {
	switch j.Resource() {
	case ResourceProjectUpdates:
		return "project update"
	case ResourceInventoryUpdates:
		return "inventory sync"
	default:
		return "job"
	}
}

// CanUpdate reports whether AWX would accept an update of a project. A GET of
// the same endpoint that starts one answers {"can_update": bool}: false when
// the project has no SCM source, or an update is already running.
func (c *Client) CanUpdate(ctx context.Context, projectID int) (bool, error) {
	var out struct {
		CanUpdate bool `json:"can_update"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v2/projects/%d/update/", projectID), nil, &out)
	return out.CanUpdate, err
}

// UpdateProject starts an SCM update of a project and returns the
// project_update AWX created.
func (c *Client) UpdateProject(ctx context.Context, projectID int) (Job, error) {
	var j Job
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/api/v2/projects/%d/update/", projectID), strings.NewReader("{}"), &j)
	if err != nil {
		return Job{}, err
	}
	if j.Type == "" {
		j.Type = ResourceProjectUpdates.recordType()
	}
	return j, nil
}

// InventorySource is one place an inventory pulls its hosts from: an SCM file,
// a cloud account, or a script.
type InventorySource struct {
	ID             int        `json:"id"`
	Name           string     `json:"name"`
	Source         string     `json:"source"`
	SourcePath     string     `json:"source_path"`
	Status         string     `json:"status"`
	LastUpdated    *time.Time `json:"last_updated"`
	UpdateOnLaunch bool       `json:"update_on_launch"`
	SummaryFields  struct {
		SourceProject struct {
			Name string `json:"name"`
		} `json:"source_project"`
	} `json:"summary_fields"`
}

// Label names a source for display. AWX allows several sources per inventory
// and they are routinely all called "git", so the name alone does not identify
// one; the source kind and path are what tell them apart.
func (s InventorySource) Label() string {
	parts := []string{s.Name}
	if s.Source != "" {
		parts = append(parts, s.Source)
	}
	if s.SourcePath != "" {
		parts = append(parts, s.SourcePath)
	}
	return strings.Join(parts, " · ")
}

// InventorySources returns a page of the sources of one inventory.
func (c *Client) InventorySources(ctx context.Context, inventoryID int, pageURL, search string) (Page[InventorySource], error) {
	return listPage[InventorySource](ctx, c, firstOr(pageURL,
		listURL(fmt.Sprintf("/api/v2/inventories/%d/inventory_sources/", inventoryID), "name", PageSize, search)))
}

// UpdateInventorySource starts a sync of one inventory source and returns the
// inventory_update AWX created.
func (c *Client) UpdateInventorySource(ctx context.Context, sourceID int) (Job, error) {
	var j Job
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/api/v2/inventory_sources/%d/update/", sourceID), strings.NewReader("{}"), &j)
	if err != nil {
		return Job{}, err
	}
	if j.Type == "" {
		j.Type = ResourceInventoryUpdates.recordType()
	}
	return j, nil
}

// ErrNoInventorySources is returned when an inventory has nothing to sync,
// which is the normal state of an inventory whose hosts were entered by hand.
var ErrNoInventorySources = fmt.Errorf("inventory has no sources to sync")

// SyncInventory updates every source of an inventory — what the AWX web UI
// calls "sync all" — and returns the updates it started, in source order.
//
// The sources are updated one POST at a time rather than through
// /api/v2/inventories/{id}/update_inventory_sources/, which starts them all in
// one request but answers with a bespoke per-source result list instead of the
// update records themselves. Doing it a source at a time is what makes the
// started updates addressable, so their output can be followed.
//
// A source that fails to start stops the run: the updates already started are
// returned alongside the error, and none is retried.
func (c *Client) SyncInventory(ctx context.Context, inventoryID int) ([]Job, error) {
	if c.readOnly {
		// Checked up front so read-only mode reports itself without first
		// issuing the GET that lists the sources.
		return nil, fmt.Errorf("%w (sync inventory %d)", ErrReadOnly, inventoryID)
	}
	page, err := c.InventorySources(ctx, inventoryID, "", "")
	if err != nil {
		return nil, err
	}
	if len(page.Results) == 0 {
		return nil, ErrNoInventorySources
	}
	var started []Job
	for _, src := range page.Results {
		j, err := c.UpdateInventorySource(ctx, src.ID)
		if err != nil {
			return started, fmt.Errorf("source %s: %w", src.Label(), err)
		}
		started = append(started, j)
	}
	return started, nil
}
