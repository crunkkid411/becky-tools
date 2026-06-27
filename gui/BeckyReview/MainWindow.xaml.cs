using System;
using System.Globalization;
using System.IO;
using System.Text.Json;
using System.Threading.Tasks;
using System.Windows;
using Microsoft.Web.WebView2.Core;
using Microsoft.Win32;
using WinForms = System.Windows.Forms;

namespace BeckyReview;

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

    // Host-drawn forensic lower-third state (mpv osd-overlay). The overlay is sized in
    // the HOST/window coordinate space (mpv's OSD maps to the window, NOT the
    // letterbox-aware video rect), so it never drifts off-screen on a portrait/odd
    // aspect clip. _ovW/_ovH (the real video dims) are kept only as a fallback.
    private bool _overlayOn;
    private string _ovFile = "";
    private string _ovDate = "";
    private string _ovLink = "";
    private double _ovFps = 30;
    private int _ovW;
    private int _ovH;
    private int _hostW;   // the mpv window (videoHost) size in DIPs — the overlay canvas
    private int _hostH;
    private double _lastPos;

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

        var uiFolder = Path.Combine(AppContext.BaseDirectory, "ui");
        core.SetVirtualHostNameToFolderMapping(
            VirtualHost, uiFolder, CoreWebView2HostResourceAccessKind.Allow);

        core.WebMessageReceived += OnWebMessage;
        core.NavigationCompleted += OnNavigationCompleted;
        core.Navigate($"https://{VirtualHost}/index.html");

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
            var hwnd = _videoPanel.Handle;

            var mpvExe = Path.Combine(AppContext.BaseDirectory, "runtime", "mpv", "mpv.exe");
            _mpv = new MpvPlayer(mpvExe);
            _mpv.PositionChanged += OnMpvPosition;
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
        });
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
        }
    }

    private async Task HandleCallAsync(JsonElement root)
    {
        var id = root.TryGetProperty("id", out var idEl) ? idEl.GetString() : null;
        var verb = root.TryGetProperty("verb", out var vEl) ? vEl.GetString() ?? "" : "";
        object? args = root.TryGetProperty("args", out var aEl) && aEl.ValueKind != JsonValueKind.Undefined
            ? aEl
            : null;

        JsonElement reply = default;
        if (_engine != null)
        {
            reply = await _engine.CallAsync(verb, args);
        }
        PostReply(id, reply);
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
            case "seek":
                _ = _mpv.SeekAbsAsync(Num(root, "at"));
                break;
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
            case "overlay":
                _overlayOn = root.TryGetProperty("on", out var onEl) && onEl.ValueKind == JsonValueKind.True;
                var f = Str(root, "file");
                if (f.Length > 0) { _ovFile = Path.GetFileName(f); }
                _ovDate = Str(root, "date");
                _ovLink = Str(root, "link");
                var fps = Num(root, "fps");
                if (fps > 0) { _ovFps = fps; }
                if (_overlayOn) { UpdateOverlay(_lastPos); } else { ClearOverlay(); }
                break;
        }
    }

    /// <summary>Load+seek+play, then read the real video dimensions for an exactly-sized overlay.</summary>
    private async Task PlayAndMeasureAsync(string file, double at)
    {
        if (_mpv == null) { return; }
        await _mpv.PlayAtAsync(file, at, play: true);
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
    // CRITICAL: mpv's osd-overlay coordinate space (res_x/res_y) maps to the WINDOW
    // (the --wid host panel), NOT the letterbox-aware video rect. Passing the video's
    // own w/h here made the text drift far off-screen whenever the clip aspect didn't
    // match the panel (e.g. a portrait phone clip in a wide panel). So we render in the
    // HOST canvas: {\an1} bottom-left then always sits at the window's bottom-left, the
    // font is a fraction of the host HEIGHT (the predictable short dimension for a
    // lower-third), and the filename is truncated to the host WIDTH so line 1 can't run
    // off the right edge.

    private void UpdateOverlay(double pos)
    {
        // Overlay canvas = the host window. Fall back to the video dims, then 1280x720.
        var w = _hostW > 0 ? _hostW : (_ovW > 0 ? _ovW : 1280);
        var h = _hostH > 0 ? _hostH : (_ovH > 0 ? _ovH : 720);

        // Font ~1/22 of the host height, clamped to a sane on-screen range.
        var fs = Math.Min(40, Math.Max(13, (int)Math.Round(h / 22.0)));
        var meta2 = Math.Max(11, fs * 5 / 6);

        // Truncate the filename so it fits the host width at this font size (a glyph is
        // ~0.55*fs wide on average; leave a margin of ~2 glyphs each side).
        var glyph = Math.Max(1.0, fs * 0.55);
        var maxChars = Math.Max(8, (int)((w - 4 * glyph) / glyph));
        var name = _ovFile.Length > maxChars
            ? _ovFile.Substring(0, Math.Max(1, maxChars - 1)) + "…"
            : _ovFile;
        var line1 = AssEscape(name);
        var line2 = "ORIG TC " + Smpte(pos, _ovFps);
        var meta = "";
        if (_ovDate.Length > 0) { meta = _ovDate; }
        if (_ovLink.Length > 0) { meta = meta.Length > 0 ? meta + "   " + _ovLink : _ovLink; }

        // {\an1} bottom-left; outline for legibility; sized in the host window's space.
        var ass = "{\\an1}{\\bord2}{\\3c&H000000&}{\\fs" + fs + "}{\\1c&H14FF39&}" + line1 +
                  "\\N{\\1c&HFFFFFF&}" + line2;
        if (meta.Length > 0)
        {
            ass += "\\N{\\fs" + meta2 + "}{\\1c&HD7D7D7&}" + AssEscape(meta);
        }
        _ = _mpv!.SendAsync(default, "osd-overlay", 1, "ass-events", ass, w, h, 0, false, false);
    }

    private void ClearOverlay()
    {
        _ = _mpv!.SendAsync(default, "osd-overlay", 1, "none", "", 0, 0, 0, false, false);
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
            Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "BeckyReview");
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
        base.OnClosed(e);
    }
}
