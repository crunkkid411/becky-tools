using System;
using System.IO;
using System.Text;
using System.Threading;
using System.Windows.Forms;

namespace BeckyVegas
{
    // UiThread runs work on VEGAS's MAIN thread - the only thread allowed to touch
    // any ScriptPortal.Vegas object (VEGAS scripting FAQ, application extensions:
    // "must interact with Vegas and its project data on its main application thread").
    //
    // This is the whole reason the old VegasAIBridge never worked. It captured
    // SynchronizationContext.Current inside InitializeModule, which is NULL there in
    // VEGAS, and its helper then fell into "No UI context - just run directly" - so
    // every request touched VEGAS from the HTTP thread and died with
    // "Unable to cast COM object ... IVegasCOM ... E_NOINTERFACE". Its write-up
    // blamed VEGAS. The fix is a real window handle: a Control created on the main
    // thread belongs to that thread, and BeginInvoke on it always runs there.
    internal static class UiThread
    {
        static Control marshal;
        static int uiThreadId;

        // Init must be called on VEGAS's main thread. InitializeModule is; the log
        // line proves it on every start (compare the thread id with the ones logged
        // by Run below).
        internal static void Init()
        {
            uiThreadId = Thread.CurrentThread.ManagedThreadId;
            marshal = new Control();
            IntPtr forceHandle = marshal.Handle; // the HWND is created NOW, on this thread
            SynchronizationContext ctx = SynchronizationContext.Current;
            Log.Info("main thread id=" + uiThreadId +
                     " apartment=" + Thread.CurrentThread.GetApartmentState() +
                     " syncContext=" + (ctx == null ? "null" : ctx.GetType().Name) +
                     " hwnd=0x" + forceHandle.ToInt64().ToString("X"));
        }

        internal static bool IsUiThread
        {
            get { return Thread.CurrentThread.ManagedThreadId == uiThreadId; }
        }

        // Post queues work on the main thread and returns immediately.
        internal static void Post(Action action)
        {
            marshal.BeginInvoke((MethodInvoker)delegate
            {
                try { action(); }
                catch (Exception ex) { Log.Error("posted work", ex); }
            });
        }

        // Run executes fn on the main thread and returns its result. If VEGAS is busy
        // for longer than timeoutMs (a render, a modal dialog, a long script) the call
        // gives up AND the queued work is cancelled, so it can never fire late: an edit
        // that lands 40 seconds after the caller gave up is worse than no edit. Work
        // that has already STARTED is always waited for - never abandon a half-done edit.
        internal static T Run<T>(Func<T> fn, int timeoutMs)
        {
            if (IsUiThread)
            {
                return fn();
            }
            using (ManualResetEvent done = new ManualResetEvent(false))
            {
                int state = 0; // 0 = queued, 1 = started, 2 = abandoned
                T result = default(T);
                Exception error = null;
                marshal.BeginInvoke((MethodInvoker)delegate
                {
                    if (Interlocked.CompareExchange(ref state, 1, 0) != 0)
                    {
                        return; // the caller already gave up
                    }
                    try { result = fn(); }
                    catch (Exception ex) { error = ex; }
                    finally { done.Set(); }
                });
                if (!done.WaitOne(timeoutMs))
                {
                    if (Interlocked.CompareExchange(ref state, 2, 0) == 0)
                    {
                        throw new TimeoutException("VEGAS is busy right now (a render, an open dialog or a running script), so nothing was changed. Try again when it is idle.");
                    }
                    done.WaitOne();
                }
                if (error != null)
                {
                    throw new ApplicationException(error.Message, error);
                }
                return result;
            }
        }
    }

    // Log writes to %APPDATA%\BeckyVegas\becky-vegas.log. Every entry point of the
    // extension catches its own exceptions and logs them here - an exception that
    // escapes into VEGAS can take the whole editor down with Jordan's project in it.
    internal static class Log
    {
        static readonly object gate = new object();
        const long MaxBytes = 5L * 1024 * 1024;

        internal static string Dir
        {
            get { return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "BeckyVegas"); }
        }

        internal static string FilePath
        {
            get { return Path.Combine(Dir, "becky-vegas.log"); }
        }

        internal static void Info(string msg)
        {
            Write("INFO ", msg);
        }

        internal static void Error(string where, Exception ex)
        {
            Write("ERROR", where + ": " + ex);
        }

        static void Write(string level, string msg)
        {
            try
            {
                lock (gate)
                {
                    Directory.CreateDirectory(Dir);
                    FileInfo fi = new FileInfo(FilePath);
                    if (fi.Exists && fi.Length > MaxBytes)
                    {
                        File.Copy(FilePath, FilePath + ".old", true);
                        File.Delete(FilePath);
                    }
                    File.AppendAllText(FilePath,
                        DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss.fff") + " " + level +
                        " [t" + Thread.CurrentThread.ManagedThreadId + "] " + msg + Environment.NewLine,
                        Encoding.UTF8);
                }
            }
            catch
            {
                // Logging must never be the thing that breaks VEGAS.
            }
        }
    }
}
