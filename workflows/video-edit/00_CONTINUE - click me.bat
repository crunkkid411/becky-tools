@echo off
rem Starts Claude Code on the next job in NEXT.md (video edit pipeline).
cd /d "%~dp0..\.."
echo Starting Claude Code on the next video edit job...
echo.
call claude "Continue the video edit pipeline work. Read workflows/video-edit/RULEBOOK.md, then workflows/video-edit/NEXT.md. Do the FIRST unticked job only, by improving the existing code (never rebuild it). If it says NEEDS JORDAN, prepare everything you can, then ask him one plain question. When the job is tested on real footage: tick it in NEXT.md, move it to Done with the date and one plain sentence, update ROADMAP.md, commit and push. Talk to Jordan in plain words, never by job numbers or file codes."
echo.
pause
