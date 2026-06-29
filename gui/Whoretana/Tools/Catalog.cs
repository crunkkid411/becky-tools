using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading.Tasks;

namespace Whoretana.Tools
{
    // The tool catalog, the single source of truth, read live from `becky-catalog --json`.
    // Shape: { "tools":[...becky-*.exe...], "ops":[...becky <verb>...] }. WHORETANA never
    // hardcodes the tool list - it shows whatever the catalog reports.
    public sealed class ToolEntry
    {
        public string verb { get; set; } = "";
        public string summary { get; set; } = "";
        public string example { get; set; } = "";
        public string[] keywords { get; set; } = Array.Empty<string>();
        public string tier { get; set; } = "red";
        public string pack { get; set; } = "";

        // Bracketed HUD label, e.g. becky-transcribe -> [TRANSCRIBE]
        public string Label
        {
            get
            {
                var v = verb.StartsWith("becky-") ? verb.Substring(6) : verb;
                return "[" + v.ToUpperInvariant().Replace('-', '_') + "]";
            }
        }
    }

    public sealed class CatalogDoc
    {
        public ToolEntry[] tools { get; set; } = Array.Empty<ToolEntry>();
        public ToolEntry[] ops { get; set; } = Array.Empty<ToolEntry>();

        public IEnumerable<ToolEntry> AllByPack(string pack)
        {
            foreach (var t in tools) if (string.Equals(t.pack, pack, StringComparison.OrdinalIgnoreCase)) yield return t;
        }
    }

    public static class Catalog
    {
        public static async Task<CatalogDoc?> LoadAsync()
        {
            var (code, stdout, _) = await ProcessRunner.RunAsync("becky-catalog", "--json");
            if (code != 0 || string.IsNullOrWhiteSpace(stdout)) return null;
            try
            {
                return JsonSerializer.Deserialize<CatalogDoc>(stdout,
                    new JsonSerializerOptions { PropertyNameCaseInsensitive = true });
            }
            catch { return null; }
        }
    }
}
