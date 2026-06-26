# shot.ps1 - capture the full virtual screen to a PNG. Usage: pwsh shot.ps1 out.png
# Throwaway GUI-test harness (screenshots Jordan's screen so the agent can see the
# forked Shotcut). Not referenced by project code.
param([string]$Out = "shot.png")
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$b = [System.Windows.Forms.SystemInformation]::VirtualScreen
$bmp = New-Object System.Drawing.Bitmap($b.Width, $b.Height)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($b.Left, $b.Top, 0, 0, $bmp.Size)
$bmp.Save($Out, [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose(); $bmp.Dispose()
Write-Output "saved $Out ($($b.Width)x$($b.Height))"
