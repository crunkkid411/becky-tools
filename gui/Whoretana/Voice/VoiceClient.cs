using System;
using System.IO;
using System.Text.Json;
using System.Threading.Tasks;
using Whoretana.Audio;

namespace Whoretana.Voice
{
    // The talk/chat brain. A typed or spoken utterance is ROUTED:
    //   1. preferred: pipe an NDJSON intent to becky-voice (the deterministic Go driver
    //      from HANDOFF-BECKY-VOICE.md) -> it gates by tier, runs green tools, returns a
    //      spoken line + decision.
    //   2. fallback: becky-ask --question (single-shot) if becky-voice is not built yet.
    // Spoken loop: record mic -> becky-transcribe -> route -> becky-tts -> play (lip-sync).
    // Everything degrades to a clear message; nothing throws to the UI.
    public sealed class VoiceClient
    {
        private readonly AudioEngine _audio;
        public string Pack { get; set; } = "default";
        public string? Target { get; set; }   // optional file the action applies to

        public VoiceClient(AudioEngine audio) { _audio = audio; }

        public sealed class Reply
        {
            public string Text { get; set; } = "";
            public string Tool { get; set; } = "";
            public string Tier { get; set; } = "green";
            public string Action { get; set; } = "none";
            public bool NeedConfirm { get; set; }
            public bool ViaVoice { get; set; }   // true if becky-voice answered (vs fallback)
        }

        // Route a typed/spoken request. Prefer becky-voice; fall back to becky-ask.
        public async Task<Reply> RouteTextAsync(string text, bool confirm = false)
        {
            string intent = JsonSerializer.Serialize(new
            {
                type = "intent",
                text,
                target = Target ?? "",
                pack = Pack,
                confirm,
            }) + "\n";

            var (code, so, _) = await ProcessRunner.RunAsync("becky-voice", "", intent);
            if (code == 0 && !string.IsNullOrWhiteSpace(so))
            {
                var rep = ParseEvent(so);
                if (rep != null) { rep.ViaVoice = true; return rep; }
            }

            // fallback: becky-ask single-shot
            string q = text.Replace("\"", "'");
            string args = "--question \"" + q + "\"";
            if (!string.IsNullOrEmpty(Target)) args += " --target \"" + Target + "\"";
            var (c2, o2, e2) = await ProcessRunner.RunAsync("becky-ask", args);
            string ans = !string.IsNullOrWhiteSpace(o2) ? o2.Trim()
                       : !string.IsNullOrWhiteSpace(e2) ? ProcessRunner.FirstLine(e2)
                       : "I couldn't reach my tools right now.";
            return new Reply { Text = ans, Action = "none", Tier = "green", ViaVoice = false };
        }

        // Parse the last JSON event line becky-voice emitted.
        private static Reply? ParseEvent(string stdout)
        {
            var lines = stdout.Replace("\r", "").Split('\n');
            for (int i = lines.Length - 1; i >= 0; i--)
            {
                string ln = lines[i].Trim();
                if (ln.Length < 2 || ln[0] != '{') continue;
                try
                {
                    using var doc = JsonDocument.Parse(ln);
                    var r = doc.RootElement;
                    return new Reply
                    {
                        Text = Str(r, "text"),
                        Tool = Str(r, "tool"),
                        Tier = Str(r, "tier", "green"),
                        Action = Str(r, "action", "none"),
                        NeedConfirm = r.TryGetProperty("need_confirm", out var nc) && nc.ValueKind == JsonValueKind.True,
                    };
                }
                catch { }
            }
            return null;
        }

        private static string Str(JsonElement e, string k, string dflt = "")
            => e.TryGetProperty(k, out var v) && v.ValueKind == JsonValueKind.String ? (v.GetString() ?? dflt) : dflt;

        // ---- spoken loop ----------------------------------------------------
        public string StartTalk() => _audio.BeginUtterance();

        public async Task<(string heard, Reply reply)> StopTalkAndProcessAsync()
        {
            _audio.EndUtterance();
            string wav = Path.Combine(Path.GetTempPath(), "whoretana_utt.wav");
            var (code, so, _) = await ProcessRunner.RunAsync("becky-transcribe", "\"" + wav + "\" --format txt");
            string heard = (code == 0 ? so : "").Trim();
            if (string.IsNullOrWhiteSpace(heard))
                return ("", new Reply { Text = "I didn't catch that.", Action = "none" });
            var reply = await RouteTextAsync(heard);
            return (heard, reply);
        }

        // Synthesize a line and play it (drives the lip-sync mouth). Returns false if TTS
        // is unavailable so the caller can still show the text.
        public async Task<bool> SpeakAsync(string text)
        {
            if (string.IsNullOrWhiteSpace(text)) return false;
            try
            {
                string txt = Path.Combine(Path.GetTempPath(), "whoretana_reply.txt");
                string wav = Path.Combine(Path.GetTempPath(), "whoretana_say.wav");
                File.WriteAllText(txt, text);
                var (code, _, _) = await ProcessRunner.RunAsync("becky-tts", "--in \"" + txt + "\" --out \"" + wav + "\"");
                if (code == 0 && File.Exists(wav))
                {
                    _audio.SpeakWav(wav);
                    return true;
                }
            }
            catch { }
            return false;
        }
    }
}
