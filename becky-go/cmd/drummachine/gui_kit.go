//go:build gui

// gui_kit.go — KIT LOADER + SAMPLE BROWSER for becky-drummachine.
//
// This is the "last mile" wire: loads Jordan's real kits and samples so
// pad-clicks and the sequencer actually play audio through becky-daw-engine
// (which exec-reads the SamplePath from the staged machine.json).
//
// Three surfaces:
//
//  1. Kit-load row (in the top bar, added via gui.go's layoutTopBar):
//     [load folder] [load sfz] [browse] — open a picker, call
//     LoadKitFromFolder / LoadKitFromSFZ, swap the machine's Kit immutably.
//
//  2. Sample browser panel (right-side, shown when browserShowing == true):
//     Scrollable list of samples from the first existing default root,
//     cached at ~/.becky/samplelib.json. Search field filters by name/role.
//     Click a sample → assignSampleToPad (sets SamplePath + Sound on the
//     selected pad via WithPadSound).
//
//  3. Active-pad sample name in the transport readout (see padSampleName,
//     called from transportReadout in gui.go).
//
// All slow work (scan, picker dialog) runs on goroutines — the UI thread never
// blocks. degrade-never-crash throughout: a missing kit, bad scan, or
// unreadable dir is one setStatus line, never a panic or a frozen window.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"becky-go/internal/drummachine"
	"becky-go/internal/samplelib"
	"becky-go/internal/sampler"
)

// kitBrowseTimeout caps how long a folder-picker or SFZ-picker dialog may stay
// open. Five minutes is generous — after this the goroutine context cancels and
// the picker process is killed.
const kitBrowseTimeout = 5 * time.Minute

// defaultSampleRoots are checked in order; the first directory that exists
// becomes the browser root. Jordan's two known sample library locations.
var defaultSampleRoots = []string{
	`X:\music-2\SAMPLES`,
	`X:\Splice`,
}

// maxBrowserRows caps the scrollable list so Gio doesn't spend a full frame
// computing 50 000 rows. Filtering still runs over the full index.
const maxBrowserRows = 400

// browserPanelWidth is the fixed dp width of the browser side panel.
const browserPanelWidth = unit.Dp(240)

// ---- kit loading ---------------------------------------------------------------

// startLoadKitFolder opens the native folder picker on a goroutine and, on
// confirmation, calls LoadKitFromFolder and swaps the machine's Kit.
// On non-Windows the picker is skipped with a status note.
func (a *App) startLoadKitFolder() {
	if runtime.GOOS != "windows" {
		a.setStatus("kit folder picker: Windows only — try: tell becky 'load kit /path/to/kit'")
		return
	}
	go func() {
		dir, err := pickKitFolder("becky-drummachine — choose a kit folder")
		if err != nil {
			a.setStatus("kit folder picker: " + err.Error())
			return
		}
		if dir == "" {
			return // user cancelled
		}
		a.applyLoadKitFromFolder(dir)
	}()
}

// startLoadKitSFZ opens a file picker (.sfz / .dspreset) on a goroutine.
func (a *App) startLoadKitSFZ() {
	if runtime.GOOS != "windows" {
		a.setStatus("SFZ picker: Windows only — try: tell becky 'load kit /path/to/kit.sfz'")
		return
	}
	go func() {
		path, err := pickSFZOrDsPreset()
		if err != nil {
			a.setStatus("SFZ picker: " + err.Error())
			return
		}
		if path == "" {
			return // cancelled
		}
		a.applyLoadKitFromSFZ(path)
	}()
}

// applyLoadKitFromFolder calls LoadKitFromFolder and swaps the machine's Kit
// (immutable: a.machine is replaced by a.machine.WithKit). Thread-safe: may be
// called from any goroutine.
func (a *App) applyLoadKitFromFolder(dir string) {
	result, err := drummachine.LoadKitFromFolder(dir)
	if err != nil {
		a.setStatus("kit folder load: " + err.Error())
		return
	}
	a.machine = a.machine.WithKit(result.Kit)
	a.window.Invalidate()

	note := fmt.Sprintf("kit %q loaded from folder", result.Kit.Name)
	if len(result.Notes) > 0 {
		note += " — " + result.Notes[len(result.Notes)-1]
	}
	a.setStatus(note)
}

// applyLoadKitFromSFZ calls LoadKitFromSFZ and swaps the machine's Kit.
func (a *App) applyLoadKitFromSFZ(path string) {
	result, err := drummachine.LoadKitFromSFZ(path)
	if err != nil {
		a.setStatus("SFZ load: " + err.Error())
		return
	}
	a.machine = a.machine.WithKit(result.Kit)
	a.window.Invalidate()

	note := fmt.Sprintf("kit %q loaded from SFZ", result.Kit.Name)
	if len(result.Notes) > 0 {
		note += " — " + result.Notes[0]
	}
	a.setStatus(note)
}

// ---- sample browser ------------------------------------------------------------

// startScanBrowser kicks off a cached sample library scan in the background.
// Uses the first existing defaultSampleRoot. Degrade-never-crash: any error → one
// status line.
func (a *App) startScanBrowser() {
	a.mu.Lock()
	if a.browserLoading {
		a.mu.Unlock()
		return // already in progress
	}
	a.browserLoading = true
	a.mu.Unlock()

	go func() {
		root := firstExistingDir(defaultSampleRoots)
		if root == "" {
			a.mu.Lock()
			a.browserLoading = false
			a.mu.Unlock()
			a.setStatus("sample browser: no library found at X:\\music-2\\SAMPLES or X:\\Splice")
			return
		}
		idx, err := samplelib.ScanWithCache(root, samplelib.PersistedIndexOptions{})
		a.mu.Lock()
		a.browserLoading = false
		if err != nil {
			a.mu.Unlock()
			a.setStatus("sample browser scan: " + err.Error())
			return
		}
		a.browserSamples = idx.Samples
		a.mu.Unlock()

		a.applyBrowserFilter("")
		a.setStatus(fmt.Sprintf("sample browser: %d samples from %s", len(idx.Samples), root))
	}()
}

// applyBrowserFilter filters browserSamples by query and stores the result in
// browserFiltered (guarded by mu). Safe to call from any goroutine.
func (a *App) applyBrowserFilter(query string) {
	a.mu.Lock()
	all := a.browserSamples
	a.mu.Unlock()

	q := strings.ToLower(strings.TrimSpace(query))
	var out []samplelib.Sample
	for _, s := range all {
		if q == "" ||
			strings.Contains(strings.ToLower(s.Name), q) ||
			strings.Contains(strings.ToLower(s.Role), q) {
			out = append(out, s)
			if len(out) >= maxBrowserRows {
				break
			}
		}
	}

	a.mu.Lock()
	a.browserFiltered = out
	a.mu.Unlock()
	a.window.Invalidate()
}

// assignSampleToPad sets the selected pad's SamplePath to s.Path and wraps a
// minimal sampler.Sound (one-shot, one layer, one variant). Returns a NEW machine
// (immutable model). Thread-safe: called from the UI goroutine via browser list click.
func (a *App) assignSampleToPad(s samplelib.Sample) {
	pad := a.selected

	// Build a minimal one-shot Sound for the sample.
	snd := sampler.NewDrumSound(sampleBaseName(s.Path))
	snd.OneShot = true
	variant := sampler.Variant{
		SamplePath:     s.Path,
		PitchKeycenter: sampler.DefaultKeycenter,
	}
	snd.Layers = []sampler.Layer{{
		VelLo:      1,
		VelHi:      127,
		RoundRobin: []sampler.Variant{variant},
	}}
	norm := snd.Normalize()

	next, err := a.machine.WithPadSound(pad, s.Path, &norm)
	if err != nil {
		a.setStatus(fmt.Sprintf("assign sample: %v", err))
		return
	}
	a.machine = next
	a.window.Invalidate()
	a.setStatus(fmt.Sprintf("pad %d ← %s", pad+1, sampleBaseName(s.Path)))
	// Audition the newly-assigned sample immediately.
	a.auditionPad(pad)
}

// ---- kit controls layout -------------------------------------------------------

// layoutKitButtons renders the [load folder] [load sfz] [browse] buttons inline
// with the top bar. Called from layoutTopBar in gui.go.
func (a *App) layoutKitButtons(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.kitFolderBtn, a.icons.folder, "kit folder", colElecBlue)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.kitSFZBtn, a.icons.run, "load sfz", colElecBlue)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := "browse"
			if a.browserShowing {
				lbl = "hide"
			}
			return a.iconBtn(gtx, &a.browserToggle, a.icons.run, lbl, colNeonPink)
		}),
	)
}

// ---- browser panel layout ------------------------------------------------------

// layoutBrowserPanel draws the collapsible sample browser side panel.
// Called from layoutFrame in gui.go when browserShowing is true.
func (a *App) layoutBrowserPanel(gtx layout.Context) layout.Dimensions {
	w := gtx.Dp(browserPanelWidth)
	gtx.Constraints.Min.X = w
	gtx.Constraints.Max.X = w

	return borderBox(gtx, colGridLine, func(gtx layout.Context) layout.Dimensions {
		return widgetBg(gtx, colPanelBg, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						hdr := material.Body2(a.th, "sample browser")
						hdr.Color = colNeonGreen
						return hdr.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return a.layoutBrowserSearch(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return a.layoutBrowserList(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return a.layoutBrowserFooter(gtx)
					}),
				)
			})
		})
	})
}

// layoutBrowserSearch draws the search box inside the browser panel.
func (a *App) layoutBrowserSearch(gtx layout.Context) layout.Dimensions {
	return fieldBox(gtx, func(gtx layout.Context) layout.Dimensions {
		ed := material.Editor(a.th, &a.browserSearch, "search…")
		ed.Color = colText
		ed.HintColor = colTextDim
		ed.TextSize = unit.Sp(12)
		return ed.Layout(gtx)
	})
}

// layoutBrowserList draws the scrollable list of filtered samples.
func (a *App) layoutBrowserList(gtx layout.Context) layout.Dimensions {
	a.mu.Lock()
	samples := a.browserFiltered
	loading := a.browserLoading
	a.mu.Unlock()

	if loading {
		lbl := material.Body2(a.th, "scanning…")
		lbl.Color = colTextDim
		return lbl.Layout(gtx)
	}
	if len(samples) == 0 {
		lbl := material.Body2(a.th, "no samples — tap scan")
		lbl.Color = colTextDim
		return lbl.Layout(gtx)
	}

	// Ensure the clickable slice has room for all rows.
	a.growBrowserBtns(len(samples))

	return material.List(a.th, &a.browserList).Layout(gtx, len(samples), func(gtx layout.Context, i int) layout.Dimensions {
		s := samples[i]
		btn := &a.browserBtns[i]
		if btn.Clicked(gtx) {
			a.assignSampleToPad(s)
		}
		rowBg := colCanvasBg
		if btn.Hovered() {
			rowBg = colHeaderBg
		}
		return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return widgetBg(gtx, rowBg, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(3)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							name := sampleBaseName(s.Path)
							lbl := material.Body2(a.th, name)
							lbl.Color = colText
							lbl.TextSize = unit.Sp(11)
							return lbl.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							role := s.Role
							if role == "" || role == "unknown" {
								role = "?"
							}
							lbl := material.Caption(a.th, role)
							lbl.Color = colTextDim
							return lbl.Layout(gtx)
						}),
					)
				})
			})
		})
	})
}

// layoutBrowserFooter draws a Scan button at the bottom of the browser panel,
// showing the current sample count if any are loaded.
func (a *App) layoutBrowserFooter(gtx layout.Context) layout.Dimensions {
	a.mu.Lock()
	n := len(a.browserSamples)
	a.mu.Unlock()
	label := "scan library"
	if n > 0 {
		label = fmt.Sprintf("rescan (%d)", n)
	}
	return a.iconBtn(gtx, &a.browserScanBtn, a.icons.folder, label, colNeonGreen)
}

// growBrowserBtns ensures browserBtns has at least n entries. Called from
// layoutBrowserList before rendering the list (UI goroutine only).
func (a *App) growBrowserBtns(n int) {
	if len(a.browserBtns) >= n {
		return
	}
	extra := make([]widget.Clickable, n-len(a.browserBtns))
	a.browserBtns = append(a.browserBtns, extra...)
}

// handleKitInput processes kit-load and browser button clicks. Called from
// handleInput in gui.go each frame before layout.
func (a *App) handleKitInput(gtx layout.Context) {
	if a.kitFolderBtn.Clicked(gtx) {
		a.startLoadKitFolder()
	}
	if a.kitSFZBtn.Clicked(gtx) {
		a.startLoadKitSFZ()
	}
	if a.browserToggle.Clicked(gtx) {
		a.browserShowing = !a.browserShowing
		if a.browserShowing {
			// Auto-scan if we have no samples yet.
			a.mu.Lock()
			noSamples := len(a.browserSamples) == 0
			a.mu.Unlock()
			if noSamples {
				a.startScanBrowser()
			}
		}
	}
	if a.browserScanBtn.Clicked(gtx) {
		// Reset browserSamples so startScanBrowser always re-scans.
		a.mu.Lock()
		a.browserSamples = nil
		a.browserFiltered = nil
		a.mu.Unlock()
		a.startScanBrowser()
	}

	// Handle search-field text changes.
	for {
		_, ok := a.browserSearch.Update(gtx)
		if !ok {
			break
		}
		a.applyBrowserFilter(a.browserSearch.Text())
	}
}

// padSampleName returns a short display name for the given pad's current sample,
// or "no sample" if none is loaded. Used in the transport readout.
func (a *App) padSampleName(pad int) string {
	m := a.machine
	if m == nil || pad < 0 || pad >= len(m.Kit.Pads) {
		return "no sample"
	}
	p := m.Kit.Pads[pad]
	if p.SamplePath != "" {
		return sampleBaseName(p.SamplePath)
	}
	if p.Sound != nil && p.Sound.Name != "" {
		return p.Sound.Name
	}
	return "no sample"
}

// ---- Windows pickers -----------------------------------------------------------

// pickKitFolder shows the Windows FolderBrowserDialog via PowerShell.
// Returns the chosen directory path, "" if cancelled, or an error.
func pickKitFolder(title string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kitBrowseTimeout)
	defer cancel()
	script := `
Add-Type -AssemblyName System.Windows.Forms | Out-Null
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = '` + strings.ReplaceAll(title, "'", "") + `'
$d.ShowNewFolderButton = $false
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.SelectedPath }
`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// pickSFZOrDsPreset shows the Windows OpenFileDialog for .sfz/.dspreset files.
// Returns the chosen file path, "" if cancelled, or an error.
func pickSFZOrDsPreset() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kitBrowseTimeout)
	defer cancel()
	const script = `
Add-Type -AssemblyName System.Windows.Forms | Out-Null
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = 'becky-drummachine - load a kit'
$d.Filter = 'Kit files (*.sfz;*.dspreset)|*.sfz;*.dspreset|All files (*.*)|*.*'
$d.CheckFileExists = $true
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.FileName }
`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ---- helpers -------------------------------------------------------------------

// firstExistingDir returns the first path in dirs that is an existing directory,
// or "" if none exist on this machine.
func firstExistingDir(dirs []string) string {
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
	}
	return ""
}

// sampleBaseName returns the file name without its final extension (e.g.
// "kick_808_dry.wav" → "kick_808_dry"). Used for display labels.
func sampleBaseName(path string) string {
	base := filepath.Base(path)
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return base[:i]
	}
	return base
}
