//go:build gui && windows

// dragdrop_windows.go implements true OS file drag-and-drop for the Gio canvas window
// on Windows via a hand-rolled IDropTarget COM object.
//
// Why not Gio's built-in transfer events?  Gio v0.10 does not implement IDropTarget on
// Windows — its Windows backend has no WM_DROPFILES / OLE drag path (verified in
// gioui.org@v0.10.0/app/os_windows.go).  This file closes that gap with pure Go:
//   - No cgo, no CGO_ENABLED=1 needed.
//   - No external dependencies beyond the standard library (syscall + unsafe).
//   - Degrades gracefully: if RegisterDragDrop fails, the app continues working via
//     the argv + Open-button paths; it never panics (CLAUDE.md §2 degrade-never-crash).
//
// COM vtable layout for IDropTarget (IUnknown + 4 drop methods):
//
//	[0] QueryInterface
//	[1] AddRef
//	[2] Release
//	[3] DragEnter
//	[4] DragOver
//	[5] DragLeave
//	[6] Drop
//
// Integration with gui.go:
//
//	loop() calls handleGioEvent(a, e) for every event.Event it receives.
//	On Windows this function intercepts the first app.Win32ViewEvent with a
//	non-zero HWND and calls enableFileDrop once, storing the revoke func in a.disableDrop.
package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"gioui.org/app"
	"gioui.org/io/event"
)

// handleGioEvent is called from loop() for every Gio event. It WOULD register an
// IDropTarget on the window's HWND for in-window OS file drops — but that path is
// DISABLED because it crashed the window on launch (0xC0000005 access violation in
// CoLockObjectExternal, see enableFileDrop).
//
// Root cause: OLE drag-drop must be set up (RegisterDragDrop + CoLockObjectExternal)
// on the exact OS thread that owns the HWND and pumps its messages, inside that
// thread's STA apartment. loop() runs on a Go goroutine that the scheduler migrates
// across OS threads, so OleInitialize/RegisterDragDrop ran in the wrong/unsynchronised
// apartment and OLE dereferenced our object on a thread that never initialised it.
// A correct fix needs a C-side drop target installed on Gio's own window thread.
//
// Until then, in-window drop degrades to the RELIABLE paths (CLAUDE.md §2 degrade-
// never-crash): drop a file onto becky-canvas.exe (argv → adoptArgv), use the Open
// button, or paste a path. The window must ALWAYS open — that is the priority.
func handleGioEvent(a *App, e event.Event) {
	ve, ok := e.(app.Win32ViewEvent)
	if !ok || !ve.Valid() {
		return
	}
	if a.disableDrop == nil {
		a.disableDrop = func() {} // mark attempted; do NOT register (crash-prone — see above)
	}
}

// ─── constants ────────────────────────────────────────────────────────────────

const (
	// OLE result codes.
	_S_OK    = 0
	_S_FALSE = 1 // OleInitialize: already initialised — that is fine

	// DROPEFFECT values (bitmask).
	_DROPEFFECT_COPY = uintptr(1)

	_CF_HDROP = 15 // clipboard format for dropped files (HDROP)

	// DragQueryFileW: pass this as iFile to get the file count.
	_dragQueryCount = uintptr(0xFFFFFFFF)
)

// ─── lazy-loaded DLL procs ────────────────────────────────────────────────────

var (
	modOle32   = syscall.NewLazyDLL("ole32.dll")
	modShell32 = syscall.NewLazyDLL("shell32.dll")

	procOleInitialize        = modOle32.NewProc("OleInitialize")
	procOleUninitialize      = modOle32.NewProc("OleUninitialize")
	procRegisterDragDrop     = modOle32.NewProc("RegisterDragDrop")
	procRevokeDragDrop       = modOle32.NewProc("RevokeDragDrop")
	procCoLockObjectExternal = modOle32.NewProc("CoLockObjectExternal")
	procReleaseStgMedium     = modOle32.NewProc("ReleaseStgMedium")

	procDragQueryFileW = modShell32.NewProc("DragQueryFileW")
)

// ─── COM vtable struct ────────────────────────────────────────────────────────

// iDropTargetVtbl mirrors the IDropTarget vtable layout exactly.
// Slot order: QueryInterface, AddRef, Release, DragEnter, DragOver, DragLeave, Drop.
type iDropTargetVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	DragEnter      uintptr
	DragOver       uintptr
	DragLeave      uintptr
	Drop           uintptr
}

// iDropTarget is our COM object.  Its very first field MUST be a pointer to the vtable
// — that is the ABI contract COM relies on (COM sees &iDropTarget as IDropTarget*).
type iDropTarget struct {
	vtbl   *iDropTargetVtbl
	onDrop func(paths []string)
}

// ─── public API ──────────────────────────────────────────────────────────────

// enableFileDrop registers an IDropTarget on hwnd.  onDrop is called (on the OS
// window-proc thread) with the dropped file paths whenever a user drops files onto
// the window.
//
// Returns a disable function (always safe to call, even on error) that revokes the
// registration and uninitialises OLE.
// On error the window continues working via argv / Open-button paths.
func enableFileDrop(hwnd uintptr, onDrop func(paths []string)) (disable func(), err error) {
	disable = func() {} // safe no-op by default

	// OleInitialize must be called once per thread that calls OLE.  S_FALSE means
	// already initialised — both S_OK and S_FALSE are success codes.
	r, _, _ := procOleInitialize.Call(0)
	if r != uintptr(_S_OK) && r != uintptr(_S_FALSE) {
		return disable, fmt.Errorf("becky-canvas drag-drop: OleInitialize 0x%08X", r)
	}

	dt := buildDropTarget(onDrop)

	// RegisterDragDrop(HWND, IDropTarget*) pins the window as a drop target.
	r, _, _ = procRegisterDragDrop.Call(hwnd, uintptr(unsafe.Pointer(dt)))
	if r != uintptr(_S_OK) {
		procOleUninitialize.Call() //nolint:errcheck — best-effort cleanup
		return disable, fmt.Errorf("becky-canvas drag-drop: RegisterDragDrop 0x%08X", r)
	}

	// CoLockObjectExternal keeps our Go-allocated object's refcount above zero while
	// it is registered, preventing COM from freeing it from under us.
	procCoLockObjectExternal.Call(uintptr(unsafe.Pointer(dt)), 1 /*fLock*/, 1 /*fLastUnlockReleases*/)

	disable = func() {
		procRevokeDragDrop.Call(hwnd)
		procCoLockObjectExternal.Call(uintptr(unsafe.Pointer(dt)), 0 /*unlock*/, 1)
		procOleUninitialize.Call()
	}
	return disable, nil
}

// ─── build the COM object ─────────────────────────────────────────────────────

// buildDropTarget constructs the iDropTarget COM object with all seven vtable slots
// wired to Go callbacks via syscall.NewCallback (stdcall calling convention on Windows).
//
// The callback pointers live for the process lifetime — that is intentional, as COM
// may call them at any time while the object is registered.
func buildDropTarget(onDrop func(paths []string)) *iDropTarget {
	dt := &iDropTarget{onDrop: onDrop}

	vtbl := &iDropTargetVtbl{
		// QueryInterface — return our own pointer; RegisterDragDrop only needs IDropTarget.
		QueryInterface: syscall.NewCallback(func(this uintptr, riid uintptr, ppvObject *uintptr) uintptr {
			if ppvObject != nil {
				*ppvObject = this
			}
			return uintptr(_S_OK)
		}),

		AddRef: syscall.NewCallback(func(this uintptr) uintptr {
			return 1 // static refcount — CoLockObjectExternal keeps us alive
		}),

		Release: syscall.NewCallback(func(this uintptr) uintptr {
			return 1
		}),

		// DragEnter(this, pDataObj, grfKeyState, ptX, ptY, pdwEffect*)
		// Setting *pdwEffect = DROPEFFECT_COPY makes the shell show the green "+" cursor.
		DragEnter: syscall.NewCallback(func(this, pDataObj uintptr, grfKeyState uint32, ptX, ptY int32, pdwEffect *uint32) uintptr {
			if pdwEffect != nil {
				*pdwEffect = uint32(_DROPEFFECT_COPY)
			}
			return uintptr(_S_OK)
		}),

		// DragOver(this, grfKeyState, ptX, ptY, pdwEffect*)
		DragOver: syscall.NewCallback(func(this uintptr, grfKeyState uint32, ptX, ptY int32, pdwEffect *uint32) uintptr {
			if pdwEffect != nil {
				*pdwEffect = uint32(_DROPEFFECT_COPY)
			}
			return uintptr(_S_OK)
		}),

		DragLeave: syscall.NewCallback(func(this uintptr) uintptr {
			return uintptr(_S_OK)
		}),

		// Drop(this, pDataObj, grfKeyState, ptX, ptY, pdwEffect*)
		// Extract CF_HDROP paths from the data object and forward to onDrop.
		Drop: syscall.NewCallback(func(this, pDataObj uintptr, grfKeyState uint32, ptX, ptY int32, pdwEffect *uint32) uintptr {
			if pdwEffect != nil {
				*pdwEffect = uint32(_DROPEFFECT_COPY)
			}
			// unsafe.Pointer(pDataObj): pDataObj is a Windows COM IDataObject*, not a Go
			// heap pointer.  go vet's unsafeptr check fires here (same as Gio's own
			// os_windows.go WndProc handlers — see gioui.org@v0.10.0/app/os_windows.go
			// lines 362/375/460 which carry the identical warning and ship in release).
			// This is an intentional, unavoidable pattern for Win32/COM interop.
			if pDataObj != 0 {
				if paths, err := extractHDrop(unsafe.Pointer(pDataObj)); err == nil && len(paths) > 0 {
					// onDrop calls a.setTarget + a.window.Invalidate() — both concurrency-safe.
					onDrop(paths)
				}
			}
			return uintptr(_S_OK)
		}),
	}

	dt.vtbl = vtbl
	return dt
}

// ─── CF_HDROP extraction ──────────────────────────────────────────────────────

// comObj is a minimal COM object layout: the first machine-word is always the
// vtable pointer.  We use a fixed-size array vtable large enough for IDataObject
// (7 methods).  Casting a raw COM pointer (uintptr) to *comObj in one expression
// is valid under unsafe.Pointer rule 1 (conversion between pointer types).
type comObj struct {
	vtbl *[7]uintptr
}

// extractHDrop calls IDataObject::GetData (vtable slot 3) to retrieve a CF_HDROP
// HGLOBAL, then uses DragQueryFileW to enumerate the file paths it contains.
//
// pDataObj must be a valid IDataObject pointer received directly from Windows COM —
// it is NOT a Go heap pointer and will not be moved by the GC.  The caller converts
// the raw uintptr to unsafe.Pointer inside a syscall.NewCallback frame (where vet
// allows the conversion), then passes the unsafe.Pointer here.
func extractHDrop(pDataObj unsafe.Pointer) ([]string, error) {
	if pDataObj == nil {
		return nil, errors.New("extractHDrop: nil IDataObject")
	}

	// Cast to comObj to read the vtable (unsafe rule 1: pointer-to-pointer conversion).
	obj := (*comObj)(pDataObj)

	// FORMATETC: request CF_HDROP in an HGLOBAL (TYMED_HGLOBAL = 1<<1 = 2).
	fmtetc := formatEtc{
		cfFormat: _CF_HDROP,
		dwAspect: 1,  // DVASPECT_CONTENT
		lindex:   -1, // all items
		tymed:    1 << 1,
	}
	var stgmed stgMedium

	// Call IDataObject::GetData (vtable slot 3).
	// uintptr(pDataObj) is valid here: pDataObj is an unsafe.Pointer that will not
	// move between this line and the SyscallN call (it is a Windows object, not GC'd).
	r, _, _ := syscall.SyscallN(obj.vtbl[3],
		uintptr(pDataObj),
		uintptr(unsafe.Pointer(&fmtetc)),
		uintptr(unsafe.Pointer(&stgmed)),
	)
	if r != uintptr(_S_OK) {
		return nil, fmt.Errorf("IDataObject::GetData CF_HDROP: 0x%08X", r)
	}
	defer func() {
		procReleaseStgMedium.Call(uintptr(unsafe.Pointer(&stgmed)))
	}()

	hDrop := stgmed.hGlobal

	// DragQueryFileW(hDrop, 0xFFFFFFFF, nil, 0) returns the file count.
	nFiles, _, _ := procDragQueryFileW.Call(hDrop, _dragQueryCount, 0, 0)
	if nFiles == 0 {
		return nil, nil
	}

	buf := make([]uint16, 32768)
	paths := make([]string, 0, nFiles)
	for i := uintptr(0); i < nFiles; i++ {
		n, _, _ := procDragQueryFileW.Call(hDrop, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n > 0 {
			paths = append(paths, syscall.UTF16ToString(buf[:n]))
		}
	}
	return paths, nil
}

// ─── COM/OLE structs ─────────────────────────────────────────────────────────

// formatEtc mirrors the Windows SDK FORMATETC structure (amd64 layout).
// cfFormat(2)+_pad2(2)+_pad4(4)+ptd(8)+dwAspect(4)+lindex(4)+tymed(4) + trailing pad = 28 bytes.
type formatEtc struct {
	cfFormat uint16
	_        [2]byte // pad to 4-byte boundary
	_        uint32  // pad to 8-byte boundary for ptd
	ptd      uintptr // DVTARGETDEVICE* (null = any device)
	dwAspect uint32
	lindex   int32
	tymed    uint32
}

// stgMedium mirrors the Windows SDK STGMEDIUM structure (amd64 layout).
// tymed(4)+_pad(4)+hGlobal(8)+pUnkForRelease(8) = 24 bytes.
type stgMedium struct {
	tymed          uint32
	_              uint32  // pad so hGlobal is 8-byte aligned
	hGlobal        uintptr // HGLOBAL / HDROP (the union — we only use HGLOBAL)
	pUnkForRelease uintptr
}

// ─── testable pure helper ─────────────────────────────────────────────────────

// hdropPathsFromBuf parses a raw CF_HDROP DROPFILES buffer into file paths.
// This is the pure, headless-testable helper.  The actual COM call path goes through
// DragQueryFileW; this function exists so that CF_HDROP parsing logic can be unit-tested
// without a real Windows window.
//
// DROPFILES layout:
//
//	DWORD pFiles  — byte offset to the path list
//	POINT pt      — drop point (unused)
//	BOOL  fNC     — non-client flag (unused)
//	BOOL  fWide   — TRUE if paths are UTF-16LE
//	<paths>       — double-null-terminated path list (UTF-16LE when fWide)
func hdropPathsFromBuf(buf []byte) ([]string, error) {
	if len(buf) < 20 {
		return nil, errors.New("CF_HDROP buffer too short")
	}
	pFiles := uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
	fWide := uint32(buf[16]) | uint32(buf[17])<<8 | uint32(buf[18])<<16 | uint32(buf[19])<<24
	if fWide == 0 {
		return nil, errors.New("CF_HDROP: ANSI paths not supported")
	}
	if int(pFiles) >= len(buf) {
		return nil, errors.New("CF_HDROP: pFiles offset out of range")
	}
	raw := buf[pFiles:]
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		u16[i] = uint16(raw[i*2]) | uint16(raw[i*2+1])<<8
	}
	var paths []string
	for len(u16) > 0 {
		end := 0
		for end < len(u16) && u16[end] != 0 {
			end++
		}
		if end == 0 {
			break // double-null terminator
		}
		paths = append(paths, syscall.UTF16ToString(u16[:end]))
		if end+1 >= len(u16) {
			break
		}
		u16 = u16[end+1:]
	}
	return paths, nil
}
