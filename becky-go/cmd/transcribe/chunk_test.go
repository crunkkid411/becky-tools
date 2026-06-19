package main

import "testing"

// TestResolveChunkSeconds verifies --no-chunk forces a single whole-file pass
// (0) and otherwise the requested window size passes through unchanged.
func TestResolveChunkSeconds(t *testing.T) {
	tests := []struct {
		name    string
		chunk   float64
		noChunk bool
		want    float64
	}{
		{"default windowing", 900, false, 900},
		{"no-chunk overrides default", 900, true, 0},
		{"no-chunk overrides custom", 10, true, 0},
		{"explicit zero stays zero", 0, false, 0},
		{"custom window passes through", 30, false, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveChunkSeconds(tt.chunk, tt.noChunk); got != tt.want {
				t.Fatalf("resolveChunkSeconds(%v, %v) = %v; want %v", tt.chunk, tt.noChunk, got, tt.want)
			}
		})
	}
}

// TestWindowCount verifies the window-geometry math matches the helper: a short
// file or a non-positive step is one window; longer files split into ceil(d/step)
// windows. The verbose log relies on this matching the Python decode loop.
func TestWindowCount(t *testing.T) {
	tests := []struct {
		name     string
		duration float64
		chunk    float64
		want     int
	}{
		{"short file single window", 50, 900, 1},
		{"exact one window", 900, 900, 1},
		{"zero step single window", 50, 0, 1},
		{"negative step single window", 50, -5, 1},
		{"just over one window", 901, 900, 2},
		{"two full windows", 1800, 900, 2},
		{"two windows plus tail", 1801, 900, 3},
		// The live-proof case: a 50s clip at --chunk-seconds 10 -> 5 windows.
		{"50s at 10s windows", 50, 10, 5},
		{"55s at 10s windows", 55, 10, 6},
		{"four hours at 15min windows", 4 * 3600, 900, 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := windowCount(tt.duration, tt.chunk); got != tt.want {
				t.Fatalf("windowCount(%v, %v) = %d; want %d", tt.duration, tt.chunk, got, tt.want)
			}
		})
	}
}
