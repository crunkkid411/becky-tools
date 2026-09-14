using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.IO.Pipes;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text;
using System.Threading;
using ScriptPortal.Vegas;

namespace BeckyVegas
{
    // Bridge is the control channel into the running VEGAS: a named pipe,
    // "\\.\pipe\becky-vegas-<pid>", one JSON request line in, one JSON reply line out.
    //
    //   request:  {"id":1,"cmd":"cursor","args":{"seconds":12.5}}
    //   reply:    {"id":1,"ok":true,"result":{...}}   or   {"id":1,"ok":false,"error":"..."}
    //
    // A named pipe instead of the old HTTP port: no port to collide with a VEGAS
    // that did not shut down cleanly (the "VegasAIBridge failed to start HTTP server"
    // dialog), and Windows IPC is what the rest of Jordan's desktop apps speak.
    // Only this Windows user can connect; network clients are refused.
    //
    // The pipe threads never touch VEGAS themselves - every VEGAS call goes through
    // UiThread.Run. Commands that change the project refuse to run while VEGAS is
    // rendering or showing a dialog, and every edit is a single undo step.
    internal sealed class Bridge
    {
        const int ReadTimeoutMs = 15000;
        const int EditTimeoutMs = 30000;
        const int ScriptTimeoutMs = 30 * 60 * 1000;

        readonly Vegas vegas;
        readonly Action showPanel;
        readonly string pipeName;
        volatile bool rendering;
        IntPtr mainWindow;

        internal static string PipeNameFor(int pid)
        {
            return "becky-vegas-" + pid;
        }

        internal Bridge(Vegas vegas, Action showPanel)
        {
            this.vegas = vegas;
            this.showPanel = showPanel;
            pipeName = PipeNameFor(Process.GetCurrentProcess().Id);
            vegas.RenderStarted += (s, e) => { rendering = true; };
            vegas.RenderFinished += (s, e) => { rendering = false; };
        }

        internal void Start()
        {
            Thread accept = new Thread(AcceptLoop);
            accept.IsBackground = true;
            accept.Name = "BeckyVegas pipe";
            accept.Start();
            Log.Info("control pipe \\\\.\\pipe\\" + pipeName);
        }

        void AcceptLoop()
        {
            while (true)
            {
                NamedPipeServerStream server = null;
                try
                {
                    server = new NamedPipeServerStream(pipeName, PipeDirection.InOut,
                        NamedPipeServerStream.MaxAllowedServerInstances, PipeTransmissionMode.Byte,
                        PipeOptions.None, 1 << 16, 1 << 16, Security());
                    server.WaitForConnection();
                    NamedPipeServerStream connection = server;
                    server = null;
                    ThreadPool.QueueUserWorkItem(_ => Serve(connection));
                }
                catch (Exception ex)
                {
                    Log.Error("pipe accept", ex);
                    if (server != null)
                    {
                        server.Dispose();
                    }
                    Thread.Sleep(1000);
                }
            }
        }

        static PipeSecurity Security()
        {
            PipeSecurity security = new PipeSecurity();
            security.AddAccessRule(new PipeAccessRule(WindowsIdentity.GetCurrent().User, PipeAccessRights.FullControl, AccessControlType.Allow));
            security.AddAccessRule(new PipeAccessRule(new SecurityIdentifier(WellKnownSidType.NetworkSid, null), PipeAccessRights.FullControl, AccessControlType.Deny));
            return security;
        }

        void Serve(NamedPipeServerStream connection)
        {
            using (connection)
            {
                try
                {
                    StreamReader reader = new StreamReader(connection, new UTF8Encoding(false), false, 1 << 16);
                    string line = reader.ReadLine();
                    byte[] reply = new UTF8Encoding(false).GetBytes(Handle(line) + "\n");
                    connection.Write(reply, 0, reply.Length);
                    connection.Flush();
                    connection.WaitForPipeDrain();
                }
                catch (IOException)
                {
                    // the client hung up early - nothing to answer
                }
                catch (Exception ex)
                {
                    Log.Error("pipe serve", ex);
                }
            }
        }

        string Handle(string line)
        {
            object id = null;
            Dictionary<string, object> reply = new Dictionary<string, object>();
            try
            {
                Dictionary<string, object> request = BeckyTools.Json().DeserializeObject(line ?? "") as Dictionary<string, object>;
                if (request == null)
                {
                    throw new ArgumentException("send one JSON object per line, e.g. {\"cmd\":\"status\"}");
                }
                request.TryGetValue("id", out id);
                string cmd = BeckyTools.Str(request, "cmd");
                object argsObj;
                Dictionary<string, object> args = request.TryGetValue("args", out argsObj) ? argsObj as Dictionary<string, object> : null;
                reply["result"] = Dispatch(cmd, args ?? new Dictionary<string, object>());
                reply["ok"] = true;
                Log.Info("pipe " + cmd + " ok");
            }
            catch (Exception ex)
            {
                Exception inner = ex is ApplicationException && ex.InnerException != null ? ex.InnerException : ex;
                reply["ok"] = false;
                reply["error"] = inner.Message;
                Log.Info("pipe request failed: " + inner.GetType().Name + ": " + inner.Message);
            }
            reply["id"] = id;
            return BeckyTools.Json().Serialize(reply);
        }

        object Dispatch(string cmd, Dictionary<string, object> a)
        {
            switch (cmd)
            {
                case "ping":
                    return Read(() => new Dictionary<string, object>
                    {
                        { "pong", true },
                        { "pid", Process.GetCurrentProcess().Id },
                        { "vegas_version", vegas.Version },
                        { "extension_version", BeckyVegasModule.ExtensionVersion },
                        { "main_thread_ok", UiThread.IsUiThread }
                    });
                case "help":
                    return Help();
                case "dialogs":
                    return OpenDialogs();
                case "status":
                    return Read(() => ProjectOps.Status(vegas, rendering));
                case "timeline":
                    return Read(() => ProjectOps.TimelineDoc(vegas, false));
                case "selected":
                    return Read(() => ProjectOps.TimelineDoc(vegas, true));
                case "markers":
                    return Read(() => ProjectOps.Markers(vegas));
                case "cursor":
                    Need(a, "seconds");
                    return Edit(() =>
                    {
                        vegas.Transport.CursorPosition = ProjectOps.Tc(BeckyTools.Num(a, "seconds"));
                        vegas.Transport.ViewCursor(true);
                        return ProjectOps.Sec(vegas.Transport.CursorPosition);
                    });
                case "jump":
                    Need(a, "start");
                    return Edit(() =>
                    {
                        double start = BeckyTools.Num(a, "start");
                        double end = BeckyTools.Has(a, "end") ? BeckyTools.Num(a, "end") : start;
                        double selected = ProjectOps.JumpTo(vegas, start, end);
                        return new Dictionary<string, object>
                        {
                            { "cursor", ProjectOps.Sec(vegas.Transport.CursorPosition) },
                            { "cursor_text", vegas.Transport.CursorPosition.ToPositionString() },
                            { "selection_length", selected }
                        };
                    });
                case "play":
                    return Edit(() => { vegas.Transport.Play(); return "playing"; });
                case "stop":
                    return Edit(() => { vegas.Transport.Stop(); return "stopped"; });
                case "pause":
                    return Edit(() => { vegas.Transport.Pause(); return "paused"; });
                case "add_marker":
                    return Edit(() =>
                    {
                        double at = BeckyTools.Has(a, "seconds") ? BeckyTools.Num(a, "seconds") : ProjectOps.Sec(vegas.Transport.CursorPosition);
                        ProjectOps.AddMarker(vegas, at, BeckyTools.Str(a, "label"));
                        return "marker added";
                    });
                case "add_region":
                    Need(a, "start", "end");
                    return Edit(() =>
                    {
                        ProjectOps.AddRegion(vegas, BeckyTools.Num(a, "start"), BeckyTools.Num(a, "end"), BeckyTools.Str(a, "label"));
                        return "region added";
                    });
                case "insert":
                    Need(a, "path");
                    return Edit(() =>
                    {
                        double at = BeckyTools.Has(a, "at") ? BeckyTools.Num(a, "at") : ProjectOps.Sec(vegas.Transport.CursorPosition);
                        double end = ProjectOps.InsertClip(vegas, BeckyTools.Str(a, "path"), BeckyTools.Num(a, "in"), BeckyTools.Num(a, "out"), at);
                        vegas.Transport.CursorPosition = ProjectOps.Tc(end);
                        return new Dictionary<string, object> { { "timeline", at }, { "timeline_end", end }, { "track", ProjectOps.PullsTrackName } };
                    });
                case "search":
                    return Search(a);
                case "show_panel":
                    return Edit(() => { showPanel(); return "Becky Search panel is open"; });
                case "transcribe":
                    Need(a, "path");
                    return BeckyTools.Transcribe(BeckyTools.Str(a, "path"), null);
                case "run_script":
                    Need(a, "path");
                    return Guarded(true, ScriptTimeoutMs, () =>
                    {
                        string path = BeckyTools.Str(a, "path");
                        if (!File.Exists(path))
                        {
                            throw new FileNotFoundException("no script at " + path);
                        }
                        vegas.RunScriptFile(path);
                        return "script finished: " + Path.GetFileName(path);
                    });
                case "command":
                    Need(a, "name");
                    return Edit(() =>
                    {
                        vegas.InvokeCommand(BeckyTools.Str(a, "section"), BeckyTools.Str(a, "name"));
                        return "invoked " + BeckyTools.Str(a, "name");
                    });
                case "snapshot":
                    Need(a, "path");
                    return Read(() =>
                    {
                        string path = BeckyTools.Str(a, "path");
                        RenderStatus status = vegas.SaveSnapshot(path, ImageFileFormat.PNG);
                        return new Dictionary<string, object> { { "path", path }, { "status", status.ToString() } };
                    });
                default:
                    throw new ArgumentException("unknown command '" + cmd + "' - send {\"cmd\":\"help\"} for the list");
            }
        }

        object Search(Dictionary<string, object> a)
        {
            Need(a, "query");
            string query = BeckyTools.Str(a, "query");
            SearchResult result;
            if (BeckyTools.Has(a, "folder"))
            {
                result = BeckyTools.SearchFolder(BeckyTools.Str(a, "folder"), query, null);
            }
            else
            {
                Dictionary<string, object> doc = Read(() => ProjectOps.TimelineDoc(vegas, false));
                result = BeckyTools.SearchTimeline(doc, query, null);
            }
            List<Dictionary<string, object>> hits = new List<Dictionary<string, object>>();
            foreach (Hit h in result.Hits)
            {
                Dictionary<string, object> d = new Dictionary<string, object>();
                d["source"] = h.Source;
                d["text"] = h.Text;
                d["source_start"] = h.SourceStart;
                d["source_end"] = h.SourceEnd;
                if (h.FromTimeline)
                {
                    d["on_timeline"] = h.OnTimeline;
                    if (h.OnTimeline)
                    {
                        d["timeline"] = h.Timeline;
                        d["timeline_end"] = h.TimelineEnd;
                        d["track"] = h.Track;
                    }
                }
                hits.Add(d);
            }
            List<string> untranscribed = new List<string>();
            foreach (MediaFile m in result.Untranscribed())
            {
                untranscribed.Add(m.Path);
            }
            return new Dictionary<string, object> { { "hits", hits }, { "untranscribed", untranscribed } };
        }

        T Read<T>(Func<T> fn)
        {
            return UiThread.Run(fn, ReadTimeoutMs);
        }

        object Edit(Func<object> fn)
        {
            return Guarded(true, EditTimeoutMs, fn);
        }

        // Guarded refuses to change anything while VEGAS is rendering or showing a
        // dialog - acting behind a dialog Jordan has not answered is how a remote
        // command ends up editing the wrong thing.
        object Guarded(bool changesProject, int timeoutMs, Func<object> fn)
        {
            if (changesProject && rendering)
            {
                throw new InvalidOperationException("VEGAS is rendering - wait until the render finishes.");
            }
            List<Dictionary<string, object>> dialogs = OpenDialogs();
            if (dialogs.Count > 0)
            {
                string what = BeckyTools.Str(dialogs[0], "text");
                throw new InvalidOperationException("VEGAS is showing a dialog (\"" + BeckyTools.Str(dialogs[0], "title") +
                    (what.Length > 0 ? ": " + what : "") + "\") - it has to be answered first.");
            }
            return UiThread.Run(fn, timeoutMs);
        }

        static void Need(Dictionary<string, object> a, params string[] keys)
        {
            foreach (string k in keys)
            {
                if (!BeckyTools.Has(a, k))
                {
                    throw new ArgumentException("missing argument '" + k + "'");
                }
            }
        }

        // OpenDialogs lists the visible windows VEGAS has on top of its main window
        // right now (message boxes, error popups, Render As, ...), with their button
        // labels. Pure Win32 - no VEGAS objects - so it is safe off the main thread.
        internal List<Dictionary<string, object>> OpenDialogs()
        {
            List<Dictionary<string, object>> found = new List<Dictionary<string, object>>();
            if (mainWindow == IntPtr.Zero)
            {
                try { mainWindow = UiThread.Run(() => vegas.MainWindow.Handle, 2000); }
                catch (TimeoutException) { }
            }
            uint pid = (uint)Process.GetCurrentProcess().Id;
            IntPtr main = mainWindow;
            Native.EnumWindows((hwnd, _) =>
            {
                uint owner;
                Native.GetWindowThreadProcessId(hwnd, out owner);
                if (owner != pid || hwnd == main || !Native.IsWindowVisible(hwnd))
                {
                    return true;
                }
                // A dialog is owned by VEGAS's main window (or by another dialog of it).
                IntPtr ownerWindow = Native.GetWindow(hwnd, Native.GW_OWNER);
                if (ownerWindow == IntPtr.Zero)
                {
                    return true;
                }
                string title = Native.Text(hwnd);
                string cls = Native.ClassName(hwnd);
                if (title.Length == 0 || cls == "tooltips_class32")
                {
                    return true; // helper windows, not something a person has to answer
                }
                List<string> buttons = new List<string>();
                List<string> text = new List<string>();
                Native.EnumChildWindows(hwnd, (child, __) =>
                {
                    if (!Native.IsWindowVisible(child))
                    {
                        return true;
                    }
                    string childClass = Native.ClassName(child);
                    string label = Native.Text(child).Replace("&", "").Trim();
                    if (label.Length > 0 && childClass == "Button")
                    {
                        buttons.Add(label);
                    }
                    else if (label.Length > 0 && childClass == "Static")
                    {
                        text.Add(label);
                    }
                    return true;
                }, IntPtr.Zero);
                // Floating tool windows (Trimmer, Video Preview, a floating Becky
                // panel) are owned too, but they do not block anything. A modal dialog
                // is the owned window that stays ENABLED while VEGAS disables the rest
                // (measured: the Plug-In Chooser was enabled, every dock window was not).
                bool modalUp = main != IntPtr.Zero && !Native.IsWindowEnabled(main);
                if (cls == "#32770" || (modalUp && Native.IsWindowEnabled(hwnd)))
                {
                    found.Add(new Dictionary<string, object> { { "title", title }, { "text", string.Join(" ", text.ToArray()) }, { "class", cls }, { "buttons", buttons } });
                }
                return true;
            }, IntPtr.Zero);
            return found;
        }

        static object Help()
        {
            return new Dictionary<string, object>
            {
                { "ping", "is this VEGAS alive, and is the main-thread dispatcher working" },
                { "status", "project path/modified, cursor, selection, counts, rendering flag" },
                { "dialogs", "visible VEGAS dialogs and their buttons (commands that change the project refuse while one is open)" },
                { "timeline", "every media event: source, in, out, timeline, track, rate, selected" },
                { "selected", "same as timeline, selected events only" },
                { "markers", "markers and regions" },
                { "cursor", "args: seconds" },
                { "jump", "args: start, end (selects the span and scrolls to it)" },
                { "play / stop / pause", "transport" },
                { "add_marker", "args: seconds (default cursor), label" },
                { "add_region", "args: start, end, label" },
                { "insert", "args: path, in, out (0 = to the end), at (default cursor) - lands on the 'Becky Pulls' tracks" },
                { "search", "args: query [, folder] - transcript search of the timeline, or of a folder" },
                { "show_panel", "opens the Becky Search panel inside VEGAS" },
                { "transcribe", "args: path - writes <clip>_parakeet_transcription.srt beside it" },
                { "run_script", "args: path - runs a VEGAS script (.cs) exactly like Tools > Scripting" },
                { "command", "args: section, name - a VEGAS command by its keyboard.ini name; section is the keyboard.ini context. Proven: section=Global name=Tools.Video.VideoEventFX. TrackView-context commands returned ok but did nothing." },
                { "snapshot", "args: path - saves the current preview frame as PNG" }
            };
        }
    }

    internal static class Native
    {
        internal const uint GW_OWNER = 4;

        internal delegate bool EnumProc(IntPtr hwnd, IntPtr lParam);

        [DllImport("user32.dll")]
        internal static extern bool EnumWindows(EnumProc callback, IntPtr lParam);

        [DllImport("user32.dll")]
        internal static extern bool EnumChildWindows(IntPtr parent, EnumProc callback, IntPtr lParam);

        [DllImport("user32.dll")]
        internal static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint processId);

        [DllImport("user32.dll")]
        internal static extern bool IsWindowVisible(IntPtr hwnd);

        [DllImport("user32.dll")]
        internal static extern bool IsWindowEnabled(IntPtr hwnd);

        [DllImport("user32.dll")]
        internal static extern IntPtr GetWindow(IntPtr hwnd, uint cmd);

        [DllImport("user32.dll")]
        internal static extern IntPtr GetForegroundWindow();

        [DllImport("user32.dll", CharSet = CharSet.Unicode)]
        static extern int GetWindowText(IntPtr hwnd, StringBuilder text, int max);

        [DllImport("user32.dll", CharSet = CharSet.Unicode)]
        static extern int GetClassName(IntPtr hwnd, StringBuilder name, int max);

        internal static string Text(IntPtr hwnd)
        {
            StringBuilder sb = new StringBuilder(512);
            GetWindowText(hwnd, sb, sb.Capacity);
            return sb.ToString();
        }

        internal static string ClassName(IntPtr hwnd)
        {
            StringBuilder sb = new StringBuilder(256);
            GetClassName(hwnd, sb, sb.Capacity);
            return sb.ToString();
        }
    }
}
