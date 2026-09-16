package awx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A read-only client must refuse every state-changing call before it reaches
// the network. This guards real, production AWX instances.
func TestReadOnlyRefusesWrites(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		w.Write([]byte(`{"count":0,"results":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "t", false).ReadOnly()
	ctx := context.Background()

	if _, err := c.Launch(ctx, 7, nil); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Launch: expected ErrReadOnly, got %v", err)
	}
	if err := c.Cancel(ctx, 7); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Cancel: expected ErrReadOnly, got %v", err)
	}
	if len(methods) > 0 {
		t.Errorf("read-only client still sent requests: %v", methods)
	}

	// Reads must keep working.
	if _, err := c.Jobs(ctx, "", ""); err != nil {
		t.Errorf("Jobs: %v", err)
	}
	for _, m := range methods {
		if m[:4] != "GET " {
			t.Errorf("unexpected non-GET request: %s", m)
		}
	}
}
