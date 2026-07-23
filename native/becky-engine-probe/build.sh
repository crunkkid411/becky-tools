#!/bin/sh
# Build becky-engine-probe with the MSYS2 mingw64 toolchain.
# Run from any shell: sh build.sh
set -e
cd "$(dirname "$0")"
MINGW=/c/msys64/mingw64
export PATH="$MINGW/bin:$PATH"
FLAGS=$("$MINGW/bin/pkg-config" --cflags --libs libavformat libavcodec libavutil libswresample)
"$MINGW/bin/g++" -O2 -Wall -o becky-engine-probe.exe main.cpp \
    $FLAGS -ld3d11 -ldxgi -ld3dcompiler -lole32 -luuid -lavrt
echo "built becky-engine-probe.exe"
