# winshot.ps1 - find a window by title substring, optionally maximize, capture just
# that window's rect to a PNG. Throwaway GUI-test harness; not referenced by project code.
# Usage: pwsh winshot.ps1 -Title "Shotcut" -Out win.png [-Maximize] [-Activate]
param(
  [string]$Title = "Shotcut",
  [string]$Out = "win.png",
  [switch]$Maximize,
  [switch]$Activate
)
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
Add-Type @"
using System;
using System.Runtime.InteropServices;
public class W {
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int n);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
}
"@
$p = Get-Process | Where-Object { $_.MainWindowTitle -like "*$Title*" -and $_.MainWindowHandle -ne 0 } | Select-Object -First 1
if (-not $p) { Write-Output "NO WINDOW matching *$Title*"; exit 1 }
$h = $p.MainWindowHandle
if ($Maximize) { [W]::ShowWindow($h, 3) | Out-Null; Start-Sleep -Milliseconds 500 }  # SW_MAXIMIZE
if ($Activate) { [W]::SetForegroundWindow($h) | Out-Null; Start-Sleep -Milliseconds 400 }
$r = New-Object W+RECT
[W]::GetWindowRect($h, [ref]$r) | Out-Null
$w = $r.Right - $r.Left; $hgt = $r.Bottom - $r.Top
if ($w -le 0 -or $hgt -le 0) { Write-Output "bad rect"; exit 1 }
$bmp = New-Object System.Drawing.Bitmap($w, $hgt)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($r.Left, $r.Top, 0, 0, $bmp.Size)
$bmp.Save($Out, [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose(); $bmp.Dispose()
Write-Output "saved $Out rect=$($r.Left),$($r.Top) ${w}x${hgt} title='$($p.MainWindowTitle)'"
