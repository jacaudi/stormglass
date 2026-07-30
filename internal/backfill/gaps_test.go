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
