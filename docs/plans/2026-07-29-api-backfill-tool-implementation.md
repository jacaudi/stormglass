# API Backfill Tool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **For Claude:** REQUIRED EXECUTION WORKFLOW (follow in order):
> 1. `superpowers:using-git-worktrees` — **already satisfied**; work in `~/Projects/github/tempestwx-exporter/.claude/worktrees/api-backfill` on branch `worktree-api-backfill`. Do NOT create a new worktree.
> 2. `superpowers:subagent-driven-development` — Dispatch a fresh subagent per task
> 3. `superpowers:test-driven-development` — All subagents use TDD
> 4. `superpowers:verification-before-completion` — Verify all tests pass per task
> 5. `superpowers:requesting-code-review` — Code review after each task (built in)
> 6. After all tasks: comprehensive cold review on the full diff from branch point
> 7. `superpowers:finishing-a-development-branch` — Complete the branch
>
> Skills carry their own model and effort settings. Do not override them.

**Goal:** Add a `backfill` subcommand that finds holes in the local observation history and fills them from the Tempest REST API, idempotently and safely re-runnably.

**Architecture:** A new leaf package `internal/weather` owns the store-neutral `Observation` and `Gap` types. The two store packages gain package-level functions (not writer methods — the writer's single-goroutine invariant forbids it) for gap detection and insertion. A new `internal/backfill` package holds a testable core with all dependencies injected, including the clock. `main.go` gains a thin shell and a real subcommand dispatch.

**Tech Stack:** Go 1.25, `database/sql` + `modernc.org/sqlite` (CGO_ENABLED=0), `pgx/v5` + `pgxpool`, stdlib `flag`, `log/slog`, stdlib testing (no testify).

**Design:** `docs/designs/2026-07-28-api-backfill-tool-design.md` — read it before starting. This plan implements it; where they disagree, the design wins and the plan is wrong.

## Global Constraints

- **Go 1.25 floor** (`go.mod` says `go 1.25.0`). **No Go 1.26-only APIs** — in particular use `errors.As`, **never** `errors.AsType`.
- **`CGO_ENABLED=0` must stay buildable.** No new cgo dependencies.
- **Zero new module dependencies.** Nothing gets added to `go.mod`.
- **`log/slog` for all new code.** Do not add `log.Printf` call sites; do not convert existing ones.
- **No goroutines** in any new package-level store function. The `sqlite.Writer.run` goroutine is documented as "the only goroutine that ever touches db" — new functions take a `*sql.DB` / `*pgxpool.Pool` directly and must not be methods on the writer types.
- **`sqlite.Open` sets `db.SetMaxOpenConns(1)`** (`internal/sqlite/db.go:73`). Any query must fully materialize its results and close its `*sql.Rows` before another query or insert runs on the same handle. A streaming iterator would deadlock with no error and no timeout.
- **Timestamps are UTC.** SQLite stores unix-epoch `INTEGER`; Postgres stores `TIMESTAMPTZ`. Conversion happens at the SQLite boundary only.
- **Tests use repo idiom:** `t.Context()`, `t.TempDir()`, stdlib table-driven subtests, no testify.
- **The full gate must pass before any task is reported complete:**
  ```bash
  CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && golangci-lint run ./... && go test ./... -race
  ```
  `gofmt -l .` must print **nothing**. Paste real output; do not assert success without it.
  **`go build ./...` does not compile `_test.go` files** — only `go vet` and `go test` do. Never cite `go build` as proof that a rename reached the tests.
- **Prefer stdlib over hand-rolled helpers.** `slices.Chunk`, `slices.SortFunc`, `cmp.Or`, `min`/`max`, range-over-int and range-over-func are all available and are the house style. Do not write a loop the stdlib already provides.
- **Work in the existing worktree.** Every task begins by confirming `pwd` ends in `.claude/worktrees/api-backfill` and that `docs/designs/2026-07-28-api-backfill-tool-design.md` exists. If not, STOP and report `NEEDS_CONTEXT`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/weather/observation.go` | **Create.** Store-neutral `Observation` + `Gap` + `Bounds`. Imports nothing from this repo. |
| `internal/tempestapi/errors.go` | **Create.** `StatusError` — the typed error the retry layer classifies on. |
| `internal/tempestapi/client.go` | **Modify.** Export `Station.SerialNumber`/`.DeviceID`. |
| `internal/tempestapi/observations.go` | **Create.** `Client.Observations` — nullable decode, status handling, empty windows. |
| `internal/postgres/pool.go` | **Create.** `OpenPool` — the single source of pool tuning. |
| `internal/postgres/writer.go` | **Modify.** `NewPostgresWriter` calls `OpenPool`. |
| `internal/sqlite/backfill.go` | **Create.** `SeriesBounds`, `FindObservationGaps`, `InsertObservations` over `*sql.DB`. |
| `internal/postgres/backfill.go` | **Create.** Same three functions over `*pgxpool.Pool`. |
| `internal/backfill/window.go` | **Create.** Pure chunking + retry classification. |
| `internal/backfill/gaps.go` | **Create.** Detection-domain assembly (head/tail/interior/empty). |
| `internal/backfill/backfill.go` | **Create.** `Run` — the testable core, plus `Config`, `Stats`, and the two consumer-side interfaces. |
| `main.go` | **Modify.** `runBackfill` shell + subcommand dispatch. |
| `CLAUDE.md` | **Modify.** Resolve the name collision, document the subcommand. |

**Dependency order:** T1 → (T2, T4 parallel) → T3 → (T5, T6 parallel) → T7 → T8 → T9 → T10 → T11.

---

### Task 1: `internal/weather` — store-neutral types

**Files:**
- Create: `internal/weather/observation.go`
- Test: `internal/weather/observation_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `weather.Observation`, `weather.Gap`, `weather.Bounds` — used by every subsequent task.

**Why this package exists:** both store packages and the REST client must name these types. They cannot live in `tempestapi` (storage would depend on the REST client), cannot live in `tempestudp` (that package is the UDP wire protocol, and `tempestudp.Gap` would be a lie), and cannot live in `internal/backfill` (`FindObservationGaps` is a package-level function *in the store packages*, so they would have to import their own consumer). `internal/weather` imports nothing from this repo, so nothing can cycle.

**Nullability:** every measurement field is `*float64`. The SQLite DDL declares every column except `id`, `serial_number`, and `timestamp` as nullable (`internal/sqlite/migrations/0001_init.sql:2-14`), and the API may return JSON `null` for any element. `InsertObservations` binds these pointers straight into the query — `database/sql` and `pgx` both map a nil `*float64` to SQL NULL — so this type never funnels through the stores' private `observationRow`, whose first thirteen fields are non-pointer `float64` and would silently coerce a null to `0.0`.

- [ ] **Step 1: Write the failing test**

Create `internal/weather/observation_test.go`:

```go
package weather

import (
	"testing"
	"time"
)

func TestGapDuration(t *testing.T) {
	g := Gap{
		SerialNumber: "ST-00000001",
		From:         time.Unix(1000, 0).UTC(),
		To:           time.Unix(4600, 0).UTC(),
	}
	if got, want := g.Duration(), time.Hour; got != want {
		t.Errorf("Duration() = %v, want %v", got, want)
	}
}

// The invariant this pins is that the measurement fields are POINTER-typed,
// so a JSON null can round-trip to SQL NULL. It asserts through the type
// system rather than through behavior — the behavioral proof is in Task 3's
// decode test and Task 5's insert test.
func TestObservationMeasurementFieldsArePointers(t *testing.T) {
	var o Observation
	// Assigning nil compiles only if these are pointers.
	o.Pressure, o.TempAir, o.Battery = nil, nil, nil
	if o.Pressure != nil || o.TempAir != nil || o.Battery != nil {
		t.Error("measurement fields must be *float64 so JSON null maps to SQL NULL")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/weather/ -run 'TestGap|TestObservation' -v`
Expected: FAIL — the package does not exist (`no Go files in .../internal/weather`).

Note the `Bounds` type below has no dedicated test: it is a plain data carrier with no behavior, and Task 5/8 exercise it end-to-end. Do not add a zero-value test for it.

- [ ] **Step 3: Write minimal implementation**

Create `internal/weather/observation.go`:

```go
// Package weather holds the store-neutral representation of a weather
// observation and of a hole in an observation series.
//
// It deliberately imports nothing else in this repository. Both store
// packages (internal/sqlite, internal/postgres) and the REST client
// (internal/tempestapi) name these types, so any home that imported one of
// them would create a cycle. It is not the UDP wire protocol — that is
// internal/tempestudp — and these types must not be added there.
package weather

import "time"

// Observation is one tempest_observations row in store-neutral form.
//
// Every measurement is a *float64 because the SQLite and Postgres DDL declare
// every column except id/serial_number/timestamp as nullable, and the Tempest
// REST API may return JSON null for any element of an obs tuple. A nil here
// means SQL NULL; database/sql and pgx both bind it that way directly.
//
// This is deliberately NOT the stores' private observationRow types, whose
// leading fields are non-pointer float64: unmarshalling a JSON null into a
// non-pointer numeric is a silent no-op that yields 0.0, which would write
// "pressure = 0.0 mb" where the API said "unknown". See the design's
// "Nullability — mandatory" section.
type Observation struct {
	// SerialNumber and Timestamp are the series key and are never NULL.
	// An observation whose ob[0] is null cannot be keyed and is dropped
	// at decode time rather than represented here.
	SerialNumber string
	Timestamp    time.Time

	WindLull             *float64 // obs_st[1]
	WindAvg              *float64 // obs_st[2]
	WindGust             *float64 // obs_st[3]
	WindDirection        *float64 // obs_st[4]
	WindSampleInterval   *float64 // obs_st[5]
	Pressure             *float64 // obs_st[6], raw mb (no conversion)
	TempAir              *float64 // obs_st[7]
	Humidity             *float64 // obs_st[8]
	Illuminance          *float64 // obs_st[9]
	UVIndex              *float64 // obs_st[10]
	Irradiance           *float64 // obs_st[11]
	RainRate             *float64 // obs_st[12]
	PrecipType           *float64 // obs_st[13]
	LightningDistance    *float64 // obs_st[14]
	LightningStrikeCount *float64 // obs_st[15]
	Battery              *float64 // obs_st[16]
	ReportInterval       *float64 // obs_st[17]
}

// NOTE: there is deliberately no TempWetbulb field. The API does not return
// wet bulb; each store derives it at its own insert boundary from
// TempAir/Humidity/Pressure using tempestudp.WetBulbTemperatureC. A field
// here would never be read by any code path, and setting it would silently
// do nothing — dead code that looks load-bearing.

// Gap is a CLOSED hole [From, To] in one station's observation series.
//
// Closed, not half-open: every producer emits endpoints that are rows which
// already exist (prev/next from LAG, First/Last from SeriesBounds), so both
// ends get re-fetched and re-offered to the store. ON CONFLICT DO NOTHING
// absorbs the duplicates, so this costs nothing but a slightly inflated
// Requested count — but the doc must not claim a half-open interval it does
// not have.
//
// The series is keyed by (SerialNumber, Timestamp) — the same uniqueness
// contract idempotent inserts rely on — so a Gap is meaningless without its
// serial.
type Gap struct {
	SerialNumber string
	From         time.Time
	To           time.Time
}

// Duration is the width of the gap.
func (g Gap) Duration() time.Duration { return g.To.Sub(g.From) }

// Bounds is the first and last observation timestamp held for one serial
// within a queried window. It is what lets the caller find head and tail
// gaps, which a SQL LAG window function cannot see: LAG yields NULL for the
// first row of each partition, so it finds interior gaps only.
type Bounds struct {
	SerialNumber string
	First        time.Time
	Last         time.Time
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/weather/ -v`
Expected: PASS — both tests.

- [ ] **Step 5: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```
Expected: build clean, vet clean, `gofmt -l .` prints nothing, all tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/weather/
git commit -m "feat(weather): add store-neutral Observation, Gap, and Bounds types"
```

---

### Task 2: `tempestapi` — export Station fields, add `StatusError` and `ListDevices`

**Files:**
- Modify: `internal/tempestapi/client.go` — `Station` struct (`:53-59`), the `ListStations` construction (`:117-125`), `:131` (`GetObservations` URL), `:160` (serial assignment), plus the shared-decode extraction
- Modify: `internal/tempestapi/client_test.go` — **20 references** must be renamed (see Step 4b); also gains the `ListDevices` tests
- Create: `internal/tempestapi/errors.go`
- Test: `internal/tempestapi/errors_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `tempestapi.Station{Name string; StationID int; DeviceID int; SerialNumber string; CreatedAt time.Time}`
  - `tempestapi.StatusError{HTTPStatus, StatusCode int; Message string}` with `func (e *StatusError) Error() string`
  - `func (c *Client) ListDevices(ctx context.Context) ([]Station, error)` — one entry per `ST` device across all stations

**Why:** `Station.serialNumber` and `.deviceID` are currently **unexported** (`client.go:55-56`), so the serial pre-flight check cannot read a serial from outside the package. Export the fields rather than adding getters: `Station` is a plain data struct with three already-exported fields and no invariants to protect, so two getters would be pure ceremony.

Three behaviors branch on the *kind* of error (retry on 429/5xx/network; a non-zero `status_code` is a real failure; exit non-zero if any gap failed). Without a typed error the retry layer could only string-match.

- [ ] **Step 1: Write the failing test**

Create `internal/tempestapi/errors_test.go`:

```go
package tempestapi

import (
	"errors"
	"fmt"
	"testing"
)

func TestStatusErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  *StatusError
		want string
	}{
		{
			name: "api level status code",
			err:  &StatusError{StatusCode: 404, Message: "NOT FOUND"},
			want: "weatherflow status_code 404: NOT FOUND",
		},
		{
			name: "http level status",
			err:  &StatusError{HTTPStatus: 503},
			want: "weatherflow API status 503",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The retry layer classifies with errors.As, so StatusError must survive
// wrapping. NOTE: errors.As, never errors.AsType — that is Go 1.26 and
// go.mod declares go 1.25.0.
func TestStatusErrorUnwrapsThroughFmtErrorf(t *testing.T) {
	wrapped := fmt.Errorf("fetch window: %w", &StatusError{HTTPStatus: 429})
	var se *StatusError
	if !errors.As(wrapped, &se) {
		t.Fatal("errors.As failed to extract *StatusError from a wrapped error")
	}
	if se.HTTPStatus != 429 {
		t.Errorf("HTTPStatus = %d, want 429", se.HTTPStatus)
	}
}

func TestStationFieldsAreExported(t *testing.T) {
	s := Station{SerialNumber: "ST-00000001", DeviceID: 42}
	if s.SerialNumber != "ST-00000001" || s.DeviceID != 42 {
		t.Error("Station.SerialNumber and Station.DeviceID must be settable from outside the package")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tempestapi/ -run 'TestStatusError|TestStationFields' -v`
Expected: FAIL to compile — `undefined: StatusError`, and `unknown field SerialNumber in struct literal`.

- [ ] **Step 3: Create the error type**

Create `internal/tempestapi/errors.go`:

```go
package tempestapi

import "fmt"

// StatusError is a failed Tempest REST call, carrying enough structure for a
// caller to decide whether retrying could help. It exists because three
// behaviors branch on the KIND of failure — retry on 429/5xx/network, treat a
// non-zero API status_code as a real failure, and exit non-zero if any gap
// failed — and string-matching an opaque error is not a classification.
//
// Exactly one of the two codes is meaningful per instance:
//
//   - HTTPStatus != 0: the transport-level response was not 200. Transient
//     for 429 and 5xx.
//   - StatusCode != 0: the response was HTTP 200 but WeatherFlow reported an
//     application-level failure in its status envelope. Never transient.
//
// Classify with errors.As, NOT errors.AsType (Go 1.26; go.mod declares 1.25.0).
type StatusError struct {
	HTTPStatus int    // HTTP response status, 0 when the failure is API-level
	StatusCode int    // WeatherFlow status.status_code, 0 when purely HTTP
	Message    string // WeatherFlow status.status_message, if any
}

func (e *StatusError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("weatherflow status_code %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("weatherflow API status %d", e.HTTPStatus)
}
```

- [ ] **Step 4: Export the Station fields**

In `internal/tempestapi/client.go`, change the struct (currently at `:53-58`):

```go
type Station struct {
	Name         string
	StationID    int
	DeviceID     int
	SerialNumber string
	CreatedAt    time.Time
}
```

Update the construction site in `ListStations` (currently `:116-121`):

```go
		if deviceId != 0 && instance != "" {
			out = append(out, Station{
				Name:         station.Name,
				DeviceID:     deviceId,
				SerialNumber: instance,
				StationID:    station.StationID,
				CreatedAt:    time.Unix(station.CreatedEpoch, 0),
			})
		}
```

Update the two read sites in `GetObservations`:
- `:131` — `station.deviceID` becomes `station.DeviceID`
- `:160` — `r.SerialNumber = station.serialNumber` becomes `r.SerialNumber = station.SerialNumber`

Do **not** change any other behavior in `client.go`. In particular, leave the missing `break` in the device loop alone — `ModeAPIExport` depends on current behavior and fixing it is an explicit non-goal.

- [ ] **Step 4b: Rename the 20 references in `client_test.go` — REQUIRED, and expected**

Exporting these fields **breaks `internal/tempestapi/client_test.go` in 20 places**. This is not optional collateral to be worked around; it is a mechanical rename with no behavior change, and it is in scope for this task.

- **12 composite-literal keys** — `:421, :422, :464, :465, :485, :500, :515, :542, :660, :661, :764, :787`
  `deviceID:` → `DeviceID:`, `serialNumber:` → `SerialNumber:`
- **8 selector accesses** — `:94, :95, :97, :98, :222, :223, :225, :226`
  `station.deviceID` → `station.DeviceID`, `station.serialNumber` → `station.SerialNumber`

Verify none remain:

```bash
grep -n "deviceID\|serialNumber" internal/tempestapi/*.go
```
Expected: **no output.**

Do **not** work around this by adding accessor methods or keeping shadow unexported fields — the whole point of Task 2 is that `Station` is a plain data struct.

- [ ] **Step 4c: Write the failing test for `ListDevices`**

`ListStations` returns **one already-collapsed `Station` per station**: its device loop (`client.go:110-115`) has no `break`, so within a station the *last* `ST` device overwrites the others. Backfill must reach every sensor, so it needs a device-level enumeration. Append to `internal/tempestapi/client_test.go`:

```go
func TestListDevicesReturnsEverySTDevice(t *testing.T) {
	// One station, TWO Tempest sensors, plus a hub that must be ignored.
	body := `{"status":{"status_code":0,"status_message":"SUCCESS"},"stations":[{
		"station_id": 1, "name": "Home", "created_epoch": 1600000000,
		"devices": [
			{"device_id": 11, "device_type": "HB", "serial_number": "HB-00000001"},
			{"device_id": 22, "device_type": "ST", "serial_number": "ST-00000022"},
			{"device_id": 33, "device_type": "ST", "serial_number": "ST-00000033"}
		]
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := NewClient("test-token", WithBaseURL(srv.URL))

	devices, err := c.ListDevices(t.Context())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2 (both ST sensors, hub excluded)", len(devices))
	}
	got := map[string]int{}
	for _, d := range devices {
		got[d.SerialNumber] = d.DeviceID
		if !d.CreatedAt.Equal(time.Unix(1600000000, 0)) {
			t.Errorf("%s CreatedAt = %v, want the owning station's created_epoch", d.SerialNumber, d.CreatedAt)
		}
	}
	if got["ST-00000022"] != 22 || got["ST-00000033"] != 33 {
		t.Errorf("device map = %v, want ST-00000022→22 and ST-00000033→33", got)
	}
}

// ListStations must keep its existing one-per-station collapse — ModeAPIExport
// depends on it. This pins the divergence so the shared decode refactor cannot
// silently change it.
func TestListStationsStillCollapsesToOneDevicePerStation(t *testing.T) {
	body := `{"status":{"status_code":0,"status_message":"SUCCESS"},"stations":[{
		"station_id": 1, "name": "Home", "created_epoch": 1600000000,
		"devices": [
			{"device_id": 22, "device_type": "ST", "serial_number": "ST-00000022"},
			{"device_id": 33, "device_type": "ST", "serial_number": "ST-00000033"}
		]
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := NewClient("test-token", WithBaseURL(srv.URL))

	stations, err := c.ListStations(t.Context())
	if err != nil {
		t.Fatalf("ListStations: %v", err)
	}
	if len(stations) != 1 {
		t.Fatalf("got %d stations, want 1", len(stations))
	}
	if stations[0].SerialNumber != "ST-00000033" {
		t.Errorf("SerialNumber = %q, want ST-00000033 (last ST wins — unchanged behavior)", stations[0].SerialNumber)
	}
}
```

Run: `go test ./internal/tempestapi/ -run TestListDevices -v`
Expected: FAIL to compile — `c.ListDevices undefined`.

- [ ] **Step 4d: Extract the shared decode, then add `ListDevices`**

In `client.go`, lift the anonymous response struct inside `ListStations` to a named type and give both methods one fetch path. The JSON contract is shared knowledge — both would have to change together if WeatherFlow changed the payload — so it lives in one place.

```go
// stationsResponse is the /stations payload. ListStations and ListDevices
// both decode into it, so the JSON contract lives in exactly one place.
type stationsResponse struct {
	Stations []struct {
		CreatedEpoch int64 `json:"created_epoch"`
		Devices      []struct {
			DeviceID     int    `json:"device_id"`
			DeviceType   string `json:"device_type"`
			SerialNumber string `json:"serial_number"`
		} `json:"devices"`
		Name      string `json:"name"`
		StationID int    `json:"station_id"`
	} `json:"stations"`
	Status struct {
		StatusCode    int    `json:"status_code"`
		StatusMessage string `json:"status_message"`
	} `json:"status"`
}

// fetchStations performs the GET /stations call and validates the status
// envelope. Behavior is byte-identical to what ListStations did inline.
func (c *Client) fetchStations(ctx context.Context) (*stationsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/stations", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{HTTPStatus: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}

	var data stationsResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	if data.Status.StatusCode != 0 {
		return nil, &StatusError{
			StatusCode: data.Status.StatusCode,
			Message:    data.Status.StatusMessage,
		}
	}
	return &data, nil
}

// ListDevices returns one Station per ST device across all stations.
//
// It exists because ListStations collapses each station to a SINGLE ST device
// (its loop has no break, so the last one wins). With two Tempest units on one
// station that silently loses a sensor — the UDP listener records both serials,
// but a caller driven by ListStations would only ever learn one, and the other
// unit's gaps would never close, unlogged.
//
// ListStations is deliberately left with its collapsing behavior: ModeAPIExport
// depends on it, and changing it is an explicit non-goal.
func (c *Client) ListDevices(ctx context.Context) ([]Station, error) {
	data, err := c.fetchStations(ctx)
	if err != nil {
		return nil, err
	}
	var out []Station
	for _, station := range data.Stations {
		for _, dev := range station.Devices {
			if dev.DeviceType != "ST" {
				continue
			}
			out = append(out, Station{
				Name:         station.Name,
				StationID:    station.StationID,
				DeviceID:     dev.DeviceID,
				SerialNumber: dev.SerialNumber,
				CreatedAt:    time.Unix(station.CreatedEpoch, 0),
			})
		}
	}
	return out, nil
}
```

Then rewrite `ListStations`' body to call `fetchStations` and keep its existing collapse loop verbatim over `data.Stations`. Its observable behavior — including last-ST-wins and the `deviceId != 0 && instance != ""` filter — must not change; `TestListStationsStillCollapsesToOneDevicePerStation` is the guard.

Note this changes `ListStations`' error *type* for HTTP and status failures from `fmt.Errorf` to `*StatusError`. That is an improvement (it makes them classifiable) and `errors.As` still matches; check `client_test.go` for any assertion on the exact error string and update it if present.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tempestapi/ -v`
Expected: PASS — the new tests, plus `client_test.go` passing **with its 20 references renamed**. The suite's *behavior* is unchanged; its *field names* are not.

- [ ] **Step 6: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && golangci-lint run ./... && go test ./... -race
```
Expected: all clean. Note that **`go build ./...` will pass even with `client_test.go` broken** — it does not compile test files. `go vet ./...` is what catches a missed rename.

- [ ] **Step 7: Commit**

```bash
git add internal/tempestapi/
git commit -m "feat(tempestapi): export Station identity, add StatusError and ListDevices"
```

---

### Task 3: `tempestapi.Client.Observations` — nullable decode

**Files:**
- Create: `internal/tempestapi/observations.go`
- Test: `internal/tempestapi/observations_test.go`

**Interfaces:**
- Consumes: `weather.Observation` (Task 1), `StatusError` + exported `Station` fields (Task 2).
- Produces: `func (c *Client) Observations(ctx context.Context, station Station, start, end time.Time) ([]weather.Observation, error)`

**Naming:** `Observations`, not `GetObservationRows`. Go getters take no `Get` prefix, and "Rows" is storage vocabulary that has no business in a REST client. It coexists with the existing `GetObservations` (which returns `[]prometheus.Metric` for `ModeAPIExport` and is untouched).

**Empty-window contract** (resolved from the published OpenAPI spec, so no token is needed to implement this): `ObservationSet` declares **no `required` array**, so neither `type` nor `obs` is required. Therefore `status_code == 0` is success; an absent, null, or empty `obs` is **zero rows, not an error**; an absent `type` is **not** an error. This method must **not** route through `tempestudp.ParseReport`, which dispatches on the top-level `type` and errors `unhandled message type: ""` on a status-only envelope — turning a legitimate empty window into a hard failure.

- [ ] **Step 1: Write the failing test**

Create `internal/tempestapi/observations_test.go`:

```go
package tempestapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient points a Client at an httptest.Server via the existing
// WithBaseURL seam (client.go:35-39).
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient("test-token", WithBaseURL(srv.URL))
}

func TestObservationsMapsNullToNilNotZero(t *testing.T) {
	// obs[6] (pressure) and obs[16] (battery) are null. They must decode to
	// nil pointers, NOT 0.0 — a 0.0 mb pressure reads as real to
	// SummarizeObservations' MIN(pressure) and to every chart.
	body := `{"status":{"status_code":0,"status_message":"SUCCESS"},"type":"obs_st","obs":[
		[1700000000,0.1,0.2,0.3,180,3,null,20.5,55.0,1000,1.5,300,0.0,0,10.0,2,null,1]
	]}`
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	obs, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1", len(obs))
	}
	if obs[0].Pressure != nil {
		t.Errorf("Pressure = %v, want nil (JSON null must not become 0.0)", *obs[0].Pressure)
	}
	if obs[0].Battery != nil {
		t.Errorf("Battery = %v, want nil", *obs[0].Battery)
	}
	if obs[0].TempAir == nil || *obs[0].TempAir != 20.5 {
		t.Errorf("TempAir = %v, want 20.5", obs[0].TempAir)
	}
	if obs[0].SerialNumber != "ST-1" {
		t.Errorf("SerialNumber = %q, want %q", obs[0].SerialNumber, "ST-1")
	}
	if !obs[0].Timestamp.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("Timestamp = %v, want %v", obs[0].Timestamp, time.Unix(1700000000, 0).UTC())
	}
}

func TestObservationsEmptyWindowIsNotAnError(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"obs empty", `{"status":{"status_code":0,"status_message":"SUCCESS"},"type":"obs_st","obs":[]}`},
		{"obs null", `{"status":{"status_code":0,"status_message":"SUCCESS"},"type":"obs_st","obs":null}`},
		{"status only, no type, no obs", `{"status":{"status_code":0,"status_message":"SUCCESS - Either no capabilities or no recent observations"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			})
			obs, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0))
			if err != nil {
				t.Fatalf("empty window must not be an error, got %v", err)
			}
			if len(obs) != 0 {
				t.Errorf("got %d observations, want 0", len(obs))
			}
		})
	}
}

func TestObservationsNonZeroStatusCodeIsStatusError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":{"status_code":404,"status_message":"NOT FOUND"}}`))
	})
	_, err := c.Observations(t.Context(), Station{DeviceID: 1}, time.Unix(0, 0), time.Unix(1, 0))
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want *StatusError, got %T: %v", err, err)
	}
	if se.StatusCode != 404 || se.Message != "NOT FOUND" {
		t.Errorf("got %+v, want StatusCode 404 / NOT FOUND", se)
	}
}

func TestObservationsHTTPErrorIsStatusError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := c.Observations(t.Context(), Station{DeviceID: 1}, time.Unix(0, 0), time.Unix(1, 0))
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want *StatusError, got %T: %v", err, err)
	}
	if se.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("HTTPStatus = %d, want 503", se.HTTPStatus)
	}
}

func TestObservationsSkipsRowWithNullTimestamp(t *testing.T) {
	// A row with no timestamp cannot be keyed by (serial, timestamp) and
	// must be dropped rather than written at the epoch.
	body := `{"status":{"status_code":0},"type":"obs_st","obs":[
		[null,0.1,0.2,0.3,180,3,1013.0,20.5,55.0,1000,1.5,300,0.0,0,10.0,2,2.6,1],
		[1700000000,0.1,0.2,0.3,180,3,1013.0,20.5,55.0,1000,1.5,300,0.0,0,10.0,2,2.6,1]
	]}`
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	obs, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1 (the null-timestamp row must be dropped)", len(obs))
	}
}

// The guards are GRADUATED, matching the UDP ingest path (>= 13 floor, tail
// filled conditionally). A 13-element tuple must be KEPT with its core
// measurements and NULL for the indices it does not carry — not dropped.
func TestObservationsKeepsShortTupleWithNullTail(t *testing.T) {
	// Exactly 13 elements: timestamp through rain_rate, nothing after.
	body := `{"status":{"status_code":0},"type":"obs_st","obs":[
		[1700000000,0.1,0.2,0.3,180,3,1013.0,20.5,55.0,1000,1.5,300,0.0]
	]}`
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	obs, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1 — a 13-element tuple must not be dropped", len(obs))
	}
	if obs[0].TempAir == nil || *obs[0].TempAir != 20.5 {
		t.Errorf("TempAir = %v, want 20.5 (core measurements must survive)", obs[0].TempAir)
	}
	if obs[0].Battery != nil {
		t.Errorf("Battery = %v, want nil (index 16 absent from a 13-element tuple)", *obs[0].Battery)
	}
	if obs[0].ReportInterval != nil {
		t.Errorf("ReportInterval = %v, want nil (index 17 absent)", *obs[0].ReportInterval)
	}
}

// Below the floor there is nothing usable, so the tuple is dropped.
func TestObservationsDropsTupleBelowFloor(t *testing.T) {
	body := `{"status":{"status_code":0},"type":"obs_st","obs":[[1700000000,0.1,0.2]]}`
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	obs, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if len(obs) != 0 {
		t.Errorf("got %d observations, want 0 — a 3-element tuple is below the 13 floor", len(obs))
	}
}

func TestObservationsRequestsCorrectWindow(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"status":{"status_code":0},"obs":[]}`))
	})
	_, err := c.Observations(t.Context(), Station{DeviceID: 77}, time.Unix(1700000000, 0), time.Unix(1700086400, 0))
	if err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if want := "time_start=1700000000&time_end=1700086400"; gotQuery != want {
		t.Errorf("query = %q, want %q", gotQuery, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tempestapi/ -run TestObservations -v`
Expected: FAIL to compile — `c.Observations undefined (type *Client has no field or method Observations)`.

- [ ] **Step 3: Write the implementation**

Create `internal/tempestapi/observations.go`:

```go
package tempestapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"tempestwx-utilities/internal/weather"
)

// observationSet mirrors the Tempest ObservationSet response schema.
//
// Obs is [][]*float64, not [][]float64: unmarshalling a JSON null into a
// non-pointer numeric is a silent no-op that yields 0.0, which would write a
// physically meaningful "pressure = 0.0 mb" where the API said "unknown".
// Backfill operates precisely on marginal windows, where nulls are most
// likely. Do not "simplify" this to [][]float64 and do not reuse
// tempestudp.TempestObservationReport, whose Obs is [][]float64.
//
// Every field is optional. The published OpenAPI schema declares no `required`
// array for ObservationSet, so a response may legitimately omit both `type`
// and `obs` — that is an empty window, not an error.
type observationSet struct {
	Status struct {
		StatusCode    int    `json:"status_code"`
		StatusMessage string `json:"status_message"`
	} `json:"status"`
	Type string       `json:"type"`
	Obs  [][]*float64 `json:"obs"`
}

// obs_st tuple indices. Indices 0-17 match the UDP layout exactly, so the
// same field semantics apply. The REST array carries 22 elements; 18-21
// (local-day rain accumulation, Nearcast accumulations, precip analysis type)
// map to no column in tempest_observations and are ignored deliberately.
const (
	obsTimestamp = iota
	obsWindLull
	obsWindAvg
	obsWindGust
	obsWindDirection
	obsWindSampleInterval
	obsPressure
	obsTempAir
	obsHumidity
	obsIlluminance
	obsUVIndex
	obsIrradiance
	obsRainRate
	obsPrecipType
	obsLightningDistance
	obsLightningStrikeCount
	obsBattery
	obsReportInterval
	obsFieldCount // 18 — the number of indices this code reads
)

// obsMinFields is the floor below which a tuple carries no usable core
// measurements. It mirrors the UDP ingest path, which accepts a report at
// len(ob) >= 13 and fills the tail conditionally (sqlite/writer.go:406-457).
//
// The guards here are GRADUATED, not all-or-nothing. A hard
// "len(ob) < 18 -> drop" would silently discard a short tuple's temperature,
// pressure and wind — a real divergence from the ingest path the design
// forbids. Because every weather.Observation measurement is a *float64,
// honoring the graduated rule is free: absent indices simply stay nil.
const obsMinFields = 13

// Observations fetches raw historical observations for one device over
// [start, end] and returns them in store-neutral form.
//
// It is deliberately separate from GetObservations, which returns
// []prometheus.Metric for ModeAPIExport and routes through
// tempestudp.ParseReport. ParseReport dispatches on the top-level `type` and
// fails with `unhandled message type: ""` on a status-only envelope, which
// would turn a legitimate empty window into a hard error.
//
// Derived columns are NOT computed here — this returns raw API fields only.
// temp_wetbulb is derived at the store boundary so backfilled and live rows
// stay indistinguishable.
func (c *Client) Observations(ctx context.Context, station Station, start, end time.Time) ([]weather.Observation, error) {
	url := fmt.Sprintf("%s/observations/device/%d?time_start=%d&time_end=%d",
		c.baseURL, station.DeviceID, start.Unix(), end.Unix())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{HTTPStatus: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}

	var set observationSet
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("decode observation set: %w", err)
	}

	// A non-zero status_code is a real, non-transient API failure. A zero
	// status_code with an absent/null/empty obs is an empty window: zero
	// rows, no error.
	if set.Status.StatusCode != 0 {
		return nil, &StatusError{
			StatusCode: set.Status.StatusCode,
			Message:    set.Status.StatusMessage,
		}
	}

	// at returns ob[i] when the tuple is long enough, nil otherwise. This is
	// the graduated guard: a short tuple keeps its core measurements and gets
	// NULL for the indices it does not carry.
	at := func(ob []*float64, i int) *float64 {
		if i < len(ob) {
			return ob[i]
		}
		return nil
	}

	out := make([]weather.Observation, 0, len(set.Obs))
	dropped := 0
	for _, ob := range set.Obs {
		if len(ob) < obsMinFields || ob[obsTimestamp] == nil {
			// Below the floor, or no timestamp: the row cannot be keyed by
			// (serial_number, timestamp), so writing it would create an
			// un-dedupable row at some arbitrary instant. Drop it — and count
			// it, so an all-malformed window is distinguishable from a
			// permanent hole.
			dropped++
			continue
		}
		out = append(out, weather.Observation{
			SerialNumber:         station.SerialNumber,
			Timestamp:            time.Unix(int64(*ob[obsTimestamp]), 0).UTC(),
			WindLull:             at(ob, obsWindLull),
			WindAvg:              at(ob, obsWindAvg),
			WindGust:             at(ob, obsWindGust),
			WindDirection:        at(ob, obsWindDirection),
			WindSampleInterval:   at(ob, obsWindSampleInterval),
			Pressure:             at(ob, obsPressure),
			TempAir:              at(ob, obsTempAir),
			Humidity:             at(ob, obsHumidity),
			Illuminance:          at(ob, obsIlluminance),
			UVIndex:              at(ob, obsUVIndex),
			Irradiance:           at(ob, obsIrradiance),
			RainRate:             at(ob, obsRainRate),
			PrecipType:           at(ob, obsPrecipType),
			LightningDistance:    at(ob, obsLightningDistance),
			LightningStrikeCount: at(ob, obsLightningStrikeCount),
			Battery:              at(ob, obsBattery),
			ReportInterval:       at(ob, obsReportInterval),
		})
	}

	if dropped > 0 {
		// WARN, not silence: without this, a window whose tuples were all
		// malformed reports zero rows — byte-identical to the permanent-hole
		// signal the reporting design rests on.
		slog.Warn("tempestapi: dropped malformed observation tuples",
			"serial", station.SerialNumber, "dropped", dropped, "total", len(set.Obs),
			"start", start, "end", end)
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tempestapi/ -run TestObservations -v`
Expected: PASS — all six tests.

- [ ] **Step 5: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

- [ ] **Step 6: Commit**

```bash
git add internal/tempestapi/observations.go internal/tempestapi/observations_test.go
git commit -m "feat(tempestapi): add Observations with null-preserving decode"
```

---

### Task 4: `postgres.OpenPool` — single-source the pool contract

**Files:**
- Create: `internal/postgres/pool.go`
- Modify: `internal/postgres/writer.go:144-167`
- Test: `internal/postgres/pool_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error)`

**Why:** there is currently **no way to obtain a `*pgxpool.Pool`**. `NewPostgresWriter` is the only constructor, and besides building the pool it pings, runs `CreateSchema`, and starts **four** background goroutines (`writer.go:190-199`) — all forbidden for a one-shot tool. Copying the five tuning lines out of `writer.go:151-155` would duplicate a genuine shared contract: change a pool setting and both callers must change together. So extract, and have `NewPostgresWriter` call it.

`OpenPool` deliberately does **not** run `CreateSchema` — backfill calls it explicitly, so schema creation stays an observable step of the caller rather than a side effect of opening a pool.

- [ ] **Step 1: Write the failing test**

Create `internal/postgres/pool_test.go`:

```go
package postgres

import (
	"strings"
	"testing"
	"time"
)

// OpenPool must reject a malformed URL before it attempts any connection, so
// a bad POSTGRES_URL fails fast with a useful message rather than hanging.
func TestOpenPoolRejectsBadURL(t *testing.T) {
	pool, err := OpenPool(t.Context(), "://not-a-url")
	if err == nil {
		pool.Close()
		t.Fatal("OpenPool accepted a malformed URL")
	}
	if !strings.Contains(err.Error(), "parse database url") {
		t.Errorf("error = %q, want it to mention parse database url", err)
	}
}

// The pool tuning is a shared contract between OpenPool and
// NewPostgresWriter. This pins the values so a change has to be deliberate.
func TestPoolConfigValues(t *testing.T) {
	cfg, err := poolConfig("postgres://u:p@localhost:5432/db")
	if err != nil {
		t.Fatalf("poolConfig: %v", err)
	}
	if cfg.MaxConns != 10 {
		t.Errorf("MaxConns = %d, want 10", cfg.MaxConns)
	}
	if cfg.MinConns != 2 {
		t.Errorf("MinConns = %d, want 2", cfg.MinConns)
	}
	if cfg.MaxConnLifetime != time.Hour {
		t.Errorf("MaxConnLifetime = %v, want 1h", cfg.MaxConnLifetime)
	}
	if cfg.MaxConnIdleTime != 10*time.Minute {
		t.Errorf("MaxConnIdleTime = %v, want 10m", cfg.MaxConnIdleTime)
	}
	if cfg.HealthCheckPeriod != 30*time.Second {
		t.Errorf("HealthCheckPeriod = %v, want 30s", cfg.HealthCheckPeriod)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/postgres/ -run 'TestOpenPool|TestPoolConfig' -v`
Expected: FAIL to compile — `undefined: OpenPool`, `undefined: poolConfig`.

- [ ] **Step 3: Write the implementation**

Create `internal/postgres/pool.go`:

```go
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// poolConfig is the single authoritative representation of this application's
// pgx pool tuning. Both NewPostgresWriter (the long-running daemon writer) and
// OpenPool (one-shot tools such as backfill) build from it, so a change to any
// value applies to both — which is exactly the shared-knowledge test that
// justifies extracting it rather than copying five lines.
func poolConfig(databaseURL string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 10 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	return config, nil
}

// OpenPool opens and verifies a connection pool.
//
// It starts no goroutines and does NOT create the schema — unlike
// NewPostgresWriter, which additionally runs CreateSchema and launches four
// background batch workers. A one-shot tool needs the pool without any of
// that, and calls CreateSchema explicitly so schema creation stays an
// observable step of the caller rather than a side effect of opening a pool.
//
// The caller owns the returned pool and must Close it.
func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := poolConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
```

- [ ] **Step 4: Refactor `NewPostgresWriter` to call it**

In `internal/postgres/writer.go`, replace the block currently at `:145-167` (from `config, err := pgxpool.ParseConfig(databaseURL)` through the `Ping` error check) with:

```go
	pool, err := OpenPool(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
```

Leave everything from `// Auto-create schema` onward unchanged.

**No imports become unused.** `pgxpool` is still referenced by the `pool *pgxpool.Pool` struct field (`writer.go:98`), and `time` is used throughout. Do not go hunting for imports to delete — if the compiler reports one unused, something else went wrong; re-read the edit.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/postgres/ -run 'TestOpenPool|TestPoolConfig' -v`
Expected: PASS — both tests.

Run the package's existing suite: `go test ./internal/postgres/ -v`
Expected: PASS, unchanged. (Integration tests requiring a live database skip when none is configured; that is pre-existing behavior.)

- [ ] **Step 6: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

- [ ] **Step 7: Commit**

```bash
git add internal/postgres/pool.go internal/postgres/pool_test.go internal/postgres/writer.go
git commit -m "refactor(postgres): extract OpenPool as the single source of pool tuning"
```

---

### Task 5: `internal/sqlite` — gap detection and idempotent insert

**Files:**
- Create: `internal/sqlite/backfill.go`
- Test: `internal/sqlite/backfill_test.go`

**Interfaces:**
- Consumes: `weather.Observation`, `weather.Gap`, `weather.Bounds` (Task 1).
- Produces (all package-level, all taking `*sql.DB`, none starting goroutines):
  - `func DistinctSerials(ctx context.Context, db *sql.DB) ([]string, error)` — **unwindowed**
  - `func SeriesBounds(ctx context.Context, db *sql.DB, from, to time.Time) ([]weather.Bounds, error)` — windowed
  - `func FindObservationGaps(ctx context.Context, db *sql.DB, from, to time.Time, minGap time.Duration) ([]weather.Gap, error)`
  - `func InsertObservations(ctx context.Context, db *sql.DB, obs []weather.Observation) (int, error)`

**Why package-level functions, not `Writer` methods:** `Writer.run` is documented as "the only goroutine that ever touches db" (`writer.go:161,236`). Adding an insert method to that type would breach the single-writer invariant and make it callable from the daemon. Also, `WriteReport`'s enqueue path is non-blocking and **drops on saturation** (`writer.go:346-357`, channel cap 1000 at `:23`) — a single 1-day window is ~1440 rows, so reusing it would silently lose data.

**`PARTITION BY serial_number` is not optional.** Without it, two stations phase-offset by ~30s produce a merged sequence in which no interval ever exceeds `minGap`, so a multi-hour outage on one station is undetectable and the tool reports "no gaps" and exits 0.

**Do not stream.** `sqlite.Open` sets `db.SetMaxOpenConns(1)` (`db.go:73`). Both queries must fully materialize their slice and close `rows` before returning; a caller inserting while iterating would deadlock on the single connection with no error and no timeout.

- [ ] **Step 1: Write the failing test**

Create `internal/sqlite/backfill_test.go`:

```go
package sqlite

import (
	"database/sql"
	"testing"
	"time"

	"tempestwx-utilities/internal/weather"

	"github.com/google/uuid"
)

// seedObs inserts bare observation rows (serial + timestamp only; every other
// column is nullable) so gap tests can express a series compactly.
func seedObs(t *testing.T, db *sql.DB, serial string, epochs ...int64) {
	t.Helper()
	for _, e := range epochs {
		_, err := db.ExecContext(t.Context(),
			`INSERT INTO tempest_observations (id, serial_number, timestamp) VALUES (?, ?, ?)`,
			uuid.Must(uuid.NewV7()).String(), serial, e)
		if err != nil {
			t.Fatalf("seed %s@%d: %v", serial, e, err)
		}
	}
}

func ts(epoch int64) time.Time { return time.Unix(epoch, 0).UTC() }

// The regression test for the partitioning bug: two serials whose interleaved
// timestamps mask each other. ST-A has a one-hour hole from 1000 to 4600.
// ST-B reports throughout, offset by 30s. Merged and unpartitioned, no
// consecutive interval exceeds minGap and the hole is invisible.
func TestFindObservationGapsPartitionsBySerial(t *testing.T) {
	db := newTestDB(t)
	seedObs(t, db, "ST-A", 1000, 4600, 5200)
	for e := int64(1030); e <= 5230; e += 600 {
		seedObs(t, db, "ST-B", e)
	}

	gaps, err := FindObservationGaps(t.Context(), db, ts(0), ts(10000), 30*time.Minute)
	if err != nil {
		t.Fatalf("FindObservationGaps: %v", err)
	}
	if len(gaps) != 1 {
		t.Fatalf("got %d gaps, want 1: %+v", len(gaps), gaps)
	}
	if gaps[0].SerialNumber != "ST-A" {
		t.Errorf("SerialNumber = %q, want ST-A", gaps[0].SerialNumber)
	}
	if !gaps[0].From.Equal(ts(1000)) || !gaps[0].To.Equal(ts(4600)) {
		t.Errorf("gap = [%v, %v], want [%v, %v]", gaps[0].From, gaps[0].To, ts(1000), ts(4600))
	}
}

func TestFindObservationGapsIgnoresJitterBelowMinGap(t *testing.T) {
	db := newTestDB(t)
	seedObs(t, db, "ST-A", 1000, 1060, 1125, 1180) // ~1min apart
	gaps, err := FindObservationGaps(t.Context(), db, ts(0), ts(10000), 30*time.Minute)
	if err != nil {
		t.Fatalf("FindObservationGaps: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("got %d gaps, want 0: %+v", len(gaps), gaps)
	}
}

func TestSeriesBoundsPerSerial(t *testing.T) {
	db := newTestDB(t)
	seedObs(t, db, "ST-A", 1000, 2000, 3000)
	seedObs(t, db, "ST-B", 5000, 6000)

	bounds, err := SeriesBounds(t.Context(), db, ts(0), ts(10000))
	if err != nil {
		t.Fatalf("SeriesBounds: %v", err)
	}
	got := map[string]weather.Bounds{}
	for _, b := range bounds {
		got[b.SerialNumber] = b
	}
	if len(got) != 2 {
		t.Fatalf("got %d serials, want 2: %+v", len(got), bounds)
	}
	if !got["ST-A"].First.Equal(ts(1000)) || !got["ST-A"].Last.Equal(ts(3000)) {
		t.Errorf("ST-A bounds = [%v, %v], want [1000, 3000]", got["ST-A"].First, got["ST-A"].Last)
	}
	if !got["ST-B"].First.Equal(ts(5000)) || !got["ST-B"].Last.Equal(ts(6000)) {
		t.Errorf("ST-B bounds = [%v, %v], want [5000, 6000]", got["ST-B"].First, got["ST-B"].Last)
	}
}

// DistinctSerials must ignore the window entirely. This is the regression
// guard for the false-mismatch bug: ST-B's only rows sit outside any window a
// caller is likely to ask about, but it is still very much in the store.
func TestDistinctSerialsIsUnwindowed(t *testing.T) {
	db := newTestDB(t)
	seedObs(t, db, "ST-A", 1000, 2000)
	seedObs(t, db, "ST-B", 900000) // far outside a [0, 10000] window

	serials, err := DistinctSerials(t.Context(), db)
	if err != nil {
		t.Fatalf("DistinctSerials: %v", err)
	}
	if len(serials) != 2 || serials[0] != "ST-A" || serials[1] != "ST-B" {
		t.Errorf("got %v, want [ST-A ST-B] — the query must not be windowed", serials)
	}

	// Contrast: the windowed query legitimately does not see ST-B.
	bounds, err := SeriesBounds(t.Context(), db, ts(0), ts(10000))
	if err != nil {
		t.Fatalf("SeriesBounds: %v", err)
	}
	if len(bounds) != 1 {
		t.Errorf("SeriesBounds returned %d serials, want 1 — this is why the two queries cannot be merged", len(bounds))
	}
}

func TestDistinctSerialsEmptyTable(t *testing.T) {
	db := newTestDB(t)
	serials, err := DistinctSerials(t.Context(), db)
	if err != nil {
		t.Fatalf("DistinctSerials on empty table must not error: %v", err)
	}
	if len(serials) != 0 {
		t.Errorf("got %d serials, want 0", len(serials))
	}
}

func TestSeriesBoundsEmptyTable(t *testing.T) {
	db := newTestDB(t)
	bounds, err := SeriesBounds(t.Context(), db, ts(0), ts(10000))
	if err != nil {
		t.Fatalf("SeriesBounds on empty table must not error: %v", err)
	}
	if len(bounds) != 0 {
		t.Errorf("got %d bounds, want 0", len(bounds))
	}
}

func f(v float64) *float64 { return &v }

func TestInsertObservationsIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	obs := []weather.Observation{
		{SerialNumber: "ST-A", Timestamp: ts(1000), TempAir: f(20.5), Humidity: f(55), Pressure: f(1013)},
		{SerialNumber: "ST-A", Timestamp: ts(1060), TempAir: f(20.6), Humidity: f(56), Pressure: f(1013)},
	}

	n, err := InsertObservations(t.Context(), db, obs)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if n != 2 {
		t.Errorf("first insert = %d, want 2", n)
	}

	n, err = InsertObservations(t.Context(), db, obs)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if n != 0 {
		t.Errorf("second insert = %d, want 0 (ON CONFLICT DO NOTHING)", n)
	}

	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM tempest_observations`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("row count = %d, want 2", count)
	}
}

func TestInsertObservationsPreservesNull(t *testing.T) {
	db := newTestDB(t)
	// Pressure is nil — it must land as SQL NULL, not 0.0.
	obs := []weather.Observation{
		{SerialNumber: "ST-A", Timestamp: ts(1000), TempAir: f(20.5), Humidity: f(55)},
	}
	if _, err := InsertObservations(t.Context(), db, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var pressure sql.NullFloat64
	if err := db.QueryRowContext(t.Context(), `SELECT pressure FROM tempest_observations`).Scan(&pressure); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if pressure.Valid {
		t.Errorf("pressure = %v, want NULL", pressure.Float64)
	}
}

func TestInsertObservationsDerivesWetBulb(t *testing.T) {
	db := newTestDB(t)
	obs := []weather.Observation{
		{SerialNumber: "ST-A", Timestamp: ts(1000), TempAir: f(20.5), Humidity: f(55), Pressure: f(1013)},
	}
	if _, err := InsertObservations(t.Context(), db, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var wb sql.NullFloat64
	if err := db.QueryRowContext(t.Context(), `SELECT temp_wetbulb FROM tempest_observations`).Scan(&wb); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !wb.Valid {
		t.Fatal("temp_wetbulb is NULL; it must be derived at the store boundary")
	}
	if wb.Float64 <= 0 || wb.Float64 >= 20.5 {
		t.Errorf("temp_wetbulb = %v, want a plausible value below dry-bulb 20.5", wb.Float64)
	}
}

func TestInsertObservationsWetBulbNullWhenInputsMissing(t *testing.T) {
	db := newTestDB(t)
	// No humidity: wet bulb is not derivable and must stay NULL rather than
	// being computed from a zero value.
	obs := []weather.Observation{
		{SerialNumber: "ST-A", Timestamp: ts(1000), TempAir: f(20.5), Pressure: f(1013)},
	}
	if _, err := InsertObservations(t.Context(), db, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var wb sql.NullFloat64
	if err := db.QueryRowContext(t.Context(), `SELECT temp_wetbulb FROM tempest_observations`).Scan(&wb); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if wb.Valid {
		t.Errorf("temp_wetbulb = %v, want NULL when humidity is absent", wb.Float64)
	}
}

func TestInsertObservationsEmptyIsNoop(t *testing.T) {
	db := newTestDB(t)
	n, err := InsertObservations(t.Context(), db, nil)
	if err != nil {
		t.Fatalf("empty insert: %v", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sqlite/ -run 'TestFindObservationGaps|TestSeriesBounds|TestInsertObservations' -v`
Expected: FAIL to compile — `undefined: FindObservationGaps`, `undefined: SeriesBounds`, `undefined: InsertObservations`.

- [ ] **Step 3: Write the implementation**

Create `internal/sqlite/backfill.go`:

```go
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"tempestwx-utilities/internal/tempestudp"
	"tempestwx-utilities/internal/weather"

	"github.com/google/uuid"
)

// This file holds the backfill tool's read/write path. Everything here is a
// package-level function taking *sql.DB, never a method on Writer:
// Writer.run is documented as "the only goroutine that ever touches db"
// (writer.go:161,236), so an insert method on that type would breach the
// single-writer invariant and be callable from the daemon. Nothing here
// starts a goroutine.
//
// IMPORTANT: sqlite.Open sets db.SetMaxOpenConns(1) (db.go:73). Every query
// below fully materializes its result slice and closes its *sql.Rows before
// returning. A streaming iterator that yielded rows while the caller inserted
// would deadlock on the single connection with no error and no timeout. Do
// not refactor these into iterators.

// findObservationGapsSQL locates interior holes in each station's series.
//
// PARTITION BY serial_number is NOT optional. The series is identified by
// (serial_number, timestamp) — the same uniqueness contract idempotency
// relies on. Without partitioning, two stations phase-offset by ~30s produce
// a merged sequence in which no consecutive interval ever exceeds minGap, so
// a multi-hour outage on one station becomes undetectable and the tool
// reports "no gaps" and exits 0. The same failure hides a hardware swap
// (a new serial) behind an apparently continuous sequence.
const findObservationGapsSQL = `
	SELECT serial_number, prev, timestamp FROM (
	  SELECT serial_number,
	         LAG(timestamp) OVER (PARTITION BY serial_number ORDER BY timestamp) AS prev,
	         timestamp
	  FROM tempest_observations
	  WHERE timestamp BETWEEN ? AND ?
	) WHERE prev IS NOT NULL AND timestamp - prev > ?
	ORDER BY serial_number, prev
`

// FindObservationGaps returns the interior gaps in [from, to] wider than
// minGap, one per (serial, hole).
//
// LAG yields NULL for the first row of each partition, so this finds interior
// gaps ONLY. Head and tail gaps — and the empty-table case — are assembled by
// the caller from SeriesBounds; see internal/backfill.
func FindObservationGaps(ctx context.Context, db *sql.DB, from, to time.Time, minGap time.Duration) ([]weather.Gap, error) {
	rows, err := db.QueryContext(ctx, findObservationGapsSQL, from.Unix(), to.Unix(), int64(minGap.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("query observation gaps: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var gaps []weather.Gap
	for rows.Next() {
		var serial string
		var prev, next int64
		if err := rows.Scan(&serial, &prev, &next); err != nil {
			return nil, fmt.Errorf("scan observation gap: %w", err)
		}
		gaps = append(gaps, weather.Gap{
			SerialNumber: serial,
			From:         time.Unix(prev, 0).UTC(),
			To:           time.Unix(next, 0).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observation gaps: %w", err)
	}
	return gaps, nil
}

const seriesBoundsSQL = `
	SELECT serial_number, MIN(timestamp), MAX(timestamp)
	FROM tempest_observations
	WHERE timestamp BETWEEN ? AND ?
	GROUP BY serial_number
	ORDER BY serial_number
`

// SeriesBounds returns the first and last observation timestamp held for each
// serial within [from, to]. An empty result means the store holds nothing in
// that window — the first-run case, where the whole range is one gap.
func SeriesBounds(ctx context.Context, db *sql.DB, from, to time.Time) ([]weather.Bounds, error) {
	rows, err := db.QueryContext(ctx, seriesBoundsSQL, from.Unix(), to.Unix())
	if err != nil {
		return nil, fmt.Errorf("query series bounds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []weather.Bounds
	for rows.Next() {
		var serial string
		var first, last int64
		if err := rows.Scan(&serial, &first, &last); err != nil {
			return nil, fmt.Errorf("scan series bounds: %w", err)
		}
		out = append(out, weather.Bounds{
			SerialNumber: serial,
			First:        time.Unix(first, 0).UTC(),
			Last:         time.Unix(last, 0).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate series bounds: %w", err)
	}
	return out, nil
}

// DistinctSerials returns every serial the table has ever held.
//
// UNWINDOWED, deliberately. This is the pre-flight check's input, and it must
// NOT be replaced by SeriesBounds' key set: SeriesBounds is windowed, so a
// station that was simply quiet during the queried window would look absent
// from the store entirely and trip a false serial mismatch — breaking
// `backfill --from X --to Y`, which is the tool's main repair path.
func DistinctSerials(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT serial_number FROM tempest_observations ORDER BY serial_number`)
	if err != nil {
		return nil, fmt.Errorf("query distinct serials: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var serial string
		if err := rows.Scan(&serial); err != nil {
			return nil, fmt.Errorf("scan distinct serial: %w", err)
		}
		out = append(out, serial)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distinct serials: %w", err)
	}
	return out, nil
}

// InsertObservations writes obs idempotently and reports how many rows were
// actually new.
//
// The count is what makes the permanent-hole tradeoff visible: if the station
// was genuinely offline, the API has no data either, and inserted stays 0
// across runs. ON CONFLICT (serial_number, timestamp) DO NOTHING is backed by
// a real UNIQUE constraint (migrations/0001_init.sql:13), and per-row
// RowsAffected returns 0 for a skipped conflict and 1 for an insert.
//
// The count is returned only after a successful Commit — execBatch rolls the
// whole transaction back on any row error.
//
// The caller bounds len(obs) to keep the transaction short; see the design's
// "Concurrency with a running daemon".
//
// Binding is direct from weather.Observation rather than through the private
// observationRow used by the UDP path: that type's leading fields are
// non-pointer float64, so routing through it would coerce a JSON null to 0.0.
func InsertObservations(ctx context.Context, db *sql.DB, obs []weather.Observation) (int, error) {
	if len(obs) == 0 {
		return 0, nil
	}

	inserted := 0
	err := execBatch(ctx, db, insertObservationSQL, obs, func(stmt *sql.Stmt, o weather.Observation) error {
		res, err := stmt.ExecContext(ctx,
			uuid.Must(uuid.NewV7()).String(), o.SerialNumber, o.Timestamp.Unix(),
			o.WindLull, o.WindAvg, o.WindGust, o.WindDirection, o.WindSampleInterval,
			o.Pressure, o.TempAir, wetBulb(o), o.Humidity,
			o.Illuminance, o.UVIndex, o.Irradiance, o.RainRate, o.PrecipType,
			o.LightningDistance, o.LightningStrikeCount,
			o.Battery, o.ReportInterval)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		inserted += int(n)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

// wetBulb derives temp_wetbulb, which the REST API does not return.
//
// It uses the same tempestudp.WetBulbTemperatureC + math.IsNaN guard the UDP
// ingest path uses (writer.go:410,432-434), single-sourced, so backfilled and
// live rows are indistinguishable. Change the formula and both paths change
// together: shared knowledge, not shared shape.
//
// Any missing input yields NULL rather than a value computed from a zero —
// the same reason the decode preserves nulls in the first place.
func wetBulb(o weather.Observation) *float64 {
	if o.TempAir == nil || o.Humidity == nil || o.Pressure == nil {
		return nil
	}
	v := tempestudp.WetBulbTemperatureC(*o.TempAir, *o.Humidity, *o.Pressure)
	if math.IsNaN(v) {
		return nil
	}
	return &v
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sqlite/ -run 'TestFindObservationGaps|TestSeriesBounds|TestInsertObservations' -v`
Expected: PASS — all nine tests. In particular `TestFindObservationGapsPartitionsBySerial` must find exactly one gap on `ST-A`.

- [ ] **Step 5: Sanity-check the partitioning claim by deleting it**

A test that guards an invariant it cannot actually detect is worse than no test. Prove this one bites — but do it so the mutation cannot survive by accident:

```bash
git add -A && git commit -m "wip: pre-mutation checkpoint"      # checkpoint first
# now edit findObservationGapsSQL: delete " PARTITION BY serial_number"
go test ./internal/sqlite/ -run TestFindObservationGapsPartitionsBySerial -v
```
Expected: **FAIL** with "got 0 gaps, want 1".

Then restore and **prove** the restore was exact:

```bash
git checkout -- internal/sqlite/backfill.go
git diff --exit-code                                            # must print nothing, exit 0
go test ./internal/sqlite/ -run TestFindObservationGapsPartitionsBySerial -v
```
Expected: `git diff --exit-code` clean, test PASS. Do not proceed while `git diff` shows anything — a half-restored mutation in production SQL is exactly the failure this step exists to prevent.

- [ ] **Step 6: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

- [ ] **Step 7: Commit**

```bash
git add internal/sqlite/backfill.go internal/sqlite/backfill_test.go
git commit -m "feat(sqlite): add partitioned gap detection and idempotent backfill insert"
```

---

### Task 6: `internal/postgres` — gap detection and idempotent insert

**Files:**
- Create: `internal/postgres/backfill.go`
- Modify: `internal/postgres/writer_integration_test.go` — no change needed unless the build breaks; verify only.
- Test: `internal/postgres/backfill_test.go`

**Interfaces:**
- Consumes: `weather.*` (Task 1), `OpenPool` (Task 4).
- Produces (mirroring Task 5's signatures exactly, with `*pgxpool.Pool` in place of `*sql.DB`):
  - `func DistinctSerials(ctx context.Context, pool *pgxpool.Pool) ([]string, error)` — **unwindowed**
  - `func SeriesBounds(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) ([]weather.Bounds, error)` — windowed
  - `func FindObservationGaps(ctx context.Context, pool *pgxpool.Pool, from, to time.Time, minGap time.Duration) ([]weather.Gap, error)`
  - `func InsertObservations(ctx context.Context, pool *pgxpool.Pool, obs []weather.Observation) (int, error)`

**Signatures are pinned to `time.Time` on both stores** so one interface satisfies both without a shim. The epoch conversion stays inside the SQLite implementation, matching how that package already handles timestamps; Postgres passes `time.Time` straight through to `TIMESTAMPTZ`.

**This task's SQL is the least-covered code in the change, and that is a stated risk, not an oversight.** Both reviews flagged it. The unit tests here cover only wet-bulb derivation; every query is exercised solely by `TestBackfillPostgresIntegration`, which **skips** unless `POSTGRES_URL` is set.

**Therefore: run it against a real database at least once before this branch merges, and paste the output.** A run in which it skipped is not evidence. If no database is available, say so explicitly when reporting this task complete — do not report the Postgres path as verified.

```bash
docker run --rm -d --name twx-itest -e POSTGRES_PASSWORD=x -e POSTGRES_DB=weather -p 55432:5432 postgres:16
# wait for readiness, then:
POSTGRES_URL='postgres://postgres:x@localhost:55432/weather?sslmode=disable' \
  go test ./internal/postgres/ -run TestBackfillPostgresIntegration -v
docker rm -f twx-itest
```

- [ ] **Step 1: Write the failing test**

Create `internal/postgres/backfill_test.go`:

```go
package postgres

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"tempestwx-utilities/internal/weather"
)

func f(v float64) *float64 { return &v }

func TestBackfillWetBulbDerivation(t *testing.T) {
	tests := []struct {
		name    string
		obs     weather.Observation
		wantNil bool
	}{
		{
			name: "all inputs present",
			obs:  weather.Observation{TempAir: f(20.5), Humidity: f(55), Pressure: f(1013)},
		},
		{
			name:    "humidity missing",
			obs:     weather.Observation{TempAir: f(20.5), Pressure: f(1013)},
			wantNil: true,
		},
		{
			name:    "temp missing",
			obs:     weather.Observation{Humidity: f(55), Pressure: f(1013)},
			wantNil: true,
		},
		{
			name:    "pressure missing",
			obs:     weather.Observation{TempAir: f(20.5), Humidity: f(55)},
			wantNil: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wetBulb(tt.obs)
			if tt.wantNil && got != nil {
				t.Errorf("wetBulb = %v, want nil", *got)
			}
			if !tt.wantNil {
				if got == nil {
					t.Fatal("wetBulb = nil, want a value")
				}
				if *got <= 0 || *got >= 20.5 {
					t.Errorf("wetBulb = %v, want a plausible value below dry-bulb 20.5", *got)
				}
			}
		})
	}
}

// --- integration: requires a live Postgres ---
//
// Everything above is a pure unit test. The SQL below is the ONLY thing that
// exercises this file's actual queries, and the Postgres dialect differs from
// SQLite's in ways that can fail at runtime and nowhere else:
//
//   - EXTRACT(EPOCH FROM (ts - prev)) yields numeric in PG14+, compared here
//     against a float64 parameter.
//   - MIN/MAX over timestamptz scanned into time.Time.
//   - precip_type is INTEGER in the DDL (schema.go:55) while the bind is a
//     *float64 — pgx truncates silently rather than erroring.
//
// Follow the existing skip idiom in writer_integration_test.go. This must be
// RUN AT LEAST ONCE against a real database before the branch merges; a run
// where it skips is not evidence.
func TestBackfillPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		t.Skip("POSTGRES_URL not set; skipping Postgres integration test")
	}

	ctx := t.Context()
	pool, err := OpenPool(ctx, dsn)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := CreateSchema(ctx, pool); err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}

	// Isolate this run from anything already in the table.
	serialA := "ST-ITEST-A"
	serialB := "ST-ITEST-B"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM tempest_observations WHERE serial_number IN ($1, $2)`, serialA, serialB)
	})
	if _, err := pool.Exec(ctx,
		`DELETE FROM tempest_observations WHERE serial_number IN ($1, $2)`, serialA, serialB); err != nil {
		t.Fatalf("pre-clean: %v", err)
	}

	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) // far from real data

	// The same interleaved fixture the SQLite suite uses: ST-A has a one-hour
	// hole; ST-B reports throughout, offset, and would mask it if the query
	// were not partitioned.
	var seed []weather.Observation
	for _, off := range []time.Duration{0, time.Hour, 90 * time.Minute} {
		seed = append(seed, weather.Observation{SerialNumber: serialA, Timestamp: base.Add(off), TempAir: f(20)})
	}
	for off := 30 * time.Second; off <= 95*time.Minute; off += 10 * time.Minute {
		seed = append(seed, weather.Observation{SerialNumber: serialB, Timestamp: base.Add(off), TempAir: f(21)})
	}

	n, err := InsertObservations(ctx, pool, seed)
	if err != nil {
		t.Fatalf("InsertObservations: %v", err)
	}
	if n != len(seed) {
		t.Errorf("inserted %d rows, want %d", n, len(seed))
	}

	// Idempotency: the same batch again must insert nothing.
	again, err := InsertObservations(ctx, pool, seed)
	if err != nil {
		t.Fatalf("second InsertObservations: %v", err)
	}
	if again != 0 {
		t.Errorf("re-insert added %d rows, want 0", again)
	}

	from, to := base.Add(-time.Hour), base.Add(3*time.Hour)

	serials, err := DistinctSerials(ctx, pool)
	if err != nil {
		t.Fatalf("DistinctSerials: %v", err)
	}
	if !slices.Contains(serials, serialA) || !slices.Contains(serials, serialB) {
		t.Errorf("DistinctSerials = %v, want it to contain %s and %s", serials, serialA, serialB)
	}

	bounds, err := SeriesBounds(ctx, pool, from, to)
	if err != nil {
		t.Fatalf("SeriesBounds: %v", err)
	}
	var gotA bool
	for _, b := range bounds {
		if b.SerialNumber == serialA {
			gotA = true
			if !b.First.Equal(base) {
				t.Errorf("%s First = %v, want %v", serialA, b.First, base)
			}
		}
	}
	if !gotA {
		t.Errorf("SeriesBounds missing %s: %+v", serialA, bounds)
	}

	gaps, err := FindObservationGaps(ctx, pool, from, to, 30*time.Minute)
	if err != nil {
		t.Fatalf("FindObservationGaps: %v", err)
	}
	var found bool
	for _, g := range gaps {
		if g.SerialNumber == serialA && g.From.Equal(base) && g.To.Equal(base.Add(time.Hour)) {
			found = true
		}
		if g.SerialNumber == serialB {
			t.Errorf("unexpected gap for %s: %+v", serialB, g)
		}
	}
	if !found {
		t.Errorf("did not find the %s hole [%v, %v]; got %+v", serialA, base, base.Add(time.Hour), gaps)
	}
}
```

The `strings.Contains(findObservationGapsSQL, "PARTITION BY serial_number")` assertion that appeared in an earlier draft of this task is **deliberately absent**. It asserts that a string constant contains a substring the same file defines — it would pass against SQL with a broken `WHERE`, a wrong comparison operator, or a missing alias, and it cannot fail for the reason its name claims. The behavioral proof is the integration test above plus the SQLite suite's mutation check.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/postgres/ -run 'TestBackfillWetBulb|TestBackfillPostgresIntegration' -v`
Expected: FAIL to compile — `undefined: wetBulb`, `undefined: DistinctSerials`, `undefined: SeriesBounds`, `undefined: FindObservationGaps`, `undefined: InsertObservations`.

- [ ] **Step 3: Write the implementation**

Create `internal/postgres/backfill.go`:

```go
package postgres

import (
	"context"
	"fmt"
	"math"
	"time"

	"tempestwx-utilities/internal/tempestudp"
	"tempestwx-utilities/internal/weather"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This file mirrors internal/sqlite/backfill.go function-for-function. The
// two are deliberately NOT unified: SQLite stores timestamps as epoch INTEGER
// and Postgres as TIMESTAMPTZ, and the two drivers bind parameters
// differently. An abstraction over that difference would be the wrong
// abstraction. The signatures are identical so one consumer-side interface
// satisfies both.

// backfillBatchTimeout bounds one backfill SendBatch. The daemon's hardcoded
// 5s at insertObservations (writer.go:240) was sized for the 1-row live path;
// a backfill batch of up to 200 rows needs its own budget.
const backfillBatchTimeout = 30 * time.Second

// findObservationGapsSQL locates interior holes in each station's series.
//
// PARTITION BY serial_number is NOT optional — see the identical comment in
// internal/sqlite/backfill.go. Without it a multi-hour outage on one of two
// phase-offset stations is undetectable.
const findObservationGapsSQL = `
	SELECT serial_number, prev, ts FROM (
	  SELECT serial_number,
	         LAG(timestamp) OVER (PARTITION BY serial_number ORDER BY timestamp) AS prev,
	         timestamp AS ts
	  FROM tempest_observations
	  WHERE timestamp BETWEEN $1 AND $2
	) q WHERE prev IS NOT NULL AND EXTRACT(EPOCH FROM (ts - prev)) > $3
	ORDER BY serial_number, prev
`

// FindObservationGaps returns the interior gaps in [from, to] wider than
// minGap. LAG yields NULL for the first row of each partition, so this finds
// interior gaps ONLY; head/tail/empty are assembled by the caller from
// SeriesBounds.
func FindObservationGaps(ctx context.Context, pool *pgxpool.Pool, from, to time.Time, minGap time.Duration) ([]weather.Gap, error) {
	rows, err := pool.Query(ctx, findObservationGapsSQL, from, to, minGap.Seconds())
	if err != nil {
		return nil, fmt.Errorf("query observation gaps: %w", err)
	}
	defer rows.Close()

	var gaps []weather.Gap
	for rows.Next() {
		var serial string
		var prev, next time.Time
		if err := rows.Scan(&serial, &prev, &next); err != nil {
			return nil, fmt.Errorf("scan observation gap: %w", err)
		}
		gaps = append(gaps, weather.Gap{
			SerialNumber: serial,
			From:         prev.UTC(),
			To:           next.UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observation gaps: %w", err)
	}
	return gaps, nil
}

const seriesBoundsSQL = `
	SELECT serial_number, MIN(timestamp), MAX(timestamp)
	FROM tempest_observations
	WHERE timestamp BETWEEN $1 AND $2
	GROUP BY serial_number
	ORDER BY serial_number
`

// SeriesBounds returns the first and last observation timestamp held for each
// serial within [from, to]. An empty result means the store holds nothing in
// that window — the first-run case.
func SeriesBounds(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) ([]weather.Bounds, error) {
	rows, err := pool.Query(ctx, seriesBoundsSQL, from, to)
	if err != nil {
		return nil, fmt.Errorf("query series bounds: %w", err)
	}
	defer rows.Close()

	var out []weather.Bounds
	for rows.Next() {
		var serial string
		var first, last time.Time
		if err := rows.Scan(&serial, &first, &last); err != nil {
			return nil, fmt.Errorf("scan series bounds: %w", err)
		}
		out = append(out, weather.Bounds{
			SerialNumber: serial,
			First:        first.UTC(),
			Last:         last.UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate series bounds: %w", err)
	}
	return out, nil
}

// DistinctSerials returns every serial the table has ever held.
//
// UNWINDOWED, deliberately — see the identical comment in
// internal/sqlite/backfill.go. Merging this into SeriesBounds causes a false
// serial mismatch for any station that was quiet during the queried window.
func DistinctSerials(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT serial_number FROM tempest_observations ORDER BY serial_number`)
	if err != nil {
		return nil, fmt.Errorf("query distinct serials: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var serial string
		if err := rows.Scan(&serial); err != nil {
			return nil, fmt.Errorf("scan distinct serial: %w", err)
		}
		out = append(out, serial)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distinct serials: %w", err)
	}
	return out, nil
}

const backfillInsertSQL = `
	INSERT INTO tempest_observations (
		id, serial_number, timestamp,
		wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
		pressure, temp_air, temp_wetbulb, humidity,
		illuminance, uv_index, irradiance, rain_rate, precip_type,
		lightning_distance, lightning_strike_count,
		battery, report_interval
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
	ON CONFLICT (serial_number, timestamp) DO NOTHING
`

// InsertObservations writes obs idempotently and reports how many rows were
// actually new. CommandTag.RowsAffected() is 0 for a skipped conflict and 1
// for an insert — the same semantics as SQLite's per-row RowsAffected, and
// the value the daemon path currently discards (writer.go:268).
//
// pgx SendBatch is all-or-nothing per batch, so the count is only meaningful
// once every Exec has succeeded. The caller bounds len(obs).
func InsertObservations(ctx context.Context, pool *pgxpool.Pool, obs []weather.Observation) (int, error) {
	if len(obs) == 0 {
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(ctx, backfillBatchTimeout)
	defer cancel()

	b := &pgx.Batch{}
	for _, o := range obs {
		b.Queue(backfillInsertSQL,
			uuid.Must(uuid.NewV7()), o.SerialNumber, o.Timestamp,
			o.WindLull, o.WindAvg, o.WindGust, o.WindDirection, o.WindSampleInterval,
			o.Pressure, o.TempAir, wetBulb(o), o.Humidity,
			o.Illuminance, o.UVIndex, o.Irradiance, o.RainRate, o.PrecipType,
			o.LightningDistance, o.LightningStrikeCount,
			o.Battery, o.ReportInterval)
	}

	br := pool.SendBatch(ctx, b)
	defer closeBatchResults(br)

	inserted := 0
	for i := range obs {
		tag, err := br.Exec()
		if err != nil {
			return 0, fmt.Errorf("insert observation %d: %w", i, err)
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// wetBulb derives temp_wetbulb, which the REST API does not return. It uses
// the same tempestudp.WetBulbTemperatureC + math.IsNaN guard the UDP ingest
// path uses, single-sourced, so backfilled and live rows are
// indistinguishable. Any missing input yields NULL rather than a value
// computed from a zero.
func wetBulb(o weather.Observation) *float64 {
	if o.TempAir == nil || o.Humidity == nil || o.Pressure == nil {
		return nil
	}
	v := tempestudp.WetBulbTemperatureC(*o.TempAir, *o.Humidity, *o.Pressure)
	if math.IsNaN(v) {
		return nil
	}
	return &v
}
```

> **Implementer note on the duplicated `wetBulb`.** It appears in both `internal/sqlite/backfill.go` and `internal/postgres/backfill.go`, four lines each. Keep it duplicated — but understand the *real* reason, because an earlier draft of this plan justified it with a false claim (that a shared home would force `internal/weather` to import `internal/tempestudp` and create a cycle). That is **not true**: `internal/tempestudp` imports only stdlib and `internal/tempest`, so `weather → tempestudp` is a perfectly legal acyclic edge. The honest justification is narrower: the duplicated part is a four-line nil guard, the *formula* — the actual shared knowledge — already lives in exactly one place (`tempestudp.WetBulbTemperatureC`), and four lines is cheaper than a new cross-package dependency. Note the counter-argument is real: the rule "wet bulb is NULL unless temp, humidity and pressure are all present" *is* shared knowledge under the DRY test, and if it ever changes both copies must change together. Leave a comment in each saying so.
>
> Also: `closeBatchResults` is already defined (unexported) at `internal/postgres/writer.go:223`. Reuse it; do not redefine it.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/postgres/ -run 'TestBackfillWetBulb|TestBackfillPostgresIntegration' -v`
Expected: the wet-bulb subtests PASS; the integration test SKIPs without `POSTGRES_URL`. Then run it for real per the note at the top of this task and paste that output too.

Run the package suite: `go test ./internal/postgres/ -v`
Expected: PASS; integration tests skip without a database, as before.

- [ ] **Step 5: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

- [ ] **Step 6: Commit**

```bash
git add internal/postgres/backfill.go internal/postgres/backfill_test.go
git commit -m "feat(postgres): add partitioned gap detection and idempotent backfill insert"
```

---

### Task 7: `internal/backfill` — chunking and retry classification

**Files:**
- Create: `internal/backfill/window.go`
- Test: `internal/backfill/window_test.go`

**Interfaces:**
- Consumes: `tempestapi.StatusError` (Task 2).
- Produces:
  - `type window struct{ from, to time.Time }` (unexported)
  - `func chunkWindow(from, to time.Time, size time.Duration) []window`
  - `func isRetryable(err error) bool`

**Chunking constraint:** the API documents that *observation data at one-minute resolution is available only for ranges of five days or less*. Chunk size is **1 day**, comfortably inside it. Exceeding the cap silently returns coarser data that would be written as if it were 1-minute observations — a data-corruption failure with no error.

**Retry classification — read the design's "per-attempt-timeout trap" before writing this.** `context.Canceled` is the only blanket non-retryable signal. A per-attempt HTTP timeout satisfies **both** `errors.Is(err, context.DeadlineExceeded)` and `errors.As(err, &netErr)`, because `http.Client.Timeout` is implemented as a context deadline — so a `DeadlineExceeded` guard would silently make every slow response permanent. Whether the *parent* context is done is answered at the call site with `ctx.Err()`, not by inspecting the error.

- [ ] **Step 1: Write the failing test**

Create `internal/backfill/window_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/backfill/ -v`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the implementation**

Create `internal/backfill/window.go`:

```go
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
// or inverted range yields no windows.
func chunkWindow(from, to time.Time, size time.Duration) []window {
	if !to.After(from) {
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/backfill/ -v`
Expected: PASS — all chunking and classification cases.

- [ ] **Step 5: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

- [ ] **Step 6: Commit**

```bash
git add internal/backfill/
git commit -m "feat(backfill): add API window chunking and retry classification"
```

---

### Task 8: `internal/backfill` — detection domain assembly

**Files:**
- Create: `internal/backfill/gaps.go`
- Test: `internal/backfill/gaps_test.go`

**Interfaces:**
- Consumes: `weather.Gap`, `weather.Bounds` (Task 1).
- Produces: `func assembleGaps(interior []weather.Gap, bounds []weather.Bounds, serials []string, detectFrom, detectTo time.Time, minGap time.Duration) []weather.Gap`

**Why this exists:** SQL `LAG` yields NULL for the first row of each partition, so gap detection finds **interior gaps only**. Left there, a fresh/empty store reports "no gaps" and writes nothing, and the natural "the box was down, repair it" case — whose outage is entirely in the tail — is invisible. The detection domain is the union of `[detectFrom, first_row]`, the interior gaps, and `[last_row, detectTo]`, with the **empty table** treated as one gap covering the whole range. That last case is first-class, not an edge case.

- [ ] **Step 1: Write the failing test**

Create `internal/backfill/gaps_test.go`:

```go
package backfill

import (
	"testing"
	"time"

	"tempestwx-utilities/internal/weather"
)

func ts(epoch int64) time.Time { return time.Unix(epoch, 0).UTC() }

func gapSet(gaps []weather.Gap) map[string][][2]int64 {
	out := map[string][][2]int64{}
	for _, g := range gaps {
		out[g.SerialNumber] = append(out[g.SerialNumber], [2]int64{g.From.Unix(), g.To.Unix()})
	}
	return out
}

func TestAssembleGapsEmptyStoreIsOneWholeRangeGap(t *testing.T) {
	got := assembleGaps(nil, nil, []string{"ST-A"}, ts(1000), ts(90000), 30*time.Minute)
	if len(got) != 1 {
		t.Fatalf("got %d gaps, want 1: %+v", len(got), got)
	}
	if got[0].SerialNumber != "ST-A" || !got[0].From.Equal(ts(1000)) || !got[0].To.Equal(ts(90000)) {
		t.Errorf("gap = %+v, want ST-A [1000, 90000]", got[0])
	}
}

func TestAssembleGapsHeadAndTail(t *testing.T) {
	bounds := []weather.Bounds{{SerialNumber: "ST-A", First: ts(50000), Last: ts(60000)}}
	got := assembleGaps(nil, bounds, []string{"ST-A"}, ts(1000), ts(90000), 30*time.Minute)

	set := gapSet(got)["ST-A"]
	if len(set) != 2 {
		t.Fatalf("got %d gaps, want 2 (head + tail): %+v", len(set), got)
	}
	if set[0] != [2]int64{1000, 50000} {
		t.Errorf("head gap = %v, want [1000, 50000]", set[0])
	}
	if set[1] != [2]int64{60000, 90000} {
		t.Errorf("tail gap = %v, want [60000, 90000]", set[1])
	}
}

func TestAssembleGapsSkipsHeadAndTailBelowMinGap(t *testing.T) {
	// First row is 60s after detectFrom and last row is 60s before detectTo:
	// both edges are narrower than minGap and must not be reported.
	bounds := []weather.Bounds{{SerialNumber: "ST-A", First: ts(1060), Last: ts(89940)}}
	got := assembleGaps(nil, bounds, []string{"ST-A"}, ts(1000), ts(90000), 30*time.Minute)
	if len(got) != 0 {
		t.Errorf("got %d gaps, want 0: %+v", len(got), got)
	}
}

func TestAssembleGapsPreservesInterior(t *testing.T) {
	interior := []weather.Gap{{SerialNumber: "ST-A", From: ts(20000), To: ts(30000)}}
	bounds := []weather.Bounds{{SerialNumber: "ST-A", First: ts(1000), Last: ts(90000)}}
	got := assembleGaps(interior, bounds, []string{"ST-A"}, ts(1000), ts(90000), 30*time.Minute)

	if len(got) != 1 {
		t.Fatalf("got %d gaps, want 1 (interior only, no head/tail): %+v", len(got), got)
	}
	if !got[0].From.Equal(ts(20000)) || !got[0].To.Equal(ts(30000)) {
		t.Errorf("gap = %+v, want [20000, 30000]", got[0])
	}
}

func TestAssembleGapsPerSerialIndependence(t *testing.T) {
	// ST-A has data; ST-B is a brand-new serial with nothing stored. ST-B's
	// whole range must be a gap even though ST-A looks healthy.
	bounds := []weather.Bounds{{SerialNumber: "ST-A", First: ts(1000), Last: ts(90000)}}
	got := assembleGaps(nil, bounds, []string{"ST-A", "ST-B"}, ts(1000), ts(90000), 30*time.Minute)

	set := gapSet(got)
	if len(set["ST-A"]) != 0 {
		t.Errorf("ST-A got %v, want no gaps", set["ST-A"])
	}
	if len(set["ST-B"]) != 1 || set["ST-B"][0] != [2]int64{1000, 90000} {
		t.Errorf("ST-B got %v, want one whole-range gap", set["ST-B"])
	}
}

func TestAssembleGapsIgnoresBoundsForUnknownSerial(t *testing.T) {
	// A serial present in the store but not returned by the API (a retired
	// station) must not produce work — there is nothing to fetch for it.
	bounds := []weather.Bounds{{SerialNumber: "ST-OLD", First: ts(1000), Last: ts(2000)}}
	got := assembleGaps(nil, bounds, []string{"ST-A"}, ts(1000), ts(90000), 30*time.Minute)

	for _, g := range got {
		if g.SerialNumber == "ST-OLD" {
			t.Errorf("produced a gap for a serial the API does not know: %+v", g)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/backfill/ -run TestAssembleGaps -v`
Expected: FAIL to compile — `undefined: assembleGaps`.

- [ ] **Step 3: Write the implementation**

Create `internal/backfill/gaps.go`:

```go
package backfill

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"tempestwx-utilities/internal/weather"
)

// assembleGaps builds the full detection domain from what SQL can and cannot
// see.
//
// FindObservationGaps uses a LAG window function, which yields NULL for the
// first row of each partition — so it finds INTERIOR gaps only. Left at that,
// a fresh store reports "no gaps" and writes nothing, and the natural "the
// box was down, repair it" case — whose outage is entirely in the tail — is
// invisible. The domain is therefore the union of:
//
//   - the head gap [detectFrom, first stored row]
//   - the interior gaps SQL found
//   - the tail gap [last stored row, detectTo]
//
// with the EMPTY-store case (no bounds for a serial) treated as one gap
// covering the whole range. That is a first-class case, not an edge case: it
// is what every first run looks like.
//
// serials is the set the API knows about. A serial present in the store but
// absent from the API (a retired station) produces no work — there is nothing
// to fetch for it. Edges narrower than minGap are ordinary reporting jitter
// and are dropped, matching the interior threshold.
func assembleGaps(
	interior []weather.Gap,
	bounds []weather.Bounds,
	serials []string,
	detectFrom, detectTo time.Time,
	minGap time.Duration,
) []weather.Gap {
	byserial := make(map[string]weather.Bounds, len(bounds))
	for _, b := range bounds {
		byserial[b.SerialNumber] = b
	}

	var out []weather.Gap
	for _, serial := range serials {
		b, ok := byserial[serial]
		if !ok {
			// Nothing stored for this serial in the detection window: the
			// whole range is one gap.
			if detectTo.Sub(detectFrom) > minGap {
				out = append(out, weather.Gap{SerialNumber: serial, From: detectFrom, To: detectTo})
			}
			continue
		}
		if b.First.Sub(detectFrom) > minGap {
			out = append(out, weather.Gap{SerialNumber: serial, From: detectFrom, To: b.First})
		}
		for _, g := range interior {
			if g.SerialNumber == serial {
				out = append(out, g)
			}
		}
		if detectTo.Sub(b.Last) > minGap {
			out = append(out, weather.Gap{SerialNumber: serial, From: b.Last, To: detectTo})
		}
	}

	// slices.SortFunc, not sort.Slice: it is the Go 1.25 idiom, and sort.Slice
	// is unstable while (serial, From) is not guaranteed unique.
	slices.SortFunc(out, func(a, b weather.Gap) int {
		return cmp.Or(
			strings.Compare(a.SerialNumber, b.SerialNumber),
			a.From.Compare(b.From),
		)
	})
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/backfill/ -run TestAssembleGaps -v`
Expected: PASS — all six tests.

- [ ] **Step 5: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

- [ ] **Step 6: Commit**

```bash
git add internal/backfill/gaps.go internal/backfill/gaps_test.go
git commit -m "feat(backfill): assemble head, tail, and empty-store gaps around LAG's interior gaps"
```

---

### Task 9: `internal/backfill` — the `Run` core

**Files:**
- Create: `internal/backfill/backfill.go`
- Test: `internal/backfill/backfill_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1, 2, 7, 8.
- Produces:
  ```go
  type Config struct {
      From, To time.Time     // zero => auto-detect
      MinGap   time.Duration
      DryRun   bool
  }
  type Stats struct{ Gaps, Returned, Inserted, Failed int }
  type ObservationSource interface {
      Observations(ctx context.Context, station tempestapi.Station, start, end time.Time) ([]weather.Observation, error)
  }
  type Store interface {
      SeriesBounds(ctx context.Context, from, to time.Time) ([]weather.Bounds, error)
      FindObservationGaps(ctx context.Context, from, to time.Time, minGap time.Duration) ([]weather.Gap, error)
      InsertObservations(ctx context.Context, obs []weather.Observation) (int, error)
  }
  func Run(ctx context.Context, cfg Config, src ObservationSource, store Store, stations []tempestapi.Station, now time.Time) (Stats, error)
  ```

**Why `now` is a parameter:** nothing below the shell calls `time.Now()`. `detectTo` defaults to `now - minGap`, which is only testable if the clock is injected.

**Why `Store` is an interface:** two concrete implementors exist on day one (SQLite and Postgres), so it is not speculative. It is defined in the consumer, Go-idiomatic; thin adapters live in the shell.

**Serial pre-flight is a hard stop.** If an API serial is absent from a **non-empty** store, backfill would write a parallel series under a second serial: `UNIQUE` never fires, rows double, and the gap never closes — silently and cumulatively. Warning-then-writing-anyway is incoherent; it names an outcome as corrupting and then produces it. An **empty** store is not a mismatch — it is the first-run case.

**Insert batches are bounded to 200 rows.** A long transaction contends with live ingest, whose error path only *logs* (`sqlite/writer.go:646`), so an unbounded backfill transaction could cause **live observations to be silently lost while repairing historical ones**.

- [ ] **Step 1: Write the failing test**

Create `internal/backfill/backfill_test.go`:

```go
package backfill

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"tempestwx-utilities/internal/tempestapi"
	"tempestwx-utilities/internal/weather"
)

// --- fakes ---

type fakeSource struct {
	calls   []window
	obs     []weather.Observation
	errs    []error // consumed one per call; nil entries succeed
	callNum int
}

func (f *fakeSource) Observations(_ context.Context, _ tempestapi.Station, start, end time.Time) ([]weather.Observation, error) {
	f.calls = append(f.calls, window{from: start, to: end})
	i := f.callNum
	f.callNum++
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.obs, nil
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
	return len(obs), nil
}

func station(serial string) tempestapi.Station {
	return tempestapi.Station{
		SerialNumber: serial,
		DeviceID:     1,
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

// Regression for the windowed-bounds bug: the pre-flight check must read
// DistinctSerials (whole table), not SeriesBounds (windowed). A station that
// simply had no rows inside the requested window is not missing from the
// store, and `backfill --from X --to Y` is the tool's main repair path.
func TestRunQuietSerialInRequestedWindowIsNotAMismatch(t *testing.T) {
	src := &fakeSource{obs: []weather.Observation{obsAt("ST-B", 5000)}}
	store := &fakeStore{
		// Both serials exist in the store...
		serials: []string{"ST-A", "ST-B"},
		// ...but only ST-A has rows inside the queried window.
		bounds: []weather.Bounds{{SerialNumber: "ST-A", First: at(1000), Last: at(2000)}},
	}

	from := at(0)
	_, err := Run(t.Context(),
		Config{From: from, To: from.Add(time.Hour), MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A"), station("ST-B")}, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("a serial with no rows in the requested window must not be a mismatch: %v", err)
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
	if stats.Inserted != 2 {
		t.Errorf("Inserted = %d, want 2 (windows 1 and 3 both landed)", stats.Inserted)
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
	_, err := Run(t.Context(),
		Config{From: from, To: from.Add(time.Hour), MinGap: 30 * time.Minute},
		src, store, []tempestapi.Station{station("ST-A")}, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("Run: %v", err)
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
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/backfill/ -run TestRun -v`
Expected: FAIL to compile — `undefined: Run`, `undefined: Config`, `undefined: ErrSerialMismatch`.

- [ ] **Step 3: Write the implementation**

Create `internal/backfill/backfill.go`:

```go
package backfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"tempestwx-utilities/internal/tempestapi"
	"tempestwx-utilities/internal/weather"
)

const (
	// insertBatchSize bounds one insert transaction.
	//
	// The realistic invocation is `docker exec <running container>
	// tempestwx-utilities backfill` against a LIVE database. A long backfill
	// transaction contends with ingest writes; busy_timeout defaults to 5s
	// and ingest's error path only LOGS (sqlite/writer.go:646), so an
	// unbounded transaction could cause live observations to be silently lost
	// while repairing historical ones. Short transactions are the mitigation.
	insertBatchSize = 200

	// maxAttempts bounds retries per window, including the first try.
	maxAttempts = 4
	// baseBackoff is doubled per attempt: 1s, 2s, 4s.
	baseBackoff = time.Second
)

// ErrSerialMismatch is returned when the API reports a serial the non-empty
// store has never seen. See preflight.
var ErrSerialMismatch = errors.New("station serial not present in store")

// Config is the backfill run's parameters. It holds no I/O handles and no
// clock — both are passed to Run explicitly so the core is testable.
type Config struct {
	// From and To bound the work explicitly. Both zero means auto-detect.
	//
	// The override exists to BOUND API WORK, not to avoid a table scan
	// (idx_obs_serial_time already covers detection): because permanent holes
	// are accepted and never recorded, auto-detect re-requests every
	// known-empty window on every run. An operator repairing a known outage
	// can name it and skip that cost.
	From time.Time
	To   time.Time

	// MinGap is the smallest interval that counts as a hole, keeping ordinary
	// reporting jitter from registering.
	MinGap time.Duration

	// DryRun detects and plans only: Run makes zero observation fetches and
	// zero writes.
	//
	// Note the shell still calls ListDevices before invoking Run, so the
	// COMMAND does make one API call in dry-run and therefore does validate
	// the token. Do not document it as "zero API calls".
	DryRun bool
}

// Stats is what the run did, in aggregate.
type Stats struct {
	Gaps int // holes detected
	// Returned counts observations the API actually handed back, AFTER
	// malformed tuples were dropped. It is not "rows requested" — the closed
	// gap interval and the shared chunk-window endpoints both mean a few
	// observations are fetched more than once.
	Returned int
	Inserted int // rows actually new (0 across runs => a permanent hole)
	Failed   int // gaps that failed after retries
}

// NOTE: dropped-tuple counts are deliberately NOT here. They are logged at
// WARN by the decode itself (Task 3), which is where the information exists.
// Threading a diagnostic counter up through ObservationSource would widen the
// seam between backfill and the REST client to carry a reporting nicety, and
// the log stream is already the machine-readable surface this design chose
// when it cut the bespoke summary line.

// ObservationSource is the REST client, narrowed to what backfill needs.
// *tempestapi.Client satisfies it.
type ObservationSource interface {
	Observations(ctx context.Context, station tempestapi.Station, start, end time.Time) ([]weather.Observation, error)
}

// Store is the persistence side, narrowed to what backfill needs. Two
// concrete implementors exist on day one — SQLite and Postgres — so this is
// an earned interface, not a speculative one. It is declared here, in the
// consumer, per Go convention; the adapters that bind a *sql.DB or
// *pgxpool.Pool to it live in the command shell.
type Store interface {
	// DistinctSerials is UNWINDOWED — the whole table. It exists only for the
	// pre-flight check and must NOT be replaced by SeriesBounds' key set:
	// SeriesBounds is windowed, so a station that was simply quiet during the
	// requested window would look absent from the store and trip the check.
	DistinctSerials(ctx context.Context) ([]string, error)
	SeriesBounds(ctx context.Context, from, to time.Time) ([]weather.Bounds, error)
	FindObservationGaps(ctx context.Context, from, to time.Time, minGap time.Duration) ([]weather.Gap, error)
	InsertObservations(ctx context.Context, obs []weather.Observation) (int, error)
}

// Run detects gaps and fills them.
//
// now is injected: nothing below the command shell calls time.Now(), so
// detectTo (now - MinGap) is deterministic under test.
//
// A failed gap logs and continues — partial progress must be preserved — and
// the per-gap errors are joined into the returned error, which the shell
// turns into a non-zero exit. Run never calls log.Fatal.
func Run(
	ctx context.Context,
	cfg Config,
	src ObservationSource,
	store Store,
	stations []tempestapi.Station,
	now time.Time,
) (Stats, error) {
	var stats Stats

	detectFrom, detectTo := detectionRange(cfg, stations, now)

	storedSerials, err := store.DistinctSerials(ctx)
	if err != nil {
		return stats, fmt.Errorf("distinct serials: %w", err)
	}
	if err := preflight(stations, storedSerials); err != nil {
		return stats, err
	}

	bounds, err := store.SeriesBounds(ctx, detectFrom, detectTo)
	if err != nil {
		return stats, fmt.Errorf("series bounds: %w", err)
	}

	gaps, err := plannedGaps(ctx, cfg, store, stations, bounds, detectFrom, detectTo)
	if err != nil {
		return stats, err
	}
	stats.Gaps = len(gaps)

	if cfg.DryRun {
		for _, g := range gaps {
			slog.Info("backfill: planned gap (dry run)",
				"serial", g.SerialNumber, "from", g.From, "to", g.To, "duration", g.Duration())
		}
		return stats, nil
	}

	byserial := make(map[string]tempestapi.Station, len(stations))
	for _, s := range stations {
		byserial[s.SerialNumber] = s
	}

	var failures []error
	for _, g := range gaps {
		returned, inserted, err := fillGap(ctx, src, store, byserial[g.SerialNumber], g)
		stats.Returned += returned
		stats.Inserted += inserted

		if err != nil {
			if ctx.Err() != nil {
				// Cancellation is not a gap failure. Inserted rows stay
				// intact; idempotency makes re-running safe.
				return stats, ctx.Err()
			}
			stats.Failed++
			failures = append(failures, fmt.Errorf("gap %s [%s, %s]: %w",
				g.SerialNumber, g.From.Format(time.RFC3339), g.To.Format(time.RFC3339), err))
			slog.Error("backfill: gap failed",
				"serial", g.SerialNumber, "from", g.From, "to", g.To, "error", err)
			continue
		}

		// returned vs inserted is what makes the permanent-hole tradeoff
		// visible: if the station was genuinely offline, the API has no data
		// either and inserted stays 0 across runs. Structured attrs are the
		// machine-readable surface — no bespoke summary format.
		slog.Info("backfill: gap filled",
			"serial", g.SerialNumber, "from", g.From, "to", g.To,
			"returned", returned, "inserted", inserted)
	}

	if len(failures) > 0 {
		return stats, errors.Join(failures...)
	}
	return stats, nil
}

// detectionRange resolves the window to work over. An explicit --from/--to
// wins; otherwise detection runs from the earliest station creation time to
// now-MinGap (trailing MinGap so the most recent, still-arriving interval is
// not mistaken for a hole).
func detectionRange(cfg Config, stations []tempestapi.Station, now time.Time) (time.Time, time.Time) {
	if !cfg.From.IsZero() && !cfg.To.IsZero() {
		return cfg.From, cfg.To
	}
	detectTo := now.Add(-cfg.MinGap)
	detectFrom := detectTo
	for _, s := range stations {
		if s.CreatedAt.Before(detectFrom) {
			detectFrom = s.CreatedAt
		}
	}
	return detectFrom, detectTo
}

// plannedGaps is the whole detection domain: SQL's interior gaps plus the
// head, tail, and empty-store cases LAG cannot see. An explicit --from/--to
// still goes through the chunker, so it is expressed as one gap per station
// rather than bypassing the pipeline.
func plannedGaps(
	ctx context.Context,
	cfg Config,
	store Store,
	stations []tempestapi.Station,
	bounds []weather.Bounds,
	detectFrom, detectTo time.Time,
) ([]weather.Gap, error) {
	serials := make([]string, 0, len(stations))
	for _, s := range stations {
		serials = append(serials, s.SerialNumber)
	}

	if !cfg.From.IsZero() && !cfg.To.IsZero() {
		gaps := make([]weather.Gap, 0, len(serials))
		for _, serial := range serials {
			gaps = append(gaps, weather.Gap{SerialNumber: serial, From: cfg.From, To: cfg.To})
		}
		return gaps, nil
	}

	interior, err := store.FindObservationGaps(ctx, detectFrom, detectTo, cfg.MinGap)
	if err != nil {
		return nil, fmt.Errorf("find observation gaps: %w", err)
	}
	return assembleGaps(interior, bounds, serials, detectFrom, detectTo, cfg.MinGap), nil
}

// preflight refuses to run when the API's serials and the store's serials are
// DISJOINT — no API serial appears in the store at all.
//
// Dedupe, gap closure, and convergence all require that the serial backfill
// writes exactly matches the serial UDP ingest writes. If the two formats
// diverge, backfill writes a PARALLEL SERIES under a second serial: UNIQUE
// never fires, rows double, and the gap never closes — silently and
// cumulatively. So this is a hard stop, not a warning: warning-then-writing-
// anyway names an outcome as corrupting and then produces it.
//
// DISJOINT is the rule, and the distinction is load-bearing. The tempting
// version — "some API serial is absent from a non-empty store" — fires on two
// completely ordinary situations and would brick the tool for both:
//
//   - A second station on the account whose broadcasts this host never hears
//     (different VLAN/subnet). Its serial will NEVER enter the store, so
//     backfill would refuse to run, including for the healthy station.
//   - A newly added station, whose first backfill would exit non-zero having
//     written nothing — permanently, until the daemon happened to ingest a row.
//
// Neither is format divergence. Under the disjoint rule both proceed, and a
// serial the API knows but the store has not seen simply becomes a whole-range
// gap, which is exactly assembleGaps' job.
//
// storedSerials MUST come from DistinctSerials (unwindowed), never from
// SeriesBounds' key set — see the Store interface comment.
//
// An EMPTY store is not a mismatch: it is the first-run case.
func preflight(stations []tempestapi.Station, storedSerials []string) error {
	if len(storedSerials) == 0 {
		return nil
	}
	stored := make(map[string]struct{}, len(storedSerials))
	for _, s := range storedSerials {
		stored[s] = struct{}{}
	}
	for _, st := range stations {
		if _, ok := stored[st.SerialNumber]; ok {
			return nil // at least one serial matches: not divergence
		}
	}

	apiSerials := make([]string, 0, len(stations))
	for _, st := range stations {
		apiSerials = append(apiSerials, st.SerialNumber)
	}
	return fmt.Errorf("%w: API reports %v, store holds %v — no overlap at all, "+
		"which means the two are using different serial formats; backfilling would "+
		"create a parallel series that never dedupes",
		ErrSerialMismatch, apiSerials, storedSerials)
}

// fillGap fetches and inserts one gap, chunked and retried.
//
// A failed WINDOW does not abandon the gap. Returning here on the first bad
// window would discard every remaining window, and for a DETERMINISTIC
// per-window failure that loss is permanent across runs, not transient:
// once the earlier windows land, the head gap collapses and the hole
// reappears as an interior gap starting at the same bad window, which fails
// again — so the windows behind it are never requested on ANY run. A tool
// whose entire premise is convergence would silently stop converging, and the
// inserted=0 signal never even fires because those windows are never
// requested. So: log, accumulate, keep going, and fail the gap at the end.
//
// An INSERT error is different and does abort the gap: it means the store is
// unhealthy, and hammering it with the remaining windows helps nobody.
func fillGap(
	ctx context.Context,
	src ObservationSource,
	store Store,
	station tempestapi.Station,
	g weather.Gap,
) (returned, inserted int, err error) {
	var windowErrs []error

	for _, w := range chunkWindow(g.From, g.To, chunkSize) {
		if err := ctx.Err(); err != nil {
			return returned, inserted, err
		}

		obs, err := fetchWithRetry(ctx, src, station, w)
		if err != nil {
			if ctx.Err() != nil {
				return returned, inserted, ctx.Err()
			}
			slog.Error("backfill: window failed, continuing with the rest of the gap",
				"serial", station.SerialNumber, "from", w.from, "to", w.to, "error", err)
			windowErrs = append(windowErrs, fmt.Errorf("window [%s, %s]: %w",
				w.from.Format(time.RFC3339), w.to.Format(time.RFC3339), err))
			continue
		}
		returned += len(obs)

		for chunk := range slices.Chunk(obs, insertBatchSize) {
			n, err := store.InsertObservations(ctx, chunk)
			if err != nil {
				return returned, inserted, fmt.Errorf("insert: %w", err)
			}
			inserted += n
		}
	}

	return returned, inserted, errors.Join(windowErrs...)
}

// fetchWithRetry applies bounded exponential backoff to transient failures.
// Context cancellation is checked between attempts as well as between
// windows.
func fetchWithRetry(
	ctx context.Context,
	src ObservationSource,
	station tempestapi.Station,
	w window,
) ([]weather.Observation, error) {
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			delay := baseBackoff << (attempt - 1)
			slog.Warn("backfill: retrying window",
				"serial", station.SerialNumber, "from", w.from, "to", w.to,
				"attempt", attempt+1, "delay", delay, "error", lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		obs, err := src.Observations(ctx, station, w.from, w.to)
		if err == nil {
			return obs, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/backfill/ -run TestRun -v`
Expected: PASS — all nine tests.

- [ ] **Step 5: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

- [ ] **Step 6: Commit**

```bash
git add internal/backfill/backfill.go internal/backfill/backfill_test.go
git commit -m "feat(backfill): add the Run core with injected clock, store, and API source"
```

---

### Task 10: `main.go` — the shell and real subcommand dispatch

**Files:**
- Create: `backfill_cmd.go` (package `main`, repo root — matches the existing flat layout)
- Modify: `main.go:188-191` (the dispatch in `main`)
- Test: `backfill_cmd_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `func runBackfill(ctx context.Context, args []string) int`, plus the two store adapters.

**Dispatch fix:** `main()` currently matches only `healthcheck` and **falls through to daemon mode** for anything else — so `tempestwx-utilities backfil` (typo) silently starts a UDP listener. Unknown non-flag first arguments must print usage and exit **2**.

**Each subcommand owns its own `flag.FlagSet` parsed from `os.Args[2:]`.** That — not an abstraction — is the seam #80's `migrate` slots into: a new subcommand is a new file plus one dispatch line, with no sibling touched.

**Standards divergence, recorded deliberately:** `go-standards` §6 says "all CLIs use Cobra". This project uses a hand-rolled `os.Args[1]` dispatch. We keep the established pattern — Cobra for two subcommands would be a dependency and a framework where a `switch` suffices, and the standard defers to established project patterns. Do not add Cobra.

**Cleanup:** `runBackfill` performs **all** cleanup via internal defers and returns an exit code. Copying the healthcheck shape (`os.Exit` at the dispatch site) would skip `db.Close()` and the pgx pool drain.

- [ ] **Step 1: Write the failing test**

Create `backfill_cmd_test.go`:

```go
package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseBackfillFlagsDefaults(t *testing.T) {
	cfg, _, err := parseBackfillFlags(nil)
	if err != nil {
		t.Fatalf("parseBackfillFlags: %v", err)
	}
	if cfg.MinGap != 30*time.Minute {
		t.Errorf("MinGap = %v, want 30m", cfg.MinGap)
	}
	if cfg.DryRun {
		t.Error("DryRun should default to false")
	}
	if !cfg.From.IsZero() || !cfg.To.IsZero() {
		t.Error("From/To should default to zero (auto-detect)")
	}
}

func TestParseBackfillFlagsRFC3339IsUTC(t *testing.T) {
	cfg, _, err := parseBackfillFlags([]string{"--from", "2026-01-02T03:04:05Z", "--to", "2026-01-03T03:04:05Z"})
	if err != nil {
		t.Fatalf("parseBackfillFlags: %v", err)
	}
	if cfg.From.Location() != time.UTC {
		t.Errorf("From location = %v, want UTC", cfg.From.Location())
	}
	if cfg.From.Format(time.RFC3339) != "2026-01-02T03:04:05Z" {
		t.Errorf("From = %v, want 2026-01-02T03:04:05Z", cfg.From.Format(time.RFC3339))
	}
}

func TestParseBackfillFlagsRejectsNonRFC3339(t *testing.T) {
	_, _, err := parseBackfillFlags([]string{"--from", "2026-01-02"})
	if err == nil {
		t.Fatal("a non-RFC3339 --from must be rejected; an ambiguous local-time parse is a quiet wrong-window bug")
	}
	if !strings.Contains(err.Error(), "from") {
		t.Errorf("error = %q, want it to name the offending flag", err)
	}
}

func TestParseBackfillFlagsRequiresBothOrNeither(t *testing.T) {
	if _, _, err := parseBackfillFlags([]string{"--from", "2026-01-02T00:00:00Z"}); err == nil {
		t.Error("--from without --to must be rejected")
	}
	if _, _, err := parseBackfillFlags([]string{"--to", "2026-01-02T00:00:00Z"}); err == nil {
		t.Error("--to without --from must be rejected")
	}
}

func TestParseBackfillFlagsRejectsInvertedRange(t *testing.T) {
	_, _, err := parseBackfillFlags([]string{"--from", "2026-01-03T00:00:00Z", "--to", "2026-01-02T00:00:00Z"})
	if err == nil {
		t.Fatal("--to before --from must be rejected")
	}
}

func TestParseBackfillFlagsDryRun(t *testing.T) {
	cfg, _, err := parseBackfillFlags([]string{"--dry-run", "--min-gap", "2h"})
	if err != nil {
		t.Fatalf("parseBackfillFlags: %v", err)
	}
	if !cfg.DryRun {
		t.Error("--dry-run not applied")
	}
	if cfg.MinGap != 2*time.Hour {
		t.Errorf("MinGap = %v, want 2h", cfg.MinGap)
	}
}

// TOKEN must be validated BEFORE any store handle is opened, so the failure
// costs no I/O and leaves nothing to close.
//
// The ordering is proven by pointing SQLITE_PATH at a path inside a writable
// temp dir and asserting the file was never created. An earlier version of
// this test used a bogus path and asserted only exit==1 — which passes
// identically whether TOKEN is checked before or after the store opens, since
// a failed open also returns 1. That test could not fail for its stated
// reason.
func TestRunBackfillWithoutTokenFailsBeforeOpeningTheStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "should-never-be-created.db")
	t.Setenv("TOKEN", "")
	t.Setenv("ENABLE_POSTGRES", "")
	t.Setenv("SQLITE_PATH", dbPath)

	if got := runBackfill(t.Context(), nil); got != 1 {
		t.Errorf("exit code = %d, want 1 for a missing TOKEN", got)
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("SQLite database at %s was created; TOKEN must be validated before the store is opened", dbPath)
	}
}

func TestParseBackfillFlagsStoreValidation(t *testing.T) {
	for _, v := range []string{"sqlite", "postgres", ""} {
		if _, got, err := parseBackfillFlags([]string{"--store", v}); err != nil || got != v {
			t.Errorf("--store=%q: got (%q, %v), want (%q, nil)", v, got, err, v)
		}
	}
	if _, _, err := parseBackfillFlags([]string{"--store", "mysql"}); err == nil {
		t.Error("--store=mysql must be rejected")
	}
}

func TestResolveStore(t *testing.T) {
	both := storeChoice{sqlite: true, postgres: true}
	onlySQLite := storeChoice{sqlite: true}
	onlyPG := storeChoice{postgres: true}
	none := storeChoice{}

	tests := []struct {
		name    string
		choice  storeChoice
		flag    string
		want    string
		wantErr bool
	}{
		{"single store needs no flag", onlySQLite, "", "sqlite", false},
		{"single store, matching flag", onlyPG, "postgres", "postgres", false},
		{"single store, contradicting flag", onlySQLite, "postgres", "", true},
		{"both configured, flag chooses", both, "postgres", "postgres", false},
		{"both configured, flag chooses sqlite", both, "sqlite", "sqlite", false},
		// The regression: never silently pick one.
		{"both configured, no flag", both, "", "", true},
		{"nothing configured", none, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveStore(tt.choice, tt.flag)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// --help is not a usage error.
func TestRunBackfillHelpExitsZero(t *testing.T) {
	t.Setenv("TOKEN", "x")
	if got := runBackfill(t.Context(), []string{"--help"}); got != 0 {
		t.Errorf("exit code = %d, want 0 — `cmd --help` is a successful invocation", got)
	}
}

func TestRunBackfillUsageErrorExitsTwo(t *testing.T) {
	t.Setenv("TOKEN", "x")
	if got := runBackfill(t.Context(), []string{"--from", "not-a-time", "--to", "also-not"}); got != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", got)
	}
}

func TestKnownSubcommands(t *testing.T) {
	for _, name := range []string{"healthcheck", "backfill"} {
		if !isKnownSubcommand(name) {
			t.Errorf("%q should be a known subcommand", name)
		}
	}
	for _, name := range []string{"backfil", "helthcheck", "migrate", ""} {
		if isKnownSubcommand(name) {
			t.Errorf("%q should NOT be a known subcommand", name)
		}
	}
}

// TestKnownSubcommands above tests a pure predicate — it would still pass if
// main() never called it. This one tests the BEHAVIOR the design mandates:
// an unknown subcommand must exit 2 and must NOT start the daemon.
//
// The regression it guards is real: before the dispatch fix, `backfil` fell
// through and silently started a UDP listener, which never exits — so a
// failure here shows up as a timeout, not just a bad exit code.
func TestUnknownSubcommandExitsTwoWithoutStartingDaemon(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "twx")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	cmd := exec.CommandContext(t.Context(), bin, "backfil")
	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected a non-zero exit, got err=%v\n%s", err, out)
	}
	if code := exitErr.ExitCode(); code != 2 {
		t.Errorf("exit code = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(string(out), "unknown subcommand") {
		t.Errorf("output should name the bad subcommand, got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'TestParseBackfill|TestRunBackfill|TestResolveStore|TestKnownSubcommands|TestUnknownSubcommand' -v`
Expected: FAIL to compile — `undefined: parseBackfillFlags`, `undefined: runBackfill`, `undefined: resolveStore`, `undefined: isKnownSubcommand`.

- [ ] **Step 3: Write the shell**

Create `backfill_cmd.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"tempestwx-utilities/internal/backfill"
	"tempestwx-utilities/internal/config"
	"tempestwx-utilities/internal/postgres"
	"tempestwx-utilities/internal/sqlite"
	"tempestwx-utilities/internal/tempestapi"
	"tempestwx-utilities/internal/weather"

	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"
)

// parseBackfillFlags reads the backfill subcommand's own FlagSet.
//
// Each subcommand owning its FlagSet (parsed from os.Args[2:]) is the seam a
// future subcommand slots into: a new subcommand is a new file plus one
// dispatch line, with no sibling touched.
//
// --from/--to are RFC3339 interpreted as UTC. The store is UTC epoch and the
// API takes epoch seconds; an ambiguous local-time parse would be a quiet
// wrong-window bug, so a non-RFC3339 value is rejected rather than guessed at.
func parseBackfillFlags(args []string) (backfill.Config, string, error) {
	fs := flag.NewFlagSet("backfill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	fromStr := fs.String("from", "", "start of the window to repair, RFC3339 UTC (default: auto-detect)")
	toStr := fs.String("to", "", "end of the window to repair, RFC3339 UTC (default: auto-detect)")
	minGap := fs.Duration("min-gap", 30*time.Minute, "smallest interval that counts as a gap")
	dryRun := fs.Bool("dry-run", false, "detect and plan only: no observation fetches, no writes")

	store := fs.String("store", "", "which store to repair: sqlite or postgres (required when both are configured)")

	if err := fs.Parse(args); err != nil {
		return backfill.Config{}, "", err
	}

	cfg := backfill.Config{MinGap: *minGap, DryRun: *dryRun}

	// --store is returned separately, not folded into backfill.Config: it
	// selects which handle the SHELL opens, and Run has no use for it. A core
	// config struct carrying a field the core ignores is a trap.
	switch *store {
	case "", "sqlite", "postgres":
	default:
		return cfg, *store, usageErr(fs, "--store must be sqlite or postgres, got %q", *store)
	}

	if (*fromStr == "") != (*toStr == "") {
		return cfg, *store, usageErr(fs, "--from and --to must be given together, or neither")
	}
	if *fromStr != "" {
		from, err := time.Parse(time.RFC3339, *fromStr)
		if err != nil {
			return cfg, *store, usageErr(fs, "--from must be RFC3339 (e.g. 2026-01-02T03:04:05Z): %w", err)
		}
		to, err := time.Parse(time.RFC3339, *toStr)
		if err != nil {
			return cfg, *store, usageErr(fs, "--to must be RFC3339 (e.g. 2026-01-02T03:04:05Z): %w", err)
		}
		cfg.From, cfg.To = from.UTC(), to.UTC()
		if !cfg.To.After(cfg.From) {
			return cfg, *store, usageErr(fs, "--to must be after --from")
		}
	}
	return cfg, *store, nil
}

// usageErr reports a validation failure the way flag reports a parse failure:
// message plus usage, on stderr. Centralizing it means runBackfill prints
// nothing itself, so flag's message is never duplicated.
func usageErr(fs *flag.FlagSet, format string, a ...any) error {
	err := fmt.Errorf(format, a...)
	fmt.Fprintln(fs.Output(), err)
	fs.Usage()
	return err
}

// resolveStore decides which single store this run repairs.
//
// Backfill repairs ONE store per run. selectStore returns BOTH when
// ENABLE_POSTGRES=true and SQLITE_PATH is set — a documented fan-out the
// daemon honors by writing every observation to both (main.go:302-336). In
// that configuration backfill cannot infer the target, and guessing is the
// worst option available: repairing Postgres while leaving the
// Litestream-replicated SQLite database holed, then exiting 0, tells the
// operator the history is fixed when it is not.
func resolveStore(choice storeChoice, flagValue string) (string, error) {
	var configured []string
	if choice.sqlite {
		configured = append(configured, "sqlite")
	}
	if choice.postgres {
		configured = append(configured, "postgres")
	}

	switch len(configured) {
	case 0:
		return "", errors.New("backfill: no store configured; set SQLITE_PATH, or ENABLE_POSTGRES=true with POSTGRES_URL")
	case 1:
		if flagValue != "" && flagValue != configured[0] {
			return "", fmt.Errorf("backfill: --store=%s, but only %s is configured", flagValue, configured[0])
		}
		return configured[0], nil
	default:
		if flagValue == "" {
			return "", fmt.Errorf(
				"backfill: both %s are configured; pass --store=sqlite or --store=postgres. "+
					"Backfill repairs one store per run and will not guess — silently repairing "+
					"only one while reporting success would leave the other permanently holed",
				strings.Join(configured, " and "))
		}
		return flagValue, nil
	}
}

// sqliteStore adapts the package-level SQLite functions to backfill.Store.
type sqliteStore struct{ db *sql.DB }

func (s sqliteStore) SeriesBounds(ctx context.Context, from, to time.Time) ([]weather.Bounds, error) {
	return sqlite.SeriesBounds(ctx, s.db, from, to)
}

func (s sqliteStore) FindObservationGaps(ctx context.Context, from, to time.Time, minGap time.Duration) ([]weather.Gap, error) {
	return sqlite.FindObservationGaps(ctx, s.db, from, to, minGap)
}

func (s sqliteStore) InsertObservations(ctx context.Context, obs []weather.Observation) (int, error) {
	return sqlite.InsertObservations(ctx, s.db, obs)
}

// postgresStore adapts the package-level Postgres functions to backfill.Store.
type postgresStore struct{ pool *pgxpool.Pool }

func (s postgresStore) SeriesBounds(ctx context.Context, from, to time.Time) ([]weather.Bounds, error) {
	return postgres.SeriesBounds(ctx, s.pool, from, to)
}

func (s postgresStore) FindObservationGaps(ctx context.Context, from, to time.Time, minGap time.Duration) ([]weather.Gap, error) {
	return postgres.FindObservationGaps(ctx, s.pool, from, to, minGap)
}

func (s postgresStore) InsertObservations(ctx context.Context, obs []weather.Observation) (int, error) {
	return postgres.InsertObservations(ctx, s.pool, obs)
}

// runBackfill is the backfill subcommand's shell: parse, read env, open
// handles, wire dependencies, return an exit code.
//
// It performs ALL cleanup via internal defers. Copying the healthcheck shape
// (os.Exit at the dispatch site, main.go:189-191) would skip db.Close() and
// the pgx pool drain.
//
// Exit codes: 0 success (including permanent holes), 1 a gap failed or a
// runtime error, 2 a usage error.
func runBackfill(ctx context.Context, args []string) int {
	cfg, storeFlag, err := parseBackfillFlags(args)
	if err != nil {
		// --help is NOT a usage error. flag prints the usage itself and
		// returns ErrHelp; exiting 2 here would break every CI smoke test
		// that runs `cmd --help`.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		// parseBackfillFlags owns the FlagSet's output and has already
		// reported the error, so nothing is printed here — printing again
		// would duplicate flag's own message.
		return 2
	}

	// TOKEN is validated BEFORE any store handle is opened, so the failure
	// costs no I/O and leaves nothing to close.
	token := os.Getenv("TOKEN")
	if token == "" {
		slog.Error("backfill: TOKEN is required to reach the Tempest REST API")
		return 1
	}

	// Signal wiring lives in the subcommand — the healthcheck path has none
	// to inherit.
	ctx, stop := signalContext(ctx, signal.NotifyContext)
	defer stop()

	enablePostgres, err := config.ParseBoolEnv("ENABLE_POSTGRES")
	if err != nil {
		slog.Error("backfill: bad ENABLE_POSTGRES", "error", err)
		return 1
	}
	choice := selectStore(enablePostgres, os.Getenv("SQLITE_PATH"))

	// Backfill repairs ONE store per run, and must never guess which. The
	// daemon fans out to both when ENABLE_POSTGRES and SQLITE_PATH are both
	// set (main.go:147-154, :302-336); silently repairing Postgres while
	// leaving the SQLite database — the one Litestream replicates to S3 —
	// still holed, then reporting success, is the worst available outcome.
	target, err := resolveStore(choice, storeFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	var store backfill.Store
	switch target {
	case "postgres":
		dbConfig, err := config.GetDatabaseConfig()
		if err != nil {
			slog.Error("backfill: database configuration", "error", err)
			return 1
		}
		if dbConfig == "" {
			slog.Error("backfill: POSTGRES_URL or POSTGRES_HOST is required when ENABLE_POSTGRES is true")
			return 1
		}
		// Backfill opens the WRITE path: it must create the schema and
		// insert. OpenPool starts no goroutines, unlike NewPostgresWriter.
		pool, err := postgres.OpenPool(ctx, dbConfig)
		if err != nil {
			slog.Error("backfill: open postgres", "error", err)
			return 1
		}
		defer pool.Close()
		if err := postgres.CreateSchema(ctx, pool); err != nil {
			slog.Error("backfill: create postgres schema", "error", err)
			return 1
		}
		store = postgresStore{pool: pool}
	case choice.sqlite:
		// The write handle, not OpenReadOnly: read-only fails when the file
		// does not exist and cannot migrate, and its ingest-contention
		// rationale does not apply to a separate one-shot process.
		db, err := sqlite.Open(ctx, choice.sqlitePath, sqlite.LoadConfig(os.Getenv))
		if err != nil {
			slog.Error("backfill: open sqlite", "path", choice.sqlitePath, "error", err)
			return 1
		}
		defer func() {
			if err := db.Close(); err != nil {
				slog.Error("backfill: close sqlite", "error", err)
			}
		}()
		store = sqliteStore{db: db}
	}
	slog.Info("backfill: store selected", "store", target)

	client := tempestapi.NewClient(token)
	// ListDevices, NOT ListStations: ListStations collapses each station to a
	// single ST device, so a two-sensor station would leave one sensor's gaps
	// permanently unrepaired and unlogged.
	devices, err := client.ListDevices(ctx)
	if err != nil {
		slog.Error("backfill: list devices", "error", err)
		return 1
	}
	if len(devices) == 0 {
		slog.Error("backfill: the API reported no ST devices for this token")
		return 1
	}
	slog.Info("backfill: devices discovered", "count", len(devices))

	stats, err := backfill.Run(ctx, cfg, client, store, devices, time.Now().UTC())
	slog.Info("backfill: complete",
		"gaps", stats.Gaps, "returned", stats.Returned,
		"inserted", stats.Inserted, "failed", stats.Failed, "dry_run", cfg.DryRun)
	if err != nil {
		slog.Error("backfill: finished with failures", "error", err)
		return 1
	}
	return 0
}
```

> **Verified helper names** (do not substitute others): `sqlite.LoadConfig(getenv func(string) string) Config` is at `internal/sqlite/db.go:33`, and `config.GetDatabaseConfig() (string, error)` is at `internal/config/database.go:18` — the latter already resolves `POSTGRES_URL` or the individual `POSTGRES_*` components, so nothing needs extracting from `main()`.

- [ ] **Step 4: Fix the dispatch in `main()`**

In `main.go`, replace the current dispatch (`:189-191`):

```go
func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
```

with:

```go
// isKnownSubcommand reports whether name is a subcommand this binary
// dispatches. It exists so an unrecognized argument becomes a usage error
// instead of silently falling through to daemon mode — a typo such as
// `tempestwx-utilities backfil` previously started a UDP listener.
func isKnownSubcommand(name string) bool {
	switch name {
	case "healthcheck", "backfill":
		return true
	default:
		return false
	}
}

const usageText = `tempestwx-utilities — Tempest weather station data utilities

usage:
  tempestwx-utilities                 run the UDP listener / API export daemon (configured by env)
  tempestwx-utilities backfill [...]  fill gaps in the observation history from the Tempest REST API
  tempestwx-utilities healthcheck     probe the running server's /healthz endpoint

run "tempestwx-utilities backfill --help" for the backfill flags
`

func main() {
	// A non-flag first argument is a subcommand. An unknown one is a usage
	// error, never a silent fallthrough to daemon mode.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		if !isKnownSubcommand(os.Args[1]) {
			fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", os.Args[1], usageText)
			os.Exit(2)
		}
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(runHealthcheck())
		case "backfill":
			// runBackfill owns all of its cleanup via internal defers and
			// wires its own signal context.
			os.Exit(runBackfill(context.Background(), os.Args[2:]))
		}
	}
```

Add `"strings"` to `main.go`'s import block. `context` and `fmt` are already imported.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test . -run 'TestParseBackfill|TestRunBackfill|TestResolveStore|TestKnownSubcommands|TestUnknownSubcommand' -v`
Expected: PASS.

- [ ] **Step 6: Verify the dispatch by hand**

These behaviors also have real tests (`TestUnknownSubcommandExitsTwoWithoutStartingDaemon`, `TestRunBackfillHelpExitsZero`); run them by hand once as a sanity check on the built artifact:

```bash
CGO_ENABLED=0 go build -o /tmp/twx . && /tmp/twx backfil; echo "exit=$?"
```
Expected: prints `unknown subcommand "backfil"` plus usage, `exit=2`, and **does not** start a UDP listener or block.

```bash
/tmp/twx backfill --help; echo "exit=$?"
```
Expected: prints the five backfill flags with their defaults, and **`exit=0`** — `--help` is not a usage error.

```bash
ENABLE_POSTGRES=true SQLITE_PATH=/tmp/x.db TOKEN=x /tmp/twx backfill; echo "exit=$?"
```
Expected: refuses with a message naming both stores and telling you to pass `--store`; `exit=2`; nothing written.

- [ ] **Step 7: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

- [ ] **Step 8: Commit**

```bash
git add backfill_cmd.go backfill_cmd_test.go main.go
git commit -m "feat: add the backfill subcommand and reject unknown subcommands"
```

---

### Task 11: Documentation

**Files:**
- Modify: `CLAUDE.md:239` (name collision), `:78` (package list), the operational-modes table

**Interfaces:**
- Consumes: the shipped behavior of Tasks 1–10.
- Produces: nothing consumed by code.

**Why this is a task and not a footnote:** `CLAUDE.md` already has a section titled "**API Export with Backfill**" (`:239`) describing the *existing* `TOKEN`-triggered `ModeAPIExport`. Shipping a `backfill` subcommand alongside a differently-meaning section of nearly the same name would leave the repo's own guidance ambiguous about which "backfill" is which.

- [ ] **Step 1: Resolve the name collision**

Rename the existing `### API Export with Backfill` heading (`CLAUDE.md:239`) to `### API Export Mode (TOKEN)`, and adjust its body so it clearly describes the env-var-triggered historical export — not the new subcommand.

- [ ] **Step 2: Add the subcommand section**

Immediately after it, add:

```markdown
### Backfill Subcommand

Fills holes in the local observation history from the Tempest REST API. Unlike
API Export Mode (which is selected by setting `TOKEN` and runs the whole export
path), this is an explicit subcommand and can be run against a live database:

```bash
TOKEN=your_api_token tempestwx-utilities backfill
TOKEN=... tempestwx-utilities backfill --dry-run
TOKEN=... tempestwx-utilities backfill --from 2026-07-01T00:00:00Z --to 2026-07-05T00:00:00Z
```

It writes to whichever store is configured (SQLite by default, Postgres when
`ENABLE_POSTGRES=true`) and is idempotent — re-running inserts nothing new.

| Flag | Default | Meaning |
|---|---|---|
| `--from`, `--to` | unset (auto-detect) | Explicit window, RFC3339 **UTC**. Must be given together. |
| `--min-gap` | `30m` | Smallest interval that counts as a gap. Raise it for stations with a long `report_interval`. |
| `--dry-run` | `false` | Detect and plan only: zero observation fetches, zero writes. It still lists devices, so it *does* validate the token. |
| `--store` | unset | `sqlite` or `postgres`. **Required** when both stores are configured. |

**`--store` and the fan-out configuration.** With `ENABLE_POSTGRES=true` *and*
`SQLITE_PATH` set, the daemon writes every observation to **both** stores.
Backfill repairs one store per run, so in that configuration it refuses to start
without `--store` rather than guessing — silently repairing Postgres while
leaving the Litestream-replicated SQLite database holed, and then reporting
success, would be worse than failing.

**Multiple sensors.** `backfill` enumerates every `ST` device on the account, so a
station with two Tempest units has both repaired. (This differs from `TOKEN`-mode
API export, which sees one device per station.)

**Exit codes:** `0` success — including `--help`, and including permanent holes
(windows the station was genuinely offline for, which the API cannot fill
either); `1` one or more gaps failed, or a runtime error; `2` usage error.

**Scope:** `tempest_observations` only. There is no historical REST endpoint for
rapid wind, hub status, or discrete events. Lightning is partially recovered in
aggregate through the observation columns, but not as `tempest_events` rows.

**Safety:** if the API reports a station serial the (non-empty) store has never
seen, backfill stops and writes nothing — a mismatched serial would create a
parallel series that never dedupes.
```

- [ ] **Step 3: Update the package list**

At `CLAUDE.md:76-79`, add to the internal-package list:

```markdown
- **`internal/weather/`**: Store-neutral `Observation`, `Gap`, and `Bounds` types shared by the REST client and both stores
- **`internal/backfill/`**: Gap detection and API backfill orchestration for the `backfill` subcommand
```

Leave the `internal/tempestudp/` line as it is — it correctly describes UDP parsing, and the backfill types deliberately live elsewhere.

- [ ] **Step 4: Note that subcommands bypass mode selection**

Under the "Operational Modes" table, add:

```markdown
> **Subcommands bypass mode selection.** `backfill` and `healthcheck` are chosen
> by the first CLI argument, not by environment variables, and neither starts the
> UDP listener or the HTTP server. Any other non-flag first argument is a usage
> error (exit 2) rather than a silent fallthrough to daemon mode.
```

- [ ] **Step 5: Verify the docs match reality**

Run each command in the new section against the built binary and confirm the flags and exit codes are exactly as documented:

```bash
CGO_ENABLED=0 go build -o /tmp/twx . && /tmp/twx backfill --help
```
Expected: the four flags with the documented defaults. Fix the doc if it disagrees — the binary is the source of truth.

- [ ] **Step 6: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

- [ ] **Step 7: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document the backfill subcommand and resolve the API-export name collision"
```

---

## After all tasks

Dispatch a **fresh, non-context-sharing** review subagent per `rules/subagent-development-workflow.md` step 4. Brief it with:
- branch `worktree-api-backfill`, diff scope `git diff 8990b88..HEAD`
- the design document path, as the spec
- the acceptance criteria: every row of the design's Testing table has a corresponding passing test

Do **not** summarize what was built — let the reviewer reach its own conclusions from the code. Then run `superpowers:finishing-a-development-branch`.

## Known scope exclusions

Stated so a reviewer does not read them as omissions:

- **`tempest_rapid_wind`, `tempest_hub_status`, `tempest_events` are not backfilled.** No historical REST endpoint exists for any of them.
- **The missing `break` in `ListStations`' device loop is not fixed.** `ModeAPIExport` depends on current behavior; backfill iterates all `ST` devices itself.
- **`ModeAPIExport` and `sqlite.WriteMetrics` are unchanged.**
- **No `--json` output.** The bespoke summary line was cut as speculative; `slog` attrs are the machine-readable surface. Add `--json` if a caller ever needs it.
- **`Retry-After` parsing and a fixed inter-request delay were cut.** WeatherFlow publishes no rate limits; bounded backoff already handles 429.
- **`CLAUDE.md` staleness elsewhere** (no SQLite-default/OTel/httpserver/radar coverage; still says "Go 1.23.0+") is out of scope — flagged for a separate docs pass.
