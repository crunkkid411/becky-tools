using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Threading.Tasks;

namespace BeckyReview;

/// <summary>
/// Thin bridge to the existing becky-*.exe tools (same principle as becky-window:
/// the GUI never reimplements the engine, it shells out for JSON). Resolves
/// <c>becky-go/bin</c> by walking up from the app, and runs becky-review-index to
/// list/search a case folder offline (no DB, no model).
/// </summary>
public static class BeckyTools
{
    private static string? _binDir;

    /// <summary>The resolved becky-go/bin directory, or null if it could not be found.</summary>
    public static string? BinDir => _binDir ??= ResolveBinDir();

    private static string? ResolveBinDir()
    {
        try
        {
            var dir = AppContext.BaseDirectory;
            for (var i = 0; i < 10 && !string.IsNullOrEmpty(dir); i++)
            {
                var bin = Path.Combine(dir, "becky-go", "bin");
                if (File.Exists(Path.Combine(bin, "becky-review-index.exe")))
                {
                    return bin;
                }
                dir = Directory.GetParent(dir)?.FullName;
            }
        }
        catch
        {
            // ignored: degrade-never-crash; callers handle a null BinDir.
        }
        return null;
    }

    /// <summary>
    /// Index a case folder, optionally ranking transcript cue hits for <paramref name="search"/>.
    /// Returns the parsed result, or null with a human message on failure (never throws).
    /// </summary>
    public static async Task<(ReviewIndex? index, string? error)> ReviewIndexAsync(string folder, string? search)
    {
        var bin = BinDir;
        if (bin == null)
        {
            return (null, "Could not find becky-go\\bin. Build the tools with build-all-tools.bat.");
        }
        var exe = Path.Combine(bin, "becky-review-index.exe");

        var args = new List<string> { "--folder", folder };
        if (!string.IsNullOrWhiteSpace(search))
        {
            args.Add("--search");
            args.Add(search!);
        }

        var (code, stdout, stderr) = await RunAsync(exe, args);
        if (code != 0)
        {
            return (null, string.IsNullOrWhiteSpace(stderr) ? $"becky-review-index exited {code}" : stderr.Trim());
        }
        try
        {
            var idx = JsonSerializer.Deserialize<ReviewIndex>(stdout,
                new JsonSerializerOptions { PropertyNameCaseInsensitive = true });
            return (idx, null);
        }
        catch (Exception ex)
        {
            return (null, "Could not parse becky-review-index output: " + ex.Message);
        }
    }

    /// <summary>Run an exe off the UI thread, capturing stdout+stderr. Never throws.</summary>
    public static Task<(int code, string stdout, string stderr)> RunAsync(string exe, IReadOnlyList<string> args)
    {
        return Task.Run(() =>
        {
            try
            {
                var psi = new ProcessStartInfo
                {
                    FileName = exe,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true,
                    UseShellExecute = false,
                    CreateNoWindow = true,
                };
                foreach (var a in args)
                {
                    psi.ArgumentList.Add(a);
                }
                using var p = Process.Start(psi);
                if (p == null)
                {
                    return (-1, "", "could not start " + exe);
                }
                var so = p.StandardOutput.ReadToEnd();
                var se = p.StandardError.ReadToEnd();
                p.WaitForExit();
                return (p.ExitCode, so, se);
            }
            catch (Exception ex)
            {
                return (-1, "", exe + " failed: " + ex.Message);
            }
        });
    }
}

// --- the JSON shape emitted by becky-review-index (cmd/review-index) ---

public sealed class ReviewIndex
{
    [JsonPropertyName("root")] public string Root { get; set; } = "";
    [JsonPropertyName("videos")] public List<ReviewVideo> Videos { get; set; } = new();
    [JsonPropertyName("candidates")] public List<ReviewCandidate> Candidates { get; set; } = new();
}

public sealed class ReviewVideo
{
    [JsonPropertyName("path")] public string Path { get; set; } = "";
    [JsonPropertyName("name")] public string Name { get; set; } = "";
    [JsonPropertyName("transcript_path")] public string TranscriptPath { get; set; } = "";
    [JsonPropertyName("has_transcript")] public bool HasTranscript { get; set; }
}

public sealed class ReviewCandidate
{
    [JsonPropertyName("source")] public string Source { get; set; } = "";
    [JsonPropertyName("name")] public string Name { get; set; } = "";
    [JsonPropertyName("timestamp")] public double Timestamp { get; set; }
    [JsonPropertyName("end")] public double End { get; set; }
    [JsonPropertyName("text")] public string Text { get; set; } = "";
    [JsonPropertyName("terms")] public List<string> Terms { get; set; } = new();
}
