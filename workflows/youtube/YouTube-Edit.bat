@echo off
setlocal
REM ===========================================================================
REM YouTube Video Edit
REM
REM Edits the raw recording of a YouTube video (not a livestream) into the
REM video you post, and saves ONE VEGAS Pro project next to it:
REM   <folder name>-video.veg
REM Run it again and it saves over that same project - unless you saved the
REM project yourself since, then your version is kept and a new one is made.
REM
REM How it decides (the openshorts way, all on this PC):
REM   - everything you say stays; the silences and breaths come out
REM   - Gemma-4 12B reads the video in parts and names only the exceptions:
REM     mic checks and getting ready, after your goodbye, and retakes (the
REM     words must show you said it again, or it stays)
REM   - what you tell your editor on camera is followed, and your words to
REM     the editor are cut
REM   - a word or two said on its own while you think ("Whatever.", "So uh")
REM     is cut; a lone word that finishes or starts a sentence stays
REM   - then it reads the edit like a viewer and puts back any cut line the
REM     story needs
REM   - every cut sits on the first and last frame of your voice, not on room
REM     noise; a stretch with no words is listened to again and goes if no
REM     word is heard (a laugh, a sound)
REM   - every "fuck" gets Censor 2.mp3 at -12.1 dB on your speech track, from
REM     one frame after the word starts to one frame before it ends
REM   - your hj-starter track effects go on the main video track (Beauty Box,
REM     AutoLooks, Color Corrector, Color Curves x2), and a slow zoom or crop
REM     about once a minute
REM   - something you mean to show ("he said this"): your file from this
REM     folder (or named in a notes .txt here) goes in; otherwise a marker,
REM     all listed in MARKERS.txt next to the project
REM   - it writes title options, a description and chapters for the upload:
REM     becky-edit\youtube-upload.txt
REM Regions only where something may not be postable (another creator's
REM video, an address on screen).
REM
REM Vertical shorts come AFTER this, from the edited project:
REM Make-Vertical-Shorts.bat on the same folder.
REM
REM OBS recordings: run "Prep OBS Footage.bat" on the folder first.
REM
REM Drag the video onto this file, or put this file in the footage folder and
REM double-click it. Your video is NEVER changed.
REM ===========================================================================

cd /d "%~dp0"
where becky-livestream >nul 2>nul
if errorlevel 1 (
  echo.
  echo becky-livestream is not installed - run build-all-tools.bat in becky-tools.
  echo.
  pause
  exit /b 1
)

becky-livestream --video %*
echo.
pause
