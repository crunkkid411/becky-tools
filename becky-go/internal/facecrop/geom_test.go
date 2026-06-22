package facecrop

import (
	"image"
	"math"
	"testing"
)

func TestCropRect(t *testing.T) {
	tests := []struct {
		name           string
		bbox           [4]float64
		margin         float64
		frameW, frameH int
		want           image.Rectangle
	}{
		{
			// Centered 100x100 face in a 1000x1000 frame, margin 0.4 -> pad =
			// 0.4*max(100,100)=40 each side. Expanded: [400-40,400-40,500+40,500+40].
			name:   "centered margin 0.4",
			bbox:   [4]float64{400, 400, 500, 500},
			margin: 0.4, frameW: 1000, frameH: 1000,
			want: image.Rect(360, 360, 540, 540),
		},
		{
			// Face flush against the LEFT edge: x1=0. pad=40. Left clamps to 0
			// (truncated margin), right keeps the full +40. Asymmetric clamp.
			name:   "flush left edge",
			bbox:   [4]float64{0, 400, 100, 500},
			margin: 0.4, frameW: 1000, frameH: 1000,
			want: image.Rect(0, 360, 140, 540),
		},
		{
			// Face at the bottom-right corner of a 1000x1000 frame: x2=1000,
			// y2=1000. pad=40. Right/bottom clamp to W/H, top/left keep -40.
			name:   "bottom-right corner clamps to W/H",
			bbox:   [4]float64{900, 900, 1000, 1000},
			margin: 0.4, frameW: 1000, frameH: 1000,
			want: image.Rect(860, 860, 1000, 1000),
		},
		{
			// Face flush against the TOP edge: y1=0.
			name:   "flush top edge",
			bbox:   [4]float64{400, 0, 500, 100},
			margin: 0.4, frameW: 1000, frameH: 1000,
			want: image.Rect(360, 0, 540, 140),
		},
		{
			// margin 0 -> rect == bbox (rounded).
			name:   "zero margin equals bbox",
			bbox:   [4]float64{120, 240, 320, 480},
			margin: 0, frameW: 1000, frameH: 1000,
			want: image.Rect(120, 240, 320, 480),
		},
		{
			// Non-square bbox (w=100, h=300): pad = 0.4*max(100,300) = 120, applied
			// uniformly to BOTH axes (larger-side basis), then clamped.
			name:   "non-square uses larger side",
			bbox:   [4]float64{400, 300, 500, 600},
			margin: 0.4, frameW: 2000, frameH: 2000,
			want: image.Rect(280, 180, 620, 720),
		},
		{
			name:   "degenerate bbox x2<=x1 -> empty",
			bbox:   [4]float64{500, 400, 500, 500},
			margin: 0.4, frameW: 1000, frameH: 1000,
			want: image.Rectangle{},
		},
		{
			name:   "degenerate bbox y2<=y1 -> empty",
			bbox:   [4]float64{400, 500, 500, 500},
			margin: 0.4, frameW: 1000, frameH: 1000,
			want: image.Rectangle{},
		},
		{
			name:   "zero frame dims -> empty",
			bbox:   [4]float64{10, 10, 50, 50},
			margin: 0.4, frameW: 0, frameH: 0,
			want: image.Rectangle{},
		},
		{
			name:   "NaN bbox -> empty",
			bbox:   [4]float64{math.NaN(), 10, 50, 50},
			margin: 0.4, frameW: 1000, frameH: 1000,
			want: image.Rectangle{},
		},
		{
			name:   "Inf bbox -> empty",
			bbox:   [4]float64{10, 10, math.Inf(1), 50},
			margin: 0.4, frameW: 1000, frameH: 1000,
			want: image.Rectangle{},
		},
		{
			// Expanded box entirely outside the frame (a stale/garbage bbox past
			// the right edge) -> empty skip, not a 0-area sliver.
			name:   "bbox entirely past frame -> empty",
			bbox:   [4]float64{2000, 2000, 2100, 2100},
			margin: 0.4, frameW: 1000, frameH: 1000,
			want: image.Rectangle{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CropRect(tc.bbox, tc.margin, tc.frameW, tc.frameH)
			if got != tc.want {
				t.Errorf("CropRect(%v, %v, %d, %d) = %v, want %v",
					tc.bbox, tc.margin, tc.frameW, tc.frameH, got, tc.want)
			}
		})
	}
}

// TestCropRectNeverOutOfBounds asserts the result is always within [0,W]x[0,H] for a
// sweep of bbox positions including all four edges and corners (no overflow).
func TestCropRectNeverOutOfBounds(t *testing.T) {
	const W, H = 640, 480
	bounds := image.Rect(0, 0, W, H)
	positions := [][4]float64{
		{0, 0, 50, 50},         // top-left corner
		{W - 50, 0, W, 50},     // top-right
		{0, H - 50, 50, H},     // bottom-left
		{W - 50, H - 50, W, H}, // bottom-right
		{300, 0, 360, 40},      // top edge
		{300, H - 40, 360, H},  // bottom edge
		{0, 200, 40, 260},      // left edge
		{W - 40, 200, W, 260},  // right edge
		{280, 200, 360, 280},   // center
	}
	for _, bb := range positions {
		r := CropRect(bb, 0.5, W, H)
		if r.Empty() {
			continue
		}
		if !r.In(bounds) {
			t.Errorf("CropRect(%v) = %v is out of frame bounds %v", bb, r, bounds)
		}
	}
}
