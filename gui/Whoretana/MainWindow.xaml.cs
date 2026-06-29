using System;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Media;
using System.Windows.Shapes;
using System.Windows.Threading;
using Microsoft.Win32;
using Whoretana.Orb;
using Whoretana.Tools;
using Whoretana.Voice;

namespace Whoretana
{
    // WHORETANA shell. The orb is driven by the FastRTC sidecar's level/state events
    // (no managed audio). Typed chat routes through becky-voice (C#) and is spoken by the
    // sidecar; the mic button hands the whole voice loop to the sidecar. Everything is
    // dialed in from the Settings panel - brain, mic, voice, key - never the CLI.
    public partial class MainWindow : Window
    {
        private readonly VoiceBridge _bridge = new VoiceBridge();
        private readonly VoiceClient _voice = new VoiceClient();
        private Settings _settings = new Settings();
        private readonly DispatcherTimer _poll = new DispatcherTimer { Interval = TimeSpan.FromMilliseconds(33) };
        private readonly DispatcherTimer _spark = new DispatcherTimer { Interval = TimeSpan.FromMilliseconds(110) };
        private readonly System.Random _rnd = new System.Random(7);

        private CatalogDoc? _catalog;
        private string? _target;
        private bool _listening;
        private bool _menuOpen = true;
        private string? _pendingConfirm;
        private string _envPath = "";

        public MainWindow()
        {
            InitializeComponent();
            ProcessRunner.EnsureToolsOnPath();
            _envPath = Settings.LoadDotEnv();
            _settings = Settings.Load();

            _bridge.StateChanged += s => Dispatcher.Invoke(() => OnBridgeState(s));
            _bridge.Transcript += t => Dispatcher.Invoke(() => { if (!string.IsNullOrWhiteSpace(t)) AddChat("you", t); });
            _bridge.ReplyReceived += r => Dispatcher.Invoke(() => OnBridgeReply(r));
            _bridge.Errored += e => Dispatcher.Invoke(() => SetStatus(e));

            ChatInput.TextChanged += (_, __) => ChatHint.Visibility = string.IsNullOrEmpty(ChatInput.Text) ? Visibility.Visible : Visibility.Collapsed;
            KeyDown += (_, ev) => { if (ev.Key == Key.Escape) { if (SettingsOverlay.Visibility == Visibility.Visible) CloseSettings(); else Close(); } };

            Loaded += async (_, __) =>
            {
                BringToFront();
                BuildElectric();
                _poll.Tick += Poll; _poll.Start();
                _spark.Tick += Spark; _spark.Start();
                UpdateModelText();
                await LoadCatalogAsync();
                StartBridge();
            };
            Closed += (_, __) => _bridge.Dispose();
        }

        private void StartBridge()
        {
            if (!VoiceBridge.Installed())
            {
                SetStatus("Voice runtime not installed - run setup-voice.bat (typed chat still works).");
                return;
            }
            if (_bridge.Start(_settings)) SetStatus("READY  -  talk (mic), or type below");
            else SetStatus("Voice runtime: " + (_bridge.LastError ?? "failed to start") + " (typed chat still works).");
        }

        // ---- orb + dial from sidecar ----------------------------------------
        private void Poll(object? sender, EventArgs e)
        {
            float lvl = Math.Min(1f, _bridge.Level);
            string st = _bridge.State;
            if (st == "speaking") { Orb.Mode = OrbMode.Speaking; Orb.SpeechLevel = lvl; Orb.MicLevel = 0; }
            else if (st == "listening") { Orb.Mode = OrbMode.Listening; Orb.MicLevel = lvl; Orb.SpeechLevel = 0; }
            else { Orb.Mode = OrbMode.Idle; Orb.MicLevel = 0; Orb.SpeechLevel = 0; }

            DialRot.Angle = -120 + 240 * lvl;
            DialCore.Opacity = 0.4 + 0.6 * lvl;

            if (SettingsOverlay.Visibility == Visibility.Visible && MicMeter.Parent is FrameworkElement track)
                MicMeter.Width = Math.Max(0, track.ActualWidth) * lvl;
        }

        private void OnBridgeState(string s)
        {
            if (s == "thinking") SetStatus("Thinking ...");
            else if (s == "listening") SetStatus("Listening ...");
            else if (s == "speaking") SetStatus("Speaking ...");
        }

        private void OnBridgeReply(VoiceBridge.Reply r)
        {
            AddChat("whoretana", r.Text);
            if (r.NeedConfirm || r.Action == "await_confirm") SetStatus("Say or type 'yes' to confirm.");
            else SetStatus("READY  -  talk (mic), or type below");
        }

        // ---- electric chat border -------------------------------------------
        private void Spark(object? sender, EventArgs e)
        {
            BuildElectric();
            ChatEdgeGlow.Opacity = 0.7 + 0.3 * _rnd.NextDouble();
            ChatEdge2.Opacity = 0.18 + 0.3 * _rnd.NextDouble();
        }

        private void BuildElectric()
        {
            ChatEdgeGlow.Data = JaggedRect(452, 338, 4, 4.5, 7);
            ChatEdge2.Data = JaggedRect(452, 338, 6, 6.5, 6);
        }

        private Geometry JaggedRect(double w, double h, double m, double amp, double step)
        {
            var pts = new System.Collections.Generic.List<Point>();
            void Edge(double x0, double y0, double x1, double y1, double nx, double ny)
            {
                double len = Math.Sqrt((x1 - x0) * (x1 - x0) + (y1 - y0) * (y1 - y0));
                int n = Math.Max(3, (int)(len / (step + 6)));
                for (int i = 0; i <= n; i++)
                {
                    double t = (double)i / n;
                    double j = (i == 0 || i == n) ? 0 : (_rnd.NextDouble() - 0.5) * 2 * amp;
                    if (_rnd.NextDouble() < 0.12) j *= 3.2;
                    pts.Add(new Point(x0 + (x1 - x0) * t + nx * j, y0 + (y1 - y0) * t + ny * j));
                }
            }
            Edge(m, m, w - m, m, 0, -1); Edge(w - m, m, w - m, h - m, 1, 0);
            Edge(w - m, h - m, m, h - m, 0, 1); Edge(m, h - m, m, m, -1, 0);
            var fig = new PathFigure { StartPoint = pts[0], IsClosed = true };
            for (int i = 1; i < pts.Count; i++) fig.Segments.Add(new LineSegment(pts[i], true));
            var g = new PathGeometry(); g.Figures.Add(fig); return g;
        }

        // ---- catalog -> tools + menu ----------------------------------------
        private async Task LoadCatalogAsync()
        {
            _catalog = await Catalog.LoadAsync();
            if (_catalog == null) { SetStatus("Could not load the tool list - is becky-catalog on PATH?"); return; }
            ToolPanel.Children.Clear();
            foreach (var t in _catalog.tools) ToolPanel.Children.Add(MakeToolButton(t));
            MenuList.Children.Clear();
            foreach (var op in _catalog.ops) MenuList.Children.Add(MakeMenuItem(op));
        }

        private Button MakeToolButton(ToolEntry t)
        {
            var b = new Button { Content = t.Label, Style = (Style)FindResource("ToolButton"), ToolTip = t.summary + "\n" + t.example, Tag = t, BorderBrush = TierBrush(t.tier), Foreground = TierBrush(t.tier) };
            b.Click += async (_, __) => await RunToolAsync(t);
            return b;
        }

        private Button MakeMenuItem(ToolEntry op)
        {
            var sp = new StackPanel { Orientation = Orientation.Horizontal };
            sp.Children.Add(new TextBlock { Text = "•", Foreground = TierBrush(op.tier), FontSize = 14, Margin = new Thickness(2, 0, 10, 0), VerticalAlignment = VerticalAlignment.Center });
            sp.Children.Add(new TextBlock { Text = op.verb, Foreground = (Brush)FindResource("TextMain"), FontFamily = (FontFamily)FindResource("HudFont"), FontSize = 13, VerticalAlignment = VerticalAlignment.Center });
            var b = new Button { Content = sp, Tag = op, ToolTip = op.summary, Background = Brushes.Transparent, BorderThickness = new Thickness(0), HorizontalContentAlignment = HorizontalAlignment.Left, Padding = new Thickness(8, 6, 8, 6), Cursor = Cursors.Hand };
            b.Click += async (_, __) => { AddChat("you", op.verb); await RouteTyped(op.verb); };
            return b;
        }

        private Brush TierBrush(string tier) => tier switch
        {
            "green" => (Brush)FindResource("Cyan"),
            "yellow" => new SolidColorBrush(Color.FromRgb(0xEC, 0xC9, 0x4B)),
            _ => (Brush)FindResource("Accent"),
        };

        private async Task RunToolAsync(ToolEntry t)
        {
            if (t.tier == "red" && MessageBox.Show(t.verb + " can change or export things. Run it?", "Confirm", MessageBoxButton.YesNo, MessageBoxImage.Warning) != MessageBoxResult.Yes) return;
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
            _target = dlg.FileName; _voice.Target = _target;
            SetStatus("Target: " + System.IO.Path.GetFileName(_target));
            return true;
        }

        // ---- chat (typed) ---------------------------------------------------
        private async void Send_Click(object sender, RoutedEventArgs e) => await SendChat();
        private async void ChatInput_KeyDown(object sender, KeyEventArgs e) { if (e.Key == Key.Enter) { e.Handled = true; await SendChat(); } }

        private async Task SendChat()
        {
            string text = ChatInput.Text.Trim();
            if (text.Length == 0) return;
            ChatInput.Clear();
            AddChat("you", text);
            if (_pendingConfirm != null && IsYes(text)) { var p = _pendingConfirm; _pendingConfirm = null; await RouteTyped(p, true); return; }
            _pendingConfirm = null;
            await RouteTyped(text);
        }

        // Route a typed utterance via becky-voice (C#), show + speak the reply.
        private async Task RouteTyped(string text, bool confirm = false)
        {
            SetStatus("Thinking ...");
            var reply = await _voice.RouteTextAsync(text, confirm);
            AddChat("whoretana", reply.Text);
            if (reply.NeedConfirm || reply.Action == "await_confirm") { _pendingConfirm = text; SetStatus("Say or type 'yes' to confirm."); }
            else SetStatus("READY  -  talk (mic), or type below");
            if (_bridge.Ready) _bridge.Say(reply.Text);   // speak via FastRTC (orb lip-syncs)
        }

        // ---- mic (voice loop in the sidecar) --------------------------------
        private void Mic_Click(object sender, RoutedEventArgs e)
        {
            if (!_bridge.Ready) { SetStatus("Voice runtime not ready - open Settings (gear) or run setup-voice.bat."); return; }
            _listening = !_listening;
            _bridge.SetListening(_listening);
            MicGlyph.Text = _listening ? "" : "";
            MicGlyph.Foreground = (Brush)FindResource(_listening ? "Accent" : "Cyan");
            SetStatus(_listening ? "Listening ... talk to me (click mic to stop)" : "READY  -  talk (mic), or type below");
        }

        // ---- command bar / workflows / launchers ----------------------------
        private void Icon_Click(object sender, RoutedEventArgs e)
        {
            switch ((string)((Button)sender).Tag)
            {
                case "search": SearchBox.Focus(); break;
                case "do": ChatInput.Text = "/"; ChatInput.Focus(); ChatInput.CaretIndex = 1; break;
                case "code": ProcessRunner.LaunchTerminal("becky shell", "echo becky shell ready"); break;
                case "shield": AddChat("whoretana", "Tiers: cyan = safe (auto), amber = asks first, red = needs your OK before I touch anything."); break;
                case "settings": OpenSettings(); break;
            }
        }

        private async void Workflow_Click(object sender, RoutedEventArgs e) { string p = (string)((Button)sender).Tag; AddChat("you", p); await RouteTyped(p); }

        private void Agent_Click(object sender, RoutedEventArgs e)
        {
            string root = ProcessRunner.RepoRoot();
            switch ((string)((Button)sender).Tag)
            {
                case "claude": ProcessRunner.LaunchTerminal("Claude Code", "claude"); break;
                case "ask": ProcessRunner.LaunchTerminal("becky-ask", "becky-ask"); break;
                case "research": ProcessRunner.LaunchTerminal("becky shell", "cd /d \"" + root + "\""); break;
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
            try { if (System.IO.File.Exists(path)) System.Diagnostics.Process.Start(new System.Diagnostics.ProcessStartInfo(path) { UseShellExecute = true }); else SetStatus(bat + " not found."); }
            catch { SetStatus("Could not launch " + bat); }
        }

        private void MenuHeader_Click(object sender, RoutedEventArgs e)
        {
            _menuOpen = !_menuOpen;
            MenuScroll.Visibility = _menuOpen ? Visibility.Visible : Visibility.Collapsed;
            MenuChevron.Text = _menuOpen ? "▾" : "▸";
        }

        private void SearchBox_TextChanged(object sender, TextChangedEventArgs e)
        {
            string q = SearchBox.Text.Trim().ToLowerInvariant();
            SearchHint.Visibility = q.Length == 0 ? Visibility.Visible : Visibility.Collapsed;
            foreach (var c in ToolPanel.Children)
                if (c is Button b && b.Tag is ToolEntry t)
                    b.Visibility = (q.Length == 0 || t.verb.ToLowerInvariant().Contains(q) || t.summary.ToLowerInvariant().Contains(q) || string.Join(" ", t.keywords).ToLowerInvariant().Contains(q)) ? Visibility.Visible : Visibility.Collapsed;
        }

        // ---- settings panel -------------------------------------------------
        private void OpenSettings()
        {
            SelectByTag(BrainCombo, _settings.Brain);
            PopulateVoices();
            RefreshKeyStatus();
            SettingsOverlay.Visibility = Visibility.Visible;
            _ = PopulateMicsAsync();
            if (_bridge.Ready) { _bridge.SetListening(true); }   // live meter
        }

        private void CloseSettings()
        {
            if (_bridge.Ready && !_listening) _bridge.SetListening(false);
            SettingsOverlay.Visibility = Visibility.Collapsed;
            _settings.Save();
            UpdateModelText();
        }
        private void CloseSettings_Click(object sender, RoutedEventArgs e) => CloseSettings();

        private async Task PopulateMicsAsync()
        {
            MicCombo.Items.Clear();
            MicCombo.Items.Add(new ComboBoxItem { Content = "System default", Tag = -1 });
            var devs = await VoiceBridge.ListDevicesAsync();
            foreach (var d in devs) MicCombo.Items.Add(new ComboBoxItem { Content = d.Index + ": " + d.Name, Tag = d.Index });
            foreach (ComboBoxItem it in MicCombo.Items) if ((int)it.Tag == _settings.MicDevice) { MicCombo.SelectedItem = it; break; }
            if (MicCombo.SelectedItem == null) MicCombo.SelectedIndex = 0;
        }

        private void PopulateVoices()
        {
            VoiceCombo.Items.Clear();
            VoiceCombo.Items.Add(new ComboBoxItem { Content = "Default (NeuTTS Air)", Tag = "default" });
            if (!string.IsNullOrEmpty(_settings.Voice) && _settings.Voice != "default")
                VoiceCombo.Items.Add(new ComboBoxItem { Content = "Cloned: " + System.IO.Path.GetFileName(_settings.Voice), Tag = _settings.Voice });
            SelectByTag(VoiceCombo, _settings.Voice);
            if (VoiceCombo.SelectedItem == null) VoiceCombo.SelectedIndex = 0;
        }

        private void RefreshKeyStatus()
        {
            KeyStatus.Text = Settings.HasGeminiKey()
                ? "GEMINI_API_KEY loaded from .env  ✓"
                : "No GEMINI_API_KEY found. Add it to .env, then reopen. (" + _envPath + ")";
        }

        private void Brain_Changed(object sender, SelectionChangedEventArgs e)
        {
            if (BrainCombo.SelectedItem is ComboBoxItem it) { _settings.Brain = (string)it.Tag; _settings.Save(); if (_bridge.Ready) _bridge.Configure(_settings); UpdateModelText(); RefreshKeyStatus(); }
        }
        private void Mic_Changed(object sender, SelectionChangedEventArgs e)
        {
            if (MicCombo.SelectedItem is ComboBoxItem it) { _settings.MicDevice = (int)it.Tag; _settings.Save(); if (_bridge.Ready) _bridge.Configure(_settings); }
        }
        private void Voice_Changed(object sender, SelectionChangedEventArgs e)
        {
            if (VoiceCombo.SelectedItem is ComboBoxItem it) { _settings.Voice = (string)it.Tag; _settings.Save(); if (_bridge.Ready) _bridge.Configure(_settings); UpdateModelText(); }
        }

        private void CloneVoice_Click(object sender, RoutedEventArgs e)
        {
            var dlg = new OpenFileDialog { Title = "Pick a voice sample (.wav) to clone", Filter = "WAV audio|*.wav" };
            if (dlg.ShowDialog() == true) { _settings.Voice = dlg.FileName; _settings.Save(); PopulateVoices(); if (_bridge.Ready) _bridge.Configure(_settings); UpdateModelText(); }
        }

        private void TestVoice_Click(object sender, RoutedEventArgs e)
        {
            if (_bridge.Ready) _bridge.Say("This is how I sound. Change the voice if you hate it.");
            else SetStatus("Voice runtime not ready - run setup-voice.bat.");
        }

        private void OpenEnv_Click(object sender, RoutedEventArgs e)
        {
            try
            {
                if (!System.IO.File.Exists(_envPath))
                    System.IO.File.WriteAllText(_envPath, "# WHORETANA secrets - never committed.\nGEMINI_API_KEY=\n");
                System.Diagnostics.Process.Start(new System.Diagnostics.ProcessStartInfo("notepad.exe", "\"" + _envPath + "\"") { UseShellExecute = true });
            }
            catch { SetStatus("Could not open " + _envPath); }
        }

        private void UpdateModelText()
        {
            string brain = _settings.Brain == "gemini" ? "Gemini 2.5 Flash (realtime)" : "Local - Gemma/Qwen (becky-voice)";
            string voice = (_settings.Voice == "default" || string.IsNullOrEmpty(_settings.Voice)) ? "NeuTTS default" : "cloned " + System.IO.Path.GetFileName(_settings.Voice);
            string rt = VoiceBridge.Installed() ? (_bridge.Ready ? "" : "  (voice runtime starting...)") : "  (voice runtime not installed)";
            ModelText.Text = "brain: " + brain + "   |   voice: " + voice + rt;
        }

        private static void SelectByTag(ComboBox combo, string tag)
        {
            foreach (ComboBoxItem it in combo.Items) if ((it.Tag as string) == tag) { combo.SelectedItem = it; return; }
        }

        // ---- chat log -------------------------------------------------------
        private void AddChat(string who, string text)
        {
            bool me = who == "you";
            var bubble = new Border
            {
                Background = me ? new SolidColorBrush(Color.FromArgb(0x33, 0x22, 0xE8, 0xFF)) : new SolidColorBrush(Color.FromArgb(0x22, 0x8C, 0xF6, 0xFF)),
                BorderBrush = me ? (Brush)FindResource("CyanDeep") : (Brush)FindResource("Cyan"),
                BorderThickness = new Thickness(me ? 0 : 1), CornerRadius = new CornerRadius(4),
                Padding = new Thickness(9, 6, 9, 6), Margin = new Thickness(me ? 40 : 0, 3, me ? 0 : 40, 3),
                HorizontalAlignment = me ? HorizontalAlignment.Right : HorizontalAlignment.Left, MaxWidth = 380,
            };
            bubble.Child = new TextBlock { Text = text, Foreground = (Brush)FindResource(me ? "TextMain" : "CyanBright"), FontFamily = (FontFamily)FindResource("HudFont"), FontSize = 12.5, TextWrapping = TextWrapping.Wrap };
            ChatLog.Children.Add(bubble);
            ChatScroll.ScrollToEnd();
        }

        private void SetStatus(string s) => StatusText.Text = s;
        private static bool IsYes(string s) { s = s.Trim().ToLowerInvariant(); return s is "y" or "yes" or "yeah" or "do it" or "go" or "confirm" or "ok" or "okay"; }
        private static string Trunc(string s, int n) => s.Length <= n ? s : s.Substring(0, n) + " ...";

        // ---- window chrome --------------------------------------------------
        private void Min_Click(object sender, RoutedEventArgs e) => WindowState = WindowState.Minimized;
        private void Max_Click(object sender, RoutedEventArgs e) => WindowState = WindowState == WindowState.Maximized ? WindowState.Normal : WindowState.Maximized;
        private void Close_Click(object sender, RoutedEventArgs e) => Close();
        private void BringToFront() { try { Topmost = true; Activate(); Topmost = false; Focus(); } catch { } }
    }
}
