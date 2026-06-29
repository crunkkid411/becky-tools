using System;
using System.Text.Json;
using System.Threading.Tasks;

namespace Whoretana.Voice
{
    // Text-only router used for the typed chat box and as the fallback when the audio
    // sidecar is not running. Routes an utterance to becky-voice (NDJSON) and falls back
    // to becky-ask --question. NO audio here - all audio lives in the FastRTC sidecar.
    public sealed class VoiceClient
    {
        public string Pack { get; set; } = "default";
        public string? Target { get; set; }

        public sealed class Reply
        {
            public string Text { get; set; } = "";
            public string Tool { get; set; } = "";
            public string Tier { get; set; } = "green";
            public string Action { get; set; } = "none";
            public bool NeedConfirm { get; set; }
            public bool ViaVoice { get; set; }
        }

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

            string q = text.Replace("\"", "'");
            string args = "--question \"" + q + "\"";
            if (!string.IsNullOrEmpty(Target)) args += " --target \"" + Target + "\"";
            var (_, o2, e2) = await ProcessRunner.RunAsync("becky-ask", args);
            string ans = !string.IsNullOrWhiteSpace(o2) ? o2.Trim()
                       : !string.IsNullOrWhiteSpace(e2) ? ProcessRunner.FirstLine(e2)
                       : "I couldn't reach my tools right now.";
            return new Reply { Text = ans, Action = "none", Tier = "green", ViaVoice = false };
        }

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
    }
}
