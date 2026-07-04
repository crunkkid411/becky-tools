using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text.Json;
using System.Windows;
using WinForms = System.Windows.Forms;

namespace BeckyReviewNative;

// Native timeline: embeds becky-timeline.exe (the GStreamer d3d11 editor) as a CHILD window inside
// the TimelineHost pane, over the page's #timelineHole — the SAME airspace trick mpv uses for
// #videoHole (a child HWND renders above the WebView2 in its rectangle). This REPLACES the WebView2
// DOM timeline: while native mode is on the page hides .tlinner and the GPU pane fills the hole.
public partial class MainWindow
{
    private WinForms.Panel? _timelinePanel;
    private Process? _tlProc;
    private string? _tlReelPath;

    [System.Runtime.InteropServices.DllImport("user32.dll")]
    private static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);

    private void StartTimeline()
    {
        try
        {
            // Mirror the mpv VideoHost structure EXACTLY: a WinForms Panel whose HWND we hand to the
            // child process via --wid. (mpv works this way; a bare Control composited behind the WebView2.)
            _timelinePanel = new WinForms.Panel { Dock = WinForms.DockStyle.Fill, BackColor = System.Drawing.Color.FromArgb(0x0A, 0x0A, 0x0A) };
            TimelineHostElement.Child = _timelinePanel;
            _ = _timelinePanel.Handle;   // realize the HWND now (like mpv's VideoHost)
            _timelinePanel.MouseEnter += (s, e) => { try { WebView.Focus(); } catch { } };
        }
        catch (Exception ex) { StatusLabel.Text = "Timeline pane failed: " + ex.Message; }
    }

    // Position the pane over the page's #timelineHole (CSS px == DIP at 100% zoom), like the mpv pane.
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
            // Raise the hosted native window ABOVE the WebView2 (airspace z-order between hosted HWNDs
            // is unreliable; the WebView2 otherwise covers this pane) — SWP_NOMOVE|NOSIZE|NOACTIVATE.
            try { var hwnd = TimelineHostElement.Handle; if (hwnd != IntPtr.Zero) { SetWindowPos(hwnd, IntPtr.Zero, 0, 0, 0, 0, 0x0001 | 0x0002 | 0x0010); } } catch { }
        });
    }

    // Page pushes the current reel (source/in/out per clip). Write it, then (re)launch the embedded editor.
    private void HandleTimelineReel(JsonElement root)
    {
        if (_timelinePanel == null) { return; }
        var trackA = new List<object>();
        var sourceA = "";
        if (root.TryGetProperty("clips", out var arr) && arr.ValueKind == JsonValueKind.Array)
        {
            foreach (var c in arr.EnumerateArray())
            {
                var src = Str(c, "source");
                var inS = Num(c, "in"); var outS = Num(c, "out");
                if (string.IsNullOrEmpty(src) || outS <= inS) { continue; }
                if (sourceA.Length == 0) { sourceA = src; }
                trackA.Add(new { source = src, @in = inS, @out = outS });
            }
        }
        if (trackA.Count == 0) { return; }
        try
        {
            var reel = new { sourceA, trackA, trackB = Array.Empty<object>() };
            _tlReelPath ??= Path.Combine(Path.GetTempPath(), "becky_review_reel.json");
            File.WriteAllText(_tlReelPath, JsonSerializer.Serialize(reel));
        }
        catch { return; }
        Dispatcher.BeginInvoke(() => EnsureEmbeddedTimeline());
    }

    // Launch becky-timeline as a CHILD of the timeline panel. ponytail: launches once and doesn't
    // live-update the reel yet — re-toggling native re-reads it; add a stdin "reload" op when the
    // reel needs to track edits live.
    private void EnsureEmbeddedTimeline()
    {
        if (_tlProc is { HasExited: false }) { return; }
        if (_timelinePanel == null || _tlReelPath == null) { return; }
        var exe = ResolveNativeTimelineExe();
        if (exe == null) { SetStatus("becky-timeline.exe not found - build native/becky-timeline (_build.bat)."); return; }
        const string gst = @"C:\Program Files\gstreamer\1.0\msvc_x86_64";
        try
        {
            var psi = new ProcessStartInfo
            {
                FileName = exe,
                UseShellExecute = false,
                CreateNoWindow = true,
                WorkingDirectory = Path.GetDirectoryName(exe)!,
            };
            psi.ArgumentList.Add("--wid");
            psi.ArgumentList.Add(_timelinePanel.Handle.ToInt64().ToString());   // .Handle forces HWND creation
            psi.ArgumentList.Add("--reel");
            psi.ArgumentList.Add(_tlReelPath);
            psi.Environment["PATH"] = Path.Combine(gst, "bin") + ";" + Environment.GetEnvironmentVariable("PATH");
            psi.Environment["GST_PLUGIN_SYSTEM_PATH_1_0"] = Path.Combine(gst, "lib", "gstreamer-1.0");
            psi.Environment["GST_PLUGIN_FEATURE_RANK"] = "d3d11h264dec:512,d3d11h265dec:512";
            _tlProc = Process.Start(psi);
        }
        catch (Exception ex) { SetStatus("Native timeline: " + ex.Message); }
    }

    // Toggle native vs DOM timeline. OFF => hide the pane + kill the editor (page shows its DOM
    // timeline). ON => the page follows with timelineRect + timelineReel, which reveal + fill it.
    private void HandleTimelineMode(JsonElement root)
    {
        var on = root.TryGetProperty("on", out var onEl) && onEl.ValueKind == JsonValueKind.True;
        if (on) { return; }
        Dispatcher.BeginInvoke(() =>
        {
            TimelineHost.Visibility = Visibility.Collapsed;
            KillTimeline();
        });
    }

    private void KillTimeline()
    {
        try { if (_tlProc is { HasExited: false }) { _tlProc.Kill(); } } catch { /* best-effort */ }
        _tlProc = null;
    }
}
