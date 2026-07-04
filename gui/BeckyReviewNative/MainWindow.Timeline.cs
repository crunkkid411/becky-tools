using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Windows;
using WinForms = System.Windows.Forms;

namespace BeckyReviewNative;

// Native timeline wiring (Phase 1). Hosts a TimelineControl over the page's #timelineHole the
// same way mpv overlays #videoHole. The page pushes the reel + playhead + view; the control
// reports a compilation-seconds scrub back, which the page turns into a seek through its
// existing seekTimeline path (ONE seek path). Kept in a partial file so the shell stays thin.
public partial class MainWindow
{
    private TimelineControl? _timeline;

    [System.Runtime.InteropServices.DllImport("user32.dll")]
    private static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);

    private void StartTimeline()
    {
        try
        {
            _timeline = new TimelineControl { Dock = WinForms.DockStyle.Fill };
            // Mirror the mpv VideoHost structure EXACTLY (WindowsFormsHost -> Panel -> content):
            // hosting a bare Control as the Child composites differently over the WebView2.
            var panel = new WinForms.Panel { Dock = WinForms.DockStyle.Fill, BackColor = System.Drawing.Color.FromArgb(0x10, 0x10, 0x10) };
            panel.Controls.Add(_timeline);
            TimelineHostElement.Child = panel;

            // Scrub on the native timeline -> let the page do the seek (it owns the reel->file
            // mapping), exactly like a click on the DOM timeline.
            _timeline.ScrubRequested += comp => PostToPage(new { t = "timelineScrub", comp });
            _timeline.ViewChanged += (px, scroll) => PostToPage(new { t = "timelineView", pxPerSec = px, scroll });

            // A native surface steals wheel/focus from the WebView; hand it back so the page's
            // shortcuts keep working (same fix the mpv pane uses).
            _timeline.MouseEnter += (s, e) => { try { WebView.Focus(); } catch { } };
        }
        catch (Exception ex)
        {
            StatusLabel.Text = "Timeline pane failed: " + ex.Message;
        }
    }

    // Position the native timeline over the page's #timelineHole (CSS px == DIP at 100% zoom).
    private void HandleTimelineRect(JsonElement root)
    {
        var x = Num(root, "x"); var y = Num(root, "y");
        var w = Num(root, "w"); var h = Num(root, "h");
        Dispatcher.BeginInvoke(() =>
        {
            if (w < 2 || h < 2) { TimelineHost.Visibility = Visibility.Collapsed; return; }
            TimelineHost.Margin = new Thickness(x, y, 0, 0);
            TimelineHost.Width = w; TimelineHost.Height = h;
            TimelineHost.Visibility = Visibility.Visible;
            // Raise the hosted native window ABOVE the WebView2 (WPF airspace z-order between
            // multiple hosted HWNDs is unreliable; the WebView2 was covering this pane).
            try { var hwnd = TimelineHostElement.Handle; if (hwnd != IntPtr.Zero) { SetWindowPos(hwnd, IntPtr.Zero, 0, 0, 0, 0, 0x0001 | 0x0002 | 0x0010); } } catch { }
        });
    }

    // The page pushes its current reel + view. clips: [{start,dur,label}] in compilation seconds.
    private void HandleTimelineReel(JsonElement root)
    {
        if (_timeline == null) { return; }
        var clips = new List<TimelineControl.Clip>();
        if (root.TryGetProperty("clips", out var arr) && arr.ValueKind == JsonValueKind.Array)
        {
            foreach (var c in arr.EnumerateArray())
            {
                clips.Add(new TimelineControl.Clip(Num(c, "start"), Num(c, "dur"), Str(c, "label")));
            }
        }
        var px = Num(root, "pxPerSec");
        var scroll = Num(root, "scroll");
        Dispatcher.BeginInvoke(() =>
        {
            _timeline.SetClips(clips);
            if (px > 0) { _timeline.SetView(px, scroll); }
        });
    }

    // Toggle native vs DOM timeline. OFF => hide the pane (page shows its DOM timeline). ON =>
    // the page follows up with timelineRect + timelineReel, which reveal + fill it.
    private void HandleTimelineMode(JsonElement root)
    {
        var on = root.TryGetProperty("on", out var onEl) && onEl.ValueKind == JsonValueKind.True;
        if (!on)
        {
            Dispatcher.BeginInvoke(() => { TimelineHost.Visibility = Visibility.Collapsed; });
        }
    }
}
