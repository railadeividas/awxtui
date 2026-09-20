package awx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// AdHocModules lists the modules AWX permits for an ad hoc command, in the
// order its own launch UI offers them (confirmed against a live instance's
// OPTIONS response for /api/v2/ad_hoc_commands/ — AWX rejects any other
// module_name).
var AdHocModules = []string{
	"command", "shell", "yum", "apt", "apt_key", "apt_repository", "apt_rpm",
	"service", "group", "user", "mount", "ping", "selinux", "setup",
	"systemd", "debug", "file", "lineinfile",
}

// AdHocCredentials returns a page of credentials an ad hoc command can
// connect with. AWX's own ad hoc launch narrows this the same way: only the
// "Machine" type authenticates to a host at all, and the API calls that kind
// "ssh" — confirmed against a live instance, where `kind` itself is not a
// filterable field but `credential_type__kind` is. Unlike a job template's
// credentials field, an ad hoc command has no use for a vault or cloud
// credential, so those are excluded server-side rather than fetched and
// discarded.
func (c *Client) AdHocCredentials(ctx context.Context, pageURL, search string) (Page[Credential], error) {
	q := url.Values{
		"order_by":              {"name"},
		"page_size":             {strconv.Itoa(PageSize)},
		"credential_type__kind": {"ssh"},
	}
	if s := strings.TrimSpace(search); s != "" {
		q.Set("search", s)
	}
	return listPage[Credential](ctx, c, firstOr(pageURL, "/api/v2/credentials/?"+q.Encode()))
}

// AllAdHocCredentials walks every page of machine credentials.
func (c *Client) AllAdHocCredentials(ctx context.Context, maxPages int) ([]Credential, error) {
	return allPages(ctx, maxPages, c.AdHocCredentials)
}

// AdHocCommands returns a page of past ad hoc commands, newest first. They
// live in their own collection, not /api/v2/jobs/ — the same reason a
// project update or inventory sync does.
func (c *Client) AdHocCommands(ctx context.Context, pageURL, search string) (Page[Job], error) {
	return listPage[Job](ctx, c, firstOr(pageURL, listURL("/api/v2/ad_hoc_commands/", "-id", jobsPageSize, search)))
}

// LaunchAdHoc runs a module against an inventory, answering with the ad hoc
// command AWX created. AWX names the record after the module itself and
// leaves extra_vars as "---" when none is given, so callers only need to
// send inventory, credential, module_name, module_args and whatever else the
// user set (limit, verbosity, become_enabled, diff_mode).
func (c *Client) LaunchAdHoc(ctx context.Context, payload map[string]any) (Job, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Job{}, err
	}
	var j Job
	err = c.do(ctx, http.MethodPost, "/api/v2/ad_hoc_commands/", strings.NewReader(string(raw)), &j)
	if j.Type == "" {
		j.Type = ResourceAdHocCommands.recordType()
	}
	return j, err
}
