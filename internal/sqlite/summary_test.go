package sqlite

import (
	"database/sql"
	"math"
	"testing"

	"github.com/google/uuid"
)

// TestSummarizeObservations proves SummarizeObservations aggregates a
// [from, to] window correctly: MIN/MAX/SUM over the seeded rows, a SUM over
// lightning_strike_count that skips a NULL row rather than treating it as
// zero, and an empty window (no rows in range) surfacing Count==0 with every
// sql.Null* field left invalid rather than zero-valued.
func TestSummarizeObservations(t *testing.T) {
	w := newTestWriter(t)
	// newTestWriter defaults readDB to db (no WithReadDB option), so seeding
	// through w.db (same package, unexported field) is equivalent to seeding
	// through whatever handle SummarizeObservations reads from here.
	db := w.db
	ctx := t.Context()

	seed := func(id string, ts int64, temp, hum, pres, wavg, wgust, rain float64, ls sql.NullFloat64) {
		_, err := db.ExecContext(ctx, `INSERT INTO stormglass_observations
			(id, serial_number, timestamp, temp_air, humidity, pressure, wind_avg, wind_gust, rain_rate, lightning_strike_count)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			id, "ST-1", ts, temp, hum, pres, wavg, wgust, rain, ls)
		if err != nil {
			t.Fatal(err)
		}
	}
	seed("a", 100, 10, 40, 1000, 3, 5, 0.5, sql.NullFloat64{Float64: 2, Valid: true})
	seed("b", 200, 25, 90, 1020, 8, 12, 1.0, sql.NullFloat64{}) // NULL lightning
	seed("c", 300, 18, 60, 1010, 5, 9, 0.25, sql.NullFloat64{Float64: 3, Valid: true})

	s, err := w.SummarizeObservations(ctx, 100, 300)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count != 3 {
		t.Fatalf("count=%d", s.Count)
	}
	if s.TempMax.Float64 != 25 || s.TempMin.Float64 != 10 {
		t.Fatalf("temp %v/%v", s.TempMax, s.TempMin)
	}
	if s.WindMax.Float64 != 8 || s.GustMax.Float64 != 12 {
		t.Fatalf("wind %v/%v", s.WindMax, s.GustMax)
	}
	if math.Abs(s.RainTotal.Float64-1.75) > 1e-9 {
		t.Fatalf("rain %v", s.RainTotal)
	}
	if s.LightningTotal.Float64 != 5 {
		t.Fatalf("lightning=%v (want 5, NULL row skipped)", s.LightningTotal.Float64)
	}
	if s.CoveredFrom.Int64 != 100 || s.CoveredTo.Int64 != 300 {
		t.Fatalf("coverage %v/%v", s.CoveredFrom, s.CoveredTo)
	}

	empty, err := w.SummarizeObservations(ctx, 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Count != 0 || empty.TempMax.Valid || empty.RainTotal.Valid || empty.CoveredFrom.Valid {
		t.Fatalf("empty window not all-null: %+v", empty)
	}
}

// TestSummarizeObservationsPreservesFractionalLightningTotal proves
// LightningTotal survives a fractional SUM. A fractional sum is the only
// thing that distinguishes a NullFloat64 LightningTotal from a NullInt64
// one: SUM(2,3) over a REAL column is 5.0, which a NullInt64 scan still
// accepts. SUM(2.5,3) is 5.5, which it rejects with "converting driver.Value
// type float64 to a int64".
func TestSummarizeObservationsPreservesFractionalLightningTotal(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	for i, v := range []float64{2.5, 3} {
		_, err := w.db.ExecContext(ctx, `
			INSERT INTO stormglass_observations (id, serial_number, timestamp, lightning_strike_count)
			VALUES (?, ?, ?, ?)`,
			uuid.Must(uuid.NewV7()).String(), "ST-A", int64(100+i), v)
		if err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	s, err := w.SummarizeObservations(ctx, 0, 1000)
	if err != nil {
		t.Fatalf("SummarizeObservations: %v", err)
	}
	if !s.LightningTotal.Valid {
		t.Fatal("LightningTotal is NULL, want 5.5")
	}
	if s.LightningTotal.Float64 != 5.5 {
		t.Errorf("LightningTotal = %v, want 5.5", s.LightningTotal.Float64)
	}
}
