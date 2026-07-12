using System;
using System.Diagnostics;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using Whoretana.Orb;

namespace OrbPreview
{
    // Dev harness (spec 2.8): every OrbInput field on a slider, EyeReveal button,
    // FPS counter, and a canned 20 s looping state sequence (`--script demo`).
    public partial class MainWindow : Window
    {
        private readonly Stopwatch _script = new Stopwatch();
        private int _frames;
        private double _fpsLast;
        private readonly Stopwatch _fpsClock = Stopwatch.StartNew();
        private int _eyeFiredLoop = -1;

        public MainWindow() : this(false) { }

        public MainWindow(bool script)
        {
            InitializeComponent();
            CompositionTarget.Rendering += OnFrame;
            Closed += (_, __) => CompositionTarget.Rendering -= OnFrame;
            if (script) StartScript();
        }

        private void StartScript()
        {
            _eyeFiredLoop = -1;
            _script.Restart();
        }

        private void OnFrame(object? sender, EventArgs e)
        {
            _frames++;
            double now = _fpsClock.Elapsed.TotalSeconds;
            if (now - _fpsLast >= 1.0)
            {
                FpsText.Text = $"{_frames / (now - _fpsLast):0} fps";
                _fpsLast = now;
                _frames = 0;
            }
            if (_script.IsRunning) DriveScript();
        }

        // Canned 20 s sequence, loops: idle -> listening -> thinking -> speaking
        // (visemes + eye reveal at t=10) -> idle. Drives the sliders so the UI
        // shows what the orb is being fed.
        private void DriveScript()
        {
            double total = _script.Elapsed.TotalSeconds;
            int loop = (int)(total / 20.0);
            double t = total % 20.0;
            string phase;

            if (t < 2)
            {
                phase = "idle";
                ModeBox.SelectedIndex = 0;
                MicS.Value = 0; SpeechS.Value = 0;
                JawS.Value = 0; FunnelS.Value = 0; PuckerS.Value = 0; SmileS.Value = 0;
            }
            else if (t < 5)
            {
                phase = "listening";
                ModeBox.SelectedIndex = 1;
                MicS.Value = 0.35 + 0.45 * Math.Abs(Math.Sin(t * 5.0));
                SpeechS.Value = 0;
            }
            else if (t < 8)
            {
                phase = "thinking";
                ModeBox.SelectedIndex = 2;
                MicS.Value = 0; SpeechS.Value = 0;
            }
            else if (t < 16)
            {
                phase = "speaking";
                ModeBox.SelectedIndex = 3;
                MicS.Value = 0;
                double sp = 0.35 + 0.45 * Math.Abs(Math.Sin(t * 6.0));
                SpeechS.Value = sp;
                JawS.Value = sp;
                FunnelS.Value = Math.Max(0, 0.25 + 0.25 * Math.Sin(t * 2.3));
                PuckerS.Value = FunnelS.Value * 0.5;
                SmileS.Value = 0.15;
                if (t >= 10 && _eyeFiredLoop != loop)
                {
                    _eyeFiredLoop = loop;
                    Orb.TriggerEyeReveal(1.2f);
                }
            }
            else
            {
                phase = "idle (wind-down)";
                ModeBox.SelectedIndex = 0;
                MicS.Value = 0; SpeechS.Value = 0;
                JawS.Value = 0; FunnelS.Value = 0; PuckerS.Value = 0; SmileS.Value = 0;
            }
            ScriptText.Text = $"script {t:0.0}s  {phase}";
        }

        // ---- manual controls -------------------------------------------------
        private void ModeChanged(object sender, SelectionChangedEventArgs e)
        {
            if (Orb != null) Orb.Mode = (OrbMode)ModeBox.SelectedIndex;
        }

        private void LevelChanged(object sender, RoutedPropertyChangedEventArgs<double> e)
        {
            if (Orb == null) return;
            Orb.MicLevel = (float)MicS.Value;
            Orb.SpeechLevel = (float)SpeechS.Value;
        }

        private void VisemeChanged(object sender, RoutedPropertyChangedEventArgs<double> e)
        {
            if (Orb == null) return;
            Orb.SetVisemes((float)JawS.Value, (float)FunnelS.Value, (float)PuckerS.Value, (float)SmileS.Value);
        }

        private void EyeClicked(object sender, RoutedEventArgs e) => Orb.TriggerEyeReveal(1.2f);

        private void DemoClicked(object sender, RoutedEventArgs e) => StartScript();
    }
}
