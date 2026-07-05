using System;
using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Input;
using System.Windows.Interop;

namespace Whoretana
{
    // Always-on-top mini orb (spec 6). Borderless, transparent, no taskbar entry,
    // WS_EX_TOOLWINDOW so it never shows in Alt-Tab. Drag anywhere to move (position
    // persisted in Settings); a plain click (no movement) asks the shell to restore
    // the HUD. MainWindow owns the VoiceBridge and drives Orb from its poll loop.
    public partial class BubbleWindow : Window
    {
        private const int GWL_EXSTYLE = -20;
        private const int WS_EX_TOOLWINDOW = 0x00000080;
        [DllImport("user32.dll")] private static extern int GetWindowLong(IntPtr hWnd, int nIndex);
        [DllImport("user32.dll")] private static extern int SetWindowLong(IntPtr hWnd, int nIndex, int dwNewLong);

        private readonly Settings _settings;
        public event Action? RestoreRequested;

        public BubbleWindow(Settings settings)
        {
            InitializeComponent();
            _settings = settings;
            if (_settings.BubbleX is double x && _settings.BubbleY is double y) { Left = x; Top = y; }
            else
            {
                var wa = SystemParameters.WorkArea;
                Left = wa.Right - Width - 24; Top = wa.Bottom - Height - 24;
            }
            SourceInitialized += (_, __) =>
            {
                var h = new WindowInteropHelper(this).Handle;
                SetWindowLong(h, GWL_EXSTYLE, GetWindowLong(h, GWL_EXSTYLE) | WS_EX_TOOLWINDOW);
            };
        }

        private void Root_MouseLeftButtonDown(object sender, MouseButtonEventArgs e)
        {
            double x0 = Left, y0 = Top;
            try { DragMove(); } catch { }   // blocks until the button is released
            if (Math.Abs(Left - x0) < 4 && Math.Abs(Top - y0) < 4) { RestoreRequested?.Invoke(); return; }
            _settings.BubbleX = Left; _settings.BubbleY = Top; _settings.Save();
        }
    }
}
