package httpserver

import "time"

// almanacWindow is a half-open-in-name, closed-in-practice [From, To]
// unix-second range: TemperatureExtremes uses BETWEEN, matching
// SummarizeObservations.
type almanacWindow struct {
	From int64
	To   int64
}

// almanacWindows returns the four calendar-aligned windows the almanac card
// reports over, each starting at a local midnight in loc and ending at the
// request instant.
//
// This deliberately differs from handleSummary's ROLLING windows
// (from := to - days*86400). The labels this feeds -- "Today", "This Week",
// "This Month", "This Year" -- read as calendar periods, and unlike the
// records summary this endpoint has a timezone to align them in. The two
// cards render side by side and WILL disagree; that is intended, not a bug
// to reconcile.
//
// Two properties that look like bugs and are not:
//
//   - week may start BEFORE month or year. The calendar week runs Sunday to
//     Saturday, so for up to six days of every month -- and for every year
//     not beginning on a Sunday -- it reaches back past the month or year
//     boundary. Do not clamp it.
//   - On a Sunday, week is week-TO-DATE and starts today, not seven days ago.
//
// A local midnight does not always exist: Santiago, Beirut and Havana
// transition DST at midnight, and time.Date then "returns a time that is
// correct in one of the two zones involved in the transition, but it does
// not guarantee which" (its own documentation). Measured on Santiago's
// 2026-09-06 transition, Go resolves the gap BACKWARDS, so the today window
// begins at 23:00 on the previous local day and includes that extra hour.
// Accepted for a window start rather than special-cased, and pinned by a
// test rather than left to chance -- but see almanacDateLabel, whose "always
// Today" property does not hold for an extreme falling in that hour.
func almanacWindows(now time.Time, loc *time.Location) (today, week, month, year almanacWindow) {
	local := now.In(loc)
	y, mo, d := local.Date()
	to := now.Unix()

	dayStart := time.Date(y, mo, d, 0, 0, 0, 0, loc)
	// The week start is computed with time.Date on a normalised day number
	// rather than dayStart.AddDate(0, 0, -n). AddDate preserves dayStart's
	// CLOCK fields, and on a midnight-DST date those are not 00:00 -- so the
	// week window would inherit that day's gap resolution instead of getting
	// its own. time.Date normalises an out-of-range day (d - 6 may be zero or
	// negative) into the previous month or year correctly.
	weekStart := time.Date(y, mo, d-int(local.Weekday()), 0, 0, 0, 0, loc)
	monthStart := time.Date(y, mo, 1, 0, 0, 0, 0, loc)
	yearStart := time.Date(y, time.January, 1, 0, 0, 0, 0, loc)

	return almanacWindow{From: dayStart.Unix(), To: to},
		almanacWindow{From: weekStart.Unix(), To: to},
		almanacWindow{From: monthStart.Unix(), To: to},
		almanacWindow{From: yearStart.Unix(), To: to}
}
