# becky-audio-host

The native **C++ audio + VST3 host sidecar** for becky (GUI-RULES.md Phases 2-3).
A standalone `becky-audio-host.exe` that the Go engine drives over the NDJSON-stdio
seam (GUI-RULES.md section 2). It hosts Jordan's VST3 plugins (MIT VST3 SDK) and outputs
to his audio interface via PortAudio (WASAPI by default; ASIO when the Steinberg ASIO
SDK is supplied).

This is a **control-plane sidecar**: NDJSON on stdin/stdout is its control plane; audio
samples never cross the JSON seam. The realtime audio thread is owned inside this process.

## Build (Jordan's PC)

```powershell
# from native/audio-host
.\scripts\fetch-deps.ps1       # clone VST3 SDK + PortAudio + nlohmann/json into third_party/
.\scripts\build.ps1            # configure + build with MinGW g++ -> build/becky-audio-host.exe
.\scripts\build.ps1 -SelfTest  # ...and run the headless proof
```

Toolchain: MinGW-w64 g++ (`C:\msys64\mingw64\bin`, override with `$env:BECKY_MINGW_BIN`),
CMake, and Ninja or mingw32-make on PATH. `third_party/` and `build/` are gitignored.

### ASIO (low-latency to the UR12)

The Steinberg ASIO SDK is account-gated, so it is **not** fetched automatically. To
enable ASIO:

1. Download the ASIO SDK from Steinberg and extract it.
2. Set `BECKY_ASIO_SDK` to its extracted folder (or drop it at `third_party/asiosdk`).
3. Re-run `scripts\build.ps1`.

Without it, the host uses **WASAPI** (works now on the UR12). With it, the host prefers
**ASIO** for the default interface.

## The seam protocol (NDJSON over stdio)

One UTF-8 JSON object per line. **stdout is ONLY protocol; all logging goes to stderr.**

- in  (stdin): `{"type":"command"|"query","id":"<str>","name":"<verb>","args":{...}}`
- out (stdout): `{"type":"response","id":"<id>","ok":true,"data":{...}}`
                | `{"type":"response","id":"<id>","ok":false,"error":"<msg>"}`
                | `{"type":"event","name":"<verb>","data":{...}}`

On startup it emits `{"type":"event","name":"ready","data":{"sidecar":"audio-host","version":"..."}}`.
Bad input is answered with `ok:false`, never a crash.

### Verbs

| verb | args | notes |
|---|---|---|
| `ping` | - | `{pong,version}` |
| `audio.devices` | - | output devices (marks default + ASIO); `{default_output,default_host_api,asio_available,devices:[...]}` |
| `audio.open` | `{device?,samplerate?,buffer?}` | default device = system default interface; prefers ASIO > WASAPI > MME |
| `audio.start` / `audio.stop` | - | start/stop the output stream |
| `vst.scan` | `{dir?,recursive?}` | enumerate `.vst3` (default dir `C:\Program Files\Common Files\VST3`, recursive into vendor subfolders); each plugin probed in a **child process** so a faulting plugin is skipped, not fatal |
| `vst.load` | `{path,samplerate?,buffer?}` | instantiate component+controller, set up 32-bit float processing, activate busses; `{instanceId,name,params:[{id,title,default,...}],hasEditor,outChannels}` |
| `vst.param.list` | `{instanceId}` | current parameter values |
| `vst.param.set` | `{instanceId,paramId,value}` | normalized [0,1] |
| `note.on` | `{instanceId,pitch,velocity,channel?}` | queues a VST3 NoteOn for the next process block |
| `note.off` | `{instanceId,pitch,channel?}` | queues a VST3 NoteOff |
| `vst.editor.open` | `{instanceId}` | **partial** (see below) |
| `render` | `{instanceId?\|path,events?,durationSec,sampleRate?,buffer?,out}` | **OFFLINE** render to a WAV; `{out,frames,channels,sampleRate,peak,rms,nonSilent}` |
| `shutdown` | - | clean exit |
| `--probe <path>` (argv) | - | internal: child process that loads ONE plugin and prints its JSON |

`render` is the headless-verifiable proof the host actually processes audio: it runs the
plugin's processor for `durationSec`, applying note/param events (instruments get a
default C4; effects get a test tone fed to their input), and writes a 16-bit PCM WAV.

## Self-test (headless proof)

`becky-audio-host --selftest` scans the standard VST3 dir, loads a real plugin, renders
~2s offline, confirms the WAV is non-silent, and prints PASS/FAIL to stderr. If no
plugins are installed it says so honestly and still PASSes the protocol/scan path.

## Honest status

- **Fully working + verified** (built with MinGW g++ 15.1; selftest PASS on Jordan's
  real 309-plugin library; render corroborated by ffmpeg volumedetect):
  `audio.devices`, `audio.open`/`start`/`stop` (opens the real UR12 via WASAPI),
  `vst.scan` (crash-isolated, recursive), `vst.load`, `vst.param.list`/`set`,
  `note.on`/`off`, `render` (real plugin -> non-silent WAV), the full NDJSON round-trip.
- **Partial - `vst.editor.open`:** reports the plugin's editor exists + its requested
  size, but honestly returns `attached:false`. Actually attaching + pumping an
  `IPlugView` needs a parent window and a UI run-loop, which belong in the Gio shell
  (GUI-RULES Phase 3), not this headless control-plane sidecar.
- **ASIO:** detected at build time; WASAPI is used until Jordan supplies the ASIO SDK.
- **Live low-latency playback to the UR12** can only be confirmed by Jordan (no audio
  hardware in CI). The device opens + starts; the realtime callback pulls from a
  registered render source.

## License facts (GUI-RULES.md section 6)

VST3 SDK = MIT, PortAudio = MIT, nlohmann/json = MIT, ASIO SDK = GPLv3/proprietary
(account-gated, fetched by Jordan, never committed). `third_party/` is gitignored.
