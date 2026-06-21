package main

// paging.go holds the pure, GUI-free math for the drum panel's bar paging — kept
// out of the //go:build gui file so it compiles and is unit-tested on headless CI
// (the panel rendering itself needs Gio). barWindow decides how many whole bars
// fit at a legible cell width and which window is currently shown.

// clampInt bounds v to [lo, hi]. If hi < lo, lo wins.
func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// barWindowResult describes the visible slice of a (possibly long) beat.
type barWindowResult struct {
	ViewStart int  // absolute first step shown
	ViewSteps int  // number of steps shown
	ViewBars  int  // number of bars shown
	TotalBars int  // total bars in the beat
	MaxOffset int  // largest valid barOffset (in bars)
	Paged     bool // true when the beat does not fit in one window
}

// barWindow computes the visible window for a beat of nSteps, in bars of
// stepsPerBar, given the available pixel width and a desired per-cell stride.
// barOffset is the requested first bar; it is clamped into range. All inputs are
// defensive: zero/negative values degrade to a single visible bar, never a panic
// or a divide-by-zero.
func barWindow(nSteps, stepsPerBar, availW, targetStride, barOffset int) barWindowResult {
	if stepsPerBar <= 0 {
		stepsPerBar = 16
	}
	if nSteps <= 0 {
		nSteps = stepsPerBar
	}
	if targetStride <= 0 {
		targetStride = 1
	}
	totalBars := (nSteps + stepsPerBar - 1) / stepsPerBar

	maxCells := availW / targetStride
	viewBars := maxCells / stepsPerBar
	if viewBars < 1 {
		viewBars = 1
	}
	if viewBars > totalBars {
		viewBars = totalBars
	}
	viewSteps := viewBars * stepsPerBar
	if viewSteps > nSteps {
		viewSteps = nSteps
	}

	maxOffset := totalBars - viewBars
	if maxOffset < 0 {
		maxOffset = 0
	}
	if barOffset > maxOffset {
		barOffset = maxOffset
	}
	if barOffset < 0 {
		barOffset = 0
	}
	viewStart := barOffset * stepsPerBar
	if viewStart+viewSteps > nSteps {
		viewStart = nSteps - viewSteps
	}
	if viewStart < 0 {
		viewStart = 0
	}

	return barWindowResult{
		ViewStart: viewStart,
		ViewSteps: viewSteps,
		ViewBars:  viewBars,
		TotalBars: totalBars,
		MaxOffset: maxOffset,
		Paged:     viewSteps < nSteps,
	}
}
