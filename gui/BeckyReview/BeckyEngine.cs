using System;
using System.Collections.Concurrent;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;

namespace BeckyReview;

/// <summary>
/// The persistent becky-clip engine, driven over stdin/stdout (the `bridge` subcommand
/// of a headless becky-clip build, shipped as becky-review-engine.exe). ONE long-lived
/// process keeps the folder index + transcript parse-cache WARM, so repeat searches are
/// instant (the fix for "search got slower" — the old per-call exe re-indexed every time).
///
/// It exposes the entire becky-clip verb set (open_folder, search, transcript, add_clip,
/// set_trim, reorder, transcribe, ask, export, ...) as one async <see cref="CallAsync"/>.
/// Replies are tagged by request id and matched back to the caller.
/// </summary>
public sealed class BeckyEngine : IDisposable
{
    private readonly string _engineExe;
    private Process? _proc;
    private readonly SemaphoreSlim _writeLock = new(1, 1);
    private int _nextId;
    private readonly ConcurrentDictionary<string, TaskCompletionSource<JsonElement>> _pending = new();

    public BeckyEngine(string engineExe)
    {
        _engineExe = engineExe;
    }

    public bool IsRunning => _proc is { HasExited: false };

    /// <summary>Spawn the engine and start reading replies. Throws if the exe is missing.</summary>
    public void Start()
    {
        if (!File.Exists(_engineExe))
        {
            throw new FileNotFoundException(
                "becky-review-engine.exe was not found. Build it: go build -o bin\\becky-review-engine.exe ./cmd/clip",
                _engineExe);
        }
        var psi = new ProcessStartInfo
        {
            FileName = _engineExe,
            Arguments = "bridge",
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true,
            StandardOutputEncoding = new UTF8Encoding(false),
            StandardInputEncoding = new UTF8Encoding(false),
        };
        _proc = Process.Start(psi) ?? throw new InvalidOperationException("Failed to start becky-review-engine.exe");
        _ = Task.Run(ReadLoopAsync);
        // Drain stderr so the engine never blocks on a full pipe (diagnostics ignored).
        _ = Task.Run(async () =>
        {
            try { await _proc.StandardError.ReadToEndAsync(); } catch { /* ignore */ }
        });
    }

    private async Task ReadLoopAsync()
    {
        var reader = _proc!.StandardOutput;
        try
        {
            string? line;
            while ((line = await reader.ReadLineAsync()) != null)
            {
                if (line.Length == 0) { continue; }
                try
                {
                    using var doc = JsonDocument.Parse(line);
                    var root = doc.RootElement;
                    if (!root.TryGetProperty("id", out var idEl)) { continue; }
                    var id = idEl.GetString();
                    if (id != null && _pending.TryRemove(id, out var tcs))
                    {
                        var reply = root.TryGetProperty("reply", out var r) ? r.Clone() : default;
                        tcs.TrySetResult(reply);
                    }
                }
                catch { /* ignore a malformed line */ }
            }
        }
        catch { /* pipe closed / engine exited */ }
        // Fail any still-pending calls so awaiters don't hang forever.
        foreach (var kv in _pending)
        {
            kv.Value.TrySetResult(default);
        }
    }

    /// <summary>
    /// Run a bridge verb. Returns the reply envelope element ({ok,data,error}); inspect
    /// it with <see cref="Ok"/> / <see cref="Data"/>. Never throws on engine errors.
    /// </summary>
    public async Task<JsonElement> CallAsync(string verb, object? args = null, CancellationToken ct = default)
    {
        if (!IsRunning)
        {
            return default;
        }
        var id = "r" + Interlocked.Increment(ref _nextId);
        var tcs = new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously);
        _pending[id] = tcs;

        var req = JsonSerializer.Serialize(new { id, verb, args });
        await _writeLock.WaitAsync(ct);
        try
        {
            await _proc!.StandardInput.WriteLineAsync(req.AsMemory(), ct);
            await _proc.StandardInput.FlushAsync();
        }
        catch
        {
            _pending.TryRemove(id, out _);
            return default;
        }
        finally
        {
            _writeLock.Release();
        }
        return await tcs.Task;
    }

    /// <summary>True when a reply envelope reports success.</summary>
    public static bool Ok(JsonElement reply)
        => reply.ValueKind == JsonValueKind.Object
           && reply.TryGetProperty("ok", out var ok)
           && ok.ValueKind == JsonValueKind.True;

    /// <summary>The reply's data element (or default if absent).</summary>
    public static JsonElement Data(JsonElement reply)
        => reply.ValueKind == JsonValueKind.Object && reply.TryGetProperty("data", out var d) ? d : default;

    /// <summary>The reply's error string (or "").</summary>
    public static string Error(JsonElement reply)
        => reply.ValueKind == JsonValueKind.Object && reply.TryGetProperty("error", out var e) && e.ValueKind == JsonValueKind.String
            ? e.GetString() ?? ""
            : "";

    public void Dispose()
    {
        try { _proc?.StandardInput.Close(); } catch { /* ignore */ }
        try
        {
            if (_proc is { HasExited: false })
            {
                _proc.Kill(entireProcessTree: true);
            }
        }
        catch { /* ignore */ }
        _proc?.Dispose();
        _writeLock.Dispose();
    }
}
