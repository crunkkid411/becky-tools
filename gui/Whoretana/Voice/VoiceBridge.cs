using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Threading.Tasks;

namespace Whoretana.Voice
{
    // Owns ALL audio by talking to the Python FastRTC sidecar (voice/whoretana_voice.py).
    // No managed audio. The GUI spawns the sidecar as a child process and exchanges NDJSON:
    //   control  (stdin):  {cmd:"config"|"listen"|"standby"|"say"|"text"|"devices"|"quit", ...}
    //   events  (stdout):  {type:"ready"|"state"|"level"|"transcript"|"reply"|"viseme"|"wake"|
    //                            "special"|"brain"|"devices"|"error"|"log", ...}
    // Level/state/visemes/brain are polled as fields (smooth orb); the rest raise events.
    public sealed class VoiceBridge : IDisposable
    {
        public sealed record Device(int Index, string Name);

        private Process? _proc;
        private readonly object _wlock = new object();

        public bool Ready { get; private set; }
        public string State { get; private set; } = "idle";   // idle|listening|thinking|speaking
        public float Level { get; private set; }              // 0..1 current amplitude
        public float Jaw { get; private set; }                // viseme weights 0..1, ~20 Hz stream
        public float Funnel { get; private set; }
        public float Pucker { get; private set; }
        public float Smile { get; private set; }
        public string Brain { get; private set; } = "router"; // router|gemma4|gemini (last brain event)
        public string? LastError { get; private set; }

        public event Action<string>? StateChanged;
        public event Action<string>? Transcript;              // what it heard
        public event Action<Reply>? ReplyReceived;            // spoken reply + routing
        public event Action? WakeDetected;                    // wake word matched in standby
        public event Action<string, float>? SpecialEvent;     // kind ("eye_reveal"), duration seconds
        public event Action<string>? BrainChanged;
        public event Action<string>? Errored;
        public event Action<string>? Log;

        public sealed class Reply
        {
            public string Text = "";
            public string Tool = "";
            public string Tier = "green";
            public string Action = "none";
            public bool NeedConfirm;
        }

        private static string VenvPython()
            => Path.Combine(ProcessRunner.RepoRoot(), "models", "voice", "venv", "Scripts", "python.exe");
        private static string Script()
            => Path.Combine(ProcessRunner.RepoRoot(), "gui", "Whoretana", "voice", "whoretana_voice.py");

        public static bool Installed() => File.Exists(VenvPython()) && File.Exists(Script());

        // Start the long-lived sidecar with the current settings.
        public bool Start(Settings s)
        {
            if (!Installed()) { LastError = "voice runtime not installed (run setup-voice.bat)"; return false; }
            try
            {
                var psi = new ProcessStartInfo
                {
                    FileName = VenvPython(),
                    Arguments = "\"" + Script() + "\"",
                    RedirectStandardInput = true,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true,
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    StandardOutputEncoding = Encoding.UTF8,
                    WorkingDirectory = ProcessRunner.RepoRoot(),
                };
                _proc = Process.Start(psi);
                if (_proc == null) { LastError = "could not start the voice sidecar"; return false; }
                _ = ReadLoopAsync(_proc.StandardOutput);
                _ = ErrLoopAsync(_proc.StandardError);
                Ready = true;
                Configure(s);
                return true;
            }
            catch (Exception ex) { LastError = ex.Message; return false; }
        }

        public void Configure(Settings s) => Send(new
        {
            cmd = "config", brain = s.Brain, mic = s.MicDevice, voice = s.Voice,
            hands_free = s.HandsFree, local_escalation = s.LocalEscalation, warm_local = s.WarmLocal,
        });
        public void SetListening(bool on) => Send(new { cmd = "listen", on });
        public void SetStandby(bool on) => Send(new { cmd = "standby", on });
        public void Say(string text) => Send(new { cmd = "say", text });
        public void SendText(string text, bool confirm = false) => Send(new { cmd = "text", text, confirm });

        private void Send(object o)
        {
            try
            {
                lock (_wlock)
                {
                    if (_proc is { HasExited: false }) { _proc.StandardInput.WriteLine(JsonSerializer.Serialize(o)); _proc.StandardInput.Flush(); }
                }
            }
            catch { }
        }

        private async Task ReadLoopAsync(StreamReader r)
        {
            try
            {
                string? line;
                while ((line = await r.ReadLineAsync()) != null) Handle(line);
            }
            catch { }
            Ready = false;
        }

        private async Task ErrLoopAsync(StreamReader r)
        {
            try { string? l; while ((l = await r.ReadLineAsync()) != null) Log?.Invoke(l); } catch { }
        }

        private void Handle(string line)
        {
            line = line.Trim();
            if (line.Length < 2 || line[0] != '{') return;
            try
            {
                using var doc = JsonDocument.Parse(line);
                var r = doc.RootElement;
                string type = S(r, "type");
                switch (type)
                {
                    case "ready": Ready = true; break;
                    case "level": Level = (float)D(r, "value"); break;
                    case "state":
                        State = S(r, "state", "idle"); StateChanged?.Invoke(State); break;
                    case "transcript": Transcript?.Invoke(S(r, "text")); break;
                    case "reply":
                        ReplyReceived?.Invoke(new Reply
                        {
                            Text = S(r, "text"), Tool = S(r, "tool"),
                            Tier = S(r, "tier", "green"), Action = S(r, "action", "none"),
                            NeedConfirm = r.TryGetProperty("need_confirm", out var nc) && nc.ValueKind == JsonValueKind.True,
                        });
                        break;
                    case "viseme":
                        Jaw = (float)D(r, "jaw"); Funnel = (float)D(r, "funnel");
                        Pucker = (float)D(r, "pucker"); Smile = (float)D(r, "smile");
                        break;
                    case "wake": WakeDetected?.Invoke(); break;
                    case "special": SpecialEvent?.Invoke(S(r, "kind"), (float)D(r, "dur")); break;
                    case "brain": Brain = S(r, "name", "router"); BrainChanged?.Invoke(Brain); break;
                    case "error": LastError = S(r, "text"); Errored?.Invoke(LastError ?? ""); break;
                    case "log": Log?.Invoke(S(r, "text")); break;
                }
            }
            catch { }
        }

        // One-shot device enumeration (does not need the long-lived sidecar running).
        public static async Task<List<Device>> ListDevicesAsync()
        {
            var list = new List<Device>();
            if (!Installed()) return list;
            var (code, so, _) = await ProcessRunner.RunAsync(VenvPython(), "\"" + Script() + "\" --list-devices");
            if (code != 0 || string.IsNullOrWhiteSpace(so)) return list;
            try
            {
                using var doc = JsonDocument.Parse(so.Trim());
                if (doc.RootElement.TryGetProperty("devices", out var arr) && arr.ValueKind == JsonValueKind.Array)
                    foreach (var d in arr.EnumerateArray())
                        list.Add(new Device(d.GetProperty("index").GetInt32(), d.GetProperty("name").GetString() ?? "?"));
            }
            catch { }
            return list;
        }

        private static string S(JsonElement e, string k, string dflt = "")
            => e.TryGetProperty(k, out var v) && v.ValueKind == JsonValueKind.String ? (v.GetString() ?? dflt) : dflt;
        private static double D(JsonElement e, string k)
            => e.TryGetProperty(k, out var v) && v.ValueKind == JsonValueKind.Number ? v.GetDouble() : 0;

        public void Dispose()
        {
            try { Send(new { cmd = "quit" }); } catch { }
            try { if (_proc is { HasExited: false }) { _proc.Kill(true); } } catch { }
            try { _proc?.Dispose(); } catch { }
        }
    }
}
