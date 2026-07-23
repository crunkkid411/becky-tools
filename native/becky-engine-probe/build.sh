#!/bin/sh
# Build becky-engine-probe with the MSYS2 mingw64 toolchain.
# Run from any shell: sh build.sh
set -e
cd "$(dirname "$0")"
MINGW=/c/msys64/mingw64
export PATH="$MINGW/bin:$PATH"
# PortAudio (WASAPI backend) reused from the already-built native/audio-host
# checkout, per HANDOFF-VIDEO-ENGINE.md step 5: "PORT the working WASAPI
# output". Absolute path since audio-host lives in the main checkout, not this
# worktree - fine for the probe; step 6 (moving into native/becky-review,
# which IS in that checkout) should switch this to a relative/vendored path.
PA_ROOT=/x/AI-2/becky-tools/native/audio-host
FLAGS=$("$MINGW/bin/pkg-config" --cflags --libs libavformat libavcodec libavutil libswresample)
taskkill //IM becky-engine-probe.exe //F >/dev/null 2>&1 || true # set -e trap: nonzero when no process
for i in 1 2 3; do
    if "$MINGW/bin/g++" -O2 -Wall -o becky-engine-probe.exe main.cpp \
        $FLAGS -I"$PA_ROOT/third_party/portaudio/include" \
        -ld3d11 -ldxgi -ld3dcompiler -lavrt \
        "$PA_ROOT/build/portaudio/libportaudio.a" -lwinmm -lsetupapi -lole32 -luuid; then
        echo "built becky-engine-probe.exe"
        exit 0
    fi
    echo "link retry $i (exe may still be locked)"; sleep 2
done
echo "BUILD FAILED"; exit 1
