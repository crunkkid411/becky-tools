using System;
using System.IO;
using System.Text.Json;

namespace Whoretana
{
    // User-changeable settings (dialed in from the GUI, never the CLI) + .env loading.
    // Persisted to %LOCALAPPDATA%\Whoretana\settings.json so it survives rebuilds.
    // API keys are NEVER stored here - they live in a repo .env that we load into the
    // process environment for the sidecar/tools to read.
    public sealed class Settings
    {
        public string Brain { get; set; } = "local";     // "local" (Gemma/Qwen) | "gemini" (realtime)
        public int MicDevice { get; set; } = -1;          // sounddevice index, -1 = system default
        public string Voice { get; set; } = "default";    // becky-tts preset name or a reference .wav path
        public string GemmaVariant { get; set; } = "e4b"; // "e4b" | "12b" (BECKY_AVLM_VARIANT)
        public bool LocalEscalation { get; set; } = true; // router no-match -> local Gemma (layer 2)
        public bool WarmLocal { get; set; } = false;      // spawn llama-server eagerly at sidecar start
        public bool HandsFree { get; set; } = false;      // desktop_click/type/press without confirm
        public string[] EyeRevealEvents { get; set; } = new[] { "greeting", "special" }; // empty = never
        public double? BubbleX { get; set; }              // bubble position (null = default corner; NaN breaks STJ)
        public double? BubbleY { get; set; }

        private static string Dir => Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Whoretana");
        private static string FilePath => Path.Combine(Dir, "settings.json");

        public static Settings Load()
        {
            try
            {
                if (File.Exists(FilePath))
                    return JsonSerializer.Deserialize<Settings>(File.ReadAllText(FilePath)) ?? new Settings();
            }
            catch { }
            return new Settings();
        }

        public void Save()
        {
            try
            {
                Directory.CreateDirectory(Dir);
                File.WriteAllText(FilePath, JsonSerializer.Serialize(this,
                    new JsonSerializerOptions { WriteIndented = true }));
            }
            catch { }
        }

        // ---- .env ----------------------------------------------------------
        // Find the repo .env (walk up from the exe) and load KEY=VALUE lines into the
        // process environment. Returns the path it used (existing or the suggested one).
        public static string LoadDotEnv()
        {
            string path = FindEnvPath();
            try
            {
                if (File.Exists(path))
                {
                    foreach (var raw in File.ReadAllLines(path))
                    {
                        var line = raw.Trim();
                        if (line.Length == 0 || line[0] == '#') continue;
                        int eq = line.IndexOf('=');
                        if (eq <= 0) continue;
                        string k = line.Substring(0, eq).Trim();
                        string v = line.Substring(eq + 1).Trim().Trim('"');
                        if (k.Length > 0) Environment.SetEnvironmentVariable(k, v);
                    }
                }
            }
            catch { }
            return path;
        }

        public static string FindEnvPath()
        {
            try
            {
                var dir = AppContext.BaseDirectory;
                for (int i = 0; i < 9 && !string.IsNullOrEmpty(dir); i++)
                {
                    if (Directory.Exists(Path.Combine(dir, "becky-go")))
                        return Path.Combine(dir, ".env");
                    var up = Directory.GetParent(dir)?.FullName;
                    if (up == null) break;
                    dir = up;
                }
            }
            catch { }
            return Path.Combine(ProcessRunner.RepoRoot(), ".env");
        }

        public static bool HasGeminiKey()
        {
            string? k = Environment.GetEnvironmentVariable("GEMINI_API_KEY")
                     ?? Environment.GetEnvironmentVariable("GOOGLE_API_KEY");
            return !string.IsNullOrWhiteSpace(k);
        }
    }
}
