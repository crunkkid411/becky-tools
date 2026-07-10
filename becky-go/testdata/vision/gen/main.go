//go:build ignore

// Command gen draws the becky-vision regression fixtures: 3-5 SYNTHETIC,
// programmatically-generated screenshot mockups checked into
// becky-go\testdata\vision\*.png (becky-AI-Agent-review-1.md §5 - "the
// IMG_7725 case reproduced synthetically so no personal photo needs to live
// in the repo").
//
// This is a one-off generator, not part of the build (go:build ignore keeps
// it out of `go build ./...` / `go vet ./...` / `go test ./...`). Run it
// deliberately to (re)produce the committed PNGs after editing a mockup:
//
//	go run testdata/vision/gen/main.go
//
// Everything is drawn with the Go standard library (image/draw) plus
// golang.org/x/image (already an indirect dependency of this module via
// gioui/x/exp/shiny — no new dependency added) for text (basicfont) and
// integer upscaling (NearestNeighbor.Scale). Each mockup is drawn small at
// native bitmap-font resolution, then scaled 3x so the final PNG is a
// plausible screenshot size (1920x1200) with crisp, OCR-legible text.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	xdraw "golang.org/x/image/draw"
)

// logical canvas size before the 3x upscale; final PNGs are 1920x1200.
const (
	logicalW = 640
	logicalH = 400
	scale    = 3
	lineH    = 16 // px between text baselines at logical (1x) resolution
)

var (
	colBG        = color.RGBA{18, 18, 22, 255}    // near-black terminal background
	colText      = color.RGBA{225, 225, 225, 255} // light gray terminal text
	colDim       = color.RGBA{130, 130, 130, 255} // dim/prior-output text
	colPromptBox = color.RGBA{90, 170, 255, 255}  // accent-blue prompt panel border
	colCursor    = color.RGBA{225, 225, 225, 255} // solid block cursor
	colDialogBG  = color.RGBA{240, 240, 240, 255} // Windows dialog body
	colDialogBar = color.RGBA{200, 200, 205, 255} // Windows dialog title bar
	colDialogEd  = color.RGBA{120, 120, 125, 255} // dialog border
	colErrorIco  = color.RGBA{205, 40, 40, 255}   // error icon red
	colButtonBG  = color.RGBA{225, 225, 225, 255} // dialog button face
	colButtonEd  = color.RGBA{120, 120, 120, 255} // dialog button border
	colDeskTop   = color.RGBA{20, 90, 150, 255}   // desktop wallpaper (flat blue)
	colDeskBand  = color.RGBA{15, 70, 120, 255}   // faint wallpaper band (cheap gradient)
	colTaskbar   = color.RGBA{32, 32, 35, 255}    // taskbar strip
)

func main() {
	outDir := filepath.Join("testdata", "vision")
	if _, err := os.Stat(outDir); err != nil {
		// allow running from the gen/ dir too: go run main.go
		outDir = ".."
	}
	fixtures := []struct {
		name string
		draw func() *image.RGBA
	}{
		{"terminal_prompt_waiting.png", drawTerminalPromptWaiting},
		{"terminal_idle_prompt.png", drawTerminalIdlePrompt},
		{"error_dialog.png", drawErrorDialog},
		{"empty_desktop.png", drawEmptyDesktop},
	}
	for _, f := range fixtures {
		img := f.draw()
		outPath := filepath.Join(outDir, f.name)
		if err := saveScaled(img, outPath); err != nil {
			fmt.Fprintf(os.Stderr, "generate %s: %v\n", f.name, err)
			os.Exit(1)
		}
		fmt.Println("wrote", outPath)
	}
}

// --- fixture 1: the canonical regression case (IMG_7725, reproduced synthetically) ---
//
// A dark terminal showing a Claude-Code-shaped permission prompt: readable
// on-screen text asking a yes/no question, boxed like the real app's TUI
// prompt panel. becky-AI-Agent-review-1.md acceptance criterion 2: on this
// image, becky-vision's no-flags answer must say the screen is WAITING ON A
// QUESTION / stuck on a permission prompt.
func drawTerminalPromptWaiting() *image.RGBA {
	img := newCanvas(logicalW, logicalH, colBG)
	y := 24
	drawText(img, 16, y, "$ claude", colDim)
	y += lineH
	drawText(img, 16, y, "> Reviewing changes in main.cpp...", colDim)
	y += lineH * 2

	boxTop := y - 12
	drawText(img, 24, y, "Use skill \"claude-in-chrome\"?", colText)
	y += lineH * 2
	drawText(img, 24, y, "Do you want to proceed?", colText)
	y += lineH
	drawText(img, 32, y, "1. Yes", colText)
	y += lineH
	drawText(img, 32, y, "2. Yes, and don't ask again for claude-in-chrome", colText)
	y += lineH
	drawText(img, 32, y, "3. No", colText)
	y += lineH + 6
	drawText(img, 24, y, "Esc to cancel   Tab to amend", colDim)
	boxBottom := y + 10
	strokeRect(img, image.Rect(16, boxTop, logicalW-16, boxBottom), colPromptBox, 1)
	return img
}

// --- fixture 2: contrast case - a normal idle shell prompt, nothing pending ---
//
// Must NOT read as stuck: a completed build log followed by a bare, waiting
// command prompt and a blinking-looking cursor. No question, no dialog box.
func drawTerminalIdlePrompt() *image.RGBA {
	img := newCanvas(logicalW, logicalH, colBG)
	y := 24
	lines := []string{
		"C:\\AI-2\\becky-tools> build-all-tools.bat",
		"Building becky.exe (cmd\\becky)...",
		"Building becky-vision.exe (cmd\\vision)...",
		"Building becky-ocr.exe (cmd\\ocr)...",
		"Done. Built 24 tools. Binaries in bin",
		"",
		"C:\\AI-2\\becky-tools>",
	}
	for _, l := range lines {
		drawText(img, 16, y, l, colDim)
		y += lineH
	}
	// solid block cursor right after the last empty prompt line
	fillRect(img, image.Rect(16+7*22, y-lineH-11, 16+7*22+8, y-lineH+2), colCursor)
	return img
}

// --- fixture 3: an error dialog box ---
func drawErrorDialog() *image.RGBA {
	img := newCanvas(logicalW, logicalH, color.RGBA{60, 60, 65, 255}) // dimmed desktop behind the modal
	dlg := image.Rect(90, 90, logicalW-90, logicalH-90)
	fillRect(img, dlg, colDialogBG)
	strokeRect(img, dlg, colDialogEd, 2)
	// title bar
	bar := image.Rect(dlg.Min.X, dlg.Min.Y, dlg.Max.X, dlg.Min.Y+22)
	fillRect(img, bar, colDialogBar)
	drawText(img, dlg.Min.X+8, dlg.Min.Y+16, "Error", color.RGBA{20, 20, 20, 255})
	// error icon: a red circle-ish square with a white X
	icon := image.Rect(dlg.Min.X+20, dlg.Min.Y+40, dlg.Min.X+52, dlg.Min.Y+72)
	fillRect(img, icon, colErrorIco)
	drawText(img, icon.Min.X+9, icon.Min.Y+22, "X", color.RGBA{255, 255, 255, 255})
	// message
	drawText(img, dlg.Min.X+64, dlg.Min.Y+50, "MissionControl.exe has stopped working.", color.RGBA{20, 20, 20, 255})
	drawText(img, dlg.Min.X+64, dlg.Min.Y+68, "A problem caused the application to close.", color.RGBA{20, 20, 20, 255})
	// OK button
	btn := image.Rect(dlg.Max.X-70, dlg.Max.Y-40, dlg.Max.X-16, dlg.Max.Y-14)
	fillRect(img, btn, colButtonBG)
	strokeRect(img, btn, colButtonEd, 1)
	drawText(img, btn.Min.X+18, btn.Min.Y+17, "OK", color.RGBA{20, 20, 20, 255})
	return img
}

// --- fixture 4: an empty desktop - nothing stuck, nothing waiting ---
func drawEmptyDesktop() *image.RGBA {
	img := newCanvas(logicalW, logicalH, colDeskTop)
	// a couple of cheap flat bands stand in for a gradient wallpaper
	fillRect(img, image.Rect(0, 0, logicalW, logicalH/3), colDeskBand)
	// taskbar strip, empty (no running app buttons, no dialogs, no icons)
	taskbar := image.Rect(0, logicalH-20, logicalW, logicalH)
	fillRect(img, taskbar, colTaskbar)
	return img
}

// --- drawing helpers ---------------------------------------------------------

func newCanvas(w, h int, bg color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	return img
}

func fillRect(img *image.RGBA, r image.Rectangle, c color.Color) {
	draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src)
}

// strokeRect draws a thickness-px outline of r (never fills the interior).
func strokeRect(img *image.RGBA, r image.Rectangle, c color.Color, thickness int) {
	fillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+thickness), c)
	fillRect(img, image.Rect(r.Min.X, r.Max.Y-thickness, r.Max.X, r.Max.Y), c)
	fillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+thickness, r.Max.Y), c)
	fillRect(img, image.Rect(r.Max.X-thickness, r.Min.Y, r.Max.X, r.Max.Y), c)
}

// drawText draws s with its baseline at (x, y) in basicfont's fixed 7x13 face
// (upscaled 3x at save time - see saveScaled), a bitmap font that needs no
// external font file / cgo / freetype dependency.
func drawText(img *image.RGBA, x, y int, s string, c color.Color) {
	d := &font.Drawer{
		Dst:  img,
		Src:  &image.Uniform{c},
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(s)
}

// saveScaled upscales img by `scale` with nearest-neighbor (keeps the bitmap
// font crisp instead of blurring it) and PNG-encodes the result to outPath.
func saveScaled(img *image.RGBA, outPath string) error {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx()*scale, b.Dy()*scale))
	xdraw.NearestNeighbor.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, dst)
}
