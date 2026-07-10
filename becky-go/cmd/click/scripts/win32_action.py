"""win32_action.py - generalized classic-Win32 name-based locate/click.

Ported from the PROVEN tools/mouse-control/pyauto_probe.py (hj-mission-control):
the already-installed pywinauto 0.6.9 win32 backend connects to a window by title
substring, finds a child control by its title (NAME) substring, and either reports
the window rect (--mode locate) or clicks it (--mode click). .click() posts a
BM_CLICK window message - no pixel coordinates, no synthetic mouse, works when the
window is not foreground. This covers the apps the managed System.Windows.Automation
InvokePattern cannot drive (they surface as bare Panes through the MSAA->UIA bridge).

Emits ONE compact JSON object on stdout; never raises to the caller (degrade, never
crash). ASCII-only. Run: python win32_action.py --window W --name N --mode locate|click
"""
import argparse
import json
import re
import sys


def emit(obj):
    print(json.dumps(obj))
    sys.exit(0)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--window", required=True)
    p.add_argument("--name", required=True)
    p.add_argument("--control-type", default="")  # accepted, unused by the win32 backend
    p.add_argument("--mode", required=True)
    a = p.parse_args()

    try:
        from pywinauto import Application
    except Exception as e:  # pywinauto missing -> degrade
        emit({"ok": False, "found": False, "error": "pywinauto unavailable: %s" % e})

    win_pat = ".*" + re.escape(a.window) + ".*"
    name_pat = ".*" + re.escape(a.name) + ".*"
    try:
        app = Application(backend="win32").connect(title_re=win_pat, timeout=10)
        dlg = app.window(title_re=win_pat)
        ctrl = dlg.child_window(title_re=name_pat)
        if not ctrl.exists():
            emit({"ok": False, "found": False, "error": "control not found by name"})
        rc = dlg.rectangle()  # WHOLE WINDOW rect so state labels are in frame for OCR
        rect = {"x": rc.left, "y": rc.top, "w": rc.right - rc.left, "h": rc.bottom - rc.top}
        if a.mode == "locate":
            emit({"ok": True, "found": True, "method": "win32", "rect": rect})
        ctrl.click()  # BM_CLICK - programmatic, coordinate-free
        emit({"ok": True, "found": True, "clicked": True, "method": "win32", "rect": rect})
    except Exception as e:
        emit({"ok": False, "found": False, "error": str(e)})


if __name__ == "__main__":
    main()
