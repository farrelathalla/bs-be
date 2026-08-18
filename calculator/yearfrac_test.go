package calculator

import (
	"testing"
	"time"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad date %q: %v", s, err)
	}
	return d
}

// Expected values are dateutil.relativedelta(end, start).years*12 + .months,
// which is what installment_software/calculator.py uses for 30/360.
func TestFullMonthsBetween(t *testing.T) {
	tests := []struct {
		start, end string
		want       int
	}{
		// A month is only counted once it has actually completed.
		{"2025-12-31", "2026-06-01", 5},
		{"2025-12-31", "2026-06-30", 6},
		{"2025-12-31", "2026-07-01", 6},
		{"2025-01-01", "2025-12-31", 11},
		{"2025-01-01", "2026-01-01", 12},

		// End-of-month clamping: the target month simply has fewer days, so
		// the month still counts even though 28 < 31.
		{"2025-01-31", "2025-02-28", 1},
		{"2025-01-31", "2026-02-28", 13},
		{"2024-02-29", "2026-02-28", 24},
		{"2025-11-30", "2026-02-28", 3},

		// Leap day handling
		{"2024-02-29", "2025-02-28", 12},
		{"2024-02-29", "2024-03-29", 1},

		// Reversed intervals truncate toward zero
		{"2025-11-30", "2025-11-29", 0},
		{"2025-12-31", "2025-11-29", -1},
		{"2025-12-31", "2025-06-15", -6},
		{"2025-12-01", "2025-06-15", -5},

		{"2025-06-15", "2025-06-15", 0},
	}

	for _, tt := range tests {
		got := fullMonthsBetween(day(t, tt.start), day(t, tt.end))
		if got != tt.want {
			t.Errorf("fullMonthsBetween(%s, %s) = %d, want %d", tt.start, tt.end, got, tt.want)
		}
	}
}

func TestYearFraction(t *testing.T) {
	start, end := day(t, "2025-12-31"), day(t, "2026-06-01")

	tests := []struct {
		convention string
		want       float64
	}{
		{"30/360", 5.0 / 12.0},   // five whole months, not six
		{"ACT/360", 152.0 / 360}, // 2025-12-31 → 2026-06-01 is 152 days
		{"ACT/365", 152.0 / 365},
		{"", 5.0 / 12.0}, // unrecognised falls back to 30/360
	}

	for _, tt := range tests {
		got := YearFraction(start, end, tt.convention)
		if diff := got - tt.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("YearFraction(%q) = %v, want %v", tt.convention, got, tt.want)
		}
	}
}

func TestAddMonthsClampedDoesNotOverflow(t *testing.T) {
	// time.AddDate would give 2025-03-03 here.
	if got := addMonthsClamped(day(t, "2025-01-31"), 1); !got.Equal(day(t, "2025-02-28")) {
		t.Errorf("2025-01-31 +1 month = %s, want 2025-02-28", got.Format("2006-01-02"))
	}
	if got := addMonthsClamped(day(t, "2025-03-31"), -1); !got.Equal(day(t, "2025-02-28")) {
		t.Errorf("2025-03-31 -1 month = %s, want 2025-02-28", got.Format("2006-01-02"))
	}
	// Crossing a year boundary backwards
	if got := addMonthsClamped(day(t, "2025-01-15"), -2); !got.Equal(day(t, "2024-11-15")) {
		t.Errorf("2025-01-15 -2 months = %s, want 2024-11-15", got.Format("2006-01-02"))
	}
}
