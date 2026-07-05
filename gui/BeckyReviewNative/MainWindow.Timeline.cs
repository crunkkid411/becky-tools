using System;
using System.Diagnostics;
using System.IO;
using System.Text.Json;
using System.Threading.Tasks;
using System.Windows;
using WinForms = System.Windows.Forms;

namespace BeckyReviewNative;

// Native timeline: embeds becky-timeline.exe (the GPU timeline) as a CHILD window inside the
// TimelineHost pane, over the page's #timelineHole — the SAME airspace trick mpv uses for
// #videoHole (D3D11 FLIP-model child HWND composites above the WebView2 in its rectangle).
//
// This host is a dumb PIPE (mpv pattern: launch ONCE, keep it running, talk over stdio):
//   page -> host -> stdin :  {"op":"loadreel"| "seek"(quiet)| "vis"| "zoom"| "wheel", ...}
//   stdout -> host -> page:  {t:"tlEvent", json:"..."} — the page routes scrub/select/edit
//                            events to the SAME engine verbs the DOM timeline used.
// The Go engine stays the edit model; becky-timeline is the fast view/controller.
public partial class MainWindow
{
    private WinForms.Panel? _timelinePanel;
    private Process? _tlProc;
    private StreamWriter? _tlIn;
    private readonly object _tlWriteLock = new();
    private bool _tlMode;   // the page's "native" toggle state

    private void StartTimeline()
    {
        try
        {
            // Mirror the mpv VideoHost structure EXACTLY: a WinForms Panel whose HWND we hand to
            // the child process via --wid. (mpv works this way; a bare Control composited behind
            // the WebView2.)
            _timelinePanel = new WinForms.Panel { Dock = WinForms.DockStyle.Fill, BackColor = System.Drawing.Color.FromArgb(0x0A, 0x0A, 0x0A) };
            TimelineHostElement.Child = _timelinePanel;
            _ = _timelinePanel.Handle;   // realize the HWND now (like mpv's VideoHost)
            // Launch EARLY and keep it running (mpv pattern) so toggling native is instant and
            // reel pushes always have a live process. It idles (vis off) while hidden.
            TryLaunchTimeline();
        }
        catch (Exception ex) { StatusLabel.Text = "Timeline pane failed: " + ex.Message; }
    }

    private void TryLaunchTimeline()
    {
        if (_tlProc is { HasExited: false }) { return; }
        if (_timelinePanel == null) { return; }
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
                RedirectStandardInput = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,   // GStreamer is chatty; drain it or the pipe fills
                WorkingDirectory = Path.GetDirectoryName(exe)!,
            };
            psi.ArgumentList.Add("--wid");
            psi.ArgumentList.Add(_timelinePanel.Handle.ToInt64().ToString());
            psi.Environment["PATH"] = Path.Combine(gst, "bin") + ";" + Environment.GetEnvironmentVariable("PATH");
            psi.Environment["GST_PLUGIN_SYSTEM_PATH_1_0"] = Path.Combine(gst, "lib", "gstreamer-1.0");
            psi.Environment["GST_PLUGIN_FEATURE_RANK"] = "d3d11h264dec:512,d3d11h265dec:512";
            _tlProc = Process.Start(psi);
            if (_tlProc == null) { return; }
            _tlIn = _tlProc.StandardInput;
            _tlProc.EnableRaisingEvents = true;
            _tlProc.Exited += OnTimelineExited;
            var proc = _tlProc;
            _ = Task.Run(() => ReadTimelineStdout(proc));
            _ = Task.Run(() => { try { proc.StandardError.ReadToEnd(); } catch { /* drain */ } });
        }
        catch (Exception ex) { SetStatus("Native timeline: " + ex.Message); }
    }

    /// <summary>Relay every stdout NDJSON line (gesture events / AI state) to the page.</summary>
    private void ReadTimelineStdout(Process proc)
    {
        try
        {
            string? line;
            while ((line = proc.StandardOutput.ReadLine()) != null)
            {
                if (line.Length == 0 || line[0] != '{') { continue; }
                // ANY native mousedown: hand keyboard focus straight back to the WebView so
                // "click the timeline, then Spacebar" always works (12.3). The native pane
                // deliberately never takes focus (MA_NOACTIVATE), so without this a click
                // leaves the keyboard wherever it was — a search box, a WPF element, nowhere.
                if (line.Contains("\"ev\":\"pointer\""))
                {
                    Dispatcher.BeginInvoke(() => { try { WebView.Focus(); } catch { } });
                }
                PostToPage(new { t = "tlEvent", json = line });
            }
        }
        catch { /* pipe closed - the Exited handler reports it */ }
    }

    private void OnTimelineExited(object? sender, EventArgs e)
    {
        _tlIn = null;
        PostToPage(new { t = "tlDead" });   // page falls back to the DOM timeline + toasts
        Dispatcher.BeginInvoke(() => TimelineHost.Visibility = Visibility.Collapsed);
    }

    /// <summary>One NDJSON line to becky-timeline's stdin (thread-safe, best-effort).</summary>
    private void TlSend(object payload)
    {
        var w = _tlIn;
        if (w == null) { return; }
        try
        {
            var json = payload is JsonElement je ? je.GetRawText() : JsonSerializer.Serialize(payload);
            lock (_tlWriteLock) { w.WriteLine(json); w.Flush(); }
        }
        catch (Exception) { /* process died mid-write; Exited handles recovery */ }
    }

    // Position the pane over the page's #timelineHole (CSS px == DIP at 100% zoom), like the mpv pane.
    private void HandleTimelineRect(JsonElement root)
    {
        var x = Num(root, "x"); var y = Num(root, "y");
        var w = Num(root, "w"); var h = Num(root, "h");
        Dispatcher.BeginInvoke(() =>
        {
            if (!_tlMode || w < 2 || h < 2) { TimelineHost.Visibility = Visibility.Collapsed; return; }
            TimelineHost.Margin = new Thickness(x, y, 0, 0);
            TimelineHost.Width = w; TimelineHost.Height = h;
            TimelineHost.Visibility = Visibility.Visible;
            // Raise the hosted native window ABOVE the WebView2 (airspace z-order between hosted
            // HWNDs is unreliable) — SWP_NOMOVE|NOSIZE|NOACTIVATE.
            try { var hwnd = TimelineHostElement.Handle; if (hwnd != IntPtr.Zero) { SetWindowPos(hwnd, IntPtr.Zero, 0, 0, 0, 0, 0x0001 | 0x0002 | 0x0010); } } catch { }
        });
    }

    [System.Runtime.InteropServices.DllImport("user32.dll")]
    private static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);

    // The page pushes the current reel on EVERY timeline change (render). Forward it verbatim as a
    // live loadreel — becky-timeline tolerates an empty reel and never stomps a mid-drag gesture.
    private void HandleTimelineReel(JsonElement root) => TlSend(new { op = "loadreel", reel = root });

    // The app's playhead (mpv time reports) -> the native playhead, plus the secondary
    // STOCK bar (pause-return bookmark; flash = blinking while auditioning ahead).
    // quiet = no echo back.
    private void HandleTimelinePlayhead(JsonElement root)
        => TlSend(new
        {
            op = "seek",
            t = Num(root, "comp"),
            quiet = true,
            playing = root.TryGetProperty("playing", out var p) && p.ValueKind == JsonValueKind.True,
            stock = root.TryGetProperty("stock", out var st) && st.ValueKind == JsonValueKind.Number ? st.GetDouble() : -1,
            flash = root.TryGetProperty("flash", out var fl) && fl.ValueKind == JsonValueKind.True,
        });

    // Page-forwarded ops (wheel/zoom — the page really has keyboard+wheel focus). Passed verbatim.
    private void HandleTimelineOp(JsonElement root) => TlSend(root);

    // Toggle native vs DOM timeline. The process STAYS alive either way; off just hides the pane
    // and idles the render loop ({"op":"vis"}), so re-toggling is instant.
    private void HandleTimelineMode(JsonElement root)
    {
        var on = root.TryGetProperty("on", out var onEl) && onEl.ValueKind == JsonValueKind.True;
        _tlMode = on;
        if (on) { TryLaunchTimeline(); }   // relaunch if it crashed since startup
        TlSend(new { op = "vis", on });
        if (!on)
        {
            Dispatcher.BeginInvoke(() => TimelineHost.Visibility = Visibility.Collapsed);
        }
    }

    private void KillTimeline()
    {
        try { if (_tlProc is { HasExited: false }) { _tlProc.Exited -= OnTimelineExited; _tlProc.Kill(); } } catch { /* best-effort */ }
        _tlProc = null; _tlIn = null;
    }
}
