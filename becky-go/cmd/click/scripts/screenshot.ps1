# screenshot.ps1 - capture a screen rect to a PNG (the render-verification channel's
# capture step). Reuses the exact System.Drawing.CopyFromScreen block proven in the
# mouse-control probe. ASCII-only. Windows PowerShell 5.1.
param(
  [int]$X = 0,
  [int]$Y = 0,
  [int]$W = 1,
  [int]$H = 1,
  [Parameter(Mandatory=$true)][string]$Out
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
if ($W -le 0) { $W = 1 }
if ($H -le 0) { $H = 1 }
$bmp = New-Object System.Drawing.Bitmap($W, $H)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($X, $Y, 0, 0, $bmp.Size)
$bmp.Save($Out, [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose()
$bmp.Dispose()
'ok'
