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
/// LEFT  = WebView2 HTML UI (find / quotes / timeline / chat), loaded with NO server.
/// RIGHT = native mpv video (frame-exact, GPU-decoded; the overlay is drawn by mpv).
/// The page talks to the persistent engine (BeckyEngine) and the video (MpvPlayer)
/// only through host messages relayed here.
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

    // Host-drawn forensic lower-third state (mpv osd-overlay).
    private bool _overlayOn;
    private string _ovFile = "";
    private string _ovDate = "";
    private string _ovLink = "";
    private double _ovFps = 30;
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
    /// Verify/dev hook: once the UI has loaded, auto-open the folder named by
    /// BECKY_REVIEW_FOLDER (so the window is self-drivable without the native dialog).
    /// Normal use leaves it unset and the user clicks "Pick folder...".
    /// </summary>
    private async void OnNavigationCompleted(object? sender, CoreWebView2NavigationCompletedEventArgs e)
    {
        var auto = Environment.GetEnvironmentVariable("BECKY_REVIEW_FOLDER");
        if (string.IsNullOrWhiteSpace(auto) || !Directory.Exists(auto) || _engine == null)
        {
            return;
        }
        _folder = auto;
        var reply = await _engine.CallAsync("open_folder", new { folder = _folder });
        PostToPage(new { t = "folder", reply = ReplyOrError(reply) });
        StatusLabel.Text = BeckyEngine.Ok(reply) ? _folder! : "Could not open folder.";
    }

    /// <summary>Embed mpv into a black WinForms panel on the RIGHT and stream its position back.</summary>
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
        // PositionChanged fires on a background thread; marshal to the UI thread.
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
                    Dispatcher.BeginInvoke(() => VideoPlaceholder.Visibility = Visibility.Collapsed);
                    _ovFile = Path.GetFileName(file);
                    _paused = false;
                    _ = _mpv.PlayAtAsync(file, at, play: true);
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
                _ovFps = fps > 0 ? fps : 30;
                if (_overlayOn)
                {
                    UpdateOverlay(_lastPos);
                }
                else
                {
                    ClearOverlay();
                }
                break;
        }
    }

    // --- the forensic lower-third, drawn by mpv (ASS via osd-overlay) ------------

    private void UpdateOverlay(double pos)
    {
        var line1 = AssEscape(_ovFile);
        var line2 = "ORIG TC " + Smpte(pos, _ovFps);
        var meta = "";
        if (_ovDate.Length > 0) { meta = _ovDate; }
        if (_ovLink.Length > 0) { meta = meta.Length > 0 ? meta + "   " + _ovLink : _ovLink; }

        // {\an1} = bottom-left; outline for legibility over any footage.
        var ass = "{\\an1}{\\fs24}{\\bord2}{\\3c&H000000&}{\\1c&H14FF39&}" + line1 +
                  "\\N{\\1c&HFFFFFF&}" + line2;
        if (meta.Length > 0)
        {
            ass += "\\N{\\fs20}{\\1c&HD7D7D7&}" + AssEscape(meta);
        }
        _ = _mpv!.SendAsync(default, "osd-overlay", 1, "ass-events", ass, 0, 0, 0, false, false);
    }

    private void ClearOverlay()
    {
        _ = _mpv!.SendAsync(default, "osd-overlay", 1, "none", "", 0, 0, 0, false, false);
    }

    // --- folder pick (native) -> engine open_folder -> push to the page ----------

    private async void PickFolderButton_Click(object sender, RoutedEventArgs e)
    {
        var dlg = new OpenFolderDialog { Title = "Pick a case folder" };
        if (dlg.ShowDialog() != true)
        {
            return;
        }
        _folder = dlg.FolderName;
        StatusLabel.Text = "Indexing " + _folder + " ...";
        if (_engine == null)
        {
            StatusLabel.Text = "Engine not available.";
            return;
        }
        var reply = await _engine.CallAsync("open_folder", new { folder = _folder });
        PostToPage(new { t = "folder", reply = ReplyOrError(reply) });
        StatusLabel.Text = BeckyEngine.Ok(reply) ? _folder! : "Could not open folder.";
    }

    // --- helpers -----------------------------------------------------------------

    private void PostReply(string? id, JsonElement reply)
    {
        PostToPage(new { t = "reply", id, reply = ReplyOrError(reply) });
    }

    /// <summary>A safe reply value: the engine envelope, or a synthetic error if missing.</summary>
    private static object ReplyOrError(JsonElement reply)
    {
        if (reply.ValueKind == JsonValueKind.Object)
        {
            return reply;
        }
        return new { ok = false, error = "engine unavailable" };
    }

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

    /// <summary>SMPTE HH:MM:SS:FF at the source fps (frame-accurate forensic timecode).</summary>
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

    /// <summary>Escape text for an ASS event (backslash, braces, newlines).</summary>
    private static string AssEscape(string s)
        => s.Replace("\\", "\\\\").Replace("{", "\\{").Replace("}", "\\}").Replace("\r", "").Replace("\n", " ");

    protected override void OnClosed(EventArgs e)
    {
        _mpv?.Dispose();
        _engine?.Dispose();
        base.OnClosed(e);
    }
}
