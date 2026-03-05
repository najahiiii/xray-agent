package agent

import "testing"

func TestNormalizeStatsDeltas(t *testing.T) {
	a := &Agent{
		statsSnapshot: map[string][2]int64{},
	}

	first := a.normalizeStatsDeltas(map[string][2]int64{
		"User@Example.com": {100, 200},
	})
	if got := first["User@Example.com"]; got != [2]int64{0, 0} {
		t.Fatalf("first sample should be warmup delta 0, got %+v", got)
	}

	second := a.normalizeStatsDeltas(map[string][2]int64{
		"user@example.com": {150, 260},
	})
	if got := second["user@example.com"]; got != [2]int64{50, 60} {
		t.Fatalf("second sample should be incremental delta, got %+v", got)
	}

	afterReset := a.normalizeStatsDeltas(map[string][2]int64{
		"user@example.com": {20, 5},
	})
	if got := afterReset["user@example.com"]; got != [2]int64{20, 5} {
		t.Fatalf("counter reset should use current absolute value, got %+v", got)
	}
}

func TestUsageCounterDelta(t *testing.T) {
	cases := []struct {
		name string
		prev int64
		curr int64
		want int64
	}{
		{name: "incremental", prev: 10, curr: 25, want: 15},
		{name: "same", prev: 10, curr: 10, want: 0},
		{name: "counter reset", prev: 80, curr: 12, want: 12},
		{name: "invalid negative current", prev: 5, curr: -1, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := usageCounterDelta(tc.prev, tc.curr)
			if got != tc.want {
				t.Fatalf("usageCounterDelta(%d, %d) = %d, want %d", tc.prev, tc.curr, got, tc.want)
			}
		})
	}
}
