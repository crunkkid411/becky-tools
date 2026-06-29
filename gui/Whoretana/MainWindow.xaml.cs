using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Media;
using System.Windows.Shapes;
using System.Windows.Threading;
using Microsoft.Win32;
using Whoretana.Audio;
using Whoretana.Orb;
using Whoretana.Tools;
using Whoretana.Voice;

namespace Whoretana
{
    // WHORETANA shell: the orb is the face; the chrome is the body. Mic drives the orb
    // and the dial; the chat box and mic both route through becky-voice (-> becky-ask
    // fallback) and the reply is spoken back with lip-sync. Everything shells out to
    // existing becky-*.exe; nothing is reimplemented and nothing throws to the user.
    public partial class MainWindow : Window
    {
        private readonly AudioEngine _audio = new AudioEngine();
        private readonly VoiceClient _voice;
        private readonly DispatcherTimer _poll = new DispatcherTimer { Interval = TimeSpan.FromMilliseconds(33) };
        private readonly DispatcherTimer _spark = new DispatcherTimer { Interval = TimeSpan.FromMilliseconds(110) };
        private readonly Random _rnd = new Random(7);

        private CatalogDoc? _catalog;
        private string? _target;            // file the tool actions apply to
        private bool _talking;
        private string? _pendingConfirm;    // utterance awaiting a yes/no

        public MainWindow()
        {
            InitializeComponent();
            _voice = new VoiceClient(_audio);
            ProcessRunner.EnsureToolsOnPath();
            _audio.SpeakDone += () => Dispatcher.Invoke(() => SetStatus("READY  -  hold the mic or type below"));
            ChatInput.TextChanged += (_, __) =>
                ChatHint.Visibility = string.IsNullOrEmpty(ChatInput.Text) ? Visibility.Visible : Visibility.Collapsed;

            Loaded += async (_, __) =>
            {
                BringToFront();
                _audio.StartMic();
                BuildElectric();
                _poll.Tick += Poll; _poll.Start();
                _spark.Tick += Spark; _spark.Start();
                await LoadCatalogAsync();
            };
            Closed += (_, __) => _audio.Dispose();

            KeyDown += (_, ev) =>
            {
                if (ev.Key == Key.Escape) Close();
            };
        }

        // ---- per-frame: drive the orb + dial from live audio ----------------
        private void Poll(object? sender, EventArgs e)
        {
            float mic = _audio.MicLevel, sp = _audio.SpeechLevel;
            Orb.MicLevel = mic; Orb.SpeechLevel = sp;
            Orb.Mode = _audio.IsSpeaking ? OrbMode.Speaking
                     : (_talking || mic > 0.16f) ? OrbMode.Listening
                     : OrbMode.Idle;

            DialRot.Angle = -120 + 240 * Math.Min(1f, mic);
            DialCore.Opacity = 0.4 + 0.6 * Math.Min(1f, _audio.IsSpeaking ? sp : mic);
        }

        // ---- electric chat border: re-jitter + flicker for the "leaking" look ----
        private void Spark(object? sender, EventArgs e)
        {
            BuildElectric();
            ChatEdgeGlow.Opacity = 0.7 + 0.3 * _rnd.NextDouble();
            ChatEdge2.Opacity = 0.18 + 0.3 * _rnd.NextDouble();
        }

        private void BuildElectric()
        {
            const double w = 452, h = 338, inset = 4;
            ChatEdgeGlow.Data = JaggedRect(w, h, inset, 4.5, 7);
            ChatEdge2.Data = JaggedRect(w, h, inset + 2, 6.5, 6);
        }

        // a rough rectangle whose edges zigzag with random perpendicular jitter + spikes.
        private Geometry JaggedRect(double w, double h, double m, double amp, double step)
        {
            var pts = new List<Point>();
            void Edge(double x0, double y0, double x1, double y1, double nx, double ny)
            {
                double len = Math.Sqrt((x1 - x0) * (x1 - x0) + (y1 - y0) * (y1 - y0));
                int n = Math.Max(3, (int)(len / (step + 6)));
                for (int i = 0; i <= n; i++)
                {
                    double t = (double)i / n;
                    double j = (i == 0 || i == n) ? 0 : (_rnd.NextDouble() - 0.5) * 2 * amp;
                    if (_rnd.NextDouble() < 0.12) j *= 3.2;     // occasional spike
                    pts.Add(new Point(x0 + (x1 - x0) * t + nx * j, y0 + (y1 - y0) * t + ny * j));
                }
            }
            Edge(m, m, w - m, m, 0, -1);             // top  (jitter up)
            Edge(w - m, m, w - m, h - m, 1, 0);      // right
            Edge(w - m, h - m, m, h - m, 0, 1);      // bottom
            Edge(m, h - m, m, m, -1, 0);             // left

            var fig = new PathFigure { StartPoint = pts[0], IsClosed = true };
            for (int i = 1; i < pts.Count; i++) fig.Segments.Add(new LineSegment(pts[i], true));
            var g = new PathGeometry(); g.Figures.Add(fig);
            return g;
        }

        // ---- catalog -> tool grid + menu ------------------------------------
        private async Task LoadCatalogAsync()
        {
            _catalog = await Catalog.LoadAsync();
            if (_catalog == null)
            {
                SetStatus("Could not load the tool list - is becky-catalog on PATH?");
                return;
            }
            ToolPanel.Children.Clear();
            foreach (var t in _catalog.tools) ToolPanel.Children.Add(MakeToolButton(t));

            MenuList.Children.Clear();
            foreach (var op in _catalog.ops) MenuList.Children.Add(MakeMenuItem(op));
            SetStatus("Loaded " + _catalog.tools.Length + " tools. Talk, type, or click a tool.");
        }

        private Button MakeToolButton(ToolEntry t)
        {
            var b = new Button
            {
                Content = t.Label,
                Style = (Style)FindResource("ToolButton"),
                ToolTip = t.summary + "\n" + t.example,
                Tag = t,
                BorderBrush = TierBrush(t.tier),
                Foreground = TierBrush(t.tier),
            };
            b.Click += async (_, __) => await RunToolAsync(t);
            return b;
        }

        private Button MakeMenuItem(ToolEntry op)
        {
            var sp = new StackPanel { Orientation = Orientation.Horizontal };
            sp.Children.Add(new TextBlock
            {
                Text = "•",
                Foreground = TierBrush(op.tier),
                FontSize = 14,
                Margin = new Thickness(2, 0, 10, 0),
                VerticalAlignment = VerticalAlignment.Center,
            });
            sp.Children.Add(new TextBlock
            {
                Text = op.verb,
                Foreground = (Brush)FindResource("TextMain"),
                FontFamily = (FontFamily)FindResource("HudFont"),
                FontSize = 13,
                VerticalAlignment = VerticalAlignment.Center,
            });
            var b = new Button
            {
                Content = sp,
                Tag = op,
                ToolTip = op.summary,
                Background = Brushes.Transparent,
                BorderThickness = new Thickness(0),
                HorizontalContentAlignment = HorizontalAlignment.Left,
                Padding = new Thickness(8, 6, 8, 6),
                Cursor = Cursors.Hand,
            };
            b.Click += async (_, __) => { AddChat("you", op.verb); await AskAndSpeak(op.verb); };
            return b;
        }

        private Brush TierBrush(string tier)
        {
            switch (tier)
            {
                case "green": return (Brush)FindResource("Cyan");
                case "yellow": return new SolidColorBrush(Color.FromRgb(0xEC, 0xC9, 0x4B));
                default: return (Brush)FindResource("Accent");
            }
        }

        private async Task RunToolAsync(ToolEntry t)
        {
            if (t.tier == "red")
            {
                var ok = MessageBox.Show(t.verb + " can change or export things. Run it?",
                    "Confirm", MessageBoxButton.YesNo, MessageBoxImage.Warning);
                if (ok != MessageBoxResult.Yes) return;
            }
            if (string.IsNullOrEmpty(_target) && !PickTarget()) return;

            string exe = ProcessRunner.FirstToken(t.example, t.verb);
            SetStatus("Running " + t.verb + " ...");
            AddChat("you", t.verb + "  " + System.IO.Path.GetFileName(_target));
            var (code, so, se) = await ProcessRunner.RunAsync(exe, "\"" + _target + "\"");
            string head = ProcessRunner.FirstLine(se);
            if (string.IsNullOrWhiteSpace(head)) head = code == 0 ? (t.verb + " done.") : (t.verb + " exited " + code + ".");
            AddChat("whoretana", head + (string.IsNullOrWhiteSpace(so) ? "" : "\n" + Trunc(so, 600)));
            SetStatus(head);
        }

        private bool PickTarget()
        {
            var dlg = new OpenFileDialog { Title = "Pick a video or audio file for the tools" };
            if (dlg.ShowDialog() != true) { SetStatus("Pick a file first."); return false; }
            _target = dlg.FileName;
            _voice.Target = _target;
            SetStatus("Target: " + System.IO.Path.GetFileName(_target));
            return true;
        }

        // ---- chat + voice ---------------------------------------------------
        private async Task AskAndSpeak(string text, bool confirm = false)
        {
            SetStatus("Thinking ...");
            var reply = await _voice.RouteTextAsync(text, confirm);
            AddChat("whoretana", reply.Text);
            if (reply.NeedConfirm || reply.Action == "await_confirm")
            {
                _pendingConfirm = text;
                SetStatus("Say or type 'yes' to confirm.");
            }
            else SetStatus("READY  -  hold the mic or type below");
            await _voice.SpeakAsync(reply.Text);
        }

        private async void Send_Click(object sender, RoutedEventArgs e) => await SendChat();
        private async void ChatInput_KeyDown(object sender, KeyEventArgs e)
        {
            if (e.Key == Key.Enter) { e.Handled = true; await SendChat(); }
        }

        private async Task SendChat()
        {
            string text = ChatInput.Text.Trim();
            if (text.Length == 0) return;
            ChatInput.Clear(); ChatHint.Visibility = Visibility.Visible;
            AddChat("you", text);

            if (_pendingConfirm != null && IsYes(text))
            {
                string p = _pendingConfirm; _pendingConfirm = null;
                await AskAndSpeak(p, confirm: true);
                return;
            }
            _pendingConfirm = null;
            await AskAndSpeak(text);
        }

        private async void Mic_Click(object sender, RoutedEventArgs e)
        {
            if (!_talking)
            {
                if (!_audio.MicAvailable) { SetStatus("No microphone found."); return; }
                _talking = true;
                _voice.StartTalk();
                MicGlyph.Text = "";                 // recording glyph
                MicGlyph.Foreground = (Brush)FindResource("Accent");
                SetStatus("Listening ... click the mic again when done");
            }
            else
            {
                _talking = false;
                MicGlyph.Text = "";
                MicGlyph.Foreground = (Brush)FindResource("Cyan");
                SetStatus("Thinking ...");
                var (heard, reply) = await _voice.StopTalkAndProcessAsync();
                if (!string.IsNullOrEmpty(heard)) AddChat("you", heard);
                AddChat("whoretana", reply.Text);
                if (reply.NeedConfirm || reply.Action == "await_confirm")
                { _pendingConfirm = heard; SetStatus("Say or type 'yes' to confirm."); }
                else SetStatus("READY  -  hold the mic or type below");
                await _voice.SpeakAsync(reply.Text);
            }
        }

        // ---- command bar / workflows / launchers ----------------------------
        private void Icon_Click(object sender, RoutedEventArgs e)
        {
            string tag = (string)((Button)sender).Tag;
            switch (tag)
            {
                case "search": SearchBox.Focus(); break;
                case "do": ChatInput.Text = "/"; ChatInput.Focus(); ChatInput.CaretIndex = 1; ChatHint.Visibility = Visibility.Collapsed; break;
                case "code": ProcessRunner.LaunchTerminal("becky shell", "echo becky shell ready"); break;
                case "shield": AddChat("whoretana", "Tiers: cyan = safe (auto), amber = asks first, red = needs your OK before I touch anything."); break;
                case "settings": PickTarget(); break;
            }
        }

        private async void Workflow_Click(object sender, RoutedEventArgs e)
        {
            string phrase = (string)((Button)sender).Tag;
            AddChat("you", phrase);
            await AskAndSpeak(phrase);
        }

        private void Agent_Click(object sender, RoutedEventArgs e)
        {
            string tag = (string)((Button)sender).Tag;
            string root = ProcessRunner.RepoRoot();
            switch (tag)
            {
                case "claude": ProcessRunner.LaunchTerminal("Claude Code", "claude"); break;
                case "ask": ProcessRunner.LaunchTerminal("becky-ask", "becky-ask"); break;
                case "research": ProcessRunner.LaunchTerminal("becky-research", "becky-research --help ^& cmd"); break;
                case "daw": LaunchBat(root, "Open Becky DAW.bat"); break;
                case "review": LaunchBat(root, "Open Becky Review.bat"); break;
                case "window": LaunchBat(root, "Open Becky Window.bat"); break;
                case "canvas": LaunchBat(root, "Open Becky Canvas.bat"); break;
                case "repo": ProcessRunner.LaunchTerminal("becky shell", "cd /d \"" + root + "\""); break;
            }
        }

        private void LaunchBat(string root, string bat)
        {
            string path = System.IO.Path.Combine(root, bat);
            try
            {
                if (System.IO.File.Exists(path))
                    System.Diagnostics.Process.Start(new System.Diagnostics.ProcessStartInfo(path) { UseShellExecute = true });
                else SetStatus(bat + " not found.");
            }
            catch { SetStatus("Could not launch " + bat); }
        }

        // ---- search filter --------------------------------------------------
        private void SearchBox_TextChanged(object sender, TextChangedEventArgs e)
        {
            string q = SearchBox.Text.Trim().ToLowerInvariant();
            SearchHint.Visibility = q.Length == 0 ? Visibility.Visible : Visibility.Collapsed;
            foreach (var child in ToolPanel.Children)
            {
                if (child is Button b && b.Tag is ToolEntry t)
                {
                    bool hit = q.Length == 0 || t.verb.ToLowerInvariant().Contains(q)
                               || t.summary.ToLowerInvariant().Contains(q)
                               || string.Join(" ", t.keywords).ToLowerInvariant().Contains(q);
                    b.Visibility = hit ? Visibility.Visible : Visibility.Collapsed;
                }
            }
        }

        // ---- chat log helpers -----------------------------------------------
        private void AddChat(string who, string text)
        {
            bool me = who == "you";
            var bubble = new Border
            {
                Background = me ? new SolidColorBrush(Color.FromArgb(0x33, 0x22, 0xE8, 0xFF))
                                : new SolidColorBrush(Color.FromArgb(0x22, 0x8C, 0xF6, 0xFF)),
                BorderBrush = me ? (Brush)FindResource("CyanDeep") : (Brush)FindResource("Cyan"),
                BorderThickness = new Thickness(me ? 0 : 1),
                CornerRadius = new CornerRadius(4),
                Padding = new Thickness(9, 6, 9, 6),
                Margin = new Thickness(me ? 40 : 0, 3, me ? 0 : 40, 3),
                HorizontalAlignment = me ? HorizontalAlignment.Right : HorizontalAlignment.Left,
                MaxWidth = 380,
            };
            bubble.Child = new TextBlock
            {
                Text = text,
                Foreground = (Brush)FindResource(me ? "TextMain" : "CyanBright"),
                FontFamily = (FontFamily)FindResource("HudFont"),
                FontSize = 12.5,
                TextWrapping = TextWrapping.Wrap,
            };
            ChatLog.Children.Add(bubble);
            ChatScroll.ScrollToEnd();
        }

        private void SetStatus(string s) => StatusText.Text = s;

        private static bool IsYes(string s)
        {
            s = s.Trim().ToLowerInvariant();
            return s == "y" || s == "yes" || s == "yeah" || s == "do it" || s == "go" || s == "confirm" || s == "ok" || s == "okay";
        }

        private static string Trunc(string s, int n) => s.Length <= n ? s : s.Substring(0, n) + " ...";

        // ---- window chrome --------------------------------------------------
        private void DragBar_MouseDown(object sender, MouseButtonEventArgs e)
        {
            if (e.ChangedButton == MouseButton.Left) DragMove();
        }
        private void Min_Click(object sender, RoutedEventArgs e) => WindowState = WindowState.Minimized;
        private void Close_Click(object sender, RoutedEventArgs e) => Close();

        private void BringToFront()
        {
            try { Topmost = true; Activate(); Topmost = false; Focus(); } catch { }
        }
    }
}
