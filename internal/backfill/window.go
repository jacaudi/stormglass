// Package backfill finds holes in the local observation history and fills
// them from the Tempest REST API.
package backfill

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"tempestwx-utilities/internal/tempestapi"
)

// chunkSize bounds one API request's window.
//
// The Tempest API documents that observation data at one-minute resolution is
// available only for ranges of FIVE DAYS OR LESS. Exceeding that cap does not
// error — it silently returns coarser data, which would then be written as if
// it were 1-minute observations. One day sits comfortably inside the cap.
// Every fetch goes through the chunker, including an explicit --from/--to.
const chunkSize = 24 * time.Hour

// window is one API request's time range.
type window struct {
	from time.Time
	to   time.Time
}

// chunkWindow splits [from, to] into consecutive windows of at most size. The
// final window is truncated to `to` rather than overshooting it. A zero-width
// or inverted range, or a non-positive size, yields no windows.
func chunkWindow(from, to time.Time, size time.Duration) []window {
	if size <= 0 || !to.After(from) {
		return nil
	}
	var out []window
	for start := from; start.Before(to); start = start.Add(size) {
		end := start.Add(size)
		if end.After(to) {
			end = to
		}
		out = append(out, window{from: start, to: end})
	}
	return out
}

// isRetryable reports whether retrying err could plausibly succeed.
//
// Classification uses errors.As, NOT errors.AsType — that is Go 1.26 and
// go.mod declares go 1.25.0.
//
// DO NOT add a `errors.Is(err, context.DeadlineExceeded) -> false` guard here.
// It looks obviously correct and it is a serious bug. http.Client.Timeout is
// IMPLEMENTED as a context deadline, so a per-attempt timeout produces a
// *url.Error that satisfies BOTH errors.Is(err, context.DeadlineExceeded) AND
// errors.As(err, &netErr):
//
//	Get "...": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
//	errors.Is(err, context.DeadlineExceeded) = true
//	errors.As(err, &net.Error)               = true
//
// Such a guard therefore classifies EVERY slow API response as permanent,
// failing the whole gap on the first try with zero retries — the single most
// likely transient failure in a tool issuing thousands of sequential requests.
// Timeouts must fall through to the net.Error branch below.
//
// Whether the PARENT context is done is a separate question, answered at the
// call site with ctx.Err(), not by inspecting this error's identity.
// context.Canceled is the one blanket signal: it is the operator's decision.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}

	var se *tempestapi.StatusError
	if errors.As(err, &se) {
		// A non-zero API-level status_code is a real failure, not congestion.
		if se.StatusCode != 0 {
			return false
		}
		return se.HTTPStatus == http.StatusTooManyRequests || se.HTTPStatus >= 500
	}

	var ne net.Error
	return errors.As(err, &ne)
}
