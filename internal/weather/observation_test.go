package weather

import (
	"reflect"
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

// The invariant this pins is that every measurement field is POINTER-typed,
// so a JSON null can round-trip to SQL NULL. It walks the struct via
// reflection and asserts, at runtime, that every field other than the
// SerialNumber/Timestamp series key is *float64 — this is a real runtime
// assertion covering every field (including any added later), not just the
// handful a compile-time nil-assignment would happen to cover. The
// behavioral proof that a JSON null actually decodes to a nil pointer lives
// in Task 3's decode test and Task 5's insert test.
func TestObservationMeasurementFieldsArePointers(t *testing.T) {
	typ := reflect.TypeFor[Observation]()
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Name == "SerialNumber" || f.Name == "Timestamp" {
			continue
		}
		if f.Type.Kind() != reflect.Pointer || f.Type.Elem().Kind() != reflect.Float64 {
			t.Errorf("%s is %s, want *float64 so JSON null maps to SQL NULL", f.Name, f.Type)
		}
	}
}
