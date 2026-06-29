using System;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Threading.Tasks;

namespace Whoretana
{
    // Thin, crash-proof shell-out layer. WHORETANA is a .exe that launches other
    // becky .exe's (JSON in / text out) - never a server. Mirrors the proven
    // BeckyWindow runner, extracted so the orb shell and voice client share it.
    public static class ProcessRunner
    {
        // Find becky-go\bin walking up from this exe and prepend it to PATH so the
        // window works when launched from a desktop shortcut, not only a script.
        public static void EnsureToolsOnPath()
        {
            try
            {
                var dir = AppContext.BaseDirectory;
                for (int i = 0; i < 9 && !string.IsNullOrEmpty(dir); i++)
                {
                    var bin = Path.Combine(dir, "becky-go", "bin");
                    if (File.Exists(Path.Combine(bin, "becky-catalog.exe")))
                    {
                        var path = Environment.GetEnvironmentVariable("PATH") ?? "";
                        Environment.SetEnvironmentVariable("PATH", bin + Path.PathSeparator + path);
                        return;
                    }
                    dir = Directory.GetParent(dir)?.FullName;
                }
            }
            catch { /* the window still opens; callers report a clean message */ }
        }

        // Run an exe off the UI thread; capture stdout+stderr; never throw.
        public static Task<(int code, string stdout, string stderr)> RunAsync(
            string exe, string args, string? stdin = null)
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
                        RedirectStandardInput = stdin != null,
                        UseShellExecute = false,
                        CreateNoWindow = true,
                        StandardOutputEncoding = Encoding.UTF8,
                        StandardErrorEncoding = Encoding.UTF8,
                    };
                    using var p = Process.Start(psi);
                    if (p == null) return (-1, "", "could not start " + exe);
                    if (stdin != null)
                    {
                        p.StandardInput.Write(stdin);
                        p.StandardInput.Close();
                    }
                    string so = p.StandardOutput.ReadToEnd();
                    string se = p.StandardError.ReadToEnd();
                    p.WaitForExit();
                    return (p.ExitCode, so, se);
                }
                catch (Exception ex)
                {
                    return (-1, "", exe + " not found or failed: " + ex.Message);
                }
            });
        }

        // exe name = first token of the catalog "example" (e.g. "becky-reaper song ..." -> becky-reaper).
        public static string FirstToken(string s, string fallback)
        {
            s = (s ?? "").Trim();
            if (s.Length == 0) return fallback;
            int sp = s.IndexOf(' ');
            return sp < 0 ? s : s.Substring(0, sp);
        }

        public static string FirstLine(string s)
        {
            if (string.IsNullOrEmpty(s)) return "";
            int nl = s.IndexOf('\n');
            return (nl < 0 ? s : s.Substring(0, nl)).Trim();
        }

        // Open a VISIBLE console running a command - the "launch a CLI instance" action
        // for the top-right agent buttons (e.g. a Claude Code agent, becky-ask TUI).
        public static void LaunchTerminal(string title, string command)
        {
            try
            {
                var psi = new ProcessStartInfo
                {
                    FileName = "cmd.exe",
                    Arguments = "/k title " + title + " ^& " + command,
                    UseShellExecute = true,    // gives it its own window
                    WorkingDirectory = RepoRoot(),
                };
                Process.Start(psi);
            }
            catch { /* a failed launch is not fatal to the shell */ }
        }

        // Best-effort repo root (folder that contains becky-go), for launched CLIs.
        public static string RepoRoot()
        {
            try
            {
                var dir = AppContext.BaseDirectory;
                for (int i = 0; i < 9 && !string.IsNullOrEmpty(dir); i++)
                {
                    if (Directory.Exists(Path.Combine(dir, "becky-go"))) return dir;
                    dir = Directory.GetParent(dir)?.FullName ?? "";
                }
            }
            catch { }
            return Environment.CurrentDirectory;
        }
    }
}
