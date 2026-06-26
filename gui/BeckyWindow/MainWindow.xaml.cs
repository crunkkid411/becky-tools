using System;
using System.Diagnostics;
using System.Text.Json;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using Microsoft.Win32;

namespace BeckyWindow
{
    // The minimal native "becky window": read the tool list from `becky-catalog --json`,
    // make a button per tool, and on click run the real becky-*.exe on the picked file and
    // show the result. No browser, no server — a .exe that launches other .exe's.
    // Degrade, never crash: a missing tool shows a message, it does not throw.
    public partial class MainWindow : Window
    {
        private string? _selectedFile;

        public MainWindow()
        {
            InitializeComponent();
            EnsureToolsOnPath();
            Loaded += async (_, __) =>
            {
                BringToFront();           // make sure Jordan SEES it (multi-monitor / busy desktop)
                await LoadCatalogAsync();
            };
        }

        // Find becky-go\bin (walking up from this .exe) and put it on THIS process's PATH,
        // so the window works when launched directly from a desktop shortcut — not only when
        // a launcher script set PATH first. If it can't be found, the catalog load shows a
        // clear message instead of crashing (degrade, never crash).
        private static void EnsureToolsOnPath()
        {
            try
            {
                var dir = AppContext.BaseDirectory;
                for (int i = 0; i < 8 && !string.IsNullOrEmpty(dir); i++)
                {
                    var bin = System.IO.Path.Combine(dir, "becky-go", "bin");
                    if (System.IO.File.Exists(System.IO.Path.Combine(bin, "becky-catalog.exe")))
                    {
                        var path = Environment.GetEnvironmentVariable("PATH") ?? "";
                        Environment.SetEnvironmentVariable("PATH", bin + System.IO.Path.PathSeparator + path);
                        return;
                    }
                    dir = System.IO.Directory.GetParent(dir)?.FullName;
                }
            }
            catch
            {
                // ignored: the window still opens; the catalog load reports the problem.
            }
        }

        // Pop the window to the front of whatever else is open. Jordan runs a busy, multi-monitor
        // desktop; a window that opens behind other windows reads as "it didn't open." A brief
        // Topmost flash + Activate reliably brings it forward without staying always-on-top.
        private void BringToFront()
        {
            try
            {
                if (WindowState == WindowState.Minimized) WindowState = WindowState.Normal;
                Topmost = true;
                Activate();
                Topmost = false;
                Focus();
            }
            catch
            {
                // ignored: not being on top is cosmetic, never fatal.
            }
        }

        // --- the JSON shape emitted by `becky-catalog --json` (Step 1) ---
        private sealed class ToolEntry
        {
            public string verb { get; set; } = "";
            public string summary { get; set; } = "";
            public string example { get; set; } = "";
            public string tier { get; set; } = "red";
            public string pack { get; set; } = "";
        }

        private sealed class CatalogDoc
        {
            public ToolEntry[] tools { get; set; } = Array.Empty<ToolEntry>();
            public ToolEntry[] ops { get; set; } = Array.Empty<ToolEntry>();
        }

        private async Task LoadCatalogAsync()
        {
            var (code, stdout, stderr) = await RunAsync("becky-catalog", "--json");
            if (code != 0 || string.IsNullOrWhiteSpace(stdout))
            {
                ShowHeadline("Could not load the tool list — is becky-catalog on your PATH? " + stderr, error: true);
                return;
            }

            CatalogDoc? doc;
            try
            {
                doc = JsonSerializer.Deserialize<CatalogDoc>(stdout,
                    new JsonSerializerOptions { PropertyNameCaseInsensitive = true });
            }
            catch (Exception ex)
            {
                ShowHeadline("Tool list was not valid JSON: " + ex.Message, error: true);
                return;
            }

            ToolPanel.Children.Clear();
            var tools = doc?.tools ?? Array.Empty<ToolEntry>();
            foreach (var t in tools)
            {
                ToolPanel.Children.Add(MakeToolButton(t));
            }
            ShowHeadline("Loaded " + tools.Length + " tools. Pick a file, then click one.", error: false);
        }

        private Button MakeToolButton(ToolEntry t)
        {
            var stack = new StackPanel();
            stack.Children.Add(new TextBlock
            {
                Text = t.verb,
                FontWeight = FontWeights.Bold,
                FontSize = 18,
                Foreground = Brushes.White,
                TextWrapping = TextWrapping.Wrap
            });
            stack.Children.Add(new TextBlock
            {
                Text = t.summary,
                FontSize = 13,
                Foreground = new SolidColorBrush(Color.FromRgb(0xC8, 0xC8, 0xD8)),
                TextWrapping = TextWrapping.Wrap,
                Margin = new Thickness(0, 4, 0, 0)
            });

            var btn = new Button
            {
                Width = 290,
                Height = 100,
                ToolTip = t.example,
                Tag = t,
                BorderThickness = new Thickness(2),
                BorderBrush = TierBrush(t.tier),
                Content = stack
            };
            btn.Click += async (_, __) => await RunToolAsync(t);
            return btn;
        }

        private static Brush TierBrush(string tier)
        {
            switch (tier)
            {
                case "green": return new SolidColorBrush(Color.FromRgb(0x48, 0xBB, 0x78));
                case "yellow": return new SolidColorBrush(Color.FromRgb(0xEC, 0xC9, 0x4B));
                default: return new SolidColorBrush(Color.FromRgb(0xE5, 0x3E, 0x3E));
            }
        }

        private void BrowseButton_Click(object sender, RoutedEventArgs e)
        {
            var dlg = new OpenFileDialog { Title = "Pick a video or audio file" };
            if (dlg.ShowDialog() == true)
            {
                _selectedFile = dlg.FileName;
                FileLabel.Text = _selectedFile;
            }
        }

        private async Task RunToolAsync(ToolEntry t)
        {
            // RED = destructive/outward-facing: confirm first (tier gate, SPEC-BECKY-VOICE §4.1).
            if (t.tier == "red")
            {
                var ok = MessageBox.Show(
                    t.verb + " can change or export things. Run it?",
                    "Confirm", MessageBoxButton.YesNo, MessageBoxImage.Warning);
                if (ok != MessageBoxResult.Yes) return;
            }

            if (string.IsNullOrEmpty(_selectedFile))
            {
                ShowHeadline("Pick a file first (the 'Pick file...' button).", error: true);
                return;
            }

            // exe = first token of the example, so e.g. reaper-bridge -> becky-reaper.
            var exe = FirstToken(t.example, fallback: t.verb);
            ShowHeadline("Running " + t.verb + "...", error: false);
            OutputBox.Text = "";

            var (code, stdout, stderr) = await RunAsync(exe, "\"" + _selectedFile + "\"");

            var headline = FirstLine(stderr);
            if (string.IsNullOrWhiteSpace(headline))
            {
                headline = code == 0 ? (t.verb + " done.") : (t.verb + " exited " + code + ".");
            }
            ShowHeadline(headline, error: code != 0);
            OutputBox.Text = string.IsNullOrWhiteSpace(stdout) ? stderr : stdout;
        }

        private static string FirstToken(string s, string fallback)
        {
            s = (s ?? "").Trim();
            if (s.Length == 0) return fallback;
            var sp = s.IndexOf(' ');
            return sp < 0 ? s : s.Substring(0, sp);
        }

        private static string FirstLine(string s)
        {
            if (string.IsNullOrEmpty(s)) return "";
            var nl = s.IndexOf('\n');
            return (nl < 0 ? s : s.Substring(0, nl)).Trim();
        }

        private void ShowHeadline(string text, bool error)
        {
            HeadlineLabel.Text = text;
            HeadlineLabel.Foreground = error
                ? new SolidColorBrush(Color.FromRgb(0xFF, 0x6B, 0x6B))
                : new SolidColorBrush(Color.FromRgb(0xFF, 0xD1, 0x66));
        }

        // RunAsync runs an exe off the UI thread, captures stdout+stderr, and NEVER throws
        // (a missing/failed tool returns code -1 with a message, so the UI degrades cleanly).
        private static Task<(int code, string stdout, string stderr)> RunAsync(string exe, string args)
        {
            return Task.Run(() =>
            {
                try
                {
                    var psi = new ProcessStartInfo
                    {
                        FileName = exe,
                        Arguments = args,
                        RedirectStandardOutput = true,
                        RedirectStandardError = true,
                        UseShellExecute = false,
                        CreateNoWindow = true
                    };
                    using (var p = Process.Start(psi))
                    {
                        if (p == null) return (-1, "", "could not start " + exe);
                        string so = p.StandardOutput.ReadToEnd();
                        string se = p.StandardError.ReadToEnd();
                        p.WaitForExit();
                        return (p.ExitCode, so, se);
                    }
                }
                catch (Exception ex)
                {
                    return (-1, "", exe + " not found or failed: " + ex.Message);
                }
            });
        }
    }
}
