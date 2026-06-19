# Build-Becky-Drum.ps1 - the one-click "Build my drum machine" button for Jordan.
#
# Double-clicking the Desktop shortcut (or "Build Becky Drum.bat") runs this. It:
#   1. Builds the drum-machine WINDOW  (becky-drummachine.exe, the GUI).
#   2. Builds the SOUND engine          (becky-daw-engine.exe, real audio).
#   3. Drops a "Becky Drum Machine" icon on your Desktop.
#   4. Opens the drum machine.
#
# Jordan never types anything. He clicks, watches, and the window opens. No local
# Claude agent involved - this just compiles what the cloud already wrote.
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
$GuiExe = Join-Path $BinDir 'becky-drummachine.exe'
$AudioExe = Join-Path $BinDir 'becky-daw-engine.exe'

# ----- plain-English helpers --------------------------------------------------
function Say   ($m) { Write-Host $m -ForegroundColor Gray }
function Good  ($m) { Write-Host $m -ForegroundColor Green }
function Warn  ($m) { Write-Host $m -ForegroundColor Yellow }
function Bad   ($m) { Write-Host $m -ForegroundColor Red }
function Title ($m) { Write-Host ""; Write-Host $m -ForegroundColor Cyan; Write-Host ("-" * 60) -ForegroundColor DarkCyan }
function Finish ($code) { Write-Host ""; if (-not $NoPause) { Read-Host 'Press Enter to close this window' }; exit $code }

Title "Becky Drum Machine - building your instrument"

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
    Title "1/3  Building the window (16 pads + AI box)..."
    & go build -tags gui -o $GuiExe .\cmd\drummachine
    if ($LASTEXITCODE -ne 0) {
        Bad  "The window failed to build. Nothing was changed."
        Say  "Copy the red text above to your assistant and it'll fix it."
        Finish 1
    }
    Good "    Window built:  $GuiExe"

    # 2) The SOUND engine (needs a C compiler for real-time audio) --------------
    Title "2/3  Building the sound engine (real audio)..."
    $oldCC = $env:CC
    $mingw = 'C:\msys64\mingw64\bin\gcc.exe'
    if (Test-Path $mingw) { $env:CC = $mingw }
    $env:CGO_ENABLED = '1'
    & go build -tags audio -o $AudioExe .\cmd\daw-engine
    $audioOk = ($LASTEXITCODE -eq 0)
    $env:CC = $oldCC
    if ($audioOk) {
        Good "    Sound engine built:  $AudioExe"
    } else {
        Warn "    Sound engine didn't build - the window will still OPEN, but pads"
        Warn "    won't make sound until this is fixed. Usually means the C compiler"
        Warn "    (MSYS2/mingw at $mingw) isn't installed. Tell your assistant."
    }

    # 3) Desktop shortcut -------------------------------------------------------
    Title "3/3  Putting a 'Becky Drum Machine' icon on your Desktop..."
    try {
        $desktop = [Environment]::GetFolderPath('Desktop')
        $lnk = Join-Path $desktop 'Becky Drum Machine.lnk'
        $ws = New-Object -ComObject WScript.Shell
        $sc = $ws.CreateShortcut($lnk)
        $sc.TargetPath = $GuiExe
        $sc.WorkingDirectory = $BinDir   # so the window finds the sibling sound engine
        $sc.Description = 'Becky Drum Machine - AI-first 16-pad groovebox'
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
Good "Your drum machine is built."
Say  "From now on, just double-click 'Becky Drum Machine' on your Desktop."

if (-not $NoLaunch) {
    Say "Opening it now..."
    try { Start-Process -FilePath $GuiExe -WorkingDirectory $BinDir } catch { Warn "Couldn't auto-open. Double-click the Desktop icon." }
}
Finish 0
