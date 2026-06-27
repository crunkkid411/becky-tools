using System;
using System.IO;
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
    }

    /// <summary>
    /// Boot WebView2, map the local <c>ui</c> folder to a virtual host (no server),
    /// and load the review UI. Sets up the page-to-host message channel.
    /// </summary>
    private async Task InitWebViewAsync()
    {
        // Keep the WebView2 profile beside the exe so the app stays offline + deterministic.
        var userData = Path.Combine(AppContext.BaseDirectory, "webview2-data");
        var env = await CoreWebView2Environment.CreateAsync(userDataFolder: userData);
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
        StatusLabel.Text = "Step 1 - UI loaded (no server)";
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

            StatusLabel.Text = "Step 2 - video pane ready (mpv)";
        }
        catch (Exception ex)
        {
            StatusLabel.Text = "Video pane failed: " + ex.Message;
        }
    }

    /// <summary>Messages from the page (JSON): play / search. Wired further in later steps.</summary>
    private void OnWebMessage(object? sender, CoreWebView2WebMessageReceivedEventArgs e)
    {
        string json;
        try { json = e.WebMessageAsJson; }
        catch { return; }
        // Step 1-2: echo that the bridge works; play/search handled in Steps 4-5.
        StatusLabel.Text = "page -> host: " + json;
    }

    private void PickFolderButton_Click(object sender, RoutedEventArgs e)
    {
        var dlg = new OpenFolderDialog { Title = "Pick a case folder" };
        if (dlg.ShowDialog() == true)
        {
            _folder = dlg.FolderName;
            StatusLabel.Text = _folder;
        }
    }

    private void SearchBox_KeyDown(object sender, KeyEventArgs e)
    {
        if (e.Key != Key.Enter)
        {
            return;
        }

        var query = SearchBox.Text ?? string.Empty;
        if (_webReady)
        {
            // Forward the search into the page so the LEFT UI owns rendering.
            var payload = System.Text.Json.JsonSerializer.Serialize(new { type = "search", query });
            WebView.CoreWebView2.PostWebMessageAsJson(payload);
        }
        StatusLabel.Text = string.IsNullOrWhiteSpace(query) ? "(empty search)" : $"Search: {query}";
    }

    protected override void OnClosed(EventArgs e)
    {
        _mpv?.Dispose();
        base.OnClosed(e);
    }
}
