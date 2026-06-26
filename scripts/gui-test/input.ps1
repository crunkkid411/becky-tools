# input.ps1 - drive native Windows mouse/keyboard for GUI testing of the forked
# Shotcut. Throwaway test harness; not referenced by project code.
# Ops (run in order): -Click "x,y"  -Double "x,y"  -Move "x,y"  -Type "text"  -Keys "{ENTER}"
# Coordinates are absolute SCREEN pixels. -Activate "TitleSubstr" brings a window up first.
param(
  [string]$Activate = "",
  [string]$Move = "",
  [string]$Click = "",
  [string]$Double = "",
  [string]$Type = "",
  [string]$Keys = "",
  [int]$Pause = 350
)
Add-Type -AssemblyName System.Windows.Forms
Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Inp {
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  public const uint LD=0x02, LU=0x04;
  public static void Click(int x,int y){ SetCursorPos(x,y); System.Threading.Thread.Sleep(80);
    mouse_event(LD,0,0,0,IntPtr.Zero); System.Threading.Thread.Sleep(40); mouse_event(LU,0,0,0,IntPtr.Zero); }
}
"@
function Activate($t) {
  $ws = New-Object -ComObject WScript.Shell
  $null = $ws.AppActivate($t); Start-Sleep -Milliseconds 500
}
if ($Activate) { Activate $Activate }
if ($Move)   { $a=$Move.Split(','); [Inp]::SetCursorPos([int]$a[0],[int]$a[1]); Start-Sleep -Milliseconds $Pause }
if ($Click)  { $a=$Click.Split(','); [Inp]::Click([int]$a[0],[int]$a[1]); Start-Sleep -Milliseconds $Pause }
if ($Double) { $a=$Double.Split(','); [Inp]::Click([int]$a[0],[int]$a[1]); Start-Sleep -Milliseconds 120; [Inp]::Click([int]$a[0],[int]$a[1]); Start-Sleep -Milliseconds $Pause }
if ($Type)   { [System.Windows.Forms.SendKeys]::SendWait($Type); Start-Sleep -Milliseconds $Pause }
if ($Keys)   { [System.Windows.Forms.SendKeys]::SendWait($Keys); Start-Sleep -Milliseconds $Pause }
Write-Output "ok"
