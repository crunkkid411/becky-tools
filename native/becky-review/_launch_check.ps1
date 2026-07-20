$ErrorActionPreference = 'Continue'
$exe = 'X:\AI-2\becky-tools\native\becky-review\becky-review.exe'
Start-Process -FilePath $exe
Start-Sleep -Seconds 8
$p = Get-Process -Name 'becky-review' -ErrorAction SilentlyContinue
if ($p) {
    "RUNNING pid=$($p.Id) title=[$($p.MainWindowTitle)]"
    $h = $p.MainWindowHandle
    "hwnd=$h"
} else {
    "NOT RUNNING - exited or crashed on launch"
    # check for a crash log / quick relaunch test
    Start-Process -FilePath $exe
    Start-Sleep -Seconds 5
    $p2 = Get-Process -Name 'becky-review' -ErrorAction SilentlyContinue
    if ($p2) { "RETRY RUNNING pid=$($p2.Id) title=[$($p2.MainWindowTitle)]" } else { "RETRY ALSO NOT RUNNING" }
}
