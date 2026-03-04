package metrics

import (
	"math"
	"testing"
)

func TestDiffUint64(t *testing.T) {
	cases := []struct {
		name string
		curr uint64
		prev uint64
		want uint64
	}{
		{name: "normal increase", curr: 200, prev: 120, want: 80},
		{name: "same value", curr: 120, prev: 120, want: 0},
		{name: "counter reset", curr: 10, prev: 120, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := diffUint64(tc.curr, tc.prev)
			if got != tc.want {
				t.Fatalf("diffUint64(%d, %d) = %d, want %d", tc.curr, tc.prev, got, tc.want)
			}
		})
	}
}

func TestBytesToMbps(t *testing.T) {
	cases := []struct {
		name    string
		delta   uint64
		seconds float64
		want    float64
	}{
		{name: "8 mbps", delta: 1_000_000, seconds: 1, want: 8},
		{name: "fraction", delta: 125_000, seconds: 1, want: 1},
		{name: "zero elapsed", delta: 1_000_000, seconds: 0, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bytesToMbps(tc.delta, tc.seconds)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("bytesToMbps(%d, %v) = %v, want %v", tc.delta, tc.seconds, got, tc.want)
			}
		})
	}
}

func TestFloatPtr(t *testing.T) {
	got := floatPtr(12.5)
	if got == nil {
		t.Fatal("floatPtr() returned nil")
	}
	if *got != 12.5 {
		t.Fatalf("floatPtr() value = %v, want %v", *got, 12.5)
	}
}
