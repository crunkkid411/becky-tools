# launch-deployed.ps1 - start the DEPLOYED engine build on the real reel,
# press play, screenshot, and LEAVE IT RUNNING for Jordan.
$dir = 'X:\AI-2\becky-tools\native\becky-review'
$log = 'X:\AI-2\becky-wt-engine\native\becky-review\deployed-launch-log.txt'
Set-Content $log "=== deployed launch $(Get-Date) ==="
function Log($m) { Add-Content $log "$((Get-Date).ToString('HH:mm:ss')) $m" }
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class Drv2 {
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, UIntPtr extra);
  public struct RECT { public int L, T, R, B; }
  public static void Tap(byte vk) { keybd_event(vk, 0, 0, UIntPtr.Zero); keybd_event(vk, 0, 0x2, UIntPtr.Zero); }
  public static IntPtr found; public static uint targetPid;
  public static IntPtr FindMain(uint pid) {
    targetPid = pid; found = IntPtr.Zero;
    EnumWindows(delegate(IntPtr h, IntPtr l) {
      uint p; GetWindowThreadProcessId(h, out p);
      if (p == targetPid && IsWindowVisible(h)) {
        RECT r; GetWindowRect(h, out r);
        if ((r.R - r.L) > 400 && (r.B - r.T) > 300) { found = h; return false; }
      }
      return true;
    }, IntPtr.Zero);
    return found;
  }
}
'@
Add-Type -AssemblyName System.Drawing
$env:BECKY_REVIEW_REEL = 'X:\Videos\2025\11_November\Rendered\post_constantly.reel.json'
$p = Start-Process -FilePath (Join-Path $dir 'becky-review.exe') -WorkingDirectory $dir -PassThru
Log "deployed pid=$($p.Id)"
$h = [IntPtr]::Zero
for ($i = 0; $i -lt 60; $i++) { Start-Sleep -Milliseconds 500; $h = [Drv2]::FindMain([uint32]$p.Id); if ($h -ne [IntPtr]::Zero) { break } }
if ($h -eq [IntPtr]::Zero) { Log 'FAIL: no window'; exit 1 }
Log 'window up'
Start-Sleep -Seconds 6
[void][Drv2]::SetForegroundWindow($h)
Start-Sleep -Milliseconds 500
[Drv2]::Tap(0x20)   # play
Start-Sleep -Seconds 4
$r = New-Object Drv2+RECT
[void][Drv2]::GetWindowRect($h, [ref]$r)
$bmp = New-Object System.Drawing.Bitmap(($r.R-$r.L), ($r.B-$r.T))
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($r.L, $r.T, 0, 0, $bmp.Size)
$bmp.Save('X:\AI-2\becky-wt-engine\native\becky-review\deployed_playing.png')
$g.Dispose(); $bmp.Dispose()
Log 'DONE - deployed app left PLAYING'
