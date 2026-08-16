package sqlite

import (
	"database/sql"
	"testing"
)

// seedTemp inserts one observation row with the given id, timestamp and
// (nullable) air temperature. Every other column is left NULL -- this test
// only exercises temp_air.
func seedTemp(t *testing.T, w *Writer, id string, ts int64, temp sql.NullFloat64) {
	t.Helper()
	_, err := w.db.ExecContext(t.Context(),
		`INSERT INTO tempest_observations (id, serial_number, timestamp, temp_air) VALUES (?,?,?,?)`,
		id, "ST-1", ts, temp)
	if err != nil {
		t.Fatal(err)
	}
}

// nullTemp is deliberately NOT named f: package sqlite already declares
// `func f(v float64) *float64` in backfill_test.go, and both files compile
// into the same test binary.
func nullTemp(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }

func TestTemperatureExtremes(t *testing.T) {
	t.Run("populated_window", func(t *testing.T) {
		w := newTestWriter(t)
		seedTemp(t, w, "a", 100, nullTemp(10))
		seedTemp(t, w, "b", 200, nullTemp(25))
		seedTemp(t, w, "c", 300, nullTemp(18))

		te, err := w.TemperatureExtremes(t.Context(), 100, 300)
		if err != nil {
			t.Fatal(err)
		}
		if !te.Max.Valid || te.Max.Float64 != 25 || !te.MaxAt.Valid || te.MaxAt.Int64 != 200 {
			t.Errorf("max = %v at %v, want 25 at 200", te.Max, te.MaxAt)
		}
		if !te.Min.Valid || te.Min.Float64 != 10 || !te.MinAt.Valid || te.MinAt.Int64 != 100 {
			t.Errorf("min = %v at %v, want 10 at 100", te.Min, te.MinAt)
		}
	})

	t.Run("empty_window_returns_all_invalid", func(t *testing.T) {
		w := newTestWriter(t)
		seedTemp(t, w, "a", 100, nullTemp(10))

		te, err := w.TemperatureExtremes(t.Context(), 500, 600)
		if err != nil {
			t.Fatal(err)
		}
		if te.Max.Valid || te.MaxAt.Valid || te.Min.Valid || te.MinAt.Valid {
			t.Fatalf("every field must be invalid for an empty window, got %+v", te)
		}
	})

	t.Run("single_row_window_is_both_extremes", func(t *testing.T) {
		w := newTestWriter(t)
		seedTemp(t, w, "a", 150, nullTemp(12.5))

		te, err := w.TemperatureExtremes(t.Context(), 100, 200)
		if err != nil {
			t.Fatal(err)
		}
		if te.Max.Float64 != 12.5 || te.MaxAt.Int64 != 150 || te.Min.Float64 != 12.5 || te.MinAt.Int64 != 150 {
			t.Fatalf("got %+v, want 12.5 at 150 for both extremes", te)
		}
	})

	// The all-NULL case is indistinguishable from an empty window by design:
	// the IS NOT NULL filter means both return zero rows, so a value and its
	// timestamp are always valid together or invalid together.
	t.Run("all_null_temp_air_returns_all_invalid", func(t *testing.T) {
		w := newTestWriter(t)
		seedTemp(t, w, "a", 100, sql.NullFloat64{})
		seedTemp(t, w, "b", 200, sql.NullFloat64{})

		te, err := w.TemperatureExtremes(t.Context(), 100, 200)
		if err != nil {
			t.Fatal(err)
		}
		if te.Max.Valid || te.MaxAt.Valid || te.Min.Valid || te.MinAt.Valid {
			t.Fatalf("every field must be invalid when every temp_air is NULL, got %+v", te)
		}
	})

	// This is the case the natural `ORDER BY temp_air ASC LIMIT 1` gets
	// wrong: NULLs sort first, so it would return the NULL row as the
	// minimum and Min/MinAt would disagree.
	t.Run("null_row_is_not_returned_as_the_minimum", func(t *testing.T) {
		w := newTestWriter(t)
		seedTemp(t, w, "null", 100, sql.NullFloat64{})
		seedTemp(t, w, "warm", 200, nullTemp(20))
		seedTemp(t, w, "cold", 300, nullTemp(5))

		te, err := w.TemperatureExtremes(t.Context(), 100, 300)
		if err != nil {
			t.Fatal(err)
		}
		if !te.Min.Valid || te.Min.Float64 != 5 || te.MinAt.Int64 != 300 {
			t.Errorf("min = %v at %v, want 5 at 300 -- the NULL row must be filtered, not ranked first",
				te.Min, te.MinAt)
		}
		if !te.Max.Valid || te.Max.Float64 != 20 || te.MaxAt.Int64 != 200 {
			t.Errorf("max = %v at %v, want 20 at 200", te.Max, te.MaxAt)
		}
	})

	// This is the case a bare-column aggregate gets wrong: it returns the
	// LATEST tied row under both ASC and DESC, because the ORDER BY is a
	// no-op over a single aggregate row.
	t.Run("ties_resolve_to_the_earliest_occurrence", func(t *testing.T) {
		w := newTestWriter(t)
		seedTemp(t, w, "first", 100, nullTemp(25))
		seedTemp(t, w, "second", 200, nullTemp(25))
		seedTemp(t, w, "third", 300, nullTemp(25))

		te, err := w.TemperatureExtremes(t.Context(), 100, 300)
		if err != nil {
			t.Fatal(err)
		}
		if te.MaxAt.Int64 != 100 {
			t.Errorf("maxAt = %d, want 100 -- ties must resolve to the EARLIEST occurrence", te.MaxAt.Int64)
		}
		if te.MinAt.Int64 != 100 {
			t.Errorf("minAt = %d, want 100 -- ties must resolve to the EARLIEST occurrence", te.MinAt.Int64)
		}
	})

	// The window is inclusive at both ends, matching SummarizeObservations.
	t.Run("window_bounds_are_inclusive", func(t *testing.T) {
		w := newTestWriter(t)
		seedTemp(t, w, "before", 99, nullTemp(-40))
		seedTemp(t, w, "at_from", 100, nullTemp(1))
		seedTemp(t, w, "at_to", 300, nullTemp(2))
		seedTemp(t, w, "after", 301, nullTemp(99))

		te, err := w.TemperatureExtremes(t.Context(), 100, 300)
		if err != nil {
			t.Fatal(err)
		}
		if te.Max.Float64 != 2 || te.Min.Float64 != 1 {
			t.Fatalf("got max %v min %v, want 2 and 1 -- rows outside [from, to] must be excluded",
				te.Max, te.Min)
		}
	})
}
