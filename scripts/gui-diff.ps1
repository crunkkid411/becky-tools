# gui-diff.ps1 - screenshot every becky GUI and put them side by side.
#
# WHY THIS EXISTS (Jordan, 2026-07-20): "Vision is CHEAP, FREE, and FAST. 16
# hours of work did not identify the things you just pointed out in less than a
# minute by simply comparing 2 screenshots; make sure this is protocol, not
# prose."
#
# He is right, and the failure is specific: an agent that reads code convinces
# itself the layout is fine. One screenshot of becky-review-3 next to
# becky-review-native showed, in seconds, that panels overlapped three ways,
# every panel heading was hidden under the menu bar, filenames were sliced
# mid-character, and the timeline was drawn underneath the side panels. None of
# that is visible in source.
#
# So: NEVER claim a becky GUI works without running this and LOOKING at the PNGs
# it produces. Not "it compiles". Not "the code looks right". Look at it.
#
#   pwsh -File X:\AI-2\becky-tools\scripts\gui-diff.ps1
#   pwsh -File ...\gui-diff.ps1 -Launch      # start the apps first
#
# Writes PNGs to -OutDir and prints their paths. Read them with your own eyes.

param(
    [string]$OutDir = "$env:TEMP\becky-gui-diff",
    [switch]$Launch,
    [int]$SettleSeconds = 7
)

$ErrorActionPreference = 'Continue'
Add-Type -AssemblyName System.Windows.Forms, System.Drawing
Add-Type @"
using System;using System.Runtime.InteropServices;
public class GD{
 [DllImport("user32.dll")]public static extern bool GetWindowRect(IntPtr h,out R r);
 [DllImport("user32.dll")]public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")]public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")]public static extern bool ShowWindow(IntPtr h,int c);
 [DllImport("user32.dll")]public static extern bool IsIconic(IntPtr h);
 public struct R{public int L,T,Rt,B;}
}
"@

if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir -Force | Out-Null }

# Every becky GUI worth comparing. becky-review-native is the REFERENCE: Jordan
# iterated on it across ten feedback documents, so where the two disagree, it is
# usually the other one that is wrong.
$apps = @(
    @{ Name = 'becky-review-native'; Proc = 'BeckyReviewNative'
       Exe  = 'X:\AI-2\becky-tools\gui\BeckyReviewNative\bin\Release\net8.0-windows\BeckyReviewNative.exe'
       Note = 'REFERENCE - ten rounds of Jordan feedback live in this layout' }
    @{ Name = 'becky-review-3'; Proc = 'becky-review'
       Exe  = 'X:\AI-2\becky-tools\native\becky-review\becky-review.exe'
       Note = 'native C++/ImGui' }
    @{ Name = 'becky-timeline'; Proc = 'becky-timeline'
       Exe  = 'X:\AI-2\becky-tools\native\becky-timeline\becky-timeline.exe'
       Note = 'the ONE working native component the others should reuse' }
)

if ($Launch) {
    foreach ($a in $apps) {
        if (-not (Test-Path $a.Exe)) { Write-Host "SKIP (not built): $($a.Name)"; continue }
        if (Get-Process -Name $a.Proc -ErrorAction SilentlyContinue) { continue }
        Start-Process -FilePath $a.Exe -WindowStyle Normal | Out-Null
    }
    Start-Sleep -Seconds $SettleSeconds
}

# An mpv child left over from a KILLED parent floats a black rectangle across
# whatever is underneath and reads as a layout bug in the screenshot. Say so
# rather than letting it be misdiagnosed (it cost a debugging round already).
$becky = @(Get-Process -Name 'becky-review' -ErrorAction SilentlyContinue)
$mpv = @(Get-Process -Name 'mpv' -ErrorAction SilentlyContinue)
if ($mpv.Count -gt $becky.Count) {
    Write-Host "WARNING: $($mpv.Count) mpv processes but only $($becky.Count) becky-review - an ORPHANED mpv may be painting over a window in these shots." -ForegroundColor Yellow
}

$shots = @()
foreach ($a in $apps) {
    $p = Get-Process -Name $a.Proc -ErrorAction SilentlyContinue |
         Where-Object { $_.MainWindowTitle -ne '' } | Select-Object -First 1
    if (-not $p) { Write-Host "not running: $($a.Name)"; continue }

    $h = $p.MainWindowHandle

    # CLOSE every other becky window before shooting this one. CopyFromScreen
    # grabs whatever is on the glass, so another app on top silently produces a
    # screenshot of the WRONG APP under the right filename.
    foreach ($other in $apps) {
        if ($other.Proc -eq $a.Proc) { continue }
        Get-Process -Name $other.Proc -ErrorAction SilentlyContinue |
            ForEach-Object { $_.CloseMainWindow() | Out-Null; Start-Sleep -Milliseconds 400
                             if (-not $_.HasExited) { $_.Kill() } }
    }
    Start-Sleep -Milliseconds 600
    # SW_SHOW(5) keeps a maximized window maximized; SW_RESTORE(9) would un-maximize it
    # and capture a shrunk, falsely-"cramped" window. Only restore if actually minimized.
    if ([GD]::IsIconic($h)) { [void][GD]::ShowWindow($h, 9) } else { [void][GD]::ShowWindow($h, 5) }
    [void][GD]::SetForegroundWindow($h)
    Start-Sleep -Milliseconds 1200

    # Prove we actually got the foreground before trusting the pixels.
    if ([GD]::GetForegroundWindow() -ne $h) {
        Write-Host "WARNING: $($a.Name) did not reach the foreground - its shot may show another window" -ForegroundColor Yellow
    }

    $r = New-Object GD+R; [void][GD]::GetWindowRect($h, [ref]$r)
    $w = $r.Rt - $r.L; $ht = $r.B - $r.T
    if ($w -le 0 -or $ht -le 0) { Write-Host "bad rect: $($a.Name)"; continue }

    $out = Join-Path $OutDir "$($a.Name).png"
    $bmp = New-Object Drawing.Bitmap $w, $ht
    $g = [Drawing.Graphics]::FromImage($bmp)
    $g.CopyFromScreen($r.L, $r.T, 0, 0, $bmp.Size)
    $bmp.Save($out, [Drawing.Imaging.ImageFormat]::Png)
    $g.Dispose(); $bmp.Dispose()

    $shots += [pscustomobject]@{ name = $a.Name; png = $out; size = "${w}x${ht}"; note = $a.Note }
}

Write-Host ''
Write-Host 'LOOK AT THESE. Do not claim a GUI works without opening them:' -ForegroundColor Cyan
foreach ($s in $shots) { Write-Host ("  {0,-22} {1,-10} {2}" -f $s.name, $s.size, $s.png) }
Write-Host ''
Write-Host 'What comparing them has already caught, in under a minute each:' -ForegroundColor Cyan
@(
  'panels overlapping (a full-width bar drawn under full-height side panels)'
  'panel headings hidden behind the main menu bar (every panel starting at y=0)'
  'text sliced mid-character by a panel too narrow for its own content'
  'a timeline with no ruler labels, no clip blocks and no empty-state text'
  'controls at default size next to the reference app''s large ones'
  'a black rectangle from an ORPHANED mpv, mistaken for a layout bug'
) | ForEach-Object { Write-Host "  - $_" }

$shots | ConvertTo-Json -Depth 4 | Set-Content (Join-Path $OutDir 'gui-diff.json') -Encoding utf8
if (-not $shots) { exit 1 }
