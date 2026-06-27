using System;
using System.Diagnostics;
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
/// (a local named pipe), so a clicked clip can load+seek to an exact moment.
/// </summary>
public sealed class MpvPlayer : IDisposable
{
    private readonly string _mpvExe;
    private readonly string _pipeName;   // short name, e.g. "beckympv"
    private Process? _proc;
    private NamedPipeClientStream? _pipe;
    private StreamWriter? _writer;
    private readonly SemaphoreSlim _writeLock = new(1, 1);

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
    }

    /// <summary>Send a single mpv JSON command: <c>{"command":[...]}</c>.</summary>
    public async Task SendAsync(CancellationToken ct = default, params object[] command)
    {
        await EnsurePipeAsync(ct);
        if (_writer == null)
        {
            return;
        }

        var json = JsonSerializer.Serialize(new { command });
        await _writeLock.WaitAsync(ct);
        try
        {
            await _writer.WriteAsync(json.AsMemory(), ct);
            await _writer.WriteAsync("\n".AsMemory(), ct);
        }
        catch (IOException)
        {
            // pipe dropped (mpv closed) - reset so the next call reconnects
            _writer = null;
            _pipe?.Dispose();
            _pipe = null;
        }
        finally
        {
            _writeLock.Release();
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

    /// <summary>Load a file and pause on an exact moment (a clicked clip's in-point).</summary>
    public async Task PlayAtAsync(string file, double seconds, bool play, CancellationToken ct = default)
    {
        await LoadAsync(file, ct);
        await SeekAbsAsync(seconds, ct);
        await SetPauseAsync(!play, ct);
    }

    public void Dispose()
    {
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
    }
}
