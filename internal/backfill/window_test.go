package backfill

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tempestwx-utilities/internal/tempestapi"
)

func TestChunkWindowSplitsMultiDayRange(t *testing.T) {
	from := time.Unix(0, 0).UTC()
	to := from.Add(72 * time.Hour)

	got := chunkWindow(from, to, 24*time.Hour)
	if len(got) != 3 {
		t.Fatalf("got %d windows, want 3: %+v", len(got), got)
	}
	for i, w := range got {
		wantFrom := from.Add(time.Duration(i) * 24 * time.Hour)
		if !w.from.Equal(wantFrom) {
			t.Errorf("window %d from = %v, want %v", i, w.from, wantFrom)
		}
	}
	if !got[len(got)-1].to.Equal(to) {
		t.Errorf("last window to = %v, want %v", got[len(got)-1].to, to)
	}
}

func TestChunkWindowPartialTail(t *testing.T) {
	from := time.Unix(0, 0).UTC()
	to := from.Add(30 * time.Hour)

	got := chunkWindow(from, to, 24*time.Hour)
	if len(got) != 2 {
		t.Fatalf("got %d windows, want 2: %+v", len(got), got)
	}
	if !got[1].to.Equal(to) {
		t.Errorf("tail window to = %v, want %v (must not overshoot)", got[1].to, to)
	}
	if got[1].to.Sub(got[1].from) != 6*time.Hour {
		t.Errorf("tail window width = %v, want 6h", got[1].to.Sub(got[1].from))
	}
}

func TestChunkWindowSingleShortRange(t *testing.T) {
	from := time.Unix(0, 0).UTC()
	to := from.Add(time.Hour)
	got := chunkWindow(from, to, 24*time.Hour)
	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1", len(got))
	}
	if !got[0].from.Equal(from) || !got[0].to.Equal(to) {
		t.Errorf("window = [%v, %v], want [%v, %v]", got[0].from, got[0].to, from, to)
	}
}

func TestChunkWindowEmptyOrInvertedRange(t *testing.T) {
	from := time.Unix(1000, 0).UTC()
	if got := chunkWindow(from, from, 24*time.Hour); len(got) != 0 {
		t.Errorf("zero-width range produced %d windows, want 0", len(got))
	}
	if got := chunkWindow(from, from.Add(-time.Hour), 24*time.Hour); len(got) != 0 {
		t.Errorf("inverted range produced %d windows, want 0", len(got))
	}
}

type fakeNetErr struct{}

func (fakeNetErr) Error() string   { return "dial tcp: connection refused" }
func (fakeNetErr) Timeout() bool   { return false }
func (fakeNetErr) Temporary() bool { return true }

var _ net.Error = fakeNetErr{}

// realTimeoutError produces the error an actual per-attempt HTTP timeout
// yields — NOT a synthetic fake.
//
// This distinction is the whole point. http.Client.Timeout is implemented as a
// context deadline, so the resulting *url.Error satisfies BOTH
// errors.Is(err, context.DeadlineExceeded) AND errors.As(err, &netErr). A
// hand-rolled net.Error fake satisfies only the second, so it cannot
// reproduce the bug and would pass against a classifier that returns false for
// every timeout. Build the real thing.
func realTimeoutError(t *testing.T) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // stall until the client gives up
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Timeout: 50 * time.Millisecond}
	resp, err := client.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected a timeout error, got none")
	}
	return err
}

// Regression test for the classifier bug: a per-attempt HTTP timeout MUST be
// retried. If this fails, isRetryable is short-circuiting on
// context.DeadlineExceeded before reaching the net.Error branch, and every
// slow API response will fail its entire gap with zero retries.
func TestIsRetryableRealHTTPTimeoutIsRetried(t *testing.T) {
	err := realTimeoutError(t)

	// Document the dual-predicate property that makes this subtle.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("precondition changed: a Client.Timeout error should satisfy errors.Is(DeadlineExceeded); got %v", err)
	}
	var ne net.Error
	if !errors.As(err, &ne) {
		t.Fatalf("precondition changed: a Client.Timeout error should satisfy errors.As(net.Error); got %v", err)
	}

	if !isRetryable(err) {
		t.Error("a per-attempt HTTP timeout must be retryable; " +
			"isRetryable is short-circuiting on context.DeadlineExceeded")
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429", &tempestapi.StatusError{HTTPStatus: http.StatusTooManyRequests}, true},
		{"500", &tempestapi.StatusError{HTTPStatus: http.StatusInternalServerError}, true},
		{"503", &tempestapi.StatusError{HTTPStatus: http.StatusServiceUnavailable}, true},
		{"404", &tempestapi.StatusError{HTTPStatus: http.StatusNotFound}, false},
		{"401", &tempestapi.StatusError{HTTPStatus: http.StatusUnauthorized}, false},
		{"api level status_code is never transient", &tempestapi.StatusError{StatusCode: 404, Message: "NOT FOUND"}, false},
		{"wrapped 503 still classifies", fmt.Errorf("gap 1: %w", &tempestapi.StatusError{HTTPStatus: 503}), true},
		{"network error", fakeNetErr{}, true},
		{"context canceled is the operator's decision", context.Canceled, false},
		{"unknown error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryable(tt.err); got != tt.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
