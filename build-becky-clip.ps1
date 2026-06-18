# Build-Becky-Clip.ps1 - the one-click "Build my video editor" button for Jordan.
#
# Double-clicking the Desktop shortcut (or "Build Becky Clip.bat") runs this. It:
#   1. Builds the becky-clip WINDOW (becky-clip.exe - the WebView2 GUI).
#   2. Checks that ffmpeg is available (used for preview proxies / frame export /
#      rendering; the window still opens without it, but export needs it).
#   3. Drops a "Becky Clip" icon on your Desktop.
#   4. Opens the editor.
#
# Jordan never types anything. He clicks, watches, and the window opens. becky-clip
# is the forensic transcript-based video editor: open a case folder, search (or ask
# becky), click a quote to preview, double-click to add it to the timeline, burn a
# forensic lower-third (filename + original-file timecode + date/person/location),
# then export one compilation MP4. Originals are never modified.
#
# NOTE: keep this file ASCII-only. Windows PowerShell 5.1 reads .ps1 files as the
# system ANSI codepage when there is no BOM, so a stray Unicode dash/quote becomes a
# parse error and the build window flashes shut. Plain ASCII avoids that entirely.

param([switch]$NoPause, [switch]$NoLaunch)   # switches are only for automated testing.

$ErrorActionPreference = 'Stop'
if (Get-Variable PSNativeCommandUseErrorActionPreference -Scope Global -ErrorAction SilentlyContinue) {
    $PSNativeCommandUseErrorActionPreference = $false
}

# ----- where things are -------------------------------------------------------
$Repo  = if ($env:BECKY_REPO) { $env:BECKY_REPO } else { 'X:\AI-2\becky-tools' }
$GoDir = Join-Path $Repo 'becky-go'
$BinDir = Join-Path $GoDir 'bin'
$GuiExe = Join-Path $BinDir 'becky-clip.exe'

# ----- plain-English helpers --------------------------------------------------
function Say   ($m) { Write-Host $m -ForegroundColor Gray }
function Good  ($m) { Write-Host $m -ForegroundColor Green }
function Warn  ($m) { Write-Host $m -ForegroundColor Yellow }
function Bad   ($m) { Write-Host $m -ForegroundColor Red }
function Title ($m) { Write-Host ""; Write-Host $m -ForegroundColor Cyan; Write-Host ("-" * 60) -ForegroundColor DarkCyan }
function Finish ($code) { Write-Host ""; if (-not $NoPause) { Read-Host 'Press Enter to close this window' }; exit $code }

Title "Becky Clip - building your video editor"

# ----- sanity checks ----------------------------------------------------------
if (-not (Test-Path $GoDir)) {
    Bad  "Can't find becky-tools at: $Repo"
    Say  "If your copy lives somewhere else, set BECKY_REPO to that folder and try again."
    Finish 1
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Bad  "Go isn't installed (or isn't on PATH)."
    Say  "Install Go from https://go.dev/dl/ , reopen this window, and click again."
    Finish 1
}

Push-Location $GoDir
try {
    if (-not (Test-Path $BinDir)) { New-Item -ItemType Directory -Path $BinDir | Out-Null }

    # 1) The WINDOW (pure Go + WebView2, no C compiler needed) ------------------
    Title "1/3  Building the window (search + preview + timeline + becky chat)..."
    $oldCgo = $env:CGO_ENABLED
    $env:CGO_ENABLED = '0'   # go-webview2 is pure Go; build without cgo
    & go build -tags gui -o $GuiExe .\cmd\clip
    $built = ($LASTEXITCODE -eq 0)
    $env:CGO_ENABLED = $oldCgo
    if (-not $built) {
        Bad  "The window failed to build. Nothing was changed."
        Say  "Copy the red text above to your assistant and it'll fix it."
        Finish 1
    }
    Good ("    Window built:  " + $GuiExe)

    # 2) ffmpeg check (preview proxies, frame export, and the final render) ------
    Title "2/3  Checking ffmpeg (needed to export your compilation)..."
    if (Get-Command ffmpeg -ErrorAction SilentlyContinue) {
        Good "    ffmpeg found - preview, frame export, and rendering will work."
    } else {
        Warn "    ffmpeg isn't on PATH. The window still OPENS and you can search +"
        Warn "    preview clips your browser can play, but EXPORT and frame-grab need"
        Warn "    ffmpeg. Tell your assistant to put ffmpeg on PATH."
    }

    # 3) Desktop shortcut -------------------------------------------------------
    Title "3/3  Putting a 'Becky Clip' icon on your Desktop..."
    try {
        $desktop = [Environment]::GetFolderPath('Desktop')
        $lnk = Join-Path $desktop 'Becky Clip.lnk'
        $ws = New-Object -ComObject WScript.Shell
        $sc = $ws.CreateShortcut($lnk)
        $sc.TargetPath = $GuiExe
        $sc.WorkingDirectory = $BinDir
        $sc.Description = 'Becky Clip - forensic transcript-based video editor'
        $sc.Save()
        Good ("    Shortcut ready:  " + $lnk)
    } catch {
        Warn "    Couldn't make the Desktop shortcut (not fatal). You can open it directly:"
        Warn ("    " + $GuiExe)
    }
}
finally {
    Pop-Location
}

Title "Done."
Good "Your video editor is built."
Say  "From now on, just double-click 'Becky Clip' on your Desktop."
Say  "Tip: inside the window, Open a case folder, then search or ask becky."

if (-not $NoLaunch) {
    Say "Opening it now..."
    try { Start-Process -FilePath $GuiExe -WorkingDirectory $BinDir } catch { Warn "Couldn't auto-open. Double-click the Desktop icon." }
}
Finish 0
