using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Input;
using Microsoft.Web.WebView2.Core;
using Microsoft.Win32;
using WinForms = System.Windows.Forms;

namespace BeckyReview;

/// <summary>
/// Becky Review main window.
/// LEFT  = WebView2 HTML UI (virtual-host mapping, NO server) - AI-authorable + CDP-verifiable.
/// RIGHT = native libmpv video pane (mpv.exe embedded via --wid) - frame-exact, GPU-decoded.
/// Shells out to existing becky-*.exe (JSON in/out) - engine unchanged.
/// </summary>
public partial class MainWindow : Window
{
    // The virtual host the local HTML is served from (no TCP server involved).
    private const string VirtualHost = "beckyreview.local";

    private string? _folder;
    private bool _webReady;

    private MpvPlayer? _mpv;
    private WinForms.Panel? _videoPanel;
    private bool _isPaused;

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
                "The left pane (WebView2) could not start.\n\n" + ex.Message +
                "\n\nInstall the Microsoft Edge WebView2 Runtime, then reopen Becky Review.",
                "Becky Review", MessageBoxButton.OK, MessageBoxImage.Warning);
        }

        StartVideo();

        // Dev/verify hook: auto-load a folder (and optionally run a search) on startup so
        // the window is self-verifiable without clicking the native dialog. Normal use
        // leaves these unset and Jordan clicks "Pick folder...".
        var autoFolder = Environment.GetEnvironmentVariable("BECKY_REVIEW_FOLDER");
        if (!string.IsNullOrWhiteSpace(autoFolder) && Directory.Exists(autoFolder))
        {
            _folder = autoFolder;
            await LoadFolderAsync();
            var autoSearch = Environment.GetEnvironmentVariable("BECKY_REVIEW_SEARCH");
            if (!string.IsNullOrWhiteSpace(autoSearch))
            {
                await RunSearchAsync(autoSearch);
            }
        }
    }

    /// <summary>
    /// Boot WebView2, map the local <c>ui</c> folder to a virtual host (no server),
    /// and load the review UI. Sets up the page-to-host message channel.
    /// </summary>
    private async Task InitWebViewAsync()
    {
        // Keep the WebView2 profile beside the exe so the app stays offline + deterministic.
        var userData = Path.Combine(AppContext.BaseDirectory, "webview2-data");

        // Opt-in CDP for the AI self-verify loop (Step 7): set BECKY_REVIEW_CDP_PORT to a
        // port to let an external agent (Playwright connectOverCDP) drive + screenshot the
        // LEFT UI. Off by default - no debug surface is exposed in normal use.
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

        // Lock it down: it only ever loads our local UI.
        core.Settings.AreDefaultContextMenusEnabled = false;
        core.Settings.IsStatusBarEnabled = false;
        core.Settings.AreDevToolsEnabled = true; // needed for the Step 7 CDP self-verify loop

        // Serve the local ui/ folder over a real https:// origin with NO server process.
        var uiFolder = Path.Combine(AppContext.BaseDirectory, "ui");
        core.SetVirtualHostNameToFolderMapping(
            VirtualHost, uiFolder, CoreWebView2HostResourceAccessKind.Allow);

        core.WebMessageReceived += OnWebMessage;

        core.Navigate($"https://{VirtualHost}/index.html");

        _webReady = true;
        StatusLabel.Text = "Ready - pick a case folder";
    }

    /// <summary>
    /// Embed mpv into a black WinForms panel on the RIGHT. The panel handle is the
    /// native parent window mpv composites into (no overlap with WebView2 = no airspace bug).
    /// </summary>
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
            var hwnd = _videoPanel.Handle; // forces native handle creation

            var mpvExe = Path.Combine(AppContext.BaseDirectory, "runtime", "mpv", "mpv.exe");
            _mpv = new MpvPlayer(mpvExe);

            // Proof / dev hook: load a clip on launch if BECKY_REVIEW_TEST_VIDEO is set.
            var testVideo = Environment.GetEnvironmentVariable("BECKY_REVIEW_TEST_VIDEO");
            _mpv.Start(hwnd, string.IsNullOrWhiteSpace(testVideo) ? null : testVideo);

            VideoPlaceholder.Visibility = string.IsNullOrWhiteSpace(testVideo)
                ? Visibility.Visible
                : Visibility.Collapsed;
        }
        catch (Exception ex)
        {
            StatusLabel.Text = "Video pane failed: " + ex.Message;
        }
    }

    // --- folder + search -> the LEFT pane's list -----------------------------

    private async void PickFolderButton_Click(object sender, RoutedEventArgs e)
    {
        var dlg = new OpenFolderDialog { Title = "Pick a case folder" };
        if (dlg.ShowDialog() == true)
        {
            _folder = dlg.FolderName;
            await LoadFolderAsync();
        }
    }

    /// <summary>Index the picked folder and show its videos in the LEFT pane.</summary>
    private async Task LoadFolderAsync()
    {
        if (string.IsNullOrEmpty(_folder))
        {
            return;
        }
        StatusLabel.Text = "Indexing " + _folder + " ...";
        var (index, error) = await BeckyTools.ReviewIndexAsync(_folder, null);
        if (index == null)
        {
            StatusLabel.Text = error ?? "Index failed";
            return;
        }

        var items = new List<object>(index.Videos.Count);
        foreach (var v in index.Videos)
        {
            items.Add(new
            {
                inSec = 0.0,
                label = v.Name,
                src = v.HasTranscript ? "has transcript" : "no transcript",
                file = v.Path,
                badge = v.HasTranscript ? "txt" : null,
            });
        }
        PostResults(items, $"{index.Videos.Count} videos");
        StatusLabel.Text = $"{index.Videos.Count} videos - type to search transcripts";
    }

    /// <summary>Run a transcript search over the picked folder and show ranked cue hits.</summary>
    private async Task RunSearchAsync(string query)
    {
        if (string.IsNullOrEmpty(_folder))
        {
            StatusLabel.Text = "Pick a case folder first";
            return;
        }
        if (string.IsNullOrWhiteSpace(query))
        {
            await LoadFolderAsync();
            return;
        }

        StatusLabel.Text = $"Searching for \"{query}\" ...";
        var (index, error) = await BeckyTools.ReviewIndexAsync(_folder, query);
        if (index == null)
        {
            StatusLabel.Text = error ?? "Search failed";
            return;
        }

        var items = new List<object>(index.Candidates.Count);
        foreach (var c in index.Candidates)
        {
            items.Add(new
            {
                inSec = c.Timestamp,
                label = c.Text,
                src = c.Name,
                file = c.Source,
                badge = c.Terms.Count > 0 ? c.Terms[0] : "hit",
            });
        }
        PostResults(items, $"{index.Candidates.Count} hits for \"{query}\"");
        StatusLabel.Text = $"{index.Candidates.Count} hits for \"{query}\"";
    }

    /// <summary>Push a result list into the page (the LEFT UI owns rendering).</summary>
    private void PostResults(IReadOnlyList<object> items, string label)
    {
        if (!_webReady)
        {
            return;
        }
        var payload = JsonSerializer.Serialize(new { type = "results", items, label });
        WebView.CoreWebView2.PostWebMessageAsJson(payload);
    }

    private void SearchBox_KeyDown(object sender, KeyEventArgs e)
    {
        if (e.Key == Key.Enter)
        {
            _ = RunSearchAsync(SearchBox.Text ?? string.Empty);
        }
    }

    // --- page -> host messages (play / search) -------------------------------

    private void OnWebMessage(object? sender, CoreWebView2WebMessageReceivedEventArgs e)
    {
        string json;
        try { json = e.WebMessageAsJson; }
        catch { return; }

        try
        {
            using var doc = JsonDocument.Parse(json);
            var root = doc.RootElement;
            if (!root.TryGetProperty("type", out var typeEl))
            {
                return;
            }
            switch (typeEl.GetString())
            {
                case "play":
                    HandlePlay(root);
                    break;
                case "search":
                    var q = root.TryGetProperty("query", out var qEl) ? qEl.GetString() : "";
                    _ = RunSearchAsync(q ?? string.Empty);
                    break;
            }
        }
        catch
        {
            // ignored: a malformed message must never crash the window.
        }
    }

    /// <summary>Load+seek the libmpv pane to a clicked clip's exact in-point.</summary>
    private void HandlePlay(JsonElement root)
    {
        var file = root.TryGetProperty("file", out var fEl) ? fEl.GetString() : null;
        var t = root.TryGetProperty("t", out var tEl) && tEl.TryGetDouble(out var tv) ? tv : 0.0;
        if (string.IsNullOrWhiteSpace(file) || _mpv == null)
        {
            return;
        }
        VideoPlaceholder.Visibility = Visibility.Collapsed;
        _isPaused = false;
        PlayPauseButton.Content = "⏸ pause";
        StatusLabel.Text = $"Playing {Path.GetFileName(file)} @ {t:0.0}s";
        _ = _mpv.PlayAtAsync(file!, t, play: true);
    }

    // --- transport (mouse-driven; mirrors the mpv arrow/space key bindings) ----

    private void FrameBackButton_Click(object sender, RoutedEventArgs e)
    {
        if (_mpv == null) { return; }
        _isPaused = true;
        PlayPauseButton.Content = "▶ play";
        _ = _mpv.FrameBackStepAsync();
    }

    private void FrameFwdButton_Click(object sender, RoutedEventArgs e)
    {
        if (_mpv == null) { return; }
        _isPaused = true;
        PlayPauseButton.Content = "▶ play";
        _ = _mpv.FrameStepAsync();
    }

    private void PlayPauseButton_Click(object sender, RoutedEventArgs e)
    {
        if (_mpv == null) { return; }
        _isPaused = !_isPaused;
        PlayPauseButton.Content = _isPaused ? "▶ play" : "⏸ pause";
        _ = _mpv.SetPauseAsync(_isPaused);
    }

    protected override void OnClosed(EventArgs e)
    {
        _mpv?.Dispose();
        base.OnClosed(e);
    }
}
