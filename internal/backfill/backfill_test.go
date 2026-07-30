package backfill

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"tempestwx-utilities/internal/tempestapi"
	"tempestwx-utilities/internal/weather"
)

// TestMain silences slog for this package's test binary. Nearly every test
// here drives Run/fillGap/fetchWithRetry, which log unconditionally by
// design (§backfill.go), so the per-test slog.SetDefault + t.Cleanup idiom
// used elsewhere in the repo (internal/tempestapi, internal/httpserver)
// would mean repeating it in essentially every test in this file. A single
// package-wide override is the less invasive form of the same idiom.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// --- fakes ---

type fakeSource struct {
	calls    []window
	stations []tempestapi.Station // which device each call was for
	obs      []weather.Observation
	errs     []error // consumed one per call; nil entries succeed
	callNum  int
}

func (f *fakeSource) Observations(_ context.Context, st tempestapi.Station, start, end time.Time) ([]weather.Observation, error) {
	f.calls = append(f.calls, window{from: start, to: end})
	f.stations = append(f.stations, st)
	i := f.callNum
	f.callNum++
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.obs, nil
}

// obsKey is the (serial, timestamp) identity the real stores' UNIQUE
// constraint and ON CONFLICT DO NOTHING key off of (sqlite/backfill.go,
// postgres/backfill.go).
type obsKey struct {
	serial string
	ts     time.Time
}

type fakeStore struct {
	// serials is what DistinctSerials returns — the UNWINDOWED whole-table
	// serial set the pre-flight check uses. It is deliberately independent of
	// bounds, because the two queries genuinely differ: a serial can be in the
	// store (serials) yet have no rows inside the queried window (bounds).
	serials   []string
	bounds    []weather.Bounds
	gaps      []weather.Gap
	inserted  [][]weather.Observation
	insertErr error

	// seen tracks which (serial, timestamp) keys have already been reported
	// as inserted, so InsertObservations can model the real store's
	// `ON CONFLICT (serial_number, timestamp) DO NOTHING`: a row whose key
	// was already reported returns 0 newly-inserted, not len(obs). Without
	// this, Returned == Inserted in every Run-level test, and the plumbing
	// between the store's real insert count and Stats.Inserted is never
	// exercised — see TestRunSecondPassInsertsNothingWhenNothingIsNew.
	seen map[obsKey]struct{}
}

func (f *fakeStore) DistinctSerials(context.Context) ([]string, error) {
	return f.serials, nil
}

func (f *fakeStore) SeriesBounds(context.Context, time.Time, time.Time) ([]weather.Bounds, error) {
	return f.bounds, nil
}

func (f *fakeStore) FindObservationGaps(context.Context, time.Time, time.Time, time.Duration) ([]weather.Gap, error) {
	return f.gaps, nil
}

func (f *fakeStore) InsertObservations(_ context.Context, obs []weather.Observation) (int, error) {
	if f.insertErr != nil {
		return 0, f.insertErr
	}
	f.inserted = append(f.inserted, obs)

	if f.seen == nil {
		f.seen = make(map[obsKey]struct{})
	}
	n := 0
	for _, o := range obs {
		key := obsKey{o.SerialNumber, o.Timestamp}
		if _, ok := f.seen[key]; ok {
			continue
		}
		f.seen[key] = struct{}{}
		n++
	}
	return n, nil
}

func station(serial string) tempestapi.Station {
	return tempestapi.Station{
		SerialNumber: serial,
		DeviceID:     1, // no assertion reads this; keeping it constant is honest
		CreatedAt:    time.Unix(1000, 0).UTC(),
	}
}

func obsAt(serial string, epoch int64) weather.Observation {
	return weather.Observation{SerialNumber: serial, Timestamp: time.Unix(epoch, 0).UTC()}
}

// at is ts (defined in gaps_test.go — same package) under the name these
// tests read better with. A one-line alias, not a second implementation.
func at(epoch int64) time.Time { return ts(epoch) }

// --- tests ---
//
// NOTE on fixtures: tests that pass an explicit Config{From, To} run the
// path where Run does NOT query SeriesBounds, so setting store.bounds in
// them is dead fixture data. It is left in place only where it documents
// intent; do not infer from its presence that such a test exercises bounds.
// TestRunAutoDetectUsesSeriesBounds is the only test that pins the fetch.

func TestRunDryRunMakesNoAPICallsAndNoWrites(t *testing.T) {
	src := &fakeSource{obs: []weather.Observation{obsAt("ST-A", 5000)}}
	store := &fakeStore{bounds: []weather.Bounds{{SerialNumber: "ST-A", First: at(1000), Last: at(2000)}}}

	stats, err := Run(t.Context(),
		Config{MinGap: 30 * time.Minute, DryRun: true},
		src, store, []tempestapi.Station{station("ST-A")}, at(200000))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(src.calls) != 0 {
		t.Errorf("dry run made %d API calls, want 0", len(src.calls))
	}
	if len(store.inserted) != 0 {
		t.Errorf("dry run made %d inserts, want 0", len(store.inserted))
	}
	if stats.Gaps == 0 {
		t.Error("dry run should still report the gaps it detected")
	}
	if stats.Inserted != 0 {
		t.Errorf("Inserted = %d, want 0", stats.Inserted)
	}
}

func TestRunSerialMismatchIsHardStopWithNoWrites(t *testing.T) {
	src := &fakeSource{obs: []weather.Observation{obsAt("ST-A", 5000)}}
	// The store holds ONLY a different serial than the API reports — the two
	// sets are disjoint, which is the signature of format divergence. Writing
	// would create a parallel series that never dedupes.
	store := &fakeStore{
		serials: []string{"ST-OTHER"},
		bounds:  []weather.Bounds{{SerialNumber: "ST-OTHER", First: at(1000), Last: at(2000)}},
	}

	_, err := Run(t.Context(),
		Config{MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, at(200000))
	if err == nil {
		t.Fatal("serial mismatch must be a hard stop, got nil error")
	}
	if !errors.Is(err, ErrSerialMismatch) {
		t.Errorf("err = %v, want ErrSerialMismatch", err)
	}
	if len(store.inserted) != 0 {
		t.Errorf("wrote %d batches despite a serial mismatch, want 0", len(store.inserted))
	}
	if len(src.calls) != 0 {
		t.Errorf("made %d API calls despite a serial mismatch, want 0", len(src.calls))
	}
}

func TestRunEmptyStoreIsNotASerialMismatch(t *testing.T) {
	src := &fakeSource{obs: []weather.Observation{obsAt("ST-A", 5000)}}
	store := &fakeStore{} // empty: first run

	stats, err := Run(t.Context(),
		Config{MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, at(200000))
	if err != nil {
		t.Fatalf("empty store must not be a mismatch: %v", err)
	}
	if stats.Gaps == 0 {
		t.Error("empty store should yield the whole range as one gap")
	}
}

// Regression: adding a second station to the account must not brick backfill.
// The sets overlap (ST-A is in both), so this is not format divergence — and
// ST-B, which the store has never seen, should simply get a whole-range gap.
func TestRunNewSerialAlongsideKnownSerialIsNotAMismatch(t *testing.T) {
	src := &fakeSource{obs: []weather.Observation{obsAt("ST-B", 5000)}}
	store := &fakeStore{
		serials: []string{"ST-A"},
		bounds:  []weather.Bounds{{SerialNumber: "ST-A", First: at(1000), Last: at(199000)}},
	}

	stats, err := Run(t.Context(),
		Config{MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A"), station("ST-B")}, at(200000))
	if err != nil {
		t.Fatalf("a new serial alongside a known one must not be a mismatch: %v", err)
	}
	if stats.Gaps == 0 {
		t.Error("the new serial ST-B should yield a whole-range gap")
	}
	if stats.Inserted == 0 {
		t.Error("the new serial's gap should have been fetched and inserted")
	}
}

// Regression guard for the CONDITIONAL SeriesBounds fetch.
//
// Run skips the SeriesBounds query on the explicit --from/--to path. Nothing
// else pins that it still performs it on the auto-detect path: deleting the
// query outright (always nil bounds) leaves every other test green, because
// assembleGaps is unit-tested with bounds passed in directly and six Run
// tests set store.bounds on the explicit path where it is dead fixture data.
//
// The consequence of nil bounds in production is not an error, which is why
// it hides: every serial gets ONE whole-range gap from CreatedAt to
// now-MinGap. For a two-year-old station that is ~730 sequential 24h API
// windows on every run, forever, instead of the handful of real holes. It
// still converges — it just silently turns a 30-second repair into hours of
// API hammering.
//
// So assert the SHAPE of the gap, not the count: with a stored series ending
// at Last, the planned tail gap must START at Last. Nil bounds would start it
// at detectFrom instead.
func TestRunAutoDetectUsesSeriesBounds(t *testing.T) {
	last := at(100000)
	src := &fakeSource{obs: []weather.Observation{obsAt("ST-A", 150000)}}
	store := &fakeStore{
		serials: []string{"ST-A"},
		bounds:  []weather.Bounds{{SerialNumber: "ST-A", First: at(1000), Last: last}},
	}

	_, err := Run(t.Context(),
		Config{MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, at(300000))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(src.calls) == 0 {
		t.Fatal("no windows fetched")
	}

	// The first fetched window must begin at the stored series' Last, not at
	// the station's CreatedAt.
	got := src.calls[0].from
	if !got.Equal(last) {
		t.Errorf("first fetch starts at %v, want %v (the stored series' Last). "+
			"Starting at CreatedAt means SeriesBounds was not consulted, and the tool "+
			"will re-request the station's entire history on every run.", got, last)
	}
}

// Regression for the windowed-bounds bug: the pre-flight check must read
// DistinctSerials (whole table), not SeriesBounds (windowed). A station that
// simply had no rows inside the requested window is not missing from the
// store, and `backfill --from X --to Y` is the tool's main repair path.
//
// The fixture is chosen so the two inputs DISAGREE — that is the whole point.
// serials={ST-A, ST-OLD} overlaps the API's {ST-A}, so preflight passes; but
// bounds={ST-OLD} does NOT overlap it, so feeding preflight the windowed key
// set would report a false mismatch. A fixture where both inputs contain an
// overlapping serial cannot tell the two apart and would pass either way.
//
// It must ALSO run on the auto-detect path (no --from/--to). Run skips the
// SeriesBounds query entirely on the explicit-range path, so under that
// config a mutated preflight would receive an empty slice, read it as
// "empty store, not a mismatch", and return nil — passing for the wrong
// reason. Do not add From/To to this test.
func TestRunQuietSerialInRequestedWindowIsNotAMismatch(t *testing.T) {
	src := &fakeSource{obs: []weather.Observation{obsAt("ST-A", 5000)}}
	store := &fakeStore{
		// Whole table: the API's ST-A IS in the store, alongside a retired
		// serial.
		serials: []string{"ST-A", "ST-OLD"},
		// In-window: only ST-OLD has rows here. ST-A was quiet.
		bounds: []weather.Bounds{{SerialNumber: "ST-OLD", First: at(1000), Last: at(2000)}},
	}

	// Auto-detect (no From/To) so SeriesBounds is genuinely consulted.
	_, err := Run(t.Context(),
		Config{MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, at(200000))
	if err != nil {
		t.Fatalf("a serial with no rows in the detection window must not be a mismatch: %v", err)
	}
}

// The design mandates that a station with two ST devices has BOTH backfilled.
// Task 2's ListDevices test proves the two devices are discovered; this proves
// Run actually fetches for each of them. Without it, the second half of that
// requirement is untested and a regression that silently drops one sensor
// would ship green.
func TestRunBackfillsEveryDevice(t *testing.T) {
	src := &fakeSource{obs: []weather.Observation{obsAt("ST-A", 5000)}}
	store := &fakeStore{} // empty: both devices get a whole-range gap

	devices := []tempestapi.Station{station("ST-A"), station("ST-B")}
	stats, err := Run(t.Context(),
		Config{MinGap: 30 * time.Minute}, src, store, devices, at(200000))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Gaps != 2 {
		t.Errorf("Gaps = %d, want exactly 2 — one whole-range gap per device", stats.Gaps)
	}

	fetched := map[string]bool{}
	for _, s := range src.stations {
		fetched[s.SerialNumber] = true
	}
	if !fetched["ST-A"] || !fetched["ST-B"] {
		t.Errorf("fetched for %v, want both ST-A and ST-B — a two-sensor station must not lose one", fetched)
	}
}

// The insert-abort asymmetry is deliberate and must stay: a WINDOW error
// continues to the next window, but an INSERT error aborts the whole gap,
// because an unhealthy store will not be helped by the remaining windows.
//
// fakeStore.insertErr exists precisely to test this and was previously never
// set by any test — inverting the behavior to `continue` would have shipped
// green while hammering a dying store with every remaining window.
func TestFillGapAbortsGapOnInsertError(t *testing.T) {
	src := &fakeSource{obs: []weather.Observation{obsAt("ST-A", 5000)}}
	store := &fakeStore{insertErr: errors.New("disk full")}

	from := at(0)
	to := from.Add(72 * time.Hour) // three windows

	stats, err := Run(t.Context(),
		Config{From: from, To: to, MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, to)

	if err == nil {
		t.Fatal("an insert failure must surface as an error")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("err = %v, want it to wrap the insert error", err)
	}
	if len(src.calls) != 1 {
		t.Errorf("made %d API calls, want 1 — an insert failure must abort the gap, "+
			"not continue fetching the remaining windows", len(src.calls))
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
}

// A permanently failing window must not abandon the windows behind it. If it
// does, those windows are never requested on ANY future run and the tool
// silently stops converging.
func TestFillGapContinuesAfterAFailedWindow(t *testing.T) {
	src := &fakeSource{
		obs: []weather.Observation{obsAt("ST-A", 5000)},
		// Window 1 succeeds, window 2 fails permanently (a status_code is not
		// retryable, so exactly one call), window 3 succeeds.
		errs: []error{nil, &tempestapi.StatusError{StatusCode: 404, Message: "NOT FOUND"}, nil},
	}
	store := &fakeStore{}

	from := at(0)
	to := from.Add(72 * time.Hour) // three 24h windows

	stats, err := Run(t.Context(),
		Config{From: from, To: to, MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, to)

	if err == nil {
		t.Fatal("the gap must still be reported failed")
	}
	if len(src.calls) != 3 {
		t.Errorf("made %d API calls, want 3 — window 3 must still be attempted after window 2 fails", len(src.calls))
	}
	// window 3 must still be OFFERED to the store even though fakeSource
	// hands back the same static observation regardless of which window
	// asked: two separate batches submitted to InsertObservations proves
	// window 3's result was not silently dropped after window 2 failed.
	if len(store.inserted) != 2 {
		t.Errorf("submitted %d batches to InsertObservations, want 2 (windows 1 and 3 each offer their result)",
			len(store.inserted))
	}
	// Inserted counts genuinely NEW rows: fakeStore dedupes on (serial,
	// timestamp), mirroring the real stores' ON CONFLICT DO NOTHING
	// (sqlite/backfill.go, postgres/backfill.go). Windows 1 and 3 return the
	// IDENTICAL observation here (fakeSource always hands back the same
	// static obs, regardless of window), so window 3's insert correctly
	// reports 0 new — a real store would do exactly the same for the same
	// key offered twice. This previously asserted 2, which only held because
	// the old fake unconditionally returned len(obs) without checking
	// whether the row had already landed; see
	// TestRunSecondPassInsertsNothingWhenNothingIsNew for the scenario that
	// exposed it.
	if stats.Inserted != 1 {
		t.Errorf("Inserted = %d, want 1 (window 1 lands the row; window 3 re-offers the identical "+
			"serial+timestamp and correctly reports 0 new)", stats.Inserted)
	}
	if stats.Returned != 2 {
		t.Errorf("Returned = %d, want 2 (windows 1 and 3 each returned one observation)", stats.Returned)
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
}

func TestRunChunksExplicitRangeIntoDays(t *testing.T) {
	src := &fakeSource{}
	store := &fakeStore{bounds: []weather.Bounds{{SerialNumber: "ST-A", First: at(0), Last: at(1)}}}

	from := at(0)
	to := from.Add(72 * time.Hour)
	_, err := Run(t.Context(),
		Config{From: from, To: to, MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, to)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(src.calls) != 3 {
		t.Fatalf("made %d API calls, want 3 single-day requests: %+v", len(src.calls), src.calls)
	}
	for i, c := range src.calls {
		if w := c.to.Sub(c.from); w > 24*time.Hour {
			t.Errorf("call %d spans %v, want <= 24h (the API caps 1-minute resolution at 5 days)", i, w)
		}
	}
}

func TestRunRetriesTransientThenSucceeds(t *testing.T) {
	src := &fakeSource{
		obs:  []weather.Observation{obsAt("ST-A", 5000)},
		errs: []error{&tempestapi.StatusError{HTTPStatus: http.StatusServiceUnavailable}},
	}
	store := &fakeStore{bounds: []weather.Bounds{{SerialNumber: "ST-A", First: at(0), Last: at(1)}}}

	from := at(0)
	stats, err := Run(t.Context(),
		Config{From: from, To: from.Add(time.Hour), MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(src.calls) != 2 {
		t.Errorf("made %d calls, want 2 (one failure + one retry)", len(src.calls))
	}
	if stats.Failed != 0 {
		t.Errorf("Failed = %d, want 0 after a successful retry", stats.Failed)
	}
	if stats.Inserted != 1 {
		t.Errorf("Inserted = %d, want 1", stats.Inserted)
	}
}

// Regression: fetchWithRetry's exhaustion path (all maxAttempts attempts
// transient) was previously dead under the suite — nothing supplied enough
// retryable failures to reach it, so raising maxAttempts (and the unbounded
// baseBackoff growth that comes with it) would have shipped green.
//
// Real backoff applies here (1s + 2s + 4s = 7s between the 4 attempts); do
// not add a second test like this.
func TestRunRetryExhaustionFailsTheWindowAfterMaxAttempts(t *testing.T) {
	src := &fakeSource{
		errs: []error{
			&tempestapi.StatusError{HTTPStatus: http.StatusServiceUnavailable},
			&tempestapi.StatusError{HTTPStatus: http.StatusServiceUnavailable},
			&tempestapi.StatusError{HTTPStatus: http.StatusServiceUnavailable},
			&tempestapi.StatusError{HTTPStatus: http.StatusServiceUnavailable},
		},
	}
	store := &fakeStore{bounds: []weather.Bounds{{SerialNumber: "ST-A", First: at(0), Last: at(1)}}}

	from := at(0)
	stats, err := Run(t.Context(),
		Config{From: from, To: from.Add(time.Hour), MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, from.Add(time.Hour))
	if err == nil {
		t.Fatal("exhausting every retry attempt must surface as an error")
	}
	if len(src.calls) != 4 {
		t.Errorf("made %d calls, want 4 (maxAttempts) — raising maxAttempts must show up here", len(src.calls))
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
	if !strings.Contains(err.Error(), "4 attempts") {
		t.Errorf("err = %v, want it to mention the attempt count", err)
	}
}

func TestRunDoesNotRetryNonTransient(t *testing.T) {
	src := &fakeSource{errs: []error{&tempestapi.StatusError{StatusCode: 404, Message: "NOT FOUND"}}}
	store := &fakeStore{bounds: []weather.Bounds{{SerialNumber: "ST-A", First: at(0), Last: at(1)}}}

	from := at(0)
	stats, err := Run(t.Context(),
		Config{From: from, To: from.Add(time.Hour), MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, from.Add(time.Hour))
	if err == nil {
		t.Fatal("a failed gap must surface as an error")
	}
	if len(src.calls) != 1 {
		t.Errorf("made %d calls, want 1 (an API-level status_code is never transient)", len(src.calls))
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
}

func TestRunContinuesAfterAFailedGapAndReportsBoth(t *testing.T) {
	// Two gaps; the first window fails permanently, the second succeeds.
	src := &fakeSource{
		obs:  []weather.Observation{obsAt("ST-A", 5000)},
		errs: []error{&tempestapi.StatusError{StatusCode: 404}},
	}
	store := &fakeStore{
		bounds: []weather.Bounds{{SerialNumber: "ST-A", First: at(10000), Last: at(20000)}},
		gaps:   nil,
	}

	stats, err := Run(t.Context(),
		Config{MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, at(200000))
	if err == nil {
		t.Fatal("want a non-nil error when any gap failed")
	}
	if stats.Failed == 0 {
		t.Error("Failed should count the failed gap")
	}
	if stats.Inserted == 0 {
		t.Error("the surviving gap should still have inserted rows — partial progress must be preserved")
	}
}

func TestRunBoundsInsertBatchSize(t *testing.T) {
	// 500 observations must arrive as batches of at most 200: an unbounded
	// transaction contends with live ingest, whose error path only logs.
	var many []weather.Observation
	for i := range 500 {
		many = append(many, obsAt("ST-A", int64(5000+i*60)))
	}
	src := &fakeSource{obs: many}
	store := &fakeStore{bounds: []weather.Bounds{{SerialNumber: "ST-A", First: at(0), Last: at(1)}}}

	from := at(0)
	stats, err := Run(t.Context(),
		Config{From: from, To: from.Add(time.Hour), MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Returned != 500 {
		t.Errorf("Returned = %d, want 500 (observations the API handed back, not rows requested)", stats.Returned)
	}
	if len(store.inserted) < 3 {
		t.Fatalf("got %d batches, want at least 3 for 500 rows at max 200/batch", len(store.inserted))
	}
	for i, b := range store.inserted {
		if len(b) > 200 {
			t.Errorf("batch %d has %d rows, want <= 200", i, len(b))
		}
	}
}

func TestRunHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	src := &fakeSource{obs: []weather.Observation{obsAt("ST-A", 5000)}}
	store := &fakeStore{bounds: []weather.Bounds{{SerialNumber: "ST-A", First: at(0), Last: at(1)}}}

	from := at(0)
	_, err := Run(ctx,
		Config{From: from, To: from.Add(72 * time.Hour), MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, from.Add(72*time.Hour))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	// Cancellation must be honored PROMPTLY — before any window is fetched or
	// inserted — not merely reported after the run has already done all the
	// work. Otherwise a cancelled multi-hour backfill would still perform
	// every API call and every insert before finally returning
	// context.Canceled, which is indistinguishable from ignoring cancellation.
	if len(src.calls) != 0 {
		t.Errorf("made %d API calls under a cancelled context, want 0", len(src.calls))
	}
	if len(store.inserted) != 0 {
		t.Errorf("made %d inserts under a cancelled context, want 0", len(store.inserted))
	}
}

// This is the design's actual convergence scenario (Stats.Inserted's doc
// comment): a station that is genuinely offline yields the exact same
// observations on every run, so a second Run against the same store must
// report Returned > 0 (the API still hands back what it has) and
// Inserted == 0 (every row already landed). Automation watches for exactly
// this "Inserted == 0 across runs" signal to decide a repair has converged
// or is permanently stuck — see Stats.Inserted and fillGap's doc comments.
//
// This is the regression guard for `inserted += n` in fillGap: replacing it
// with `inserted += len(chunk)` (discarding the store's real insert count)
// leaves Returned == Inserted on every run, so a permanently-offline station
// would report inserted>0 forever and automation would never notice.
func TestRunSecondPassInsertsNothingWhenNothingIsNew(t *testing.T) {
	src := &fakeSource{obs: []weather.Observation{obsAt("ST-A", 5000)}}
	store := &fakeStore{}

	from := at(0)
	to := from.Add(time.Hour)
	cfg := Config{From: from, To: to, MinGap: 30 * time.Minute}
	stations := []tempestapi.Station{station("ST-A")}

	first, err := Run(t.Context(), cfg, src, store, stations, to)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Inserted == 0 {
		t.Fatal("first run should have inserted the fresh row")
	}

	second, err := Run(t.Context(), cfg, src, store, stations, to)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Returned == 0 {
		t.Error("second run must still fetch — the API always returns what it has")
	}
	if second.Inserted != 0 {
		t.Errorf("second run Inserted = %d, want 0 — every row already landed on the "+
			"first run, which is the permanent-hole/already-converged signal automation "+
			"watches for", second.Inserted)
	}
}

// A half-specified range (only From, or only To) must be rejected outright,
// not silently discarded in favor of a full auto-detect. Auto-detect over a
// long-lived station is hours of API hammering — exactly what an explicit
// --from/--to is meant to let an operator avoid (see Config.From's doc
// comment). Both subcases share one assertion set, which is why they are one
// table rather than two near-duplicate test functions.
func TestRunRejectsHalfSpecifiedRange(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"From set, To zero", Config{From: at(1000), MinGap: 30 * time.Minute}},
		{"To set, From zero", Config{To: at(1000), MinGap: 30 * time.Minute}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{obs: []weather.Observation{obsAt("ST-A", 5000)}}
			store := &fakeStore{}

			_, err := Run(t.Context(), tt.cfg,
				src, store, []tempestapi.Station{station("ST-A")}, at(200000))
			if err == nil {
				t.Fatal("a half-specified range must be rejected, got nil error")
			}
			if len(src.calls) != 0 {
				t.Errorf("made %d API calls for a rejected config, want 0", len(src.calls))
			}
			if len(store.inserted) != 0 {
				t.Errorf("made %d inserts for a rejected config, want 0", len(store.inserted))
			}
		})
	}
}
