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

func TestAssembleGapsInteriorGapIsScopedToItsSerial(t *testing.T) {
	// Two serials, but the interior gap belongs only to ST-A. Without the
	// SerialNumber guard, ST-B would wrongly inherit ST-A's outage.
	interior := []weather.Gap{{SerialNumber: "ST-A", From: ts(20000), To: ts(30000)}}
	bounds := []weather.Bounds{
		{SerialNumber: "ST-A", First: ts(1000), Last: ts(90000)},
		{SerialNumber: "ST-B", First: ts(1000), Last: ts(90000)},
	}
	got := assembleGaps(interior, bounds, []string{"ST-A", "ST-B"}, ts(1000), ts(90000), 30*time.Minute)

	set := gapSet(got)
	if len(set["ST-A"]) != 1 || set["ST-A"][0] != [2]int64{20000, 30000} {
		t.Errorf("ST-A got %v, want one interior gap [20000, 30000]", set["ST-A"])
	}
	if len(set["ST-B"]) != 0 {
		t.Errorf("ST-B got %v, want no gaps (ST-A's interior gap must not leak)", set["ST-B"])
	}
}

func TestAssembleGapsSortsAcrossAndWithinSerials(t *testing.T) {
	// serials is deliberately out of lexicographic order, and ST-A's interior
	// gaps are deliberately out of chronological order in the input slice —
	// both must be corrected by the trailing sort, not by input order.
	interior := []weather.Gap{
		{SerialNumber: "ST-A", From: ts(56000), To: ts(58000)},
		{SerialNumber: "ST-A", From: ts(52000), To: ts(54000)},
	}
	bounds := []weather.Bounds{
		{SerialNumber: "ST-A", First: ts(50000), Last: ts(60000)},
	}
	got := assembleGaps(interior, bounds, []string{"ST-B", "ST-A"}, ts(1000), ts(90000), 30*time.Minute)

	want := []struct {
		serial   string
		from, to int64
	}{
		{"ST-A", 1000, 50000},
		{"ST-A", 52000, 54000},
		{"ST-A", 56000, 58000},
		{"ST-A", 60000, 90000},
		{"ST-B", 1000, 90000},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d gaps, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].SerialNumber != w.serial || !got[i].From.Equal(ts(w.from)) || !got[i].To.Equal(ts(w.to)) {
			t.Errorf("gap[%d] = %+v, want {%s [%d, %d]}", i, got[i], w.serial, w.from, w.to)
		}
	}
}

func TestAssembleGapsHeadInteriorAndTailTogether(t *testing.T) {
	// All three sources contribute for one serial at once — the case the
	// other tests deliberately avoid by spanning bounds over the full range.
	interior := []weather.Gap{{SerialNumber: "ST-A", From: ts(52000), To: ts(58000)}}
	bounds := []weather.Bounds{{SerialNumber: "ST-A", First: ts(50000), Last: ts(60000)}}
	got := assembleGaps(interior, bounds, []string{"ST-A"}, ts(1000), ts(90000), 30*time.Minute)

	want := [][2]int64{{1000, 50000}, {52000, 58000}, {60000, 90000}}
	set := gapSet(got)["ST-A"]
	if len(set) != len(want) {
		t.Fatalf("got %d gaps, want %d (head + interior + tail): %+v", len(set), len(want), got)
	}
	for i, w := range want {
		if set[i] != w {
			t.Errorf("gap[%d] = %v, want %v", i, set[i], w)
		}
	}
}

func TestAssembleGapsHeadEdgeExactlyMinGapIsDropped(t *testing.T) {
	// First is exactly detectFrom + minGap: the strict '>' check must drop
	// this edge, not include it.
	minGap := 30 * time.Minute
	bounds := []weather.Bounds{{SerialNumber: "ST-A", First: ts(1000).Add(minGap), Last: ts(80000)}}
	got := assembleGaps(nil, bounds, []string{"ST-A"}, ts(1000), ts(90000), minGap)

	if len(got) != 1 {
		t.Fatalf("got %d gaps, want 1 (tail only, head dropped at the boundary): %+v", len(got), got)
	}
	if got[0].SerialNumber != "ST-A" || !got[0].From.Equal(ts(80000)) || !got[0].To.Equal(ts(90000)) {
		t.Errorf("gap = %+v, want ST-A [80000, 90000]", got[0])
	}
}

func TestAssembleGapsEmptyStoreNarrowerThanMinGapProducesNoGap(t *testing.T) {
	// The detection window itself is narrower than minGap, so even the
	// empty-store whole-range gap must be dropped.
	got := assembleGaps(nil, nil, []string{"ST-A"}, ts(1000), ts(2000), 30*time.Minute)
	if len(got) != 0 {
		t.Errorf("got %d gaps, want 0: %+v", len(got), got)
	}
}
