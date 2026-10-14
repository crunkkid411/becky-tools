package vegasctl

import (
	"syscall"
	"unsafe"
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	enumWindows     = user32.NewProc("EnumWindows")
	windowPID       = user32.NewProc("GetWindowThreadProcessId")
	isIconic        = user32.NewProc("IsIconic")
	isWindowVisible = user32.NewProc("IsWindowVisible")
	getWindow       = user32.NewProc("GetWindow")
	showWindow      = user32.NewProc("ShowWindow")
)

// Restore un-minimizes VEGAS's main window without taking the keyboard
// (SW_SHOWNOACTIVATE): a minimized VEGAS renders every frame pure black.
func Restore() {
	if PID <= 0 {
		return
	}
	cb := syscall.NewCallback(func(h, _ uintptr) uintptr {
		var pid uint32
		windowPID.Call(h, uintptr(unsafe.Pointer(&pid)))
		if int(pid) == PID {
			vis, _, _ := isWindowVisible.Call(h)
			owner, _, _ := getWindow.Call(h, 4) // GW_OWNER: the main window has none
			if icon, _, _ := isIconic.Call(h); vis != 0 && owner == 0 && icon != 0 {
				showWindow.Call(h, 4)
			}
		}
		return 1
	})
	enumWindows.Call(cb, 0)
}
