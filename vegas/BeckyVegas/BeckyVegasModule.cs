// BeckyVegas - becky-tools inside VEGAS Pro 18, as an Application Extension.
//
// Two things live in this one DLL:
//
//   1. The "Becky Search" panel (View > Extensions > Becky Search): type words,
//      find every place they are spoken - on the timeline you have open, or in any
//      folder of footage - and jump the cursor to it or pull the line onto the
//      timeline. The search itself is becky-review-index, the same engine as Becky
//      Review; transcription is becky-transcribe.
//
//   2. A local control channel (named pipe "becky-vegas-<pid>") so Claude Code,
//      becky tools and Whoretana can read and drive the running VEGAS. The
//      becky-vegas.exe command-line tool is its client.
//
// It is an extension, not an OpenFX plugin, on purpose: the OFX kit VEGAS ships
// (Sony Vegas Video Plug-in SDK + ofxSonyVegas.h) gives a plugin parameters, a
// custom panel and the timeline cursor, but no access to the project, its tracks,
// its events or the files on them - so an OFX plugin cannot search a timeline.
//
// The one rule everything here obeys: VEGAS objects are touched ONLY on VEGAS's
// main thread (see UiThread.cs for why the old VegasAIBridge broke that rule).
//
// Loaded from "Documents\Vegas Application Extensions" (no admin), or for a test
// session only: vegas180.exe -CMDMODULE:"<path>\BeckyVegas.dll".
using System;
using System.Collections;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.IO.Pipes;
using System.Text;
using System.Threading;
using System.Windows.Forms;
using ScriptPortal.Vegas;

namespace BeckyVegas
{
    public sealed class BeckyVegasModule : ICustomCommandModule
    {
        internal const string ExtensionVersion = "1.0.0";

        // Never rename these two: VEGAS stores keyboard shortcuts, toolbar buttons
        // and the panel's dock position under these names.
        internal const string PanelInstanceName = "BeckyVegas.SearchPanel";
        const string PanelCommandName = "BeckyVegas.ShowSearchPanel";

        Vegas vegas;
        CustomCommand panelCommand;
        Bridge bridge;
        System.Windows.Forms.Timer focusTimer;
        bool wasFocused;

        void CheckFocus()
        {
            uint owner;
            Native.GetWindowThreadProcessId(Native.GetForegroundWindow(), out owner);
            bool focused = owner == (uint)Process.GetCurrentProcess().Id;
            if (focused && !wasFocused)
            {
                Presence.Publish(vegas, true);
            }
            wasFocused = focused;
        }

        public void InitializeModule(Vegas vegas)
        {
            try
            {
                this.vegas = vegas;
                Log.Info("BeckyVegas " + ExtensionVersion + " starting in VEGAS " + vegas.Version +
                         " pid=" + Process.GetCurrentProcess().Id + " dll=" + typeof(BeckyVegasModule).Assembly.Location);
                UiThread.Init();
                bridge = new Bridge(vegas, ShowPanel);
                bridge.Start();
                vegas.ProjectOpened += (s, e) => SafeUi("ProjectOpened", () => { Presence.Publish(vegas, false); });
                vegas.ProjectSaved += (s, e) => SafeUi("ProjectSaved", () => { Presence.Publish(vegas, false); });
                // Queued, so it runs once VEGAS's message loop is up - not during its splash screen.
                UiThread.Post(() => Presence.Publish(vegas, false));
                // Vegas.AppActivated never fired for this extension in VEGAS 18 (measured
                // 2026-09-13), so notice focus the plain way: once a second, is the window
                // in front one of ours? One Win32 call, on the main thread.
                focusTimer = new System.Windows.Forms.Timer { Interval = 1000 };
                focusTimer.Tick += (s, e) => SafeUi("focus check", CheckFocus);
                focusTimer.Start();
            }
            catch (Exception ex)
            {
                Log.Error("InitializeModule", ex);
            }
        }

        public ICollection GetCustomCommands()
        {
            try
            {
                panelCommand = new CustomCommand(CommandCategory.View, PanelCommandName);
                panelCommand.DisplayName = "Becky Search";
                panelCommand.MenuItemName = "Becky Search";
                panelCommand.Invoked += (s, e) => SafeUi("show panel", ShowPanel);
                panelCommand.MenuPopup += (s, e) => SafeUi("menu popup", () => { panelCommand.Checked = vegas.FindDockView(PanelInstanceName); });
                return new CustomCommand[] { panelCommand };
            }
            catch (Exception ex)
            {
                Log.Error("GetCustomCommands", ex);
                return new CustomCommand[0];
            }
        }

        void ShowPanel()
        {
            if (vegas.ActivateDockView(PanelInstanceName))
            {
                return;
            }
            SearchPanel panel = new SearchPanel(vegas);
            panel.AutoLoadCommand = panelCommand;
            panel.PersistDockWindowState = true;
            vegas.LoadDockView(panel);
        }

        static void SafeUi(string where, Action action)
        {
            try
            {
                action();
            }
            catch (Exception ex)
            {
                Log.Error(where, ex);
            }
        }
    }

    // Presence tells the outside world this VEGAS exists, what it has open and when
    // Jordan last focused it:
    //   - %LOCALAPPDATA%\BeckyVegas\instances\<pid>.json, which becky-vegas.exe reads
    //     to find the right pipe (several VEGAS windows can be open at once);
    //   - one NDJSON line to Whoretana's pipe (\\.\pipe\Whoretana) on focus, the
    //     {"active_app", "project_path", "timeline_state"} payload from
    //     X:\AI-2\CLAUDE.md. Whoretana ignores commands it does not know yet, so this
    //     is harmless until it reads them; if Whoretana is not running it costs one
    //     failed 50 ms connect on a background thread.
    internal static class Presence
    {
        internal static string InstancesDir
        {
            get { return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "BeckyVegas", "instances"); }
        }

        // Publish must be called on the main thread (it reads the project).
        internal static void Publish(Vegas vegas, bool focused)
        {
            int pid = Process.GetCurrentProcess().Id;
            string project = vegas.Project == null ? "" : (vegas.Project.FilePath ?? "");
            Dictionary<string, object> info = new Dictionary<string, object>();
            info["pid"] = pid;
            info["pipe"] = Bridge.PipeNameFor(pid);
            info["extension_version"] = BeckyVegasModule.ExtensionVersion;
            info["vegas_version"] = vegas.Version;
            info["project_path"] = project;
            info["updated"] = DateTime.UtcNow.ToString("o");
            if (focused)
            {
                info["last_focused"] = DateTime.UtcNow.ToString("o");
            }
            else
            {
                string previous = ReadField(pid, "last_focused");
                if (previous.Length > 0)
                {
                    info["last_focused"] = previous;
                }
            }
            WriteInstance(pid, BeckyTools.Json().Serialize(info));

            if (!focused)
            {
                return;
            }
            Dictionary<string, object> state = new Dictionary<string, object>();
            state["cursor"] = ProjectOps.Sec(vegas.Transport.CursorPosition);
            state["selection_start"] = ProjectOps.Sec(vegas.Transport.SelectionStart);
            state["selection_length"] = ProjectOps.Sec(vegas.Transport.SelectionLength);
            state["playing"] = vegas.Transport.IsPlaying;
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["cmd"] = "active_app";
            payload["active_app"] = "vegas_pro";
            payload["project_path"] = project;
            payload["pipe"] = Bridge.PipeNameFor(pid);
            payload["timeline_state"] = state;
            string line = BeckyTools.Json().Serialize(payload);
            ThreadPool.QueueUserWorkItem(_ => SendToWhoretana(line));
        }

        static void SendToWhoretana(string line)
        {
            try
            {
                using (NamedPipeClientStream pipe = new NamedPipeClientStream(".", "Whoretana", PipeDirection.Out))
                {
                    pipe.Connect(50);
                    byte[] bytes = new UTF8Encoding(false).GetBytes(line + "\n");
                    pipe.Write(bytes, 0, bytes.Length);
                    pipe.Flush();
                }
            }
            catch (TimeoutException)
            {
                // Whoretana is not running - nothing to tell.
            }
            catch (IOException)
            {
                // busy or gone mid-write; the next focus change sends a fresh state
            }
        }

        static void WriteInstance(int pid, string json)
        {
            try
            {
                Directory.CreateDirectory(InstancesDir);
                string path = Path.Combine(InstancesDir, pid + ".json");
                string tmp = path + ".tmp";
                File.WriteAllText(tmp, json, new UTF8Encoding(false));
                if (File.Exists(path))
                {
                    File.Delete(path);
                }
                File.Move(tmp, path);
            }
            catch (Exception ex)
            {
                Log.Error("write instance file", ex);
            }
        }

        static string ReadField(int pid, string field)
        {
            try
            {
                string path = Path.Combine(InstancesDir, pid + ".json");
                if (!File.Exists(path))
                {
                    return "";
                }
                Dictionary<string, object> d = BeckyTools.Json().DeserializeObject(File.ReadAllText(path)) as Dictionary<string, object>;
                return BeckyTools.Str(d, field);
            }
            catch (Exception)
            {
                return "";
            }
        }
    }
}
