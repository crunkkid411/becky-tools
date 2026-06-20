package audiotrack

// peaks.go generates the waveform overview the UI actually draws: for a target pixel
// width N, the source frames are bucketed into N columns and each column reports its
// MIN and MAX sample value. Drawing a vertical line from min to max per column is the
// standard, fast waveform render (this is what Audacity/Reaper/Cubase draw at zoom).
//
// Pure Go, deterministic, degrade-never-crash: zero/negative widths and empty audio
// return an empty Peaks; a region pointing past its clip is bounded safely.

// Peak is one column of a waveform overview: the minimum and maximum sample value
// (each in [-1, 1]) of all frames that fall in this column. Abs (the larger
// magnitude of |Min|,|Max|) is precomputed for callers that draw a simple bar.
type Peak struct {
	Min float32
	Max float32
	Abs float32
}

// Peaks is a fixed-width waveform overview plus the bucketing it was built with, so
// the UI can map a pixel column back to a frame range (for click-to-seek).
type Peaks struct {
	// Width is the number of columns (== len(Columns)).
	Width int
	// FramesPerBucket is how many source frames each column summarizes (>= 1). The
	// last column may cover fewer frames; this is the nominal stride.
	FramesPerBucket float64
	// Columns is the per-pixel min/max overview, left to right.
	Columns []Peak
}

// FrameAtColumn returns the approximate source/timeline frame at the left edge of
// column i (i clamped to [0, Width-1]); useful for click-to-seek on the overview.
func (pk Peaks) FrameAtColumn(i int) int {
	if pk.Width <= 0 {
		return 0
	}
	if i < 0 {
		i = 0
	}
	if i >= pk.Width {
		i = pk.Width - 1
	}
	return int(float64(i) * pk.FramesPerBucket)
}

// BuildClipPeaks builds an N-column waveform overview for a whole Clip. Channels are
// summed to mono first (a stereo file shows one waveform), matching how a DAW draws a
// collapsed overview. width <= 0 or an empty clip yields an empty Peaks.
func BuildClipPeaks(c *Clip, width int) Peaks {
	if c == nil {
		return Peaks{}
	}
	return buildPeaks(c.Samples, c.Channels, 0, c.Frames(), width)
}

// BuildRegionPeaks builds an N-column overview of just the source window a Region
// plays ([SourceIn, SourceOut)). This is what the UI draws inside a region block. The
// region's gain/fades are NOT applied here (the overview shows the source material;
// use BuildRegionPeaksShaped for the post-fader shape). width <= 0 / nil clip / empty
// window -> empty Peaks.
func BuildRegionPeaks(r Region, width int) Peaks {
	if r.Clip == nil {
		return Peaks{}
	}
	r = r.Normalize()
	return buildPeaks(r.Clip.Samples, r.Clip.Channels, r.SourceIn, r.SourceOut, width)
}

// BuildRegionPeaksShaped is like BuildRegionPeaks but applies the region's gain and
// fade envelope to each frame before bucketing, so the overview shows exactly what
// the mixdown will render for this region (the "post-fader" waveform). Useful when the
// UI wants the visible waveform to reflect a fade the user just drew.
func BuildRegionPeaksShaped(r Region, width int) Peaks {
	if r.Clip == nil {
		return Peaks{}
	}
	r = r.Normalize()
	ch := r.Clip.Channels
	if ch <= 0 || width <= 0 {
		return Peaks{}
	}
	n := r.LenFrames()
	if n <= 0 {
		return Peaks{}
	}
	// Materialize the shaped mono frames, then bucket them with the shared helper.
	mono := make([]float32, n)
	src := r.Clip.Samples
	for i := 0; i < n; i++ {
		frame := r.SourceIn + i
		var sum float32
		base := frame * ch
		for cI := 0; cI < ch; cI++ {
			idx := base + cI
			if idx >= 0 && idx < len(src) {
				sum += src[idx]
			}
		}
		mono[i] = (sum / float32(ch)) * float32(r.gainAt(i))
	}
	return buildPeaks(mono, 1, 0, n, width)
}

// buildPeaks is the shared bucketer. It summarizes frames [start, end) of an
// interleaved buffer (channels averaged to mono) into `width` min/max columns.
// Robust to out-of-range windows (clamped) and to width > frame-count (some columns
// then cover a single frame; none are left uninitialized).
func buildPeaks(samples []float32, channels, start, end, width int) Peaks {
	if channels <= 0 || width <= 0 {
		return Peaks{}
	}
	totalFrames := len(samples) / channels
	if start < 0 {
		start = 0
	}
	if end > totalFrames {
		end = totalFrames
	}
	n := end - start
	if n <= 0 {
		return Peaks{}
	}
	cols := make([]Peak, width)
	framesPerBucket := float64(n) / float64(width)

	for col := 0; col < width; col++ {
		// Frame range for this column: [bStart, bEnd). Use the float stride so the
		// remainder spreads evenly across columns; no column is empty when width <= n.
		bStart := start + int(float64(col)*framesPerBucket)
		bEnd := start + int(float64(col+1)*framesPerBucket)
		if col == width-1 {
			bEnd = end // last column absorbs any rounding remainder
		}
		if bEnd <= bStart {
			bEnd = bStart + 1 // width > n: ensure each column reads >= 1 frame
		}
		if bEnd > end {
			bEnd = end
		}
		mn, mx := minMaxMono(samples, channels, bStart, bEnd)
		a := mx
		if -mn > a {
			a = -mn
		}
		cols[col] = Peak{Min: mn, Max: mx, Abs: a}
	}
	return Peaks{Width: width, FramesPerBucket: framesPerBucket, Columns: cols}
}

// minMaxMono returns the min and max mono (channel-averaged) sample value over frames
// [start, end) of an interleaved buffer. Returns (0,0) for an empty range.
func minMaxMono(samples []float32, channels, start, end int) (min, max float32) {
	first := true
	for f := start; f < end; f++ {
		var sum float32
		base := f * channels
		for c := 0; c < channels; c++ {
			idx := base + c
			if idx >= 0 && idx < len(samples) {
				sum += samples[idx]
			}
		}
		v := sum / float32(channels)
		if first {
			min, max = v, v
			first = false
			continue
		}
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return min, max
}
