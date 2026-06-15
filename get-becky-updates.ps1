# Get-Becky-Updates.ps1 — the one-click "button" for Jordan (non-dev).
#
# Double-clicking the Desktop shortcut runs this. It does the whole cloud->local
# handoff automatically and explains everything in plain English:
#   1. Ask GitHub if the cloud helper pushed any new work.
#   2. If yes, check it actually builds + passes its tests on this PC.
#   3. If it's a clean, finished, safe update -> install it (merge) by itself.
#   4. If it needs a human/AI judgement call -> open the assistant to handle it.
#   5. If nothing new -> say "you're all caught up" and stop.
#
# Jordan never types anything. He clicks the icon, watches, and closes the window.
# (The matching Go tool "becky-handoff" is queued in CLAUDE.md section 6; this
# PowerShell version ships the button today.)

param([switch]$NoPause)   # -NoPause is only used for automated testing.

$ErrorActionPreference = 'Stop'
# git/go report failure via exit codes, which we check explicitly below. Stop
# PowerShell 7 from ALSO throwing on their normal stderr chatter, so this behaves
# the same in Windows PowerShell 5.1 (the shortcut) and in pwsh 7.
if (Get-Variable PSNativeCommandUseErrorActionPreference -Scope Global -ErrorAction SilentlyContinue) {
    $PSNativeCommandUseErrorActionPreference = $false
}
$Repo = if ($env:BECKY_REPO) { $env:BECKY_REPO } else { 'X:\AI-2\becky-tools' }

# ----- helpers (Gx is named so it never shadows the real 'git' program) ---------
function Gx       { & git -C $Repo @args }   # run git in the becky-tools repo
function Say     ($m) { Write-Host $m -ForegroundColor Gray }
function Good    ($m) { Write-Host $m -ForegroundColor Green }
function Warn    ($m) { Write-Host $m -ForegroundColor Yellow }
function Bad     ($m) { Write-Host $m -ForegroundColor Red }
function Title   ($m) { Write-Host ""; Write-Host $m -ForegroundColor Cyan; Write-Host ("-" * 60) -ForegroundColor DarkCyan }
function Finish  ($code) { Write-Host ""; if (-not $NoPause) { Read-Host 'Press Enter to close this window' }; exit $code }
function OpenAssistant ($why) {
    Warn $why
    Say  "Opening your assistant to finish this for you. Just watch - it works on its"
    Say  "own, won't ask you anything, and will say when it's done."
    # This is a hands-off, one-click button for a non-developer. Run Claude FULLY
    # autonomous: --dangerously-skip-permissions so Jordan is never asked to approve
    # tool calls, and a prompt that forbids ALL interactive questions/menus (those are
    # what froze the window before). Safe here because the button only ever runs this
    # one scoped, local handoff (fetch -> build -> test -> merge) on Jordan's own PC.
    $prompt = @'
You were launched by Jordan's one-click "Get Becky Updates" button. Do the full
cloud-to-local handoff per CLAUDE.md section 4 "Minimal trigger", completely on your
own: fetch, check out the newest claude/* branch, read section 6, run `go build ./...`
and `go test ./...` in becky-go, and if it is good and finished, merge it into master,
push, and delete the merged branch.

CRITICAL - this is an UNATTENDED button for a NON-DEVELOPER:
- NEVER ask Jordan anything. Do NOT use AskUserQuestion. Do NOT show multiple-choice
  menus. Do NOT ask him to type commands or make a decision. He is not watching closely
  and any prompt just freezes the window.
- Make the obvious safe choice yourself and proceed. A docs/specs-only or infra branch
  whose section 6 says nothing is left for the local agent should simply be merged
  (resolve any CLAUDE.md merge conflict yourself, keeping both sides' content).
- Only STOP if something is genuinely unsafe, broken, or unfinished. If you stop, do
  NOT prompt - just print a short plain-English note of what you found, then exit.
- When done, print a short plain-English summary of what got installed, then stop.
'@
    try { & claude --dangerously-skip-permissions $prompt }
    catch { Bad "Could not open the assistant automatically. Tell Claude: grab the latest cloud branch." }
    Finish 0
}

try { Clear-Host } catch {}   # harmless if there's no real console (e.g. automated test)
Write-Host "============================================================" -ForegroundColor Cyan
Write-Host "   GET BECKY UPDATES" -ForegroundColor White
Write-Host "   Checking if the cloud helper sent anything new." -ForegroundColor Gray
Write-Host "   You don't need to do anything - just watch." -ForegroundColor Gray
Write-Host "============================================================" -ForegroundColor Cyan

# ----- sanity: tools present ----------------------------------------------------
foreach ($t in 'git','go') {
    if (-not (Get-Command $t -ErrorAction SilentlyContinue)) {
        Bad "Can't find '$t' on this PC, which this needs."
        Bad "Tell Claude: 'the update button says it can't find $t' and it'll sort it out."
        Finish 1
    }
}
if (-not (Test-Path (Join-Path $Repo '.git'))) { Bad "becky-tools project not found at $Repo."; Finish 1 }

# ----- 1. ask GitHub for new work ----------------------------------------------
Title "Step 1 of 3: Asking GitHub for new work..."
Gx fetch origin --prune --quiet
if ($LASTEXITCODE -ne 0) {
    Bad "Couldn't reach GitHub. Check your internet connection and try again."
    Finish 1
}

# newest cloud branch that is NOT already merged into master
$branches = Gx for-each-ref --sort=-committerdate --format='%(refname:short)' refs/remotes/origin/claude
$target = $null
foreach ($b in $branches) {
    if (-not $b) { continue }
    Gx merge-base --is-ancestor $b origin/master 2>$null
    if ($LASTEXITCODE -ne 0) { $target = $b; break }   # not an ancestor => not merged yet
}

if (-not $target) {
    Title "All caught up!"
    Good  "There's nothing new from the cloud helper right now."
    Say   "Everything it has sent is already installed. Nothing to do."
    Finish 0
}

$short   = $target -replace '^origin/',''
$subject = (Gx log -1 --format='%s' $target)
Good "Found new work from the cloud helper:"
Say  "   `"$subject`""

# ----- 2. is it safe + finished to auto-install? --------------------------------
Title "Step 2 of 3: Checking that it's finished and safe to install..."

# working tree clean + local master matches GitHub (no half-done local changes)
if ((Gx status --porcelain)) { OpenAssistant "There are unsaved changes here, so I won't auto-install." }
$localMaster  = Gx rev-parse master 2>$null
$remoteMaster = Gx rev-parse origin/master 2>$null
if ($localMaster -ne $remoteMaster) { OpenAssistant "Your project is out of step with GitHub, so I won't auto-install." }

# clean fast-forward only (master must be an ancestor of the new branch)
Gx merge-base --is-ancestor origin/master $target 2>$null
if ($LASTEXITCODE -ne 0) { OpenAssistant "This update isn't a simple add-on, so it needs the assistant." }

# the cloud agent marks "Left for local agent:" in CLAUDE.md section 6.
# Only auto-install when it explicitly says nothing is left to do.
$claudeMd = (Gx show "${target}:CLAUDE.md" 2>$null) -join "`n"
$leftLine = ($claudeMd -split "`n" | Where-Object { $_ -match 'Left for local agent' } | Select-Object -First 1)
if (-not $leftLine) { OpenAssistant "I can't tell if this update is finished, so the assistant should check it." }
if ($leftLine -notmatch '(?i)nothing') { OpenAssistant "This update still needs hands-on work, so I'll open the assistant." }

# ----- 3. build + test, then install -------------------------------------------
Title "Step 3 of 3: Testing it on your PC (this takes about a minute)..."
Push-Location (Join-Path $Repo 'becky-go')
try {
    Say "   Building every tool..."
    & go build ./... ; $buildOk = ($LASTEXITCODE -eq 0)
    if ($buildOk) { Say "   Running every test..."; & go test ./... ; $testOk = ($LASTEXITCODE -eq 0) } else { $testOk = $false }
} finally { Pop-Location }

if (-not $buildOk) { OpenAssistant "It didn't build cleanly on this PC, so the assistant should look." }
if (-not $testOk)  { OpenAssistant "A test didn't pass, so the assistant should look before installing." }

Good "It builds and all tests pass on your PC."
Say  "Installing the update..."
Gx checkout master --quiet ;          $ok = ($LASTEXITCODE -eq 0)
if ($ok) { Gx merge --ff-only $target --quiet ; $ok = ($LASTEXITCODE -eq 0) }
if ($ok) { Gx push origin master --quiet ;      $ok = ($LASTEXITCODE -eq 0) }
if (-not $ok) {
    OpenAssistant "Something went wrong while installing, so the assistant should finish it safely."
}
Gx push origin --delete $short --quiet 2>$null          # tidy up the finished cloud branch (ok if already gone)
Gx branch -D $short 2>$null | Out-Null                   # and any local copy (ignore if none)

Title "Done!"
Good "The update is installed."
Say  "   What it was: `"$subject`""
Say  "You can close this window. Click the icon again any time to check for more."
Finish 0
