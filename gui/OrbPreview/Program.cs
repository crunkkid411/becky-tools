using System;
using System.Windows;

namespace OrbPreview
{
    public static class Program
    {
        [STAThread]
        public static void Main(string[] args)
        {
            // `--script demo` (or bare `--script`) plays the canned 20 s state sequence.
            bool script = Array.IndexOf(args, "--script") >= 0;
            var app = new Application { ShutdownMode = ShutdownMode.OnMainWindowClose };
            app.Run(new MainWindow(script));
        }
    }
}
