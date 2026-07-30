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
