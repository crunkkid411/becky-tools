---
name: msys2-native-builds
description: "Building native apps with MSYS2 on Jordan's PC: the Shotcut fork, MLT, mingw64, cmake and ninja, and the pacman -Syu deadlock workaround."
---

# MSYS2 native builds

Moved verbatim out of the always-loaded CLAUDE.md files on 2026-10-10 so it is read only when this kind
of work comes up. It has the same authority as CLAUDE.md.

**MSYS2 native builds on THIS PC (the Shotcut fork, 2026-06-23):** `pacman -Syu` DEADLOCKS when run
non-interactively/in the background (hangs for hours on the in-use `msys2-runtime` DLL swap; killing
it corrupts the local DB). What WORKED: drive a REAL `C:\msys64\msys2_shell.cmd -mingw64` window via
keyboard automation (PowerShell `WScript.Shell` `AppActivate('MINGW64')` + `SendKeys`) and type
`pacman -Syuu --noconfirm --overwrite "*"` into it — interactive completes in minutes. And MSYS2's
`mingw-w64-x86_64-mlt 7.36.1` package satisfies Shotcut's `mlt++-7>=7.36.0`, so you can SKIP the
multi-hour FFmpeg/MLT/OpenCV from-source build and just `cmake+ninja` Shotcut. (Full saga: `HANDOFF-LOG.md`.)
