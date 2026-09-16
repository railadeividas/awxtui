package awx

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// countingServer records each request method and can fail the first n attempts.
type countingServer struct {
	*httptest.Server
	mu       sync.Mutex
	attempts int
	methods  []string
}

func (s *countingServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

func newFlakyServer(t *testing.T, failFirst int, status int, header map[string]string) *countingServer {
	t.Helper()
	s := &countingServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.attempts++
		n := s.attempts
		s.methods = append(s.methods, r.Method)
		s.mu.Unlock()
		if n <= failFirst {
			for k, v := range header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(status)
			return
		}
		_, _ = fmt.Fprint(w, `{"count":0,"next":null,"results":[]}`)
	}))
	t.Cleanup(s.Close)
	return s
}

// A GET that hits a transient 503 should recover without the caller noticing.
func TestGetRetriesServerErrors(t *testing.T) {
	srv := newFlakyServer(t, 2, http.StatusServiceUnavailable, nil)
	c := New(srv.URL, "t", false)

	if _, err := c.Jobs(context.Background(), "", ""); err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}
	if got := srv.count(); got != 3 {
		t.Errorf("made %d attempts, want 3", got)
	}
}

// Retries are bounded; a permanently broken endpoint must surface its error.
func TestRetriesGiveUp(t *testing.T) {
	srv := newFlakyServer(t, 99, http.StatusBadGateway, nil)
	c := New(srv.URL, "t", false)

	_, err := c.Jobs(context.Background(), "", "")
	if err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v, want it to mention the status", err)
	}
	if got := srv.count(); got != maxAttempts {
		t.Errorf("made %d attempts, want %d", got, maxAttempts)
	}
}

// A launch that may already have started a job must never be repeated on an
// ambiguous failure: that would run the playbook twice.
func TestLaunchIsNotRetriedOnServerError(t *testing.T) {
	srv := newFlakyServer(t, 1, http.StatusBadGateway, nil)
	c := New(srv.URL, "t", false)

	if _, err := c.Launch(context.Background(), 7, nil); err == nil {
		t.Fatal("expected the 502 to be reported, not retried away")
	}
	if got := srv.count(); got != 1 {
		t.Errorf("POST was attempted %d times, want exactly 1", got)
	}
}

// 429 means AWX did no work, so even a POST is safe to repeat.
func TestRateLimitIsRetriedForWrites(t *testing.T) {
	srv := newFlakyServer(t, 1, http.StatusTooManyRequests, map[string]string{"Retry-After": "0"})
	c := New(srv.URL, "t", false)

	if _, err := c.Launch(context.Background(), 7, nil); err != nil {
		t.Fatalf("a rate-limited launch should be retried: %v", err)
	}
	if got := srv.count(); got != 2 {
		t.Errorf("made %d attempts, want 2", got)
	}
}

func TestBackoffHonoursRetryAfter(t *testing.T) {
	res := &http.Response{Header: http.Header{"Retry-After": []string{"2"}}}
	if got := backoff(1, res); got != 2*time.Second {
		t.Errorf("backoff with Retry-After: 2 = %v, want 2s", got)
	}
	// Absurd values are capped rather than hanging the UI.
	res.Header.Set("Retry-After", "3600")
	if got := backoff(1, res); got != maxBackoff {
		t.Errorf("backoff = %v, want it capped at %v", got, maxBackoff)
	}
	// Without a header it grows per attempt.
	if a, b := backoff(1, nil), backoff(2, nil); !(b > a) {
		t.Errorf("backoff should grow: attempt1=%v attempt2=%v", a, b)
	}
}

// A cancelled context must stop the retry loop promptly.
func TestRetriesStopOnContextCancel(t *testing.T) {
	srv := newFlakyServer(t, 99, http.StatusServiceUnavailable, nil)
	c := New(srv.URL, "t", false)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Jobs(ctx, "", ""); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v to give up on a cancelled context", elapsed)
	}
}

// The token travels in a header, but must never reach an error message even
// if a server echoes it back.
func TestErrorsRedactTheToken(t *testing.T) {
	const token = "super-secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"detail":"bad token %s"}`, token)
	}))
	defer srv.Close()

	_, err := New(srv.URL, token, false).Jobs(context.Background(), "", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("error leaked the token: %v", err)
	}
	if !strings.Contains(err.Error(), "redacted") {
		t.Errorf("err = %v, want it to note the redaction", err)
	}
}

// Redacting a very short token would replace every occurrence of those
// characters and destroy the message.
func TestShortTokensAreNotRedacted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"detail":"the template was not found"}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "t", false).Jobs(context.Background(), "", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "the template was not found") {
		t.Errorf("message was mangled by redaction: %v", err)
	}
}
