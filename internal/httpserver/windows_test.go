package httpserver

import (
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func TestAlmanacWindows(t *testing.T) {
	denver := mustLoad(t, "America/Denver")

	t.Run("four_boundaries_on_a_wednesday", func(t *testing.T) {
		// 2026-07-15 is a Wednesday. Local 14:30 in Denver.
		now := time.Date(2026, time.July, 15, 14, 30, 0, 0, denver)
		today, week, month, year := almanacWindows(now, denver)

		wantTo := now.Unix()
		for name, w := range map[string]almanacWindow{"today": today, "week": week, "month": month, "year": year} {
			if w.To != wantTo {
				t.Errorf("%s.To = %d, want the request instant %d", name, w.To, wantTo)
			}
		}
		assertLocalStart(t, "today", today, time.Date(2026, time.July, 15, 0, 0, 0, 0, denver))
		// The preceding Sunday is 2026-07-12.
		assertLocalStart(t, "week", week, time.Date(2026, time.July, 12, 0, 0, 0, 0, denver))
		assertLocalStart(t, "month", month, time.Date(2026, time.July, 1, 0, 0, 0, 0, denver))
		assertLocalStart(t, "year", year, time.Date(2026, time.January, 1, 0, 0, 0, 0, denver))
	})

	t.Run("on_a_sunday_the_week_starts_today", func(t *testing.T) {
		// 2026-07-12 is a Sunday. Week-to-date means the window starts TODAY,
		// not seven days ago.
		now := time.Date(2026, time.July, 12, 9, 0, 0, 0, denver)
		today, week, _, _ := almanacWindows(now, denver)

		if week.From != today.From {
			t.Fatalf("week.From = %d, today.From = %d -- on a Sunday they must be equal (week-to-date)",
				week.From, today.From)
		}
	})

	// This is the consequence that must NOT be clamped: for up to six days of
	// every month the calendar week begins before the calendar month.
	t.Run("week_may_start_before_month", func(t *testing.T) {
		// 2026-07-01 is a Wednesday, so the week began Sunday 2026-06-28.
		now := time.Date(2026, time.July, 1, 12, 0, 0, 0, denver)
		_, week, month, _ := almanacWindows(now, denver)

		if week.From >= month.From {
			t.Fatalf("week.From = %d, month.From = %d -- the week legitimately precedes the month here and must not be clamped",
				week.From, month.From)
		}
		assertLocalStart(t, "week", week, time.Date(2026, time.June, 28, 0, 0, 0, 0, denver))
	})

	t.Run("week_may_start_before_year", func(t *testing.T) {
		// 2026-01-01 is a Thursday, so the week began Sunday 2025-12-28.
		now := time.Date(2026, time.January, 1, 12, 0, 0, 0, denver)
		_, week, _, year := almanacWindows(now, denver)

		if week.From >= year.From {
			t.Fatalf("week.From = %d, year.From = %d -- the week legitimately precedes the year here",
				week.From, year.From)
		}
		assertLocalStart(t, "week", week, time.Date(2025, time.December, 28, 0, 0, 0, 0, denver))
	})

	// Santiago transitions DST AT midnight on 2026-09-06 (a Sunday): local
	// 00:00 DOES NOT EXIST that day, and time.Date's own documentation says
	// it "returns a time that is correct in one of the two zones involved in
	// the transition, but it does not guarantee which". Measured, Go resolves
	// the gap BACKWARDS, to 2026-09-05 23:00 -04.
	//
	// The design accepts that imprecision for a window start (§6.2) rather
	// than special-casing it. This test therefore pins what is actually
	// guaranteed -- a start within an hour of the intended midnight, not
	// after the request instant, and consistent between today and week -- and
	// deliberately does NOT assert the calendar day, which Go does not
	// promise here.
	t.Run("midnight_dst_transition_zone", func(t *testing.T) {
		santiago := mustLoad(t, "America/Santiago")
		now := time.Date(2026, time.September, 6, 15, 0, 0, 0, santiago)
		today, week, _, _ := almanacWindows(now, santiago)

		// intended is built with the SAME time.Date call almanacWindows uses,
		// so it resolves the nonexistent midnight identically -- the two are
		// equal by construction. The assertion below is a contract on that
		// shared resolution (at or before the nominal midnight, by at most an
		// hour), not a comparison of two independent computations.
		intended := time.Date(2026, time.September, 6, 0, 0, 0, 0, santiago)
		t.Logf("local midnight resolved to %s", time.Unix(today.From, 0).In(santiago))

		if d := intended.Unix() - today.From; d < 0 || d > 3600 {
			t.Fatalf("today.From = %s, want within one hour at or before the intended local midnight %s",
				time.Unix(today.From, 0).In(santiago), intended)
		}
		if today.From > now.Unix() {
			t.Fatalf("today.From = %d is after the request instant %d", today.From, now.Unix())
		}
		// 2026-09-06 is a Sunday, so week-to-date starts at the same instant
		// -- including the same gap resolution.
		if week.From != today.From {
			t.Errorf("week.From = %d, today.From = %d", week.From, today.From)
		}
	})
}

func assertLocalStart(t *testing.T, label string, got almanacWindow, want time.Time) {
	t.Helper()
	if got.From != want.Unix() {
		t.Errorf("%s.From = %d (%s), want %d (%s)",
			label, got.From, time.Unix(got.From, 0).In(want.Location()),
			want.Unix(), want)
	}
}
