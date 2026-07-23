# enumwin.ps1 - list every top-level window of becky-review.exe (any session view)
$log = 'X:\AI-2\becky-wt-engine\native\becky-review\enumwin-log.txt'
Set-Content $log "=== enum $(Get-Date) ==="
Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public class EW {
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern int GetWindowTextW(IntPtr h, StringBuilder sb, int max);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern int GetClassNameW(IntPtr h, StringBuilder sb, int max);
}
'@
$target = (Get-Process -Name becky-review -ErrorAction SilentlyContinue | Select-Object -First 1).Id
Add-Content $log "target pid: $target"
$lines = New-Object System.Collections.ArrayList
$cb = [EW+EnumProc]{
  param($h, $l)
  $pid2 = 0
  [void][EW]::GetWindowThreadProcessId($h, [ref]$pid2)
  if ($pid2 -eq $target) {
    $sb = New-Object System.Text.StringBuilder 256
    [void][EW]::GetWindowTextW($h, $sb, 256)
    $cn = New-Object System.Text.StringBuilder 256
    [void][EW]::GetClassNameW($h, $cn, 256)
    [void]$lines.Add("hwnd=$h class='$($cn.ToString())' title='$($sb.ToString())' visible=$([EW]::IsWindowVisible($h))")
  }
  return $true
}
[void][EW]::EnumWindows($cb, [IntPtr]::Zero)
foreach ($l in $lines) { Add-Content $log $l }
Add-Content $log "count: $($lines.Count)"
Add-Content $log "ENUM_DONE"
