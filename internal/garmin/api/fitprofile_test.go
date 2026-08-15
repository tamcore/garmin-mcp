package api

import (
	"testing"
	"time"

	"github.com/muktihari/fit/profile/typedef"
)

// The sport labels these tests assert on.
const (
	sportRunningName = "running"
	sportCyclingName = "cycling"
)

// spanBase is a synthetic instant. Nothing in this file reads a recording.
var spanBase = time.Date(2026, time.January, 2, 8, 0, 0, 0, time.UTC)

// TestSpanEndPrefersTheElapsedTime covers the window derivation that the real files
// broke: a device that writes the same instant into start_time and timestamp must
// still produce a window as long as the elapsed time it reported.
func TestSpanEndPrefersTheElapsedTime(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		start     time.Time
		timestamp time.Time
		elapsed   FITNumber
		want      time.Time
	}{
		"collapsed timestamp": {
			start: spanBase, timestamp: spanBase, elapsed: fitNumber(600),
			want: spanBase.Add(10 * time.Minute),
		},
		"later timestamp wins": {
			start: spanBase, timestamp: spanBase.Add(time.Hour), elapsed: fitNumber(600),
			want: spanBase.Add(time.Hour),
		},
		"no elapsed time": {
			start: spanBase, timestamp: spanBase.Add(time.Minute), elapsed: FITNumber{},
			want: spanBase.Add(time.Minute),
		},
		"no start time": {
			timestamp: spanBase.Add(time.Minute), elapsed: fitNumber(600),
			want: spanBase.Add(time.Minute),
		},
		"zero elapsed time": {
			start: spanBase, timestamp: spanBase, elapsed: fitNumber(0),
			want: spanBase,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := spanEnd(tc.start, tc.timestamp, tc.elapsed); !got.Equal(tc.want) {
				t.Errorf("spanEnd() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSportNameReportsAnUnnamedCodeByNumber keeps a sport the profile does not name
// from being labelled with a guess, and keeps an absent sport absent.
func TestSportNameReportsAnUnnamedCodeByNumber(t *testing.T) {
	t.Parallel()

	cases := map[typedef.Sport]string{
		typedef.SportRunning: sportRunningName,
		typedef.SportCycling: sportCyclingName,
		typedef.SportInvalid: "",
		typedef.Sport(96):    "sport_96",
	}
	for sport, want := range cases {
		if got := sportName(sport); got != want {
			t.Errorf("sportName(%d) = %q, want %q", sport, got, want)
		}
	}
}

// TestReadBalanceReportsTheRightSideShare pins the conversion of the left-right
// split, whose high bit says which pedal the stored percentage describes.
func TestReadBalanceReportsTheRightSideShare(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		packed typedef.LeftRightBalance
		want   FITNumber
	}{
		"right side form": {typedef.LeftRightBalanceRight | 52, fitNumber(52)},
		"left side form":  {typedef.LeftRightBalance(48), fitNumber(52)},
		"absent":          {typedef.LeftRightBalanceInvalid, FITNumber{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := readBalance(tc.packed); got != tc.want {
				t.Errorf("readBalance() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
