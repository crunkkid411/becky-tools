using System;
using System.Windows;
using System.Windows.Threading;

namespace Whoretana
{
    // App entry. Degrade, never crash: an unhandled UI-thread exception shows a
    // message and keeps running rather than killing the window (repo invariant).
    public partial class App : Application
    {
        public App()
        {
            DispatcherUnhandledException += OnUnhandled;
        }

        private void OnUnhandled(object sender, DispatcherUnhandledExceptionEventArgs e)
        {
            try
            {
                MessageBox.Show(
                    "Something glitched, but WHORETANA is still here.\n\n" + e.Exception.Message,
                    "WHORETANA", MessageBoxButton.OK, MessageBoxImage.Warning);
            }
            catch
            {
                // even the message failed; swallow so we never hard-crash.
            }
            e.Handled = true;
        }
    }
}
