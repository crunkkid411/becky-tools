package main

import "testing"

func TestBarWindow_fitsInOne(t *testing.T) {
	// 16 steps, plenty of width → one bar, not paged.
	w := barWindow(16, 16, 1000, 20, 0)
	if w.Paged {
		t.Errorf("16 steps in 1000px should not page: %+v", w)
	}
	if w.ViewSteps != 16 || w.ViewStart != 0 || w.TotalBars != 1 {
		t.Errorf("unexpected window: %+v", w)
	}
}

func TestBarWindow_pagesLongBeat(t *testing.T) {
	// 64 steps (4 bars) but only room for ~1 bar (16*20=320px window worth).
	w := barWindow(64, 16, 360, 20, 0)
	if !w.Paged {
		t.Fatalf("64 steps in a narrow panel should page: %+v", w)
	}
	if w.TotalBars != 4 {
		t.Errorf("totalBars = %d, want 4", w.TotalBars)
	}
	if w.ViewSteps%16 != 0 || w.ViewSteps == 0 || w.ViewSteps >= 64 {
		t.Errorf("viewSteps should be whole bars and < 64: %+v", w)
	}
	if w.ViewStart != 0 {
		t.Errorf("offset 0 should start at step 0, got %d", w.ViewStart)
	}
}

func TestBarWindow_offsetClampedAndWindowed(t *testing.T) {
	// 4 bars, 1 bar visible. Offset 2 → start at step 32.
	w := barWindow(64, 16, 360, 20, 2)
	if w.ViewBars != 1 {
		t.Fatalf("expected 1 visible bar for this width, got %d", w.ViewBars)
	}
	if w.MaxOffset != 3 {
		t.Errorf("maxOffset = %d, want 3", w.MaxOffset)
	}
	if w.ViewStart != 32 {
		t.Errorf("offset 2 → viewStart 32, got %d", w.ViewStart)
	}
	// Over-large offset clamps to the last window.
	w2 := barWindow(64, 16, 360, 20, 99)
	if w2.ViewStart != 48 { // last bar of 4 starts at step 48
		t.Errorf("clamped offset should show last bar (start 48), got %d", w2.ViewStart)
	}
}

func TestBarWindow_neverRunsPastEnd(t *testing.T) {
	for off := 0; off < 10; off++ {
		w := barWindow(48, 16, 400, 20, off)
		if w.ViewStart+w.ViewSteps > 48 {
			t.Errorf("offset %d ran past end: start=%d steps=%d", off, w.ViewStart, w.ViewSteps)
		}
		if w.ViewStart < 0 {
			t.Errorf("offset %d gave negative start %d", off, w.ViewStart)
		}
	}
}

func TestBarWindow_degenerateInputs(t *testing.T) {
	// Zero/negative inputs must not panic or divide by zero.
	cases := [][5]int{
		{0, 0, 0, 0, 0},
		{16, 0, 0, 0, 5},
		{-5, -1, -10, -2, -3},
		{32, 16, 0, 20, 0}, // zero width → at least one bar
	}
	for _, c := range cases {
		w := barWindow(c[0], c[1], c[2], c[3], c[4])
		if w.ViewSteps < 1 {
			t.Errorf("barWindow%v gave non-positive viewSteps %d", c, w.ViewSteps)
		}
		if w.ViewStart < 0 {
			t.Errorf("barWindow%v gave negative viewStart %d", c, w.ViewStart)
		}
	}
}

func TestClampInt(t *testing.T) {
	if clampInt(5, 0, 3) != 3 {
		t.Error("clamp high")
	}
	if clampInt(-1, 0, 3) != 0 {
		t.Error("clamp low")
	}
	if clampInt(2, 0, 3) != 2 {
		t.Error("in range")
	}
	if clampInt(5, 0, -1) != 0 {
		t.Error("hi<lo should yield lo")
	}
}
