//go:build gui && windows

package main

import (
	"encoding/binary"
	"testing"
)

// buildDropfilesBuffer constructs a synthetic CF_HDROP DROPFILES buffer for testing.
//
// DROPFILES layout (Windows SDK):
//
//	DWORD pFiles  — byte offset from start to the path list (always 20 for this helper)
//	POINT pt      — drop coordinates (8 bytes total: x int32, y int32)
//	BOOL  fNC     — non-client flag (4 bytes, zero)
//	BOOL  fWide   — 1 = UTF-16LE paths (4 bytes)
//	<paths>       — double-null-terminated UTF-16LE list
//
// Total header = 20 bytes (DWORD + POINT(8) + BOOL + BOOL).
func buildDropfilesBuffer(paths []string, wideFlag bool) []byte {
	const headerSize = 20
	var pathData []byte
	if wideFlag {
		for _, p := range paths {
			for _, r := range p {
				var u [2]byte
				binary.LittleEndian.PutUint16(u[:], uint16(r))
				pathData = append(pathData, u[:]...)
			}
			pathData = append(pathData, 0, 0) // UTF-16 null terminator for this path
		}
		pathData = append(pathData, 0, 0) // double-null (end of list)
	}

	buf := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(buf[0:], uint32(headerSize)) // pFiles offset
	// bytes 4..11: POINT pt — zeroed (unused by hdropPathsFromBuf)
	// bytes 12..15: BOOL fNC — zeroed
	fwide := uint32(0)
	if wideFlag {
		fwide = 1
	}
	binary.LittleEndian.PutUint32(buf[16:], fwide)
	return append(buf, pathData...)
}

func TestHdropPathsFromBuf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		paths []string
	}{
		{name: "single path", paths: []string{`C:\Users\jordan\evidence\clip.mp4`}},
		{name: "two paths", paths: []string{`C:\clip1.mp4`, `D:\audio\track.wav`}},
		{name: "empty drop", paths: []string{}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			buf := buildDropfilesBuffer(c.paths, true)
			got, err := hdropPathsFromBuf(buf)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(c.paths) {
				t.Fatalf("got %d paths, want %d: %v", len(got), len(c.paths), got)
			}
			for i, want := range c.paths {
				if got[i] != want {
					t.Errorf("path[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestHdropPathsFromBuf_tooShort(t *testing.T) {
	t.Parallel()
	_, err := hdropPathsFromBuf([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for buffer shorter than 20 bytes")
	}
}

func TestHdropPathsFromBuf_ansiRejected(t *testing.T) {
	t.Parallel()
	// fWide = 0 means ANSI paths — we do not support them (extremely rare post-Win95).
	buf := buildDropfilesBuffer([]string{`C:\file.txt`}, false)
	_, err := hdropPathsFromBuf(buf)
	if err == nil {
		t.Fatal("expected error for ANSI (fWide=0) buffer")
	}
}

func TestHdropPathsFromBuf_offsetOutOfRange(t *testing.T) {
	t.Parallel()
	buf := make([]byte, 20)
	binary.LittleEndian.PutUint32(buf[0:], 9999) // pFiles beyond buffer length
	binary.LittleEndian.PutUint32(buf[16:], 1)   // fWide = true
	_, err := hdropPathsFromBuf(buf)
	if err == nil {
		t.Fatal("expected error for pFiles offset out of range")
	}
}
