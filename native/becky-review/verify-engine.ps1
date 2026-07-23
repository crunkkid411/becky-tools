# verify-engine.ps1 - drives the ENGINE build of Becky Review on the real reel.
# Runs in the INTERACTIVE session via schtasks (the agent sandbox desktop cannot
# see or screenshot windows). ASCII only. Leaves the app RUNNING at the end.
$ErrorActionPreference = 'Continue'
$dir = 'X:\AI-2\becky-wt-engine\native\becky-review'
$log = Join-Path $dir 'verify-engine-log.txt'
function Log($m) { $ts = (Get-Date).ToString('HH:mm:ss.fff'); Add-Content -Path $log -Value "$ts $m" }
Set-Content -Path $log -Value "=== engine verify run $(Get-Date) ==="

Add-Type @'
using System;
using System.Runtime.InteropServices;
public class Drv {
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, UIntPtr extra);
  public struct RECT { public int L, T, R, B; }
  public const uint KEYUP = 0x2;
  public static void Tap(byte vk) { keybd_event(vk, 0, 0, UIntPtr.Zero); keybd_event(vk, 0, KEYUP, UIntPtr.Zero); }
  // Find the LARGEST visible top-level window of a pid (the app's main window).
  public static IntPtr found;
  public static uint targetPid;
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

function Shot($h, $name) {
  $r = New-Object Drv+RECT
  [void][Drv]::GetWindowRect($h, [ref]$r)
  $w = $r.R - $r.L; $ht = $r.B - $r.T
  if ($w -le 0 -or $ht -le 0) { Log "shot $name skipped (bad rect)"; return }
  $bmp = New-Object System.Drawing.Bitmap($w, $ht)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($r.L, $r.T, 0, 0, $bmp.Size)
  $bmp.Save((Join-Path $dir $name))
  $g.Dispose(); $bmp.Dispose()
  Log "shot $name saved ($w x $ht)"
}

function CpuPct($proc, $seconds) {
  $t0 = $proc.TotalProcessorTime.TotalMilliseconds
  Start-Sleep -Seconds $seconds
  $proc.Refresh()
  $t1 = $proc.TotalProcessorTime.TotalMilliseconds
  [math]::Round(($t1 - $t0) / ($seconds * 1000.0) * 100.0, 1)
}

# 1) launch on the real reel
$env:BECKY_REVIEW_REEL = 'X:\Videos\2025\11_November\Rendered\post_constantly.reel.json'
$p = Start-Process -FilePath (Join-Path $dir 'becky-review.exe') -WorkingDirectory $dir -PassThru
Log "launched pid=$($p.Id)"
$h = [IntPtr]::Zero
for ($i = 0; $i -lt 60; $i++) {
  Start-Sleep -Milliseconds 500
  $h = [Drv]::FindMain([uint32]$p.Id)
  if ($h -ne [IntPtr]::Zero) { break }
}
if ($h -eq [IntPtr]::Zero) { Log 'FAIL: window never appeared'; exit 1 }
Log "window found, visible=$([Drv]::IsWindowVisible($h))"
Start-Sleep -Seconds 6   # let the reel load, peaks queue, first frame paint

[void][Drv]::SetForegroundWindow($h)
Start-Sleep -Milliseconds 500
Shot $h 'engine_idle.png'
$idleCpu = CpuPct $p 8
Log "idle CPU pct-of-one-core = $idleCpu"

# 2) PLAY 12 s (space), screenshots 4 s apart to prove the frame advances
[void][Drv]::SetForegroundWindow($h)
[Drv]::Tap(0x20)  # VK_SPACE
Log 'sent SPACE (play)'
Start-Sleep -Seconds 4
Shot $h 'engine_play1.png'
$playCpu = CpuPct $p 6
Log "playback CPU pct-of-one-core = $playCpu"
Shot $h 'engine_play2.png'

# 3) pause + frame steps (+/- 1f)
[Drv]::Tap(0x20)  # pause
Start-Sleep -Milliseconds 600
for ($i = 0; $i -lt 6; $i++) { [Drv]::Tap(0x27); Start-Sleep -Milliseconds 150 }  # RIGHT x6
for ($i = 0; $i -lt 3; $i++) { [Drv]::Tap(0x25); Start-Sleep -Milliseconds 150 }  # LEFT x3
Shot $h 'engine_steps.png'
Log 'frame steps sent (6 right, 3 left)'

# 4) scrub churn through the app's real seek path: 20 presses/s for 5 s
$sw = [System.Diagnostics.Stopwatch]::StartNew()
$n = 0
while ($sw.Elapsed.TotalSeconds -lt 5) {
  [Drv]::Tap(0x27)
  $n++
  Start-Sleep -Milliseconds 45
}
Log "scrub churn: $n right-steps in 5s"
$churnCpu = CpuPct $p 3
Log "post-churn CPU pct-of-one-core = $churnCpu"
Shot $h 'engine_churn.png'

# 5) play again and LEAVE RUNNING (Jordan wakes to it playing)
[void][Drv]::SetForegroundWindow($h)
[Drv]::Tap(0x20)
Log 'left PLAYING - app stays up'
Start-Sleep -Seconds 3
Shot $h 'engine_final.png'
Log 'DONE'
