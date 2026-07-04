# runtime/mpv

The mpv video runtime for Becky Review's RIGHT pane. These are large prebuilt
binaries (~112 MB each) and are **git-ignored** — they are not committed.

To install them, run from `gui/BeckyReview/`:

```powershell
powershell -ExecutionPolicy Bypass -File fetch-mpv.ps1
```

That downloads a pinned, known-good Windows mpv build and drops these here:

| File | Used by | Why |
|---|---|---|
| `mpv.exe` | Step 2 (current) | embedded video pane via the `wid` child-window handle |
| `libmpv-2.dll` | Step 8 | in-process render API (becky draws its own playhead) |
| `include/mpv/*.h`, `libmpv.dll.a` | Step 8 | headers + import lib for P/Invoke |

The build is from <https://github.com/zhongfly/mpv-winbuild> (mpv is GPLv2+/LGPLv2.1+).
`mpv.exe` and `libmpv-2.dll` are statically linked, so they need no sibling DLLs.

The project file copies `mpv.exe` and `libmpv-2.dll` to the build output if present;
if they are missing, the app still builds and runs — only the video pane reports that
it needs `fetch-mpv.ps1`.
