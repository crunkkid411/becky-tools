using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;
using System.Threading.Tasks;
using System.Windows;
using Microsoft.Web.WebView2.Core;
using Microsoft.Win32;
using WinForms = System.Windows.Forms;

namespace BeckyReviewNative;

/// <summary>
/// Becky Review main window — a thin shell over becky-clip's engine + native mpv.
/// The WebView2 UI fills the window (find | video-hole | chat + timeline); the native
/// mpv pane is OVERLAID on the page's #videoHole, positioned from the page's
/// {t:"videoRect"} message. Page talks to the persistent engine (BeckyEngine) and
/// the video (MpvPlayer) only through host messages relayed here.
/// </summary>
public partial class MainWindow : Window
{
    private const string VirtualHost = "beckyreview.local";

    private BeckyEngine? _engine;
    private MpvPlayer? _mpv;
    private WinForms.Panel? _videoPanel;
    private bool _webReady;
    private bool _paused = true;
    private string? _folder;

    // Host-drawn forensic lower-third state (mpv osd-overlay). The overlay is drawn in
    // the window's coordinate space (mpv's OSD maps to the window, NOT the video rect),
    // but \pos'd onto the letterbox-aware video rect so it tracks the VIDEO and never
    // runs wider than it. _ovW/_ovH (the real video dims) drive that rect.
    private bool _overlayOn;
    private bool _ovShowName = true;   // the filename line is optional (Jordan) — off shows only Date/TC/link
    private string _ovFile = "";
    private string _ovDate = "";
    private string _ovLink = "";
    private double _ovFps = 30;
    private double _ovTcOffset = 0; // add to mpv pos -> SOURCE timecode (clip.in - clip.start_sec during EDL playback)
    private int _ovW;
    private int _ovH;
    private int _hostW;   // the mpv window (videoHost) size in DIPs — the overlay canvas
    private int _hostH;
    private double _lastPos;

    // --- caption preview (the .srt shown ON the video, osd id 3) -----------------
    // The page owns the cues (it loads/edits/writes the .srt); the host only DRAWS the
    // one that covers the current position, styled to match the burned-in render, and
    // owns the up/down DRAG that sets the placement — because the mpv pane is a native
    // window sitting ON TOP of the WebView, so the page never sees a mouse on the video.
    private readonly List<CapCue> _caps = new();
    private bool _capsOn;
    private int _capMarginV = 90;   // ASS MarginV in the 384x288 srt->ass script box
    private int _capLastIdx = -2;   // which cue is currently drawn (-1 = none); -2 = nothing drawn yet
    private CapDrag? _capDrag;      // an in-progress up/down placement drag on the video
    private bool _capDragMoved;     // that drag actually travelled -> swallow the trailing click
    private sealed record CapCue(double Start, double End, string Text);
    private sealed record CapDrag(int StartY, int StartMargin);

    // becky-subtitle's shipped look (internal/subs/style.go DefaultStyle), expressed in
    // the SAME 384x288 script box ffmpeg's srt->ass conversion uses — so the preview and
    // the burned-in render read the same. Outline is 1: Jordan judged cli-cut's 2 too heavy.
    private const double AssResY = 288.0;
    private const int CapFontSize = 12;
    private const int CapOutline = 1;
    private const int CapMarginMax = 240;   // 240/288 = ~83% up the frame

    public MainWindow()
    {
        InitializeComponent();
    }

    private async void Window_Loaded(object sender, RoutedEventArgs e)
    {
        try
        {
            await InitWebViewAsync();
        }
        catch (Exception ex)
        {
            StatusLabel.Text = "WebView2 failed to start";
            MessageBox.Show(
                "The UI (WebView2) could not start.\n\n" + ex.Message +
                "\n\nInstall the Microsoft Edge WebView2 Runtime, then reopen Becky Review.",
                "Becky Review", MessageBoxButton.OK, MessageBoxImage.Warning);
        }
        StartVideo();
        StartEngine();
        StartTimeline();
    }

    private async Task InitWebViewAsync()
    {
        var userData = Path.Combine(AppContext.BaseDirectory, "webview2-data");

        CoreWebView2Environment env;
        var cdpPort = Environment.GetEnvironmentVariable("BECKY_REVIEW_CDP_PORT");
        if (!string.IsNullOrWhiteSpace(cdpPort))
        {
            var opts = new CoreWebView2EnvironmentOptions
            {
                AdditionalBrowserArguments = $"--remote-debugging-port={cdpPort}",
            };
            env = await CoreWebView2Environment.CreateAsync(null, userData, opts);
        }
        else
        {
            env = await CoreWebView2Environment.CreateAsync(userDataFolder: userData);
        }
        await WebView.EnsureCoreWebView2Async(env);

        var core = WebView.CoreWebView2;
        core.Settings.AreDefaultContextMenusEnabled = false;
        core.Settings.IsStatusBarEnabled = false;
        core.Settings.AreDevToolsEnabled = true;
        // Disable the WebView's built-in Ctrl+wheel page zoom: zooming is reserved for
        // the TIMELINE (the page handles Ctrl+wheel over it), so Ctrl+scroll anywhere
        // else must leave the UI untouched rather than scaling the whole page.
        core.Settings.IsZoomControlEnabled = false;

        var uiFolder = Path.Combine(AppContext.BaseDirectory, "ui");
        core.SetVirtualHostNameToFolderMapping(
            VirtualHost, uiFolder, CoreWebView2HostResourceAccessKind.Allow);

        core.WebMessageReceived += OnWebMessage;
        core.NavigationCompleted += OnNavigationCompleted;
        core.Navigate($"https://{VirtualHost}/index.html");

        // External drag-and-drop (item 21): WebView2's own drop handling hides the file
        // paths, so disable it and let the WINDOW handle the drop (robust to the native
        // child-window airspace — a WebView-level handler can miss the drop). The paths
        // are posted to the page, which does the add (so it lands at the playhead).
        WebView.AllowExternalDrop = false;
        AllowDrop = true;
        PreviewDragOver += OnWebDragOver;
        Drop += OnWebDrop;

        // Whenever the pointer enters the WebView, make sure it holds focus — so the
        // timeline's mouse-wheel-to-zoom works "no matter what" (even right after the user
        // was interacting with the native mpv pane), not only after a click in the page.
        WebView.MouseEnter += (s, e) => { try { WebView.Focus(); } catch { } };

        // Belt-and-suspenders for the same class of bug: ANY mouse-wheel tick anywhere in
        // the window re-asserts WebView focus BEFORE the tick is handled (tunnelling
        // PreviewMouseWheel fires ahead of the page's own wheel listener). MouseEnter alone
        // only fires when the pointer CROSSES into the WebView's bounds, not on every
        // scroll, so a focus loss that happens without a fresh mouse-enter (e.g. a native
        // dialog closing, or Windows quietly handing focus elsewhere) could otherwise leave
        // scroll-to-zoom silently dead until an unrelated click "un-sticks" it.
        PreviewMouseWheel += (s, e) => { try { WebView.Focus(); } catch { } };

        _webReady = true;
        StatusLabel.Text = "Pick a case folder to begin.";
    }

    /// <summary>
    /// After the UI loads, auto-open the remembered folder (or the BECKY_REVIEW_FOLDER
    /// override) so Jordan doesn't re-pick it every launch.
    /// </summary>
    private async void OnNavigationCompleted(object? sender, CoreWebView2NavigationCompletedEventArgs e)
    {
        var auto = Environment.GetEnvironmentVariable("BECKY_REVIEW_FOLDER");
        if (string.IsNullOrWhiteSpace(auto))
        {
            auto = LoadLastFolder();
        }
        if (!string.IsNullOrWhiteSpace(auto) && Directory.Exists(auto))
        {
            await OpenFolderAsync(auto!);
        }
    }

    /// <summary>Embed mpv into a black WinForms panel and stream its position back.</summary>
    private void StartVideo()
    {
        try
        {
            _videoPanel = new WinForms.Panel
            {
                BackColor = System.Drawing.Color.Black,
                Dock = WinForms.DockStyle.Fill,
            };
            VideoHostElement.Child = _videoPanel;

            // Click the video pane to play/pause. mpv is embedded via --wid (it renders
            // INTO this panel and never receives the mouse itself), so the HOST panel
            // takes the click and toggles pause over IPC. The pause-observe then syncs
            // _paused + the page, so the timeline + transport stay consistent.
            // Drag a caption UP or DOWN on the video to set where EVERY caption sits
            // (Jordan: "Simply dragging a caption up or down should affect all captions
            // vertical placement"). Horizontal is centred and deliberately has no control.
            // Armed only while the caption preview is on, so ordinary review clicks on the
            // video still just play/pause.
            _videoPanel.MouseDown += (s, e) =>
            {
                _capDragMoved = false;
                _capDrag = (e.Button == WinForms.MouseButtons.Left && _capsOn && _caps.Count > 0)
                    ? new CapDrag(e.Y, _capMarginV)
                    : null;
            };
            _videoPanel.MouseMove += (s, e) =>
            {
                if (_capDrag is not { } d) { return; }
                var dy = d.StartY - e.Y;                       // dragging UP (smaller Y) lifts the captions
                if (!_capDragMoved && Math.Abs(dy) < 4) { return; }
                _capDragMoved = true;
                var dispH = PanelVideoHeight();
                var v = d.StartMargin + dy / Math.Max(1.0, dispH) * AssResY;
                _capMarginV = (int)Math.Round(Math.Clamp(v, 0, CapMarginMax));
                DrawCaption(_lastPos, force: true);            // the captions follow the hand live
                SetStatus("Captions " + (int)Math.Round(_capMarginV / AssResY * 100) + "% up from the bottom");
                // Report DURING the drag, not only on release: a MouseUp on this panel is
                // not guaranteed to arrive (mpv owns a child window over it and can swallow
                // the release), and losing it would silently lose the placement Jordan just
                // set. The page debounces the disk write, so a per-move post is cheap.
                PostToPage(new { t = "capMargin", v = _capMarginV });
            };
            _videoPanel.MouseUp += (s, e) =>
            {
                if (_capDrag == null) { return; }
                _capDrag = null;
                if (_capDragMoved) { PostToPage(new { t = "capMargin", v = _capMarginV }); }
            };

            _videoPanel.MouseClick += (s, e) =>
            {
                // A caption drag ends with a click on this panel too — don't also toggle
                // playback, or every height adjustment would start or stop the video.
                if (_capDragMoved) { _capDragMoved = false; return; }
                if (e.Button == WinForms.MouseButtons.Left && _mpv != null)
                {
                    _paused = !_paused;
                    _ = _mpv.SetPauseAsync(_paused);
                    // Clicking the native mpv pane steals focus from the WebView, after which
                    // the page stops receiving mouse-wheel events (so timeline scroll-to-zoom
                    // silently dies until the user clicks a page button). Hand focus straight
                    // back to the WebView so the wheel keeps working.
                    Dispatcher.BeginInvoke(() => { try { WebView.Focus(); } catch { } });
                }
            };

            var hwnd = _videoPanel.Handle;

            var mpvExe = Path.Combine(AppContext.BaseDirectory, "runtime", "mpv", "mpv.exe");
            _mpv = new MpvPlayer(mpvExe);
            _mpv.PositionChanged += OnMpvPosition;
            _mpv.PauseChanged += OnMpvPause;
            _mpv.VideoSizeChanged += OnMpvVideoSize;
            _mpv.Start(hwnd, null);
        }
        catch (Exception ex)
        {
            StatusLabel.Text = "Video pane failed: " + ex.Message;
        }
    }

    /// <summary>Spawn the persistent becky-clip engine (warm index + parse cache).</summary>
    private void StartEngine()
    {
        var bin = BeckyTools.BinDir;
        var exe = bin != null
            ? Path.Combine(bin, "becky-review-engine.exe")
            : "becky-review-engine.exe";
        _engine = new BeckyEngine(exe);
        try
        {
            _engine.Start();
        }
        catch (Exception ex)
        {
            StatusLabel.Text = "Engine not available: " + ex.Message;
        }
    }

    // --- mpv position -> page (timeline playhead) + live overlay timecode --------

    private void OnMpvPosition(double pos, double dur)
    {
        _lastPos = pos;
        Dispatcher.BeginInvoke(() =>
        {
            PostToPage(new { t = "time", pos, dur });
            if (_overlayOn)
            {
                UpdateOverlay(pos);
            }
            if (_capsOn) { DrawCaption(pos); }   // no-ops unless the visible cue changed
        });
    }

    // The real displayed video size from mpv (observed). This is what makes the
    // overlay fit the VIDEO instead of the whole window — without it, _ovW/_ovH stay
    // 0 (the one-shot read races the async load) and the overlay falls back to the
    // full window width, which is why the preview text ran wider than the video.
    private void OnMpvVideoSize(int w, int h)
    {
        Dispatcher.BeginInvoke(() =>
        {
            _ovW = w;
            _ovH = h;
            if (_overlayOn) { UpdateOverlay(_lastPos); }
            if (_capsOn) { DrawCaption(_lastPos, force: true); }   // re-fit to the new video rect
        });
    }

    // mpv's real pause state (changed by a command, the spacebar, OR a click on the
    // video). Keep our mirror in sync and tell the page so it knows when it is playing
    // — the timeline "play through to the next clip" logic depends on this.
    private void OnMpvPause(bool paused)
    {
        _paused = paused;
        PostToPage(new { t = "play", paused });
    }

    // --- page -> host messages ---------------------------------------------------

    private void OnWebMessage(object? sender, CoreWebView2WebMessageReceivedEventArgs e)
    {
        JsonElement root;
        try
        {
            using var doc = JsonDocument.Parse(e.WebMessageAsJson);
            root = doc.RootElement.Clone();
        }
        catch
        {
            return;
        }

        var t = root.TryGetProperty("t", out var tt) ? tt.GetString() : null;
        switch (t)
        {
            case "call":
                _ = HandleCallAsync(root);
                break;
            case "mpv":
                HandleMpv(root);
                break;
            case "videoRect":
                HandleVideoRect(root);
                break;
            case "timelineRect":
                HandleTimelineRect(root);
                break;
            case "timelineReel":
                HandleTimelineReel(root);
                break;
            case "timelinePlayhead":
                HandleTimelinePlayhead(root);
                break;
            case "timelineMode":
                HandleTimelineMode(root);
                break;
            case "tlOp":
                HandleTimelineOp(root);
                break;
            case "captions":
                HandleCaptions(root);
                break;
        }
    }

    // (The old "openNativeTimeline" separate-window launcher is GONE — a separate window
    // was a hard boundary for Jordan; the embedded pane in MainWindow.Timeline.cs is the
    // only native timeline now.)

    // becky-timeline.exe lives at <repo>/native/becky-timeline/ — walk up from the app dir.
    private static string? ResolveNativeTimelineExe()
    {
        try
        {
            var dir = AppContext.BaseDirectory;
            for (var i = 0; i < 10 && !string.IsNullOrEmpty(dir); i++)
            {
                var exe = Path.Combine(dir, "native", "becky-timeline", "becky-timeline.exe");
                if (File.Exists(exe)) { return exe; }
                dir = Directory.GetParent(dir)?.FullName;
            }
        }
        catch { /* degrade: caller shows "not found" */ }
        return null;
    }

    private void SetStatus(string text) => Dispatcher.BeginInvoke(() => StatusLabel.Text = text);

    // ---- external file drop (item 21) ----------------------------------------

    private void OnWebDragOver(object sender, DragEventArgs e)
    {
        e.Effects = e.Data.GetDataPresent(DataFormats.FileDrop) ? DragDropEffects.Copy : DragDropEffects.None;
        e.Handled = true;
    }

    // A video dragged in from ANY folder: post its path(s) to the page, which calls the
    // engine's add_external so the clip lands at the playhead (like a panel add) and shows
    // a toast. Posting to the page (not calling the engine here) keeps ONE add path.
    private async void OnWebDrop(object sender, DragEventArgs e)
    {
        e.Handled = true;
        if (!e.Data.GetDataPresent(DataFormats.FileDrop)) { return; }
        if (e.Data.GetData(DataFormats.FileDrop) is not string[] paths) { return; }

        // An EDIT dropped in (a Vegas .txt / Final Cut .xml export, or a reel .json)
        // loads as the timeline. Dropping one used to do nothing at all, silently,
        // because only video extensions were accepted.
        foreach (var p in paths)
        {
            if (!IsEditFile(p) && !IsReelFile(p)) { continue; }
            var reel = await ConvertEditIfNeededAsync(p);
            if (!string.IsNullOrEmpty(reel)) { PostToPage(new { t = "openReel", path = reel }); }
            return;
        }

        var vids = new List<string>();
        foreach (var p in paths)
        {
            if (IsVideoFile(p)) { vids.Add(p); }
        }
        if (vids.Count > 0) { PostToPage(new { t = "externalDrop", paths = vids }); }
    }

    private static readonly HashSet<string> VideoExts = new(StringComparer.OrdinalIgnoreCase)
    { ".mp4", ".mov", ".mkv", ".avi", ".webm", ".m4v", ".ts", ".m2ts", ".wmv", ".flv", ".mpg", ".mpeg" };
    private static bool IsVideoFile(string path) => VideoExts.Contains(Path.GetExtension(path));

    // An NLE edit export: Vegas "EDL TXT" or Final Cut Pro 7 XML.
    private static readonly HashSet<string> EditExts = new(StringComparer.OrdinalIgnoreCase)
    { ".txt", ".xml" };
    private static bool IsEditFile(string path) => EditExts.Contains(Path.GetExtension(path));
    private static bool IsReelFile(string path) =>
        string.Equals(Path.GetExtension(path), ".json", StringComparison.OrdinalIgnoreCase);

    /// <summary>
    /// Turns a Vegas/Final Cut edit export into a becky reel and returns the reel path.
    /// A path that is already a reel (or empty, i.e. the dialog was cancelled) comes
    /// straight back. The conversion is becky-otio --import; on failure the reason is
    /// shown in the status bar and "" is returned, so the caller loads nothing rather
    /// than a broken reel.
    /// </summary>
    private async Task<string> ConvertEditIfNeededAsync(string path)
    {
        if (string.IsNullOrWhiteSpace(path) || !IsEditFile(path)) { return path; }

        var bin = BeckyTools.BinDir;
        var exe = bin is null ? "becky-otio.exe" : Path.Combine(bin, "becky-otio.exe");
        SetStatus($"Converting {Path.GetFileName(path)}...");

        var outPath = Path.Combine(
            Path.GetDirectoryName(path) ?? ".",
            Path.GetFileNameWithoutExtension(path) + ".reel.json");
        try
        {
            var (code, stdout, stderr) = await BeckyTools.RunAsync(exe, new[] { "--import", path, "--out", outPath });
            if (code != 0 || !File.Exists(outPath))
            {
                var why = string.IsNullOrWhiteSpace(stderr) ? stdout : stderr;
                SetStatus($"Could not read that edit: {FirstLine(why)}");
                return "";
            }
            var clips = 0;
            try
            {
                using var doc = JsonDocument.Parse(stdout);
                if (doc.RootElement.TryGetProperty("clips", out var cEl)) { clips = cEl.GetInt32(); }
            }
            catch { /* the reel loaded; the count is only for the status line */ }
            SetStatus(clips > 0
                ? $"Loaded {clips} cuts from {Path.GetFileName(path)}"
                : $"Loaded {Path.GetFileName(path)}");
            return outPath;
        }
        catch (Exception ex)
        {
            SetStatus($"Could not read that edit: {ex.Message}");
            return "";
        }
    }

    private static string FirstLine(string s)
    {
        if (string.IsNullOrEmpty(s)) { return "unknown error"; }
        var i = s.IndexOfAny(new[] { '\r', '\n' });
        return (i >= 0 ? s[..i] : s).Trim();
    }

    private async Task HandleCallAsync(JsonElement root)
    {
        var id = root.TryGetProperty("id", out var idEl) ? idEl.GetString() : null;
        var verb = root.TryGetProperty("verb", out var vEl) ? vEl.GetString() ?? "" : "";
        object? args = root.TryGetProperty("args", out var aEl) && aEl.ValueKind != JsonValueKind.Undefined
            ? aEl
            : null;

        // Host-handled verbs: native Save/Open file dialogs. The engine is headless and
        // can't show a WPF dialog, and the old in-page window.prompt froze behind the
        // always-on-top mpv surface — so the host shows the dialog and replies with the
        // chosen path ({path:""} = cancelled). The page then calls save_reel/load_reel.
        if (verb == "save_dialog" || verb == "load_dialog")
        {
            var def = "";
            if (args is JsonElement ae && ae.ValueKind == JsonValueKind.Object
                && ae.TryGetProperty("default", out var dEl) && dEl.ValueKind == JsonValueKind.String)
            {
                def = dEl.GetString() ?? "";
            }
            var path = await ShowReelDialogAsync(verb == "save_dialog", def);
            if (verb == "load_dialog") { path = await ConvertEditIfNeededAsync(path); }
            PostToPage(new { t = "reply", id, reply = new { ok = true, data = new { path } } });
            return;
        }

        // Host-handled context-menu verbs: reveal a file in Explorer / copy text to the
        // clipboard. The headless engine can't touch the shell or the WPF clipboard, so
        // the host does it. Args: reveal_file {path}, copy_text {text}.
        if (verb == "reveal_file" || verb == "copy_text")
        {
            var val = "";
            if (args is JsonElement ce && ce.ValueKind == JsonValueKind.Object)
            {
                var key = verb == "reveal_file" ? "path" : "text";
                if (ce.TryGetProperty(key, out var vEl2) && vEl2.ValueKind == JsonValueKind.String)
                {
                    val = vEl2.GetString() ?? "";
                }
            }
            var ok = verb == "reveal_file" ? RevealInExplorer(val) : CopyToClipboard(val);
            PostToPage(new { t = "reply", id, reply = new { ok, data = new { } } });
            return;
        }

        // Host-handled caption sidecar I/O: the caption lane reads and writes the reel's
        // .srt directly. The engine's write_srt REGENERATES the file from the reel, which
        // would throw away Jordan's hand-typed wording and hand-dragged timings — so the
        // lane owns this file and the host is a plain text read/write.
        // Args: read_srt {path} -> {text}; write_srt_file {path, text} -> {path}.
        if (verb == "read_srt" || verb == "write_srt_file")
        {
            HandleSrtIo(id, verb, args as JsonElement?);
            return;
        }

        JsonElement reply = default;
        if (_engine != null)
        {
            reply = await _engine.CallAsync(verb, args);
        }
        PostReply(id, reply);
    }

    /// <summary>
    /// Read or write a caption sidecar for the timeline's caption lane. Bounded to .srt
    /// so a page bug can never overwrite a video or a reel; a missing file on read is a
    /// normal empty result (the lane just shows nothing), not an error.
    /// </summary>
    private void HandleSrtIo(string? id, string verb, JsonElement? argsEl)
    {
        string Arg(string key)
        {
            if (argsEl is not { ValueKind: JsonValueKind.Object } a) { return ""; }
            return a.TryGetProperty(key, out var v) && v.ValueKind == JsonValueKind.String ? v.GetString() ?? "" : "";
        }

        // Bounded to the two caption sidecars so a page bug can never overwrite a video,
        // a transcript, or a reel: the captions themselves (.srt) and the one global
        // vertical placement that travels beside them (.capstyle.json).
        var path = Arg("path");
        if (path.Length == 0 ||
            !(path.EndsWith(".srt", StringComparison.OrdinalIgnoreCase) ||
              path.EndsWith(".capstyle.json", StringComparison.OrdinalIgnoreCase)))
        {
            PostToPage(new { t = "reply", id, reply = new { ok = false, error = "caption path must be an .srt or .capstyle.json file" } });
            return;
        }

        try
        {
            if (verb == "read_srt")
            {
                var text = File.Exists(path) ? File.ReadAllText(path) : "";
                PostToPage(new { t = "reply", id, reply = new { ok = true, data = new { text, exists = File.Exists(path) } } });
                return;
            }
            var dir = Path.GetDirectoryName(path);
            if (!string.IsNullOrEmpty(dir)) { Directory.CreateDirectory(dir); }
            File.WriteAllText(path, Arg("text"));
            PostToPage(new { t = "reply", id, reply = new { ok = true, data = new { path } } });
        }
        catch (Exception ex)
        {
            PostToPage(new { t = "reply", id, reply = new { ok = false, error = ex.Message } });
        }
    }

    /// <summary>
    /// Show a native Save/Open dialog for a .reel.json file and return the chosen path
    /// ("" if cancelled). Runs on the UI thread. This replaces the old window.prompt,
    /// whose modal rendered behind the always-on-top native mpv pane and froze the page;
    /// it also spares Jordan from typing a full path.
    /// </summary>
    private Task<string> ShowReelDialogAsync(bool save, string def)
    {
        return Dispatcher.InvokeAsync(() =>
        {
            // Jordan edits in Vegas and opens the EXPORT, not a becky reel: "i STILL
            // can't load .txt or .xml files with the load button (it should be able to
            // fucking convert them)". So the edit formats come FIRST in the list and are
            // converted transparently on the way in - he must never have to run a tool
            // by hand to open his own edit.
            string filter = save
                ? "Becky reel (*.reel.json)|*.reel.json|JSON (*.json)|*.json|All files (*.*)|*.*"
                : "Edits and reels (*.txt;*.xml;*.json)|*.txt;*.xml;*.json"
                  + "|Vegas EDL text (*.txt)|*.txt"
                  + "|Final Cut / Premiere XML (*.xml)|*.xml"
                  + "|Becky reel (*.json)|*.json"
                  + "|All files (*.*)|*.*";
            string initialDir = "", fileName = "";
            try
            {
                if (!string.IsNullOrWhiteSpace(def))
                {
                    if (Directory.Exists(def))
                    {
                        initialDir = def;
                    }
                    else
                    {
                        var d = Path.GetDirectoryName(def);
                        if (!string.IsNullOrWhiteSpace(d) && Directory.Exists(d)) { initialDir = d!; }
                        fileName = Path.GetFileName(def);
                    }
                }
            }
            catch { /* best-effort defaults */ }

            if (save)
            {
                var dlg = new SaveFileDialog
                {
                    Title = "Save reel",
                    Filter = filter,
                    DefaultExt = "reel.json",
                    AddExtension = true,
                };
                if (!string.IsNullOrEmpty(initialDir)) { dlg.InitialDirectory = initialDir; }
                if (!string.IsNullOrEmpty(fileName)) { dlg.FileName = fileName; }
                return dlg.ShowDialog(this) == true ? dlg.FileName : "";
            }
            else
            {
                var dlg = new OpenFileDialog
                {
                    Title = "Load reel",
                    Filter = filter,
                    // Explicit (not relying on the implicit default): start scoped to
                    // *.reel.json only, so a folder full of videos/transcripts never
                    // shows as one long unsorted list — the sort Jordan wants is really
                    // "don't show me the whole folder", which the OS's native Open
                    // dialog can't otherwise be told (it has no app-controllable sort API).
                    FilterIndex = 1,
                    CheckFileExists = true,
                };
                if (!string.IsNullOrEmpty(initialDir)) { dlg.InitialDirectory = initialDir; }
                return dlg.ShowDialog(this) == true ? dlg.FileName : "";
            }
        }).Task;
    }

    /// <summary>Open Explorer with the file selected. Best-effort; returns false on failure.</summary>
    private static bool RevealInExplorer(string path)
    {
        try
        {
            if (string.IsNullOrWhiteSpace(path) || !File.Exists(path)) { return false; }
            Process.Start("explorer.exe", "/select,\"" + path + "\"");
            return true;
        }
        catch { return false; }
    }

    /// <summary>Copy text to the Windows clipboard on the UI thread. Best-effort.</summary>
    private bool CopyToClipboard(string text)
    {
        if (string.IsNullOrEmpty(text)) { return false; }   // WPF Clipboard.SetText throws on empty
        try
        {
            Dispatcher.Invoke(() => Clipboard.SetText(text));
            return true;
        }
        catch { return false; }
    }

    /// <summary>Position the native mpv pane over the page's #videoHole (CSS px == DIP at 100% zoom).</summary>
    private void HandleVideoRect(JsonElement root)
    {
        var x = Num(root, "x");
        var y = Num(root, "y");
        var w = Num(root, "w");
        var h = Num(root, "h");
        Dispatcher.BeginInvoke(() =>
        {
            if (w < 2 || h < 2)
            {
                VideoHost.Visibility = Visibility.Collapsed;
                return;
            }
            VideoHost.Margin = new Thickness(x, y, 0, 0);
            VideoHost.Width = w;
            VideoHost.Height = h;
            VideoHost.Visibility = Visibility.Visible;
            // The mpv --wid window IS this panel, so its size is the overlay's canvas.
            _hostW = (int)Math.Round(w);
            _hostH = (int)Math.Round(h);
            if (_overlayOn) { UpdateOverlay(_lastPos); }   // re-fit the lower-third to the new size
            if (_capsOn) { DrawCaption(_lastPos, force: true); }
        });
    }

    private void HandleMpv(JsonElement root)
    {
        if (_mpv == null)
        {
            return;
        }
        var op = root.TryGetProperty("op", out var opEl) ? opEl.GetString() : null;
        switch (op)
        {
            case "play":
            {
                var file = Str(root, "file");
                var at = Num(root, "at");
                if (!string.IsNullOrWhiteSpace(file))
                {
                    _ovFile = Path.GetFileName(file);
                    _paused = false;
                    _ = PlayAndMeasureAsync(file, at);
                }
                break;
            }
            case "loadAt":
            {
                // Navigate to a moment WITHOUT auto-playing: load + exact-seek, then hold
                // the frame paused. The timeline uses this so a click moves the playhead and
                // shows the frame but never starts playback (that's the ▶ / spacebar job).
                var file = Str(root, "file");
                var at = Num(root, "at");
                if (!string.IsNullOrWhiteSpace(file))
                {
                    _ovFile = Path.GetFileName(file);
                    _paused = true;
                    _ = PlayAndMeasureAsync(file, at, play: false);
                }
                break;
            }
            case "seek":
            {
                // fast=true (mid-scrub) = keyframe seek: instant on long-GOP sources; the
                // gesture's release sends a final exact seek to land frame-accurately.
                var fast = root.TryGetProperty("fast", out var fEl) && fEl.ValueKind == JsonValueKind.True;
                _ = fast ? _mpv.SeekFastAsync(Num(root, "at")) : _mpv.SeekAbsAsync(Num(root, "at"));
                break;
            }
            case "pause":
                _paused = true;
                _ = _mpv.SetPauseAsync(true);
                break;
            case "resume":
                _paused = false;
                _ = _mpv.SetPauseAsync(false);
                break;
            case "toggle":
                _paused = !_paused;
                _ = _mpv.SetPauseAsync(_paused);
                break;
            case "frame":
                _paused = true;
                _ = Num(root, "dir") < 0 ? _mpv.FrameBackStepAsync() : _mpv.FrameStepAsync();
                break;
            case "speed":
            {
                var sp = Num(root, "value");
                if (sp > 0) { _ = _mpv.SetSpeedAsync(sp); }
                break;
            }
            case "screenshot":
                _ = TakeScreenshotAsync();
                break;
            case "overlay":
                _overlayOn = root.TryGetProperty("on", out var onEl) && onEl.ValueKind == JsonValueKind.True;
                var f = Str(root, "file");
                if (f.Length > 0) { _ovFile = Path.GetFileName(f); }
                _ovDate = Str(root, "date");
                _ovLink = Str(root, "link");
                _ovTcOffset = Num(root, "tc_off");   // 0 for a single-source preview; set during EDL playback
                // The filename line is optional: honor showName (absent => shown, matching
                // the engine's ShowFilename default). Preview + render stay consistent.
                _ovShowName = !root.TryGetProperty("showName", out var snEl) || snEl.ValueKind != JsonValueKind.False;
                var fps = Num(root, "fps");
                if (fps > 0) { _ovFps = fps; }
                if (_overlayOn) { UpdateOverlay(_lastPos); } else { ClearOverlay(); }
                break;
        }
    }

    /// <summary>
    /// Screenshot the CURRENT preview frame exactly as shown (mpv's "window" flag
    /// captures the rendered window, including our forensic overlay if it's on) into
    /// the case folder's render/ dir — the same human-facing-output location as
    /// exports (cmd/clip's renderDir(), and already excluded from search/browse).
    /// Auto-increments Screenshot_0001.png, _0002.png, ... so a repeat never
    /// overwrites a previous capture. Degrades to a "failed" toast (via the null
    /// path reply) when no folder is open or the dir can't be created.
    /// </summary>
    private async Task TakeScreenshotAsync()
    {
        if (_mpv == null) { return; }
        var dir = string.IsNullOrWhiteSpace(_folder) ? Path.GetTempPath() : Path.Combine(_folder, "render");
        try { Directory.CreateDirectory(dir); }
        catch { PostToPage(new { t = "screenshot", path = (string?)null }); return; }
        var path = NextScreenshotPath(dir);
        await _mpv.SendAsync(default, "screenshot-to-file", path, "window");
        PostToPage(new { t = "screenshot", path });
    }

    /// <summary>"<dir>\Screenshot_NNNN.png" for the lowest 4-digit NNNN (>=1) that
    /// doesn't already exist, mirroring cmd/clip's nextSequencedPath naming.</summary>
    private static string NextScreenshotPath(string dir)
    {
        for (var n = 1; n <= 9999; n++)
        {
            var p = Path.Combine(dir, $"Screenshot_{n:D4}.png");
            if (!File.Exists(p)) { return p; }
        }
        return Path.Combine(dir, "Screenshot_9999.png");
    }

    /// <summary>Load+seek+play, then read the real video dimensions for an exactly-sized overlay.</summary>
    private async Task PlayAndMeasureAsync(string file, double at, bool play = true)
    {
        if (_mpv == null) { return; }
        await _mpv.PlayAtAsync(file, at, play: play);
        try
        {
            var w = await _mpv.GetPropertyAsync("width");
            var h = await _mpv.GetPropertyAsync("height");
            var fps = await _mpv.GetPropertyAsync("container-fps");
            if (w.ValueKind == JsonValueKind.Number) { _ovW = w.GetInt32(); }
            if (h.ValueKind == JsonValueKind.Number) { _ovH = h.GetInt32(); }
            if (fps.ValueKind == JsonValueKind.Number) { var v = fps.GetDouble(); if (v > 0) { _ovFps = v; } }
        }
        catch { /* dims are best-effort; overlay falls back to 1280x720 */ }
    }

    // --- the forensic lower-third, drawn by mpv (ASS via osd-overlay) ------------
    // mpv's osd-overlay coordinate space (res_x/res_y) maps to the WINDOW (the --wid
    // host panel), NOT the letterbox-aware video rect — so res stays the window size
    // (passing the video's own w/h here once made the text drift off-screen). To keep
    // the lower-third aligned to the VIDEO and never wider than it, we work out where
    // the video actually sits inside the window (letterbox/pillarbox), \pos the text at
    // that rect's bottom-left, and scale each line's font to the video's displayed
    // WIDTH (a detective needs the whole name/URL, so we shrink rather than truncate;
    // the floor keeps it legible — widen the panel if a line ends up small).

    /// <summary>
    /// Where the VIDEO really sits inside the mpv window (letterbox/pillarbox), in mpv's
    /// OSD coordinate space — which maps to the WINDOW, not the picture. Both the
    /// forensic lower-third and the caption preview anchor to this rect so they track the
    /// video instead of the window. Falls back to the whole window until the real dims
    /// are known (they arrive as an observed property, not a racy one-shot read).
    /// </summary>
    private (int W, int H, int DispW, int DispH, int XOff, int YOff) VideoRect()
    {
        var w = _hostW > 0 ? _hostW : (_ovW > 0 ? _ovW : 1280);
        var h = _hostH > 0 ? _hostH : (_ovH > 0 ? _ovH : 720);
        int dispW = w, dispH = h, xoff = 0, yoff = 0;
        if (_ovW > 0 && _ovH > 0 && h > 0)
        {
            double videoAspect = (double)_ovW / _ovH, winAspect = (double)w / h;
            if (winAspect > videoAspect) { dispH = h; dispW = (int)Math.Round(h * videoAspect); xoff = (w - dispW) / 2; }
            else { dispW = w; dispH = (int)Math.Round(w / videoAspect); yoff = (h - dispH) / 2; }
        }
        return (w, h, dispW, dispH, xoff, yoff);
    }

    private void UpdateOverlay(double pos)
    {
        var (w, h, dispW, dispH, xoff, yoff) = VideoRect();

        // MATCH THE RENDER (drawtext.go): white monospace lines on a semi-transparent black
        // panel. The render draws 42px text on the source-resolution frame; scale that fixed
        // px to the video's DISPLAYED size so the preview reads the SAME as the burned
        // overlay (the old preview scaled to the window and came out ~4x too big + coloured).
        double scaleY = _ovH > 0 ? (double)dispH / _ovH : dispH / 1000.0;
        double scaleX = _ovW > 0 ? (double)dispW / _ovW : dispW / 1000.0;
        int fontSize = Math.Max(12, (int)Math.Round(42 * scaleY)); // render ltFontSize = 42
        int lineStep = Math.Max(fontSize + 2, (int)Math.Round(58 * scaleY)); // render ltLineH = 58
        int marginX = Math.Max(6, (int)Math.Round(20 * scaleX));   // render ltMarginX = 20
        int botPad = Math.Max(8, (int)Math.Round(61 * scaleY));    // render ltBottomPad = 61

        // yt-dlp provenance recovery (date/link from the file name) when no sidecar supplied it.
        var date = _ovDate.Length > 0 ? _ovDate : DateFromName(_ovFile);
        var link = _ovLink.Length > 0 ? _ovLink : LinkFromName(_ovFile);
        var tcLine = "ORIG TC " + Smpte(pos + _ovTcOffset, _ovFps);
        var dateLine = date.Length > 0 ? "Date: " + date + " UTC" : "";

        // Display lines top -> bottom (Date, ORIG TC, filename, link), each wrapped to the
        // video width — the render's exact order + wrapping.
        var lines = new List<string>();
        void Add(string t) { if (t.Length > 0) { foreach (var s in WrapToWidth(t, fontSize, dispW)) { lines.Add(s); } } }
        Add(dateLine);
        Add(tcLine);
        if (_ovShowName) { Add(_ovFile); }
        Add(link);
        if (lines.Count == 0) { ClearOverlay(); return; }

        int px = xoff + marginX;
        int py = yoff + dispH - botPad;   // the text block's bottom-left (\an1 grows upward)
        int n = lines.Count;

        // Panel (osd id 1, painted UNDER the text): one semi-transparent black rectangle
        // behind the whole block — the render uses per-line boxes@0.6; a single snug panel
        // reads the same at this size and is reliable in libass. Generous pad so it covers.
        int widest = 0;
        foreach (var s in lines) { widest = Math.Max(widest, s.Length); }
        int blockW = (int)Math.Round(widest * fontSize * 0.55); // monospace glyph ~0.55*font
        int pad = Math.Max(4, fontSize / 5);
        int bx1 = px - pad, by1 = py - n * lineStep - pad, bx2 = px + blockW + pad, by2 = py + pad;
        var box = new StringBuilder();
        box.Append("{\\an7}{\\pos(0,0)}{\\bord0}{\\1c&H000000&}{\\1a&H66&}{\\p1}")   // 0x66 alpha = ~60% opaque (render box@0.6)
           .Append("m ").Append(bx1).Append(' ').Append(by1)
           .Append(" l ").Append(bx2).Append(' ').Append(by1)
           .Append(' ').Append(bx2).Append(' ').Append(by2)
           .Append(' ').Append(bx1).Append(' ').Append(by2)
           .Append("{\\p0}");
        _ = _mpv!.SendAsync(default, "osd-overlay", 1, "ass-events", box.ToString(), w, h, 0, false, false);

        // Text (osd id 2, ON TOP — mpv paints higher ids above lower ones): white Consolas,
        // \N-stacked, bottom-left anchored at the video rect.
        var sb = new StringBuilder();
        sb.Append("{\\an1}{\\fnConsolas}{\\pos(").Append(px).Append(',').Append(py)
          .Append(")}{\\bord1}{\\3c&H000000&}{\\1c&HFFFFFF&}{\\fs").Append(fontSize).Append('}');
        for (int i = 0; i < n; i++) { if (i > 0) { sb.Append("\\N"); } sb.Append(AssEscape(lines[i])); }
        _ = _mpv!.SendAsync(default, "osd-overlay", 2, "ass-events", sb.ToString(), w, h, 0, false, false);
    }

    // Inset (px) from the video's bottom edge to the lowest overlay line, so the
    // lower-third isn't cramped against the very bottom (awkward to read).
    private const int OverlayBottomPad = 28;

    // WrapToWidth splits text into lines that each fit widthPx at fontSize, breaking
    // on spaces where it can and HARD-breaking an over-long token (a long filename or
    // URL with no spaces) so nothing is ever clipped. Empty text -> no lines.
    private static List<string> WrapToWidth(string text, int fontSize, int widthPx)
    {
        var lines = new List<string>();
        if (string.IsNullOrEmpty(text)) { return lines; }
        var maxChars = Math.Max(8, (int)((widthPx - 32) / (fontSize * 0.55)));
        if (text.Length <= maxChars) { lines.Add(text); return lines; }
        var cur = new StringBuilder();
        foreach (var word in text.Split(new[] { ' ' }, StringSplitOptions.RemoveEmptyEntries))
        {
            var wr = word;
            while (wr.Length > maxChars) // hard-break a token longer than a whole line
            {
                if (cur.Length > 0) { lines.Add(cur.ToString()); cur.Clear(); }
                lines.Add(wr.Substring(0, maxChars));
                wr = wr.Substring(maxChars);
            }
            if (cur.Length == 0) { cur.Append(wr); }
            else if (cur.Length + 1 + wr.Length <= maxChars) { cur.Append(' ').Append(wr); }
            else { lines.Add(cur.ToString()); cur.Clear(); cur.Append(wr); }
        }
        if (cur.Length > 0) { lines.Add(cur.ToString()); }
        if (lines.Count == 0) { lines.Add(text); }
        return lines;
    }

    // --- yt-dlp filename provenance ---------------------------------------------
    // yt-dlp embeds the upload date and video id in the file name, e.g.
    // "2026-06-27_Some Title_[abcdefghijk].mp4". These recover the overlay's Date
    // and Link when no becky sidecar provided them (the sidecar still wins above).

    // A leading recording-date prefix: dashed "2026-06-27_" or compact "20260627_".
    private static readonly Regex DatePrefixRe =
        new(@"^(?:(\d{4})-(\d{2})-(\d{2})|(\d{4})(\d{2})(\d{2}))[_ -]", RegexOptions.Compiled);

    // The bracketed 11-char YouTube id token, e.g. "[abcdefghijk]" (case-sensitive).
    private static readonly Regex YtIdRe =
        new(@"\[([A-Za-z0-9_-]{11})\]", RegexOptions.Compiled);

    private static string DateFromName(string name)
    {
        var m = DatePrefixRe.Match(name);
        if (!m.Success) { return ""; }
        return m.Groups[1].Success
            ? m.Groups[1].Value + "-" + m.Groups[2].Value + "-" + m.Groups[3].Value
            : m.Groups[4].Value + "-" + m.Groups[5].Value + "-" + m.Groups[6].Value;
    }

    private static string LinkFromName(string name)
    {
        var m = YtIdRe.Match(name);
        return m.Success ? "https://www.youtube.com/watch?v=" + m.Groups[1].Value : "";
    }

    private void ClearOverlay()
    {
        // The lower-third now uses two overlays (1 = black panel, 2 = white text) — clear both.
        _ = _mpv!.SendAsync(default, "osd-overlay", 1, "none", "", 0, 0, 0, false, false);
        _ = _mpv!.SendAsync(default, "osd-overlay", 2, "none", "", 0, 0, 0, false, false);
    }

    // --- the caption preview (osd id 3, ABOVE the lower-third) --------------------
    // The page owns the .srt — it loads, edits and writes it. The host only draws the
    // cue covering the current position, in the burned-in render's style, and owns the
    // up/down DRAG that moves every caption: the mpv pane is a native window ON TOP of
    // the WebView, so a mouse on the video never reaches the page at all.

    /// <summary>Page pushed a new cue list / placement / on-off. Redraw immediately.</summary>
    private void HandleCaptions(JsonElement root)
    {
        _capsOn = root.TryGetProperty("on", out var onEl) && onEl.ValueKind == JsonValueKind.True;
        if (root.TryGetProperty("marginV", out var mEl) && mEl.ValueKind == JsonValueKind.Number
            && mEl.TryGetDouble(out var mv))
        {
            _capMarginV = (int)Math.Round(Math.Clamp(mv, 0, CapMarginMax));
        }
        _caps.Clear();
        if (root.TryGetProperty("cues", out var cEl) && cEl.ValueKind == JsonValueKind.Array)
        {
            foreach (var it in cEl.EnumerateArray())
            {
                if (it.ValueKind != JsonValueKind.Object) { continue; }
                double s = Num(it, "s"), e = Num(it, "e");
                if (e > s) { _caps.Add(new CapCue(s, e, Str(it, "t"))); }
            }
        }
        if (_capsOn) { DrawCaption(_lastPos, force: true); } else { ClearCaption(); }
    }

    /// <summary>Index of the cue covering pos, or -1 between cues.</summary>
    private int CapIndexAt(double pos)
    {
        for (var i = 0; i < _caps.Count; i++)
        {
            if (pos >= _caps[i].Start && pos < _caps[i].End) { return i; }
        }
        return -1;
    }

    /// <summary>
    /// Draw the caption for <paramref name="pos"/> exactly as the render burns it: white
    /// fill, thin black outline, no shadow, centred, lifted MarginV off the bottom. The
    /// style numbers live in the 384x288 script box ffmpeg's srt->ass conversion uses, so
    /// they are scaled by the video's DISPLAYED height to preview at the same size.
    /// Only talks to mpv when the visible cue actually changes (a position tick fires
    /// several times a second; re-sending the same ASS would be pure churn).
    /// </summary>
    private void DrawCaption(double pos, bool force = false)
    {
        if (_mpv == null) { return; }
        if (!_capsOn) { ClearCaption(); return; }
        var idx = CapIndexAt(pos);
        if (!force && idx == _capLastIdx) { return; }
        _capLastIdx = idx;
        if (idx < 0) { ClearCaption(keepIndex: true); return; }   // between cues: blank, as the render is

        var r = VideoRect();
        var k = r.DispH / AssResY;                                 // script units -> displayed px
        var fontPx = Math.Max(10, (int)Math.Round(CapFontSize * k));
        var marginPx = (int)Math.Round(_capMarginV * k);
        var bord = Math.Max(1.0, Math.Round(CapOutline * k, 1));
        var cx = r.XOff + r.DispW / 2;                             // centred: no horizontal control by design
        var cy = r.YOff + r.DispH - marginPx;                      // \an2 = the text's bottom edge

        var sb = new StringBuilder();
        sb.Append("{\\an2}{\\fnProximaNova-Semibold}{\\pos(").Append(cx).Append(',').Append(cy).Append(")}")
          .Append("{\\1c&HFFFFFF&}{\\3c&H000000&}{\\shad0}{\\bord")
          .Append(bord.ToString(CultureInfo.InvariantCulture))
          .Append("}{\\fs").Append(fontPx).Append('}');
        var lines = _caps[idx].Text.Replace("\r", "").Split('\n');
        for (var i = 0; i < lines.Length; i++)
        {
            if (i > 0) { sb.Append("\\N"); }
            sb.Append(AssEscape(lines[i]));
        }
        _ = _mpv.SendAsync(default, "osd-overlay", 3, "ass-events", sb.ToString(), r.W, r.H, 0, false, false);
    }

    private void ClearCaption(bool keepIndex = false)
    {
        if (!keepIndex) { _capLastIdx = -2; }
        _ = _mpv?.SendAsync(default, "osd-overlay", 3, "none", "", 0, 0, 0, false, false);
    }

    /// <summary>
    /// The video's DISPLAYED height inside the mpv panel, in the PANEL's own pixels — so
    /// an up/down caption drag converts pixels to placement without any DIP/DPI step
    /// (the OSD rect above is in DIPs; a mouse event here is not).
    /// </summary>
    private double PanelVideoHeight()
    {
        if (_videoPanel == null) { return 0; }
        double ph = _videoPanel.ClientSize.Height, pw = _videoPanel.ClientSize.Width;
        if (ph <= 0) { return 0; }
        if (_ovW <= 0 || _ovH <= 0) { return ph; }
        double videoAspect = (double)_ovW / _ovH;
        return (pw / ph) > videoAspect ? ph : pw / videoAspect;
    }

    // --- folder open (native pick OR remembered) ---------------------------------

    private async void PickFolderButton_Click(object sender, RoutedEventArgs e)
    {
        var dlg = new OpenFolderDialog { Title = "Pick a case folder" };
        if (dlg.ShowDialog() != true)
        {
            return;
        }
        await OpenFolderAsync(dlg.FolderName);
    }

    private async Task OpenFolderAsync(string folder)
    {
        _folder = folder;
        StatusLabel.Text = "Indexing " + folder + " ...";
        if (_engine == null)
        {
            StatusLabel.Text = "Engine not available.";
            return;
        }
        var reply = await _engine.CallAsync("open_folder", new { folder });
        PostToPage(new { t = "folder", reply = ReplyOrError(reply) });
        if (BeckyEngine.Ok(reply))
        {
            StatusLabel.Text = folder;
            SaveLastFolder(folder);
        }
        else
        {
            StatusLabel.Text = "Could not open folder.";
        }
    }

    // --- last-folder persistence -------------------------------------------------

    private static string SettingsPath()
    {
        var dir = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "BeckyReviewNative");
        Directory.CreateDirectory(dir);
        return Path.Combine(dir, "last-folder.txt");
    }

    private static void SaveLastFolder(string folder)
    {
        try { File.WriteAllText(SettingsPath(), folder); } catch { /* best-effort */ }
    }

    private static string? LoadLastFolder()
    {
        try
        {
            var p = SettingsPath();
            return File.Exists(p) ? File.ReadAllText(p).Trim() : null;
        }
        catch { return null; }
    }

    // --- helpers -----------------------------------------------------------------

    private void PostReply(string? id, JsonElement reply)
        => PostToPage(new { t = "reply", id, reply = ReplyOrError(reply) });

    private static object ReplyOrError(JsonElement reply)
        => reply.ValueKind == JsonValueKind.Object ? reply : new { ok = false, error = "engine unavailable" };

    private void PostToPage(object payload)
    {
        if (!_webReady)
        {
            return;
        }
        var json = JsonSerializer.Serialize(payload);
        if (Dispatcher.CheckAccess())
        {
            WebView.CoreWebView2.PostWebMessageAsJson(json);
        }
        else
        {
            Dispatcher.BeginInvoke(() => WebView.CoreWebView2.PostWebMessageAsJson(json));
        }
    }

    private static string Str(JsonElement root, string name)
        => root.TryGetProperty(name, out var el) && el.ValueKind == JsonValueKind.String ? el.GetString() ?? "" : "";

    private static double Num(JsonElement root, string name)
        => root.TryGetProperty(name, out var el) && el.ValueKind == JsonValueKind.Number && el.TryGetDouble(out var v) ? v : 0;

    private static string Smpte(double seconds, double fps)
    {
        if (seconds < 0) { seconds = 0; }
        if (fps <= 0) { fps = 30; }
        var total = (int)Math.Floor(seconds);
        var h = total / 3600;
        var m = (total % 3600) / 60;
        var s = total % 60;
        var fr = (int)Math.Floor((seconds - total) * fps);
        return string.Format(CultureInfo.InvariantCulture, "{0:00}:{1:00}:{2:00}:{3:00}", h, m, s, fr);
    }

    private static string AssEscape(string s)
        => s.Replace("\\", "\\\\").Replace("{", "\\{").Replace("}", "\\}").Replace("\r", "").Replace("\n", " ");

    protected override void OnClosed(EventArgs e)
    {
        _mpv?.Dispose();
        _engine?.Dispose();
        KillTimeline();
        base.OnClosed(e);
    }
}
