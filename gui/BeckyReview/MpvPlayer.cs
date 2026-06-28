using System;
using System.Collections.Concurrent;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.IO.Pipes;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;

namespace BeckyReview;

/// <summary>
/// Drives a native mpv video pane embedded into a Win32 child window (<c>--wid</c>).
/// Video is hardware-decoded and frame-exact (<c>--hr-seek=yes</c>) and never goes
/// through the browser or any TCP server. becky controls it over mpv's JSON IPC
/// (a local named pipe), full-duplex: commands out, replies + property-change events
/// back (so the overlay timecode and timeline scrubber track playback live).
/// </summary>
public sealed class MpvPlayer : IDisposable
{
    private readonly string _mpvExe;
    private readonly string _pipeName;   // short name, e.g. "beckympv"
    private Process? _proc;
    private NamedPipeClientStream? _pipe;
    private StreamWriter? _writer;
    private StreamReader? _reader;
    private readonly SemaphoreSlim _writeLock = new(1, 1);
    private readonly SemaphoreSlim _connectLock = new(1, 1);
    private CancellationTokenSource? _readCts;
    private int _nextRequestId = 100;
    private readonly ConcurrentDictionary<int, TaskCompletionSource<JsonElement>> _pending = new();

    /// <summary>Raised (on a background thread) when playback position changes. Args: (timePos, duration).</summary>
    public event Action<double, double>? PositionChanged;

    private double _lastDuration;

    public MpvPlayer(string mpvExe, string pipeName = "beckympv")
    {
        _mpvExe = mpvExe;
        _pipeName = pipeName;
    }

    public bool IsRunning => _proc is { HasExited: false };

    /// <summary>
    /// Launch mpv embedded into <paramref name="parentHwnd"/>. Starts idle + paused
    /// so a frame can be shown and stepped deterministically.
    /// </summary>
    public void Start(IntPtr parentHwnd, string? initialFile = null)
    {
        if (!File.Exists(_mpvExe))
        {
            throw new FileNotFoundException(
                "mpv.exe was not found. Run gui/BeckyReview/fetch-mpv.ps1 to install the video runtime.",
                _mpvExe);
        }

        var args = new StringBuilder();
        args.Append("--wid=").Append(parentHwnd.ToInt64());
        args.Append(" --input-ipc-server=\\\\.\\pipe\\").Append(_pipeName);
        args.Append(" --hr-seek=yes");          // seek to the EXACT frame (forensic)
        args.Append(" --hwdec=auto-safe");       // GPU decode (D3D11VA/NVDEC on the 3070)
        args.Append(" --keep-open=yes");         // hold the last frame at EOF
        args.Append(" --idle=yes");              // stay alive with no file
        args.Append(" --force-window=yes");      // show the pane immediately
        args.Append(" --no-osc --osc=no");       // becky draws its own controls
        args.Append(" --sub-auto=no --sid=no");  // never burn the sidecar .srt onto the video
        args.Append(" --no-config");             // deterministic: ignore user mpv config
        args.Append(" --pause=yes");             // start on a held frame
        args.Append(" --input-conf=\"").Append(InputConfPath()).Append('"');
        if (!string.IsNullOrEmpty(initialFile))
        {
            args.Append(" -- \"").Append(initialFile).Append('"');
        }

        _proc = Process.Start(new ProcessStartInfo
        {
            FileName = _mpvExe,
            Arguments = args.ToString(),
            UseShellExecute = false,
            CreateNoWindow = true,
        }) ?? throw new InvalidOperationException("Failed to start mpv.exe");
    }

    /// <summary>Forensic key map: arrows frame-step; Shift+arrows seek 1s exact.</summary>
    private string InputConfPath()
    {
        var dir = Path.GetDirectoryName(_mpvExe)!;
        var path = Path.Combine(dir, "becky-input.conf");
        if (!File.Exists(path))
        {
            File.WriteAllText(path,
                "RIGHT frame-step\n" +
                "LEFT frame-back-step\n" +
                "Shift+RIGHT seek 1 exact\n" +
                "Shift+LEFT seek -1 exact\n" +
                "SPACE cycle pause\n");
        }
        return path;
    }

    private async Task EnsurePipeAsync(CancellationToken ct = default)
    {
        if (_writer != null)
        {
            return;
        }
        await _connectLock.WaitAsync(ct);
        try
        {
            if (_writer != null)
            {
                return;
            }

            // mpv creates the pipe a moment after launch; retry briefly.
            var client = new NamedPipeClientStream(".", _pipeName, PipeDirection.InOut, PipeOptions.Asynchronous);
            for (var attempt = 0; attempt < 50; attempt++)
            {
                try
                {
                    await client.ConnectAsync(200, ct);
                    break;
                }
                catch (Exception) when (attempt < 49)
                {
                    await Task.Delay(100, ct);
                }
            }

            _pipe = client;
            _writer = new StreamWriter(client, new UTF8Encoding(false)) { AutoFlush = true };
            _reader = new StreamReader(client, new UTF8Encoding(false));

            _readCts = new CancellationTokenSource();
            _ = Task.Run(() => ReadLoopAsync(_readCts.Token));

            // Observe position so the overlay + timeline update live (events, not polling).
            await WriteRawAsync(JsonSerializer.Serialize(new { command = new object[] { "observe_property", 1, "time-pos" } }), ct);
            await WriteRawAsync(JsonSerializer.Serialize(new { command = new object[] { "observe_property", 2, "duration" } }), ct);
        }
        finally
        {
            _connectLock.Release();
        }
    }

    private async Task WriteRawAsync(string json, CancellationToken ct)
    {
        if (_writer == null) { return; }
        await _writeLock.WaitAsync(ct);
        try
        {
            await _writer.WriteAsync(json.AsMemory(), ct);
            await _writer.WriteAsync("\n".AsMemory(), ct);
        }
        catch (IOException)
        {
            _writer = null;
            _pipe?.Dispose();
            _pipe = null;
        }
        finally
        {
            _writeLock.Release();
        }
    }

    private async Task ReadLoopAsync(CancellationToken ct)
    {
        var reader = _reader;
        if (reader == null) { return; }
        try
        {
            while (!ct.IsCancellationRequested)
            {
                var line = await reader.ReadLineAsync();
                if (line == null) { break; }
                if (line.Length == 0) { continue; }
                HandleLine(line);
            }
        }
        catch
        {
            // pipe closed / mpv exited - stop reading quietly
        }
    }

    private void HandleLine(string line)
    {
        JsonDocument doc;
        try { doc = JsonDocument.Parse(line); }
        catch { return; }
        using (doc)
        {
            var root = doc.RootElement;
            // Command reply (matched by request_id).
            if (root.TryGetProperty("request_id", out var ridEl) && ridEl.TryGetInt32(out var rid))
            {
                if (_pending.TryRemove(rid, out var tcs))
                {
                    if (root.TryGetProperty("data", out var data))
                    {
                        tcs.TrySetResult(data.Clone());
                    }
                    else
                    {
                        tcs.TrySetResult(default);
                    }
                }
                return;
            }
            // Async property-change event.
            if (root.TryGetProperty("event", out var evEl) && evEl.GetString() == "property-change")
            {
                var name = root.TryGetProperty("name", out var nEl) ? nEl.GetString() : null;
                if (name == "duration" && root.TryGetProperty("data", out var dEl) && dEl.ValueKind == JsonValueKind.Number)
                {
                    _lastDuration = dEl.GetDouble();
                }
                else if (name == "time-pos" && root.TryGetProperty("data", out var tEl) && tEl.ValueKind == JsonValueKind.Number)
                {
                    PositionChanged?.Invoke(tEl.GetDouble(), _lastDuration);
                }
            }
        }
    }

    /// <summary>Send a single mpv JSON command: <c>{"command":[...]}</c> (fire-and-forget).</summary>
    public async Task SendAsync(CancellationToken ct = default, params object[] command)
    {
        await EnsurePipeAsync(ct);
        await WriteRawAsync(JsonSerializer.Serialize(new { command }), ct);
    }

    /// <summary>Read an mpv property, awaiting the matching reply. Returns default on failure.</summary>
    public async Task<JsonElement> GetPropertyAsync(string name, CancellationToken ct = default)
    {
        await EnsurePipeAsync(ct);
        var id = Interlocked.Increment(ref _nextRequestId);
        var tcs = new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously);
        _pending[id] = tcs;
        await WriteRawAsync(JsonSerializer.Serialize(new { command = new object[] { "get_property", name }, request_id = id }), ct);
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(2));
        using (timeout.Token.Register(() => { if (_pending.TryRemove(id, out var t)) t.TrySetResult(default); }))
        {
            return await tcs.Task;
        }
    }

    public Task LoadAsync(string file, CancellationToken ct = default)
        => SendAsync(ct, "loadfile", file, "replace");

    public Task SeekAbsAsync(double seconds, CancellationToken ct = default)
        => SendAsync(ct, "seek", seconds, "absolute", "exact");

    public Task SetPauseAsync(bool paused, CancellationToken ct = default)
        => SendAsync(ct, "set_property", "pause", paused);

    public Task FrameStepAsync(CancellationToken ct = default)
        => SendAsync(ct, "frame-step");

    public Task FrameBackStepAsync(CancellationToken ct = default)
        => SendAsync(ct, "frame-back-step");

    /// <summary>
    /// Load a file and begin at an EXACT moment (a clicked search hit or clip
    /// in-point), race-free. mpv's <c>loadfile</c> is asynchronous: it returns the
    /// instant the command is accepted, not when the file is loaded. Issuing a
    /// separate <c>seek</c> right after therefore races the load and is silently
    /// dropped, so the new file plays from 0 — the "plays from the beginning instead
    /// of the timestamp" bug. The fix passes the position as the per-file
    /// <c>start</c> option, so mpv seeks as PART of loading (atomic, no race window);
    /// <c>--hr-seek=yes</c> keeps it frame-exact. The number MUST be formatted with the
    /// invariant culture — a locale comma decimal would split mpv's option list.
    /// </summary>
    public async Task PlayAtAsync(string file, double seconds, bool play, CancellationToken ct = default)
    {
        var at = seconds > 0 ? seconds : 0;
        var startOpt = "start=" + at.ToString(CultureInfo.InvariantCulture);
        // mpv >= 0.38 loadfile signature: <url> <flags> <index> <options>. Index 0 is
        // ignored for the "replace" flag; <options> applies to THIS file only.
        await SendAsync(ct, "loadfile", file, "replace", 0, startOpt);
        await SetPauseAsync(!play, ct);
    }

    public void Dispose()
    {
        try { _readCts?.Cancel(); } catch { /* ignore */ }
        try { _reader?.Dispose(); } catch { /* ignore */ }
        try { _writer?.Dispose(); } catch { /* ignore */ }
        try { _pipe?.Dispose(); } catch { /* ignore */ }
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
        _connectLock.Dispose();
    }
}
