# Build-Becky-NLE.ps1 - the one-click "Build my video editor" button for Jordan.
#
# Double-clicking the Desktop shortcut (or "Build Becky NLE.bat") runs this. It:
#   1. Builds the NLE WINDOW            (becky-nle.exe, the Gio GUI).
#   2. Builds the GPU video sidecar     (becky-video-preview.exe, Rust+wgpu) if missing.
#   3. Drops a "Becky NLE" icon on your Desktop.
#   4. Opens the editor.
#
# Jordan never types anything. He clicks, watches, and the window opens. The NLE
# previews + scrubs video through the Rust sidecar over the becky seam.
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
$GuiExe = Join-Path $BinDir 'becky-nle.exe'
$VideoDir = Join-Path $Repo 'native\video-preview'
$VideoBuilt = Join-Path $VideoDir 'target\release\becky-video-preview.exe'
$VideoExe = Join-Path $BinDir 'becky-video-preview.exe'

# ----- plain-English helpers --------------------------------------------------
function Say   ($m) { Write-Host $m -ForegroundColor Gray }
function Good  ($m) { Write-Host $m -ForegroundColor Green }
function Warn  ($m) { Write-Host $m -ForegroundColor Yellow }
function Bad   ($m) { Write-Host $m -ForegroundColor Red }
function Title ($m) { Write-Host ""; Write-Host $m -ForegroundColor Cyan; Write-Host ("-" * 60) -ForegroundColor DarkCyan }
function Finish ($code) { Write-Host ""; if (-not $NoPause) { Read-Host 'Press Enter to close this window' }; exit $code }

Title "Becky NLE - building your video editor"

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

    # 1) The WINDOW (pure Go + Gio, no C compiler needed) -----------------------
    Title "1/3  Building the window (preview + timeline + AI box)..."
    & go build -tags gui -o $GuiExe .\cmd\becky-nle
    if ($LASTEXITCODE -ne 0) {
        Bad  "The window failed to build. Nothing was changed."
        Say  "Copy the red text above to your assistant and it'll fix it."
        Finish 1
    }
    Good "    Window built:  $GuiExe"

    # 2) The GPU video sidecar (Rust + wgpu) ------------------------------------
    Title "2/3  Building the GPU video preview (Rust)..."
    if (Test-Path $VideoBuilt) {
        Say  "    Already built; reusing it."
    } elseif (Get-Command cargo -ErrorAction SilentlyContinue) {
        Push-Location $VideoDir
        try {
            & cargo build --release
        } finally {
            Pop-Location
        }
    } else {
        Warn "    Rust/cargo isn't installed - the window will OPEN but video preview"
        Warn "    won't work until becky-video-preview is built. Install Rust from"
        Warn "    https://rustup.rs/ , then click this again."
    }
    if (Test-Path $VideoBuilt) {
        Copy-Item -Path $VideoBuilt -Destination $VideoExe -Force
        Good "    GPU preview ready:  $VideoExe"
    } else {
        Warn "    No GPU preview yet - the editor still opens; export still works via ffmpeg."
    }

    # 3) Desktop shortcut -------------------------------------------------------
    Title "3/3  Putting a 'Becky NLE' icon on your Desktop..."
    try {
        $desktop = [Environment]::GetFolderPath('Desktop')
        $lnk = Join-Path $desktop 'Becky NLE.lnk'
        $ws = New-Object -ComObject WScript.Shell
        $sc = $ws.CreateShortcut($lnk)
        $sc.TargetPath = $GuiExe
        $sc.WorkingDirectory = $BinDir   # so the window finds the sibling video sidecar
        $sc.Description = 'Becky NLE - AI-integrated video editor (preview + scrub + cut)'
        $sc.Save()
        Good "    Shortcut ready:  $lnk"
    } catch {
        Warn "    Couldn't make the Desktop shortcut (not fatal). You can open it directly:"
        Warn "    $GuiExe"
    }
}
finally {
    Pop-Location
}

Title "Done."
Good "Your video editor is built."
Say  "From now on, just double-click 'Becky NLE' on your Desktop."

if (-not $NoLaunch) {
    Say "Opening it now..."
    try { Start-Process -FilePath $GuiExe -WorkingDirectory $BinDir } catch { Warn "Couldn't auto-open. Double-click the Desktop icon." }
}
Finish 0
