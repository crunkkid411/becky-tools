# uia_action.ps1 - generalized name-based UI Automation locate/click.
# Ported from the PROVEN tools/mouse-control/uia_click_probe.ps1 (hj-mission-control):
# find a top-level window by NAME (substring), find a descendant control by NAME
# (+ optional ControlType) that exposes a real InvokePattern, and either report its
# window rect (-Mode locate) or Invoke it (-Mode click). No pixel coordinates, no
# synthetic mouse, no foreground steal - the OS routes the action by control identity.
# A control that matches the name but has NO InvokePattern (a Win32/MSAA-bridged Pane)
# is treated as "not found here" so the caller falls through to the win32 backend.
# ASCII-only. Runs under Windows PowerShell 5.1 (has the UIAutomation assemblies).
param(
  [Parameter(Mandatory=$true)][string]$Window,
  [Parameter(Mandatory=$true)][string]$Name,
  [string]$ControlType = "",
  [Parameter(Mandatory=$true)][string]$Mode
)
$ErrorActionPreference = 'Stop'
function emit($o) { $o | ConvertTo-Json -Compress; exit 0 }
function finiteInt($v) { if ([double]::IsNaN($v) -or [double]::IsInfinity($v)) { return 0 } return [int]$v }

try {
  Add-Type -AssemblyName UIAutomationClient
  Add-Type -AssemblyName UIAutomationTypes
} catch {
  emit @{ ok=$false; found=$false; error=("uia assemblies unavailable: " + $_.Exception.Message) }
}

$auto     = [System.Windows.Automation.AutomationElement]
$root     = $auto::RootElement
$true_cond = [System.Windows.Automation.Condition]::TrueCondition
$children = [System.Windows.Automation.TreeScope]::Children
$descend  = [System.Windows.Automation.TreeScope]::Descendants
$wantWin  = $Window.ToLower()
$wantName = $Name.ToLower()
$wantType = $ControlType.ToLower()

# top-level window whose Name CONTAINS the requested substring (case-insensitive)
$win = $null
for ($i = 0; $i -lt 40; $i++) {
  $kids = $root.FindAll($children, $true_cond)
  foreach ($k in $kids) {
    $n = ""
    try { $n = $k.Current.Name } catch {}
    if ($n -and $n.ToLower().Contains($wantWin)) { $win = $k; break }
  }
  if ($win) { break }
  Start-Sleep -Milliseconds 150
}
if (-not $win) { emit @{ ok=$false; found=$false; error="window not found by name" } }

# descendant control matching name (+ optional type) that HAS an InvokePattern
$target = $null
$all = $win.FindAll($descend, $true_cond)
foreach ($el in $all) {
  $n = ""
  try { $n = $el.Current.Name } catch {}
  if (-not $n) { continue }
  if (-not $n.ToLower().Contains($wantName)) { continue }
  if ($wantType -ne "") {
    $ct = ""
    try { $ct = $el.Current.ControlType.ProgrammaticName } catch {}
    # ProgrammaticName looks like "ControlType.Button"
    if (-not $ct.ToLower().EndsWith("." + $wantType)) { continue }
  }
  $ip = $null
  try { $ip = $el.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern) } catch { $ip = $null }
  if ($ip) { $target = $el; break }
}
if (-not $target) { emit @{ ok=$false; found=$false; error="no invokable UIA control matched name" } }

# capture the WHOLE WINDOW rect (so state labels around the control are in frame for OCR)
$wr = $win.Current.BoundingRectangle
$rect = @{ x=(finiteInt $wr.X); y=(finiteInt $wr.Y); w=(finiteInt $wr.Width); h=(finiteInt $wr.Height) }

if ($Mode -eq "locate") {
  emit @{ ok=$true; found=$true; method="uia"; rect=$rect }
}

# click mode: name-based InvokePattern.Invoke - no coordinates, no cursor move
try {
  $ip = $target.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)
  $ip.Invoke()
} catch {
  emit @{ ok=$false; found=$true; clicked=$false; method="uia"; error=("invoke failed: " + $_.Exception.Message) }
}
emit @{ ok=$true; found=$true; clicked=$true; method="uia"; rect=$rect }
