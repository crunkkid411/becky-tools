//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	procEnumWindows  = user32.NewProc("EnumWindows")
	procEnumChildren = user32.NewProc("EnumChildWindows")
	procWindowPID    = user32.NewProc("GetWindowThreadProcessId")
	procIsVisible    = user32.NewProc("IsWindowVisible")
	procGetText      = user32.NewProc("GetWindowTextW")
	procGetClass     = user32.NewProc("GetClassNameW")
	procSendMessage  = user32.NewProc("SendMessageW")

	enumMu   sync.Mutex
	enumHits []uintptr
	enumCB   = syscall.NewCallback(func(h, _ uintptr) uintptr {
		enumHits = append(enumHits, h)
		return 1
	})
)

const bmClick = 0x00F5

func topWindows() []uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumHits = nil
	procEnumWindows.Call(enumCB, 0)
	return append([]uintptr(nil), enumHits...)
}

func childWindows(parent uintptr) []uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumHits = nil
	procEnumChildren.Call(parent, enumCB, 0)
	return append([]uintptr(nil), enumHits...)
}

func winText(h uintptr) string {
	buf := make([]uint16, 512)
	procGetText.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func winClass(h uintptr) string {
	buf := make([]uint16, 256)
	procGetClass.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func winPID(h uintptr) int {
	var pid uint32
	procWindowPID.Call(h, uintptr(unsafe.Pointer(&pid)))
	return int(pid)
}

// clickDialogButton answers a VEGAS dialog the proven way (memory:
// vegas-extension-bridge-facts): the visible #32770 window on the VEGAS pid,
// BM_CLICK on the one child Button whose label matches. No mouse, keyboard or
// focus is involved. Exactly one match is required, so it can never press the
// wrong button.
func clickDialogButton(pid int, button, title string) (string, error) {
	type hit struct {
		btn   uintptr
		title string
	}
	var hits []hit
	for _, d := range topWindows() {
		if winPID(d) != pid || winClass(d) != "#32770" {
			continue
		}
		if v, _, _ := procIsVisible.Call(d); v == 0 {
			continue
		}
		t := winText(d)
		if title != "" && !strings.EqualFold(t, title) {
			continue
		}
		for _, c := range childWindows(d) {
			if winClass(c) == "Button" && strings.EqualFold(strings.ReplaceAll(winText(c), "&", ""), button) {
				hits = append(hits, hit{c, t})
			}
		}
	}
	if len(hits) != 1 {
		return "", fmt.Errorf("found %d open VEGAS dialog buttons labelled %q (need exactly 1), so nothing was clicked", len(hits), button)
	}
	procSendMessage.Call(hits[0].btn, bmClick, 0, 0)
	return hits[0].title, nil
}
