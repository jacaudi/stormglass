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
