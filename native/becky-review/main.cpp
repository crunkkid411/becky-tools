// becky-review - the full-native single-window Becky Review (phases 3+4 start).
//
// Grown from native/becky-timeline (Dear ImGui + D3D11).
// ONE process owns the whole window - no WebView2, no WPF, no airspace:
//   left  = library / search / transcript (ImGui)
//   center = video pane (in-process engine.cpp, D3D11VA hw decode, no subprocess)
//   right = Q&A / ask-becky (ImGui)
//   bottom = native timeline (the seed's code, in-process instead of embedded)
//
// The Go engine (becky-review-engine.exe, the clip cmd's stdin/stdout bridge mode)
// is the ONE brain: folder index, search, qmd, reel/EDL, peaks, export,
// ask. This process is VIEW/CONTROLLER only - every edit routes to engine verbs,
// engine undo is THE undo. NDJSON seam = {"id","verb","args":{...}} -> {"id","reply":{ok,data,error}}.
//
// D-1 (2026-07-19) launched the video pane on mpv; step 6 of the overnight
// mpv-swap mission (2026-07-23, HANDOFF-VIDEO-ENGINE.md/SPEC-BECKY-VIDEO-ENGINE.md)
// deleted it entirely - see the "D-1 (step 6 rewrite)" block below for the
// current native engine architecture. GStreamer stays linked/initialized
// (gstInitSEH) only as a legacy runtime dependency; since cycle 23 the waveform
// peaks are decoded by ffmpeg (see decodeWindow), because the gst uridecodebin
// pipeline hangs on real corpus files.
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <shellapi.h>
#include <commdlg.h>
#include <d3d11.h>
#include <dxgi1_3.h>
#include <dwmapi.h>    // DwmFlush - efficient windowed vsync pacing that replaces Present(1,0)'s
                       // driver busy-wait (the ~345% idle-CPU spin); see the render loop.
#pragma comment(lib, "dwmapi.lib")
#include <timeapi.h>   // I-1 fix: timeBeginPeriod(1) - see the frame-pacing timer in the render loop.
                       // CREATE_WAITABLE_TIMER_HIGH_RESOLUTION only sharpens the TIMER's own due-time;
                       // the thread's actual WAKE-UP after the timer signals still rides the system
                       // clock-interrupt rate, which defaults to ~15.6ms/64Hz and adds wake jitter on
                       // top of a precisely-fired timer. Raising it to 1ms/1000Hz tightens that jitter.
#pragma comment(lib, "winmm.lib")
#include <wincodec.h> // E-11: WIC decodes the thumb verb's JPEG - native platform feature, no image lib dependency
#include "imgui.h"
#include "imgui_impl_win32.h"
#include "imgui_impl_dx11.h"
#include "misc/freetype/imgui_freetype.h"   // color-emoji rasterizer (Segoe UI Emoji)
#include "json.hpp"
#include "engine.h" // the native video engine (mpv replacement, step 6)

// HYBRID-GPU FIX (found 2026-07-22, chasing the reviewer's "idle CPU regressed to
// 300-370%, higher even than the documented 46.9% post-fix baseline" report). The
// 490%->47% fix (2c6fb53) correctly diagnosed DWM recompositing an overlapping
// child HWND every frame as the mechanism - but on Jordan's hybrid-graphics laptop
// (RTX 3070 + Intel iGPU), `D3D11CreateDeviceAndSwapChain(nullptr, ...)` with a
// null adapter does not necessarily pick the RTX 3070: `nvidia-smi` shows
// becky-review.exe absent from the GPU's process list entirely (0% GPU util)
// while burning 3+ CPU cores sustained even fully idle with nothing loaded -
// exactly the signature of DWM (which composites on whichever GPU is the desktop's
// primary) having to CPU-copy this window's frames from a different adapter every
// present. These two exported globals are the standard, decade-old fix laptop
// drivers look for (NVIDIA Optimus / AMD PowerXpress): they tell the driver this
// process wants the DISCRETE GPU, not whatever Windows defaulted it to. Zero-cost,
// no adapter-enumeration code needed - the driver reads the export by symbol name.
extern "C" { __declspec(dllexport) DWORD NvOptimusEnablement = 1; }
extern "C" { __declspec(dllexport) int AmdPowerXpressRequestHighPerformance = 1; }

#include <gst/gst.h>
#include <gst/app/gstappsink.h>
#include <gst/video/video.h>
#include <cstdio>
#include <cstdint>
#include <cstring>
#include <cctype>
#include <cmath>
#include <algorithm>
#include <atomic>
#include <condition_variable>
#include <deque>
#include <functional>   // engineCallAsync completion callbacks
#include <map>
#include <memory>
#include <mutex>
#include <set>
#include <string>
#include <thread>
#include <vector>
#include <iostream>
#include <fstream>
#include <sstream>
#include <array>
#include <exception>
#include <iomanip>   // std::fixed/setprecision - editLog needs ms resolution (default double
                     // formatting is 6 sig figs, which is only tenths-of-a-second once process
                     // uptime passes ~10000s, useless for measuring a sub-100ms round trip)
#include <csignal>   // SIGABRT hook: names the thread+stack behind any abort() (bug-1 forensics)
using json = nlohmann::json;

static double nowSec() {
    static LARGE_INTEGER fq = [] { LARGE_INTEGER f; QueryPerformanceFrequency(&f); return f; }();
    LARGE_INTEGER c; QueryPerformanceCounter(&c);
    return (double)c.QuadPart / fq.QuadPart;
}
static void fwslash(std::string& s) { for (auto& c : s) if (c == '\\') c = '/'; }
static void editLog(const std::string& line);   // fwd decl - defined below, used inside engineCall for diagnosis
static thread_local const char* t_threadTag = "main/ui";   // set at each named thread's entry; read by the crash-log terminate handler
static std::string baseName(const std::string& p) {
    size_t i = p.find_last_of("/\\");
    return i == std::string::npos ? p : p.substr(i + 1);
}

// --------------- Go engine seam (subprocess, NDJSON over stdin/stdout) ---------------
// Spawned once at boot: becky-review-engine.exe (clip cmd, bridge mode). One warm
// process = the folder index + transcript cache stay hot (the whole point of the engine).
struct Engine {
    PROCESS_INFORMATION pi = {};
    HANDLE hin = nullptr, hout = nullptr;   // our write-end of its stdin, read-end of its stdout
    std::mutex mx;                      // guards the request id counter + reply map
    std::mutex writeMx;                 // serializes WriteFile() - multiple threads call engineCall()
    std::condition_variable cv;
    std::map<std::string, json> replies;   // id -> reply envelope (ok/data/error)
    std::map<std::string, bool> seen;     // id -> received
    std::atomic<uint64_t> nextId{ 1 };
    std::atomic<bool> alive{ false };
    std::string lastError;
};
static Engine g_engine;

// H-5: what becky has been doing, so Jordan can see it WITHOUT being interrupted.
//
// BUILD_1.md §4-H's H-5 requires the engine to announce agent activity "so the
// right panel shows what the AI is doing without blocking Jordan's own editing".
// The contract is in HANDOFF-VIDEOAGENT-SEAM.md: the engine pushes
// {"event":{"kind","source","text"}} lines down the SAME NDJSON stdio bridge as
// replies, distinguished by having no "id".
//
// Written only by the engine reader thread, read only by the UI thread, both
// under g_activityMx - and neither ever waits on the other, because the entire
// point is that an AI pass narrating itself must never cost him a frame.
struct Activity {
    std::string kind;    // "started" | "progress" | "done"
    std::string source;  // which verb produced it, e.g. "ask", "apply_edit_batch"
    std::string text;    // one line of plain language, already human-readable
    double at = 0;
};
static std::deque<Activity> g_activityLog;
static std::mutex g_activityMx;

static bool engineStart() {
    std::lock_guard<std::mutex> lk(g_engine.mx);
    if (g_engine.alive) return true;
    // Prefer the built engine next to the repo; fall back to the known bin path.
    std::string exe = "X:/AI-2/becky-tools/becky-go/bin/becky-review-engine.exe";
    if (!std::ifstream(exe)) exe = "X:/AI-2/becky-tools/becky-go/bin/clip.exe";
    if (!std::ifstream(exe)) { g_engine.lastError = "engine exe not found"; return false; }
    fwslash(exe);

    HANDLE childInR = nullptr, childInW = nullptr, childOutR = nullptr, childOutW = nullptr;
    SECURITY_ATTRIBUTES sa{ sizeof sa, nullptr, TRUE };
    if (!CreatePipe(&childInR, &childInW, &sa, 0)) return false;
    if (!CreatePipe(&childOutR, &childOutW, &sa, 0)) { CloseHandle(childInR); CloseHandle(childInW); return false; }
    // Inherit only the ends the child needs; keep ours non-inherited.
    SetHandleInformation(childInW, HANDLE_FLAG_INHERIT, 0);
    SetHandleInformation(childOutR, HANDLE_FLAG_INHERIT, 0);

    STARTUPINFOW si{ sizeof si };
    si.dwFlags = STARTF_USESTDHANDLES | STARTF_USESHOWWINDOW;
    si.hStdInput = childInR; si.hStdOutput = childOutW; si.hStdError = GetStdHandle(STD_ERROR_HANDLE);
    si.wShowWindow = SW_HIDE;

    std::wstring wex = std::wstring(exe.begin(), exe.end());
    std::wstring cmd; cmd += L'"'; cmd += wex; cmd += L'"'; cmd += L" bridge";
    if (!CreateProcessW(nullptr, &cmd[0], nullptr, nullptr, TRUE, CREATE_NO_WINDOW, nullptr, nullptr, &si, &g_engine.pi)) {
        CloseHandle(childInR); CloseHandle(childInW); CloseHandle(childOutR); CloseHandle(childOutW);
        g_engine.lastError = "CreateProcess failed";
        return false;
    }
    CloseHandle(childInR); CloseHandle(childOutW);   // child owns these duplicates
    g_engine.hin = childInW; g_engine.hout = childOutR;
    g_engine.alive = true;
    return true;
}

// reader: parse the engine's {"id":..,"reply":{..}} lines, stash by id.
// I-1 FIX (found live against the real E:\TakingBack2007 corpus, 2258 videos):
// this used to hold the whole in-flight line in a FIXED 64KB buffer. open_folder's
// reply for a real multi-thousand-clip corpus is well over 64KB of JSON with no
// newline before the buffer fills, so `kBuf - held - 1` hit 0, ReadFile returned
// got=0, and the `got > 0` loop condition silently exited - engineReader thought
// the engine had died (it hadn't) and every in-flight call reported "engine
// timeout / no reply" in single-digit milliseconds. Looked exactly like an engine
// crash; was actually a fixed-size accumulator with no room for a big reply. Fix:
// accumulate into a std::string that grows with the reply (no line-length cap) -
// each ReadFile still reads a bounded 64KB chunk, but a partial line just carries
// over to the next read instead of being capped.
static void engineReader() {
    t_threadTag = "engineReader";
    std::string buf;
    char chunk[1 << 16];
    DWORD got = 0;
    while (g_engine.hout && ReadFile(g_engine.hout, chunk, sizeof chunk, &got, nullptr) && got > 0) {
        buf.append(chunk, got);
        size_t nl;
        while ((nl = buf.find('\n')) != std::string::npos) {
            std::string line = buf.substr(0, nl);
            buf.erase(0, nl + 1);
            if (line.empty()) continue;
            try {
                json j = json::parse(line);
                if (j.contains("id") && j.contains("reply")) {
                    std::lock_guard<std::mutex> lk(g_engine.mx);
                    std::string id = j["id"].get<std::string>();
                    g_engine.replies[id] = j["reply"];
                    g_engine.seen[id] = true;
                    g_engine.cv.notify_all();
                } else if (j.contains("event") && j["event"].is_object()) {
                    // H-5: the engine's AI-activity stream. These lines carry NO
                    // "id" - they are pushed, not requested - so they must be
                    // handled here rather than by the reply router above.
                    //
                    // The point of H-5 is that Jordan can SEE what becky is doing
                    // without it blocking him. This branch therefore only appends
                    // to a small deque; it never touches the timeline, never waits,
                    // and never notifies the edit path. His editing cannot be
                    // slowed down by the AI narrating itself.
                    //
                    // H-2: every field is read with a defaulting accessor rather
                    // than operator[]. This runs on the reader thread that also
                    // delivers every engine REPLY - a throw here from one malformed
                    // event would take the whole app's engine communication down
                    // with it. A bad event is dropped, never fatal.
                    const json& ev = j["event"];
                    Activity a;
                    a.kind = ev.value("kind", std::string());
                    a.source = ev.value("source", std::string());
                    a.text = ev.value("text", std::string());
                    a.at = nowSec();
                    if (!a.text.empty()) {
                        std::lock_guard<std::mutex> lk(g_activityMx);
                        g_activityLog.push_back(std::move(a));
                        // A status feed, not a database. Oldest falls off so a long
                        // session cannot grow this without bound.
                        while (g_activityLog.size() > 50) g_activityLog.pop_front();
                    }
                }
            } catch (...) {}
        }
    }
    std::lock_guard<std::mutex> lk(g_engine.mx);
    g_engine.alive = false;
    g_engine.cv.notify_all();
}

// Fire-and-wait: send a verb, block until its reply (or engine death). Returns the reply
// envelope; ok=false with an error string on timeout/death. Thread-safe.
static json engineCall(const std::string& verb, const json& args, double timeoutSec = 20.0) {
    editLog("engineCall(" + verb + ") enter");
    if (!g_engine.alive) { if (!engineStart()) return { {"ok",false}, {"error","engine not running"} }; }
    std::string id;
    { std::lock_guard<std::mutex> lk(g_engine.mx); id = "c" + std::to_string(g_engine.nextId.fetch_add(1)); }
    json req = { {"id", id}, {"verb", verb}, {"args", args} };
    std::string line = req.dump() + "\n";
    DWORD written = 0;
    editLog("engineCall(" + verb + ") id=" + id + " about to write");
    {
        // Multiple threads can call engineCall() concurrently (editWorker,
        // emitSelect's detached thread, occasional direct UI-thread calls) -
        // serialize the actual pipe write so two callers' JSON lines can
        // never interleave into one garbled line the engine can't parse.
        std::lock_guard<std::mutex> wlk(g_engine.writeMx);
        if (!g_engine.hin || !WriteFile(g_engine.hin, line.c_str(), (DWORD)line.size(), &written, nullptr)) {
            return { {"ok",false}, {"error","write to engine failed"} };
        }
    }
    editLog("engineCall(" + verb + ") id=" + id + " wrote, waiting for reply");
    std::unique_lock<std::mutex> lk(g_engine.mx);
    auto deadline = std::chrono::steady_clock::now() + std::chrono::milliseconds((int64_t)(timeoutSec * 1000));
    while (!g_engine.seen[id]) {
        if (!g_engine.alive) break;
        if (g_engine.cv.wait_until(lk, deadline) == std::cv_status::timeout) break;
    }
    editLog("engineCall(" + verb + ") id=" + id + " wait done seen=" + (g_engine.seen[id] ? "1" : "0") + " alive=" + (g_engine.alive ? "1" : "0"));
    if (!g_engine.seen[id]) return { {"ok",false}, {"error","engine timeout / no reply"} };
    json r = g_engine.replies[id];
    g_engine.replies.erase(id); g_engine.seen.erase(id);
    return r;
}
static void engineShutdown() {
    if (g_engine.hin) { DWORD w = 0; WriteFile(g_engine.hin, "\n", 1, &w, nullptr); CloseHandle(g_engine.hin); g_engine.hin = nullptr; }
    if (g_engine.pi.hProcess) { WaitForSingleObject(g_engine.pi.hProcess, 1500); CloseHandle(g_engine.pi.hProcess); CloseHandle(g_engine.pi.hThread); }
}

// --------------- EDIT WORKER: split/delete/trim/undo routed off the UI thread (A-4) ---------------
// Same request/poll shape as the decode worker's P1 fix, but DRAIN-ALL, not
// coalesce-to-latest: a compose() request can safely drop stale positions
// (only the newest matters), but an edit must never be dropped - 20 rapid
// splits must land as 20 real edits (I-6). So completed edits queue up and
// the UI thread applies every one, in strict FIFO order, once per frame -
// never blocking the render loop while the engine round-trip is in flight.
struct EditReq {
    std::string verb;
    json args;
    int kind = 0;      // 0=split 1=remove 2=trimOut 3=trimIn 4=undo
    double t = 0;       // editT() at request time, for the local group-track mirror
    bool group = false;
    std::pair<double, double> rem{ 0, 0 };   // precomputed ripple (Del/O/I only)
    // Bug-2 fix (4AM verification: "edit ops dead on real clips"): a clip that
    // reached the timeline as a LOCAL PREVIEW (single-click search hit, cue click,
    // Space-played video, or add_clip's engine-failure fallback) has no engine id,
    // so split/trim aimed at it silently no-opped. With promote set, editWorker
    // first registers the span with the engine (add_clip) and patches the real id
    // into args before running the verb - the first edit on a preview clip
    // PROMOTES it to a real reel clip instead of dying silently.
    bool promote = false;
    std::string pSource; double pIn = 0, pOut = 0; std::string pLabel;
};
struct EditResult { EditReq req; bool ok = false; json data; };
static std::deque<EditReq> g_editQ;
static std::mutex g_editQMx; static std::condition_variable g_editQCv;
static std::deque<EditResult> g_editDone;
static std::mutex g_editDoneMx;
static bool g_editQuit = false;
// Clip ids with a split/remove/trim (kind 0/1/2/3) request already queued or
// in flight on editWorker. UI-thread-only (inserted on keypress, erased when
// the reply is drained), no extra lock needed. ROOT CAUSE FIX (found live
// this session, real engine-backed clip, not the demo fallback): the S/Del/O/I
// handlers read c->id from g_track[0] synchronously at keypress time, but
// g_track[0] is only refreshed once the matching reply lands. A rapid burst
// (real Jordan-speed multi-tap, or playback auto-repeat) queues N requests
// against the SAME pre-split id before the first reply updates the track; the
// engine accepts only the first (the id then no longer exists) and silently
// rejects the rest (ok:false) - the UI drain loop's `if (!res.ok) continue`
// swallows them with zero visible error. Net effect: 15 rapid S presses on a
// real clip produced exactly 1 real split, not 15 - I-6's literal contract
// line, previously "architecturally plausible but not end-to-end proven"
// (the demo fallback's clips all share id="" so this race was invisible
// there). Fix: don't let a second edit targeting the same still-resolving
// clip id be queued at all; once its reply lands (typically single-digit ms),
// the next press resolves against the fresh, engine-confirmed id.
static std::set<std::string> g_editsInFlight;
// One preview-clip promotion (add_clip + verb, see EditReq.promote) in flight at
// a time. UI-thread-only, like g_editsInFlight: set when a promote request is
// queued, cleared when its reply drains. Keeps key-spam on an unregistered clip
// from stacking N add_clips of the same span. ponytail: one global gate, not a
// per-span map - promotions are rare (first edit on a preview) and serialized
// through the FIFO editWorker anyway.
static bool g_promoteInFlight = false;

// Ground-truth edit trace, OPT-IN via BECKY_REVIEW_EDIT_LOG=<path> (unset =
// zero overhead, no file touched). Settles the still-open question from the
// prior session's COULD NOT DO: whether a rapid-burst S/Del/O/I keypress
// actually reaches this handler at all (a GetAsyncKeyState edge-detection
// question) vs. the request being correctly built but rejected/gated
// downstream (an edit-correctness question). A screenshot/undo-count can't
// tell those apart; this log can, independent of any synthetic-input or
// vision-API flakiness.
static std::ofstream g_editLog;
static std::mutex g_editLogMx;   // editWorker's thread and the UI thread both log
static void editLogInit() {
    if (const char* p = getenv("BECKY_REVIEW_EDIT_LOG")) g_editLog.open(p, std::ios::app);
}
static void editLog(const std::string& line) {
    if (!g_editLog.is_open()) return;
    std::lock_guard<std::mutex> lk(g_editLogMx);
    // fixed/setprecision(4): nowSec() is QPC seconds since process start, so
    // default 6-sig-fig double formatting silently loses sub-second resolution
    // once uptime passes ~10000s - exactly when you'd want to measure a
    // millisecond-scale round trip.
    g_editLog << std::fixed << std::setprecision(4) << nowSec() << " " << line << "\n"; g_editLog.flush();
}

// I-5 evidence trail, OPT-IN via BECKY_REVIEW_SCRUB_LOG=<path> (unset = zero
// overhead, no file touched). Logs every requestCompose() call (UI thread, one
// per frame whose curSec changed) and every composeOnDecodeThread() completion
// (decode thread, the actual engine seek) with wall-clock timestamps, so "a new
// frame per mouse event during scrub" is a grepped, correlated timestamp series
// - request cadence vs. decode-thread completion cadence - not a claim.
static std::ofstream g_scrubLog;
static std::mutex g_scrubLogMx;
static void scrubLogInit() {
    if (const char* p = getenv("BECKY_REVIEW_SCRUB_LOG")) g_scrubLog.open(p, std::ios::app);
}
static void scrubLog(const std::string& line) {
    if (!g_scrubLog.is_open()) return;
    std::lock_guard<std::mutex> lk(g_scrubLogMx);
    g_scrubLog << nowSec() << " " << line << "\n"; g_scrubLog.flush();
}

// I-9 evidence trail, OPT-IN via BECKY_REVIEW_FRAME_TRACE=<path> (unset = zero
// overhead, no file touched). Every prior cycle's I-9/I-7 claim was a spot-check
// or a log-timestamp inference; this is a per-frame wall-clock CSV so "no >100ms
// stall for 5 minutes" is a number anyone can grep, not a narrative.
static std::ofstream g_frameTrace;
static long g_frameTraceStalls = 0;
static void frameTraceInit() {
    if (const char* p = getenv("BECKY_REVIEW_FRAME_TRACE")) {
        g_frameTrace.open(p, std::ios::app);
        if (g_frameTrace.is_open()) g_frameTrace << "frame,tSec,deltaMs,stall\n";
    }
}
static void frameTraceTick(long frameIdx, double tSec, double deltaMs) {
    if (!g_frameTrace.is_open()) return;
    bool stall = deltaMs > 100.0;
    if (stall) g_frameTraceStalls++;
    g_frameTrace << frameIdx << "," << tSec << "," << deltaMs << "," << (stall ? 1 : 0) << "\n";
    if (stall || (frameIdx % 600) == 0) g_frameTrace.flush();
}

// STAGE TIMER (2026-07-23, hunting the "Not Responding" finding from the prior
// session's real-footage playback drive): frameTraceTick above only proves a
// frame was slow, not WHERE the main thread spent the time inside it - and the
// prior session's hang was never caught by a frame trace because
// BECKY_REVIEW_FRAME_TRACE was not set during that run. This is always-on (no
// env gate, like crashLog) but near-zero-cost when healthy: two QueryPerformance
// reads per checkpoint, and a crashLog line (already flush-per-call) ONLY when a
// span exceeds the threshold - so a normal 60fps session never writes a byte,
// but the exact main-thread span that blocks for seconds (or minutes) gets a
// name and a duration in crash.log. Marks are placed at the panel boundaries
// (menu bar / left library-search-transcript / center video / right Q&A /
// bottom timeline+waveforms / Render / Present) already used to lay out the
// frame, so a hit narrows the hang to one panel's code, not just "somewhere".
static void crashLog(const std::string& line);   // fwd decl - defined just below, needed by stageMark
static double g_stageT = 0;
static const char* g_stageName = "frame-top";
static void stageMark(const char* name) {
    double t = nowSec() * 1000.0;
    double d = t - g_stageT;
    if (d > 80.0) crashLog(std::string("STAGE SLOW [") + g_stageName + " -> " + name + "] " +
                            std::to_string(d) + "ms");
    g_stageT = t; g_stageName = name;
}

// Always-on crash diagnostic (no env gate - this is a safety net, not an opt-in
// trace). Root cause of the recurring "becky-review.exe has stopped working"
// (ucrtbase.dll, exception 0xC0000409) IS KNOWN from the undo-stack-underrun fix
// above: an uncaught C++ exception on ANY thread reaches std::terminate(), whose
// default handler calls abort(), and modern UCRT's abort() raises exactly that
// fastfail code - there is no memory corruption, just a missed try/catch. A
// std::terminate handler runs BEFORE abort() (fastfail bypasses SEH/VEH entirely,
// but terminate() is a normal function call), so this is the one place that can
// reliably capture what actually threw, on whichever thread it happened.
static std::ofstream g_crashLog;
static std::mutex g_crashLogMx;
static void crashLog(const std::string& line) {
    std::lock_guard<std::mutex> lk(g_crashLogMx);
    if (!g_crashLog.is_open()) return;
    g_crashLog << nowSec() << " [tid " << GetCurrentThreadId() << " " << t_threadTag << "] " << line << "\n";
    g_crashLog.flush();
}
// Bridge for engine.cpp (crashLog above is static by design).
void engineLog(const std::string& s) { crashLog(s); }
static void crashLogInit() {
    char exe[MAX_PATH] = { 0 }; GetModuleFileNameA(nullptr, exe, MAX_PATH);
    std::string p(exe); auto pos = p.find_last_of("\\/");
    p = (pos == std::string::npos ? std::string(".") : p.substr(0, pos)) + "\\crash.log";
    g_crashLog.open(p, std::ios::app);
    std::set_terminate([] {
        std::string msg = "terminate() with no active exception (likely noexcept violation or pure-virtual call)";
        if (auto ep = std::current_exception()) {
            try { std::rethrow_exception(ep); }
            catch (const std::exception& e) { msg = std::string("uncaught std::exception: ") + e.what(); }
            catch (...) { msg = "uncaught non-std exception"; }
        }
        crashLog(std::string("TERMINATE - ") + msg);
        std::abort();
    });
    // Bug-1 forensics: the recurring 0xc0000409 faults INSIDE ucrtbase!abort
    // (export-map-verified against fault offset 0x7286e), yet crash.log stayed
    // empty - so abort() is being reached WITHOUT std::terminate (GLib/GStreamer
    // g_error(), or a direct CRT abort). UCRT's abort() raises SIGABRT before it
    // fastfails, and ucrtbase.dll is one shared CRT for every module in the
    // process, so this hook is the one place that sees those too. Log the thread
    // tag + raw stack (module+offset, resolvable via becky-review.map) then let
    // the crash proceed - this is a flight recorder, not a rescue.
    signal(SIGABRT, [](int) {
        void* frames[32];
        USHORT n = CaptureStackBackTrace(0, 32, frames, nullptr);
        std::string s = "SIGABRT (abort called) - stack:";
        for (USHORT i = 0; i < n; i++) {
            HMODULE m = nullptr;
            GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                               (LPCWSTR)frames[i], &m);
            char name[MAX_PATH] = { 0 };
            const char* base = "?";
            if (m && GetModuleFileNameA(m, name, MAX_PATH)) {
                const char* p = strrchr(name, '\\');
                base = p ? p + 1 : name;
            }
            char fr[MAX_PATH + 32];
            snprintf(fr, sizeof fr, " %s+0x%llx", base,
                     (unsigned long long)((uintptr_t)frames[i] - (uintptr_t)m));
            s += fr;
        }
        crashLog(s);
    });
}

static void queueEdit(EditReq req) {
    std::lock_guard<std::mutex> lk(g_editQMx);
    g_editQ.push_back(std::move(req));
    g_editQCv.notify_one();
}
// Worker thread: pops one request at a time (FIFO) and blocks ONLY this
// thread on the engine round-trip - the UI thread is never touched. Requests
// are processed strictly in enqueue order, so Ctrl+Z after a burst of splits
// always undoes the correct (latest) one.
static void editWorker() {
    t_threadTag = "editWorker";
    for (;;) {
        EditReq req;
        {
            std::unique_lock<std::mutex> lk(g_editQMx);
            g_editQCv.wait(lk, [] { return g_editQuit || !g_editQ.empty(); });
            if (g_editQuit) return;
            req = std::move(g_editQ.front()); g_editQ.pop_front();
        }
        EditResult res;
        try {
            if (req.promote) {
                // Register the preview span as a real reel clip first, then aim the
                // queued verb at the id the engine hands back. add_clip APPENDS, so
                // the new clip is the reply's last one (ponytail: last-wins is safe
                // because this FIFO worker is the only promote path; a concurrent
                // double-click add racing this in the same instant just means the
                // verb lands on that identical span instead).
                json ar = engineCall("add_clip", { {"source", req.pSource}, {"in", req.pIn},
                                                   {"out", req.pOut}, {"label", req.pLabel} }, 6.0);
                std::string newId;
                if (ar.value("ok", false)) {
                    // I-2 wire-protocol fix (cycle 27): add_clip's reply now carries
                    // ONLY the new clip under "clip" (see becky-go bridge.go
                    // addClipReply), not the whole "clips" array - this call always
                    // appends (no "at" arg above), so the new clip's own id is right
                    // there, no need to scan a full clip list for the last entry.
                    json ad = ar.value("data", json::object());
                    if (ad.contains("clip") && ad["clip"].is_object())
                        newId = ad["clip"].value("id", std::string());
                }
                editLog("PROMOTE preview -> engine verb=" + req.verb +
                        " id=" + (newId.empty() ? std::string("(add_clip failed)") : newId));
                if (newId.empty()) {
                    res.ok = false;
                    res.req = std::move(req);
                    std::lock_guard<std::mutex> lk(g_editDoneMx);
                    g_editDone.push_back(std::move(res));
                    continue;
                }
                req.args["id"] = newId;
            }
            json r = engineCall(req.verb, req.args, 5.0);
            res.ok = r.value("ok", false); res.data = r.value("data", json::object());
            if (res.ok && req.verb == "undo") {
                // ROOT-CAUSED THIS SESSION (was the unsolved "undo-stack-underrun" artifact):
                // "undo" on an exhausted stack still replies ok=true, changed=false, carrying
                // the CURRENT (unchanged) engine timeline inline - it never needs the extra
                // "timeline" round-trip split/remove/trim need. The old code (and this code,
                // before this fix) blindly reloaded from it regardless of "changed", which
                // wipes the display to whatever the engine's reel actually is - empty, if the
                // UI is showing the client-only demo fallback (main() lines ~1737-1741, never
                // registered with the engine) rather than a real opened/edited reel. Only
                // apply the reload when the engine confirms something actually changed.
                //
                // CRASH ROOT-CAUSED THIS SESSION: this used to re-read the raw `r["data"]`
                // here (a SECOND, separate access from the `res.data` already safely built
                // above via r.value("data", json::object())). nlohmann's operator[] on an
                // object silently vivifies a null child for a missing key; .value() on that
                // null then THROWS json::type_error (306) instead of defaulting. An "undo"
                // reply that omits "data" - observed live, right at undo-stack exhaustion,
                // e.g. after 14 splits + 1 add, the 15th/16th Ctrl+Z - threw here, uncaught,
                // on this background thread: std::terminate -> abort -> the exact recurring
                // "becky-review.exe has stopped working" (ucrtbase.dll, 0xC0000409) seen in
                // the Windows Event Log across many prior sessions, never root-caused before
                // because it was always screenshot/undo-count verified, never log-instrumented.
                // Fix: reuse res.data (already object-typed, already defaulted) - never touch
                // raw `r` a second time.
                if (res.data.value("changed", false)) res.data["__timeline"] = res.data.value("timeline", json::object());
            } else if (res.ok) {
                json tv = engineCall("timeline", {}, 5.0);
                if (tv.value("ok", false)) res.data["__timeline"] = tv.value("data", json::object());
            }
        } catch (const std::exception& e) {
            // H-2/H-3 "degrade, never crash": any other unexpected engine reply shape
            // must never take the whole app down with it - log it and hand the UI thread
            // a clean ok=false (its existing `if (!res.ok) continue;` already degrades
            // gracefully) instead of letting the exception escape this thread.
            editLog(std::string("EXCEPTION in editWorker verb=") + req.verb + ": " + e.what());
            res = EditResult{}; res.ok = false;
        }
        editLog("editWorker post-try, about to push_back verb=" + req.verb);
        res.req = std::move(req);
        {
            std::lock_guard<std::mutex> lk(g_editDoneMx);
            g_editDone.push_back(std::move(res));
        }
        editLog("editWorker pushed_back, looping");
    }
}

// #0 CRITICAL: on this machine gst_init()/its plugin-registry scan can hard-crash the
// process (a native access violation, NOT a C++ exception - a plain try/catch cannot see
// it) when the official msvc_x86_64 GStreamer DLLs and an Anaconda/conda-forge shadow
// GStreamer install both land on PATH. SEH (__try/__except) is the only mechanism that can
// catch a hardware/structured exception. If init fails or crashes, g_gstAvailable stays
// false; every GStreamer call site below (peaksProcessBatch, decodeWorker/composeOnDecodeThread,
// the video pane draw, shutdown) checks it first, so the window still opens (shell, library,
// timeline, search all work) and the video pane shows a plain "video decode unavailable"
// note instead of the whole app dying before CreateWindow ever runs.
static std::atomic<bool> g_gstAvailable{ false };
// T-1: set true (once, from bootWork's background thread, as its LAST write) once the
// engine has started and the initial reel/folder load has finished. The render loop
// gates all of g_track/g_folderView/etc. on this so a background thread can populate
// them once, unlocked, with no reader until the flag flips - see main()'s "T-1 fix" comment.
static std::atomic<bool> g_bootDone{ false };
static int gstInitSEH(int argc, char** argv) {
    __try {
        gst_init(&argc, &argv);
        // FIX (cycle-4, root-caused via isolated repro): GStreamer/GLib lazily create
        // GLib's internal "pool-spawner" thread-pool manager on the FIRST pipeline state
        // change anywhere in the process. If that first-ever call happens on a thread
        // already in THREAD_MODE_BACKGROUND_BEGIN - which every bgPool worker thread
        // enters immediately - Windows rejects the manager thread's own SetThreadPriority
        // call and GLib treats that as fatal. Fix: force that one-time lazy init here, on
        // the main thread at normal priority, before any worker thread exists to race it.
        GError* warmErr = nullptr;
        GstElement* warm = gst_parse_launch("fakesrc num-buffers=1 ! fakesink", &warmErr);
        if (warm) {
            gst_element_set_state(warm, GST_STATE_PAUSED);
            gst_element_get_state(warm, nullptr, nullptr, 5 * GST_SECOND);
            gst_element_set_state(warm, GST_STATE_NULL);
            gst_object_unref(warm);
        }
        if (warmErr) g_error_free(warmErr);
        return 1;
    } __except (EXCEPTION_EXECUTE_HANDLER) {
        return 0;
    }
}

// --------------- D-1 (step 6 rewrite): native in-process video engine ---------------
// mpv is GONE: no subprocess, no child HWND, no named pipes, no EDL temp file.
// engine.cpp (SPEC-BECKY-VIDEO-ENGINE.md) decodes with libavcodec/D3D11VA on
// its OWN device, converts NV12->BGRA into a shared-texture ring, and the pane
// draws it as a plain ImGui::Image. Audio is WASAPI shared, audio-clock master.
// Scrub/seek/play are plain function calls - the whole IPC failure class
// (blocked pipe writes, seek floods, time-pos lag) no longer exists.
static std::atomic<bool> g_edlActive{ false };   // engine reel playback active
static double g_playRate = 1.0;

// Show one exact frame of one source, paused (replaces mpvSeekExact; the
// engine chases the newest request internally, so the exact flag is moot).
static void engineShowFrame(const std::string& source, double srcSec, bool /*exact*/) {
    if (!engine::available()) return;
    if (g_edlActive.load()) return; // while the reel plays, the engine owns position
    engine::showSource(source, srcSec);
}

// ===== I-8 / §3.4 P3: Bounded background worker pool =====
// All peaks decode (GStreamer audio -> .bpk min/max) runs through a fixed pool
// of N = max(1, physical_cores / 2) threads in Windows BACKGROUND processing
// mode. This prevents the FB9 failure mode: 100+ concurrent GStreamer decode
// threads (one per unique source file) from saturating disk I/O and stalling
// even the OS cursor during a cold folder load.
//
// Workers process pending source jobs from a shared FIFO. A source currently
// being processed is tracked so no two workers decode the same source
// simultaneously - the per-source Peaks.jobs/secFilled/inFlight dedup stays
// intact (only one thread touches a Peaks at a time).
//
// Also handles thumbnails (requestThumb) and external file probes
// (requestAddExternal) through the same pool, so those also benefit from the
// concurrency cap.

struct Peaks;   // forward decl — defined below in the waveform section
static std::shared_ptr<Peaks> peaksGet(const std::string& source);
static bool peaksProcessBatch(std::shared_ptr<Peaks> P);

class BgWorkPool {
    int N;
    std::vector<std::thread> workers;
    std::deque<std::string> pending;          // sources with pending peaks work
    std::set<std::string> pendingSet;         // O(log n) dedup
    std::set<std::string> active;             // sources being processed
    std::deque<std::function<void()>> extras; // one-shot jobs (thumbs/add_ext)
    std::mutex mx;
    std::condition_variable cv;
    bool stop = false;
public:
    BgWorkPool() {
        SYSTEM_INFO si; GetSystemInfo(&si);
        N = std::max(1, (int)si.dwNumberOfProcessors / 2);
        for (int i = 0; i < N; i++)
            workers.emplace_back([this]{ loop(); });
    }
    ~BgWorkPool() {
        { std::lock_guard lk(mx); stop = true; cv.notify_all(); }
        for (auto& w : workers) if (w.joinable()) w.join();
    }
    /// Queue a source's pending peaks jobs for processing. Dedup'd: if the
    /// source is already active or queued, this is a no-op.
    void wakeSource(const std::string& s) {
        if (s.empty()) return;
        std::lock_guard lk(mx);
        if (stop || active.count(s) || pendingSet.count(s)) return;
        pending.push_back(s);
        pendingSet.insert(s);
        cv.notify_one();
    }
    /// Submit a one-shot job (thumbnail, add_external, etc.).
    void submit(std::function<void()> f) {
        std::lock_guard lk(mx);
        extras.push_back(std::move(f));
        cv.notify_one();
    }
private:
    void loop() {
        t_threadTag = "bgPool";
        if (!SetThreadPriority(GetCurrentThread(), THREAD_MODE_BACKGROUND_BEGIN))
            SetThreadPriority(GetCurrentThread(), THREAD_PRIORITY_BELOW_NORMAL);
        for (;;) {
            std::function<void()> extra;
            bool haveExtra = false;
            std::string src;
            {
                std::unique_lock lk(mx);
                cv.wait(lk, [this]{ return stop || !extras.empty() || !pending.empty(); });
                if (stop) return;
                if (!extras.empty()) {
                    extra = std::move(extras.front()); extras.pop_front(); haveExtra = true;
                } else if (!pending.empty()) {
                    src = pending.front(); pending.pop_front(); pendingSet.erase(src);
                    active.insert(src);
                } else continue;
            }
            if (haveExtra) { extra(); continue; }
            auto P = peaksGet(src);
            bool redo = false;
            if (P) redo = peaksProcessBatch(P);  // returns true if jobs remain
            {
                std::lock_guard lk(mx);
                active.erase(src);
                if (redo && !stop && !pendingSet.count(src) && !active.count(src)) {
                    pending.push_back(src); pendingSet.insert(src); cv.notify_one();
                }
                if (!pending.empty()) cv.notify_one();
            }
        }
    }
};
static BgWorkPool* g_bgPool = nullptr;

// --------------- ACCURATE WAVEFORMS: windowed, seek-first min/max peak pyramid ---------------
static const int    kSpb = 64;
static const int    kPeakRate = 48000;
static const double kBinsPerSec = (double)kPeakRate / kSpb;
static std::atomic<int> g_fillEpoch{ 0 };

struct Peaks {
    std::mutex mx;
    std::vector<int8_t> n0, x0, n1, x1, n2, x2;
    std::vector<uint8_t> secFilled;
    size_t bins = 0;
    double duration = 0;
    // Some sources' AUDIO stream starts later than their VIDEO stream (ffprobe
    // start_time differs between the two) - a real, per-file mic/encoder lead-in,
    // not a becky-review bug. cin/cout/compStart everywhere else in this file are
    // VIDEO-frame time; decodeWindow must add this before seeking the audio
    // stream, or the waveform (and anything cut against it) reads as shifted by
    // that same constant. Zero for a normally-muxed file - a no-op.
    double avSkew = 0;
    bool ready = false;
    bool failed = false;
    bool dirty = false;
    std::deque<std::pair<double, double>> jobs;
    // I-6 dedup: the window currently popped off `jobs` and being decoded (not
    // yet in secFilled, no longer in the deque either) - see peaksRequest.
    std::pair<double, double> inFlight{ -1.0, -1.0 };
    std::condition_variable cv;
    double lastMissReq = 0;
    // cycle 19 real-corpus finding (E:\TakingBack2007, a partially-downloaded
    // livestream .mkv with a companion ".live_chat.json.part" - the known
    // capture-gap corpus issue, see memory livestream-capture-corruption): a
    // window whose audio is genuinely gapped/corrupt makes gst_element_seek's
    // pipeline never produce samples for it. decodeWindow returns (no error, no
    // crash) but fills NOTHING; drawWave's once-a-second "still missing" retry
    // (throttled by lastMissReq) then re-requests it forever - confirmed live
    // over 4+ minutes, filledSecs stuck at 0/N, job counter climbing at a slow
    // but truly UNBOUNDED steady rate. stuckAttempts counts consecutive popped
    // jobs that made zero fill progress; past kMaxStuckAttempts the source is
    // marked `failed` (peaksRequest/drawWave both already early-return on
    // `failed`), which stops the retries permanently instead of forever - the
    // same "degrade, never hang" contract as a real decode error.
    int stuckAttempts = 0;
    std::string source, cachePath;
};
static const int kMaxStuckAttempts = 8;
// cycle 22: bounds a single decodeWindow() attempt so a hung GStreamer pipeline
// (confirmed reproducible on a real, otherwise-clean corpus file - see
// decodeWindow's comment) can't block a bgPool worker thread forever. Worst
// case before giving up on a window entirely: kMaxStuckAttempts * this.
static const double kDecodeWindowTimeoutSec = 15.0;
static std::map<std::string, std::shared_ptr<Peaks>> g_peaks;
static std::mutex g_peaksMx;
static std::mutex g_decMx; static std::condition_variable g_decCv; static int g_decActive = 0;
static std::atomic<bool> g_busyHint{ false };

// ---- "something is happening" ----
//
// Jordan asked for "a non-intrusive, semi-transparent loading bar for any
// operation taking more than one second" (feedback5). Before this, a transcribe
// (up to 900s), a render, a qmd search or an edit round-trip gave NO visible
// sign the app was working - it simply looked frozen. He is vision-impaired and
// describes the previous build as having "FROZEN when i tried touching it"; an
// app that is busy and an app that is dead must not look the same.
//
// ONE SECOND is the threshold on purpose: showing an indicator for a 100ms
// operation is flicker, which is worse than nothing. beginWork/endWork are
// counted, not boolean, so overlapping operations (a search while a transcribe
// runs) do not clear each other's indicator.
static std::mutex g_workMx;
static std::string g_workLabel;
static int g_workCount = 0;
static double g_workSince = 0;

static void beginWork(const std::string& label) {
    std::lock_guard<std::mutex> lk(g_workMx);
    if (g_workCount == 0) g_workSince = nowSec();
    g_workCount++;
    g_workLabel = label;   // newest wins: it is the thing he just asked for
}
static void endWork() {
    std::lock_guard<std::mutex> lk(g_workMx);
    if (g_workCount > 0) g_workCount--;
    if (g_workCount == 0) g_workLabel.clear();
}

// BUTTONS MUST NEVER MOVE. Jordan stated this twice (feedback7, item 6/110):
// "Timeline toolbar buttons must never shift position". A button whose LABEL
// changes - Play/Pause, the 3-state Overlay, Skip Quiet On/Off - is a different
// width in each state, so every button to its right jumps sideways on each
// click. He aims by muscle memory at speed; a control that moves under the
// cursor is a control he mis-clicks.
//
// ---- REAL ICONS instead of words (Segoe MDL2 Assets) ----
//
// Jordan: "i'm bad at reading and THATS WHY we use meaningful icons for
// buttons when they can replace text". He is sighted but vision-impaired and
// reading costs him physical effort, so every word removed from a control he
// already recognises is real effort saved.
//
// The rule cuts BOTH ways, and the second half matters as much as the first:
// a control gets an icon ONLY if the glyph is self-evident on its own. A glyph
// he has to decode is WORSE than the word. So Play/Pause/skip-to-start/
// camera/floppy/folder/running-man go icon-only; Render, Render Selection (N),
// the 3-state Overlay, 2x, the +-1f nudges and Export EDL keep their words -
// no glyph conveys "three states" or "EDL", and the (N) count must stay
// readable.
//
// Written as \x escapes, never literal UTF-8: non-ASCII bytes in this file
// break the MSVC build (codepage assumption). Each escape ends at its closing
// quote, so the "##id" suffix is concatenated as a separate literal and can
// never be swallowed into the hex escape.
#define ICON_PLAY   "\xEE\x9D\xA8"   // U+E768 Play
#define ICON_PAUSE  "\xEE\x9D\xA9"   // U+E769 Pause
#define ICON_START  "\xEE\xA2\x92"   // U+E892 Previous / back to start
#define ICON_RUN    "\xF0\x9F\x8F\x83"   // U+1F3C3 running man - COLOR emoji (Segoe UI Emoji), matches becky-review-native
#define ICON_CAMERA "\xF0\x9F\x93\xB7"   // U+1F4F7 camera  - COLOR emoji
#define ICON_SAVE   "\xEE\x9D\x8E"   // U+E74E Floppy disk
#define ICON_OPEN   "\xEE\xB4\xA5"   // U+ED25 Open folder
// U+E7A7 curls LEFT (undo), U+E7A6 curls RIGHT (redo) - verified by rendering
// both from C:\Windows\Fonts\segmdl2.ttf at 96px and looking at them, against
// the canonical U+E10E/U+E10D pair, which they match exactly. Getting these two
// backwards would be worse than shipping no icon at all.
#define ICON_UNDO   "\xE2\x86\xBA"   // U+21BA anticlockwise circular arrow - matches becky-review-native
#define ICON_REDO   "\xE2\x86\xBB"   // U+21BB clockwise circular arrow
// Round 3 additions (BR3-ROUND3-VISUAL-WORKORDER items 2/9): a Split button
// (the reference toolbar's scissors) and glyphs for the 3-state Overlay toggle
// (item 9 - a glyph, not the words "Overlay: On (hidden)"). Byte sequences
// computed from the codepoint (Python chr(cp).encode('utf-8')), same method
// that would have caught undo/redo backwards above.
#define ICON_SCISSORS "\xE2\x9C\x82"  // U+2702 scissors - COLOR emoji (Segoe UI Emoji), matches becky-review-native
#define ICON_BROOM    "\xF0\x9F\xA7\xB9" // U+1F9F9 broom - COLOR emoji (Trim silence)
#define ICON_CHECK    "\xEE\x9C\xBE"  // U+E73E CheckMark - overlay mode 2 (on, shown)
#define ICON_CANCEL   "\xEE\x9C\x91"  // U+E711 Cancel (X) - overlay mode 0 (off)
#define ICON_EYE      "\xEE\xA2\x90"  // U+E890 View (eye) - overlay mode 1 (on, hidden)
#define ICON_BACK     "\xEE\x9C\xAB"  // U+E72B Back (left arrow) - item 11, left-panel back button

// False until segmdl2.ttf is actually loaded. EVERY icon button below routes
// its label through ico(), so if the font is missing the whole toolbar falls
// back to the old text labels and still runs. A missing glyph renders as a
// HOLLOW SQUARE - the documented "square play button" failure in this project -
// and this flag is why that can never ship.
static bool g_iconsOk = false;
static const char* ico(const char* iconLabel, const char* textLabel) {
    return g_iconsOk ? iconLabel : textLabel;
}

// ---- reference button HOVER (CSS .btn:hover { border-color:neon; color:neon }) ----
// ImGui recolors only the FILL on hover, never the text/border. becky-review-native
// turns a plain white button's TEXT and BORDER neon on hover (and the fill stays put).
// Predict the hover from the button's own rect and push those colours for the frame.
// A button that had a colour PUSHED (a blue extend button, a green toggle-on) is left
// alone - it keeps whatever hover its own push defines - so this only ever fires on the
// default #0A0A0A chip, exactly like the CSS.
static const ImVec4 kNeon = ImVec4(0.224f, 1.0f, 0.078f, 1.0f);

// A toolbar/panel button drawn MANUALLY so its hover is STABLE. The old version pushed
// hover colours from a per-frame prediction (IsWindowHovered + hit-test) that FLICKERED
// the instant a tooltip window appeared and stole the hover - that flicker was the
// "seizure" flashing Jordan saw. IsItemHovered() on our own InvisibleButton is rock
// steady. We read the CURRENT Button/Text/Border style, so a caller that PUSHED a colour
// is respected: a plain #0A0A0A chip gets the reference's neon text+border on hover; a
// pushed-blue chip (the frame/extend buttons) inverts to white text + blue glow; a green
// toggle-on chip keeps its fill and just gets a soft glow. Colour-emoji glyphs ignore the
// text colour and stay in colour either way.
static bool refBtnCore(const char* label, float w) {
    const ImVec4 baseBg = ImGui::GetStyleColorVec4(ImGuiCol_Button);
    const ImVec4 baseTx = ImGui::GetStyleColorVec4(ImGuiCol_Text);
    const ImVec4 baseBd = ImGui::GetStyleColorVec4(ImGuiCol_Border);
    // A "white" chip = the default #0A0A0A dark fill (white/neon text). A "blue" chip is
    // now identified by its BLUE TEXT on a dark fill (get-captions, frame [<|/|>]) - the
    // reference draws those as blue TEXT on dark, NOT a solid blue FILL (that fill is the
    // look Jordan banned, item 22). Detect blue by the pushed text colour (#00AEEF -> z
    // high, x low) so the fill can stay dark.
    const bool isWhite = baseBg.x < 0.06f && baseBg.y < 0.06f && baseBg.z < 0.06f;
    const bool isBlue  = baseTx.z > 0.6f && baseTx.x < 0.3f && baseBg.z < 0.30f;
    ImVec2 p = ImGui::GetCursorScreenPos();
    float h = ImGui::GetFrameHeight();
    bool clicked = ImGui::InvisibleButton(label, ImVec2(w, h));
    bool hov = ImGui::IsItemHovered(), act = ImGui::IsItemActive();
    ImVec2 mn = ImGui::GetItemRectMin(), mx = ImGui::GetItemRectMax();
    ImVec4 bg = baseBg, tx = baseTx, bd = baseBd;
    const ImVec4 kBlue = ImVec4(0.0f, 0.68f, 0.94f, 1.0f);           // #00AEEF
    if (isBlue) bd = ImVec4(0.078f, 0.314f, 0.416f, 1.0f);           // #14506A muted-blue border at rest (.tbtn2.extend)
    ImU32 glow = 0;
    if (hov) {
        // Jordan (items 17/18): EVERY button gets the same subtle WHITE outer glow on
        // hover (matches becky-review-native's rgba(255,255,255,.35) button hover). A
        // blue-text chip turns its text WHITE + border blue; a white chip turns its
        // text+border neon (.btn:hover); a green-on chip just glows. Glow is white in
        // every case, never green/blue. Check blue FIRST - a blue chip's fill is also
        // dark, so it would otherwise fall into the isWhite branch.
        const ImU32 whiteGlow = IM_COL32(255, 255, 255, 90);
        if (isBlue)      { tx = ImVec4(1, 1, 1, 1); bd = kBlue; glow = whiteGlow; }
        else if (isWhite){ tx = kNeon; bd = kNeon; glow = whiteGlow; }
        else             { glow = whiteGlow; }
    }
    if (act && (isWhite || isBlue)) bg = ImVec4(0.08f, 0.08f, 0.09f, 1.0f);
    ImDrawList* dl = ImGui::GetWindowDrawList();
    float r = ImGui::GetStyle().FrameRounding;
    dl->AddRectFilled(mn, mx, ImGui::ColorConvertFloat4ToU32(bg), r);
    if (glow) dl->AddRect(ImVec2(mn.x - 1, mn.y - 1), ImVec2(mx.x + 1, mx.y + 1), glow, r, 0, 3.0f);
    dl->AddRect(mn, mx, ImGui::ColorConvertFloat4ToU32(bd), r, 0, 1.0f);
    const char* end = label; while (*end && !(end[0] == '#' && end[1] == '#')) end++;
    ImVec2 ts = ImGui::CalcTextSize(label, end);
    dl->AddText(ImVec2(mn.x + (w - ts.x) * 0.5f, mn.y + (h - ts.y) * 0.5f),
                ImGui::ColorConvertFloat4ToU32(tx), label, end);
    return clicked;
}
// refBtn: a plain (auto-sized) button with the reference hover.
static bool refBtn(const char* label) {
    float w = ImGui::CalcTextSize(label, nullptr, true).x + ImGui::GetStyle().FramePadding.x * 2.0f;
    return refBtnCore(label, w);
}

// Reference input focus: a text box shows the NEON border (+ soft glow) while it has the
// cursor, so it is obvious which box you are typing into (.searchbar:focus-within /
// #ask:focus / .cuesearch:focus). Call right after the InputText.
static void inputFocusBorder() {
    if (!ImGui::IsItemActive()) return;
    ImVec2 mn = ImGui::GetItemRectMin(), mx = ImGui::GetItemRectMax();
    ImDrawList* dl = ImGui::GetWindowDrawList();
    dl->AddRect(ImVec2(mn.x - 1, mn.y - 1), ImVec2(mx.x + 1, mx.y + 1), IM_COL32(0x39, 0xFF, 0x14, 70), 4.0f, 0, 3.0f);
    dl->AddRect(ImVec2(mn.x - 1, mn.y - 1), ImVec2(mx.x + 1, mx.y + 1), IM_COL32(0x39, 0xFF, 0x14, 255), 4.0f, 0, 1.5f);
}

// fixedButton sizes to the WIDEST label the control can ever show, so its
// footprint is constant whatever state it is in. Pass every variant. Carries the
// same reference white->neon hover as refBtn.
static bool fixedButton(const char* label, std::initializer_list<const char*> allStates) {
    float w = 0;
    for (const char* s : allStates) w = (std::max)(w, ImGui::CalcTextSize(s).x);
    w += ImGui::GetStyle().FramePadding.x * 2.0f;
    return refBtnCore(label, w);
}

// ---- run an engine verb WITHOUT freezing the window ----
//
// THIS IS THE FREEZE. Jordan: "the becky-review-native app FROZE when i tried
// touching it (cuz i'm too fast - i wasn't even trying; literally my muscle
// memory broke the entire goddamn thing)."
//
// Render, Save Reel, Load Reel, Export EDL, Screenshot, ask and apply_proposal
// all called engineCall() straight from the button handler - i.e. on the UI
// thread, inside the frame - with timeouts from 10 up to 300 SECONDS. For that
// entire span the message pump never runs: no repaint, no input, Windows greys
// the title bar and offers to kill it. The app is not slow during a render, it
// is DEAD, and there is no way for him to tell that apart from a crash.
//
// The app already had the right shape for this everywhere else (transcribe,
// thumbnails and edits all hand off to a worker and drain per frame). These
// call sites simply never got converted. engineCallAsync is that shape, once,
// so no future call site has to reinvent it: the verb runs on its own thread,
// the work indicator shows automatically, and the completion callback is
// delivered on the UI THREAD by drainAsync() - so callbacks can touch UI state
// (g_renderMsg, the timeline, curSec) exactly as the old inline code did.
struct AsyncReply {
    json r;
    std::function<void(const json&)> cb;
};
static std::mutex g_asyncMx;
static std::deque<AsyncReply> g_asyncQ;

static void engineCallAsync(const std::string& verb, json args, double timeoutSec,
                            const std::string& label, std::function<void(const json&)> cb) {
    beginWork(label);
    std::thread([verb, args, timeoutSec, cb]() {
        t_threadTag = "asyncVerb";
        json r;
        try {
            r = engineCall(verb, args, timeoutSec);
        } catch (...) {
            r = json{ {"ok", false}, {"error", "the engine call failed"} };
        }
        endWork();
        std::lock_guard<std::mutex> lk(g_asyncMx);
        g_asyncQ.push_back(AsyncReply{ r, cb });
    }).detach();
}

// Delivers finished async verbs on the UI thread. Called once per frame.
static void drainAsync() {
    std::deque<AsyncReply> q;
    { std::lock_guard<std::mutex> lk(g_asyncMx); q.swap(g_asyncQ); }
    for (auto& a : q) {
        if (!a.cb) continue;
        try { a.cb(a.r); } catch (...) {}   // a bad callback must not kill the frame
    }
}
// E-18/I-6 instrumentation: counts every job actually PUSHED onto a Peaks.jobs
// deque (peaksRequest below) - not decode work, the enqueue itself. BUILD_1.md's
// verification bar for I-6 is literally "split 20x rapidly, assert 0 jobs
// enqueued"; this is the counter that assertion reads (see peaksRequest's
// already-filled short-circuit, which is what keeps it at 0 once a source's
// audio is warm).
static std::atomic<uint64_t> g_peaksJobsEnqueued{ 0 };
// E-18 thumbnail half (cycle 15 review's "ONE THING"): mirrors g_peaksJobsEnqueued
// but for the thumb worker - counts every "thumb" engine call actually submitted,
// not decode work, the submit itself. See requestThumb below for why this stays
// flat across split (the cache key dropped clip in-point entirely).
static std::atomic<uint64_t> g_thumbJobsEnqueued{ 0 };
// cycle 19 diagnostic: mirrors g_track[0].size() (declared later in this file) so
// peaksRequest can log it without a forward-declaration of Clip/g_track. Updated
// once per loadTimelineView call, right after the real track is rebuilt.
static std::atomic<size_t> g_trackClipCountForLog{ 0 };

static uint64_t fnv1a64(const std::string& s) {
    uint64_t h = 1469598103934665603ULL;
    for (unsigned char c : s) { h ^= c; h *= 1099511628211ULL; }
    return h;
}
static std::wstring utf8ToWide(const std::string& s) {
    int n = MultiByteToWideChar(CP_UTF8, 0, s.c_str(), -1, nullptr, 0);
    std::wstring w(n > 0 ? n - 1 : 0, L'\0');
    if (n > 0) MultiByteToWideChar(CP_UTF8, 0, s.c_str(), -1, &w[0], n);
    return w;
}
static bool fileMeta(const std::string& path, uint64_t& size, uint64_t& mtime) {
    WIN32_FILE_ATTRIBUTE_DATA fad;
    if (!GetFileAttributesExW(utf8ToWide(path).c_str(), GetFileExInfoStandard, &fad)) return false;
    size = ((uint64_t)fad.nFileSizeHigh << 32) | fad.nFileSizeLow;
    mtime = ((uint64_t)fad.ftLastWriteTime.dwHighDateTime << 32) | fad.ftLastWriteTime.dwLowDateTime;
    return true;
}
static std::string peaksCachePath(const std::string& source) {
    uint64_t size = 0, mtime = 0; fileMeta(source, size, mtime);
    char key[64]; snprintf(key, sizeof key, "|%llu|%llu", (unsigned long long)size, (unsigned long long)mtime);
    uint64_t h = fnv1a64(source + key);
    const char* base = getenv("LOCALAPPDATA");
    std::string dir = std::string(base ? base : ".") + "\\becky";
    CreateDirectoryA(dir.c_str(), nullptr);
    dir += "\\peaks"; CreateDirectoryA(dir.c_str(), nullptr);
    char fn[64]; snprintf(fn, sizeof fn, "\\%016llx.bpk", (unsigned long long)h);
    return dir + fn;
}
static void sizeArrays(Peaks& P, double duration) {
    P.duration = duration;
    P.bins = (size_t)(duration * kBinsPerSec) + 2;
    P.n0.assign(P.bins, 127); P.x0.assign(P.bins, -128);
    P.n1.assign(P.bins / 16 + 1, 127); P.x1.assign(P.bins / 16 + 1, -128);
    P.n2.assign(P.bins / 256 + 1, 127); P.x2.assign(P.bins / 256 + 1, -128);
    P.secFilled.assign((size_t)duration + 2, 0);
    P.ready = true;
}
static void pyramidRegion(Peaks& P, size_t a, size_t b) {
    if (b > P.bins) b = P.bins;
    if (b <= a) return;
    for (size_t i = a >> 4; i <= ((b - 1) >> 4) && i < P.n1.size(); i++) {
        int8_t mn = 127, mx = -128;
        size_t s0 = i << 4, s1 = std::min(P.bins, s0 + 16);
        for (size_t k = s0; k < s1; k++) { mn = std::min(mn, P.n0[k]); mx = std::max(mx, P.x0[k]); }
        P.n1[i] = mn; P.x1[i] = mx;
    }
    for (size_t i = a >> 8; i <= ((b - 1) >> 8) && i < P.n2.size(); i++) {
        int8_t mn = 127, mx = -128;
        size_t s0 = i << 4, s1 = std::min(P.n1.size(), s0 + 16);
        for (size_t k = s0; k < s1; k++) { mn = std::min(mn, P.n1[k]); mx = std::max(mx, P.x1[k]); }
        P.n2[i] = mn; P.x2[i] = mx;
    }
}
static bool loadPeaksCache(Peaks& P) {
    FILE* f = nullptr; fopen_s(&f, P.cachePath.c_str(), "rb");
    if (!f) return false;
    char magic[4]; uint32_t spb = 0, rate = 0; uint64_t count = 0; double dur = 0;
    bool ok = fread(magic, 1, 4, f) == 4
        && fread(&spb, 4, 1, f) == 1 && spb == (uint32_t)kSpb
        && fread(&rate, 4, 1, f) == 1 && rate == (uint32_t)kPeakRate
        && fread(&count, 8, 1, f) == 1 && count < (1ULL << 32)
        && fread(&dur, 8, 1, f) == 1 && dur > 0;
    // cycle 23: BPK3 only. BPK1/BPK2 caches were written while the old GStreamer
    // decoder was hanging on real corpus files, and the give-up path stamped the
    // hung ranges secFilled=1 with silent amplitudes - PERMANENT fake silence.
    // Rejecting the old magic makes every poisoned cache rebuild through the
    // ffmpeg decoder below; layout is unchanged from BPK2, only the magic moved.
    bool v3 = ok && memcmp(magic, "BPK3", 4) == 0;
    if (ok && v3) {
        std::lock_guard<std::mutex> lk(P.mx);
        sizeArrays(P, dur);
        {
            uint32_t secN = 0;
            ok = fread(&secN, 4, 1, f) == 1 && secN <= P.secFilled.size();
            if (ok && secN) ok = fread(P.secFilled.data(), 1, secN, f) == secN;
        }
        if (ok) {
            std::vector<int8_t> buf((size_t)count * 2);
            ok = count == 0 || fread(buf.data(), 1, buf.size(), f) == buf.size();
            if (ok) {
                size_t n = std::min((size_t)count, P.bins);
                for (size_t i = 0; i < n; i++) { P.n0[i] = buf[i * 2]; P.x0[i] = buf[i * 2 + 1]; }
                pyramidRegion(P, 0, n);
            }
        }
        if (!ok) { P.ready = false; P.bins = 0; }
    } else ok = false;
    fclose(f);
    return ok && P.ready;
}
static void savePeaksCache(Peaks& P) {
    FILE* f = nullptr; fopen_s(&f, P.cachePath.c_str(), "wb");
    if (!f) return;
    fwrite("BPK3", 1, 4, f);
    uint32_t spb = kSpb, rate = kPeakRate; uint64_t count = P.bins; double dur = P.duration;
    uint32_t secN = (uint32_t)P.secFilled.size();
    fwrite(&spb, 4, 1, f); fwrite(&rate, 4, 1, f); fwrite(&count, 8, 1, f); fwrite(&dur, 8, 1, f);
    fwrite(&secN, 4, 1, f);
    fwrite(P.secFilled.data(), 1, secN, f);
    std::vector<int8_t> buf(P.bins * 2);
    for (size_t i = 0; i < P.bins; i++) { buf[i * 2] = P.n0[i]; buf[i * 2 + 1] = P.x0[i]; }
    fwrite(buf.data(), 1, buf.size(), f);
    fclose(f);
    P.dirty = false;
}
// cycle 23 (Jordan: "we no longer have waveforms" - his top blocker): the peaks
// decoder is now FFMPEG, not GStreamer. cycle 22 proved with gst-launch-1.0 that
// this app's exact "uridecodebin ! ... ! appsink" pipeline HANGS after PLAYING on
// real E:\TakingBack2007 files (90+s, zero buffers, zero EOS, zero bus error)
// while ffmpeg decodes the same audio track end to end in ~12s with zero
// warnings. cycle 22 only BOUNDED the hang (watchdogs); a bounded hang still
// fills nothing, and its give-up path stamped the hung ranges "filled" into the
// .bpk cache as permanent fake silence. Root fix: decode with the decoder that
// demonstrably works on this corpus. ffmpeg writes s16le mono PCM at kPeakRate
// to an anonymous pipe; we min/max it into the same bins/pyramid as before.
//
// runPipeCapture: spawn cmd, stream its stdout into onData until EOF or
// deadlineSec (then the child is terminated - "degrade, never hang"). Polling
// with PeekNamedPipe instead of a blocking ReadFile means no watchdog thread is
// needed: the deadline check and the read live in the same loop.
static bool runPipeCapture(const std::string& cmd8, double deadlineSec,
                           const std::function<void(const uint8_t*, size_t)>& onData) {
    SECURITY_ATTRIBUTES sa{ sizeof(sa), nullptr, TRUE };
    HANDLE rd = nullptr, wr = nullptr;
    if (!CreatePipe(&rd, &wr, &sa, 1 << 20)) return false;
    SetHandleInformation(rd, HANDLE_FLAG_INHERIT, 0);

    // D-7/E-2 root-cause fix (found live 2026-07-23 against real audio-bearing
    // corpus files: probeAudioDuration deterministically reported "NO AUDIO
    // TRACK" for files proven by a standalone ffprobe run - same binary, same
    // command line - to have a real AAC stream). bInheritHandles=TRUE below
    // inherits EVERY currently-inheritable handle in the process, not just
    // `wr` - with peaks/decodeWindow spawning ffmpeg/ffprobe from concurrent
    // worker threads, each child could leak in another in-flight call's own
    // pipe handle. PROC_THREAD_ATTRIBUTE_HANDLE_LIST restricts inheritance to
    // exactly {wr}, the documented fix for this class of Windows handle leak.
    SIZE_T attrSize = 0;
    InitializeProcThreadAttributeList(nullptr, 1, 0, &attrSize);
    std::vector<uint8_t> attrBuf(attrSize);
    auto* attrList = (LPPROC_THREAD_ATTRIBUTE_LIST)attrBuf.data();
    bool haveAttrList = attrSize > 0 && InitializeProcThreadAttributeList(attrList, 1, 0, &attrSize) != 0;
    HANDLE inheritList[1] = { wr };
    if (haveAttrList) {
        haveAttrList = UpdateProcThreadAttribute(attrList, 0, PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
            inheritList, sizeof(inheritList), nullptr, nullptr) != 0;
    }

    // cb MUST be sizeof(STARTUPINFOEXW) (the OUTER struct), not sizeof(StartupInfo) -
    // empirically proven 500/500 CreateProcessW failures (ERROR_INVALID_PARAMETER)
    // with the inner size vs 0/500 with the outer size, once EXTENDED_STARTUPINFO_PRESENT
    // is in play. This was the actual root cause of every ffprobe/ffmpeg pipe call
    // failing (E-2 waveforms, D-7 audio-track probing) - not a PATH problem.
    STARTUPINFOEXW six{}; six.StartupInfo.cb = sizeof(six);
    six.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
    six.StartupInfo.hStdOutput = wr;           // stderr/stdin stay null: -v error is quiet,
    if (haveAttrList) six.lpAttributeList = attrList;
    PROCESS_INFORMATION pi{};                 // and PCM must never be interleaved with text
    std::wstring cmd = utf8ToWide(cmd8);
    DWORD flags = CREATE_NO_WINDOW | (haveAttrList ? EXTENDED_STARTUPINFO_PRESENT : 0);
    BOOL ok = CreateProcessW(nullptr, &cmd[0], nullptr, nullptr, TRUE, flags, nullptr, nullptr,
                             &six.StartupInfo, &pi);
    if (haveAttrList) DeleteProcThreadAttributeList(attrList);
    CloseHandle(wr);                          // ours only - the child holds its inherited copy
    if (!ok) {
        crashLog("runPipeCapture: CreateProcessW failed, GetLastError=" + std::to_string(GetLastError()) +
                  " haveAttrList=" + std::to_string(haveAttrList) + " cmd=" + cmd8);
        CloseHandle(rd); return false;
    }
    const double t0 = nowSec();
    static thread_local std::vector<uint8_t> buf(1 << 16);
    for (;;) {
        DWORD avail = 0;
        if (!PeekNamedPipe(rd, nullptr, 0, nullptr, &avail, nullptr)) break;   // pipe closed = EOF
        if (avail == 0) {
            if (WaitForSingleObject(pi.hProcess, 20) == WAIT_OBJECT_0) {
                // child exited - drain the last bytes still sitting in the pipe
                while (PeekNamedPipe(rd, nullptr, 0, nullptr, &avail, nullptr) && avail) {
                    DWORD got = 0;
                    if (!ReadFile(rd, buf.data(), (DWORD)std::min<size_t>(avail, buf.size()), &got, nullptr) || !got) break;
                    onData(buf.data(), got);
                }
                break;
            }
            if (nowSec() - t0 > deadlineSec) { TerminateProcess(pi.hProcess, 1); break; }
            continue;
        }
        DWORD got = 0;
        if (!ReadFile(rd, buf.data(), (DWORD)std::min<size_t>(avail, buf.size()), &got, nullptr) || !got) break;
        onData(buf.data(), got);
        if (nowSec() - t0 > deadlineSec) { TerminateProcess(pi.hProcess, 1); break; }
    }
    CloseHandle(rd);
    WaitForSingleObject(pi.hProcess, 2000);
    CloseHandle(pi.hProcess); CloseHandle(pi.hThread);
    return true;
}
// One ffprobe per cold source: does it have an audio track, how long is it, and
// - the reason this now reads EVERY stream instead of just "a:0" - does its
// audio stream start at the same real time as its video stream? A source whose
// mic activated ~0.2s after its camera (common on phone-captured clips; verified
// on this corpus with `ffprobe -show_entries stream=codec_type,start_time`,
// video start_time=0.000000, audio start_time=0.230000) has NO audio samples
// before that gap - not a decode bug, just missing data - and every waveform
// pixel after it reads as shifted by that same constant unless decodeWindow
// compensates. avSkew is that gap (audio start_time minus video start_time),
// zero for a normally-muxed file.
static bool probeAudioDuration(const std::string& source, double& dur, bool& hasAudio, double& avSkew) {
    std::string cmd = "ffprobe -v error -show_entries stream=codec_type,start_time:format=duration "
        "-of default=noprint_wrappers=1 \"" + source + "\"";
    std::string out;
    if (!runPipeCapture(cmd, 20.0, [&](const uint8_t* d, size_t n) { out.append((const char*)d, n); }))
        return false;
    hasAudio = false; dur = 0; avSkew = 0;
    double videoStart = 0, audioStart = -1;
    bool haveVideoStart = false, haveAudioStart = false;
    std::string curType;
    size_t pos = 0;
    while (pos < out.size()) {
        size_t nl = out.find('\n', pos);
        std::string line = out.substr(pos, nl == std::string::npos ? std::string::npos : nl - pos);
        pos = (nl == std::string::npos) ? out.size() : nl + 1;
        // D-7 fix (cycle-14, found live this session): ffprobe's piped stdout on
        // Windows is CRLF-terminated even with the console showing plain "\n" -
        // splitting on '\n' alone left every line ending in a trailing '\r', so
        // curType ("audio\r") never equalled "audio" and hasAudio was FALSE for
        // every file ever probed (confirmed: 0 successful avSkew lines in this
        // project's entire crash.log history, 31/31 probes failed). Every source
        // in the corpus showed "no audio/no waveform" regardless of whether it
        // actually had an audio track.
        if (!line.empty() && line.back() == '\r') line.pop_back();
        if (line.rfind("codec_type=", 0) == 0) { curType = line.substr(11); if (curType == "audio") hasAudio = true; }
        else if (line.rfind("start_time=", 0) == 0) {
            double t = atof(line.c_str() + 11);
            // first stream of each type only - matches the old "-select_streams a:0"
            if (curType == "video" && !haveVideoStart) { videoStart = t; haveVideoStart = true; }
            else if (curType == "audio" && !haveAudioStart) { audioStart = t; haveAudioStart = true; }
        } else if (line.rfind("duration=", 0) == 0) dur = atof(line.c_str() + 9);
    }
    if (hasAudio && audioStart >= 0) avSkew = audioStart - videoStart;
    return true;
}
// Decodes [a,b) of P.source into the peak bins via ffmpeg. Returns the highest
// second actually reached by real sample data (clamped to b), NOT b itself -
// the caller uses this, not the request bounds, to decide what to mark filled.
// A window bigger than the deadline can decode simply stops at the deadline;
// the covered part is kept and drawWave's still-missing retry resumes from there.
static double decodeWindow(Peaks& P, double a, double b) {
    if (b <= a) return a;
    char head[128], tail[192];
    // Seek the AUDIO stream at a + avSkew, not a: `a`/`b` are VIDEO-frame time
    // (same clock as every cin/cout/compStart elsewhere in this file), and on a
    // source whose audio starts later than its video (see avSkew's comment on
    // Peaks) the audio content that plays in sync with video time `a` sits at
    // that later position in the file, not at `a` itself.
    snprintf(head, sizeof head, "ffmpeg -v error -nostdin -ss %.3f -i ", std::max(0.0, a + P.avSkew));
    snprintf(tail, sizeof tail, " -t %.3f -map a:0 -vn -sn -dn -ac 1 -ar %d -f s16le pipe:1", b - a, kPeakRate);
    const std::string cmd = std::string(head) + "\"" + P.source + "\"" + tail;
    // Bins stay indexed by VIDEO time `a` - only the disk-seek position moves.
    const uint64_t sampleBase = (uint64_t)(a * kPeakRate);
    uint64_t nsTotal = 0;         // samples consumed so far (position = sampleBase + nsTotal)
    uint8_t carry = 0; bool haveCarry = false;   // an s16 sample split across two reads
    bool started = runPipeCapture(cmd, kDecodeWindowTimeoutSec, [&](const uint8_t* d, size_t n) {
        std::lock_guard<std::mutex> lk(P.mx);
        size_t firstBin = (size_t)((sampleBase + nsTotal) / kSpb), lastBin = firstBin;
        size_t i = 0;
        while (i < n) {
            int16_t s;
            if (haveCarry) { s = (int16_t)(carry | (d[i] << 8)); haveCarry = false; i += 1; }
            else if (i + 1 < n) { s = (int16_t)(d[i] | (d[i + 1] << 8)); i += 2; }
            else { carry = d[i]; haveCarry = true; break; }
            size_t bin = (size_t)((sampleBase + nsTotal) / kSpb);
            nsTotal++;
            if (bin >= P.bins) continue;
            int8_t q = (int8_t)(s >> 8);
            if (q < P.n0[bin]) P.n0[bin] = q;
            if (q > P.x0[bin]) P.x0[bin] = q;
            lastBin = bin;
        }
        pyramidRegion(P, firstBin, lastBin + 1);
    });
    if (!started) {
        crashLog("peaks: " + baseName(P.source) + " - could not start ffmpeg (not on PATH?), window [" +
            std::to_string(a) + "," + std::to_string(b) + "] skipped");
        return a;
    }
    double maxCoveredSec = a + (double)nsTotal / kPeakRate;
    if (maxCoveredSec > b) maxCoveredSec = b;
    {
        std::lock_guard<std::mutex> lk(P.mx);
        for (size_t s = (size_t)std::ceil(a); s + 1 <= (size_t)std::floor(maxCoveredSec) && s < P.secFilled.size(); s++)
            P.secFilled[s] = 1;
        P.dirty = true;
    }
    g_fillEpoch.fetch_add(1);
    return maxCoveredSec;
}
static bool peaksProcessBatch(std::shared_ptr<Peaks> P) {
    t_threadTag = "peaksBatch";
    // I-8: called from BgWorkPool (which already set BACKGROUND priority).
    // Batch-drains all currently-queued jobs instead of looping forever.
    // Returns true if any jobs remain (caller should re-signal the pool).
    if (!P || P->failed) return false;
    if (loadPeaksCache(*P)) g_fillEpoch.fetch_add(1);
    if (!P->ready) {
        // cycle 23: one ffprobe replaces the old gst preroll + duration query,
        // both of which could hang on this corpus (see decodeWindow's comment).
        double dur = 0, avSkew = 0; bool hasAudio = false;
        if (!probeAudioDuration(P->source, dur, hasAudio, avSkew)) {
            crashLog("peaks: " + baseName(P->source) + " - ffprobe could not be started (not on PATH?), waveform disabled");
            P->failed = true; return false;
        }
        if (!hasAudio) {
            crashLog("peaks: " + baseName(P->source) + " - source has NO AUDIO TRACK (e.g. a silent screen capture), waveform disabled");
            P->failed = true; return false;
        }
        if (dur <= 0) {
            crashLog("peaks: " + baseName(P->source) + " - audio duration probe failed, waveform disabled");
            P->failed = true; return false;
        }
        if (std::abs(avSkew) > 0.001)
            crashLog("peaks: " + baseName(P->source) + " - audio stream starts " + std::to_string(avSkew) +
                      "s after video (ffprobe start_time) - compensating every waveform seek");
        std::lock_guard<std::mutex> lk(P->mx);
        P->avSkew = avSkew;
        sizeArrays(*P, dur);
    }
    g_fillEpoch.fetch_add(1);
    // Drain all pending jobs (no per-source infinite loop)
    for (;;) {
        std::pair<double, double> job;
        {
            std::unique_lock<std::mutex> lk(P->mx);
            if (P->jobs.empty() || P->failed) break;
            job = P->jobs.front(); P->jobs.pop_front();
            P->inFlight = job;
        }
        double a = std::max(0.0, job.first), b = std::min(P->duration, job.second);
        std::vector<std::pair<double, double>> runs;
        {
            std::lock_guard<std::mutex> lk(P->mx);
            double runA = -1;
            for (size_t s = (size_t)a; s <= (size_t)b && s < P->secFilled.size(); s++) {
                bool filled = P->secFilled[s] != 0;
                if (!filled && runA < 0) runA = std::max(a, (double)s);
                if ((filled || s == (size_t)b) && runA >= 0) { runs.push_back({ runA, std::min(b, (double)s + 1) }); runA = -1; }
            }
            if (runA >= 0) runs.push_back({ runA, b });
        }
        bool progressed = false;
        for (auto& r : runs) {
            if (r.second - r.first < 0.01) continue;
            {
                std::unique_lock<std::mutex> g(g_decMx);
                g_decCv.wait(g, [] { return g_decActive < (g_busyHint.load() ? 1 : 2); });
                g_decActive++;
            }
            double covered = r.first;
            try {
                covered = decodeWindow(*P, r.first, r.second);
            } catch (const std::exception& e) {
                crashLog(std::string("peaksBatch decodeWindow: caught ") + e.what() + " - window skipped, not crashing");
            } catch (...) {
                crashLog("peaksBatch decodeWindow: caught non-std exception - window skipped, not crashing");
            }
            if (covered > r.first + 0.25) progressed = true;
            {
                std::lock_guard<std::mutex> g(g_decMx);
                g_decActive--;
            }
            g_decCv.notify_one();
        }
        {
            std::unique_lock<std::mutex> lk(P->mx);
            P->inFlight = { -1.0, -1.0 };
            size_t s0 = (size_t)std::ceil(a), s1 = std::min(P->secFilled.size(), (size_t)std::floor(b));
            bool nowFilled = true;
            for (size_t s = s0; s < s1; s++) if (!P->secFilled[s]) { nowFilled = false; break; }
            // cycle 23: PROGRESS also resets the stuck counter. A multi-hour
            // window fills ~15s of decode per attempt (the per-window deadline),
            // so "not fully filled yet" is normal for many consecutive attempts;
            // only ZERO forward progress means the decoder is actually stuck on
            // this range. Without this, a long window that was filling perfectly
            // well got stamped silent after kMaxStuckAttempts partial fills.
            if (nowFilled || progressed) P->stuckAttempts = 0;
            else if (++P->stuckAttempts >= kMaxStuckAttempts) {
                // Mark just the stuck range filled (silent/placeholder - the
                // amplitude arrays already default to that) so it stops being
                // retried forever, but leave the source usable: whatever DID
                // decode stays visible, and any other window for this source
                // can still be requested normally.
                for (size_t s = s0; s < s1; s++) P->secFilled[s] = 1;
                P->dirty = true;
                P->stuckAttempts = 0;
                lk.unlock();
                crashLog("peaksBatch: giving up on " + baseName(P->source) + " - window [" +
                    std::to_string(a) + "," + std::to_string(b) + "] made zero decode progress after " +
                    std::to_string(kMaxStuckAttempts) + " attempts - marking it silent and moving on");
                std::lock_guard<std::mutex> lk2(P->mx);
                if (P->dirty) savePeaksCache(*P);
                return !P->jobs.empty();
            }
        }
    }
    // Save cache now that we're done draining (pool will wake us if more arrive)
    {
        std::lock_guard<std::mutex> lk(P->mx);
        if (P->dirty) savePeaksCache(*P);
    }
    // Return true if any jobs still remain for this source (pool will re-signal)
    { std::lock_guard<std::mutex> lk(P->mx); return !P->jobs.empty(); }
}
static std::shared_ptr<Peaks> peaksGet(const std::string& source) {
    std::lock_guard<std::mutex> lk(g_peaksMx);
    auto it = g_peaks.find(source);
    return it == g_peaks.end() ? nullptr : it->second;
}
static std::shared_ptr<Peaks> peaksEnsure(const std::string& source) {
    if (source.empty()) return nullptr;
    std::lock_guard<std::mutex> lk(g_peaksMx);
    auto it = g_peaks.find(source);
    if (it != g_peaks.end()) return it->second;
    auto P = std::make_shared<Peaks>();
    P->source = source;
    P->cachePath = peaksCachePath(source);
    g_peaks[source] = P;
    // I-8: no per-source thread. peaksRequest will wake the pool when the first
    // job is pushed, and the pool's bounded workers drain the job queue.
    return P;
}
// True if every second in [a,b) is already decoded (P.secFilled) - a pure cache
// hit, nothing left for peaksProcessBatch to do. Caller must hold P.mx.
//
// Uses ceil(a)/floor(b), matching decodeWindow's OWN fill-marking promise
// (it only marks the INTERIOR whole seconds of a decoded run, never the
// fractional boundary seconds - see decodeWindow above). A floor/floor check
// (what this used to do) checks a boundary second decodeWindow can never mark,
// so it always reported "not filled" - live-tested this session: every single
// split re-enqueued exactly 1 job even on a fully-warm clip, because
// peaksRequest's own -1s/+5s padding is essentially never second-aligned. A
// window entirely inside one fractional second (no interior whole second to
// check) is never trackable either way, so it's conservatively "not filled" -
// re-checking a sub-second window is cheap; wrongly calling it cached is not.
static bool peaksWindowFilled(const Peaks& P, double a, double b) {
    if (!P.ready) return false;   // duration/secFilled not sized yet - unknown, not "filled"
    double aa = std::max(0.0, a), bb = std::min((double)P.duration, b);
    if (bb <= aa) return true;    // degenerate/empty window
    size_t s0 = (size_t)std::ceil(aa), s1 = (size_t)std::floor(bb);
    if (s1 <= s0) return false;   // sub-second window - no interior second to confirm, always re-check
    if (s1 > P.secFilled.size()) s1 = P.secFilled.size();
    for (size_t s = s0; s < s1; s++) if (!P.secFilled[s]) return false;
    return true;
}
static void peaksRequest(const std::string& source, double a, double b) {
    auto P = peaksEnsure(source);
    if (!P || P->failed) return;
    std::lock_guard<std::mutex> lk(P->mx);
    double aa = std::max(0.0, a);
    // E-18/I-6 (BUILD_1.md SS3.4 P5): loadTimelineView re-requests peaks for EVERY
    // clip on the track on EVERY edit reply (split/trim/delete/undo all reload the
    // whole timeline) - without this short-circuit, splitting a clip 20x rapidly
    // pushes a fresh job per clip per reload even though the audio was decoded once
    // and is sitting in secFilled/the .bpk cache. A window that's already fully
    // decoded is a pure cache hit: enqueue NOTHING (not even a cheap no-op job) -
    // this is the literal "assert 0 jobs enqueued" the I-6 verification bar asks for.
    if (peaksWindowFilled(*P, aa, b)) return;
    // I-6 measured regression (this session, real corpus, real numbers): the
    // "already decoded" short-circuit above only covers COMPLETED windows -
    // it says nothing about windows already sitting in P->jobs waiting for the
    // worker. Splitting a clip re-triggers loadTimelineView -> a fresh
    // peaksRequest for every clip on the track (see the comment above); each
    // split's two children request a window that is a SUBSET of the window
    // already requested when the clip was first added (splitting only ever
    // carves an EXISTING clip's span into smaller pieces, never extends it).
    // Before this check, every reload re-pushed a brand-new job for every
    // still-decoding source, even though an as-good-or-wider job for that
    // exact source was already queued: live-measured on E:\TakingBack2007
    // with 6 freshly-added sources, a burst of 20 rapid splits pushed the
    // counter from 232 to 530 jobs (not the flat "0 enqueued once warm" the
    // I-6 verification bar requires). Skip the push if any pending job for
    // this source already covers [aa,b] - it will get decoded when that
    // job's turn comes, same result, no duplicate work. Also check `inFlight`:
    // a job already popped off `jobs` and mid-decode is in neither `jobs` nor
    // `secFilled` - without this second check the counter kept climbing even
    // after the `jobs`-only dedup above (measured: still +6..+17 per reload),
    // because decodeWindow can take real wall-clock time and rapid splits
    // land reloads faster than that.
    for (auto& j : P->jobs) if (j.first <= aa && j.second >= b) return;
    if (P->inFlight.first <= aa && P->inFlight.second >= b) return;
    P->jobs.push_front({ aa, b });
    g_peaksJobsEnqueued.fetch_add(1, std::memory_order_relaxed);
    g_bgPool->wakeSource(source);
    // cycle 19 diagnostic (review's suggested next step): log how many seconds of
    // [aa,b] were ALREADY filled at push time and how many total clips are on the
    // track right now. If pushes correlate with trackClips growing while
    // filledSecs stays near 0 for a source whose OWN full window was requested
    // long ago, that's the "still-cold-source" race, not a dedup logic bug - the
    // fix is "wait for warm before splitting", not another dedup layer.
    size_t filledSecs = 0, totalSecs = 0;
    { size_t s0 = (size_t)std::ceil(aa), s1 = std::min(P->secFilled.size(), (size_t)std::floor(b));
      if (s1 > s0) { totalSecs = s1 - s0; for (size_t s = s0; s < s1; s++) if (P->secFilled[s]) filledSecs++; } }
    editLog("PEAKS PUSH src=" + baseName(source) + " aa=" + std::to_string(aa) + " b=" + std::to_string(b) +
        " ready=" + (P->ready ? "1" : "0") + " dur=" + std::to_string(P->duration) +
        " jobsLeft=" + std::to_string(P->jobs.size()) + " secFilledSz=" + std::to_string(P->secFilled.size()) +
        " filledSecs=" + std::to_string(filledSecs) + "/" + std::to_string(totalSecs) +
        " trackClips=" + std::to_string(g_trackClipCountForLog.load(std::memory_order_relaxed)));
}

// --------------- the clip track ---------------
struct Clip {
    double in, out, compStart;
    std::string label, source, id;
    uint8_t r = 0, g = 174, b = 239;
    bool ready = true;
    // D-6: provenance fields carried straight from the engine's ClipView JSON
    // (becky-go/cmd/clip/app.go) - the same Meta the render's burned-in lower
    // third uses (internal/reel/drawtext.go), so the preview overlay can show
    // IDENTICAL text without a second source of truth.
    std::string date, person, location, link;
    // The EDIT's own frame rate (ClipView.source_fps), carried from the Vegas/FCP7
    // import - 30000/1001 for Jordan's NTSC footage, not 30. 0 = the reel did not
    // carry one, in which case reelFps() falls back to the async ffprobe.
    double srcFps = 0;
};
static std::vector<Clip> g_track[2];
static double g_compDur = 0;
static bool g_group = true;

// I-2 fix (cycle 26, root cause of the review's flagged 16ms->130-172ms add_clip
// growth past ~5,000 clips): loadTimelineView reloads on EVERY single-clip edit
// (add_clip/split/trim/label/undo/redo all return a fresh, full TimelineView) and
// used to call peaksRequest for EVERY clip on the whole track on every single
// reload - each individual call is cheap once warm (peaksWindowFilled's E-18/I-6
// short-circuit above), but paying peaksEnsure's global-mutex lookup + that check
// N times per edit, when an add_clip only ever changes ONE clip, is exactly the
// O(n)-redone-per-edit cost the review measured. Cache the (source,in,out) last
// requested per clip id; a reload skips the call entirely for any clip whose
// window is unchanged since last time, so adding clip #5001 pays for one real
// peaksRequest, not 5001. Keyed by id (not source+window alone) so a clip cannot
// be mistaken for an unrelated one that happens to reuse the same window.
static std::map<std::string, std::pair<std::string, std::pair<double, double>>> g_peaksRequestedFor;
static void requestPeaksIfChanged(const Clip& c) {
    auto it = g_peaksRequestedFor.find(c.id);
    if (it != g_peaksRequestedFor.end() && it->second.first == c.source &&
        it->second.second.first == c.in && it->second.second.second == c.out) return;
    peaksRequest(c.source, c.in - 1.0, c.out + 5.0);
    g_peaksRequestedFor[c.id] = { c.source, { c.in, c.out } };
}
// G-1 "Play tied clips" preview: g_track[0] doubles as both "the loaded edit reel"
// and "whatever is currently being previewed" (the same pattern seekToSpan already
// uses for search-hit auditions). Unlike a seekToSpan clip, a Q&A card's tied clips
// are REAL clips copied straight out of the loaded reel with their REAL engine ids -
// so while a tied-clip preview is showing, a drag-reorder gesture would compute its
// "to" index against the visible SUBSET and send that position to the engine,
// silently reshuffling the real reel. Snapshot the real reel before swapping in the
// preview and restore it the instant playback stops; loadTimelineView (the function
// that refreshes g_track[0] from an authoritative engine reply) always wins over a
// stale preview restore, so it clears this flag too.
static std::vector<Clip> g_reelBeforePreview;
static bool g_inTiedPreview = false;
// Item 1 fix (round 3): where the REAL playhead sat the instant a preview began -
// so the timeline widget can be drawn FROZEN there (real reel, real duration, real
// playhead) for the entire preview, instead of visibly showing the swapped one-clip
// (or tied-clip) audition and only snapping back once playback stops. Captured once,
// on the false->true edge of g_inTiedPreview, by every place that starts a preview.
static double g_previewFrozenPlayhead = 0.0;

// D-6: provenance overlay state, mirroring the engine's edl.Overlay (app.go
// newReel defaults: everything on, position "bottom") so the native preview
// and the render burn-in agree on which fields are enabled. Parsed fresh from
// every loadTimelineView reply's "overlay" object - the engine is the one
// source of truth, this is just a read-only mirror for preview rendering.
struct OverlayState {
    bool enabled = true, showFilename = true, showTimecode = true, showDate = true;
    bool showLink = true, showPerson = true, showLocation = true;
    std::string position = "bottom";
};
static OverlayState g_overlay;
// 3-state PREVIEW toggle (BUILD_1.md D-6): 0=off (no overlay anywhere), 1=on
// but hidden in the live preview (DEFAULT - render still burns it in), 2=on
// and shown in the live preview too. This is purely a native/preview concept;
// "enabled" on the engine's Overlay always tracks state!=0 so render is never
// out of sync with "off".
static int g_ovMode = 1;
static std::atomic<bool> g_ovEngineEnabled{ true }; // last "enabled" value pushed to the engine
// Item 7 (round 2): "there should be a way to toggle OFF the captions, they
// should be optional." Default ON (captions are the useful default); gates
// BOTH the timeline caption lane and the preview's burned-in-style overlay -
// off means fully hidden, not just dimmed.
static bool g_capsOn = true;

static void relabel(int tr) {
    const char* p = tr == 0 ? "clip " : "pip ";
    for (size_t i = 0; i < g_track[tr].size(); i++)
        if (g_track[tr][i].label.empty() || g_track[tr][i].label.compare(0, 4, tr == 0 ? "clip" : "pip ") == 0)
            g_track[tr][i].label = p + std::to_string(i + 1);
}
static void packTrack(int tr) {
    double cs = 0;
    for (auto& c : g_track[tr]) { c.compStart = cs; cs += c.out - c.in; }
}
// Finds the clip on track tr covering compilation time t (nullptr if none). Shared by
// the S/Del/O/I edit handlers below, which need the clip's engine id + source time
// (splitTrack/deleteTrack only mutate the LOCAL track, which track 0 must no longer do).
static Clip* clipAtComp(int tr, double t) {
    for (auto& c : g_track[tr]) {
        double d = c.out - c.in;
        if (t >= c.compStart && t < c.compStart + d) return &c;
    }
    return nullptr;
}

// D-2: per-source frame rate cache, keyed by source path, so frame-exact stepping
// uses the CLIP's actual fps instead of a hardcoded 30 (a 25fps source stepped at
// 1/30s drifts 1.2 frames per press - not frame-exact). Populated lazily off the
// UI thread (P1: never block a keypress on an engine round trip) via the probe
// verb, which now also returns fps (ProbeResult.Fps, becky-go/cmd/clip/app.go).
// Falls back to 30.0 until the async probe lands, matching playWholeVideo's own
// probe-then-fallback shape for duration.
static std::map<std::string, double> g_fpsBySource;
static std::set<std::string> g_fpsInFlight;
static std::mutex g_fpsMx;
static double sourceFps(const std::string& source) {
    if (source.empty()) return 30.0;
    std::lock_guard<std::mutex> lk(g_fpsMx);
    auto it = g_fpsBySource.find(source);
    if (it != g_fpsBySource.end()) return it->second;
    if (!g_fpsInFlight.count(source)) {
        g_fpsInFlight.insert(source);
        std::thread([source] {
            json r = engineCall("probe", { {"source", source} }, 8.0);
            double fps = 30.0;
            if (r.value("ok", false)) {
                const json& d = r.contains("data") ? r["data"] : r;
                double p = d.value("fps", 0.0);
                if (p > 1.0) fps = p;
            }
            std::lock_guard<std::mutex> lk2(g_fpsMx);
            g_fpsBySource[source] = fps;
        }).detach();
    }
    return 30.0;
}

// 2026-06-30(2): "we need an 'index' icon for results which have not yet been
// indexed, similar to our 'plus' button which transcribes" - a plain-keyword
// hit's source can have a transcript that never got converted to a qmd
// search locator (internal/qmdindex's "_md" folder), so smart search will
// never surface it until it is. Cached per source, same lazy-background-probe
// shape as sourceFps just above - never stats the whole corpus per frame, and
// a source is only ever asked about once until an index click marks it done.
enum class IndexState { Unknown, Indexed, NotIndexed };
static std::map<std::string, IndexState> g_indexBySource;
static std::set<std::string> g_indexInFlight;
static std::mutex g_indexMx;
static IndexState sourceIndexState(const std::string& source) {
    if (source.empty()) return IndexState::Indexed; // nothing playable to flag
    std::lock_guard<std::mutex> lk(g_indexMx);
    auto it = g_indexBySource.find(source);
    if (it != g_indexBySource.end()) return it->second;
    if (!g_indexInFlight.count(source)) {
        g_indexInFlight.insert(source);
        std::thread([source] {
            json r = engineCall("index_status", { {"source", source} }, 8.0);
            IndexState st = IndexState::Indexed; // unknown/error - never flash a false alarm
            if (r.value("ok", false)) {
                const json& d = r.contains("data") ? r["data"] : r;
                st = d.value("indexed", true) ? IndexState::Indexed : IndexState::NotIndexed;
            }
            std::lock_guard<std::mutex> lk2(g_indexMx);
            g_indexBySource[source] = st;
        }).detach();
    }
    return IndexState::Unknown;
}
// Click-to-fix half of the icon: runs the same qmdindex convert the
// transcribe pipeline already calls, then marks the source Indexed locally
// so the icon clears immediately instead of waiting on a re-check round trip.
static void requestIndexSource(const std::string& source) {
    engineCallAsync("index_source", { {"source", source} }, 30.0, "Indexing for search...",
        [source](const json& r) {
            if (r.value("ok", false)) {
                std::lock_guard<std::mutex> lk(g_indexMx);
                g_indexBySource[source] = IndexState::Indexed;
            }
        });
}

// --------------- D-6: provenance overlay (Date+UTC / ORIG TC / filename) ---------------
// Builds the SAME lines the engine's render burns in (becky-go/internal/reel/
// drawtext.go lowerThirdFilter: Date, ORIG TC, filename|person|location, Link -
// in that exact order) from the currently-loaded clip's fields, so switching the
// preview to "shown" (g_ovMode==2) can never disagree with what Render produces
// (BUILD_1.md D-6: "preview and render show IDENTICAL text or preview shows
// none"). ASS special characters are escaped so a filename/person/location value
// can never inject an override tag into the overlay.
static std::string assEscape(const std::string& s) {
    std::string out; out.reserve(s.size());
    for (char c : s) {
        if (c == '\\' || c == '{' || c == '}') out += '\\';
        out += c;
    }
    return out;
}
// HH:MM:SS:FF non-drop timecode at fps - same rounding as edl.SecondsToTimecode
// (Go side) so the burned-in and previewed timecodes are the identical string.
static std::string secondsToTimecode(double sec, double fps) {
    if (fps <= 0) fps = 30.0;
    if (sec < 0) sec = 0;
    long long ifps = (long long)std::llround(fps);
    if (ifps <= 0) ifps = 30;
    long long totalFrames = (long long)std::llround(sec * (double)ifps);
    long long frames = totalFrames % ifps;
    long long totalSecs = totalFrames / ifps;
    long long secs = totalSecs % 60;
    long long totalMins = totalSecs / 60;
    long long mins = totalMins % 60;
    long long hours = totalMins / 60;
    char buf[32];
    snprintf(buf, sizeof buf, "%02lld:%02lld:%02lld:%02lld", hours, mins, secs, frames);
    return buf;
}
// Returns the overlay lines (top -> bottom) for clip c, honoring g_overlay's
// per-field toggles - empty when nothing is enabled/has content, mirroring
// metaLine/overlayDate/overlayLink in drawtext.go.
static std::vector<std::string> overlayLines(const Clip& c) {
    std::vector<std::string> lines;
    if (g_overlay.showDate && !c.date.empty())
        lines.push_back("Date: " + c.date + " UTC");
    if (g_overlay.showTimecode)
        lines.push_back("ORIG TC " + secondsToTimecode(c.in, sourceFps(c.source)));
    {
        std::vector<std::string> fields;
        if (g_overlay.showFilename) {
            std::string name = baseName(c.source);
            if (!name.empty()) fields.push_back(name);
        }
        if (g_overlay.showPerson && !c.person.empty()) fields.push_back(c.person);
        if (g_overlay.showLocation && !c.location.empty()) fields.push_back(c.location);
        if (!fields.empty()) {
            std::string joined;
            for (size_t i = 0; i < fields.size(); i++) { if (i) joined += " | "; joined += fields[i]; }
            lines.push_back(joined);
        }
    }
    if (g_overlay.showLink && !c.link.empty()) lines.push_back(c.link);
    return lines;
}
// Step 6: the engine's frame is a plain ImGui::Image now, so the provenance
// overlay is drawn straight onto the pane by ImGui - the OSD/ASS round trip
// through mpv no longer exists. Outline = 4 offset shadow draws (cheap, crisp).
static void imguiOutlinedText(ImDrawList* dl, ImVec2 pos, float fontSize,
                              const char* text) {
    ImFont* font = ImGui::GetFont();
    const ImU32 black = IM_COL32(0, 0, 0, 255), white = IM_COL32(255, 255, 255, 255);
    for (int dy = -1; dy <= 1; dy++)
        for (int dx = -1; dx <= 1; dx++)
            if (dx || dy)
                dl->AddText(font, fontSize, ImVec2(pos.x + dx, pos.y + dy), black, text);
    dl->AddText(font, fontSize, pos, white, text);
}
// Consolas, loaded in the font block; the overlay's monospace face so the forensic
// timecode digits don't jitter (item 25). Null until loaded -> falls back to the UI font.
static ImFont* g_overlayFont = nullptr;
// Item 25 - the forensic lower-third must be IDENTICAL to becky-review-native / the burned
// render (becky-go/internal/reel/drawtext.go). That spec, authored at the SOURCE frame
// resolution: Consolas monospace, WHITE text, 42px per line (45px for the ORIG TC line), a
// per-line semi-transparent BLACK BOX (boxcolor=black@0.6) behind the text, lower-LEFT,
// left margin 20 / bottom pad 61 (bottom edge -> top of the lowest line) / line step 58.
// All of it is scaled to the DISPLAYED video by `scale` = the caller's fit scale
// (sc = displayedHeight / sourceHeight), so it tracks the frame exactly like the reference.
static void drawOverlayImGui(const Clip* cur, ImVec2 origin, ImVec2 size, float scale) {
    if (g_ovMode != 2 || !cur) return;
    std::vector<std::string> lines = overlayLines(*cur);
    if (lines.empty()) return;
    if (scale <= 0.0f) scale = 1.0f;
    ImDrawList* dl = ImGui::GetWindowDrawList();
    ImFont* font = g_overlayFont ? g_overlayFont : ImGui::GetFont();
    const float fsBody = 42.0f * scale, fsTC = 45.0f * scale;
    const float marginX = 20.0f * scale, bottomPad = 61.0f * scale, lineStep = 58.0f * scale;
    const bool top = (g_overlay.position == "top");
    const int n = (int)lines.size();
    const ImU32 black = IM_COL32(0, 0, 0, 255), white = IM_COL32(255, 255, 255, 255);
    for (int i = 0; i < n; i++) {
        const std::string& ln = lines[i];
        const float fs = (ln.rfind("ORIG TC", 0) == 0) ? fsTC : fsBody;   // TC line is the larger anchor
        const float x = origin.x + marginX;
        const float y = top ? origin.y + 20.0f * scale + (float)i * lineStep
                            : origin.y + size.y - (bottomPad + (float)(n - 1 - i) * lineStep);
        const ImVec2 ts = font->CalcTextSizeA(fs, FLT_MAX, 0.0f, ln.c_str());
        const float bpx = fs * 0.22f, bpy = fs * 0.10f;                    // snug per-line box padding
        dl->AddRectFilled(ImVec2(x - bpx, y - bpy), ImVec2(x + ts.x + bpx, y + ts.y + bpy),
                          IM_COL32(0, 0, 0, 153));                          // black @ 0.6 (boxcolor=black@0.6)
        for (int dy = -1; dy <= 1; dy++)                                    // thin 1px outline (the C# preview's bord1)
            for (int dx = -1; dx <= 1; dx++)
                if (dx || dy) dl->AddText(font, fs, ImVec2(x + dx, y + dy), black, ln.c_str());
        dl->AddText(font, fs, ImVec2(x, y), white, ln.c_str());
    }
}
// Cycles/sets the 3-state preview toggle and keeps the engine's Overlay.Enabled
// in sync: "off" must disable the render burn-in too (D-6's third state means no
// overlay ANYWHERE), while both "on" states keep it enabled so Render always
// matches whichever text the preview would show in "shown" mode. set_overlay is
// a plain in-memory field flip on the engine side (no ffmpeg/IO), so this is
// called synchronously like the other button-triggered engine verbs in this file.
static void setOverlayMode(int m) {
    g_ovMode = m;
    bool wantEnabled = (m != 0);
    // P1: never block the UI thread on this engine round trip. set_overlay is
    // normally instant (an in-memory struct-field flip), but the engine's bridge
    // dispatches one verb at a time - if a slow verb (e.g. transcribe, which can
    // run for minutes) is already in flight, this call would otherwise queue
    // behind it and stall the click that fired it (caught live this session: a
    // synchronous version of this call took ~2.9s under exactly that contention).
    // Fire-and-forget on its own thread, same shape as sourceFps/requestTranscribe.
    if (wantEnabled != g_ovEngineEnabled.load()) {
        std::thread([wantEnabled] {
            json r = engineCall("set_overlay", { {"field", "enabled"}, {"value", wantEnabled} }, 20.0);
            if (r.value("ok", false)) g_ovEngineEnabled.store(wantEnabled);
        }).detach();
    }
}

static void splitTrack(int tr, double t) {
    for (size_t i = 0; i < g_track[tr].size(); i++) {
        Clip& c = g_track[tr][i]; double d = c.out - c.in;
        if (t > c.compStart + 0.05 && t < c.compStart + d - 0.05) {
            double srcT = c.in + (t - c.compStart);
            Clip right = c;
            right.in = srcT; right.compStart = t; right.label.clear(); right.id.clear();
            c.out = srcT;
            g_track[tr].insert(g_track[tr].begin() + i + 1, right); relabel(tr); return;
        }
    }
}
// Ripple-deletes the clip covering t on track tr. Returns {removedCompStart, removedDur}
// (removedDur==0 if nothing was deleted) so callers can compensate curSec on track 0 -
// E-7: a ripple delete/trim must never silently shift what's already playing.
static std::pair<double, double> deleteTrack(int tr, double t) {
    for (size_t i = 0; i < g_track[tr].size(); i++) {
        Clip& c = g_track[tr][i]; double d = c.out - c.in;
        if (t >= c.compStart && t < c.compStart + d) {
            double cs = c.compStart;
            g_track[tr].erase(g_track[tr].begin() + i);
            for (size_t j = i; j < g_track[tr].size(); j++) g_track[tr][j].compStart -= d;
            relabel(tr); return { cs, d };
        }
    }
    return { 0, 0 };
}
// Applies deleteTrack's ripple to curSec: if the removed region started at or before
// curSec, curSec shifts left by the removed duration (never past the removal point) so
// playback stays pinned to the same underlying footage instead of jumping (B7/E-7).
static void rippleCurSec(double& curSec, const std::pair<double, double>& rem) {
    if (rem.second > 0 && rem.first <= curSec) curSec = std::max(rem.first, curSec - rem.second);
}
static void recomputeDur() {
    g_compDur = 0;
    for (int tr = 0; tr < 2; tr++)
        if (!g_track[tr].empty()) {
            auto& c = g_track[tr].back();
            g_compDur = std::max(g_compDur, c.compStart + (c.out - c.in));
        }
}

// --------------- video compose (center pane) - OFF the UI thread (spec 3.4 P1) ---------------
// The decode thread's dispatch shape (post latest-wanted, overwrite a pending request
// rather than queue it) is unchanged from the GStreamer build - review cycle-4's #1
// finding was that this used to run synchronously on the UI thread every frame curSec
// changed, the P1 violation behind B18/B22/B23. A burst of scrub/split/seek events still
// can never back up behind stale decode work; only the body (now a call into the
// in-process engine, engineShowFrame, instead of a GStreamer pull) changed for D-1.
static std::mutex g_decReqMx;
static std::condition_variable g_decReqCv;
static std::string g_decReqSource;
static double g_decReqSrcSec = 0, g_decReqCompT = -1;
static bool g_decReqExact = true;
static bool g_decReqPending = false;
static bool g_decQuit = false;

static void composeOnDecodeThread(const std::string& source, double srcSec, double compT, bool exact) {
    double t0 = nowSec();
    engineShowFrame(source, srcSec, exact);
    scrubLog("DECODE compT=" + std::to_string(compT) + " srcSec=" + std::to_string(srcSec) +
        " exact=" + (exact ? "1" : "0") + " seekMs=" + std::to_string((nowSec() - t0) * 1000.0));
}
static void decodeWorker() {
    t_threadTag = "decodeWorker";
    for (;;) {
        std::string source; double srcSec, compT; bool exact;
        {
            std::unique_lock<std::mutex> lk(g_decReqMx);
            g_decReqCv.wait(lk, [] { return g_decQuit || g_decReqPending; });
            if (g_decQuit) return;
            source = g_decReqSource; srcSec = g_decReqSrcSec; compT = g_decReqCompT; exact = g_decReqExact;
            g_decReqPending = false;
        }
        try {
            composeOnDecodeThread(source, srcSec, compT, exact);
        } catch (const std::exception& e) {
            crashLog(std::string("decodeWorker: caught ") + e.what() + " source=" + source + " - degrading, not crashing");
        } catch (...) {
            crashLog("decodeWorker: caught non-std exception - degrading, not crashing");
        }
    }
}
// UI-thread entry point: NON-BLOCKING. Resolves which clip/source-time t maps to (a cheap
// array scan over g_track[0], no I/O) and hands it to the decode thread; never calls into
// the engine directly from the UI thread. `exact` is false for continuous churn (playing,
// or an active scrub-drag) and true once it settles - see engineShowFrame's comment for
// why this distinction is load-bearing, not cosmetic (I-5).
static void requestCompose(double t, bool exact) {
    Clip* ca = nullptr;
    for (auto& c : g_track[0]) { double d = c.out - c.in; if (t >= c.compStart && t < c.compStart + d) { ca = &c; break; } }
    if (!ca && !g_track[0].empty()) ca = &g_track[0].back();
    if (!ca) return;
    double srcSec = ca->in + (t > ca->compStart ? t - ca->compStart : 0); if (srcSec > ca->out) srcSec = ca->out;
    scrubLog("REQUEST compT=" + std::to_string(t) + " exact=" + (exact ? "1" : "0"));
    std::lock_guard<std::mutex> lk(g_decReqMx);
    g_decReqSource = ca->source; g_decReqSrcSec = srcSec; g_decReqCompT = t; g_decReqExact = exact;
    g_decReqPending = true;
    g_decReqCv.notify_one();
}

// Non-destructive single-click preview (item B, corrected live 2026-07-23):
// shows ONE frame of ONE source at ONE timestamp in the video pane WITHOUT
// touching g_track[0] at all - unlike seekToSpan, which replaces the whole
// live edit reel with a one-clip audition and had no restore of its own. A
// single click is "let me look at this", not "let me rebuild the timeline
// around this". Bypasses requestCompose's g_track[0] lookup and posts the
// (source, srcSec) straight to the same decode thread/engine::showSource path
// requestCompose already uses, so it costs nothing extra and never fights the
// real timeline. Cleared (see clearScrubPreview) the instant the user does
// anything that means "back to the real timeline": clicks the timeline itself,
// starts real playback, or actually adds a clip.
static bool g_scrubPreviewActive = false;
static bool g_scrubPreviewDispatched = false;
static std::string g_scrubPreviewSource;
static double g_scrubPreviewSec = 0;
static void previewSourceFrame(const std::string& src, double sec) {
    g_scrubPreviewActive = true; g_scrubPreviewDispatched = false;
    g_scrubPreviewSource = src; g_scrubPreviewSec = sec;
}
static void clearScrubPreview() { g_scrubPreviewActive = false; }
static void requestComposeSource(const std::string& source, double srcSec, bool exact) {
    scrubLog("REQUEST(preview) source=" + source + " srcSec=" + std::to_string(srcSec) + " exact=" + (exact ? "1" : "0"));
    std::lock_guard<std::mutex> lk(g_decReqMx);
    g_decReqSource = source; g_decReqSrcSec = srcSec; g_decReqCompT = -1; g_decReqExact = exact;
    g_decReqPending = true;
    g_decReqCv.notify_one();
}

// --------------- D-9 (step 6 rewrite): REAL playback with AUDIO - the engine plays the reel ---------------
// The segment list goes to the in-process engine STRAIGHT FROM g_track[0] - the old
// mpv EDL temp file, its CRLF trap, and the time-pos observe/extrapolate dance are all
// gone. Composition time IS the engine's clock (audio-master).
// SCOPE unchanged: reel mode is for PLAYBACK ONLY; paused scrubbing keeps the
// per-clip frame-exact path (engineShowFrame).
static uint64_t g_edlSigLoaded = 0;
static double g_edlSpeedSet = -1;

// FNV-1a over (source, in, out) of every clip: detects a mid-playback edit so the
// engine's segment list can be rebuilt. A hash because this runs every frame.
static uint64_t edlTrackSig() {
    uint64_t h = 1469598103934665603ull;
    auto mix = [&h](const void* p, size_t n) {
        const unsigned char* b = (const unsigned char*)p;
        for (size_t i = 0; i < n; i++) { h ^= b[i]; h *= 1099511628211ull; }
    };
    for (auto& c : g_track[0]) { mix(c.source.data(), c.source.size()); mix(&c.in, sizeof c.in); mix(&c.out, sizeof c.out); }
    return h;
}

static double reelFps(); // defined later (needs the async ffprobe state)
static double quantToFrame(double t); // defined later (needs reelFps)

// Hands the engine the current reel and starts it PLAYING at compT. Also used to
// re-enter after a mid-playback edit (rebuild + resume at the same spot).
static void engineReelEnter(double compT) {
    if (!engine::available() || g_track[0].empty()) return;
    std::vector<EngineSeg> segs;
    segs.reserve(g_track[0].size());
    for (auto& c : g_track[0]) {
        if (c.out - c.in <= 0) continue;
        segs.push_back(EngineSeg{ c.source, c.in, c.out });
    }
    if (segs.empty()) return;
    if (compT < 0) compT = 0;
    engine::enterReel(segs, reelFps(), compT, g_playRate);
    g_edlSigLoaded = edlTrackSig();
    g_edlSpeedSet = g_playRate;
    g_edlActive.store(true);
}

static void engineReelExit() {
    if (!g_edlActive.load()) return;
    g_edlActive.store(false);
    engine::exitReel();
    g_edlSigLoaded = 0;
}

static void engineReelSeek(double compT) {
    if (!g_edlActive.load()) return;
    engine::seekReel(compT);
}

// --------------- NDJSON out to the engine is over the subprocess; UI sends via engineCall ---------------
// --------------- view + gesture state ---------------
static HWND g_hwnd = nullptr;
static double g_pps = 60;
// Up/Down arrow zoom. The zoom math is a lambda inside drawTimeline (it needs the
// panel's own rect to keep an anchor point fixed), so the key handler in the main
// loop cannot call it directly - it leaves a request here and drawTimeline spends
// it on the same frame. +1 = in, -1 = out, 0 = nothing pending.
static int g_zoomReq = 0;
static double g_scrollSec = 0;
static bool g_visible = true;
static bool g_playingExt = false;
// (g_playRate - D-4's 2x playback - now lives up with the engine globals; see D-9.)
static double g_stockSec = -1;
static bool g_stockFlash = false;
// Where the CURRENT run of playback began (item 59: "press 'pause' and playhead
// should return back to where it was at the start of playback"). -1 = not
// playing / nothing to return to.
//
// RELATED TO g_stockSec BUT NOT THE SAME THING, and the difference is the whole
// design. The STOCK is an EXPLICIT choice he made mid-playback by clicking a
// clip - his words: "that placement is where the playhead will return when the
// user pauses playback". The PLAY-START is the DEFAULT return point for when he
// chose nothing. So on pause the stock wins if one is set; otherwise the
// playhead goes back to where he pressed play. The stock also still decides
// where edit keys apply during playback (E-6) - untouched.
static double g_playStartSec = -1;
static double g_lastUserScroll = 0;
static std::set<std::string> g_sel;
static std::string g_selAnchor;
static bool g_thrOn = false;
static double g_thrLevel = 0.14;
static bool g_quietDirty = true;
static int g_quietEpochSeen = -1;
static std::vector<std::pair<double, double>> g_quietRanges;
static double g_lastQuietEmit = 0, g_lastThrEmit = 0;

// "Skip quiet parts" accuracy (Jordan, 2026-07-24): the old detector thresholded
// the 8-bit waveform PEAK cache - a coarse, windowed, DISPLAY signal that
// overestimates loudness and, worse, is filled lazily per-window so a clip whose
// [in,out] window isn't fully cached reads as "not quiet" near its edges (his
// exact symptom: complete silences near clip starts/ends never dimmed). The real
// fix he asked for: use auto-editor's own per-frame volume, which is frame-accurate
// to the source fps and is the SAME signal auto-editor thresholds when it cuts.
// audio_levels (cmd/clip/audiolevels.go) runs `auto-editor levels ... --edit audio`
// ONCE per source and returns {fps, levels[]} (each a normalized max|sample|/32767,
// linear 0..1 - the same units g_thrLevel is in). Cached here; the live threshold
// drag re-thresholds the cached array with zero re-decode. If auto-editor is missing
// or a source hasn't reported yet, computeQuietRangesNow falls back to the old peak
// method so the feature still works, just less precisely.
struct SrcLevels { double fps = 0; std::vector<float> lv; bool ready = false; };
static std::map<std::string, SrcLevels> g_srcLevels;       // source path -> envelope
static std::set<std::string> g_srcLevelsInFlight;          // async fetches running

struct Gesture {
    int kind = 0;
    int idx = -1;
    float pressX = 0;
    bool ctrl = false, shiftK = false;
    double gIn = 0, gOut = 0;
    std::vector<int> group;
    double grabOff = 0;
    bool dragged = false;
};
static Gesture g_gest;
static double g_lastScrubEmit = 0, g_lastViewEmit = 0;
static double g_lastUndoQueued = -1;
static double g_lastRedoQueued = -1;   // redo debounce, same reason as undo's
static double g_lastSplitQueued = -1;  // split ('S') debounce, item 10 - same reason as undo's
static double g_lastCapSplitQueued = -1;  // item 9 (round 2): caption split, same 'S' key, own debounce
// Ctrl read from the SAME clock and BOTH bits as every other modifier here -
// see the ctrlDown comment in the arrow handler for why that matters.
static bool ctrlDownForRedo() { SHORT c = GetAsyncKeyState(VK_CONTROL); return (c & 0x8000) != 0 || (c & 1) != 0; }

// ONE undo path and ONE redo path, shared by the keyboard chord and the toolbar
// button. Written as functions rather than copied into the button handler on
// purpose: the debounce below is load-bearing (an extra undo walks PAST the
// intended edit and is destructive), and a second hand-copied call site is
// exactly how a debounce gets left off one of them.
//
// 250ms -> 60ms (2026-07-23), measured after the mpv->native-engine swap. The
// 250ms figure dated from when engineCall() blocked the UI thread on mpv's IPC
// round trip; that block WAS the throttle, so 250ms was never really "chosen",
// it was however long mpv happened to take. Measured on the real engine, 10
// rapid Ctrl+Z presses driven at 90ms apart via editLog timestamps
// (undo_measure.log, 2026-07-23): engineCall(undo) "wrote" -> "wait done" is
// 1.3-1.6ms - the debounce was ~150-200x the real round trip, and at 90ms
// gaps it silently dropped half of every real press (6 sent, 3 landed,
// verified by clip count). 60ms sits inside Jordan's requested 50-75ms floor:
// far under any deliberate double-tap, still comfortably absorbs a same/next-
// frame double edge.
static const double kEditDebounceSec = 0.06;
static void queueUndo(double t) {
    double n = nowSec();
    if (n - g_lastUndoQueued <= kEditDebounceSec) return;
    g_lastUndoQueued = n;
    editLog("EDGE UNDO");
    // req.args MUST be json::object(), not {} - see the long note at the Ctrl+Z
    // handler: an empty brace list picks the default ctor and yields JSON null,
    // which threw in the drain and silently skipped the timeline refresh.
    EditReq req; req.verb = "undo"; req.args = json::object(); req.kind = 4; req.t = t;
    queueEdit(std::move(req));
}
static void queueRedo(double t) {
    double n = nowSec();
    if (n - g_lastRedoQueued <= kEditDebounceSec) return;
    g_lastRedoQueued = n;
    editLog("EDGE REDO");
    EditReq req; req.verb = "redo"; req.args = json::object(); req.kind = 4; req.t = t;
    queueEdit(std::move(req));
}

// The TWO ways playback ends on purpose, item 59, in one place so they can never
// drift apart:
//
//   returnToStart = true   PAUSE (Space / the Pause button). "press 'pause' and
//                          playhead should return back to where it was at the
//                          start of playback." Auditioning a passage should not
//                          cost him the spot he was working at - which is the
//                          same complaint as clicking a clip moving the playhead.
//   returnToStart = false  ENTER. "press 'enter' and playhead stops where it is."
//                          A deliberate STOP HERE: he heard the thing he was
//                          listening for and wants to work at THAT frame.
//
// Deliberately NOT wired into the other places that set playing=false (arrow-key
// stepping, boundary jumps, the tied-clip preview teardown). Those are not the
// user asking to stop - yanking the playhead backwards there would be the exact
// lost-my-place bug this is meant to cure.
static void stopPlayback(double& curSec, bool& playing, bool returnToStart) {
    playing = false; g_playingExt = false;
    if (returnToStart) {
        if (g_stockSec >= 0)            curSec = g_stockSec;
        else if (g_playStartSec >= 0)   curSec = g_playStartSec;
    }
    if (curSec < 0) curSec = 0;
    g_stockSec = -1; g_stockFlash = false; g_playStartSec = -1;
}

// SEEK WORKER (coalesce-to-latest). emitScrub used to call engineCall("seek")
// SYNCHRONOUSLY on the UI thread on every frame of a drag/scrub - a full blocking
// pipe round-trip to the Go engine (~ms each), whose reply was then thrown away.
// To a fast editor scrubbing the timeline that IS the sluggishness: the frame
// cannot present until the engine answers a seek nobody reads. This worker takes
// the LATEST requested position and seeks to that, dropping every intermediate
// one (you only care where the playhead is NOW), so the UI thread never blocks.
// Same shape as the decode worker's coalesce-to-latest; detached, so process exit
// is its only shutdown. The engine "seek" is shared-state telemetry for the AI
// seam - it never needed to be on the UI thread's critical path.
static std::mutex g_seekMx;
static std::condition_variable g_seekCv;
static double g_seekTarget = -1.0;   // pending target; <0 means nothing queued
static bool   g_seekQuiet  = true;
static void ensureSeekWorker() {
    static std::once_flag once;
    std::call_once(once, [] {
        std::thread([] {
            t_threadTag = "seekWorker";
            for (;;) {
                double t; bool quiet;
                {
                    std::unique_lock<std::mutex> lk(g_seekMx);
                    g_seekCv.wait(lk, [] { return g_seekTarget >= 0.0; });
                    t = g_seekTarget; quiet = g_seekQuiet;
                    g_seekTarget = -1.0;   // consumed; a newer scrub re-posts
                }
                json r = engineCall("seek", { {"t", t}, {"quiet", quiet} }, 2.0);
                (void)r;
            }
        }).detach();
    });
}
static void emitScrub(double t, bool final_) {
    double n = nowSec();
    if (!final_ && n - g_lastScrubEmit < 0.016) return;
    g_lastScrubEmit = n;
    // Post the latest playhead position to the seek worker and return immediately -
    // NEVER block the UI thread here (see ensureSeekWorker). The final scrub (mouse
    // release) is not throttled above, so the committed position always lands last.
    ensureSeekWorker();
    {
        std::lock_guard<std::mutex> lk(g_seekMx);
        g_seekTarget = (t < 0.0 ? 0.0 : t);
        g_seekQuiet  = !final_;
    }
    g_seekCv.notify_one();
}
static bool emitView() {
    double n = nowSec();
    if (n - g_lastViewEmit < 0.1) return false;
    g_lastViewEmit = n;
    return true;
}
static void emitSelect() {
    // A-4/P1 FIX (found live this session): this used to call engineCall()
    // SYNCHRONOUSLY, and it is invoked from the split-reply-apply drain step
    // in the main loop - i.e. from the UI thread, once per completed split.
    // A burst of splits (I-6's exact scenario) lands several replies in one
    // frame, each stacking ANOTHER blocking engine round trip on the UI
    // thread before it can pump messages or present a frame - a direct P1
    // violation that the rest of this file's split/delete/trim/undo path was
    // specifically rearchitected (editWorker) to avoid. Detach it: selection
    // sync to the engine is best-effort telemetry for the AI seam, not
    // something the UI needs to block on or strictly order.
    json ids = json::array();
    for (auto& c : g_track[0]) if (g_sel.count(c.id)) ids.push_back(c.id);
    // Adversarial-review fix: this spawns a fresh OS thread on every selection
    // change, unlike the one-time startup workers above - under a rapid burst
    // (I-6's exact scenario, dozens of edits/sec each touching selection) OS
    // thread creation can transiently fail (resource exhaustion), and an
    // uncaught std::system_error from the std::thread ctor reaches
    // std::terminate() -> abort() (see crashLogInit's note on this exact
    // mechanism). This is best-effort telemetry, so a failed thread just means
    // this one selection sync is skipped - never worth crashing the session.
    try {
        std::thread([ids] { json r = engineCall("set_select", { {"ids", ids} }, 2.0); (void)r; }).detach();
    } catch (const std::exception& e) {
        editLog(std::string("emitSelect: thread spawn failed, skipping sync: ") + e.what());
    }
}
static void emitThreshold(bool final_) {
    double n = nowSec();
    if (!final_ && n - g_lastThrEmit < 0.1) return;
    g_lastThrEmit = n;
    // cycle 18 review's THE ONE THING (item 1 of 2): this fired a synchronous
    // engineCall on the UI thread every time the threshold slider moved (up to
    // every 100ms while dragging) - the same P1 violation emitSelect() above was
    // already fixed for. Same fix, same reasoning: best-effort telemetry for the
    // AI seam, never worth blocking a frame or crashing the session over.
    bool on = g_thrOn; double level = g_thrLevel;
    try {
        std::thread([on, level] { json r = engineCall("set_threshold", { {"on", on}, {"level", level} }, 2.0); (void)r; }).detach();
    } catch (const std::exception& e) {
        editLog(std::string("emitThreshold: thread spawn failed, skipping sync: ") + e.what());
    }
}
// ensureLevels kicks off (once) an async fetch of a source's auto-editor volume
// envelope into g_srcLevels. Cheap to call every recompute - it no-ops once the
// source is cached or already in flight. On arrival it flags g_quietDirty so the
// dim regions recompute against the accurate signal. Callback runs on the UI
// thread (drainAsync), same as the transcript fetch, so no locking is needed.
static void ensureLevels(const std::string& source) {
    if (source.empty()) return;
    if (g_srcLevels.count(source) || g_srcLevelsInFlight.count(source)) return;
    g_srcLevelsInFlight.insert(source);
    engineCallAsync("audio_levels", { {"source", source} }, 600.0, "Analyzing audio (auto-editor)...",
        [source](const json& r) {
            g_srcLevelsInFlight.erase(source);
            SrcLevels sl;
            if (r.value("ok", false)) {
                const json& d = r.contains("data") ? r["data"] : r;
                if (d.is_object()) {
                    sl.fps = d.value("fps", 0.0);
                    if (d.contains("levels") && d["levels"].is_array()) {
                        sl.lv.reserve(d["levels"].size());
                        for (auto& v : d["levels"]) sl.lv.push_back((float)v.get<double>());
                    }
                }
            }
            // Cache even an empty result: "auto-editor couldn't answer" is an
            // answer, asked once; computeQuietRangesNow falls back to peaks for it.
            sl.ready = true;
            g_srcLevels[source] = std::move(sl);
            g_quietDirty = true;
        });
}

// Item 3b: the broomstick button needs quiet ranges regardless of whether
// "skip during playback" (g_thrOn) is toggled on - they're two independent
// uses of the SAME threshold level. Pulled the actual scan out of
// recomputeQuiet so both can share it without the g_thrOn gate.
//
// Per clip it prefers auto-editor's per-frame envelope (frame-accurate, covers the
// WHOLE source so silence near a clip edge is never missed) and falls back to the
// old 8-bit peak cache only until the levels arrive / if auto-editor is absent.
// g_thrLevel is a linear peak fraction (0..1), the SAME units as an auto-editor
// level (max|sample|/32767), so the dB the bar shows means the same thing here.
static std::vector<std::pair<double, double>> computeQuietRangesNow() {
    std::vector<std::pair<double, double>> raw, out;
    for (auto& c : g_track[0]) {
        ensureLevels(c.source);
        auto lit = g_srcLevels.find(c.source);
        bool haveLevels = lit != g_srcLevels.end() && lit->second.ready &&
                          !lit->second.lv.empty() && lit->second.fps > 1.0;
        if (haveLevels) {
            const SrcLevels& sl = lit->second;
            double fps = sl.fps;
            long long n = (long long)sl.lv.size();
            long long f0 = std::max(0LL, (long long)std::floor(c.in * fps));
            long long f1 = std::min(n, (long long)std::ceil(c.out * fps));
            double runA = -1;
            for (long long f = f0; f <= f1; f++) {
                bool quiet = (f < f1 && f < n) ? sl.lv[f] < g_thrLevel : false;
                double compT = c.compStart + ((double)f / fps - c.in);
                if (quiet && runA < 0) runA = compT;
                else if (!quiet && runA >= 0) { raw.push_back({ runA, compT }); runA = -1; }
            }
        } else {
            auto pk = peaksGet(c.source);
            if (!pk) continue;
            std::lock_guard<std::mutex> lk(pk->mx);
            if (!pk->ready) continue;
            long long b0 = std::max(0LL, (long long)(c.in * kBinsPerSec));
            long long b1 = std::min((long long)pk->bins, (long long)(c.out * kBinsPerSec));
            double runA = -1;
            for (long long b = b0; b <= b1; b++) {
                bool quiet = false;
                if (b < b1) {
                    int8_t mn = pk->n0[b], mx = pk->x0[b];
                    if (mn <= mx) {
                        double amp = std::max(std::abs((int)mn), std::abs((int)mx)) / 127.0;
                        quiet = amp < g_thrLevel;
                    }
                }
                double compT = c.compStart + (b / kBinsPerSec - c.in);
                if (quiet && runA < 0) runA = compT;
                else if (!quiet && runA >= 0) { raw.push_back({ runA, compT }); runA = -1; }
            }
        }
    }
    std::sort(raw.begin(), raw.end());
    for (auto& r : raw) {
        if (!out.empty() && r.first <= out.back().second + 0.06)
            out.back().second = std::max(out.back().second, r.second);
        else out.push_back(r);
    }
    out.erase(std::remove_if(out.begin(), out.end(),
        [](const std::pair<double, double>& r) { return r.second - r.first < 0.35; }), out.end());
    return out;
}
static void recomputeQuiet() {
    g_quietRanges.clear();
    if (!g_thrOn) return;
    g_quietRanges = computeQuietRangesNow();
}

// Load a TimelineView (from engine "timeline" verb) into the native track.
// A-1: defined in the caption section below; loadTimelineView/seekToSpan re-derive
// the caption lane from the clips' own source transcripts after every rebuild.
static void rebuildDerivedCaptions();

// B (Jordan: "all clips from that source video are made a certain color and that
// color does not change for the rest of the project"): the ENGINE owns the
// per-source colour (cmd/clip/clipcolor.go, persisted per project). This is the
// native mirror of every colour the engine has stated, so PREVIEW clips built
// locally (seekToSpan auditions, the add_clip fallback) wear the same colour as
// the source's real timeline clips instead of a hardcoded crimson.
static std::map<std::string, uint32_t> g_srcRGB;   // lowercased source path -> 0xRRGGBB
static std::string srcColorKey(std::string s) {
    for (auto& c : s) { if (c == '/') c = '\\'; c = (char)tolower((unsigned char)c); }
    return s;
}
static void paintClipFromKnownSource(Clip& cl) {
    auto it = g_srcRGB.find(srcColorKey(cl.source));
    if (it != g_srcRGB.end()) {
        cl.r = (uint8_t)((it->second >> 16) & 0xFF);
        cl.g = (uint8_t)((it->second >> 8) & 0xFF);
        cl.b = (uint8_t)(it->second & 0xFF);
    } else { cl.r = 220; cl.g = 30; cl.b = 60; }   // engine never met it: preview crimson
}

static void loadTimelineView(const json& tv) {
    // An authoritative reload always wins over a stale "Play tied clips" preview.
    g_inTiedPreview = false; g_reelBeforePreview.clear();
    g_track[0].clear(); g_track[1].clear();
    if (tv.contains("clips") && tv["clips"].is_array()) {
        for (auto& c : tv["clips"]) {
            double i = c.value("in", 0.0), o = c.value("out", 0.0);
            std::string src = c.value("source", std::string());
            if (o <= i || src.empty()) continue;
            std::string label = c.value("label", std::string());
            if (label.empty()) label = baseName(src);
            Clip cl; cl.in = i; cl.out = o; cl.compStart = c.value("start_sec", 0.0);
            cl.label = label; cl.source = src; cl.id = c.value("id", std::string());
            std::string hex = c.value("color", std::string());
            if (hex.size() == 7 && hex[0] == '#') {
                long v = strtol(hex.c_str() + 1, nullptr, 16);
                cl.r = (uint8_t)((v >> 16) & 0xFF); cl.g = (uint8_t)((v >> 8) & 0xFF); cl.b = (uint8_t)(v & 0xFF);
                g_srcRGB[srcColorKey(src)] = (uint32_t)v;   // B: previews reuse this colour
            }
            cl.ready = true;
            cl.date = c.value("date", std::string());
            cl.person = c.value("person", std::string());
            cl.location = c.value("location", std::string());
            cl.link = c.value("link", std::string());
            cl.srcFps = c.value("source_fps", 0.0);
            g_track[0].push_back(cl);
        }
    }
    packTrack(0); recomputeDur();
    g_trackClipCountForLog.store(g_track[0].size(), std::memory_order_relaxed);
    // Measure, don't assert (same reason the caption-commit code below logs
    // instead of asserting): Jordan cut every clip in Vegas frame-exact, so
    // every clip boundary MUST already sit on quantToFrame's grid - this is
    // not something the app can be wrong about silently. One line per boundary
    // in crash.log, grepped for "PACK boundary", for the first 10 cuts of every
    // freshly loaded reel.
    for (size_t i = 0; i < g_track[0].size() && i < 10; i++) {
        double b = g_track[0][i].compStart, q = quantToFrame(b);
        crashLog("PACK boundary i=" + std::to_string(i) + " t=" + std::to_string(b) +
                 " quant=" + std::to_string(q) + " err=" + std::to_string(q - b));
    }
    // Item 4 fix (round 3): PRESERVE selection across a reload when the clip id
    // still exists post-edit (a trim/extend just resized it in place, same id) -
    // only DROP ids that genuinely disappeared (delete/split/replace). An
    // unconditional clear() here was WHY the extend-selected-clip-by-one-frame
    // buttons deselected the clip on every press: each press is a set_trim edit,
    // and every edit reply reloads the timeline through this exact function.
    // Root-caused once, here, instead of patching every edit-kind switch case
    // that reselects (split already does this itself at the drain site; trim
    // needs no such patch because the id it operates on never changes).
    if (g_sel.empty()) {
        // nothing to preserve
    } else {
        std::set<std::string> keep;
        for (auto& c : g_track[0]) if (g_sel.count(c.id)) keep.insert(c.id);
        g_sel.swap(keep);
    }
    // Windowed waveform decode: only what's on the timeline, newest first. (FB9 fix: keyed by SOURCE.)
    // I-2 fix: skip clips whose (source,in,out) is unchanged since the last reload -
    // see requestPeaksIfChanged above. This runs on EVERY edit reply, so this is the
    // one call site that actually needed the dedup (the other 3 peaksRequest loops in
    // this file run at boot / on rare preview toggles, not once per edit).
    for (auto& c : g_track[0]) requestPeaksIfChanged(c);
    g_quietDirty = true;
    // D-6: mirror the engine's current overlay field toggles (edl.Overlay), so the
    // preview knows exactly which lines the render is about to burn in.
    if (tv.contains("overlay") && tv["overlay"].is_object()) {
        const json& ov = tv["overlay"];
        g_overlay.enabled = ov.value("enabled", g_overlay.enabled);
        g_overlay.showFilename = ov.value("show_filename", g_overlay.showFilename);
        g_overlay.showTimecode = ov.value("show_timecode", g_overlay.showTimecode);
        g_overlay.showDate = ov.value("show_date", g_overlay.showDate);
        g_overlay.showLink = ov.value("show_link", g_overlay.showLink);
        g_overlay.showPerson = ov.value("show_person", g_overlay.showPerson);
        g_overlay.showLocation = ov.value("show_location", g_overlay.showLocation);
        g_overlay.position = ov.value("position", g_overlay.position);
        g_ovEngineEnabled = g_overlay.enabled;
    }
    // A-1: the timeline changed - re-derive the caption lane from the clips'
    // source transcripts (no-op when a hand-edited reel sidecar is loaded).
    rebuildDerivedCaptions();
}

// I-2 wire-protocol fix (cycle 27, the "ONE THING" three straight adversarial
// reviews flagged): add_clip's reply used to be a full TimelineView (loadTimelineView
// above, clear + rebuild ALL clips) even though an add only ever changes ONE clip.
// At 5,000 clips that full-timeline reply was ~4.7MB of JSON, costing ~45-48ms of
// nlohmann DOM parse alone (measured cycle 26) PLUS an O(n) native rebuild
// (peaksRequest/captions/packTrack over every clip) on every single add. The
// engine (becky-go bridge.go addClipReply) now replies with ONLY the new clip -
// this appends that one clip locally and repacks positions itself (packTrack is a
// cheap O(n) loop over doubles already in memory, not JSON), instead of
// clear-and-rebuilding the whole track from a full reply. Mirrors loadTimelineView's
// per-clip field handling exactly (same colour/meta/peaks logic) so a delta-applied
// clip is indistinguishable from one that came through a full reload.
static void applyAddClipDelta(const json& d) {
    if (!d.contains("clip") || !d["clip"].is_object()) return;
    // DRIFT GUARD (found live this session): seekToSpan (single-click hit preview,
    // C-4 - a completely separate, pre-existing feature) locally REPLACES g_track[0]
    // with just the one audition clip, with no restore of its own - the old
    // always-full-reload code silently healed this on the next add/split/undo/
    // anything, because it re-synced from the engine's actual reel every time. This
    // delta path does not reload, so applying it on top of a locally-corrupted
    // track would compound the corruption (e.g. insert onto a 1-clip preview
    // instead of the real 5,000). clip_count is the engine's authoritative total
    // AFTER this add - if it doesn't match what a local append would produce, local
    // state has drifted, so fall back to one real reload (same as before this
    // session, just only paid on the rare divergent case instead of every add).
    int expectCount = d.value("clip_count", -1);
    if (expectCount >= 0 && (int)g_track[0].size() + 1 != expectCount) {
        // Async, not a blocking engineCall - this runs on the UI thread (drainAsync
        // callback), and the whole point of this session's fix is to stop blocking
        // it on a full-timeline round trip. The rare divergent case still costs one
        // full reload, it just never blocks input while it happens.
        engineCallAsync("timeline", {}, 10.0, "Resyncing timeline...", [](const json& tv) {
            if (tv.value("ok", false)) loadTimelineView(tv["data"]);
        });
        return;
    }
    const json& c = d["clip"];
    double i = c.value("in", 0.0), o = c.value("out", 0.0);
    std::string src = c.value("source", std::string());
    if (o <= i || src.empty()) return;
    // Same as loadTimelineView: an authoritative engine-confirmed add cancels any
    // stale "Play tied clips" preview.
    g_inTiedPreview = false; g_reelBeforePreview.clear();
    std::string label = c.value("label", std::string());
    if (label.empty()) label = baseName(src);
    Clip cl; cl.in = i; cl.out = o; cl.compStart = 0.0; // packTrack below sets the real value
    cl.label = label; cl.source = src; cl.id = c.value("id", std::string());
    std::string hex = c.value("color", std::string());
    if (hex.size() == 7 && hex[0] == '#') {
        long v = strtol(hex.c_str() + 1, nullptr, 16);
        cl.r = (uint8_t)((v >> 16) & 0xFF); cl.g = (uint8_t)((v >> 8) & 0xFF); cl.b = (uint8_t)(v & 0xFF);
        g_srcRGB[srcColorKey(src)] = (uint32_t)v;   // B: previews reuse this colour
    } else {
        paintClipFromKnownSource(cl);
    }
    cl.ready = true;
    cl.date = c.value("date", std::string());
    cl.person = c.value("person", std::string());
    cl.location = c.value("location", std::string());
    cl.link = c.value("link", std::string());
    cl.srcFps = c.value("source_fps", 0.0);

    int idx = d.value("index", (int)g_track[0].size());
    if (idx < 0 || idx > (int)g_track[0].size()) idx = (int)g_track[0].size();
    g_track[0].insert(g_track[0].begin() + idx, cl);

    packTrack(0); recomputeDur();
    g_trackClipCountForLog.store(g_track[0].size(), std::memory_order_relaxed);
    g_sel.clear();   // matches loadTimelineView - a track mutation clears selection
    requestPeaksIfChanged(cl);
    g_quietDirty = true;
    // A-1: same as loadTimelineView - re-derive captions so the new clip's own
    // transcript (if already cached) shows immediately. Still O(n) over the whole
    // track (unlike everything else in this function) - not fixed this session,
    // see the result file's follow-up notes.
    rebuildDerivedCaptions();
}

// --------------- D3D11 display ---------------
// (mpv is gone - step 6 of the video-engine swap. The video frame is a D3D11
// texture again, decoded by engine.cpp and drawn by ImGui via currentFrameSRV()
// into THIS same swap chain - see the video pane in the render loop. No child
// hwnd, no separate paint surface.)
static ID3D11Device* g_dev = nullptr; static ID3D11DeviceContext* g_ctx = nullptr; static IDXGISwapChain* g_swap = nullptr;
static ID3D11RenderTargetView* g_rtv = nullptr;
static int g_W = 1280, g_H = 800;
static bool g_resize = false;

static void createRTV() {
    ID3D11Texture2D* bb = nullptr;
    g_swap->GetBuffer(0, __uuidof(ID3D11Texture2D), (void**)&bb);
    if (bb) { g_dev->CreateRenderTargetView(bb, nullptr, &g_rtv); bb->Release(); }
}
// ---- INPUT LATENCY: the reason this app could not keep up with Jordan's hands ----
//
// He is a professional editor - "one of the fastest video editors in the world" -
// and said the previous (WPF) build "FROZE when i tried touching it... literally
// my muscle memory broke the entire goddamn thing", with the bar set at "as snappy
// as Vegas Pro timeline (or faster)".
//
// The default presentation path was costing ~50ms of input-to-screen delay before
// a single line of this app's code ran:
//   * DXGI's default MaximumFrameLatency is THREE. The app is allowed to run up to
//     three frames ahead of what the display is showing, so a click was rendered
//     into a frame the user would not see for ~3 refreshes.
//   * Present(1,0) then blocked at the END of the frame. Blocking after rendering
//     is backwards: input sampled at the top of the frame is already one whole
//     frame stale by the time it is presented.
//
// The fix is the standard low-latency desktop pattern, and it needs a swap chain
// created through IDXGIFactory2 (D3D11CreateDeviceAndSwapChain cannot request it):
//   * FRAME_LATENCY_WAITABLE_OBJECT + SetMaximumFrameLatency(1) - at most one frame
//     in flight instead of three.
//   * Wait on that object at the TOP of the frame (see the render loop). The thread
//     sleeps until the GPU is actually ready for a new frame, and input is sampled
//     immediately AFTER the wait - as late as possible before rendering, which is
//     what makes a scrub feel attached to the mouse.
//
// vsync is deliberately KEPT (Present(1,0)): this window also hosts video playback,
// and tearing on playback would be a worse defect than the latency this removes.
// g_frameWait is null on a machine where the waitable path is unavailable, and every
// use is null-guarded, so this degrades to the old behaviour rather than failing.
static HANDLE g_frameWait = nullptr;

static bool CreateD3D(HWND h) {
    UINT flags = 0;
#ifdef _DEBUG
    flags |= D3D11_CREATE_DEVICE_DEBUG;
#endif
    // BITBLT swap chain (DXGI_SWAP_EFFECT_DISCARD), deliberately NOT flip-model.
    //
    // THE CPU-SPIN ROOT CAUSE, HISTORICAL (measured 2026-07-20, when mpv still owned
    // an OVERLAPPING --wid child HWND): a flip-model swap chain (FLIP_DISCARD, the
    // setting at the time) cannot be composited cheaply against an overlapping child
    // window - DWM dropped to a continuous per-frame redraw path that spun ~12 Windows
    // thread-pool threads at ~345% CPU with the app sitting IDLE and even EMPTY (no
    // reel). That spin starved mpv's decode to ~10-15 fps on a 29.97fps clip and made a
    // plain click feel slow. Proof it was the compositor, not our code: idle CPU was
    // 345% with the window visible and 29% the instant it was minimized (the render
    // loop skips drawing when hidden). Bitblt composites with child windows without
    // that spin.
    //
    // mpv (and its child hwnd) is gone since the step-6 engine swap - the video frame
    // is now an in-process D3D11 texture drawn into THIS swap chain, so the specific
    // overlapping-hwnd trigger no longer exists. Kept on BITBLT anyway: nothing has
    // re-measured flip-model + FRAME_LATENCY_WAITABLE_OBJECT against the new
    // texture-in-same-swapchain design, so switching back is an unverified change, not
    // a settled one. If idle-CPU work ever revisits this, that measurement is the
    // open question, not a re-run of the old repro.
    //
    // Cost: bitblt cannot use FRAME_LATENCY_WAITABLE_OBJECT, so g_frameWait stays null
    // and vsync pacing comes from Present(1,0) alone - one extra frame of latency. That
    // is the right trade: a low-latency path that pegs 4 cores and plays at 10fps is not
    // actually low-latency to the user, it is unusable. Present(1,0) still blocks on
    // vsync, so the loop is paced (61 fps measured), never busy-spinning.
    DXGI_SWAP_CHAIN_DESC sd = {};
    sd.BufferCount = 2;
    sd.BufferDesc.Width = g_W; sd.BufferDesc.Height = g_H;
    sd.BufferDesc.Format = DXGI_FORMAT_R8G8B8A8_UNORM;
    sd.BufferUsage = DXGI_USAGE_RENDER_TARGET_OUTPUT;
    sd.OutputWindow = h;
    sd.SampleDesc.Count = 1;
    sd.Windowed = TRUE;
    sd.SwapEffect = DXGI_SWAP_EFFECT_DISCARD;
    if (FAILED(D3D11CreateDeviceAndSwapChain(nullptr, D3D_DRIVER_TYPE_HARDWARE, nullptr, flags, nullptr, 0,
        D3D11_SDK_VERSION, &sd, &g_swap, &g_dev, nullptr, &g_ctx))) return false;
    createRTV();
    return true;
}
static void resizeD3D() {
    if (!g_swap || !g_ctx) return;
    if (g_rtv) { g_rtv->Release(); g_rtv = nullptr; }
    // The flags argument MUST repeat FRAME_LATENCY_WAITABLE_OBJECT. Passing 0 here
    // silently drops it, and the waitable handle obtained at creation stops being
    // signalled - the render loop would then block a full second per frame on its
    // wait. Resizing must not quietly undo the low-latency setup.
    g_swap->ResizeBuffers(0, g_W, g_H, DXGI_FORMAT_UNKNOWN,
                          g_frameWait ? DXGI_SWAP_CHAIN_FLAG_FRAME_LATENCY_WAITABLE_OBJECT : 0);
    createRTV();
}

// --------------- E-11: clip thumbnails ---------------
// The engine's "thumb" verb (becky-go cmd/clip/export.go Thumb) already returns a
// small CACHED first-frame JPEG as a base64 data: URI - built for the abandoned
// WebView2 build's <img> tag. This native app has no <img>, so it needs its own
// decode: base64 -> JPEG bytes -> WIC (Windows' built-in image codec, a native
// platform feature - no external image library needed, ladder rung 4) -> a D3D11
// texture ImGui can draw with AddImage. Fetched off the UI thread (the engine
// round-trip + JPEG decode are both too slow to do inline in a frame - same A-4
// shape as requestTranscribe/requestAddExternal); the finished SRV is created via
// the DEVICE (not the immediate context), which is free-threaded per the D3D11
// spec, so it is safe to hand straight to the UI thread's cache for drawing.
static std::vector<uint8_t> base64Decode(const std::string& in) {
    static int8_t T[256]; static bool init = false;
    if (!init) {
        std::fill(std::begin(T), std::end(T), (int8_t)-1);
        const char* alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
        for (int i = 0; i < 64; i++) T[(unsigned char)alpha[i]] = (int8_t)i;
        init = true;
    }
    std::vector<uint8_t> out; out.reserve(in.size() * 3 / 4 + 3);
    int val = 0, bits = -8;
    for (unsigned char c : in) {
        if (c == '=' || T[c] < 0) continue;
        val = (val << 6) + T[c]; bits += 6;
        if (bits >= 0) { out.push_back((uint8_t)((val >> bits) & 0xFF)); bits -= 8; }
    }
    return out;
}
struct ThumbTex { ID3D11ShaderResourceView* srv = nullptr; int w = 0, h = 0; };
static std::map<std::string, ThumbTex> g_thumbCache; // UI-thread-owned, no lock needed
static std::mutex g_thumbMx;
static std::set<std::string> g_thumbInFlight;
struct ThumbDone { std::string key; ID3D11ShaderResourceView* srv = nullptr; int w = 0, h = 0; };
static std::deque<ThumbDone> g_thumbDoneQ;
// E-18 (cycle 15 review's "ONE THING"): the contract line is explicit - caches
// are "keyed by SOURCE FILE + resolution level, never by clip identity or clip
// in/out." This used to be keyed by source+t where t was the CLIP's in-point
// (cin), so every split minted a brand-new key (the child clip's in-point is a
// timestamp nobody ever requested before) and fired a real ffmpeg decode on the
// engine (becky-go cmd/clip/export.go Thumb: cache miss on a new t -> GrabThumb).
// That is genuine per-split media work, exactly what P2/P5 forbid. Fix: key (and
// request) by SOURCE ONLY, same as peaks - one representative frame per source
// file, fetched once, reused by every clip regardless of where it's split. A
// split never changes the source, so it is now unconditionally a cache hit.
static const double kThumbRefT = 0.0; // "first-frame thumbnail" per the engine's own doc comment
static std::string thumbKey(const std::string& source) {
    return source;
}
// Degrade-never-crash: any WIC failure (bad JPEG, codec missing) yields nullptr -
// the clip just shows no thumbnail, exactly like the engine's own {data:""} degrade.
static ID3D11ShaderResourceView* decodeJpegToTexture(const uint8_t* data, size_t len, int& outW, int& outH) {
    if (!data || len == 0 || !g_dev) return nullptr;
    HRESULT coHr = CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    bool coOwned = SUCCEEDED(coHr);
    IWICImagingFactory* factory = nullptr;
    IWICStream* stream = nullptr; IWICBitmapDecoder* decoder = nullptr;
    IWICBitmapFrameDecode* frame = nullptr; IWICFormatConverter* conv = nullptr;
    ID3D11ShaderResourceView* srv = nullptr;
    do {
        if (FAILED(CoCreateInstance(CLSID_WICImagingFactory, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&factory)))) break;
        if (FAILED(factory->CreateStream(&stream))) break;
        if (FAILED(stream->InitializeFromMemory((BYTE*)data, (DWORD)len))) break;
        if (FAILED(factory->CreateDecoderFromStream(stream, nullptr, WICDecodeMetadataCacheOnDemand, &decoder))) break;
        if (FAILED(decoder->GetFrame(0, &frame))) break;
        if (FAILED(factory->CreateFormatConverter(&conv))) break;
        if (FAILED(conv->Initialize(frame, GUID_WICPixelFormat32bppBGRA, WICBitmapDitherTypeNone, nullptr, 0, WICBitmapPaletteTypeCustom))) break;
        UINT w = 0, h = 0; conv->GetSize(&w, &h);
        if (w == 0 || h == 0 || w > 4096 || h > 4096) break;
        std::vector<uint8_t> px((size_t)w * h * 4);
        if (FAILED(conv->CopyPixels(nullptr, w * 4, (UINT)px.size(), px.data()))) break;
        D3D11_TEXTURE2D_DESC td{}; td.Width = w; td.Height = h; td.MipLevels = 1; td.ArraySize = 1;
        td.Format = DXGI_FORMAT_B8G8R8A8_UNORM; td.SampleDesc.Count = 1; td.Usage = D3D11_USAGE_IMMUTABLE;
        td.BindFlags = D3D11_BIND_SHADER_RESOURCE;
        D3D11_SUBRESOURCE_DATA sub{}; sub.pSysMem = px.data(); sub.SysMemPitch = w * 4;
        ID3D11Texture2D* tex = nullptr;
        if (FAILED(g_dev->CreateTexture2D(&td, &sub, &tex)) || !tex) break;
        D3D11_SHADER_RESOURCE_VIEW_DESC svd{}; svd.Format = td.Format;
        svd.ViewDimension = D3D11_SRV_DIMENSION_TEXTURE2D; svd.Texture2D.MipLevels = 1;
        HRESULT hr = g_dev->CreateShaderResourceView(tex, &svd, &srv);
        tex->Release();
        if (FAILED(hr)) { srv = nullptr; break; }
        outW = (int)w; outH = (int)h;
    } while (false);
    if (conv) conv->Release();
    if (frame) frame->Release();
    if (decoder) decoder->Release();
    if (stream) stream->Release();
    if (factory) factory->Release();
    if (coOwned) CoUninitialize();
    return srv;
}
static void requestThumb(const std::string& source) {
    std::string key = thumbKey(source);
    {
        std::lock_guard<std::mutex> lk(g_thumbMx);
        if (g_thumbInFlight.count(key)) return;
        g_thumbInFlight.insert(key);
    }
    g_thumbJobsEnqueued.fetch_add(1, std::memory_order_relaxed);
    g_bgPool->submit([source, key] {
        t_threadTag = "thumbWorker";
        ThumbDone d; d.key = key;
        try {
            json r = engineCall("thumb", { {"source", source}, {"t", kThumbRefT} }, 15.0);
            if (r.value("ok", false)) {
                std::string uri = r.value("data", json::object()).value("data", std::string());
                const std::string marker = "base64,";
                size_t p = uri.find(marker);
                if (p != std::string::npos) {
                    auto bytes = base64Decode(uri.substr(p + marker.size()));
                    if (!bytes.empty()) d.srv = decodeJpegToTexture(bytes.data(), bytes.size(), d.w, d.h);
                }
            }
        } catch (...) {}
        std::lock_guard<std::mutex> lk(g_thumbMx);
        g_thumbInFlight.erase(key);
        g_thumbDoneQ.push_back(d);
    });
}
// Moves any textures finished since last frame into the UI-thread cache. Call
// once per frame before drawing clips.
static void drainThumbs() {
    std::deque<ThumbDone> done;
    { std::lock_guard<std::mutex> lk(g_thumbMx); done.swap(g_thumbDoneQ); }
    for (auto& d : done) g_thumbCache[d.key] = ThumbTex{ d.srv, d.w, d.h };
}
// nullptr = not ready yet (kicks off a fetch the first time a clip asks); a
// cached entry with srv==nullptr means "fetched, no thumbnail available" - a
// terminal degrade state that is never retried every frame (E-18: no repeated
// media work for a clip that's already been resolved, even to "none").
static ThumbTex* getThumb(const std::string& source) {
    std::string key = thumbKey(source);
    auto it = g_thumbCache.find(key);
    if (it != g_thumbCache.end()) return &it->second;
    requestThumb(source);
    return nullptr;
}

// E-13: WM_DROPFILES only captures client-space drop data here - the real work
// (path filtering, screen->timeline-seconds conversion via g_scrollSec/g_pps,
// and the add_external engine call) happens once per frame in drawTimeline,
// same "WndProc stays a thin OS-message forwarder" pattern as g_resize/g_W/g_H.
struct PendingDrop { std::vector<std::string> paths; int clientX = 0, clientY = 0; };
static std::vector<PendingDrop> g_pendingDrops;
static void requestAddExternal(const std::string& path, int at); // defined below, near requestTranscribe
static bool hasExtCI(const std::string& path, const char* ext);  // defined below, near pickOpenReelFile
static std::string convertEditIfNeeded(const std::string& path); // defined below, near pickOpenReelFile

// ---- render/export toolbar requests (done on engine, shown in-window) ----
// Moved up from beside the library helpers (its original spot, further down) so the
// caption-lane / edit-file drop handling in drawTimeline (which now also loads reels
// dropped in as .txt/.xml/.json) can report progress/errors through it too.
static std::string g_renderMsg;           // last render outcome (plain language)
static double g_renderMsgAt = 0;

// Item 3b (round 2): the "broomstick" - Jordan had a button that removed every
// silent stretch FROM the timeline (a real destructive edit, not the playback
// skip), using wherever the threshold slider is set. Builds the "loud only"
// clip list locally (cut every g_track[0] clip against computeQuietRangesNow's
// composition-time spans, converting back to each clip's own source time) and
// replaces the whole reel in ONE call - becky-go's `set_clips` verb
// (cmd/clip/bridge.go) exists for exactly this ("the 'trim to the loud parts'
// action, one undoable edit" per its own comment), so this is wiring, not new
// engine work. One Ctrl+Z undoes the whole sweep.
static void applyRemoveSilence(double& curSec, double& lastComposed) {
    auto ranges = computeQuietRangesNow();
    if (ranges.empty()) {
        g_renderMsg = "No quiet parts found at the current threshold - drag the bar on the timeline to lower it";
        g_renderMsgAt = nowSec();
        return;
    }
    std::vector<Clip> kept;
    for (auto& c : g_track[0]) {
        double segStart = c.compStart, segEnd = c.compStart + (c.out - c.in);
        double cursor = segStart;
        for (auto& r : ranges) {
            double a = std::max(r.first, segStart), b = std::min(r.second, segEnd);
            if (b <= a) continue;
            if (a > cursor) {
                Clip nc = c;
                nc.in = c.in + (cursor - c.compStart);
                nc.out = c.in + (a - c.compStart);
                if (nc.out - nc.in > 0.01) kept.push_back(nc);
            }
            cursor = std::max(cursor, b);
        }
        if (cursor < segEnd) {
            Clip nc = c;
            nc.in = c.in + (cursor - c.compStart);
            nc.out = c.out;
            if (nc.out - nc.in > 0.01) kept.push_back(nc);
        }
    }
    if (kept.empty()) {
        g_renderMsg = "Removing silence at this threshold would empty the timeline - skipped";
        g_renderMsgAt = nowSec();
        return;
    }
    if (kept.size() == g_track[0].size()) {
        bool same = true;
        for (size_t i = 0; i < kept.size(); i++)
            if (std::abs(kept[i].in - g_track[0][i].in) > 0.001 || std::abs(kept[i].out - g_track[0][i].out) > 0.001)
                { same = false; break; }
        if (same) {
            g_renderMsg = "No silent parts to remove at the current threshold";
            g_renderMsgAt = nowSec();
            return;
        }
    }
    json clips = json::array();
    for (auto& c : kept) clips.push_back({ {"source", c.source}, {"in", c.in}, {"out", c.out}, {"label", c.label} });
    engineCallAsync("set_clips", { {"clips", clips} }, 30.0, "Removing silent parts...",
        [&curSec, &lastComposed](const json& r) {
            if (r.value("ok", false)) {
                loadTimelineView(r.contains("data") ? r["data"] : r);
                curSec = std::min(curSec, g_compDur);
                lastComposed = -1;
                g_renderMsg = "Removed the silent parts (Ctrl+Z undoes the whole sweep)";
            } else {
                g_renderMsg = "Could not remove silent parts: " + r.value("error", std::string("unknown"));
            }
            g_renderMsgAt = nowSec();
        });
}

// DragQueryFileW/DragQueryPoint/DragFinish are SHELL32 calls - live-tested this
// session and a malformed/foreign drop payload faulted (0xc0000005) INSIDE
// SHELL32.dll, killing the whole process. SEH-guarded per the exact #0 CRITICAL
// precedent above (gstInitSEH): degrade (drop the message) rather than crash the
// window. Kept free of C++ objects with destructors, matching gstInitSEH's shape -
// MSVC disallows mixing __try with unwind-cleanup objects in the same function.
static bool dropFilesSEH(HDROP hDrop, POINT& pt, wchar_t paths[16][MAX_PATH], int& count) {
    count = 0;
    __try {
        DragQueryPoint(hDrop, &pt);
        UINT n = DragQueryFileW(hDrop, 0xFFFFFFFF, nullptr, 0);
        if (n > 16) n = 16;
        for (UINT i = 0; i < n; i++)
            if (DragQueryFileW(hDrop, i, paths[count], MAX_PATH)) count++;
        DragFinish(hDrop);
        return true;
    } __except (EXCEPTION_EXECUTE_HANDLER) {
        return false;
    }
}

extern IMGUI_IMPL_API LRESULT ImGui_ImplWin32_WndProcHandler(HWND, UINT, WPARAM, LPARAM);
static LRESULT WINAPI WndProc(HWND h, UINT m, WPARAM w, LPARAM l) {
    if (ImGui_ImplWin32_WndProcHandler(h, m, w, l)) return true;
    if (m == WM_SIZE && w != SIZE_MINIMIZED) { g_W = LOWORD(l); g_H = HIWORD(l); g_resize = true; }
    if (m == WM_DESTROY) { PostQuitMessage(0); return 0; }
    if (m == WM_DROPFILES) {
        POINT pt{};
        static wchar_t paths[16][MAX_PATH];
        int count = 0;
        if (dropFilesSEH((HDROP)w, pt, paths, count) && count > 0) {
            PendingDrop d; d.clientX = pt.x; d.clientY = pt.y;
            for (int i = 0; i < count; i++) {
                int len = WideCharToMultiByte(CP_UTF8, 0, paths[i], -1, nullptr, 0, nullptr, nullptr);
                if (len > 1) {
                    std::string p(len - 1, '\0');
                    WideCharToMultiByte(CP_UTF8, 0, paths[i], -1, p.data(), len, nullptr, nullptr);
                    d.paths.push_back(std::move(p));
                }
            }
            if (!d.paths.empty()) g_pendingDrops.push_back(std::move(d));
        }
        return 0;
    }
    return DefWindowProcW(h, m, w, l);
}

// --------------- the timeline surface ---------------
static const ImU32 COL_BG       = IM_COL32(16, 18, 22, 255);
static const ImU32 COL_LANE     = IM_COL32(24, 27, 33, 255);
// Round 4, item 2: the ruler is DARK like the rest of the timeline - NOT a gray
// band. Jordan, comparing to becky-review-buttons-correct.JPG: "you've made the
// entire thing gray ... the timeline is divided from the buttons by a THIN GRAY
// LINE". Measured off that reference: the toolbar->timeline divider is one ~#3E3F41
// hairline; the ruler/track below it are dark; the tick labels are LIGHT gray so
// they read on the dark ruler (this reverses round 3's gray #676767 band, which is
// exactly the "entire thing gray" he rejected - newest request wins).
static const ImU32 COL_TLDIVIDER = IM_COL32(62, 63, 65, 255);   // #3E3F41 toolbar|timeline hairline
static const ImU32 COL_RULERTX  = IM_COL32(214, 216, 222, 255); // BRIGHT labels on the dark ruler (round 5: was too dim/small)
static const ImU32 COL_TICK     = IM_COL32(120, 122, 128, 255); // major tick, clearly visible on dark
static const ImU32 COL_TICKMIN  = IM_COL32(64, 66, 72, 255);    // minor tick, dim
static const ImU32 COL_CLIP     = IM_COL32(38, 56, 84, 255);
static const ImU32 COL_CLIPBRD  = IM_COL32(255, 255, 255, 70);
static const ImU32 COL_WAVE     = IM_COL32(255, 255, 255, 128);
static const ImU32 COL_WAVEDIM  = IM_COL32(255, 255, 255, 60);
static const ImU32 COL_PLAYHEAD = IM_COL32(0, 0, 0, 255);
static const ImU32 COL_PHFLAG   = IM_COL32(255, 255, 255, 255);
static const ImU32 COL_PHGRIP   = IM_COL32(58, 58, 58, 255);
static const ImU32 COL_DROPMARK = IM_COL32(255, 210, 0, 255);
static const ImU32 COL_LABEL    = IM_COL32(235, 238, 245, 235);
static const ImU32 COL_PIP      = IM_COL32(0, 160, 96, 255);
static const ImU32 COL_THRBAR   = IM_COL32(255, 120, 70, 235);
// Caption lane - deliberately AMBER so it reads as a different kind of thing from
// the blue clip lane at a glance. High contrast on purpose (accessibility aid).
static const ImU32 COL_CAPLANE  = IM_COL32(28, 24, 18, 255);
static const ImU32 COL_CAP      = IM_COL32(96, 68, 16, 255);
static const ImU32 COL_CAPSEL   = IM_COL32(168, 118, 20, 255);
static const ImU32 COL_CAPBRD   = IM_COL32(255, 190, 60, 255);
static const ImU32 COL_CAPTX    = IM_COL32(255, 240, 208, 255);
static const ImU32 COL_CAPCUT   = IM_COL32(255, 255, 255, 46);
// Item 3 root cause (round 2): detection AND the seamless skip during playback
// were BOTH already correct - proven live with a synthetic loud/silence/loud
// clip (the skip landed exactly on the silent span, confirmed by playhead
// position vs elapsed wall time). Jordan, live, on the crimson experiment:
// the plain semi-transparent black is CORRECT and reads fine, precisely
// because it sits on top of already-colourful clips - reverted to the
// original.
static const ImU32 COL_QUIETDIM = IM_COL32(0, 0, 0, 110);

// Jordan's screenshot showed "0:08.5" printed twice in a row on the ruler, then
// the whole label sequence one tick off. Root cause: this used to do
// `int d = (int)((s - t) * 10)` - TRUNCATING the decisecond digit. A tick that
// should read 8.6 but is actually the double 8.599999999996 (either from the
// ruler loop's accumulated `s += step` drift, or just because 0.1 has no exact
// binary representation) truncates to d=5, printing "0:08.5" again instead of
// "0:08.6". Rounding the whole decisecond count as ONE integer - with carry,
// so 59.96 correctly becomes 1:00.0 instead of 0:59.9 - makes the label match
// whatever tick is actually nearest, every time.
static void fmtTime(double s, char* out, size_t n, bool subSec) {
    if (s < 0) s = 0;
    if (subSec) {
        long long deci = std::llround(s * 10.0), t = deci / 10, d = deci % 10;
        int h = (int)(t / 3600), m = (int)((t % 3600) / 60), sec = (int)(t % 60);
        if (h) snprintf(out, n, "%d:%02d:%02d.%lld", h, m, sec, d);
        else snprintf(out, n, "%d:%02d.%lld", m, sec, d);
    } else {
        long long t = std::llround(s);
        int h = (int)(t / 3600), m = (int)((t % 3600) / 60), sec = (int)(t % 60);
        if (h) snprintf(out, n, "%d:%02d:%02d", h, m, sec);
        else snprintf(out, n, "%d:%02d", m, sec);
    }
}
static double rulerStep(double pps) {
    static const double steps[] = { 0.1, 0.2, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1800, 3600 };
    for (double s : steps) if (s * pps >= 70) return s;
    return 7200;
}
static void drawWave(ImDrawList* dl, const std::string& source, double cin, double cout,
                     float clipX0, float wx0, float wx1, float wy0, float wy1, double pps, ImU32 col) {
    auto pk = peaksGet(source);
    if (!pk || pk->failed) return;
    bool missed = false;
    {
        std::lock_guard<std::mutex> lk(pk->mx);
        if (!pk->ready) return;
        float mid = (wy0 + wy1) * 0.5f, half = (wy1 - wy0) * 0.5f - 1.0f;
        if (half < 2) return;
        int x0 = (int)std::floor(wx0), x1 = (int)std::ceil(wx1);
        for (int x = x0; x < x1; x++) {
            double s0 = cin + (x - clipX0) / pps, s1 = s0 + 1.0 / pps;
            if (s1 <= cin) continue;
            if (s0 >= cout) break;
            s0 = std::max(s0, cin); s1 = std::min(s1, cout);
            long long b0 = (long long)(s0 * kBinsPerSec), b1 = (long long)(s1 * kBinsPerSec) + 1;
            b0 = std::max(0LL, b0); b1 = std::min((long long)pk->bins, b1);
            if (b1 <= b0) continue;
            long long span = b1 - b0;
            int mn = 127, mx = -128;
            if (span >= 4096 && !pk->n2.empty()) {
                long long c0 = b0 >> 8, c1 = std::min((long long)pk->n2.size(), (b1 >> 8) + 1);
                for (long long i = c0; i < c1; i++) { mn = std::min(mn, (int)pk->n2[i]); mx = std::max(mx, (int)pk->x2[i]); }
            } else if (span >= 256 && !pk->n1.empty()) {
                long long c0 = b0 >> 4, c1 = std::min((long long)pk->n1.size(), (b1 >> 4) + 1);
                for (long long i = c0; i < c1; i++) { mn = std::min(mn, (int)pk->n1[i]); mx = std::max(mx, (int)pk->x1[i]); }
            } else {
                for (long long i = b0; i < b1; i++) { mn = std::min(mn, (int)pk->n0[i]); mx = std::max(mx, (int)pk->x0[i]); }
            }
            if (mn > mx) { missed = true; continue; }
            float yTop = mid - (mx / 127.0f) * half;
            float yBot = mid - (mn / 127.0f) * half;
            if (yBot - yTop < 1.0f) { yTop = mid - 0.5f; yBot = mid + 0.5f; }
            dl->AddLine(ImVec2((float)x, yTop), ImVec2((float)x, yBot), col);
        }
        if (missed && nowSec() - pk->lastMissReq < 1.0) missed = false;
        if (missed) pk->lastMissReq = nowSec();
    }
    if (missed) peaksRequest(source, cin - 1.0, cout + 5.0);
}
static bool clipPreparing(const Clip& c) {
    if (!c.ready) return true;
    auto pk = peaksGet(c.source);
    if (!pk) return true;
    if (pk->failed) return false;
    std::lock_guard<std::mutex> lk(pk->mx);
    if (!pk->ready) return true;
    long long s0 = (long long)std::ceil(c.in), s1 = (long long)std::floor(c.out) - 1;
    for (long long s = s0; s <= s1 && s >= 0 && s < (long long)pk->secFilled.size(); s++)
        if (!pk->secFilled[(size_t)s]) return true;
    return false;
}
static double snapComp(double t, double pps, double curSec, int exclIdx, float px = 8.0f) {
    double best = t, bestD = px / pps;
    auto try_ = [&](double e) { double d = std::abs(e - t); if (d < bestD) { bestD = d; best = e; } };
    for (size_t i = 0; i < g_track[0].size(); i++) {
        if ((int)i == exclIdx) continue;
        try_(g_track[0][i].compStart);
        try_(g_track[0][i].compStart + (g_track[0][i].out - g_track[0][i].in));
    }
    try_(curSec);
    return best;
}

// --------------- the reel's FRAME GRID ---------------
// Every caption edge this lane writes lands on a whole FRAME at the reel's real
// rate. That is not pedantry: Jordan's footage is true NTSC, 30000/1001 =
// 29.97002997 fps, so a frame is 33.3667ms - NOT a whole number of milliseconds.
// Anything that quietly assumes 30, or rounds to the millisecond, drifts off the
// cut points the captions were snapped to, and the drift compounds along a
// 150-second reel. The cut points from the Vegas/FCP7 edit are ground truth; a
// caption edge sitting between two frames cannot be rendered, so we never make one.
//
// The rate comes from the EDIT itself (ClipView.source_fps, set by the importer
// from the edit's own <rate>), and only falls back to the async ffprobe - never
// to a hardcoded constant.
static double reelFps() {
    for (auto& c : g_track[0]) if (c.srcFps > 1.0) return c.srcFps;
    if (!g_track[0].empty()) return sourceFps(g_track[0][0].source);
    return 30.0;
}
static double quantToFrame(double t) {
    if (t < 0) return 0;
    double fps = reelFps();
    if (fps <= 1.0) return t;
    return (double)std::llround(t * fps) / fps;
}

// --------------- CAPTION TRACK: the .srt sitting beside the loaded reel ---------------
// becky-subtitle (becky-go/cmd/subtitle) writes "<reel name>.srt" next to the reel
// with every caption snapped to the edit's cut points. This lane loads THAT file so
// a wrong word can be retyped and a late caption dragged back onto its cut, then
// writes it straight back.
//
// SRT is parsed/written here rather than through an engine verb on purpose: the
// engine's write_srt REGENERATES captions from the clip transcripts (app.go
// WriteSRTOnly -> edl.WriteSRT), so routing a hand edit through it would throw the
// edit away. The format is four lines per cue - nothing here needs the engine.
struct Caption { double start = 0, end = 0; std::string text; };
static std::vector<Caption> g_caps;
static std::string g_capPath;        // the .srt on disk; "" = no reel loaded, lane hidden
// A-1 (Jordan: "i don't see captions"): captions no longer require a saved reel
// sidecar. Every timeline clip whose SOURCE video has a transcript (.srt beside
// the source, e.g. E:\TakingBack2007\<video>.srt) shows its captions
// automatically: the engine's transcript verb (the SAME parser the transcript
// view and search already trust) is fetched once per source, cached here, and
// each clip's cues are mapped through the clip's in/out offsets onto the
// timeline. Cue times stay VERBATIM from the .srt - the only arithmetic is the
// clip's own offset, and a cue straddling a cut is clamped to the clip so it
// never paints over the neighbouring clip. A reel-stem sidecar .srt, when
// present, still OVERRIDES all of this (it is the hand-edited, cut-snapped
// artifact becky-subtitle wrote).
static bool g_capSidecar = false;    // a real sidecar .srt was loaded; derived captions stand down
static std::map<std::string, std::vector<Caption>> g_srcCues;  // source basename -> source-time cues
static std::set<std::string> g_srcCuesInFlight;                // async transcript fetches running
static std::string g_capErr;         // plain-language load/save problem, shown in the lane
static int  g_capSel = -1;           // selected caption (white border)
static int  g_capEdit = -1;          // caption whose text is being typed, -1 = none
static char g_capEditBuf[1024] = { 0 };
static bool g_capEditFocus = false;  // one-shot: put the keyboard in the box next frame

// ONE vertical placement for the whole reel - Jordan: "Simply dragging a caption up
// or down should affect all captions vertical placement. horzontal placement is fine
// how it is (centered)". So: no per-caption position, and no horizontal control at all.
//
// The number is becky-subtitle's MarginV (internal/subs/style.go) - the distance up
// from the bottom edge, in the 384x288 canvas ffmpeg's SRT-to-ASS conversion uses.
// 90 of 288 is the shipped default, i.e. about 30% up from the bottom.
static const int  CAP_ASS_H = 288;         // ff_ass_subtitle_header_default PlayResY
static const int  CAP_ASS_W = 384;         // ...and PlayResX
static int    g_capMarginV = 90;           // subs.DefaultStyle().MarginV
static bool   g_capMarginDrag = false;     // a vertical drag is live over the video pane
static int    g_capMarginAtGrab = 90;
static double g_capMarginGrabY = 0;
static double g_capMarginUnitsPerPx = 1.0; // screen pixels -> MarginV units, set at grab

// "00:01:02,500" (or with a '.') -> 62.5 seconds. Returns -1 if it is not a timestamp.
static double srtTimeToSec(std::string s) {
    for (auto& ch : s) if (ch == ',') ch = '.';
    int h = 0, m = 0, sec = 0, ms = 0;
    if (sscanf(s.c_str(), "%d:%d:%d.%d", &h, &m, &sec, &ms) < 3) return -1;
    return h * 3600.0 + m * 60.0 + sec + ms / 1000.0;
}
static std::string secToSrtTime(double t) {
    if (t < 0) t = 0;
    long long ms = (long long)(t * 1000.0 + 0.5);
    char b[32];
    snprintf(b, sizeof b, "%02lld:%02lld:%02lld,%03lld",
             ms / 3600000, (ms / 60000) % 60, (ms / 1000) % 60, ms % 1000);
    return b;
}
static void capTrimRight(std::string& s) {
    while (!s.empty() && (s.back() == '\r' || s.back() == '\n' || s.back() == ' ' || s.back() == '\t')) s.pop_back();
}

// loadCaptions points the lane at "<reel stem>.srt". Both "<name>.json" and
// "<name>.reel.json" are in circulation as reel files (Jordan's Vegas-import
// reels are the latter) and becky-subtitle always writes plain "<name>.srt" -
// so both stems are tried, same as the engine's reelCaptions() in
// cmd/clip/export.go. Trying only the reel's own stem is the exact regression
// that made a real, present .srt read as "no captions yet" for a *.reel.json
// reel: stripping one extension off "post_constantly.reel.json" leaves
// "post_constantly.reel", and "post_constantly.reel.srt" never existed - the
// actual file beside it is "post_constantly.srt". A missing file is NOT an
// error (the reel simply has not been captioned yet) - the lane still appears
// and says so, which is how Jordan finds out he needs to run becky-subtitle.
static void loadCapStyle();   // defined just below - needs g_capPath, which this sets
static void loadCaptions(const std::string& reelPath) {
    g_caps.clear(); g_capErr.clear(); g_capPath.clear();
    g_capSel = -1; g_capEdit = -1; g_capEditFocus = false;
    g_capSidecar = false;
    if (reelPath.empty()) return;
    std::string p = reelPath; fwslash(p);
    size_t dot = p.find_last_of('.'), slash = p.find_last_of('/');
    if (dot != std::string::npos && (slash == std::string::npos || dot > slash)) p = p.substr(0, dot);
    std::vector<std::string> stems = { p };
    const std::string reelSuffix = ".reel";
    if (p.size() > reelSuffix.size() && p.compare(p.size() - reelSuffix.size(), reelSuffix.size(), reelSuffix) == 0)
        stems.push_back(p.substr(0, p.size() - reelSuffix.size()));
    std::string p_found;
    for (auto& s : stems) {
        std::string cand = s + ".srt";
        std::ifstream test(cand);
        if (test.good()) { p_found = cand; break; }
    }
    p = p_found.empty() ? (stems[0] + ".srt") : p_found;
    g_capPath = p;
    loadCapStyle();                        // this reel's saved vertical placement
    std::ifstream f(p);
    if (!f.good()) {
        // A-1: no sidecar is NOT "no captions" any more - derive the lane from
        // each clip's own source transcript instead (async; appears per source).
        rebuildDerivedCaptions();
        return;
    }
    Caption cur; bool haveTime = false;
    auto flush = [&]() {
        capTrimRight(cur.text);
        if (haveTime && cur.end > cur.start) g_caps.push_back(cur);
        cur = Caption{}; haveTime = false;
    };
    std::string line;
    while (std::getline(f, line)) {
        capTrimRight(line);
        size_t arrow = line.find("-->");
        if (arrow != std::string::npos) {
            flush();
            double a = srtTimeToSec(line.substr(0, arrow));
            double b = srtTimeToSec(line.substr(arrow + 3));
            if (a >= 0 && b > a) { cur.start = a; cur.end = b; haveTime = true; }
        } else if (line.empty()) {
            flush();                       // blank line closes the cue
        } else if (haveTime) {
            if (!cur.text.empty()) cur.text += "\n";
            cur.text += line;              // keep the wrap; only an EDITED cue collapses to one line
        }
        // a line before any "-->" is the cue number - ignored on purpose
    }
    flush();                               // a file with no trailing blank line still yields its last cue
    // A-1: a sidecar that parsed to real cues is the hand-edited truth and wins;
    // an empty/garbled one falls back to the derived per-source captions.
    g_capSidecar = !g_caps.empty();
    if (!g_capSidecar) rebuildDerivedCaptions();
}

// Item 8 (round 3): CLI-CUT captions - becky-subtitle.exe (becky-go/cmd/subtitle),
// NOT the per-clip Parakeet transcript loadCaptions already falls back to above.
// Jordan: the raw forensic transcript is too limited for real time-appropriate
// TikTok captions - becky-subtitle snaps caption boundaries to the actual cut
// points and (by default) has a free-model pass regroup lines onto phrase
// breaks, which is the actual CLI-CUT look. It needs a reel.json on disk, so the
// button first asks the engine to save the CURRENT reel (the same save_reel verb
// the Save button already uses), then shells out to becky-subtitle --reel
// <path>, and on success calls loadCaptions(reelPath) - which ALREADY knows
// becky-subtitle's "<reel stem>.srt" naming convention (see its own comment
// above), so no srt path needs to be threaded back through here at all.
// Same async shape as engineCallAsync (thread -> g_asyncQ -> drainAsync on the
// UI thread), reused directly since this is a plain external exe, not an
// engine verb - AsyncReply doesn't care which one produced its json.
static std::atomic<bool> g_cliCutBusy{ false };
// Item 27: set when the video-row "Get Captions" put a whole video on the timeline and now
// wants captions built for it - consumed once the add_external reply lands (drainAsync).
static bool g_getCaptionsAfterAdd = false;
static void runCliCutCaptions(const std::string& reelPath) {
    beginWork("Building CLI-CUT captions (becky-subtitle)...");
    std::thread([reelPath]() {
        t_threadTag = "cliCutSubtitle";
        json result;
        std::string exe = "X:/AI-2/becky-tools/becky-go/bin/becky-subtitle.exe";
        if (!std::ifstream(exe)) {
            result = { {"ok", false}, {"error", "becky-subtitle.exe not found - run build-all-tools.bat"} };
        } else {
            // ITEM 15 (2026-07-24): the LLM review pass is what makes the captions usable
            // (Jordan: "we need llm review because those captions are not usable"). It now
            // routes through his OpenCode Zen account (hy3) in ONE shot and falls back to the
            // deterministic captions if the LLM fails - so it no longer hangs on the dead
            // OpenRouter free models. We ALSO pass --out = the EXACT .srt path loadCaptions()
            // reads (reel path with its last extension stripped, + .srt) so the fresh file is
            // the one the app picks up, and delete any stale sidecar first so a previous run's
            // captions can never win (the old "loads the filler/demo transcript" symptom).
            std::string srtOut = reelPath;
            {
                size_t dot = srtOut.find_last_of('.'), slash = srtOut.find_last_of("/\\");
                if (dot != std::string::npos && (slash == std::string::npos || dot > slash)) srtOut = srtOut.substr(0, dot);
                const std::string rs = ".reel";
                if (srtOut.size() > rs.size() && srtOut.compare(srtOut.size() - rs.size(), rs.size(), rs) == 0)
                    std::remove((srtOut.substr(0, srtOut.size() - rs.size()) + ".srt").c_str());   // the .reel-stripped stale one
                srtOut += ".srt";
                std::remove(srtOut.c_str());                                                       // and this one
            }
            // --review-model haiku: route the regroup pass through Jordan's Claude Max session
            // (OAuth, $0, ~1 min ONE-SHOT). hy3 was removed from OpenCode Zen and its free
            // deepseek replacement is far too slow (6+ min) for this button; Claude Max is free
            // per his rules, fast, and higher quality. If it fails, becky-subtitle falls back
            // to the (now cut-snapped, ?/!-breaking) deterministic captions.
            std::string cmd = "\"" + exe + "\" --reel \"" + reelPath + "\" --review-model haiku --out \"" + srtOut + "\"";
            // Pass the reel's real frame rate so captions SNAP to whole frames (else it warns
            // "no frame rate known ... pass --fps"). reelFps() = the edit's own rate (29.97).
            double fps = reelFps();
            if (fps > 1.0) { char fbuf[48]; snprintf(fbuf, sizeof fbuf, " --fps %.6f", fps); cmd += fbuf; }
            std::string out;
            // 420s covers the one-shot Claude Max review (~1-2 min typical) plus a possible
            // one-time re-transcribe of a source with no word-level sidecar. On LLM failure
            // becky-subtitle falls back to the deterministic captions, which are now cut-snapped
            // and break at ?/! - a usable result either way.
            bool ran = runPipeCapture(cmd, 420.0, [&](const uint8_t* d, size_t n) { out.append((const char*)d, n); });
            bool haveReport = false;
            try { if (ran && !out.empty()) { json rep = json::parse(out); haveReport = rep.contains("srt"); } } catch (...) {}
            result = haveReport ? json{ {"ok", true} }
                                 : json{ {"ok", false}, {"error", "becky-subtitle did not report an .srt - run it by hand on this reel to see why"} };
        }
        endWork();
        std::lock_guard<std::mutex> lk(g_asyncMx);
        g_asyncQ.push_back(AsyncReply{ result, [reelPath](const json& r) {
            g_cliCutBusy.store(false);
            if (r.value("ok", false)) {
                loadCaptions(reelPath);
                g_capsOn = true;
                g_renderMsg = "CLI-CUT captions built and loaded";
            } else {
                g_renderMsg = "CLI-CUT captions failed: " + r.value("error", std::string("?"));
            }
            g_renderMsgAt = nowSec();
        } });
    }).detach();
}

// "get captions" (the toolbar button + both right-click menus, items 16/27): save the
// CURRENT reel, then build real TikTok-style captions for it with becky-subtitle. Extracted
// so the toolbar button and the clip/timeline context menu all run the identical pipeline.
static void triggerGetCaptions() {
    if (g_cliCutBusy.load() || g_track[0].empty()) return;
    g_cliCutBusy.store(true);
    engineCallAsync("save_reel", { {"path", ""} }, 20.0, "Saving reel for captions...", [](const json& r) {
        if (r.value("ok", false)) {
            std::string path = r.value("data", json::object()).value("path", std::string());
            if (!path.empty()) runCliCutCaptions(path);
            else { g_cliCutBusy.store(false); g_renderMsg = "get captions failed: save_reel returned no path"; g_renderMsgAt = nowSec(); }
        } else {
            g_cliCutBusy.store(false);
            g_renderMsg = "get captions failed: could not save reel: " + r.value("error", std::string("?"));
            g_renderMsgAt = nowSec();
        }
    });
}

// The vertical placement is PER REEL, and deliberately so - Jordan: "the default
// setting is correct MOST of the time...but it depends on how the speaker is
// sitting". It lives beside the .srt as "<stem>.capstyle.json" so the burn-in can
// be handed the SAME number the reviewer set (becky-subtitle --margin-v N).
static std::string capStylePath() {
    if (g_capPath.empty()) return "";
    std::string p = g_capPath;
    size_t dot = p.find_last_of('.'), slash = p.find_last_of('/');
    if (dot != std::string::npos && (slash == std::string::npos || dot > slash)) p = p.substr(0, dot);
    return p + ".capstyle.json";
}
static void loadCapStyle() {
    g_capMarginV = 90;
    std::string p = capStylePath();
    if (p.empty()) return;
    std::ifstream f(p);
    if (!f.good()) return;                 // never set = the shipped default, not an error
    try {
        json j; f >> j;
        int m = j.value("margin_v", 90);
        if (m >= 0 && m <= CAP_ASS_H - 20) g_capMarginV = m;
    } catch (...) { /* a corrupt sidecar just means the default placement */ }
}
static void saveCapStyle() {
    std::string p = capStylePath();
    if (p.empty()) return;
    std::ofstream f(p, std::ios::binary | std::ios::trunc);
    if (!f.good()) { g_capErr = "could not save caption placement to " + p; return; }
    f << "{\"margin_v\": " << g_capMarginV << "}\n";
}

// saveCaptions rewrites the whole .srt in time order after any edit. SRT is
// conventionally time-ordered and a drag can reorder cues, so it sorts - and then
// repairs g_capSel so the white border stays on the caption the user is holding.
static void saveCaptions() {
    // A-1: derived captions with no reel loaded have nowhere to live on disk.
    // The edit still shows this session; tell him why it won't survive a restart.
    if (g_capPath.empty()) { g_capErr = "save a reel first - caption edits need a reel to live beside"; return; }
    Caption keep; bool haveKeep = false;
    if (g_capSel >= 0 && g_capSel < (int)g_caps.size()) { keep = g_caps[g_capSel]; haveKeep = true; }
    std::sort(g_caps.begin(), g_caps.end(),
              [](const Caption& a, const Caption& b) { return a.start < b.start; });
    if (haveKeep) {
        g_capSel = -1;
        for (size_t i = 0; i < g_caps.size(); i++)
            if (g_caps[i].start == keep.start && g_caps[i].end == keep.end && g_caps[i].text == keep.text) { g_capSel = (int)i; break; }
    }
    std::ofstream f(g_capPath, std::ios::binary | std::ios::trunc);
    if (!f.good()) { g_capErr = "could not save captions to " + g_capPath; return; }
    for (size_t i = 0; i < g_caps.size(); i++)
        f << (i + 1) << "\r\n"
          << secToSrtTime(g_caps[i].start) << " --> " << secToSrtTime(g_caps[i].end) << "\r\n"
          << g_caps[i].text << "\r\n\r\n";
    g_capErr.clear();
    // Editing a DERIVED caption materialises the whole set into the reel's
    // sidecar - from here on the sidecar is the truth (same as a becky-subtitle
    // run), so a later timeline reload must not clobber the hand edit.
    g_capSidecar = true;
}

// A-1: build the caption lane from each clip's own source transcript. Runs on
// the UI thread only (all callers are UI-thread: loadTimelineView/seekToSpan
// directly, the transcript fetch via drainAsync) - so no locking here.
// Transcripts arrive asynchronously; each arrival re-runs this, so captions
// appear per source as its transcript lands, never blocking a frame.
static void rebuildDerivedCaptions() {
    if (g_capSidecar) return;                 // the hand-edited sidecar wins
    g_caps.clear();
    g_capSel = -1; g_capEdit = -1; g_capEditFocus = false;
    bool waiting = false;
    for (auto& c : g_track[0]) {
        std::string name = baseName(c.source);
        auto it = g_srcCues.find(name);
        if (it == g_srcCues.end()) {
            if (!g_srcCuesInFlight.count(name)) {
                g_srcCuesInFlight.insert(name);
                engineCallAsync("transcript", { {"name", name} }, 25.0, "loading captions",
                    [name](const json& r) {
                        g_srcCuesInFlight.erase(name);
                        if (!r.value("ok", false)) {
                            // NOT cached: the usual cause is boot ordering - the
                            // forensic launcher loads the reel BEFORE open_folder
                            // indexes the case folder, so the first transcript ask
                            // lands on an engine that hasn't met the video yet.
                            // Retry (bounded) until the index exists; only a real
                            // answer is worth remembering.
                            static std::map<std::string, int> retries;
                            if (++retries[name] > 8) g_srcCues[name] = {};   // give up this session
                            rebuildDerivedCaptions();
                            return;
                        }
                        std::vector<Caption> cues;
                        if (r.contains("data") && r["data"].is_array())
                            for (auto& q : r["data"]) {
                                Caption cp; cp.start = q.value("start", 0.0); cp.end = q.value("end", 0.0);
                                cp.text = q.value("text", std::string());
                                if (cp.end > cp.start && !cp.text.empty()) cues.push_back(cp);
                            }
                        // an empty ok-list is cached too - "this source has no
                        // transcript" is an answer, asked exactly once
                        g_srcCues[name] = std::move(cues);
                        rebuildDerivedCaptions();
                    });
            }
            waiting = true;
            continue;
        }
        for (auto& q : it->second) {
            if (q.end <= c.in || q.start >= c.out) continue;   // cue outside this clip's span
            Caption cp;
            cp.start = c.compStart + (std::max(q.start, c.in) - c.in);
            cp.end   = c.compStart + (std::min(q.end,   c.out) - c.in);
            cp.text = q.text;
            if (cp.end > cp.start) g_caps.push_back(cp);
        }
    }
    std::sort(g_caps.begin(), g_caps.end(),
              [](const Caption& a, const Caption& b) { return a.start < b.start; });
    if (!g_caps.empty()) g_capErr.clear();
    else if (!waiting && !g_track[0].empty())
        g_capErr = "no captions - no transcript found beside these clips' source videos";
}

// The caption under the playhead, drawn ON the video at the placement the burn-in
// will use - so the thing Jordan drags is the thing he gets. Step 6 draws these
// straight onto the pane with ImGui, in the same swap chain as the video texture
// (no child hwnd, no OSD round-trip needed - that was the pre-step-6 mpv approach).
//
// The ASS canvas is still declared 384x288 because that is the PlayRes ffmpeg's
// SRT-to-ASS conversion uses (ff_ass_subtitle_header_default) - which makes MarginV,
// FontSize and Outline mean the SAME thing here as in becky-subtitle's force_style,
// rather than an eyeballed lookalike, and g_capMarginV means exactly what it meant
// under the old mpv OSD: one canvas unit maps to paneH/288 pixels, fs12 maps to
// 12*paneH/288 pixels. For footage that fills the pane vertically (portrait clips in
// this wide pane - the normal case) the preview height is exact. Letterboxed footage
// (source WIDER than the pane) would sit slightly low, since the canvas then spans
// the black bars too.
static bool g_capOsdShowing = false;
static void drawCaptionsImGui(double t, ImVec2 origin, ImVec2 size) {
    if (g_caps.empty()) { g_capOsdShowing = false; return; }
    const Caption* cur = nullptr;
    for (auto& c : g_caps) if (t >= c.start && t < c.end) { cur = &c; break; }
    // Mid-drag there must always be a caption on screen to judge placement by.
    if (!cur && g_capMarginDrag) {
        double best = 1e18;
        for (auto& c : g_caps) {
            double d = t < c.start ? c.start - t : (t > c.end ? t - c.end : 0);
            if (d < best) { best = d; cur = &c; }
        }
    }
    if (!cur) { g_capOsdShowing = false; return; }
    ImDrawList* dl = ImGui::GetWindowDrawList();
    float unit = size.y / (float)CAP_ASS_H;   // one ASS-canvas unit in pane pixels
    float fs = 12.0f * unit;
    if (fs < 9.0f) fs = 9.0f;
    // split cue into lines
    std::vector<std::string> lines;
    { std::string line;
      for (char ch : cur->text) {
          if (ch == '\n') { lines.push_back(line); line.clear(); }
          else if (ch != '\r') line += ch;
      }
      lines.push_back(line); }
    float y = origin.y + size.y - (float)g_capMarginV * unit - fs * 1.15f * (float)lines.size();
    for (auto& ln : lines) {
        ImVec2 ts = ImGui::GetFont()->CalcTextSizeA(fs, FLT_MAX, 0, ln.c_str());
        imguiOutlinedText(dl, ImVec2(origin.x + (size.x - ts.x) * 0.5f, y), fs, ln.c_str());
        y += fs * 1.15f;
    }
    g_capOsdShowing = true;
}

// Forward decls (defined later, with the library/panel state they need) so the
// timeline's right-click clip menu (E-14) can reach them.
static void openInFileBrowser(const std::string& path);
static void openTranscript(const std::string& fullVideoPath);

// ---- Ctrl+Left / Ctrl+Right: step to the previous/next EDIT POINT ----
//
// Jordan marked this CRITICAL and noted "we've tried to fix this several times".
// The reason it kept coming back is that the two directions were separate loops
// searching DIFFERENT things: Ctrl+Left scanned only clip STARTS (c.compStart)
// while Ctrl+Right scanned only clip ENDS, and BOTH looked at g_track[0] alone.
// So the playhead could not reach a boundary that existed only on track 1 or in
// the caption lane, the two directions disagreed about where the edit points
// were, and neither could land on 0 or the very end of the timeline - which is
// what "sticks at the clip edge" feels like in the hand.
//
// One list, built once, used by both directions. Every clip edge on EVERY track,
// plus the two ends of the timeline. Now Ctrl+Left and Ctrl+Right are exact
// inverses by construction, which is the property that was missing - not any
// single off-by-one.
//
// CAPTION edges are deliberately NOT in this list. The first version included
// them and I drove it: three Ctrl+Right presses advanced 0.8s, because 179
// captions subdivide the 88 clips. Stepping caption-by-caption makes crossing
// the timeline slower, which is the opposite of the complaint. Ctrl+arrow means
// CLIP edit points, the way Vegas does it.
//
// eps is one frame at 60fps: a boundary closer than that to the playhead is the
// one we are standing on, not one to jump to, so holding the key walks instead
// of sticking.
static void collectBoundaries(std::vector<double>& out) {
    out.clear();
    out.push_back(0.0);
    if (g_compDur > 0) out.push_back(g_compDur);
    for (int tr = 0; tr < 2; tr++)
        for (auto& c : g_track[tr]) {
            out.push_back(c.compStart);
            out.push_back(c.compStart + (c.out - c.in));
        }
    std::sort(out.begin(), out.end());
    out.erase(std::unique(out.begin(), out.end(),
                          [](double a, double b) { return std::fabs(a - b) < 1e-6; }),
              out.end());
}

static bool nextBoundary(double from, double& hit) {
    static std::vector<double> b; collectBoundaries(b);
    const double eps = 1.0 / 60.0;
    for (double t : b) if (t > from + eps) { hit = t; return true; }
    return false;
}

static bool prevBoundary(double from, double& hit) {
    static std::vector<double> b; collectBoundaries(b);
    const double eps = 1.0 / 60.0;
    for (auto it = b.rbegin(); it != b.rend(); ++it) if (*it < from - eps) { hit = *it; return true; }
    return false;
}

// Item 31: a CLOSED-HAND (grab) cursor for "I am moving something". ImGui/Win32 has no
// closed-hand cursor (IDC_HAND is the POINTING hand), so hide the OS cursor and hand-draw
// a small fist - palm + four curled knuckles + a thumb - on the foreground draw list at
// the pointer. White fill + dark outline so it reads on any timeline colour.
static void drawGrabCursor() {
    ImGui::SetMouseCursor(ImGuiMouseCursor_None);
    ImVec2 m = ImGui::GetMousePos();
    ImDrawList* dl = ImGui::GetForegroundDrawList();
    const ImU32 fill = IM_COL32(240, 240, 245, 255), line = IM_COL32(20, 20, 24, 255);
    const float s = 9.0f;
    ImVec2 a(m.x - s * 0.8f, m.y - s * 0.15f), b(m.x + s * 0.9f, m.y + s);
    dl->AddRectFilled(a, b, fill, s * 0.45f);                 // the fist body
    dl->AddRect(a, b, line, s * 0.45f, 0, 1.5f);
    for (int i = 0; i < 4; i++) {                             // four curled-finger knuckles
        float kx = a.x + (b.x - a.x) * (0.22f + i * 0.19f);
        dl->AddCircleFilled(ImVec2(kx, a.y), s * 0.26f, fill);
        dl->AddCircle(ImVec2(kx, a.y), s * 0.26f, line, 0, 1.2f);
    }
    dl->AddCircleFilled(ImVec2(a.x, m.y + s * 0.4f), s * 0.28f, fill);   // thumb
    dl->AddCircle(ImVec2(a.x, m.y + s * 0.4f), s * 0.28f, line, 0, 1.2f);
}

static void drawTimeline(double& curSec, bool& playing) {
    ImDrawList* dl = ImGui::GetWindowDrawList();
    ImVec2 p = ImGui::GetCursorScreenPos();
    float availW = ImGui::GetContentRegionAvail().x;
    float availH = ImGui::GetContentRegionAvail().y;
    if (availW < 16 || availH < 44) return;
    float tlX = p.x, tlW = availW;
    float rulerH = 24, sbH = 12, gap = 4;   // round 5b: 24px == the reference's .ruler height
    int lanes = 1;
    float lanesH = availH - rulerH - sbH - gap * 2;
    // The caption lane sits directly UNDER the clip lane and inside the same
    // InvisibleButton below, so one gesture handler drives both. With no reel
    // loaded (g_capPath empty) capH/capGap are 0 and the layout is byte-identical
    // to the pre-caption one.
    // A-1: derived captions (no sidecar, no reel) still get a lane - the lane
    // shows whenever there ARE captions or a reel is loaded to explain itself on.
    bool showCaps = g_capsOn && (!g_capPath.empty() || !g_caps.empty()) && lanesH > 90;
    float capH = showCaps ? 36.0f : 0.0f;
    float capGap = showCaps ? 4.0f : 0.0f;
    float laneH = lanesH - capH - capGap;
    if (laneH < 24) laneH = 24;
    float aY = p.y + rulerH + gap;
    float capY = aY + laneH + capGap;
    float bot = capY + capH;
    float sbY = bot + gap;

    dl->AddRectFilled(p, ImVec2(p.x + tlW, sbY + sbH), COL_BG);
    // Item 2 (round 4): NO gray ruler band. The whole timeline (ruler included) is
    // the dark COL_BG fill above; the toolbar is divided from it by ONE thin gray
    // hairline at the very top (the reference's only visible divider), and a fainter
    // dark rule under the ruler sets the timecodes off from the clips.
    dl->AddLine(ImVec2(p.x, p.y + 0.5f), ImVec2(p.x + tlW, p.y + 0.5f), COL_TLDIVIDER, 1.0f);
    dl->AddLine(ImVec2(p.x, p.y + rulerH), ImVec2(p.x + tlW, p.y + rulerH), IM_COL32(26, 26, 26, 255), 1.0f);

    ImGui::SetCursorScreenPos(p);
    ImGui::InvisibleButton("tl", ImVec2(tlW, bot - p.y));
    // Item 8 (round 2): this ONE giant button covers the whole timeline,
    // caption lane included, and is submitted before the caption-edit
    // InputText further down - so without this, every click meant to place
    // a caret or double-click-select a word inside an open caption edit box
    // was being claimed by "tl" first (a normal ImGui click-priority rule:
    // the button that's ALREADY submitted sees itself as hovered before a
    // later widget at the same position exists yet), and dispatched as a
    // timeline click/scrub instead - found live: a second click meant for
    // the caret moved the playhead and silently closed the edit box.
    // AllowOverlap is the same fix the library card's round "+" button
    // already uses for exactly this shape of problem: it lets a LATER
    // widget at an overlapping position still win hover/click priority.
    ImGui::SetItemAllowOverlap();
    bool hovered = ImGui::IsItemHovered();
    // NORMAL POINTER over the timeline. Jordan asked for the I-beam on 2026-06-30
    // (feedback1, replacing the hand) and then REVERSED that later - he wants the
    // ordinary arrow back. Newest instruction wins, so do not "restore" the I-beam
    // by citing the older feedback file. Leaving the cursor unset here means ImGui
    // keeps ImGuiMouseCursor_Arrow, which is exactly what he asked for.
    (void)hovered;
    // Item 1 fix (round 3): a preview audition swaps g_track[0] for a one-clip
    // (or tied-clips) reel WHILE THE REAL REEL IS FROZEN AND SHOWN INSTEAD (see the
    // drawTimeline call site, which swaps the real reel/duration/playhead back in
    // for this render).
    //
    // Round 5: clicking the timeline while an audition is playing is exactly the
    // gesture that means "I'm done previewing, take me back to the reel". Because the
    // frozen render already put the REAL reel into g_track[0] for this call, ending
    // the preview here (clear g_inTiedPreview) makes the very same click fall through
    // to the normal gesture below and select/seek on the real reel - one click both
    // exits the audition and acts. The call site notices g_inTiedPreview flipped and
    // keeps the reel instead of restoring the preview clip.
    if (ImGui::IsItemActivated() && g_inTiedPreview) g_inTiedPreview = false;
    bool pressed = ImGui::IsItemActivated() && !g_inTiedPreview;
    bool active = ImGui::IsItemActive() && !g_inTiedPreview;
    bool released = ImGui::IsItemDeactivated() && !g_inTiedPreview;
    ImGuiIO& io = ImGui::GetIO();
    float mx = io.MousePos.x, my = io.MousePos.y;

    auto xToSec = [&](float x) { return std::max(0.0, g_scrollSec + (x - tlX) / g_pps); };
    auto secToX = [&](double s) { return tlX + (float)((s - g_scrollSec) * g_pps); };

    // Item 8 (round 2): SetItemAllowOverlap (above) is not enough on its own -
    // "tl" still computes its OWN pressed/active/released every frame from its
    // OWN hover test, independent of whatever gets submitted later, so a click
    // meant for the open caption-edit InputText was still dispatched as a
    // capHit() body-drag gesture (confirmed live: mx/my landing squarely
    // inside the edit box still logged a capHit press, kind 8, and the box
    // closed instead of placing a caret). The InputText itself needs NO help
    // to receive the click and place the caret/select a word - that is stock
    // ImGui InputText behaviour - it only needs "tl" to not ALSO react to the
    // same click and stomp the edit box. Recompute the exact same edit-box
    // rect the render code below uses and suppress "tl"'s three flags when a
    // press/release lands inside it while a caption is being edited.
    if (g_capEdit >= 0 && g_capEdit < (int)g_caps.size()) {
        float cx0 = secToX(g_caps[g_capEdit].start), cx1 = secToX(g_caps[g_capEdit].end);
        float ecx0 = std::max(cx0, tlX), ecx1 = std::min(cx1, tlX + tlW);
        if (ecx1 - ecx0 < 220) ecx1 = std::min(tlX + tlW, ecx0 + 220);
        if (ecx1 - ecx0 < 80) { ecx0 = tlX; ecx1 = std::min(tlX + tlW, tlX + 220); }
        if (mx >= ecx0 && mx <= ecx1 && my >= capY && my <= capY + capH) {
            pressed = false; active = false; released = false;
        }
    }

    // E-13: drain any WM_DROPFILES drops queued this frame. Only a drop landing
    // ON the clip lane counts as a timeline drop (dropping elsewhere - e.g. onto
    // the ruler or library - is a no-op, matching the deliberate "engine add_external"
    // scope in BUILD_1.md). Each dropped file inserts at the drop position, in
    // drop order, same insertion-index math the multi-select drag reorder uses below.
    if (!g_pendingDrops.empty()) {
        static const std::set<std::string> kVideoExts = {
            ".mp4",".mov",".mkv",".avi",".m4v",".webm",".mpg",".mpeg",
            ".wmv",".flv",".ts",".mts",".m2ts",".3gp",".vob"
        };
        std::vector<PendingDrop> drops; drops.swap(g_pendingDrops);
        for (auto& d : drops) {
            if (d.clientY < aY || d.clientY > aY + laneH) continue;
            // A reel (.json) or an edit export (.txt Vegas EDL / .xml Final Cut) dropped
            // in loads as the WHOLE TIMELINE, same as the Load Reel button - same fix as
            // that button's filter (Jordan drags his Vegas export straight in). Takes
            // priority over the per-clip video-insert loop below and only the first such
            // file in the drop is used, matching the WPF app's OnWebDrop.
            bool loadedEdit = false;
            for (auto& path : d.paths) {
                if (!hasExtCI(path, ".json") && !hasExtCI(path, ".txt") && !hasExtCI(path, ".xml")) continue;
                std::string rp = convertEditIfNeeded(path);
                if (!rp.empty()) {
                    // cycle 18 review's THE ONE THING (item 2 of 2): this was still a
                    // synchronous 30s engineCall on the UI thread - dropping a reel/EDL
                    // onto the window froze exactly like the Load Reel button used to
                    // before cycle 18 (main.cpp:1055's comment). curSec/playing are
                    // drawTimeline's own reference params (bound to main()'s locals,
                    // alive for the process lifetime), so capturing them by reference is
                    // exactly as safe as the button fix.
                    engineCallAsync("load_reel", { {"path", rp} }, 30.0, "Loading reel...",
                                    [rp, &curSec, &playing](const json& r) {
                        if (r.value("ok", false)) {
                            loadTimelineView(r.contains("data") ? r["data"] : r);
                            // NB: no lastComposed reset here (unlike the Load Reel button) -
                            // that variable is local to main()'s loop, out of scope in
                            // drawTimeline; playing=false already makes main()'s own
                            // "if (!playing) lastComposed = -1" catch it next frame.
                            curSec = 0; playing = false; g_playingExt = false;
                            loadCaptions(rp); g_renderMsg = "Loaded reel " + baseName(rp);
                        } else g_renderMsg = "Load reel failed: " + r.value("error", std::string("?"));
                        g_renderMsgAt = nowSec();
                    });
                }
                loadedEdit = true;
                break;
            }
            if (loadedEdit) continue;
            double dropSec = xToSec((float)d.clientX);
            int to = 0;
            for (auto& c : g_track[0]) if (c.compStart + (c.out - c.in) / 2 < dropSec) to++;
            for (auto& path : d.paths) {
                std::string ext = path.substr(path.find_last_of('.') == std::string::npos ? path.size() : path.find_last_of('.'));
                std::transform(ext.begin(), ext.end(), ext.begin(), [](unsigned char c) { return (char)std::tolower(c); });
                if (!kVideoExts.count(ext)) continue; // not a video file - silently skip (degrade, never crash)
                requestAddExternal(path, to);
                to++; // subsequent files in the same drop insert after the previous one
            }
        }
    }

    float labelH = laneH > 46 ? 17.0f : 0.0f;
    // E-11: "clips 2x tall with the small fixed thumbnail kept out of the cut
    // area" - a small fixed-size thumbnail chip shares the header row with the
    // label, ABOVE the waveform band. thumbH is fixed (doesn't grow with laneH
    // like the old label-only header did) so it stays "small", and wy0 is
    // pushed down by whichever of the two is taller - the waveform (the "cut
    // area" zero-crossings live in) is never overlapped by the thumbnail, and
    // is drawn at its FULL clip width underneath, same as before.
    float thumbH = laneH > 70 ? 40.0f : 0.0f;
    float headerH = std::max(labelH, thumbH > 0 ? thumbH + 4 : 0.0f);
    float wy0 = aY + 2 + headerH, wy1 = aY + laneH - 2;
    float waveMid = (wy0 + wy1) * 0.5f, waveHalf = (wy1 - wy0) * 0.5f - 1.0f;
    drainThumbs(); // cheap (swaps a small deque under a lock) even when nothing finished this frame
    // (drainAsync used to be called HERE. It is not anymore - see main()'s drain
    // block. Delivering async replies from the MIDDLE of drawTimeline meant a
    // callback like add_clip's or apply_proposal's could replace g_track while
    // this function was halfway through reading it. It happened not to crash only
    // because no live reference to g_track survived across this exact line - a
    // property nobody could see, that any future edit above this point would have
    // silently broken. Model mutations now land with every other drain, BEFORE
    // the frame reads anything.)

    auto zoomTo = [&](double newPps, float anchorX) {
        double anchor = xToSec(anchorX);
        g_pps = std::min(2000.0, std::max(0.5, newPps));
        g_scrollSec = std::max(0.0, anchor - (anchorX - tlX) / g_pps);
        emitView();
    };
    auto zoomAnchorX = [&]() -> float {
        float phx = secToX(curSec);
        if (phx >= tlX && phx <= tlX + tlW) return phx;
        return hovered ? mx : tlX + tlW / 2;
    };
    auto applyWheel = [&](float notches, bool ctrl, float atX) {
        (void)atX;
        if (ctrl) { g_scrollSec = std::max(0.0, g_scrollSec + (-notches * 100.0) / g_pps); g_lastUserScroll = nowSec(); }
        else { zoomTo(g_pps * std::pow(1.15, (double)notches), zoomAnchorX()); }
    };
    if (hovered && io.MouseWheel != 0) applyWheel(io.MouseWheel, io.KeyCtrl, mx);
    // Keyboard zoom (item 48): same path as the wheel, so the two can never
    // disagree about anchoring or limits. Two notches per press - one is barely
    // perceptible and he would have to hammer the key.
    if (g_zoomReq != 0) { applyWheel((float)g_zoomReq * 2.0f, false, zoomAnchorX()); g_zoomReq = 0; }

    static bool s_midPan = false;
    if (hovered && ImGui::IsMouseClicked(ImGuiMouseButton_Middle)) { s_midPan = true; }
    if (s_midPan && ImGui::IsMouseDown(ImGuiMouseButton_Middle)) {
        if (io.MouseDelta.x != 0) { g_scrollSec = std::max(0.0, g_scrollSec - io.MouseDelta.x / g_pps); g_lastUserScroll = nowSec(); }
    } else s_midPan = false;

    bool playingNow = g_playingExt;
    double viewDur = tlW / g_pps;
    // FB6/E-6: once the stock has been manually placed, stop auto-following the live
    // playhead off-screen - the user is looking at the stock, not chasing playback.
    if (playingNow && g_gest.kind == 0 && g_stockSec < 0 && nowSec() - g_lastUserScroll > 1.5) {
        if (curSec < g_scrollSec || curSec > g_scrollSec + viewDur * 0.95)
            g_scrollSec = std::max(0.0, curSec - viewDur * 0.3);
    }
    double maxScroll = std::max(0.0, g_compDur - viewDur * 0.15);
    // A DELETE MUST NOT DRAG THE VIEW SIDEWAYS UNDER HIM (items 96/106, stated
    // twice). This clamp used to run unconditionally every frame, so the moment
    // an edit shortened the reel maxScroll dropped and the whole timeline slid
    // left while he was working in it — he loses his place mid-edit, which for
    // someone editing at speed is worse than the wasted pixels it was saving.
    //
    // Now it only intervenes when the view has scrolled past EVERYTHING and is
    // showing nothing at all, and never mid-gesture. Sitting slightly past the
    // end of a shortened reel is normal NLE behaviour; being teleported is not.
    if (g_gest.kind == 0 && g_scrollSec > g_compDur) {
        g_scrollSec = maxScroll;
    }

    const double kThrFloorDb = -50.0;
    float thrLaneTop = aY + 1, thrLaneBot = aY + laneH - 1;
    auto thrY = [&]() -> float {
        double db = g_thrLevel <= 0 ? kThrFloorDb
                                    : std::max(kThrFloorDb, std::min(0.0, 20.0 * std::log10(g_thrLevel)));
        double frac = (db - kThrFloorDb) / -kThrFloorDb;
        return thrLaneBot - (float)(frac * (thrLaneBot - thrLaneTop));
    };
    auto onThresholdBar = [&](float x, float y) {
        return g_thrOn && x >= tlX && x <= tlX + tlW && std::abs(y - thrY()) < 6;
    };

    auto clipHit = [&](float x, float y, int& idx, int& zone) {
        idx = -1; zone = 0;
        if (y < aY || y > aY + laneH) return false;
        for (size_t i = 0; i < g_track[0].size(); i++) {
            Clip& c = g_track[0][i];
            float x0 = secToX(c.compStart), x1 = secToX(c.compStart + (c.out - c.in));
            if (x < x0 || x > x1) continue;
            idx = (int)i;
            // Item 1 (round 2): the trim gesture itself was never gone (kind 4/5
            // below, set_trim on release) - it was just a 10px hairline, easy to
            // miss by a few pixels and land on "select the clip" or the neighbour's
            // edge instead (measured live: a drag that started 10px past the real
            // boundary silently grabbed the WRONG clip). Widened to 16px - still
            // capped at width/4 so a short clip keeps SOME body left to click, and
            // the width gate raised from 20 to 40 so two 16px zones on a tiny clip
            // can never swallow its whole body.
            float hw = std::min(16.0f, (x1 - x0) / 4);
            if ((x1 - x0) > 40 && x - x0 <= hw) zone = 4;
            else if ((x1 - x0) > 40 && x1 - x <= hw) zone = 5;
            else zone = 0;
            return true;
        }
        return false;
    };

    // Caption hit test - the same shape as clipHit above so captions behave like
    // clips: a body grab moves the whole cue, an edge grab retimes just that edge.
    // zone doubles as the gesture kind (8 body / 9 start edge / 10 end edge).
    auto capHit = [&](float x, float y, int& idx, int& zone) {
        idx = -1; zone = 0;
        if (!showCaps || y < capY || y > capY + capH) return false;
        for (size_t i = 0; i < g_caps.size(); i++) {
            float x0 = secToX(g_caps[i].start), x1 = secToX(g_caps[i].end);
            if (x < x0 || x > x1) continue;
            idx = (int)i;
            float hw = std::min(8.0f, (x1 - x0) / 4);
            if ((x1 - x0) > 18 && x - x0 <= hw) zone = 9;
            else if ((x1 - x0) > 18 && x1 - x <= hw) zone = 10;
            else zone = 8;
            return true;
        }
        return false;
    };
    // Captions snap to the reel's CUT POINTS by default (that is the whole reason
    // this lane exists - a caption that drifts off its cut is what made the old
    // burned-in output unreadable). Alt held = free positioning. snapComp already
    // walks every clip's start/end plus the playhead; -1 excludes no clip.
    //
    // The cut points come from the Vegas/FCP7 edit and are ground truth - already on
    // a frame - so quantToFrame is a no-op when a snap lands, and only bites in the
    // Alt/free case. Either way no caption edge is ever written between two frames.
    auto capSnapCut = [&](double t) {
        return io.KeyAlt ? t : snapComp(t, g_pps, curSec, -1, 12.0f);
    };

    // E-14: right-click a clip -> Open in File Browser / Copy File Name / Open transcript.
    static int s_ctxIdx = -1;
    if (hovered && ImGui::IsMouseClicked(ImGuiMouseButton_Right)) {
        int idx, zone;
        if (clipHit(mx, my, idx, zone)) { s_ctxIdx = idx; ImGui::OpenPopup("clipctx"); }
    }
    if (ImGui::BeginPopup("clipctx")) {
        if (s_ctxIdx >= 0 && s_ctxIdx < (int)g_track[0].size()) {
            Clip& c = g_track[0][s_ctxIdx];
            ImGui::TextDisabled("%s", c.label.c_str());
            ImGui::Separator();
            if (ImGui::MenuItem("Open in File Browser")) openInFileBrowser(c.source);
            if (ImGui::MenuItem("Copy File Name")) ImGui::SetClipboardText(baseName(c.source).c_str());
            if (ImGui::MenuItem("Open Transcript")) openTranscript(c.source);
            ImGui::Separator();
            // Item 27: build REAL TikTok-style captions (becky-subtitle: cut-snapped +
            // phrase-broken) for the whole timeline, not the raw Parakeet transcript.
            ImGui::BeginDisabled(g_cliCutBusy.load());
            if (ImGui::MenuItem("Get Captions")) triggerGetCaptions();
            ImGui::EndDisabled();
        }
        ImGui::EndPopup();
    }

    if (pressed) {
        // A real click on the real timeline means "back to the real timeline" -
        // drop any single-click cue/hit preview that might be showing (item B).
        clearScrubPreview();
        int idx, zone;
        g_gest = Gesture{};
        g_gest.pressX = mx; g_gest.ctrl = io.KeyCtrl; g_gest.shiftK = io.KeyShift;
        if (my < aY && std::abs(mx - secToX(curSec)) <= 10.0f) {
            // Item 8, corrected live: grabbing the PLAYHEAD HANDLE ITSELF must SCRUB
            // (drag = the frame follows the cursor), never pan - panning was eating
            // the one gesture an editor expects to work everywhere: drag the
            // playhead. Hit test is a little wider than the drawn flag (fw=8 in the
            // playhead draw block below) for an easy grab. Same mechanics as an
            // empty-track click-drag (kind 1): pauses, scrubs frame-exact.
            g_gest.kind = 1;
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            playing = false; g_playingExt = false;
            // Item 2: same stale-stock fix as the paused clip-click below - grabbing
            // the real playhead and dragging it must not leave an old stock flag
            // parked behind, or the visible playhead reads as "stuck" at two places.
            g_stockSec = -1; g_stockFlash = false;
            g_gest.gIn = curSec;
            emitScrub(curSec, false);
        } else if (my < aY) {
            // RULER BAND (items 28/29). Jordan reversed the earlier "drag pans" design:
            // a CLICK moves the PLAYHEAD there instantly, and DRAG SCRUBS the playhead -
            // panning is the MIDDLE mouse button's job (s_midPan above) and works great.
            // If the timeline is PLAYING, the click/drag SEEKS and KEEPS PLAYING from the
            // new spot (engine jumps, audio follows); paused, it is a frame-exact reposition.
            // (Grabbing the playhead HANDLE itself is kind 1 above and already works - "do
            // not break the playhead body".)
            g_gest.kind = 11;
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            g_gest.gIn = curSec;                      // drag throttle baseline (was the scroll pos)
            if (g_playingExt) { g_stockSec = curSec; engineReelSeek(curSec); }
            else { g_stockSec = -1; g_stockFlash = false; emitScrub(curSec, false); }
        } else if (onThresholdBar(mx, my)) {
            g_gest.kind = 7;
        } else if (clipHit(mx, my, idx, zone)) {
            g_gest.idx = idx;
            Clip& c = g_track[0][idx];
            if (zone == 4) { g_gest.kind = 4; g_gest.gIn = c.in; g_gest.gOut = c.out; }
            else if (zone == 5) { g_gest.kind = 5; g_gest.gIn = c.in; g_gest.gOut = c.out; }
            else {
                g_gest.kind = 2;
                if (g_sel.count(c.id) && g_sel.size() > 1)
                    for (size_t i = 0; i < g_track[0].size(); i++)
                        if (g_sel.count(g_track[0][i].id)) g_gest.group.push_back((int)i);
            }
        } else if (capHit(mx, my, idx, zone)) {
            g_gest.idx = idx; g_gest.kind = zone;            // 8 body / 9 start edge / 10 end edge
            g_gest.gIn = g_caps[idx].start; g_gest.gOut = g_caps[idx].end;
            g_gest.grabOff = xToSec(mx) - g_caps[idx].start; // so the cue does not jump to the cursor
            if (g_capEdit != idx) g_capEdit = -1;            // clicking another cue leaves the text box
            g_capSel = idx;
        } else {
            g_gest.kind = 1;
            curSec = std::min(xToSec(mx), g_compDur);
            playing = false; g_playingExt = false;
            // Item 2: same stale-stock fix - an empty-timeline click/scrub is also a
            // deliberate reposition while stopped.
            g_stockSec = -1; g_stockFlash = false;
            g_gest.gIn = curSec;
            emitScrub(curSec, false);
        }
    }

    if (active && g_gest.kind != 0) {
        if (g_gest.kind == 1) {
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            if (std::abs(curSec - g_gest.gIn) > 1e-9) { g_gest.gIn = curSec; emitScrub(curSec, false); }
        } else if (g_gest.kind == 11) {
            // Item 29: DRAG SCRUBS the playhead (NOT pan). It follows the cursor; seeks the
            // engine while playing (keeps playing), frame-exact recompose while paused.
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            if (std::abs(curSec - g_gest.gIn) > 1e-9) {
                g_gest.gIn = curSec;
                if (g_playingExt) { g_stockSec = curSec; engineReelSeek(curSec); }
                else { g_stockSec = -1; emitScrub(curSec, false); }
            }
        } else if (g_gest.kind == 7) {
            float y = std::max(thrLaneTop, std::min(thrLaneBot, my));
            double frac = (thrLaneBot - y) / std::max(1.0f, thrLaneBot - thrLaneTop);
            g_thrLevel = frac <= 0.002 ? 0.0 : std::pow(10.0, (kThrFloorDb + frac * -kThrFloorDb) / 20.0);
            g_quietDirty = true;
            emitThreshold(false);
        } else if (g_gest.kind == 2 && std::abs(mx - g_gest.pressX) > 4) {
            g_gest.kind = 3; g_gest.dragged = true;
            if (g_gest.group.empty()) g_gest.group.push_back(g_gest.idx);
        } else if (g_gest.kind == 4 && g_gest.idx >= 0 && g_gest.idx < (int)g_track[0].size()
                   && std::abs(mx - g_gest.pressX) > 4) {
            // Item 3 fix (round 3): a plain CLICK on the trim handle (no real drag)
            // must never trim a frame off the clip. gIn/gOut start out equal to
            // c.in/c.out at press (see clipHit above); this block used to recompute
            // them on the very first "active" frame regardless of movement, and
            // snapComp's pixel->second->frame-snap round trip can land a hair off
            // c.in even with a motionless mouse - enough to clear the release
            // handler's 0.001 no-op check and commit a phantom 1-frame trim. Same
            // DRAG_PX=4 slop every other click-vs-drag gesture here already uses
            // (see kind==2 -> kind==3 promotion above); below it, this stays a click.
            Clip& c = g_track[0][g_gest.idx];
            double edgeComp = snapComp(xToSec(mx), g_pps, curSec, g_gest.idx);
            double nIn = c.in + (edgeComp - c.compStart);
            nIn = std::max(0.0, std::min(nIn, c.out - 0.05));
            g_gest.gIn = nIn; g_gest.gOut = c.out;
        } else if (g_gest.kind == 5 && g_gest.idx >= 0 && g_gest.idx < (int)g_track[0].size()
                   && std::abs(mx - g_gest.pressX) > 4) {
            // Same click-vs-drag guard as kind==4 above, right-edge handle.
            Clip& c = g_track[0][g_gest.idx];
            double edgeComp = snapComp(xToSec(mx), g_pps, curSec, g_gest.idx);
            double nOut = c.in + (edgeComp - c.compStart);
            auto pk = peaksGet(c.source);
            double srcDur = (pk && pk->ready) ? pk->duration : 0;
            if (srcDur > 0.1) nOut = std::min(nOut, srcDur);
            nOut = std::max(nOut, c.in + 0.05);
            g_gest.gIn = c.in; g_gest.gOut = nOut;
        } else if (g_gest.kind == 8 && std::abs(mx - g_gest.pressX) > 4) {
            // 12, NOT 11. Dragging a caption used to promote the gesture to kind
            // 11 - which is ALSO the ruler-pan kind added later in the file's
            // life. Because the pan branch is tested FIRST in this chain, the
            // caption-move branch below became unreachable: dragging a caption
            // PANNED THE TIMELINE and the cue never moved. Two features, one
            // number; the newer one silently ate the older one.
            g_gest.kind = 12; g_gest.dragged = true;   // body press became a MOVE
        } else if (g_gest.kind == 12) {
            // Move: duration is preserved, so gOut-gIn is still the cue's length.
            // Snap the START to a cut; if that finds nothing, try snapping the END
            // so a caption can be parked flush against the cut on either side.
            double dur = g_gest.gOut - g_gest.gIn;
            double ns = xToSec(mx) - g_gest.grabOff;
            double ss = capSnapCut(ns);
            if (std::abs(ss - ns) > 1e-9) ns = ss;
            else {
                double se = capSnapCut(ns + dur);
                if (std::abs(se - (ns + dur)) > 1e-9) ns = se - dur;
            }
            if (ns < 0) ns = 0;
            g_gest.gIn = quantToFrame(ns); g_gest.gOut = quantToFrame(ns + dur);
        } else if (g_gest.kind == 9 && std::abs(mx - g_gest.pressX) > 4) {
            // Item 30: only trim once the mouse has actually DRAGGED > 4px - the same guard
            // the clip edges (kinds 4/5) use. Without it a bare CLICK on the start edge
            // quantized to a neighbouring frame and the release committed it, shaving one
            // frame off the caption (the identical bug we already fixed for clips).
            double t = quantToFrame(capSnapCut(xToSec(mx)));
            double lim = quantToFrame(g_gest.gOut - 1.0 / reelFps());   // never shorter than one frame
            g_gest.gIn = std::max(0.0, std::min(t, lim));
        } else if (g_gest.kind == 10 && std::abs(mx - g_gest.pressX) > 4) {
            double t = quantToFrame(capSnapCut(xToSec(mx)));
            double lim = quantToFrame(g_gest.gIn + 1.0 / reelFps());
            g_gest.gOut = std::max(t, lim);
        }
    }

    // Item 31: show the closed-hand (grab) cursor while dragging a clip to a new slot
    // (kind 3) or middle-mouse panning the timeline - the "I'm moving this" feedback.
    if (g_gest.kind == 3 || s_midPan) drawGrabCursor();

    if (released && g_gest.kind != 0) {
        Gesture g = g_gest; g_gest = Gesture{};
        if (g.kind == 1) {
            emitScrub(curSec, true);
        } else if (g.kind == 7) {
            emitThreshold(true);
            g_quietDirty = true;
        } else if (g.kind == 2 && g.idx >= 0 && g.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g.idx];
            if (g.ctrl) {
                if (g_sel.count(c.id)) g_sel.erase(c.id); else { g_sel.insert(c.id); g_selAnchor = c.id; }
                emitSelect();
            } else if (g.shiftK && !g_selAnchor.empty()) {
                int ai = -1, bi = g.idx;
                for (size_t i = 0; i < g_track[0].size(); i++)
                    if (g_track[0][i].id == g_selAnchor) { ai = (int)i; break; }
                if (ai >= 0) {
                    g_sel.clear();
                    for (int i = std::min(ai, bi); i <= std::max(ai, bi); i++) g_sel.insert(g_track[0][i].id);
                } else { g_sel.clear(); g_sel.insert(c.id); g_selAnchor = c.id; }
                emitSelect();
            } else {
                g_sel.clear(); g_sel.insert(c.id); g_selAnchor = c.id;
                emitSelect();
                // REVERSED, live, 2026-07-23 (item 9): the earlier "a clip-body click
                // never moves the playhead" rule cost him the ability to click a clip
                // to work on it. His corrected word: PAUSED, a clip-body click DOES
                // move the playhead to the click - the click is where he wants to work.
                // PLAYING, it still only sets the STOCK (unchanged) - moving the live
                // playhead mid-playback would disrupt it, and the stock is where edit
                // keys apply and where Space returns to (E-6).
                if (g_playingExt) {
                    g_stockSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
                    g_stockFlash = true;
                } else {
                    curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
                    // Item 2 (round 2): a STOPPED clip-click moved curSec but left any
                    // earlier g_stockSec exactly where it was - the stock draws its OWN
                    // flag (the solid black one, right above the white real playhead in
                    // the draw code), so a stale stock from an earlier ruler click or
                    // playhead drag kept showing as a playhead that "didn't move", even
                    // though curSec (the white flag) had. Jordan, verbatim: "the
                    // playhead body remains where i last clicked the playhead". The
                    // stock's whole purpose is a MID-PLAYBACK return point (see its
                    // declaration comment) - there is no playback running here, so
                    // clearing it is correct, not a workaround.
                    g_stockSec = -1; g_stockFlash = false;
                    emitScrub(curSec, true);
                }
            }
        } else if (g.kind == 3 && !g.group.empty()) {
            double cur = xToSec(mx);
            std::set<int> dragged(g.group.begin(), g.group.end());
            int to = 0;
            for (size_t i = 0; i < g_track[0].size(); i++) {
                if (dragged.count((int)i)) continue;
                Clip& c = g_track[0][i];
                if (c.compStart + (c.out - c.in) / 2 < cur) to++;
            }
            std::vector<Clip> moved, rest;
            for (size_t i = 0; i < g_track[0].size(); i++)
                (dragged.count((int)i) ? moved : rest).push_back(g_track[0][i]);
            int ins = std::min(to, (int)rest.size());
            rest.insert(rest.begin() + ins, moved.begin(), moved.end());
            bool changed = false;
            for (size_t i = 0; i < rest.size(); i++)
                if (rest[i].id != g_track[0][i].id) { changed = true; break; }
            // A tied-clip preview (G-1) only ever shows a SUBSET of the real reel, so a
            // "to" index computed against it does not mean the same position in the
            // real reel - drop the drag instead of sending the engine a reorder that
            // would corrupt the real reel out from under the preview.
            if (changed && !g_inTiedPreview) {
                g_track[0] = rest; packTrack(0); recomputeDur();
                // A-1: the reorder is optimistic-local (no timeline reload), so the
                // derived caption lane must follow the clips RIGHT NOW - found live:
                // dragging the blue clip first left every caption at its old position.
                rebuildDerivedCaptions();
                // cycle 18 review's gap 4: local state (g_track[0], above) is already
                // updated optimistically, so this engine sync - like emitSelect/
                // emitThreshold - is best-effort telemetry the UI must never block a
                // frame on. A drag-reorder during a fast multi-select edit burst (E-8/
                // I-7/I-9) is exactly the case a 4s stall on the UI thread would hit.
                if (g.group.size() > 1) {
                    json ids = json::array();
                    for (auto& c : moved) ids.push_back(c.id);
                    int toArg = to;
                    try {
                        std::thread([ids, toArg] { json r = engineCall("reorder_many", { {"ids", ids}, {"to", toArg} }, 4.0); (void)r; }).detach();
                    } catch (const std::exception& e) {
                        editLog(std::string("reorder_many: thread spawn failed, skipping sync: ") + e.what());
                    }
                } else {
                    std::string movedId = moved[0].id; int toArg = to;
                    try {
                        std::thread([movedId, toArg] { json r = engineCall("reorder", { {"id", movedId}, {"to", toArg} }, 4.0); (void)r; }).detach();
                    } catch (const std::exception& e) {
                        editLog(std::string("reorder: thread spawn failed, skipping sync: ") + e.what());
                    }
                }
            }
            g_quietDirty = true;
        } else if ((g.kind == 4 || g.kind == 5) && g.idx >= 0 && g.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g.idx];
            if (std::abs(g.gIn - c.in) > 0.001 || std::abs(g.gOut - c.out) > 0.001) {
                c.in = g.gIn; c.out = g.gOut;
                packTrack(0); recomputeDur();
                if (curSec > g_compDur) curSec = g_compDur;
                g_quietDirty = true;
                rebuildDerivedCaptions();   // A-1: same reason as the reorder above
                // Same fix as reorder/reorder_many above: local state is already
                // updated, so this is best-effort telemetry, never a UI-thread stall.
                {
                    std::string trimId = c.id; double trimIn = c.in, trimOut = c.out;
                    try {
                        std::thread([trimId, trimIn, trimOut] { json r = engineCall("set_trim", { {"id", trimId}, {"in", trimIn}, {"out", trimOut} }, 4.0); (void)r; }).detach();
                    } catch (const std::exception& e) {
                        editLog(std::string("set_trim: thread spawn failed, skipping sync: ") + e.what());
                    }
                }
            } else {
                g_sel.clear(); g_sel.insert(c.id); g_selAnchor = c.id;
                emitSelect();
            }
        } else if (g.kind == 8 && g.idx >= 0 && g.idx < (int)g_caps.size()) {
            // A caption CLICK (pressed and released without dragging) opens the
            // inline text box on that cue - "click and type the correct caption".
            g_capSel = g.idx; g_capEdit = g.idx; g_capEditFocus = true;
            std::string t = g_caps[g.idx].text;
            for (auto& ch : t) if (ch == '\n' || ch == '\r') ch = ' ';
            snprintf(g_capEditBuf, sizeof g_capEditBuf, "%s", t.c_str());
        } else if ((g.kind == 9 || g.kind == 10 || g.kind == 12) && g.idx >= 0 && g.idx < (int)g_caps.size()) {
            Caption& cp = g_caps[g.idx];
            if (std::abs(g.gIn - cp.start) > 0.001 || std::abs(g.gOut - cp.end) > 0.001) {
                // Measure, don't claim (same reason I-2/I-5 log their timings): one line
                // per committed caption edit saying whether the edge actually landed on a
                // cut point and on a whole frame. "Snapping works" is then a grepped
                // number in crash.log, not an assertion.
                double fps = reelFps();
                bool onCut = false;
                for (auto& c : g_track[0]) {
                    double e = c.compStart + (c.out - c.in);
                    if (std::abs(c.compStart - g.gIn) < 0.0006 || std::abs(e - g.gIn) < 0.0006) { onCut = true; break; }
                }
                crashLog("CAP commit kind=" + std::to_string(g.kind) +
                         " start=" + std::to_string(g.gIn) + " end=" + std::to_string(g.gOut) +
                         " startFrame=" + std::to_string(g.gIn * fps) +
                         " fps=" + std::to_string(fps) +
                         " pps=" + std::to_string(g_pps) +
                         " onCut=" + (onCut ? "1" : "0"));
                cp.start = g.gIn; cp.end = g.gOut;
                saveCaptions();          // straight back to the .srt - no hidden unsaved state
            }
        }
    }

    int epoch = g_fillEpoch.load();
    if (g_thrOn && (g_quietDirty || epoch != g_quietEpochSeen)) {
        g_quietDirty = false; g_quietEpochSeen = epoch;
        recomputeQuiet();
    }

    ImGui::PushClipRect(p, ImVec2(p.x + tlW, sbY + sbH), true);

    double step = rulerStep(g_pps);
    double t0 = std::floor(g_scrollSec / step) * step;
    // Each tick is t0 + k*step from an INTEGER k, never `s += step` in a loop -
    // accumulating a double step (0.1 has no exact binary value) drifts a
    // fraction of a step further off with every iteration, and by the far edge
    // of a long timeline a tick that should be exactly on the step lands just
    // under it - which is what fed the duplicate-label bug fmtTime just fixed.
    // Computing every tick straight from t0 keeps the error bounded to one
    // rounding step, not an accumulating one.
    long long nTicks = (long long)std::ceil((g_scrollSec + viewDur + step - t0) / step) + 1;
    double frameDur = 1.0 / reelFps();
    // Round 5: per-FRAME ticks were flooding the ruler ("excessive hash marks") because
    // they switched on as soon as a frame was 4px wide - at a normal working zoom that
    // is a tick every few pixels. The reference ruler uses a handful of even
    // subdivisions per label until you are zoomed in FAR enough that individual frames
    // are genuinely spaced out; require >= 12px per frame before showing them.
    bool frameTicks = reelFps() > 1.0 && g_pps * frameDur >= 12.0;
    for (long long k = 0; k < nTicks; k++) {
        double s = t0 + (double)k * step;
        float x = secToX(s);
        if (x < tlX - 60 || x > tlX + tlW + 60) continue;
        dl->AddLine(ImVec2(x, p.y + 6), ImVec2(x, p.y + rulerH), COL_TICK);
        char b[24]; fmtTime(s, b, sizeof b, step < 1.0);
        dl->AddText(ImVec2(x + 3, p.y + 3), COL_RULERTX, b);
        if (frameTicks) {
            // Zoomed in enough that a frame is a real, clickable width (>= 4px):
            // Jordan cut every clip by hand in Vegas, one frame at a time, and
            // reads these minor ticks AS frame marks - so at this zoom they must
            // BE frame boundaries (multiples of 1/fps from comp time 0, the same
            // grid quantToFrame snaps to), not the old meaningless step/5 split.
            long long f = (long long)std::ceil(s / frameDur);
            while (f * frameDur <= s + 1e-9) f++;
            for (; f * frameDur < s + step - 1e-9; f++) {
                float xm = secToX((double)f * frameDur);
                dl->AddLine(ImVec2(xm, p.y + rulerH - 5), ImVec2(xm, p.y + rulerH), COL_TICKMIN);
            }
        } else {
            for (int m = 1; m < 5; m++) {
                float xm = secToX(s + step * m / 5.0);
                dl->AddLine(ImVec2(xm, p.y + rulerH - 5), ImVec2(xm, p.y + rulerH), COL_TICKMIN);
            }
        }
    }

    dl->AddRectFilled(ImVec2(tlX, aY), ImVec2(tlX + tlW, aY + laneH), COL_LANE, 3);

    if (g_track[0].empty()) {
        // Name the gesture that actually fills it. Double-clicking a search hit
        // adds that clip to the timeline (addHitToTimeline) - that is how the reel
        // gets BUILT, and "load a reel from the engine" told him about the other
        // path only. Wording follows the reference's .tlempty hint.
        const char* msg = "timeline empty - double-click a quote in the search results to add clips, or use Load Reel";
        ImVec2 ts = ImGui::CalcTextSize(msg);
        // Brightened from (120,128,140) - 4.3:1 on the near-black lane - to about
        // 8:1. It is the ONLY message on screen when nothing else is, so it is the
        // one that must not need effort to read.
        dl->AddText(ImVec2(tlX + (tlW - ts.x) / 2, aY + (laneH - ts.y) / 2), IM_COL32(178, 186, 200, 255), msg);
    }

    for (size_t i = 0; i < g_track[0].size(); i++) {
        Clip& c = g_track[0][i];
        double cin = c.in, cout = c.out, compStart = c.compStart;
        bool ghost = (g_gest.kind == 4 || g_gest.kind == 5) && (int)i == g_gest.idx;
        if (ghost) { cin = g_gest.gIn; cout = g_gest.gOut; }
        double drawStart = compStart, drawDur = cout - cin;
        if (ghost && g_gest.kind == 4) drawStart = compStart + (cin - c.in);
        float x0 = secToX(drawStart), x1 = secToX(drawStart + drawDur);
        if (x1 < tlX - 4 || x0 > tlX + tlW + 4) continue;
        bool selected = g_sel.count(c.id) != 0;
        bool inDrag = g_gest.kind == 3 && std::find(g_gest.group.begin(), g_gest.group.end(), (int)i) != g_gest.group.end();
        // Jordan cuts butt-joined clips with ZERO gap in Vegas - the reel data
        // proves it (compStart of clip i+1 equals clip i's compStart+dur exactly).
        // But every clip used to get a 1px inset on BOTH sides unconditionally,
        // so two touching clips always left a 2px unfilled seam (the lane
        // background showing through), and rounding all 4 corners on both clips
        // widened that seam further with a rounded notch on each side - together
        // reading as the "visible dark gap between clips that are supposed to be
        // butt-joined" he flagged. An edge that touches a real neighbour now
        // draws flush (no inset, no rounding on that side); only an edge facing
        // actual empty timeline (or a real gap) keeps the 1px inset + rounding.
        bool touchesPrev = !ghost && i > 0 &&
            std::abs((g_track[0][i - 1].compStart + (g_track[0][i - 1].out - g_track[0][i - 1].in)) - drawStart) < 1e-4;
        bool touchesNext = !ghost && i + 1 < g_track[0].size() &&
            std::abs((drawStart + drawDur) - g_track[0][i + 1].compStart) < 1e-4;
        float fx0 = x0 + (touchesPrev ? 0.0f : 1.0f), fx1 = x1 - (touchesNext ? 0.0f : 1.0f);
        ImDrawFlags rf = ImDrawFlags_RoundCornersNone;
        if (!touchesPrev) rf |= ImDrawFlags_RoundCornersLeft;
        if (!touchesNext) rf |= ImDrawFlags_RoundCornersRight;
        // SELECTION = OPAQUE FILL, NEVER AN OUTLINE. Jordan, verbatim: "Pleae
        // remove the yellow outline around the selected clip" [feedback2], and
        // a clip's border must match its own colour [feedback4]. The clip
        // colours are his ACCESSIBILITY AID - he identifies a clip by its
        // colour at a glance - so selection has to read THROUGH that colour by
        // going solid, not by drawing a different colour on top of it.
        ImU32 fill = IM_COL32(c.r, c.g, c.b, selected ? 255 : 62);
        if (inDrag) fill = (fill & 0x00FFFFFF) | 0x60000000;
        dl->AddRectFilled(ImVec2(fx0, aY + 1), ImVec2(fx1, aY + laneH - 1), fill, 3, rf);
        float vx0 = std::max(fx0, tlX), vx1 = std::min(fx1, tlX + tlW);
        if (vx1 > vx0 && wy1 - wy0 > 6) {
            drawWave(dl, c.source, cin, cout, x0, vx0, vx1, wy0, wy1, g_pps,
                     inDrag ? COL_WAVEDIM : (selected ? IM_COL32(255, 255, 255, 190) : COL_WAVE));
            // A source whose audio decode FAILED (most often: no audio track at
            // all - silent screen captures, the ges-bench demo proxy) used to
            // draw an unlabeled flat block, indistinguishable from "waveforms
            // are broken" - that exact ambiguity was escalated as a regression
            // on 2026-07-22 and cost a diagnostic session. Name the state, once,
            // dim, only when there is room: a labeled degrade is legible, a
            // silent one looks like a bug. (peaksGet here is the same per-clip
            // per-frame cost class clipPreparing below already pays.)
            if (auto pkf = peaksGet(c.source); pkf && pkf->failed && wy1 - wy0 > 14 && vx1 - vx0 > 96) {
                const char* nam = "no audio / no waveform";
                ImVec2 nts = ImGui::CalcTextSize(nam);
                dl->AddText(ImVec2(vx0 + 6, (wy0 + wy1 - nts.y) * 0.5f), IM_COL32(158, 166, 180, 170), nam);
            }
        }
        // The border ALWAYS matches the clip's own colour - no white ring on the
        // selected clip. The opaque fill above is what says "selected".
        ImU32 brd = IM_COL32(c.r, c.g, c.b, 242);
        dl->AddRect(ImVec2(fx0, aY + 1), ImVec2(fx1, aY + laneH - 1), brd, 3, rf, 1.0f);
        if (clipPreparing(c)) {
            ImVec2 pr0(std::max(x0 + 1, tlX), aY + 1), pr1(std::min(x1 - 1, tlX + tlW), aY + laneH - 1);
            if (pr1.x > pr0.x) {
                dl->PushClipRect(pr0, pr1, true);
                dl->AddRectFilled(pr0, pr1, IM_COL32(0, 0, 0, 96));
                for (float sx = x0 - laneH; sx < x1; sx += 16.0f)
                    dl->AddLine(ImVec2(sx, aY + laneH), ImVec2(sx + laneH, aY), IM_COL32(255, 255, 255, 30), 3.0f);
                const char* pmsg = "preparing...";
                ImVec2 ts = ImGui::CalcTextSize(pmsg);
                if (pr1.x - pr0.x > ts.x + 10) {
                    float cx = (pr0.x + pr1.x - ts.x) * 0.5f, cy = aY + (laneH - ts.y) * 0.5f;
                    dl->AddText(ImVec2(cx + 1, cy + 1), IM_COL32(0, 0, 0, 220), pmsg);
                    dl->AddText(ImVec2(cx, cy), IM_COL32(255, 255, 255, 240), pmsg);
                }
                dl->PopClipRect();
            }
        }
        // E-11: small fixed thumbnail chip, top-left of the header row - never
        // resized to the clip's width (stays "small fixed"), never drawn into
        // the waveform band below it (wy0 already accounts for thumbH above).
        bool showThumb = thumbH > 0 && (x1 - x0) > thumbH + 28;
        float labX0 = x0 + 6;
        if (showThumb) {
            ThumbTex* tt = getThumb(c.source);
            ImVec2 t0(x0 + 3, aY + 3), t1(x0 + 3 + thumbH, aY + 3 + thumbH);
            if (tt && tt->srv) dl->AddImage((ImTextureID)tt->srv, t0, t1);
            else dl->AddRectFilled(t0, t1, IM_COL32(0, 0, 0, 90));
            dl->AddRect(t0, t1, IM_COL32(255, 255, 255, 60));
            labX0 = t1.x + 6;
        }
        if (labelH > 0 && x1 - x0 > 34) {
            char lab[160]; double d = cout - cin; char tb[24]; fmtTime(d, tb, sizeof tb, d < 10);
            snprintf(lab, sizeof lab, "%s  %s", c.label.c_str(), tb);
            dl->PushClipRect(ImVec2(labX0, aY), ImVec2(x1 - 4, aY + headerH + 4), true);
            dl->AddText(ImVec2(labX0 + 1, aY + 4), IM_COL32(0, 0, 0, 200), lab);
            dl->AddText(ImVec2(labX0, aY + 3), COL_LABEL, lab);
            dl->PopClipRect();
        }
        if (x1 - x0 > 20) {
            ImU32 hcol = IM_COL32(c.r, c.g, c.b, selected ? 255 : 150);
            dl->AddRectFilled(ImVec2(x0 + 1, aY + 1), ImVec2(x0 + 4, aY + laneH - 1), hcol);
            dl->AddRectFilled(ImVec2(x1 - 4, aY + 1), ImVec2(x1 - 1, aY + laneH - 1), hcol);
        }
    }

    // ---- caption lane ----
    if (showCaps) {
        dl->AddRectFilled(ImVec2(tlX, capY), ImVec2(tlX + tlW, capY + capH), COL_CAPLANE, 3);
        // The reel's cut points, drawn THROUGH the caption lane, so it is visible at
        // a glance whether a caption is sitting on its cut or drifting off it.
        for (auto& c : g_track[0]) {
            float cx = secToX(c.compStart);
            if (cx >= tlX && cx <= tlX + tlW) dl->AddLine(ImVec2(cx, capY), ImVec2(cx, capY + capH), COL_CAPCUT);
        }
        float tlh = ImGui::GetTextLineHeight();
        if (g_caps.empty()) {
            const char* m = g_capErr.empty() ? "no captions in this reel's .srt" : g_capErr.c_str();
            dl->AddText(ImVec2(tlX + 8, capY + (capH - tlh) * 0.5f), IM_COL32(170, 150, 120, 255), m);
        }
        for (size_t i = 0; i < g_caps.size(); i++) {
            double s = g_caps[i].start, e = g_caps[i].end;
            bool ghost = (g_gest.kind == 9 || g_gest.kind == 10 || g_gest.kind == 12) && (int)i == g_gest.idx;
            if (ghost) { s = g_gest.gIn; e = g_gest.gOut; }
            float x0 = secToX(s), x1 = secToX(e);
            if (x1 < tlX - 4 || x0 > tlX + tlW + 4) continue;
            bool sel = (int)i == g_capSel;
            dl->AddRectFilled(ImVec2(x0 + 1, capY + 2), ImVec2(x1 - 1, capY + capH - 2), sel ? COL_CAPSEL : COL_CAP, 3);
            dl->AddRect(ImVec2(x0 + 1, capY + 2), ImVec2(x1 - 1, capY + capH - 2),
                        sel ? IM_COL32(255, 255, 255, 255) : COL_CAPBRD, 3, 0, sel ? 2.0f : 1.0f);
            if (x1 - x0 > 18) {   // drag grips, same affordance the clips use
                dl->AddRectFilled(ImVec2(x0 + 1, capY + 2), ImVec2(x0 + 4, capY + capH - 2), COL_CAPBRD);
                dl->AddRectFilled(ImVec2(x1 - 4, capY + 2), ImVec2(x1 - 1, capY + capH - 2), COL_CAPBRD);
            }
            if ((int)i == g_capEdit) continue;   // the InputText renders the text instead
            std::string t = g_caps[i].text;
            for (auto& ch : t) if (ch == '\n' || ch == '\r') ch = ' ';
            float tx0 = std::max(x0 + 6, tlX + 2), tx1 = std::min(x1 - 5, tlX + tlW);
            if (tx1 > tx0 + 8) {
                dl->PushClipRect(ImVec2(tx0, capY), ImVec2(tx1, capY + capH), true);
                dl->AddText(ImVec2(tx0, capY + (capH - tlh) * 0.5f), COL_CAPTX, t.c_str());
                dl->PopClipRect();
            }
        }
    }

    if (g_thrOn) {
        for (auto& r : g_quietRanges) {
            float qx0 = secToX(r.first), qx1 = secToX(r.second);
            if (qx1 < tlX || qx0 > tlX + tlW) continue;
            dl->AddRectFilled(ImVec2(std::max(qx0, tlX), aY + 1), ImVec2(std::min(qx1, tlX + tlW), aY + laneH - 1), COL_QUIETDIM);
        }
        float ty = thrY();
        dl->AddLine(ImVec2(tlX, ty), ImVec2(tlX + tlW, ty), COL_THRBAR, 2.0f);
        dl->AddRectFilled(ImVec2(tlX + 10, ty - 4), ImVec2(tlX + 20, ty + 4), COL_THRBAR, 2.0f);
        char tb[64];
        if (g_thrLevel <= 0) snprintf(tb, sizeof tb, "threshold -50 dB - skipping nothing (drag up)");
        else snprintf(tb, sizeof tb, "threshold %.0f dB  (drag)", std::max(kThrFloorDb, 20.0 * std::log10(g_thrLevel)));
        float labY = (ty - thrLaneTop > 20) ? ty - 18 : ty + 6;
        dl->AddText(ImVec2(tlX + 26, labY), COL_THRBAR, tb);
    }

    // 2026-07-03: "Add a second Playhead Stock (the black bar)... 2 identical
    // black bars, but only one of them has the white playhead that moves." The
    // stock used to draw as a plain 2px line - wrong SHAPE, not just wrong
    // color. It now draws the SAME flag geometry (rect + triangle tip) as the
    // moving playhead below, just solid black instead of white, so at rest the
    // two read as identical bars and only the real playhead's cap is white.
    // The slow black/white flash after a manual mid-playback move is preserved
    // unchanged - a separate, wanted behavior.
    if (g_stockSec >= 0) {
        float sx = secToX(g_stockSec);
        if (sx >= tlX - 2 && sx <= tlX + tlW + 2) {
            // Item 6 (round 4): the stock is a PLAIN BAR with NO flag head - only the
            // real moving playhead below carries the white flag (CSS #stock vs
            // #playhead). Drawing the stock as a full flag made a "phantom" second
            // playhead Jordan rejected. The BAR itself blinks black<->white when it
            // was moved during playback (CSS #stock.flashing / stockBlink 0.8s);
            // black (COL_PLAYHEAD) at rest.
            bool wht = g_stockFlash && std::fmod(nowSec(), 0.8) >= 0.4;
            ImU32 barCol = wht ? IM_COL32(255, 255, 255, 255) : COL_PLAYHEAD;
            dl->AddLine(ImVec2(sx, p.y + 2), ImVec2(sx, bot), barCol, 2.0f);
        }
    }

    float px = secToX(curSec);
    if (px >= tlX - 2 && px <= tlX + tlW + 2) {
        dl->AddLine(ImVec2(px, p.y + 2), ImVec2(px, bot), COL_PLAYHEAD, 2.0f);
        float fw = 8, ftop = p.y + 1, fmid = p.y + 13, ftip = p.y + 20;
        dl->AddRectFilled(ImVec2(px - fw, ftop), ImVec2(px + fw, fmid), COL_PHFLAG);
        dl->AddTriangleFilled(ImVec2(px - fw, fmid), ImVec2(px + fw, fmid), ImVec2(px, ftip), COL_PHFLAG);
        dl->AddRect(ImVec2(px - fw, ftop), ImVec2(px + fw, fmid), IM_COL32(0, 0, 0, 115));
        // 2026-07-03: "add 2 tiny vertical hashmarks inside the white part of the
        // playhead" (his reference photo, playhead.JPG - 2 small dark ticks with
        // a real gap between them). Filled rects, not thin AddLine strokes: at
        // this size 2 nearly-touching antialiased lines blur into one blob.
        dl->AddRectFilled(ImVec2(px - 4.0f, ftop + 3), ImVec2(px - 2.0f, fmid - 2), COL_PHGRIP);
        dl->AddRectFilled(ImVec2(px + 2.0f, ftop + 3), ImVec2(px + 4.0f, fmid - 2), COL_PHGRIP);
    }

    ImGui::PopClipRect();

    // Inline caption text editing. Submitted AFTER the "tl" InvisibleButton so ImGui
    // gives this box hover/keyboard priority over the timeline surface underneath it,
    // and while it is active io.WantCaptureKeyboard is true - which is what stops the
    // S / Del / space edit shortcuts from firing into the typed text (they are already
    // gated on that flag in the main loop).
    if (showCaps && g_capEdit >= 0 && g_capEdit < (int)g_caps.size()) {
        float x0 = secToX(g_caps[g_capEdit].start), x1 = secToX(g_caps[g_capEdit].end);
        float ex0 = std::max(x0, tlX), ex1 = std::min(x1, tlX + tlW);
        if (ex1 - ex0 < 220) ex1 = std::min(tlX + tlW, ex0 + 220);   // always wide enough to read what you type
        if (ex1 - ex0 < 80) { ex0 = tlX; ex1 = std::min(tlX + tlW, tlX + 220); }
        ImGui::SetCursorScreenPos(ImVec2(ex0, capY + 4));
        ImGui::SetNextItemWidth(ex1 - ex0);
        if (g_capEditFocus) { ImGui::SetKeyboardFocusHere(); g_capEditFocus = false; }
        bool enter = ImGui::InputText("##capedit", g_capEditBuf, sizeof g_capEditBuf,
                                      ImGuiInputTextFlags_EnterReturnsTrue);
        // Item 32: reflect the typed text in the PREVIEW in realtime. drawCaptionsImGui
        // reads g_caps[i].text, so update the in-memory caption EVERY frame while typing
        // (Jordan: "when manually editing captions, they should update in realtime" - no
        // more clicking away first). Only WRITE the .srt (saveCaptions) on commit, never
        // per keystroke. ImGui restores the buffer to the original on Escape, so this same
        // live-write reverts the text on cancel too.
        std::string live = g_capEditBuf;
        for (auto& ch : live) if (ch == '\n' || ch == '\r') ch = ' ';
        if (live != g_caps[g_capEdit].text) g_caps[g_capEdit].text = live;
        if (enter || ImGui::IsItemDeactivated()) {
            saveCaptions();          // persist the final text to the .srt on commit
            g_capEdit = -1;
        }
    }

    ImGui::SetCursorScreenPos(ImVec2(tlX, sbY));
    ImGui::InvisibleButton("tlsb", ImVec2(tlW, sbH));
    double total = std::max(viewDur, maxScroll + viewDur);
    float thW = total > 0 ? (float)(viewDur / total) * tlW : tlW;
    thW = std::max(thW, 24.0f);
    float thX = total > viewDur ? tlX + (float)(g_scrollSec / (total - viewDur)) * (tlW - thW) : tlX;
    dl->AddRectFilled(ImVec2(tlX, sbY), ImVec2(tlX + tlW, sbY + sbH), IM_COL32(28, 31, 37, 255), 4);
    dl->AddRectFilled(ImVec2(thX, sbY + 1), ImVec2(thX + thW, sbY + sbH - 1), IM_COL32(95, 104, 120, 255), 4);
    if (ImGui::IsItemActivated()) {
        g_gest = Gesture{}; g_gest.kind = 6;
        g_gest.grabOff = (mx >= thX && mx <= thX + thW) ? (mx - thX) : thW / 2;
    }
    if (ImGui::IsItemActive() && g_gest.kind == 6 && total > viewDur && tlW > thW) {
        double frac = (mx - g_gest.grabOff - tlX) / (tlW - thW);
        g_scrollSec = std::max(0.0, std::min(1.0, frac)) * (total - viewDur);
        g_lastUserScroll = nowSec();
    }
    if (ImGui::IsItemDeactivated() && g_gest.kind == 6) {
        g_gest = Gesture{};
    }

    static double s_lastPps = -1, s_lastScroll = -1;
    if (std::abs(g_pps - s_lastPps) > 1e-9 || std::abs(g_scrollSec - s_lastScroll) > 0.05) {
        if (emitView()) { s_lastPps = g_pps; s_lastScroll = g_scrollSec; }
    }
}

// --------------- left panel: library / search / transcript (ImGui) ---------------
// The left panel is the LIBRARY: a scrollable list of the open folder's videos
// (with transcript pairing), a search box whose hits render as structured rows
// (verbatim .srt timecode, playable clip), and a flowing single-video
// transcript view (audapolis pattern) reachable by Enter/double-click on a row.

// ---- library state ----
struct VideoRow {
    std::string path, name, date; bool hasTranscript = false;
    // B-1 card display cache: the middle-ellipsised name and the width it was
    // measured at. Recomputed only when the panel width changes, so a 2258-video
    // corpus costs zero CalcTextSize work per frame while the panel is still.
    std::string disp; float dispW = -1.0f;
};
static std::vector<VideoRow> g_videos;
static std::string g_folderRoot;
static int g_orphanCount = 0;
static std::string g_folderErr;

// Sort mode for the library list (B-3): 0=date-newest,1=date-oldest,2=name-AZ,3=name-ZA
static int g_sortMode = 0;
// "Transcribe all" is in flight (UI thread only: set on click, cleared in the
// drainAsync callback, which also runs on the UI thread).
static bool g_transcribeAllBusy = false;
// The library row whose right-click menu is open. The list is clipped, so
// without this the menu vanishes the moment its row scrolls off screen.
static int g_libCtxIdx = -1;
// ONE selection model (B-4): a single selected index shared by mouse + arrows.
static int g_libSel = -1;
static bool g_libScrollPending = false;   // keyboard nav just moved g_libSel; scroll it into view
static bool g_libFocused = false;        // library window (or a child) had focus last frame
static int g_libJustViewedIdx = -1;      // green outline for the just-viewed video (B-6)

// ---- B-2: one-click local transcription ----
// The engine's "transcribe" verb (becky-go/cmd/clip/transcribe.go) already does the
// whole job - official-caption-first, else a local Parakeet pass into a SEPARATE
// "<stem>_parakeet_transcription.srt" sidecar, NEVER touching an original transcript
// - and is synchronous + long-running (real ASR, can take minutes on a long clip).
// Calling it on the UI thread would freeze the whole window for that whole span,
// exactly the P1 mistake this file already root-caused once for search (see
// searchWorker above) - so this is a one-shot background thread per click (a
// context-menu click is a single discrete action, not a rapid-fire stream like
// search-as-you-type, so no coalescing queue is needed, just an in-flight guard
// against double-firing the same video).
struct TranscribeDone { std::string name; bool ok = false; std::string err; };
static std::mutex g_transcribeMx;
static std::set<std::string> g_transcribeInFlight; // video paths currently transcribing
static std::deque<TranscribeDone> g_transcribeDoneQ;
// path is the full source path (used as the UI's in-flight/done-queue key, same
// as every other row identifier in this file); baseName is the bare filename the
// engine's lookupVideo/VideoByName actually indexes by. A REAL BUG FOUND LIVE THIS
// SESSION: this used to send the full path as the "name" arg to the "transcribe"
// verb, but becky-go's VideoByName matches only v.Name (the basename) - so every
// Transcribe() call was guaranteed to fail with "no such video in folder" on
// every prior session, no matter how the menu was clicked. Confirmed by tracing
// becky-go/internal/footage/index.go's VideoByName + becky-go/cmd/clip/app.go's
// lookupVideo, and independently by running becky-transcribe.exe directly on the
// same test clip (succeeded instantly, proving the ASR pipeline itself was never
// the problem).
static void requestTranscribe(const std::string& path, const std::string& baseName) {
    {
        std::lock_guard<std::mutex> lk(g_transcribeMx);
        if (g_transcribeInFlight.count(path)) return; // already running - never double-fire
        g_transcribeInFlight.insert(path);
    }
    beginWork("Transcribing " + baseName + "...");
    std::thread([path, baseName] {
        t_threadTag = "transcribeWorker";
        struct WorkGuard { ~WorkGuard() { endWork(); } } wg;   // clears on EVERY exit path, including a throw
        TranscribeDone d; d.name = path;
        try {
            json r = engineCall("transcribe", { {"name", baseName} }, 900.0); // real ASR - generous timeout
            d.ok = r.value("ok", false);
            if (!d.ok) d.err = r.value("error", std::string("transcribe failed"));
        } catch (const std::exception& e) {
            d.ok = false; d.err = std::string("transcribe exception: ") + e.what();
        }
        std::lock_guard<std::mutex> lk(g_transcribeMx);
        g_transcribeInFlight.erase(path);
        g_transcribeDoneQ.push_back(std::move(d));
    }).detach();
}

// E-13: add_external shells out to ffprobe (AddExternalClip's Probe() call) to
// learn the dropped file's duration, which can be slow for a file on a network
// share or a huge capture - so this is a background thread per drop, same A-4
// "never block the UI thread on an engine call" shape as requestTranscribe,
// not the direct engineCall() the fast in-memory verbs (reorder/set_trim) use.
struct AddExternalDone { bool ok = false; std::string err; json data; };
static std::mutex g_addExtMx;
static std::deque<AddExternalDone> g_addExtDoneQ;
static void requestAddExternal(const std::string& path, int at) {
    g_bgPool->submit([path, at] {
        t_threadTag = "addExternalWorker";
        AddExternalDone d;
        try {
            json r = engineCall("add_external", { {"path", path}, {"at", at} }, 20.0);
            d.ok = r.value("ok", false);
            if (d.ok) d.data = r.contains("data") ? r["data"] : json::object();
            else d.err = r.value("error", std::string("add_external failed"));
        } catch (const std::exception& e) {
            d.ok = false; d.err = std::string("add_external exception: ") + e.what();
        }
        std::lock_guard<std::mutex> lk(g_addExtMx);
        g_addExtDoneQ.push_back(std::move(d));
    });
}

// ---- search state ----
static char g_searchBuf[256] = { 0 };
// Checklist 20: qmd is a persistent TOGGLE (the reference's "smart" pill), not a
// second submit button - so Enter always runs the mode he can see is armed.
static bool g_smartSearch = false;
struct Hit {
    std::string source, name, date, text, timecode;
    double start = 0, end = 0, score = 0;
    bool transcriptOnly = false;
    // The order the ENGINE returned this hit in (it sorts by date). Kept so the
    // relevance sort below is REVERSIBLE without re-running the search - one int
    // per hit instead of a second copy of the whole result set.
    int ord = 0;
};
static std::vector<Hit> g_hits;
// Which hit row the keyboard is on (items 68/76). Mirrors g_libSel for the video
// rows, including the "scroll it into view after an arrow key" flag.
static int g_hitSel = -1;
static bool g_hitScrollPending = false;
// Items 18/19: the third sort, "most relevant results at top". The engine already
// returns a score on every hit and sorts by date, so this is purely a client-side
// re-sort. Sticky across searches - having to re-assert it every query would make
// it useless.
static bool g_hitRelevance = false;
static void applyHitSort() {
    if (g_hitRelevance)
        std::stable_sort(g_hits.begin(), g_hits.end(),
                         [](const Hit& a, const Hit& b) { return a.score > b.score; });
    else
        std::stable_sort(g_hits.begin(), g_hits.end(),
                         [](const Hit& a, const Hit& b) { return a.ord < b.ord; });
    // Every row index just changed meaning, so the keyboard cursor has to go back
    // to the top rather than point at whatever landed on its old index.
    g_hitSel = g_hits.empty() ? -1 : 0;
    g_hitScrollPending = true;
}
static std::string g_searchMode;         // "" | "keyword" | "qmd"
static std::string g_searchNote;         // qmd note / degradation note
static std::string g_searchErr;
static bool g_searching = false;          // C-5 "Searching..." state

// I-* fix (found live this session via the frame-trace CSV, BECKY_REVIEW_FRAME_TRACE):
// runSearch() used to call engineCall("search"/"qmd_search", ...) directly on the UI
// thread. Against the real corpus (10,000 quotes for a common word like "the") that
// round trip took over FIVE SECONDS - a single frame's "dt" spiked to 5131ms in the
// trace, a dead, unresponsive window for that whole span (Present() never runs while
// blocked inside engineCall). Every edit (S/Del/O/I/Z) was already made async via
// editWorker/g_editQ (see A-4) specifically to avoid this; search never got the same
// treatment. Fixed the same way: search runs on its own worker thread; the UI thread
// only ever touches g_searchPending/g_searchDone under their own small mutex.
struct SearchReq { std::string query; bool qmd = false; double t0 = 0; };
struct SearchDone { bool ok = false; std::string mode, note, err, query; std::vector<Hit> hits; double elapsedMs = 0; };
static std::deque<SearchReq> g_searchQ;
static std::mutex g_searchQMx; static std::condition_variable g_searchQCv;
static bool g_searchQuit = false;
static std::mutex g_searchDoneMx;
static bool g_searchDonePending = false;
static SearchDone g_searchDoneResult;

// ---- transcript view (B-8) ----
struct CueRow { std::string source, name, text, timecode; double start = 0, end = 0; };
static std::vector<CueRow> g_cues;
static std::string g_cueName;             // which video's transcript is open
static std::string g_cueErr;
// Item 5: a selected-cue state (visibly highlighted) and Up/Down keyboard nav
// through the open transcript, mirroring g_hitSel/g_hitScrollPending exactly.
static int g_cueSel = -1;
// Items 10/11: Ctrl/Shift multi-selection of transcript quotes. g_cueMulti holds the
// selected cue indices (a std::set, so it iterates in ascending order); g_cueAnchor is the
// shift-range pivot. Empty = plain single-select (g_cueSel) with its audition-on-click.
static std::set<int> g_cueMulti;
static int g_cueAnchor = -1;
static bool g_cueScrollPending = false;
static char g_withinBuf[128] = { 0 };     // search-within-this-transcript
static std::string g_withinLast;          // last frame's search text, to fire the
                                           // auto-scroll-to-first-match only on change
// case-insensitive "find" - a real word processor's search never makes you match
// the ASR's exact capitalization to find a word you know is in the transcript.
static bool ciContains(const std::string& hay, const std::string& needle) {
    auto it = std::search(hay.begin(), hay.end(), needle.begin(), needle.end(),
        [](unsigned char a, unsigned char b) { return std::tolower(a) == std::tolower(b); });
    return it != hay.end();
}

// ---- Q&A cards (G-1) ----
struct QACard {
    std::string id, question, answer;
    std::vector<std::string> clipIDs;
    bool answered = false;
};
static std::vector<QACard> g_cards;
static std::string g_cardsErr;
static std::string g_askAnswer;           // last ask-becky reply (G-3)
// H-6: a mutating "ask" turn returns a Proposal (id + preview + diff), not a
// direct edit - nothing lands on the timeline until the human hits Apply.
// This is the small inline card the adversarial review found missing: without
// it apply_edit_batch (H-4) and applyActions' one-undo-span fix (H-6 Go side)
// were unreachable from the chat - "ask" just dumped JSON text and threw the
// proposal away. G-3 rules out a heavy dialog ("no apply/reject friction
// wall"), so this stays two small buttons inline, never a modal.
static std::string g_proposalID;
static std::string g_proposalPreview;
static std::string g_proposalNote;
static json g_proposalDiff = json::array();  // Proposal.Preview: []{label,before,after}
static bool g_proposalPending = false;
// palette assignment for cards (G-4), persistent by id
static std::map<std::string, uint32_t> g_cardColor;
static const uint32_t kPalette[8] = {
    IM_COL32(0x14,0xFF,0x39,255), IM_COL32(0x00,0xAE,0xEF,255), IM_COL32(0xDC,0x14,0x3C,255),
    IM_COL32(0x8A,0x2B,0xE2,255), IM_COL32(0xFF,0x57,0xD1,255), IM_COL32(0xFF,0xD7,0x00,255),
    IM_COL32(0x16,0xF0,0xEA,255), IM_COL32(0xFF,0x8C,0x00,255),
};
static uint32_t cardColorFor(const std::string& id) {
    auto it = g_cardColor.find(id);
    if (it != g_cardColor.end()) return it->second;
    uint32_t c = kPalette[(g_cardColor.size()) % 8];
    g_cardColor[id] = c; return c;
}

// ---- ask-becky panel state (matches gui/BeckyReviewNative ui/index.html .chat) ----
static char g_askBuf[512] = { 0 };   // was a function-static inside the frame; the chips
                                     // and the Q&A cards both need to write it, so it is
                                     // file scope now.
static bool g_askFocus = false;      // "put the caret in the ask box next frame"
static std::string g_askEcho;        // the question he last sent, echoed above the answer
static std::string g_backendSummary; // engine `status` -> one plain sentence
static bool g_backendOK = false;     // any backend live? drives the status card's colour
static std::string g_answerCardID;   // non-empty => the ask box is answering THIS card
static std::string g_answerCardQ;
// H-7: one forensic run at a time. The judge stage is an LLM pass that can take
// minutes; a double-click must never start two pipelines over the same folder
// (they would race on the same _forensic_hits.json / reel artifacts). Set on
// click, cleared in the completion callback (which drainAsync delivers on the
// UI thread, so a plain bool is enough - no atomics needed).
static bool g_forensicBusy = false;

// Real prompts for a video editor reviewing his OWN footage. The reference's chips
// ("find every threat to the host family") are forensic-case examples and read as
// nonsense in an edit session. Each maps to a verb the engine actually has:
// compile -> ask/apply_proposal, dead air -> autocut_silence, lower-third -> overlay.
//
// LABEL and PROMPT are separate on purpose. The panel is ~300px wide and this font
// is ~9.45px/char at the 1.35 UI scale, so the label budget is about 26 characters -
// "compile every take where I said the intro line" is 455px and would be CLIPPED at
// every real window size. ImGui buttons do not wrap. Short label on the chip, full
// wording into the box.
static const char* kAskChipLabel[3] = { "compile my takes", "cut dead air", "lower-third on" };
static const char* kAskChipPrompt[3] = {
    "compile every take where I said the intro line",
    "cut the dead air out of this reel",
    "turn the lower-third on",
};

// A DRAWN robot, not a glyph. The merged icon font covers Segoe MDL2's private-use
// range only, and CLAUDE.md bans non-ASCII bytes in this source, so the reference's
// U+1F916 would render as a box or break the build. Six primitives, no atlas rebuild,
// reads as a robot at a glance - which is the whole job of the mark.
// Draws the becky robot mark from top-left (x, y0), height h, in colour col. Shared by the
// green ask-becky/brand mark and item 13's BLUE per-card robot so the two are identical
// shapes in different colours.
static void drawRobotMark(ImDrawList* d, float x, float y0, float h, ImU32 col) {
    float w = h * 0.86f, y = y0 + h * 0.20f;
    d->AddLine({ x + w * 0.5f, y }, { x + w * 0.5f, y - h * 0.14f }, col, 2.0f);
    d->AddCircleFilled({ x + w * 0.5f, y - h * 0.16f }, h * 0.08f, col);
    d->AddRect({ x, y }, { x + w, y + h * 0.62f }, col, h * 0.15f, 0, 2.0f);
    d->AddRectFilled({ x + w * 0.20f, y + h * 0.20f }, { x + w * 0.38f, y + h * 0.35f }, col, 1.5f);
    d->AddRectFilled({ x + w * 0.62f, y + h * 0.20f }, { x + w * 0.80f, y + h * 0.35f }, col, 1.5f);
    d->AddLine({ x + w * 0.28f, y + h * 0.48f }, { x + w * 0.72f, y + h * 0.48f }, col, 2.0f);
}
static void askBeckyMark(float h) {
    ImDrawList* d = ImGui::GetWindowDrawList();
    ImVec2 p = ImGui::GetCursorScreenPos();
    drawRobotMark(d, p.x, p.y, h, kPalette[0]);          // #14FF39 green
    ImGui::Dummy(ImVec2(h * 0.86f, h * 0.82f));
}

// THE SEND ICON HE ASKED FOR (BR3-VISUAL-SPEC): "'send' button should be that
// same icon instead of the word 'send'". Never a font glyph (the merged icon
// font has no arrow in range and a missing glyph draws a hollow square, same
// reasoning as askBeckyMark above). The button keeps the caller's pushed
// green fill/hover/active; the arrow itself is always dark ink so it reads on
// green, same as every other active-green control in this file.
//
// Item 10 (round 3): a plain equilateral triangle IS a play button - Jordan
// said so directly. Drawn as an actual ARROW instead - a shaft plus a
// wide-based head that TAPERS TO A POINT (the reference's send arrow, U+27A4),
// not a symmetric triangle. A play glyph has no shaft and no notch; this one
// has both, so it cannot be mistaken for one at a glance.
static bool sendArrowButton(ImVec2 size) {
    bool clicked = ImGui::Button("##send", size);
    // GetItemRectMin/Max, not the input `size` - a 0 component there means
    // "auto" to ImGui::Button (e.g. height defaults to the frame height), so
    // using it directly collapsed the shape to a point (found live: solid
    // green square, no arrow at all). The rect ImGui actually drew is the only
    // reliable source for where the button really landed.
    ImVec2 p0 = ImGui::GetItemRectMin(), p1 = ImGui::GetItemRectMax();
    ImDrawList* dl = ImGui::GetWindowDrawList();
    ImVec2 c{ (p0.x + p1.x) * 0.5f, (p0.y + p1.y) * 0.5f };
    float s = (std::min)(p1.x - p0.x, p1.y - p0.y) * 0.30f;
    const ImU32 ink = IM_COL32(20, 20, 22, 255);
    // Shaft: a short thick horizontal bar reaching from the left edge of the
    // glyph up to the head - the part a play triangle simply does not have.
    float shaftHalfH = s * 0.28f;
    dl->AddRectFilled({ c.x - s * 0.9f, c.y - shaftHalfH }, { c.x + s * 0.05f, c.y + shaftHalfH }, ink);
    // Head: wider than the shaft (so it reads as an arrowhead, not a flag) and
    // notched at the back (two short diagonals biting into the base) - the
    // concave "V" a real send-arrow glyph has, a filled triangle does not.
    ImVec2 tip{ c.x + s * 1.05f, c.y };
    ImVec2 topBack{ c.x - s * 0.05f, c.y - s };
    ImVec2 botBack{ c.x - s * 0.05f, c.y + s };
    ImVec2 notch{ c.x + s * 0.35f, c.y };
    dl->AddTriangleFilled(topBack, tip, notch, ink);
    dl->AddTriangleFilled(notch, tip, botBack, ink);
    return clicked;
}

// ---- header: WHICH FOLDER IS OPEN, on WHICH DRIVE (safety, not decoration) ----
//
// Jordan works across two drives that must never be confused: X: is his own video
// work, E: is a REMOVABLE criminal-case evidence drive. g_folderRoot was set at
// boot and then shown NOWHERE permanent - only its basename, for a few seconds,
// inside the transient g_renderMsg line - so "which case am I in" could not be
// answered by looking. The menu bar now carries it permanently.
//
// The colour is keyed to the DRIVE LETTER, deterministically, so a given drive
// always wears the SAME colour and the wrong drive is wrong on sight before he has
// read a character. No hardcoded drive list: a new evidence volume gets a stable
// colour for free.
static uint32_t driveColor(const std::string& path) {
    char d = path.empty() ? '?' : path[0];
    if (d >= 'a' && d <= 'z') d = (char)(d - 'a' + 'A');
    if (d < 'A' || d > 'Z') return IM_COL32(0x8A, 0x8A, 0x8A, 255);   // UNC / relative
    return kPalette[(d - 'A') % 8];
}
// Black or white ink, whichever actually READS on that chip. kPalette spans gold
// through blueviolet; one fixed ink colour is illegible on half of it. Threshold
// 105 was checked against all 8 palette entries plus the grey fallback.
static ImU32 inkFor(uint32_t bg) {
    int r = bg & 0xFF, g = (bg >> 8) & 0xFF, b = (bg >> 16) & 0xFF;   // IM_COL32 packs R,G,B,A low->high
    return ((r * 299 + g * 587 + b * 114) / 1000 > 105) ? IM_COL32(0, 0, 0, 255)
                                                        : IM_COL32(255, 255, 255, 255);
}
// Trim the MIDDLE of a path to fit maxW, keeping the two load-bearing ends: the
// DRIVE LETTER and the actual folder name. A plain right-truncating ellipsis would
// eat the folder name; a left one would eat the drive. Both are the point.
// Cached, because this runs every frame and CalcTextSize per candidate is not free.
static std::string elideMiddle(const std::string& s, float maxW) {
    // Bucketed to 16px: maxW is derived from the window width, so during a resize
    // DRAG it changes every frame, every frame misses the cache, and the loop runs
    // a CalcTextSize per candidate for the whole drag. Re-fit once per 16px instead.
    maxW = floorf(maxW / 16.0f) * 16.0f;
    static std::string cacheIn, cacheOut;
    static float cacheW = -1.0f;
    if (s == cacheIn && maxW == cacheW) return cacheOut;
    std::string out = s;
    if (ImGui::CalcTextSize(s.c_str()).x > maxW) {
        const size_t head = (s.size() > 2 && s[1] == ':') ? 3 : 2;   // "X:\" or "\\"
        for (size_t tail = s.size(); tail > 4; tail--) {
            std::string cand = s.substr(0, head) + "..." + s.substr(s.size() - tail);
            if (ImGui::CalcTextSize(cand.c_str()).x <= maxW) { out = cand; break; }
            out = cand;
        }
    }
    cacheIn = s; cacheW = maxW; cacheOut = out;
    return out;
}

// ---- library helpers ----
// Sort g_videos in place per g_sortMode (B-3).
static void sortLibrary() {
    auto cmp = [](const VideoRow& a, const VideoRow& b) -> bool {
        switch (g_sortMode) {
        case 1: return a.date < b.date;                                 // oldest
        case 2: return a.name < b.name;                                // name A-Z
        case 3: return a.name > b.name;                                // name Z-A
        default: return a.date > b.date;                               // newest first
        }
    };
    std::sort(g_videos.begin(), g_videos.end(), cmp);
}

// ---------------- B-1: the library is CARDS, not a flat list ----------------
// The reference GUI (gui/BeckyReviewNative) shows each video as a tall rounded
// card: a big readable filename, a dim date/status line, and one large round
// green "+" that transcribes it. This app showed an ImGui::Selectable per video,
// which sliced the filename mid-word and offered no visible affordance at all.
// Jordan reads the screen with difficulty - a truncated name is not a cosmetic
// problem, it is the difference between finding the video and not.
// Immediate-mode: two InvisibleButtons and a handful of ImDrawList calls. No
// widget framework, no theme system.

// MIDDLE-ellipsis, not tail-ellipsis. His filenames are
// "2026-07-19_they_tried_to_kill_me.mp4" - the head (the date he scans by) and
// the tail (the extension, and the digits that tell near-duplicates apart) are
// BOTH load-bearing; the middle is the disposable part. Tail-truncation throws
// away the half that disambiguates. The card also tooltips the FULL name.
static std::string midEllipsis(const std::string& s, float maxW) {
    if (maxW <= 0.0f || ImGui::CalcTextSize(s.c_str()).x <= maxW) return s;
    size_t tail = (std::min)(s.size() / 3, (size_t)12);
    std::string tailS = s.substr(s.size() - tail);
    for (size_t head = s.size() - tail; head > 1; head--) {
        std::string out = s.substr(0, head - 1) + "..." + tailS;
        if (ImGui::CalcTextSize(out.c_str()).x <= maxW) return out;
    }
    return "..." + tailS;
}

// The reference's little rounded segmented control (.sortbtn / .smartbtn): a pill
// that is TINTED WITH ITS ACCENT COLOUR when on, outlined when off. Colour is how
// he reads state - never render these as plain grey text. The OFF state is still
// a clearly drawn outline with near-white text: a 15%-alpha ghost outline reads
// as "nothing is there" to an impaired eye, which is worse than the plain button
// this replaces.
static bool pillButton(const char* label, bool on, ImU32 accent) {
    const float S = ImGui::GetIO().FontGlobalScale;
    ImVec4 a = ImGui::ColorConvertU32ToFloat4(accent);
    const ImVec2 pad(11.0f * S, 4.0f * S);
    const char* end = label; while (*end && !(end[0] == '#' && end[1] == '#')) end++;
    ImVec2 ts = ImGui::CalcTextSize(label, end);
    bool hit = ImGui::InvisibleButton(label, ImVec2(ts.x + pad.x * 2.0f, ts.y + pad.y * 2.0f));
    bool hov = ImGui::IsItemHovered();
    ImVec2 mn = ImGui::GetItemRectMin(), mx = ImGui::GetItemRectMax();
    ImDrawList* dl = ImGui::GetWindowDrawList();
    const float r = (mx.y - mn.y) * 0.5f;   // fully-round pill
    // Item 23: OFF hover turns the TEXT + BORDER the accent colour with NO semi-transparent
    // fill highlight (the reference .smartbtn:hover). ON = accent-tinted fill + white text +
    // accent border (.smartbtn.on / a selected .sortbtn). Every state glows WHITE on hover.
    ImU32 border, txt, fill = 0;
    if (on) {
        fill   = ImGui::ColorConvertFloat4ToU32(ImVec4(a.x * 0.22f, a.y * 0.22f, a.z * 0.22f, 1.0f));
        border = accent; txt = IM_COL32(255, 255, 255, 255);
    } else if (hov) {
        border = accent; txt = accent;
    } else {
        border = IM_COL32(255, 255, 255, 130); txt = IM_COL32(204, 214, 230, 255);
    }
    if (fill) dl->AddRectFilled(mn, mx, fill, r);
    if (hov)  dl->AddRect(ImVec2(mn.x - 1, mn.y - 1), ImVec2(mx.x + 1, mx.y + 1), IM_COL32(255, 255, 255, 90), r, 0, 3.0f);
    dl->AddRect(mn, mx, border, r, 0, on ? 2.0f : 1.5f);
    dl->AddText(ImVec2(mn.x + pad.x, mn.y + pad.y), txt, label, end);
    return hit;
}

// Item 19, corrected live: a HAND-DRAWN crown, band + 3 spikes + 3 jewel dots,
// same InvisibleButton+ImDrawList technique the card's round "+" button above
// uses (that comment's own words: "DRAWN, not a glyph"). Segoe MDL2 has no
// crown glyph, so this can never regress into a hollow square.
static bool crownButton(bool on) {
    const float S = ImGui::GetIO().FontGlobalScale;
    const ImU32 accent = IM_COL32(0x00, 0xAE, 0xEF, 255);
    float d = ImGui::GetTextLineHeight() + 12.0f * S;
    ImVec2 p0 = ImGui::GetCursorScreenPos();
    ImGui::InvisibleButton("##crown", ImVec2(d, d));
    bool clicked = ImGui::IsItemClicked();
    bool hovered = ImGui::IsItemHovered();
    ImDrawList* dl = ImGui::GetWindowDrawList();
    ImVec4 a4 = ImGui::ColorConvertU32ToFloat4(accent);
    float bgA = on ? 0.28f : (hovered ? 0.16f : 0.0f);
    dl->AddRectFilled(p0, ImVec2(p0.x + d, p0.y + d),
        IM_COL32((int)(a4.x * 255), (int)(a4.y * 255), (int)(a4.z * 255), (int)(bgA * 255)), 6.0f * S);
    ImU32 shapeCol = on ? accent : IM_COL32(190, 196, 206, 255);
    float pad = d * 0.24f;
    float bx0 = p0.x + pad, bx1 = p0.x + d - pad;
    float bandY0 = p0.y + d * 0.60f, bandY1 = p0.y + d - pad;
    float topY = p0.y + pad;
    float w = bx1 - bx0, spikeW = w / 3.0f;
    dl->AddRectFilled(ImVec2(bx0, bandY0), ImVec2(bx1, bandY1), shapeCol, 1.0f * S);
    for (int k = 0; k < 3; k++) {
        float leftX = bx0 + spikeW * k, rightX = bx0 + spikeW * (k + 1), cx = (leftX + rightX) * 0.5f;
        dl->AddTriangleFilled(ImVec2(leftX, bandY0), ImVec2(rightX, bandY0), ImVec2(cx, topY), shapeCol);
        dl->AddCircleFilled(ImVec2(cx, topY), 1.6f * S, shapeCol);
    }
    return clicked;
}

// Round 5b: the reference's "trim silence" button IS the 🧹 broom emoji. Use the real
// color emoji (via the Segoe UI Emoji merge) in a normal chip so it matches exactly and
// sits at the same size/border as its toolbar neighbours - the old hand-drawn broom was
// a monochrome sketch that read as one of the "ambiguous" icons. Degrades to a word if
// the emoji font is missing, same rule as every other icon button.
static bool broomButton() {
    return ImGui::Button(ico(ICON_BROOM "##broom", "Trim Silence##broom"));
}

struct LibCardResult { bool clicked = false, dbl = false, plus = false, robot = false; };

// ONE number, so the card and the list clipper can never disagree about row height.
static float libCardHeight() {
    const float S = ImGui::GetIO().FontGlobalScale;
    return 10.0f * S * 2.0f + ImGui::GetTextLineHeight() * 2.0f + 4.0f * S;
}
static float libCardStride() { return libCardHeight() + 8.0f * ImGui::GetIO().FontGlobalScale; }

// One card. `accent` is the colour this video's clips already wear on the timeline
// (0 = none on the timeline yet). Returns what the user did; the CALLER performs
// the actions, so this helper needs nothing declared later in the file.
static LibCardResult drawLibraryCard(VideoRow& v, bool selected, bool justViewed,
                                     bool inFlight, ImU32 accent) {
    LibCardResult res;
    const float S    = ImGui::GetIO().FontGlobalScale;
    const float pad  = 10.0f * S;
    const float lh   = ImGui::GetTextLineHeight();
    const float btnD = 30.0f * S;                    // round action button
    const float h    = libCardHeight();
    const float w    = ImGui::GetContentRegionAvail().x;
    const ImVec2 p0  = ImGui::GetCursorScreenPos();
    // InvisibleButton asserts on a zero size. Bail out, but STILL advance one
    // stride - the clipper assumes a fixed height per item, and a row that
    // silently occupies none would slide every card below it out of place.
    if (w < 40.0f) { ImGui::SetCursorScreenPos(ImVec2(p0.x, p0.y + libCardStride())); return res; }
    ImDrawList* dl   = ImGui::GetWindowDrawList();
    const ImVec2 p1  = ImVec2(p0.x + w, p0.y + h);

    // --- the card body. AllowOverlap so the round button submitted below can
    //     steal the click when the cursor is over it.
    ImGui::SetNextItemAllowOverlap();
    ImGui::InvisibleButton("##card", ImVec2(w, h));
    bool hov    = ImGui::IsItemHovered();
    res.clicked = ImGui::IsItemClicked(ImGuiMouseButton_Left);
    res.dbl     = hov && ImGui::IsMouseDoubleClicked(ImGuiMouseButton_Left);
    if (ImGui::IsItemClicked(ImGuiMouseButton_Right)) ImGui::OpenPopup("rowctx");

    // Checklist 35/101: selection is a FILL, never a yellow/white outline.
    ImU32 bg = selected ? IM_COL32(28, 44, 28, 255)
             : hov      ? IM_COL32(24, 28, 22, 255)
                        : IM_COL32(20, 22, 26, 255);
    dl->AddRectFilled(p0, p1, bg, 7.0f * S);
    dl->AddRect(p0, p1, selected ? IM_COL32(0x14, 0xFF, 0x39, 255) : IM_COL32(255, 255, 255, 26),
                7.0f * S, 0, selected ? 2.0f : 1.0f);
    // Checklist 22: the transcript just viewed keeps a green outline after "back".
    if (justViewed)
        dl->AddRect(ImVec2(p0.x - 1, p0.y - 1), ImVec2(p1.x + 1, p1.y + 1),
                    IM_COL32(0x14, 0xFF, 0x39, 255), 8.0f * S, 0, 2.0f);
    // Checklist 32/36/37: the card wears the SAME colour its clips wear on the
    // timeline, so "which library video is the crimson stuff from" is one glance.
    if (accent) dl->AddRectFilled(p0, ImVec2(p0.x + 4.0f * S, p1.y), accent, 7.0f * S, ImDrawFlags_RoundCornersLeft);

    // --- text. Name is big; the sub-line is dim and never competes with it.
    const float textX = p0.x + pad + (accent ? 6.0f * S : 0.0f);
    const float btnGap = 8.0f * S;
    // Item 13: reserve room for TWO round buttons now (the blue robot + the green "+").
    const float textR = p1.x - pad - btnD * 2.0f - btnGap - 8.0f * S;
    const float nameW = textR - textX;
    if (v.dispW != nameW) { v.disp = midEllipsis(v.name, nameW); v.dispW = nameW; }
    dl->PushClipRect(ImVec2(textX, p0.y), ImVec2(textR, p1.y), true);
    dl->AddText(ImVec2(textX, p0.y + pad), IM_COL32(235, 238, 245, 255), v.disp.c_str());
    std::string sub = v.date;
    // Round 5c: "no transcript" text removed - the green [+] add button already IS the
    // "this one has no transcript yet" indicator (Jordan: redundant). Keep "transcribing...".
    const char* status = inFlight ? "transcribing..." : nullptr;
    if (status) { if (!sub.empty()) sub += "  -  "; sub += status; }
    if (!sub.empty())
        dl->AddText(ImVec2(textX, p0.y + pad + lh + 4.0f * S),
                    inFlight ? IM_COL32(0xFF, 0xD7, 0x00, 255) : IM_COL32(150, 158, 170, 255), sub.c_str());
    dl->PopClipRect();

    // --- the round action button (the reference's green "+"). DRAWN, not a glyph:
    //     the merged Segoe MDL2 range is the toolbar's, and a circled plus is two
    //     primitives - cheaper and crisper than another font dependency.
    const ImVec2 bc = ImVec2(p1.x - pad - btnD * 0.5f, (p0.y + p1.y) * 0.5f);
    ImGui::SetCursorScreenPos(ImVec2(bc.x - btnD * 0.5f, bc.y - btnD * 0.5f));
    res.plus = ImGui::InvisibleButton("##add", ImVec2(btnD, btnD)) && !inFlight;
    const bool bhov = ImGui::IsItemHovered();
    // The button is INSIDE the card, so a click on it also registered as a card
    // click above (ImGui resolves overlap after the fact). Clicking "+" must not
    // also open the transcript.
    if (bhov) { res.clicked = false; res.dbl = false; }
    const float r = btnD * 0.5f;
    if (inFlight) {
        float a0 = (float)(ImGui::GetTime() * 3.0);
        dl->PathArcTo(bc, r - 2.0f * S, a0, a0 + 4.2f, 24);
        dl->PathStroke(IM_COL32(0xFF, 0xD7, 0x00, 255), 0, 3.0f * S);
    } else if (v.hasTranscript) {
        ImU32 tc = bhov ? IM_COL32(0x14, 0xFF, 0x39, 255) : IM_COL32(170, 178, 190, 255);
        dl->AddCircle(bc, r - 1.0f, bhov ? IM_COL32(0x14, 0xFF, 0x39, 255) : IM_COL32(255, 255, 255, 60), 0, 2.0f);
        dl->AddLine(ImVec2(bc.x - r * 0.34f, bc.y + r * 0.02f), ImVec2(bc.x - r * 0.06f, bc.y + r * 0.30f), tc, 2.5f * S);
        dl->AddLine(ImVec2(bc.x - r * 0.06f, bc.y + r * 0.30f), ImVec2(bc.x + r * 0.38f, bc.y - r * 0.30f), tc, 2.5f * S);
    } else {
        dl->AddCircleFilled(bc, bhov ? r : r - 1.0f, IM_COL32(0x14, 0xFF, 0x39, 255));
        float k = r * 0.44f;
        dl->AddLine(ImVec2(bc.x - k, bc.y), ImVec2(bc.x + k, bc.y), IM_COL32(0, 0, 0, 255), 3.0f * S);
        dl->AddLine(ImVec2(bc.x, bc.y - k), ImVec2(bc.x, bc.y + k), IM_COL32(0, 0, 0, 255), 3.0f * S);
    }
    if (bhov) {
        ImGui::SetMouseCursor(ImGuiMouseCursor_Hand);
        ImGui::SetTooltip("%s", inFlight ? "transcribing..." : v.hasTranscript
            ? "re-transcribe locally (writes a SEPARATE _parakeet_transcription.srt; your original is never touched)"
            : "transcribe this video (local Parakeet ASR)");
    } else if (hov) {
        ImGui::SetTooltip("%s", v.name.c_str());   // the FULL name, never ellipsised
    }

    // Item 13: the BLUE robot, just LEFT of the green "+". Same robot shape as ask-becky,
    // blue instead of green. One click = auto-cut this video AND caption it (becky-subtitle)
    // as one pipeline, dropping the resulting clips + captions onto the timeline.
    const ImVec2 bc2 = ImVec2(bc.x - btnD - btnGap, bc.y);
    ImGui::SetCursorScreenPos(ImVec2(bc2.x - btnD * 0.5f, bc2.y - btnD * 0.5f));
    res.robot = ImGui::InvisibleButton("##robot", ImVec2(btnD, btnD)) && !inFlight;
    const bool rhov = ImGui::IsItemHovered();
    if (rhov) { res.clicked = false; res.dbl = false; }
    {
        const ImU32 blue = rhov ? IM_COL32(0x33, 0xC2, 0xF2, 255) : IM_COL32(0x00, 0xAE, 0xEF, 255);
        const float rh = btnD * 0.92f;
        drawRobotMark(dl, bc2.x - rh * 0.86f * 0.5f, bc2.y - rh * 0.5f, rh, blue);
        if (rhov) {
            ImGui::SetMouseCursor(ImGuiMouseCursor_Hand);
            ImGui::SetTooltip("Auto-cut this video AND caption it (becky-subtitle) - clips + captions onto the timeline");
        }
    }

    // Advance EXACTLY one stride so ImGuiListClipper's fixed item height matches.
    ImGui::SetCursorScreenPos(ImVec2(p0.x, p1.y + 8.0f * S));
    return res;
}

// Remember the last opened folder across launches (A-3): a tiny sidecar file,
// not the registry — cheap, and this app has no other persisted settings yet.
static std::string lastFolderStatePath() {
    const char* base = getenv("LOCALAPPDATA");
    std::string dir = std::string(base ? base : ".") + "\\becky";
    CreateDirectoryA(dir.c_str(), nullptr);
    return dir + "\\becky_review_last_folder.txt";
}
static void rememberFolder(const std::string& folder) {
    std::ofstream f(lastFolderStatePath(), std::ios::trunc);
    if (f) f << folder;
}
static std::string recallFolder() {
    std::ifstream f(lastFolderStatePath());
    std::string s; if (f) std::getline(f, s);
    return s;
}
// applyFolderView loads a FolderView (from open_folder or pick_folder) into the
// local library list and remembers it as the last-opened folder.
static void applyFolderView(const json& d, const std::string& fallbackRoot) {
    g_folderRoot = d.value("root", fallbackRoot);
    g_orphanCount = d.value("orphan_count", 0);
    g_videos.clear();
    if (d.contains("videos") && d["videos"].is_array()) {
        for (auto& v : d["videos"]) {
            VideoRow row;
            row.path = v.value("path", std::string());
            row.name = v.value("name", std::string());
            row.date = v.value("date", std::string());
            row.hasTranscript = v.value("has_transcript", false);
            if (!row.name.empty()) g_videos.push_back(row);
        }
    }
    sortLibrary();
    g_libSel = g_videos.empty() ? -1 : 0;
    g_libJustViewedIdx = -1;
    g_cueName.clear(); g_cues.clear();
    g_renderMsg = "Loaded " + std::to_string(g_videos.size()) + " videos from " + baseName(g_folderRoot);
    g_renderMsgAt = nowSec();
    if (!g_folderRoot.empty()) rememberFolder(g_folderRoot);
}
// loadFolder loads a folder into the engine and caches its view locally.
static bool loadFolder(const std::string& folder) {
    // A cold index of a large real-world case folder (hundreds of GB, many
    // sidecar files) is a multi-directory-walk filesystem scan, not a media
    // decode - it can genuinely take minutes on a big corpus. This is a
    // one-time per-session cost, so a long timeout beats a false "failed".
    json r = engineCall("open_folder", { {"folder", folder} }, 600.0);
    if (!r.value("ok", false)) { g_folderErr = r.value("error", std::string("open_folder failed")); return false; }
    g_folderErr.clear();
    applyFolderView(r.contains("data") ? r["data"] : r, folder);
    return true;
}

// reveal a file in Explorer with it pre-selected (B-7).
static void openInFileBrowser(const std::string& path) {
    std::wstring arg = L"/select,\"" + utf8ToWide(path) + L"\"";
    ShellExecuteW(nullptr, L"open", L"explorer.exe", arg.c_str(), nullptr, SW_SHOWNORMAL);
}
static std::string wideToUtf8(const std::wstring& w) {
    int n = WideCharToMultiByte(CP_UTF8, 0, w.c_str(), -1, nullptr, 0, nullptr, nullptr);
    std::string s(n > 0 ? n - 1 : 0, '\0');
    if (n > 0) WideCharToMultiByte(CP_UTF8, 0, w.c_str(), -1, &s[0], n, nullptr, nullptr);
    return s;
}
// Native Win32 "Open" dialog. Jordan edits in Vegas and opens the EXPORT, not a
// becky reel - "i STILL can't load .txt or .xml files with the load button (it
// should be able to fucking convert them)". So the edit formats (Vegas EDL TXT,
// Final Cut/Premiere XML) come FIRST in the filter list, same order the WPF app
// (gui/BeckyReviewNative) already ships, and convertEditIfNeeded (below) converts
// them transparently on the way in.
static std::string pickOpenReelFile(HWND owner) {
    wchar_t file[MAX_PATH] = L"";
    OPENFILENAMEW ofn = {};
    ofn.lStructSize = sizeof ofn;
    ofn.hwndOwner = owner;
    ofn.lpstrFilter =
        L"Edits and reels (*.txt;*.xml;*.json)\0*.txt;*.xml;*.json\0"
        L"Vegas EDL text (*.txt)\0*.txt\0"
        L"Final Cut / Premiere XML (*.xml)\0*.xml\0"
        L"Becky reel (*.json)\0*.json\0"
        L"All files\0*.*\0";
    ofn.lpstrFile = file;
    ofn.nMaxFile = MAX_PATH;
    ofn.Flags = OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST;
    ofn.lpstrTitle = L"Load Reel (or a Vegas/Final Cut edit export)";
    // 2026-07-02(5): "'load' button should have .json files at the top of the
    // 'Load reel' window, not at the bottom" - a stock GetOpenFileNameW dialog
    // can't reorder files inside one filter, but it DOES default to whichever
    // filter entry nFilterIndex names (1-based, counting the pairs above:
    // 1=mixed, 2=.txt, 3=.xml, 4=.json, 5=all). Reels are the common case,
    // edit imports the rare one, so default the dropdown to "Becky reel
    // (*.json)" - the 4th entry - instead of the mixed *.txt;*.xml;*.json
    // filter; .json is what shows first without the user touching the dropdown.
    ofn.nFilterIndex = 4;
    if (!GetOpenFileNameW(&ofn)) return {};
    std::string s = wideToUtf8(file);
    fwslash(s);
    return s;
}

static bool hasExtCI(const std::string& path, const char* ext) {
    size_t n = strlen(ext);
    if (path.size() < n) return false;
    std::string e = path.substr(path.size() - n);
    std::transform(e.begin(), e.end(), e.begin(), [](unsigned char c) { return (char)std::tolower(c); });
    return e == ext;
}

// A Vegas 'EDL TXT' (.txt) or Final Cut Pro 7 XML (.xml) export is an edit, not a
// reel - converts it via becky-otio --import into "<stem>.reel.json" beside the
// edit file and returns that path. A reel (.json) passes straight through. Runs
// SYNCHRONOUSLY on the UI thread, same as the Load Reel button's own engineCall
// below - the conversion is a fast, offline, deterministic Go pass (no model
// call), matching the WPF app's ConvertEditIfNeededAsync. On failure this shows
// why in the status line and returns "", so the caller loads nothing rather than
// a broken reel.
static std::string convertEditIfNeeded(const std::string& path) {
    if (path.empty() || hasExtCI(path, ".json")) return path;
    if (!hasExtCI(path, ".txt") && !hasExtCI(path, ".xml")) return path;

    std::string exe = "X:/AI-2/becky-tools/becky-go/bin/becky-otio.exe";
    if (!std::ifstream(exe)) {
        g_renderMsg = "Could not read that edit: becky-otio.exe not found"; g_renderMsgAt = nowSec();
        return "";
    }
    g_renderMsg = "Converting " + baseName(path) + "..."; g_renderMsgAt = nowSec();

    std::string p = path; fwslash(p);
    size_t dot = p.find_last_of('.'), slash = p.find_last_of('/');
    std::string stem = (dot != std::string::npos && (slash == std::string::npos || dot > slash)) ? p.substr(0, dot) : p;
    std::string outPath = stem + ".reel.json";

    std::wstring wexe = utf8ToWide(exe), wpath = utf8ToWide(path), wout = utf8ToWide(outPath);
    std::wstring cmd = L"\"" + wexe + L"\" --import \"" + wpath + L"\" --out \"" + wout + L"\"";
    STARTUPINFOW si{ sizeof si }; si.dwFlags = STARTF_USESHOWWINDOW; si.wShowWindow = SW_HIDE;
    PROCESS_INFORMATION pi{};
    if (!CreateProcessW(nullptr, &cmd[0], nullptr, nullptr, FALSE, CREATE_NO_WINDOW, nullptr, nullptr, &si, &pi)) {
        g_renderMsg = "Could not read that edit: could not launch becky-otio"; g_renderMsgAt = nowSec();
        return "";
    }
    WaitForSingleObject(pi.hProcess, 30000);
    CloseHandle(pi.hProcess); CloseHandle(pi.hThread);
    if (!std::ifstream(outPath).good()) {
        g_renderMsg = "Could not read that edit: " + baseName(path) + " did not convert"; g_renderMsgAt = nowSec();
        return "";
    }
    return outPath;
}

// seekToSpan puts ONE clip [a,b) of source on the (local) track and repositions
// the playhead to it, atomically (no load-then-seek race). D-3: a transcript/
// library click navigates PAUSED; a search-hit click / Play / Space starts
// playback (startPlaying=true) — shared by C-4 (search hit) and B-8 (cue click).
static void seekToSpan(const std::string& source, double a, double b, bool startPlaying,
                        double& curSec, bool& playing, double& lastComposed) {
    Clip cl; cl.in = a; cl.out = (b > a + 0.05) ? b : a + 0.05;
    cl.source = source; cl.label = baseName(source);
    paintClipFromKnownSource(cl);   // B: an audition wears its source's project colour
    g_track[0].clear(); g_track[0].push_back(cl);
    packTrack(0); recomputeDur();
    curSec = 0; playing = startPlaying; g_playingExt = playing; lastComposed = -1;
    g_quietDirty = true; peaksRequest(source, a - 1.0, b + 5.0);
    // A-1: an audition clip gets its own source's captions too (mapped to the
    // preview's 0-based time), instead of stale reel captions at wrong times.
    rebuildDerivedCaptions();
}
// Round 2, items 4/5: clicking a quote (search hit or transcript cue) or moving
// the arrow-key selection onto one now PLAYS that quote's span in the preview
// pane, with real audio - but must NEVER touch the real edit reel (that was
// round 1's destructive-wipe bug: seekToSpan above clears g_track[0] with no
// way back). Reuses the exact swap-and-restore the "Play tied clips" Q&A
// preview (G-1) already proved safe: back the real reel up once into
// g_reelBeforePreview, swap in a one-clip preview reel, and the existing
// "g_inTiedPreview && !playing" handler in the main loop restores the real
// reel the instant playback stops - pause, arrow-step elsewhere, or the clip
// running out - so the timeline is never actually mutated from the user's
// point of view.
static void previewPlaySpan(const std::string& source, double a, double b,
                             double& curSec, bool& playing, double& lastComposed) {
    if (!g_inTiedPreview) { g_reelBeforePreview = g_track[0]; g_previewFrozenPlayhead = curSec; g_inTiedPreview = true; }
    // Item 7: clicking a quote plays the video FROM the quote onward and KEEPS PLAYING
    // past it (Jordan: "continue playing the video like normal even past the point of that
    // quote"), instead of looping just the tiny a..b span. So the audition clip runs from
    // the quote start to the END of the source video - duration from the warm peaks decoder,
    // else a generous cap corrected by an async probe (same shape as playWholeVideo).
    double dur = 0;
    if (auto pk = peaksGet(source)) { std::lock_guard<std::mutex> lk(pk->mx); if (pk->ready) dur = pk->duration; }
    bool provisional = dur <= 0;
    if (provisional) dur = 3600;
    Clip cl; cl.in = a; cl.out = std::max(dur, b > a + 0.05 ? b : a + 0.05);
    cl.source = source; cl.label = baseName(source);
    paintClipFromKnownSource(cl);
    g_track[0].clear(); g_track[0].push_back(cl);
    packTrack(0); recomputeDur();
    curSec = 0; playing = true; g_playingExt = true; lastComposed = -1;
    g_quietDirty = true; peaksRequest(source, a - 1.0, b + 5.0);
    if (provisional) {
        engineCallAsync("probe", { {"source", source} }, 8.0, "checking video length...",
            [source](const json& pr) {
                double d2 = 0;
                if (pr.value("ok", false)) { const json& d = pr.contains("data") ? pr["data"] : pr; d2 = d.value("duration", 0.0); }
                if (d2 <= 0.05) return;   // unprobe-able: keep the cap
                if (g_inTiedPreview && g_track[0].size() == 1 && g_track[0][0].source == source && g_track[0][0].out > d2) {
                    g_track[0][0].out = d2; packTrack(0); recomputeDur(); g_quietDirty = true;
                }
            });
    }
    // Item 1 (round 4): a preview must NOT touch the timeline's caption lane.
    // The clip track is already drawn frozen during a preview (the frozen-render
    // swap at the drawTimeline call), but rebuilding g_caps here rewrote the
    // caption lane to the AUDITIONED clip's captions - which is exactly the
    // "previewing changes the captions on the timeline" Jordan rejected. Leaving
    // g_caps untouched keeps the lane showing the real reel's captions for the
    // whole preview, and the pane overlay is separately suppressed during a
    // preview (drawCaptionsImGui call, gated on !g_inTiedPreview) so no stale
    // real-reel caption is burned over the audition frame either.
}
// Round 5: end an active audition and put the REAL reel back - the state any timeline
// EDIT must act against. Without this, pressing S while auditioning would promote the
// preview clip onto the reel (its "no engine id" path), quietly adding a clip he never
// asked for. Returns true if it actually ended a preview. Safe to call when not
// previewing.
static bool endPreviewRestore(double& curSec, bool& playing, double& lastComposed) {
    if (!g_inTiedPreview) return false;
    g_track[0] = g_reelBeforePreview;
    g_reelBeforePreview.clear();
    g_inTiedPreview = false;
    playing = false; g_playingExt = false;
    packTrack(0); recomputeDur();
    curSec = std::min(g_previewFrozenPlayhead, g_compDur);
    lastComposed = -1; g_quietDirty = true;
    for (auto& c : g_track[0]) peaksRequest(c.source, c.in - 1.0, c.out + 5.0);
    return true;
}
// playWholeVideo puts a video's WHOLE span on the track (B-5 "spacebar plays the
// selected row"). Duration comes from the engine probe; an unprobe-able source
// degrades to a generous cap rather than blocking playback.
static void playWholeVideo(const std::string& path, double& curSec, bool& playing, double& lastComposed) {
    // D (violent-input pass, 2026-07-22): this was a SYNCHRONOUS engineCall("probe")
    // on the UI thread - Space on a library row froze the whole window for the probe
    // round trip (up to the 8s timeout on a slow source). Play must start NOW:
    // use the peaks decoder's duration when the source is warm, else the same
    // generous 3600s degrade cap this function already shipped with - and let an
    // async probe pull the out-point in when it lands a moment later.
    double dur = 0;
    if (auto pk = peaksGet(path)) { std::lock_guard<std::mutex> lk(pk->mx); if (pk->ready) dur = pk->duration; }
    bool provisional = dur <= 0;
    if (provisional) dur = 3600;
    seekToSpan(path, 0.0, dur, true, curSec, playing, lastComposed);
    if (provisional) {
        engineCallAsync("probe", { {"source", path} }, 8.0, "checking video length...",
            [path](const json& pr) {
                double d2 = 0;
                if (pr.value("ok", false)) { const json& d = pr.contains("data") ? pr["data"] : pr; d2 = d.value("duration", 0.0); }
                if (d2 <= 0.05) return;   // unprobe-able: keep the cap, exactly the old degrade
                // Only correct the clip if he is still on THIS single-clip audition.
                if (g_track[0].size() == 1 && g_track[0][0].source == path && g_track[0][0].out > d2) {
                    g_track[0][0].out = d2; packTrack(0); recomputeDur();
                    g_quietDirty = true;
                    rebuildDerivedCaptions();
                }
            });
    }
}

// openTranscript opens a video's transcript (B-8) and remembers which row was viewed.
// D (violent-input pass, 2026-07-22): was a SYNCHRONOUS engineCall on the UI thread
// with a 25s timeout - the freeze risk UI-PARITY-SPECS flagged. The row is claimed
// immediately (dedupes rapid re-clicks) and the cues land via drainAsync.
static void openTranscript(const std::string& fullVideoPath) {
    std::string name = baseName(fullVideoPath);
    if (g_cueName == name) return;       // already open (or already loading)
    g_cueErr.clear();
    g_cueName = name;
    g_cues.clear();
    g_cueSel = -1; g_cueScrollPending = false;
    g_cueMulti.clear(); g_cueAnchor = -1;   // items 10/11: indices are stale once the transcript changes
    engineCallAsync("transcript", { {"name", name} }, 25.0, "Opening transcript...",
        [name](const json& r) {
            if (g_cueName != name) return;   // he moved to another row - stale reply
            if (!r.value("ok", false)) { g_cueErr = r.value("error", std::string("transcript unavailable")); g_cues.clear(); return; }
            const json& d = r.contains("data") ? r["data"] : r;
            g_cues.clear();
            if (d.is_array()) {
                for (auto& c : d) {
                    CueRow cr;
                    cr.source = c.value("source", std::string());
                    cr.name = c.value("name", std::string());
                    cr.text = c.value("text", std::string());
                    cr.timecode = c.value("timecode", std::string());
                    cr.start = c.value("start", 0.0);
                    cr.end = c.value("end", 0.0);
                    g_cues.push_back(cr);
                }
            }
        });
}

// render Q&A cards from the engine `questions` verb (G-1).
// Parse split out of refreshCards so save_answer's reply (which carries the updated
// list) can refresh the cards WITHOUT a second blocking round trip on the UI thread.
static void cardsFromJSON(const json& d) {
    g_cards.clear();
    if (d.contains("questions") && d["questions"].is_array()) {
        for (auto& q : d["questions"]) {
            QACard card;
            card.id = q.value("id", std::string());
            card.question = q.value("question", std::string());
            card.answered = q.value("answered", false);
            card.answer = q.value("answer", std::string());
            if (q.contains("clip_ids") && q["clip_ids"].is_array())
                for (auto& cid : q["clip_ids"]) card.clipIDs.push_back(cid.get<std::string>());
            g_cards.push_back(card);
        }
    }
}
static void refreshCards() {
    g_cardsErr.clear();
    json r = engineCall("questions", {}, 8.0);
    if (!r.value("ok", false)) { g_cardsErr = r.value("error", std::string("questions unavailable")); g_cards.clear(); return; }
    cardsFromJSON(r.contains("data") ? r["data"] : r);
}

// parse one search reply (shared by keyword + qmd) into a flat Hit list.
static void parseSearchReply(bool qmd, const json& d, std::vector<Hit>& out) {
    if (qmd) {
        if (d.contains("results") && d["results"].is_array()) {
            for (auto& h : d["results"]) {
                Hit z; z.source=h.value("source",""); z.name=h.value("name",""); z.date=h.value("date","");
                z.text=h.value("text",""); z.timecode=h.value("timecode",""); z.start=h.value("start",0.0);
                z.end=h.value("end",0.0); z.score=h.value("score",0.0); z.transcriptOnly=h.value("transcript_only",false);
                out.push_back(z);
            }
        }
    } else {
        if (d.is_array()) {
            for (auto& h : d) {
                Hit z; z.source=h.value("source",""); z.name=h.value("name",""); z.date=h.value("date","");
                z.text=h.value("text",""); z.timecode=h.value("timecode",""); z.start=h.value("start",0.0);
                z.end=h.value("end",0.0); z.score=h.value("score",0.0); z.transcriptOnly=h.value("transcript_only",false);
                out.push_back(z);
            }
        } else if (d.is_object() && d.contains("results")) {
            for (auto& h : d["results"]) {
                Hit z; z.source=h.value("source",""); z.name=h.value("name",""); z.date=h.value("date","");
                z.text=h.value("text",""); z.timecode=h.value("timecode",""); z.start=h.value("start",0.0);
                z.end=h.value("end",0.0); z.score=h.value("score",0.0); z.transcriptOnly=h.value("transcript_only",false);
                out.push_back(z);
            }
        }
    }
}

// Worker thread: owns the engineCall("search"/"qmd_search", ...) round trip, which
// can take several real seconds against the actual corpus (see g_searchDoneResult's
// comment at declaration). Only the LATEST queued query is ever run - if the user
// retypes/resubmits before a slow search returns, the stale one is dropped rather
// than both racing to post a result.
static void searchWorker() {
    t_threadTag = "searchWorker";
    for (;;) {
        SearchReq req;
        {
            std::unique_lock<std::mutex> lk(g_searchQMx);
            g_searchQCv.wait(lk, [] { return g_searchQuit || !g_searchQ.empty(); });
            if (g_searchQuit) return;
            req = std::move(g_searchQ.back()); g_searchQ.clear();
        }
        SearchDone done; done.mode = req.qmd ? "qmd" : "keyword"; done.query = req.query;
        try {
            json r = req.qmd
                ? engineCall("qmd_search", { {"query", req.query} }, 25.0)
                : engineCall("search",       { {"query", req.query} }, 20.0);
            done.ok = r.value("ok", false);
            if (!done.ok) { done.err = r.value("error", std::string("search failed")); }
            else {
                const json& d = r.contains("data") ? r["data"] : r;
                if (req.qmd) done.note = d.value("mode", std::string()) + (d.contains("note") && !d["note"].get<std::string>().empty() ? (" \xE2\x80\x94 " + d["note"].get<std::string>()) : std::string());
                parseSearchReply(req.qmd, d, done.hits);
            }
        } catch (const std::exception& e) {
            done.ok = false; done.err = std::string("search exception: ") + e.what();
        }
        // I-4 measurement: log wall-clock round-trip so "<2s over the full corpus"
        // is a grepped number (crash.log), not a claim. req.t0 is stamped in
        // runSearch() on the UI thread before the request ever reaches this worker.
        done.elapsedMs = (nowSec() - req.t0) * 1000.0;
        crashLog("I-4 search query='" + done.query + "' mode=" + done.mode +
                  " ok=" + (done.ok ? "1" : "0") + " hits=" + std::to_string(done.hits.size()) +
                  " elapsedMs=" + std::to_string(done.elapsedMs));
        std::lock_guard<std::mutex> lk(g_searchDoneMx);
        g_searchDoneResult = std::move(done);
        g_searchDonePending = true;
    }
}

// enqueue a search (C-1/C-2/C-3/C-5) - returns immediately, never blocks the UI thread.
static void runSearch(bool qmd) {
    std::string q(g_searchBuf);
    if (q.empty()) { g_hits.clear(); g_searchMode.clear(); g_searchNote.clear(); return; }
    g_searching = true; g_searchErr.clear();
    beginWork("Searching...");
    {
        std::lock_guard<std::mutex> lk(g_searchQMx);
        g_searchQ.clear();
        g_searchQ.push_back({ q, qmd, nowSec() });
    }
    g_searchQCv.notify_one();
}

// Where a non-destructive add lands: right after whatever clip is under/before
// curSec (Jordan, corrected live: double-click/Enter must insert "to the RIGHT
// of the current playhead, WITHOUT deleting or replacing any existing clips" -
// never the seekToSpan-style whole-track replace). Empty track, or curSec past
// the last clip, appends. Mirrors requestCompose's own clip-lookup above.
static int insertIndexAtPlayhead(double curSec) {
    for (size_t i = 0; i < g_track[0].size(); i++) {
        Clip& c = g_track[0][i];
        double dur = c.out - c.in;
        if (curSec >= c.compStart && curSec < c.compStart + dur) return (int)i + 1;
    }
    return (int)g_track[0].size();
}

// Add ONE span [a,b) of source as a clip, inserted at the playhead - shared by
// addHitToTimeline (search hit, C-4 double-click/Enter) and addCueToTimeline
// (transcript cue, double-click). The engine is authoritative on success
// ("clip" = just the ONE new clip, cycle 27's I-2 wire-protocol fix - see
// applyAddClipDelta); a degraded/failed engine call still responds locally so
// the UI never silently no-ops.
//
// THE "EVERY NEW CLIP LAGS EVERYTHING" BUG. Jordan, feedback9, verbatim:
// "every new clip on the timeline makes everything - even my mouse - lag super
// bad for like 2 seconds."
//
// This ran engineCall("add_clip", ...) with a SIX SECOND timeout directly on the
// UI thread, from the search-hit double-click handler. For that whole span the
// message pump does not run: no repaint, no input, nothing. add_clip itself is a
// fast in-memory reel edit - but the engine's bridge DISPATCHES ONE VERB AT A
// TIME (see setOverlayMode's comment, which measured 2.9s of exactly this
// contention), so the add waits behind whatever else is in flight - a peaks
// probe, a thumbnail, a transcript - and hands that entire wait to the UI
// thread. "About two seconds", every time he adds a clip, is precisely that.
//
// Async: the click lands instantly, the reply arrives on the UI thread via
// drainAsync, and the >1s work indicator covers a genuinely slow one.
//
// THE PLAYHEAD-POSITION FIX itself is one field: add_clip's request already
// accepts an optional "at" insert index - becky-go app.go:AddClipAt inserts
// there and shifts everything after it back, non-destructively, and
// bridge.go's addClipReply already echoes that same index back as "index" for
// applyAddClipDelta to use. All of that was already built and tested
// (TestAddClipAtInsertsAfterIndex) - it just was never being SENT from here,
// so every add silently fell back to "at<0 -> append", which reads as "goes to
// the end", not "goes next to what I'm looking at".
static void addSpanToTimeline(const std::string& source, double a, double b, const std::string& label,
                              double& curSec, bool& playing, double& lastComposed) {
    b = (b > a + 0.05) ? b : a + 0.05;
    // Item 6: end any single-click AUDITION first. previewPlaySpan swaps the audition clip
    // ONTO g_track[0] in place of the real reel; without restoring it here the add lands on
    // top of the audition clip and the timeline shows TWO copies of the quote (the leftover
    // audition + the real add) - exactly Jordan's "double-click puts 2 clips" bug. No-op when
    // not previewing.
    endPreviewRestore(curSec, playing, lastComposed);
    clearScrubPreview();   // a real add always supersedes any single-click scrub proxy
    int at = insertIndexAtPlayhead(curSec);
    std::string src = source;
    // I-2 measurement: wall-clock the add_clip round trip (always-on, crash.log -
    // one line per add, negligible cost) so "<200ms, proxy building never gates
    // the add" is a grepped number, not a claim - same pattern as I-4's search
    // timing (see searchWorker). It now measures the WORKER's wait, not a stall
    // Jordan can feel, which is the entire point of the change.
    double t0 = nowSec();
    engineCallAsync("add_clip", { {"source", src}, {"in", a}, {"out", b}, {"label", label}, {"at", at} }, 6.0,
                    "Adding " + label + " to the timeline...",
                    [src, a, b, label, at, t0](const json& r) {
        crashLog("I-2 add_clip source=" + label + " elapsedMs=" + std::to_string((nowSec() - t0) * 1000.0));
        if (r.value("ok", false) && r.contains("data") && r["data"].contains("clip")) {
            applyAddClipDelta(r["data"]);
            return;
        }
        Clip cl; cl.in = a; cl.out = b; cl.source = src; cl.label = label;
        paintClipFromKnownSource(cl);   // B: same colour as the source's real clips
        int idx = std::max(0, std::min(at, (int)g_track[0].size()));
        g_track[0].insert(g_track[0].begin() + idx, cl); packTrack(0); recomputeDur();
        g_quietDirty = true; peaksRequest(src, a - 1.0, b + 5.0);
        // Bug-2 fix: this fallback used to be SILENT, leaving a clip that looked
        // real but had no engine id (every edit no-opped). Say so - and the first
        // edit on it now auto-registers it anyway (see EditReq.promote).
        g_renderMsg = "Engine didn't confirm the add - clip shown as a preview; your first edit will register it.";
        g_renderMsgAt = nowSec();
    });
}
static void addHitToTimeline(const Hit& h, double& curSec, bool& playing, double& lastComposed) {
    addSpanToTimeline(h.source, h.start, h.end, baseName(h.source), curSec, playing, lastComposed);
}
static void addCueToTimeline(const CueRow& c, double& curSec, bool& playing, double& lastComposed) {
    addSpanToTimeline(c.source, c.start, c.end, baseName(c.source), curSec, playing, lastComposed);
}
// Items 10/11: add a MULTI-SELECTION of transcript quotes in ONE undo. CONSECUTIVE selected
// cues (adjacent indices, same source) merge into a SINGLE clip - the video is continuous
// there. A SKIPPED quote (a gap in the selected indices) breaks the run, so the next cue
// becomes a SEPARATE clip and the omission shows as a cut on the timeline (Jordan's rule).
// Everything is inserted to the LEFT of the playhead as one set_clips edit (one Ctrl+Z).
static void addCuesToTimeline(const std::set<int>& sel, double& curSec, bool& playing, double& lastComposed) {
    if (sel.empty()) return;
    endPreviewRestore(curSec, playing, lastComposed);
    clearScrubPreview();
    struct Span { std::string source, label; double in, out; int lastIdx; };
    std::vector<Span> spans;
    for (int idx : sel) {   // std::set iterates ascending
        if (idx < 0 || idx >= (int)g_cues.size()) continue;
        const CueRow& c = g_cues[idx];
        if (!spans.empty() && spans.back().lastIdx == idx - 1 && spans.back().source == c.source)
            { spans.back().out = c.end; spans.back().lastIdx = idx; }   // extend a consecutive run
        else
            spans.push_back({ c.source, baseName(c.source), c.start, c.end, idx });
    }
    if (spans.empty()) return;
    int n = (int)g_track[0].size();
    int at = insertIndexAtPlayhead(curSec);
    if (at < 0) at = 0; if (at > n) at = n;
    json clips = json::array();
    auto emit = [&](const std::string& src, double in, double out, const std::string& label) {
        clips.push_back({ {"source", src}, {"in", in}, {"out", out}, {"label", label} });
    };
    for (int k = 0; k < n; k++) {
        if (k == at) for (auto& s : spans) emit(s.source, s.in, s.out, s.label);
        Clip& c = g_track[0][k];
        emit(c.source, c.in, c.out, c.label);
    }
    if (at == n) for (auto& s : spans) emit(s.source, s.in, s.out, s.label);
    engineCallAsync("set_clips", { {"clips", clips} }, 8.0, "Adding the selected quotes...",
        [](const json& r) {
            if (r.value("ok", false)) loadTimelineView(r.contains("data") ? r["data"] : r);
            else { g_renderMsg = "Add quotes failed: " + r.value("error", std::string("?")); g_renderMsgAt = nowSec(); }
        });
}
// Item 8: which cue indices START a paragraph, replicating the transcript's OWN render
// logic (a >1.5s pause, OR >= 180s since the last paragraph header) so the paragraph-jump
// keys land exactly on the visible paragraph breaks. Recomputed on demand - cheap, and
// there is no per-frame render state to read from the keyboard handler.
static std::vector<bool> cueParagraphStarts() {
    std::vector<bool> para(g_cues.size(), false);
    double lastEnd = -1000.0, lastTimestampAt = -1e18;
    const double kIntervalSec = 180.0;
    for (size_t i = 0; i < g_cues.size(); i++) {
        const CueRow& c = g_cues[i];
        bool np = (c.start - lastEnd > 1.5) || (c.start - lastTimestampAt >= kIntervalSec);
        para[i] = np;
        if (np) lastTimestampAt = c.start;
        lastEnd = c.end;
    }
    return para;
}

// Item 3c: "auto-cut" - runs becky-cut's existing silence/VAD detector on ONE
// video and drops the resulting keep-segments onto the timeline FOR HUMAN
// REVIEW (Jordan's own words) - it never renders, it proposes. The engine
// side (autocut_silence, becky-go/cmd/clip/autocut.go) already shells the
// real becky-cut and returns segments in the SOURCE video's own seconds,
// explicitly documented as ready to feed straight into a clip add - this is
// wiring, not new engine work.
//
// Splices the segments into the CURRENT reel at the playhead's index and
// pushes the whole result through set_clips in ONE call, rather than firing
// N separate add_clip calls - each add_clip computes its insert index from
// g_track[0] at THAT moment, and N of them fired back-to-back would all read
// the same stale index (the first N-1 replies have not landed yet), scrambling
// the order. One set_clips call has no such race, and (like the broomstick,
// item 3b) is one Ctrl+Z for the whole insert.
static void triggerGetCaptions();
static void applyAutoCut(const std::string& name, const std::string& source, double& curSec, double& lastComposed,
                         std::vector<std::pair<double, double>> restrictRanges = {}, bool thenCaptions = false) {
    engineCallAsync("autocut_silence", { {"name", name} }, 90.0, "Running auto-cut...",
        [source, restrictRanges, thenCaptions, &curSec, &lastComposed](const json& r) {
            if (!r.value("ok", false)) {
                g_renderMsg = "Auto-cut failed: " + r.value("error", std::string("unknown"));
                g_renderMsgAt = nowSec();
                return;
            }
            const json& d = r.contains("data") ? r["data"] : r;
            json segs = d.value("segments", json::array());
            if (!segs.is_array() || segs.empty()) {
                // Item 3c explicitly: when segments come back empty, surface the
                // plain-language `note` field (becky-cut missing, shell failure,
                // etc) - never a bare "nothing happened".
                g_renderMsg = "Auto-cut: " + d.value("note", std::string("becky-cut found nothing to keep"));
                g_renderMsgAt = nowSec();
                return;
            }
            // Build the kept-segment list (source seconds).
            std::vector<std::pair<double, double>> keep;
            for (auto& s : segs) {
                double a = s.value("in", 0.0), b = s.value("out", 0.0);
                if (b - a > 0.01) keep.push_back({ a, b });
            }
            // Item 12: if the caller passed a restrict-set (the >1 selected quotes), keep
            // ONLY the parts of each segment that fall inside a selected quote's range - i.e.
            // auto-cut the SELECTED quotes only. Empty restrict = the whole video (default).
            if (!restrictRanges.empty()) {
                std::vector<std::pair<double, double>> clipped;
                for (auto& k : keep)
                    for (auto& rg : restrictRanges) {
                        double a = (std::max)(k.first, rg.first), b = (std::min)(k.second, rg.second);
                        if (b - a > 0.01) clipped.push_back({ a, b });
                    }
                std::sort(clipped.begin(), clipped.end());
                keep.swap(clipped);
            }
            if (keep.empty()) { g_renderMsg = "Auto-cut: nothing to keep in the selection"; g_renderMsgAt = nowSec(); return; }
            int at = insertIndexAtPlayhead(curSec);
            std::vector<Clip> newTrack;
            for (int i = 0; i < at && i < (int)g_track[0].size(); i++) newTrack.push_back(g_track[0][i]);
            for (auto& k : keep) {
                Clip cl; cl.in = k.first; cl.out = k.second; cl.source = source; cl.label = baseName(source);
                paintClipFromKnownSource(cl);
                newTrack.push_back(cl);
            }
            for (int i = at; i < (int)g_track[0].size(); i++) newTrack.push_back(g_track[0][i]);
            json clips = json::array();
            for (auto& c : newTrack) clips.push_back({ {"source", c.source}, {"in", c.in}, {"out", c.out}, {"label", c.label} });
            engineCallAsync("set_clips", { {"clips", clips} }, 30.0, "Adding auto-cut segments...",
                [&lastComposed, thenCaptions](const json& r2) {
                    if (r2.value("ok", false)) {
                        loadTimelineView(r2.contains("data") ? r2["data"] : r2);
                        lastComposed = -1;
                        if (thenCaptions) {
                            // Item 13: the blue-robot pipeline - after the auto-cut clips land,
                            // build TikTok captions for them (becky-subtitle) in the same flow.
                            g_renderMsg = "Auto-cut done - building captions...";
                            triggerGetCaptions();
                        } else {
                            g_renderMsg = "Auto-cut segments added for review (Ctrl+Z undoes it)";
                        }
                    } else {
                        g_renderMsg = "Could not add auto-cut segments: " + r2.value("error", std::string("unknown"));
                    }
                    g_renderMsgAt = nowSec();
                });
        });
}

// --------------- main ---------------
int main(int argc, char** argv) {
    crashLogInit();
    crashLog("=== becky-review starting ===");
    editLogInit();
    frameTraceInit();
    scrubLogInit();
    // #0 CRITICAL: SEH-guarded - a gst_init crash must never take the window down with it.
    // GStreamer is only used by peaksProcessBatch now (one-time per-source audio decode into
    // the .bpk peak cache, E-2) - the video player is the in-process engine (D-1/step 6,
    // brought up after the window exists, below).
    g_gstAvailable.store(gstInitSEH(argc, argv) != 0);
    // cycle 23: waveforms decode via ffmpeg now (see decodeWindow) - gst is a
    // legacy runtime dependency only, its failure no longer costs any feature.
    if (g_gstAvailable.load()) crashLog("gst_init: OK (legacy - waveforms decode via ffmpeg now)");
    else crashLog("gst_init: FAILED or crashed (caught) - harmless, waveforms decode via ffmpeg");
    // I-8 / §3.4 P3: bounded background worker pool. Created AFTER gstInitSEH
    // (which must run at normal priority - see the GLib pool-spawner fix above).
    g_bgPool = new BgWorkPool();
    crashLog("bgPool: created with " + std::to_string([]{
        SYSTEM_INFO si; GetSystemInfo(&si);
        return std::max(1, (int)si.dwNumberOfProcessors / 2);
    }()) + " workers");
    std::thread(decodeWorker).detach();   // P1 fix: owns the engine seek dispatch, off the UI thread
    std::thread(editWorker).detach();     // A-4 fix: owns split/delete/trim/undo engine round-trips, off the UI thread
    std::thread(searchWorker).detach();   // I-* fix: owns search/qmd_search engine round-trips, off the UI thread

    double curSec = 0;

    // T-1 fix: CreateWindow/D3D/ImGui come up FIRST, before the engine start + reel/folder
    // load - a cold index of a large real-world case folder is a multi-minute filesystem
    // walk (see open_folder's 600s timeout above), and it used to run entirely before
    // CreateWindow even existed. To Jordan that reads as "nothing happened" for up to 15s+
    // on a double-click. Now the window is on screen almost immediately; the slow engine/
    // reel/folder work moves to a background thread (bootWork below), same "blocking call
    // off the UI thread, drained via a done-flag" shape already used for search (see I-*
    // comment on g_searchDoneResult) - except here the loop simply doesn't touch g_track/
    // g_folderView/curSec until g_bootDone is observed true, so there is nothing to drain.
    WNDCLASSEXW wc = { sizeof wc, CS_OWNDC, WndProc, 0, 0, GetModuleHandle(nullptr), nullptr, LoadCursor(nullptr, IDC_ARROW), nullptr, nullptr, L"beckyreview", nullptr };
    RegisterClassExW(&wc);
    HWND hwnd = CreateWindowW(wc.lpszClassName, L"Becky Review (native)", WS_OVERLAPPEDWINDOW, 80, 40, g_W, g_H, nullptr, nullptr, wc.hInstance, nullptr);
    g_hwnd = hwnd;
    DragAcceptFiles(hwnd, TRUE); // E-13: external video files can be dropped onto the timeline
    if (!CreateD3D(hwnd)) { fprintf(stderr, "D3D11 init failed\n"); return 4; }
    // A-2: opens maximized. ShowWindow is called TWICE on purpose - this is the
    // documented Win32 trap, and it is why the app was "not able to be used":
    // the FIRST ShowWindow of a process ignores its nCmdShow argument whenever the
    // launching process supplied STARTUPINFO with STARTF_USESHOWWINDOW (every
    // .bat / shortcut / Start-Process launcher does). The window was created,
    // D3D/the video player came up and the render loop ran - but WS_VISIBLE was never set, so
    // double-clicking the desktop button produced a live process and NOTHING on
    // screen. Only the first call is special-cased, so the second one always wins.
    ShowWindow(hwnd, SW_SHOWMAXIMIZED);
    ShowWindow(hwnd, SW_SHOWMAXIMIZED);
    UpdateWindow(hwnd);

    // Step 6: bring up the in-process video engine AFTER the window is visible
    // (device+shader init is tens of ms; media never blocks - decode threads).
    if (!engine::init()) crashLog("engine: init failed - video decode disabled, window still opening");

    IMGUI_CHECKVERSION(); ImGui::CreateContext(); ImGui::GetIO().IniFilename = nullptr; ImGui::StyleColorsDark();

    // Item 13 (round 2): "why are we still using that ugly blue... it's not in
    // our color palette." ImGui::StyleColorsDark()'s built-in accent IS that
    // blue (Button/Header/CheckMark/SliderGrab default to roughly (0.26,0.59,
    // 0.98)) - baked into every toolbar button, selected search-hit row,
    // checkbox and slider, because nothing ever overrode it. The old app
    // (gui/BeckyReviewNative - becky-review-gui2.JPG) was neon green
    // throughout; restyle onto kPalette[0] (#14FF39) as the one accent colour
    // everywhere the default theme used to reach for blue.
    {
        ImVec4 neonV = ImGui::ColorConvertU32ToFloat4(kPalette[0]);
        ImVec4 neonDim(neonV.x * 0.55f, neonV.y * 0.55f, neonV.z * 0.55f, 1.0f);
        ImVec4 neonDimmer(neonV.x * 0.32f, neonV.y * 0.32f, neonV.z * 0.32f, 0.85f);
        ImGuiStyle& acc = ImGui::GetStyle();
        acc.Colors[ImGuiCol_Button]           = neonDimmer;
        acc.Colors[ImGuiCol_ButtonHovered]    = neonDim;
        acc.Colors[ImGuiCol_ButtonActive]     = neonV;
        acc.Colors[ImGuiCol_Header]           = neonDimmer;
        acc.Colors[ImGuiCol_HeaderHovered]    = neonDim;
        acc.Colors[ImGuiCol_HeaderActive]     = neonV;
        acc.Colors[ImGuiCol_CheckMark]        = neonV;
        acc.Colors[ImGuiCol_SliderGrab]       = neonDim;
        acc.Colors[ImGuiCol_SliderGrabActive] = neonV;
        acc.Colors[ImGuiCol_FrameBgActive]    = ImVec4(neonV.x * 0.20f, neonV.y * 0.20f, neonV.z * 0.20f, 0.90f);
        acc.Colors[ImGuiCol_SeparatorHovered] = neonDim;
        acc.Colors[ImGuiCol_SeparatorActive]  = neonV;
        acc.Colors[ImGuiCol_ResizeGripHovered]= neonDim;
        acc.Colors[ImGuiCol_ResizeGripActive] = neonV;
        acc.Colors[ImGuiCol_TabHovered]       = neonDim;
        acc.Colors[ImGuiCol_TabActive]        = neonDimmer;
        acc.Colors[ImGuiCol_TextSelectedBg]   = ImVec4(neonV.x, neonV.y, neonV.z, 0.35f);
    }

    // ---- SIZED FOR JORDAN, not for a default ImGui demo ----
    //
    // He is sighted but vision-impaired, works by mouse and keyboard only, and
    // reading costs him physical effort. Next to becky-review-native (the WPF
    // app, whose sizing he has never complained about) this app's controls were
    // visibly half the size: default 1.2x text with default 4x3px frame padding
    // gave ~22px-tall buttons like "Play", "|<<", "2x". Small targets are not a
    // taste question here - they are the difference between clicking a control
    // and hunting for it.
    //
    // BECKY_UI_SCALE overrides it (e.g. "1.6") without a rebuild, because the
    // right number is whatever he can actually read, not whatever I picked.
    // Round 5b: match becky-review-native EXACTLY. That app renders its CSS at TRUE
    // pixel size (body 16px, .btn 14px, no UI upscale). The old 1.35 FontGlobalScale
    // inflated every glyph by 35% and ALSO softened it (a 14px atlas stretched 1.35x),
    // which is why Jordan - comparing the two side by side - said mine were
    // "significantly larger" and the text "looks different". Render 1:1 like the
    // reference and load the fonts at their true size so they stay crisp. BECKY_UI_SCALE
    // still overrides if he ever wants it bigger than the reference.
    float uiScale = 1.0f;
    if (const char* s = getenv("BECKY_UI_SCALE")) {
        float v = (float)atof(s);
        if (v >= 0.8f && v <= 3.0f) uiScale = v;
    }
    ImGui::GetIO().FontGlobalScale = uiScale;
    {
        ImGuiStyle& st = ImGui::GetStyle();
        st.FramePadding = ImVec2(10, 7);   // bigger click targets
        st.ItemSpacing = ImVec2(9, 7);     // room to tell controls apart
        st.ScrollbarSize = 18.0f;          // grabbable without precision aiming
        st.GrabMinSize = 14.0f;
        st.FrameRounding = 4.0f;
        // Round-5: DRAW the button/input outline. ImGui only strokes ImGuiCol_Border
        // when FrameBorderSize >= 1 - it was 0, so the #262626 border set below was
        // invisible, which is why every button looked borderless next to the
        // reference's "VERY subtle gray outline". Now every chip gets that 1px rule.
        st.FrameBorderSize = 1.0f;
        st.WindowPadding = ImVec2(12, 10);
        // Round 5c: NO window borders. They put a 1px rule on EACH panel edge, so the
        // library|video boundary showed the library's right border + the splitter bar +
        // the video's left border - Jordan's "a thick one surrounded by two small ones".
        // The splitter draws its OWN single bar (below); the timeline draws its own top
        // hairline; that is all the seam we want.
        st.WindowBorderSize = 0.0f;
        st.SeparatorTextBorderSize = 2.0f;

        // ---- BR3-VISUAL-SPEC: the reference app's palette, not ImGui's default ----
        // Default StyleColorsDark makes every plain button/input a blue-tinted
        // control (ImGuiCol_Button ~0.26,0.59,0.98 @40%; FrameBg the same blue at
        // 54%). That is the "ask-becky box is blue-tinted" bug and, per-widget,
        // where a stray green PushStyleColor crept onto ordinary toolbar buttons -
        // both read as an "accent" when they are supposed to be inert chrome. Green
        // (#14FF39) and blue (#00AEEF) are RESERVED accents applied explicitly per
        // control (pillButton, the 2x/Overlay/Skip-Quiet active states, Render
        // Selection, Send) - every ordinary button and every text input falls back
        // to this dark-neutral/dark-gray pair so it never competes with them.
        // Reference CSS exactly: .btn background = --panel #0A0A0A (DARKER than the
        // toolbar bar it sits on, which is --chrome #121212), a SOLID --line #262626
        // border so each button reads as a distinct dark chip, and WHITE bold text.
        // The old (38,38,42) button was LIGHTER than the bar - the "reversed" look.
        st.Colors[ImGuiCol_Button]          = ImVec4(10 / 255.0f, 10 / 255.0f, 10 / 255.0f, 1.0f);   // #0A0A0A panel
        st.Colors[ImGuiCol_ButtonHovered]   = ImVec4(21 / 255.0f, 21 / 255.0f, 21 / 255.0f, 1.0f);
        st.Colors[ImGuiCol_ButtonActive]    = ImVec4(15 / 255.0f, 15 / 255.0f, 15 / 255.0f, 1.0f);
        st.Colors[ImGuiCol_FrameBg]         = ImVec4(14 / 255.0f, 14 / 255.0f, 14 / 255.0f, 1.0f);   // text inputs: --panel-2 #0E0E0E
        st.Colors[ImGuiCol_FrameBgHovered]  = ImVec4(20 / 255.0f, 20 / 255.0f, 20 / 255.0f, 1.0f);
        st.Colors[ImGuiCol_FrameBgActive]   = ImVec4(24 / 255.0f, 24 / 255.0f, 24 / 255.0f, 1.0f);
        st.Colors[ImGuiCol_Border]          = ImVec4(38 / 255.0f, 38 / 255.0f, 38 / 255.0f, 1.0f);   // #262626 SOLID
        st.Colors[ImGuiCol_Text]            = ImVec4(1.0f, 1.0f, 1.0f, 1.0f);                          // #FFFFFF white
        st.Colors[ImGuiCol_WindowBg]        = ImVec4(18 / 255.0f, 18 / 255.0f, 18 / 255.0f, 1.0f);   // --chrome #121212: toolbar bar is LIGHTER than its buttons
    }

    // ---- load the icon font (see the ICON_* block near fixedButton) ----
    //
    // Segoe MDL2 Assets ships with every Windows 10 install, so this adds no
    // dependency to ship. It is MERGED into the default font at a deliberately
    // larger pixel size than the 13px text: an icon he has to squint at defeats
    // the whole point. 20px * the 1.35 UI scale = ~27px of glyph inside a ~32px
    // button, which still clears the frame padding so nothing is clipped.
    //
    // The existence check is not paranoia. ImGui 1.90.9 calls
    // IM_ASSERT_USER_ERROR on a font file it cannot read, and this binary is
    // built with /O2 but WITHOUT NDEBUG - assert() is live, so handing
    // AddFontFromFileTTF a missing path would pop an assert dialog instead of
    // degrading. Checking first keeps the fallback silent and safe.
    //
    // BASE UI FONT: real proportional Segoe UI - the SAME family the reference
    // WPF app uses - not ImGui's 13px ProggyClean bitmap. ProggyClean read as a
    // "DOS terminal" and was the #1 thing two independent eyes (Jordan, and a
    // free vision model comparing the two apps) called out as making Review 3
    // look unpolished.
    //
    // Round 3 readability pass: Jordan's impaired vision read the regular-weight
    // face as "too small and dim" even though ImGuiCol_Text is plain white (1,1,1) -
    // a thin stroke on black reads as dim to him regardless of the RGB value,
    // which is exactly the same "high-contrast is an accessibility aid" reasoning
    // ACCESSIBILITY.md already states for colour. segoeuiB.ttf (Semibold - built
    // into every Windows 10 install beside the regular face) is loaded as THE
    // DEFAULT FONT instead of regular Segoe UI, one size up (14px, was 13px), so
    // the whole app - buttons, panels, the timeline header - gets bolder+bigger
    // in this one place rather than needing a second PushFont() at every one of
    // the hundreds of call sites in this file. Oversampled 3x3 so the 1.35
    // UI-scale upscale stays crisp. Falls back to the bitmap default if the file
    // is somehow missing (never assert - the same degrade-don't-crash rule as
    // the icon load below).
    {
        // Round 5c: match becky-review-native's text WEIGHT and SIZE. The reference is
        // Segoe UI at font-weight 700 (BOLD) and, rendered by the browser, reads ~35%
        // heavier/larger than ImGui does at the same nominal px - so 16px Semibold came
        // out visibly smaller and thinner than the reference beside it. Real Segoe UI
        // BOLD at 20px, rendered 1:1 (crisp, no upscale), matches it.
        const char* uiFontPath = "C:\\Windows\\Fonts\\segoeuib.ttf";     // Segoe UI Bold (weight 700)
        bool baseLoaded = false;
        if (FILE* f = fopen(uiFontPath, "rb")) {
            fclose(f);
            ImFontConfig uiCfg;
            uiCfg.OversampleH = 3;
            uiCfg.OversampleV = 3;
            uiCfg.PixelSnapH  = false;
            baseLoaded = ImGui::GetIO().Fonts->AddFontFromFileTTF(uiFontPath, 20.0f, &uiCfg) != nullptr;
        }
        if (!baseLoaded) {
            const char* sbPath = "C:\\Windows\\Fonts\\seguisb.ttf";      // Semibold fallback
            if (FILE* f = fopen(sbPath, "rb")) {
                fclose(f);
                ImFontConfig uiCfg;
                uiCfg.OversampleH = 3;
                uiCfg.OversampleV = 3;
                uiCfg.PixelSnapH  = false;
                baseLoaded = ImGui::GetIO().Fonts->AddFontFromFileTTF(sbPath, 20.0f, &uiCfg) != nullptr;
            }
        }
        if (!baseLoaded) ImGui::GetIO().Fonts->AddFontDefault();
    }
    {
        const char* iconPath = "C:\\Windows\\Fonts\\segmdl2.ttf";
        // BECKY_ICONS=0 forces the text-label fallback. It exists so the
        // fallback is PROVABLE: "it degrades safely if the font is missing" is
        // otherwise an untestable claim on a machine where the font is always
        // present, and this is the exact path that must never ship squares.
        const char* iconsEnv = getenv("BECKY_ICONS");
        bool wantIcons = !(iconsEnv && iconsEnv[0] == '0');
        bool haveFile = false;
        if (wantIcons) { if (FILE* f = fopen(iconPath, "rb")) { haveFile = true; fclose(f); } }
        if (haveFile) {
            // MUST be static: ImGui stores this pointer and only dereferences it
            // later, when the atlas is built on the first NewFrame. A stack array
            // here is a use-after-scope - the classic crash in this exact code.
            static const ImWchar kIconRange[] = { 0xE700, 0xEDFF, 0 };
            ImFontConfig cfg;
            cfg.MergeMode = true;
            cfg.PixelSnapH = true;
            // MEASURED, not guessed. A merged font's glyphs are placed on the
            // DESTINATION font's baseline, and a 20px icon has far more ascent
            // than 13px ProggyClean - at offset 2 the tall glyphs (the runner,
            // the camera) drew their heads ABOVE the button's blue rect, onto
            // the background. Screenshotting at 8x put the runner at 719..744
            // inside a 722..753 button; centring 25.5px of glyph in a 31px
            // button needs 4.3 more units of drop, hence 6.
            cfg.GlyphOffset = ImVec2(0.0f, 4.0f);   // 20px icon centred against the 20px base font
            cfg.GlyphMinAdvanceX = 22.0f;          // uniform icon cells, so nothing jitters
            g_iconsOk = ImGui::GetIO().Fonts->AddFontFromFileTTF(iconPath, 20.0f, &cfg, kIconRange) != nullptr;

            // COLOR EMOJI (Jordan: "use the same emojis ... identical"). The reference
            // renders scissors/camera/running-man as real color emoji via the browser's
            // Segoe UI Emoji font; the monochrome Segoe MDL2 shapes above read as
            // ambiguous to him. Merge those exact emoji codepoints from Segoe UI Emoji,
            // rasterized IN COLOR by freetype (IMGUI_ENABLE_FREETYPE build + LoadColor).
            // Needs IMGUI_USE_WCHAR32 for the > U+FFFF codepoints, which the build sets.
            const char* emojiPath = "C:\\Windows\\Fonts\\seguiemj.ttf";
            if (FILE* ef = fopen(emojiPath, "rb")) {
                fclose(ef);
                static const ImWchar kEmojiRange[] = {
                    0x2702, 0x2702,   // scissors  (Split)
                    0x1F3C3, 0x1F3C3, // running man (Threshold)
                    0x1F4F7, 0x1F4F7, // camera    (Screenshot)
                    0x1F9F9, 0x1F9F9, // broom     (Trim silence)
                    0x1F441, 0x1F441, // eye       (overlay = on AND previewed)
                    0x1F4C1, 0x1F4C1, // folder    (Open Folder button)
                    0x1F50D, 0x1F50D, // magnifier (search box - item 2, reference &#128269;)
                    0
                };
                ImFontConfig ecfg;
                ecfg.MergeMode = true;
                ecfg.FontBuilderFlags = ImGuiFreeTypeBuilderFlags_LoadColor;
                ecfg.GlyphOffset = ImVec2(0.0f, 3.0f);
                ecfg.GlyphMinAdvanceX = 22.0f;
                ImGui::GetIO().Fonts->AddFontFromFileTTF(emojiPath, 18.0f, &ecfg, kEmojiRange);
            }
            // Segoe UI BOLD lacks U+2717 (✗) - it rendered as "?" on the overlay button.
            // Segoe UI Symbol has the mono ✓/✗; merge them (no LoadColor) so they tint with
            // the button's text colour like the reference's "overlay ✓".
            const char* symPath = "C:\\Windows\\Fonts\\seguisym.ttf";
            if (FILE* sf = fopen(symPath, "rb")) {
                fclose(sf);
                static const ImWchar kSymRange[] = {
                    0x2713, 0x2713,   // check
                    0x2717, 0x2717,   // x
                    0x25C0, 0x25C0,   // left triangle  (extend LEFT)
                    0x25B6, 0x25B6,   // right triangle (extend RIGHT)
                    0x23EE, 0x23EF,   // skip-to-start / play-pause
                    0x27A4, 0x27A4,   // send arrowhead
                    0
                };
                ImFontConfig scfg;
                scfg.MergeMode = true;
                ImGui::GetIO().Fonts->AddFontFromFileTTF(symPath, 20.0f, &scfg, kSymRange);
                // Item 19: the undo/redo circular arrows (U+21BA/21BB) read TOO THIN vs
                // becky-review-native. Merge them SEPARATELY, emboldened by freetype and a
                // touch larger, so their stroke weight matches the reference's bolder pair.
                static const ImWchar kUndoRedoRange[] = { 0x21BA, 0x21BB, 0 };
                ImFontConfig urcfg;
                urcfg.MergeMode = true;
                urcfg.FontBuilderFlags = ImGuiFreeTypeBuilderFlags_Bold;
                urcfg.GlyphOffset = ImVec2(0.0f, 1.0f);
                ImGui::GetIO().Fonts->AddFontFromFileTTF(symPath, 22.0f, &urcfg, kUndoRedoRange);
            }
        }
        if (!g_iconsOk) crashLog(wantIcons ? "icons: segmdl2.ttf unavailable - toolbar falls back to text labels"
                                           : "icons: disabled by BECKY_ICONS=0 - toolbar using text labels");
    }
    // Item 25: the forensic OVERLAY font is CONSOLAS (monospace, so the timecode digits
    // don't jitter frame to frame) - matching becky-review-native / the burned render
    // (drawtext.go). Loaded standalone (NOT merged) at 48px, which covers the 45px ORIG TC
    // line with headroom; drawOverlayImGui draws it downscaled to the displayed video size.
    // Degrade-don't-crash: if the file is missing the overlay just falls back to the UI font.
    {
        const char* consPath = "C:\\Windows\\Fonts\\consola.ttf";
        if (FILE* cf = fopen(consPath, "rb")) {
            fclose(cf);
            ImFontConfig ccfg;
            ccfg.OversampleH = 2;
            ccfg.OversampleV = 2;
            g_overlayFont = ImGui::GetIO().Fonts->AddFontFromFileTTF(consPath, 48.0f, &ccfg);
        }
    }

    ImGui_ImplWin32_Init(hwnd); ImGui_ImplDX11_Init(g_dev, g_ctx);

    // bootWork: engine start + reel/folder load, moved off the UI thread (see T-1 comment
    // above). Sets g_bootDone LAST, after every write - the render loop below never reads
    // g_track/g_folderView/etc. until it observes g_bootDone true, so there is no reader
    // racing this writer despite no lock (same single-writer-then-flag shape, just without
    // a done-struct to copy since the render loop simply waits instead of draining).
    g_engine.lastError.clear();
    std::thread([]() {
        t_threadTag = "bootWork";
        bool engineOk = engineStart();
        if (!engineOk) fprintf(stderr, "engine: %s\n", g_engine.lastError.c_str());
        else {
            std::thread(engineReader).detach();
            // Pre-load a reel if the caller set BECKY_REVIEW_REEL (the "Open Forensic Hits" launcher).
            if (const char* rp = getenv("BECKY_REVIEW_REEL")) {
                json r = engineCall("load_reel", { {"path", std::string(rp)} }, 30.0);
                if (r.value("ok", false)) {
                    json tv = engineCall("timeline", {}, 10.0); if (tv.value("ok", false)) loadTimelineView(tv["data"]);
                    loadCaptions(std::string(rp));   // "<reel stem>.srt" beside the reel
                }
            }
            // Boot a default folder if supplied (env); else A-3: reopen whatever
            // folder was open last session, so the app is never blank on relaunch.
            if (const char* fp = getenv("BECKY_REVIEW_FOLDER")) {
                loadFolder(std::string(fp));
            } else {
                std::string last = recallFolder();
                if (!last.empty()) loadFolder(last);
            }
            if (const char* rp = getenv("BECKY_REVIEW_REEL")) {
                json tv = engineCall("timeline", {}, 10.0);
                if (tv.value("ok", false)) loadTimelineView(tv["data"]);
            }
            // Q&A cards via BECKY_REVIEW_QUESTIONS sidecar (G-1). The engine loads them
            // itself from that env at bridge boot; here we just pull the exposed list.
            if (const char* qp = getenv("BECKY_REVIEW_QUESTIONS")) { (void)qp; refreshCards(); }
            // Which AI is actually powering the chat. Jordan's rule is anti-"are you
            // lying to me": the panel SAYS the backend rather than implying one.
            // Written here, before g_bootDone - the panel is not drawn until that
            // flag is observed, so this is the file's single-writer-then-flag shape.
            {
                json st = engineCall("status", {}, 8.0);
                if (st.value("ok", false)) {
                    const json& sd = st.contains("data") ? st["data"] : st;
                    g_backendSummary = sd.value("summary", std::string());
                    g_backendOK = sd.value("claude_cli", false) || sd.value("api", false) || sd.value("local", false);
                }
            }
        }
        // A dead engine must SAY SO, not sit on "Checking which AI is connected..."
        // forever - which is exactly the "are you lying to me" failure the status
        // card exists to prevent.
        if (g_backendSummary.empty()) {
            g_backendSummary = "The becky engine is not answering - the chat is offline.";
            g_backendOK = false;
        }

        // cycle-13 review (becky-review-3-review.md "THE ONE THING"): a fake demo
        // reel used to seed 4 clips here with engine id="" whenever no folder/reel
        // loaded - looked like a real project but silently rejected every split/
        // trim/delete with zero on-screen explanation. Killed outright: an empty
        // g_track[0] now falls straight through to the timeline's existing honest
        // "timeline empty - double-click a quote in the search results to add
        // clips, or use Load Reel" message (drawn whenever g_track[0].empty(), see
        // the timeline pane) instead of a look-alike uneditable surface.
        recomputeDur(); relabel(0); relabel(1);
        for (auto& c : g_track[0]) peaksRequest(c.source, c.in - 1.0, c.out + 5.0);
        g_bootDone.store(true);
    }).detach();

    double lastComposed = -1; bool playing = false;
    bool engineArmedOnce = false;   // forces one dispatch the instant the engine finishes init (see below)
    bool wasComposeContinuous = false;   // I-5: was the PREVIOUS frame mid-churn (playing/dragging)?
    double lastComposeContinuousEmit = -1;   // I-5: last time a continuous-churn compose dispatched (throttle to ~60/s)
    LARGE_INTEGER fq, prev; QueryPerformanceFrequency(&fq); QueryPerformanceCounter(&prev);
    // Frame pacing: a HIGH-RESOLUTION periodic waitable timer - NOT Present(1,0) and NOT
    // DwmFlush. Both of those tied idle CPU to the GPU driver / compositor: Present(1,0)'s
    // vsync-wait busy-SPUN ~12 thread-pool threads (~345% CPU idle), and DwmFlush only
    // paces while the window is the FOREGROUND composited surface - backgrounded, it
    // returns instantly and the loop spins again (measured: no-reel jumped back to 440%
    // when the window lost focus). A waitable timer paces the loop at ~60fps efficiently
    // and IDENTICALLY whether focused, background, or occluded. Windowed Present(0,0) is
    // still composited tear-free by DWM.
#ifndef CREATE_WAITABLE_TIMER_HIGH_RESOLUTION
#define CREATE_WAITABLE_TIMER_HIGH_RESOLUTION 0x00000002
#endif
    HANDLE frameTimer = CreateWaitableTimerExW(nullptr, nullptr,
        CREATE_WAITABLE_TIMER_HIGH_RESOLUTION, TIMER_MODIFY_STATE | SYNCHRONIZE);
    if (!frameTimer) frameTimer = CreateWaitableTimerW(nullptr, FALSE, nullptr);  // pre-Win10-1803 fallback
    // I-1 fix (cycle-14 reviewer finding): a PERIODIC waitable timer's repeat interval is
    // still quantized to the system timer resolution even with CREATE_WAITABLE_TIMER_HIGH_
    // RESOLUTION - that flag only sharpens the first (one-shot) due time, not a repeating
    // timer's period. Measured against a requested 16ms period: p95=17.04ms, mean=16.70ms -
    // drifting slower than the 60fps/16.667ms bar. Fix: re-arm a ONE-SHOT timer every frame
    // against a fixed phase-locked epoch (nextFrameQpc += intervalQpc, not "+16ms from now"),
    // so a single frame's overshoot self-corrects on the next wait instead of accumulating.
    // If the app falls behind by more than one interval (e.g. mid-boot or a real stall),
    // nextFrameQpc is clamped back to "now" so it free-runs instead of bursting to catch up.
    LARGE_INTEGER frameFq; QueryPerformanceFrequency(&frameFq);
    const LONGLONG intervalQpc = frameFq.QuadPart / 60;   // one 60fps tick, in QPC ticks
    LARGE_INTEGER qpcNow0; QueryPerformanceCounter(&qpcNow0);
    LONGLONG nextFrameQpc = qpcNow0.QuadPart + intervalQpc;
    auto armFrameTimer = [&]() {
        if (!frameTimer) return;
        LARGE_INTEGER qpcNow; QueryPerformanceCounter(&qpcNow);
        if (nextFrameQpc < qpcNow.QuadPart - intervalQpc) nextFrameQpc = qpcNow.QuadPart;   // clamp: don't burst-catch-up
        LONGLONG deltaQpc = nextFrameQpc - qpcNow.QuadPart;
        LONGLONG deltaHns = (deltaQpc > 0) ? (LONGLONG)((double)deltaQpc * 10000000.0 / frameFq.QuadPart) : 1;
        LARGE_INTEGER due; due.QuadPart = -deltaHns;   // negative = relative, 100ns units
        SetWaitableTimer(frameTimer, &due, 0, nullptr, nullptr, FALSE);   // one-shot; re-armed every frame below
    };
    const bool hiResTimerOn = (timeBeginPeriod(1) == TIMERR_NOERROR);   // see the timeapi.h comment above
    armFrameTimer();
    bool run = true;
    long frameIdx = 0; double traceT0 = nowSec();
    while (run) {
        // Block HERE, before input is read, not at Present() after rendering.
        //
        // This is the whole point of the waitable swap chain (see CreateD3D): the
        // thread sleeps until the GPU is actually ready for the next frame, and the
        // messages drained immediately below are therefore the FRESHEST possible
        // when they get rendered. Waiting after rendering instead - which is what
        // Present(1,0) alone does - guarantees every frame shows input that is at
        // least one refresh stale, which is precisely the lag a fast editor feels
        // as the app not keeping up with their hands.
        //
        // The 100ms cap means a lost/never-signalled handle costs a few dropped
        // frames rather than hanging the window forever.
        // Pace the frame HERE, at the top, with DwmFlush: it blocks efficiently until
        // the compositor's next vblank (an event wait, not a spin). This replaces the
        // frame-latency waitable object (unusable now we are on a bitblt swap chain) AND
        // the reason Present(1,0) is gone: Present(1,0)'s driver-side vsync-wait was
        // busy-SPINNING ~12 thread-pool worker threads at ~345% CPU while idle. With
        // DwmFlush pacing + Present(0,0) below (no driver wait), the loop still runs at
        // the refresh rate but sleeps between frames. Input is sampled right after this
        // wait, so a scrub still feels attached to the cursor. (g_frameWait is always
        // null now; the guard is kept only so a future waitable path can slot back in.)
        if (frameTimer) WaitForSingleObject(frameTimer, 100);
        nextFrameQpc += intervalQpc;   // phase-locked next tick, computed from the schedule not "now"
        armFrameTimer();               // re-arm the one-shot for that tick before doing any frame work

        MSG msg; while (PeekMessage(&msg, nullptr, 0, 0, PM_REMOVE)) { TranslateMessage(&msg); DispatchMessage(&msg); if (msg.message == WM_QUIT) run = false; }
        if (!run) break;
        LARGE_INTEGER now; QueryPerformanceCounter(&now);
        double dt = (double)(now.QuadPart - prev.QuadPart) / fq.QuadPart; prev = now;
        frameTraceTick(++frameIdx, nowSec() - traceT0, dt * 1000.0);
        g_stageT = nowSec() * 1000.0; g_stageName = "peek-message";   // reset the stage clock each frame

        g_busyHint = playing || g_gest.kind != 0;

        // #0-adjacent self-heal (cycle-13 reviewer finding): on this always-multi-
        // agent shared machine, something OUTSIDE this process can clear WS_VISIBLE
        // on our own hwnd minutes into a run - confirmed live via GetWindowLong
        // (style bit 0x10000000 missing, IsZoomed still true, process still
        // Responding) while nothing in this file ever calls ShowWindow(hwnd,
        // SW_HIDE) on the main window. Root cause unconfirmed (external script /
        // input-injection tool on the shared box is the leading theory - see
        // becky-review-3-review.md cycle 12), so rather than chase every possible
        // external actor, make the app self-heal: since we NEVER intend to hide our
        // own window, force it back whenever we notice it happened. Throttled to
        // ~2x/sec - IsWindowVisible is a cheap read, this is not a per-frame cost.
        // ponytail: blunt watchdog, not a root-cause fix; if this fires often, the
        // crashLog line below tells the next session exactly when.
        static int s_visCheckCounter = 0;
        if (++s_visCheckCounter >= 30) {
            s_visCheckCounter = 0;
            if (!IsWindowVisible(hwnd)) {
                crashLog("SELF-HEAL: hwnd lost WS_VISIBLE unexpectedly (no in-app SW_HIDE on main window) - forcing ShowWindow back");
                ShowWindow(hwnd, IsZoomed(hwnd) ? SW_SHOWMAXIMIZED : SW_SHOW);
            }
        }

        if (!g_visible) { Sleep(30); continue; }

        // T-1: bootWork (engine start + reel/folder load) is still running on its own
        // thread - paint a plain "loading" frame instead of touching g_track/g_folderView/
        // curSec, which bootWork owns exclusively until it flips g_bootDone. This is the
        // whole fix for the ~15s blank-window boot: the window, D3D and message pump are
        // already up (see main()'s "T-1 fix" comment), so Jordan sees it appear and say
        // something immediately instead of nothing for 15 seconds.
        if (!g_bootDone.load()) {
            ImGui_ImplDX11_NewFrame(); ImGui_ImplWin32_NewFrame(); ImGui::NewFrame();
            ImGui::SetNextWindowPos(ImVec2(24, 24));
            ImGui::Begin("boot", nullptr, ImGuiWindowFlags_NoDecoration | ImGuiWindowFlags_NoMove |
                          ImGuiWindowFlags_AlwaysAutoResize | ImGuiWindowFlags_NoSavedSettings |
                          ImGuiWindowFlags_NoFocusOnAppearing | ImGuiWindowFlags_NoNav);
            ImGui::Text("Loading Becky Review...");
            ImGui::End();
            ImGui::Render();
            float clr[4] = { 0.06f, 0.07f, 0.09f, 1.0f };
            g_ctx->OMSetRenderTargets(1, &g_rtv, nullptr);
            g_ctx->ClearRenderTargetView(g_rtv, clr);
            ImGui_ImplDX11_RenderDrawData(ImGui::GetDrawData());
            g_swap->Present(0, 0);   // no driver vsync-wait (that busy-spun); DwmFlush at the loop top paces us
            continue;
        }

        // keyboard (standalone focus): play / split / delete / trim / group / seek.
        // Gated on !WantCaptureKeyboard so typing in the search/ask/within boxes
        // never also splits/deletes/undoes the timeline underneath it.
        if (GetForegroundWindow() == hwnd && !ImGui::GetIO().WantCaptureKeyboard) {
            // E-6 (the differentiator): while playing, edits apply at the STOCK
            // playhead (set by clicking a clip during playback), never at the live
            // curSec - so a burst of splits never touches the running playback and
            // never forces a reload. Paused = the only playhead is curSec, as before.
            auto editT = [&]() -> double { return (playing && g_stockSec >= 0) ? g_stockSec : curSec; };
            if (GetAsyncKeyState(VK_SPACE) & 1) {
                if (GetAsyncKeyState(VK_SHIFT) & 0x8001) {   // held OR pressed since last frame - see ctrlDown
                    // D-4: Shift+Space toggles 2x playback rate (not play state).
                    g_playRate = (g_playRate > 1.5) ? 1.0 : 2.0;
                } else {
                    if (playing) stopPlayback(curSec, playing, true);   // pause = go back where he started
                    else { playing = true; g_playingExt = true; }
                }
            }
            // ENTER = STOP WHERE IT IS (item 59). The counterpart to pause: pause
            // gives him his place back, Enter keeps the frame he just heard. Both
            // go through stopPlayback so "stop" can only ever mean one thing.
            //
            // Gated with every other key here on !WantCaptureKeyboard, so Enter in
            // the search / ask / caption boxes still belongs to the text box.
            //
            // !g_libFocused because the left panel owns Enter when it has focus
            // (submit a search / add the selected hit / open a transcript). Without
            // it, Enter on a search hit would ALSO stop playback - one key, two
            // owners. g_libFocused is last frame's value, which is what the other
            // cross-panel guard in this loop already uses.
            if ((GetAsyncKeyState(VK_RETURN) & 1) && playing && !g_libFocused) stopPlayback(curSec, playing, false);
            // NOTE (fixes the "split-brain edit model" the adversarial review flagged as
            // priority #1): track 0 is no longer mutated locally by these handlers. The
            // engine's reel is the ONE model - every S/Del/O/I keypress calls a REAL verb
            // (split/remove_clip/set_trim all exist in bridge.go; "seek"/"set_select"/
            // "trim_out"/"trim_in" never did and were silent no-ops). The keypress only
            // reads local state to build the request and hands it to editWorker (A-4 fix:
            // was a synchronous NDJSON round-trip on the UI thread; now the request is
            // enqueued and the round-trip runs on editWorker's own thread) - the render
            // loop never blocks on a split/delete/trim/undo. Track 1 ("pip") has no engine
            // equivalent at all (the Go reel models one track), so its local mirror mutation
            // is deferred to the drain step below, applied only once the engine confirms.
            // B-5 gate-leak fix (cycle 11 review, priority #1): a clip built by seekToSpan
            // (Space-played whole video, a single-click search-hit preview, a cue click) has
            // NO engine id by design - it is a local-only preview, not yet on the real reel
            // (only addHitToTimeline's double-click actually calls add_clip). Before this fix,
            // pressing S/Del/O/I on one of these inserted "" into g_editsInFlight; the engine
            // correctly rejects the edit (empty id), but the drain's gate-release only fires
            // for a non-empty id, so the empty-string gate was NEVER released - permanently
            // (silently) swallowing every future edit on any local-only clip for the rest of
            // the session. Fix: treat "no id" as "not editable yet", same as "gated" - never
            // insert the gate for it in the first place. Root-caused once here, in the shared
            // check every edit key goes through, so it covers all three preview call sites.
            // Item 10 root cause: GetAsyncKeyState's "was pressed" bit is the SAME
            // primitive that needed the 60ms debounce for undo/redo (measured live:
            // 6 presses sent at 90ms apart, 3 landed, plus doubles under a slightly
            // held key) - split had only the by-id in-flight gate, no time debounce.
            // That gate is not enough: this engine's split round trip is a few ms, so
            // a HELD 's' (or a fast repeat edge) can see the FIRST split's reply land
            // and its gate clear, THEN fire again while curSec/the playhead has not
            // moved - and the second split lands on one of the two clips the first
            // split just created, at essentially the same point. That reads exactly
            // like "duplicates the clip and freaks out". Same fix, same constant,
            // same shared reasoning as queueUndo/queueRedo above.
            // Round 5: S while auditioning ENDS the audition (back to the reel) instead
            // of promoting the preview clip onto it. The GetAsyncKeyState low bit is a
            // one-shot "pressed since last read", so consuming it here also stops the
            // split handler below from firing this frame - he presses S again to split
            // on the real reel.
            if (g_inTiedPreview && (GetAsyncKeyState('S') & 1)) {
                endPreviewRestore(curSec, playing, lastComposed);
            }
            if (!g_inTiedPreview && (GetAsyncKeyState('S') & 1) && nowSec() - g_lastSplitQueued > kEditDebounceSec) {
                double t = editT();
                Clip* c = clipAtComp(0, t);
                bool noId = c && c->id.empty();
                bool gated = c && !noId && g_editsInFlight.count(c->id);
                editLog("EDGE S clip=" + (c ? c->id : std::string("none")) + " gated=" + (gated ? "1" : "0") + (noId ? " (preview-only, no engine id)" : ""));
                if (c && noId && !g_promoteInFlight) {
                    // Preview clip: promote it to a real reel clip, then split (bug-2 fix).
                    double srcT = c->in + (t - c->compStart);
                    EditReq req; req.verb = "split"; req.args = { {"at", srcT} };
                    req.kind = 0; req.t = t; req.group = g_group;
                    req.promote = true; req.pSource = c->source; req.pIn = c->in; req.pOut = c->out; req.pLabel = c->label;
                    g_promoteInFlight = true;
                    g_lastSplitQueued = nowSec();
                    queueEdit(std::move(req));
                    editLog("QUEUE promote+split src=" + c->label);
                } else if (c && !noId && !gated) {
                    double srcT = c->in + (t - c->compStart);
                    EditReq req; req.verb = "split"; req.args = { {"id", c->id}, {"at", srcT} };
                    req.kind = 0; req.t = t; req.group = g_group;
                    g_editsInFlight.insert(c->id);
                    g_lastSplitQueued = nowSec();
                    queueEdit(std::move(req));
                    editLog("QUEUE split id=" + c->id);
                }
                if (!playing) lastComposed = -1;
            }
            // Item 9 (round 2): caption split, same 'S' key as the clip split right
            // above (own debounce, so a rapid clip split can't starve it or vice
            // versa) - "function exactly like how clips on the timeline are moved
            // [...] a split should take into consideration the timestamps of each
            // word if available". They are not: Caption is {start,end,text}
            // (main.cpp ~2814) and the .srt format itself carries no per-word
            // timing anywhere in this pipeline - confirmed by reading the whole
            // caption load/save path before writing this. Falls back to the time
            // position, honestly, per the work order's own fallback clause - and
            // still snaps to the nearest WORD BOUNDARY (never mid-word) using the
            // elapsed-time fraction as a proxy for the elapsed-text fraction, which
            // is the closest approximation available without real per-word timing.
            //
            // ImGui::IsKeyPressed, NOT a second GetAsyncKeyState('S') read: found
            // live that GetAsyncKeyState's low bit is a ONE-TIME "pressed since the
            // last call to GetAsyncKeyState for this key" flag - the clip-split
            // check right above already consumed it for this frame, so a second
            // raw read here always saw 0 and this branch silently never fired.
            // ImGui's own key-edge tracking is independent bookkeeping (compares
            // this frame's io state to last frame's), so it is unaffected by how
            // many other call sites also asked about 'S' this same frame.
            if (ImGui::IsKeyPressed(ImGuiKey_S, false) && nowSec() - g_lastCapSplitQueued > kEditDebounceSec &&
                g_capSel >= 0 && g_capSel < (int)g_caps.size()) {
                g_lastCapSplitQueued = nowSec();
                double t = editT();
                Caption& cp = g_caps[g_capSel];
                if (t > cp.start + 0.08 && t < cp.end - 0.08 && cp.text.find(' ') != std::string::npos) {
                    double frac = (t - cp.start) / (cp.end - cp.start);
                    size_t n = cp.text.size();
                    size_t guess = (size_t)std::llround(frac * (double)n);
                    if (guess > n) guess = n;
                    size_t splitAt = std::string::npos;
                    for (size_t d = 0; d <= n; d++) {
                        if (d <= guess && cp.text[guess - d] == ' ') { splitAt = guess - d; break; }
                        if (guess + d < n && cp.text[guess + d] == ' ') { splitAt = guess + d; break; }
                    }
                    if (splitAt != std::string::npos && splitAt > 0 && splitAt < n - 1) {
                        Caption tail;
                        tail.start = t; tail.end = cp.end;
                        tail.text = cp.text.substr(splitAt + 1);
                        cp.end = t;
                        cp.text = cp.text.substr(0, splitAt);
                        g_caps.insert(g_caps.begin() + g_capSel + 1, tail);
                        saveCaptions();
                        editLog("CAP split idx=" + std::to_string(g_capSel) + " at t=" + std::to_string(t) +
                                " word-snap fallback (no per-word timestamps in this data model)");
                    }
                }
            }
            // Delete OR Escape. Jordan asked for Escape to delete the selected clip
            // too ("esc should also delete the selected clip", feedback4/lag) - on a
            // full-size keyboard Delete is a reach, and his hand is already near Esc
            // between takes. Same edit, same undo span; Esc is purely a second key
            // onto the identical path so the two can never drift apart.
            //
            // DELETE TARGETS THE SELECTION. This used to be clipAtComp(0, editT()) - the
            // clip under the PLAYHEAD - and never looked at g_sel at all. Once clicking a
            // clip deliberately stopped moving the playhead (his MUST-NEVER-DO: "when a
            // user clicks within a clip, the playhead should not be affected"), only half
            // the pair had been changed, so the app had TWO competing cursors and Delete
            // followed the wrong one:
            //     click clip B  ->  B highlights  ->  press Delete  ->  clip A disappears.
            // Silent, destructive, and Escape fired the same path. (The +-1f buttons below
            // already targeted g_sel, so the app disagreed with itself.)
            //
            // Now: the SELECTION is the target whenever there is one, and every selected
            // clip goes in ONE undo group (same g_group) so one Ctrl+Z puts them all back.
            // Delete alone still falls back to the clip under the playhead when nothing is
            // selected - that is the old muscle memory and it is unambiguous with an empty
            // selection. ESCAPE never falls back: it is universally "cancel", so with
            // nothing selected it must be a no-op, not a deletion of whatever the playhead
            // happens to be sitting on.
            bool delKey = (GetAsyncKeyState(VK_DELETE) & 1) != 0;
            bool escKey = (GetAsyncKeyState(VK_ESCAPE) & 1) != 0;
            if (delKey || escKey) {
                double t = editT();
                std::vector<Clip*> targets;
                if (!g_sel.empty()) {
                    for (auto& c : g_track[0]) if (g_sel.count(c.id)) targets.push_back(&c);
                } else if (delKey) {
                    if (Clip* c = clipAtComp(0, t)) targets.push_back(c);
                }
                editLog("EDGE DEL key=" + std::string(delKey ? "Del" : "Esc") +
                        " sel=" + std::to_string(g_sel.size()) + " targets=" + std::to_string(targets.size()));
                bool anyQueued = false;
                for (Clip* c : targets) {
                    if (!c || c->id.empty()) { editLog("  skip: preview-only, no engine id"); continue; }
                    if (g_editsInFlight.count(c->id)) { editLog("  skip: gated (in flight) id=" + c->id); continue; }
                    EditReq req; req.verb = "remove_clip"; req.args = { {"id", c->id} };
                    req.kind = 1; req.t = t; req.group = g_group;
                    req.rem = std::make_pair(c->compStart, c->out - c->in);
                    g_editsInFlight.insert(c->id);
                    queueEdit(std::move(req));
                    editLog("  QUEUE remove_clip id=" + c->id);
                    anyQueued = true;
                }
                // Bug-2 fix: a preview-only clip (no engine id) is not on the engine
                // reel at all, so "delete" for it is a plain LOCAL removal, not an
                // engine verb - the old "skip" left the clip on screen and Delete
                // looking broken (reproduced live before this fix). Indices collected
                // first, erased back-to-front, because the Clip* targets point into
                // the vector being edited.
                {
                    std::vector<size_t> localKill;
                    for (Clip* c : targets)
                        if (c && c->id.empty()) localKill.push_back((size_t)(c - g_track[0].data()));
                    if (!localKill.empty()) {
                        std::sort(localKill.begin(), localKill.end());
                        for (size_t k = localKill.size(); k-- > 0; )
                            if (localKill[k] < g_track[0].size()) g_track[0].erase(g_track[0].begin() + localKill[k]);
                        packTrack(0); recomputeDur();
                        g_sel.clear(); g_quietDirty = true;
                        editLog("  local remove: " + std::to_string(localKill.size()) + " preview-only clip(s)");
                    }
                }
                // The removed clips are gone; a stale selection would leave the +-1f
                // buttons and Render Selection pointing at ids that no longer exist.
                if (anyQueued && !g_sel.empty()) g_sel.clear();
                curSec = std::min(curSec, g_compDur); if (!playing) lastComposed = -1;
            }
            if (GetAsyncKeyState('O') & 1) {
                // Trim OUT: shorten the clip's tail to end at t. No engine "trim_out" verb
                // exists, but this IS exactly what set_trim(id, in, out) is for - same clip
                // id, out lowered to srcT. ONE engine edit = ONE Ctrl+Z undoes it (an
                // earlier draft used split+remove_clip here, which is two separate
                // pushUndoLocked edits - it would have cost the user two undo presses per
                // trim, the exact "phantom moves" undo bug the spec calls out by name).
                double t = editT();
                Clip* c = clipAtComp(0, t);
                double srcT = c ? c->in + (t - c->compStart) : 0;
                bool noId = c && c->id.empty();
                bool gated = c && !noId && g_editsInFlight.count(c->id);
                editLog("EDGE O clip=" + (c ? c->id : std::string("none")) + " gated=" + (gated ? "1" : "0") + (noId ? " (preview-only, no engine id)" : ""));
                if (c && noId && !g_promoteInFlight && srcT > c->in + 0.05 && srcT < c->out - 0.05) {
                    // Preview clip: promote to a real reel clip, then trim (bug-2 fix).
                    auto rem = std::make_pair(t, c->compStart + (c->out - c->in) - t);
                    EditReq req; req.verb = "set_trim"; req.args = { {"in", c->in}, {"out", srcT} };
                    req.kind = 2; req.t = t; req.group = g_group; req.rem = rem;
                    req.promote = true; req.pSource = c->source; req.pIn = c->in; req.pOut = c->out; req.pLabel = c->label;
                    g_promoteInFlight = true;
                    queueEdit(std::move(req));
                    editLog("QUEUE promote+set_trim(out) src=" + c->label);
                } else if (c && !noId && !gated && srcT > c->in + 0.05 && srcT < c->out - 0.05) {
                    auto rem = std::make_pair(t, c->compStart + (c->out - c->in) - t);
                    EditReq req; req.verb = "set_trim"; req.args = { {"id", c->id}, {"in", c->in}, {"out", srcT} };
                    req.kind = 2; req.t = t; req.group = g_group; req.rem = rem;
                    g_editsInFlight.insert(c->id);
                    queueEdit(std::move(req));
                    editLog("QUEUE set_trim(out) id=" + c->id);
                }
                curSec = std::min(curSec, g_compDur); if (!playing) lastComposed = -1;
            }
            if (GetAsyncKeyState('I') & 1) {
                // Trim IN: shorten the clip's head to start at t - same clip id, `in`
                // raised to srcT, one set_trim edit. Same one-press-undo reasoning as O;
                // also keeps the original id (a split+remove_clip version would hand the
                // surviving piece a NEW id, silently orphaning anything keyed to the old
                // one, e.g. a Q&A card tied to this clip).
                double t = editT();
                Clip* c = clipAtComp(0, t);
                double srcT = c ? c->in + (t - c->compStart) : 0;
                bool noId = c && c->id.empty();
                bool gated = c && !noId && g_editsInFlight.count(c->id);
                editLog("EDGE I clip=" + (c ? c->id : std::string("none")) + " gated=" + (gated ? "1" : "0") + (noId ? " (preview-only, no engine id)" : ""));
                if (c && noId && !g_promoteInFlight && srcT > c->in + 0.05 && srcT < c->out - 0.05) {
                    // Preview clip: promote to a real reel clip, then trim (bug-2 fix).
                    auto rem = std::make_pair(c->compStart, t - c->compStart);
                    EditReq req; req.verb = "set_trim"; req.args = { {"in", srcT}, {"out", c->out} };
                    req.kind = 3; req.t = t; req.group = g_group; req.rem = rem;
                    req.promote = true; req.pSource = c->source; req.pIn = c->in; req.pOut = c->out; req.pLabel = c->label;
                    g_promoteInFlight = true;
                    queueEdit(std::move(req));
                    editLog("QUEUE promote+set_trim(in) src=" + c->label);
                } else if (c && !noId && !gated && srcT > c->in + 0.05 && srcT < c->out - 0.05) {
                    auto rem = std::make_pair(c->compStart, t - c->compStart);
                    EditReq req; req.verb = "set_trim"; req.args = { {"id", c->id}, {"in", srcT}, {"out", c->out} };
                    req.kind = 3; req.t = t; req.group = g_group; req.rem = rem;
                    g_editsInFlight.insert(c->id);
                    queueEdit(std::move(req));
                    editLog("QUEUE set_trim(in) id=" + c->id);
                }
                curSec = std::min(curSec, g_compDur); if (!playing) lastComposed = -1;
            }
            if (GetAsyncKeyState('G') & 1) { g_group = !g_group; }
            // UNDO / REDO chords. Both go through queueUndo/queueRedo, the same
            // two functions the toolbar buttons call, so kEditDebounceSec can
            // never be present on one route and missing on the other.
            //
            // Debounce, and why it is load-bearing: with the blocking engineCall()
            // this replaced, a physical keypress could never queue a second "undo"
            // before the first's round trip finished - the block was an accidental
            // throttle. Non-blocking removes it, and undo is the one edit where a
            // spurious extra call is destructive (it walks PAST the intended edit
            // into whatever came before it - a single split plus two Ctrl+Z presses
            // emptied a whole demo reel). See kEditDebounceSec's own comment for the
            // 250ms->60ms measurement; 60ms is far under any real double-tap but
            // still absorbs a same/next-frame double edge.
            //
            // READ EACH KEY'S EDGE EXACTLY ONCE, THEN DISPATCH ON THE MODIFIERS.
            // GetAsyncKeyState's low bit means "pressed since the PREVIOUS call",
            // so reading it CONSUMES it. The undo handler used to read 'Z' first
            // and the redo chord tested 'Z' again below it - by then the edge was
            // gone, so the Ctrl+Shift+Z branch could never be reached and the
            // chord fell through to the undo above it. Ctrl+Shift+Z silently did
            // an UNDO: the exact opposite of what it advertises, on the one pair
            // of keys whose whole job is to be reversible. One read, one dispatch.
            //
            // Both Ctrl+Y and Ctrl+Shift+Z, because editors' muscle memory splits
            // between them and guessing wrong costs him a lookup.
            {
                bool zEdge = (GetAsyncKeyState('Z') & 1) != 0;
                bool yEdge = (GetAsyncKeyState('Y') & 1) != 0;
                SHORT sh = GetAsyncKeyState(VK_SHIFT);
                bool shiftDown = (sh & 0x8000) != 0 || (sh & 1) != 0;
                bool ctrl = ctrlDownForRedo();
                if (zEdge && ctrl) { if (shiftDown) queueRedo(curSec); else queueUndo(curSec); }
                if (yEdge && ctrl) queueRedo(curSec);
            }
            // MODIFIERS MUST BE READ FROM THE SAME CLOCK AS THE KEYS.
            //
            // The arrow keys above are polled with GetAsyncKeyState - real-time
            // hardware state. Ctrl was read with GetKeyState, which reports the
            // state as of the last message THIS THREAD RETRIEVED. Those are two
            // different clocks, and the gap between them is exactly one message
            // pump.
            //
            // Press Ctrl+Right quickly and the arrow's edge is seen instantly by
            // GetAsyncKeyState while GetKeyState still says Ctrl is UP, because no
            // message carrying the Ctrl-down has been processed yet. The chord
            // silently degrades to a plain Right - a one-frame nudge instead of a
            // jump to the next edit point. The faster the user, the more often it
            // happens, which is why "Ctrl+arrow sticks at the clip edge" kept
            // coming back after being "fixed" several times: nothing was wrong
            // with the jump logic, the modifier was being dropped.
            //
            // Observed directly: driving Ctrl+Right four times advanced the
            // playhead 0.2s (four single frames) instead of four edit points.
            //
            // Moving Ctrl to GetAsyncKeyState was necessary but NOT sufficient, and
            // the second half is the subtler half. The two bits mean different
            // things: 0x8000 is "held RIGHT NOW", while bit 0 is "was pressed at
            // some point since the previous call" - a LATCH. The arrow keys are
            // read with the latch (& 1), so an arrow tapped between two polls is
            // still caught. Reading Ctrl with 0x8000 alone asked whether it happened
            // to be down at the instant of the poll, so a chord pressed and released
            // inside one 16ms frame kept its arrow but lost its modifier.
            //
            // Reading BOTH bits puts the modifier on the same footing as the key it
            // modifies: held, or pressed since the last frame, either counts.
            SHORT ctrlState = GetAsyncKeyState(VK_CONTROL);
            bool ctrlDown = (ctrlState & 0x8000) != 0 || (ctrlState & 1) != 0;
            // Up/Down = zoom the timeline (item 48). Guarded on the library NOT
            // having focus, because Up/Down move the selection in that list - a
            // key must never do two things at once depending on where he last
            // clicked. He works mouse+keyboard and precise mouse work costs him
            // physically, so zoom needs to be reachable without the wheel.
            if (!g_libFocused) {
                if (GetAsyncKeyState(VK_UP) & 1) g_zoomReq = 1;
                if (GetAsyncKeyState(VK_DOWN) & 1) g_zoomReq = -1;
            }
            if (GetAsyncKeyState(VK_LEFT) & 1) {
                if (ctrlDown) {
                    // E-4: step to the previous EDIT POINT anywhere on the timeline.
                    double best; if (prevBoundary(curSec, best)) { curSec = best; playing = false; g_playingExt = false; }
                } else {
                    // D-2/E-5: one-frame step back at the clip's OWN source fps (sourceFps
                    // degrades to 30.0 until its async probe lands - see sourceFps above).
                    Clip* c = clipAtComp(0, curSec);
                    double fps = sourceFps(c ? c->source : std::string());
                    curSec = std::max(0.0, curSec - (1.0 / fps)); playing = false; g_playingExt = false;
                }
            }
            if (GetAsyncKeyState(VK_RIGHT) & 1) {
                if (ctrlDown) {
                    double best; if (nextBoundary(curSec, best)) { curSec = best; playing = false; g_playingExt = false; }
                } else {
                    Clip* c = clipAtComp(0, curSec);
                    double fps = sourceFps(c ? c->source : std::string());
                    curSec = std::min(g_compDur, curSec + (1.0 / fps)); playing = false; g_playingExt = false;
                }
            }
        } else {
            // FOCUS-BLEED FIX (bug 3, reproduced live: typing "dog" into the search
            // box logged an EDGE O when Enter submitted). GetAsyncKeyState's low bit
            // means "pressed since the LAST call" - while a text box owns the
            // keyboard the block above never polls, so every key typed into the box
            // (Enter, Space, S/O/I/Del...) stays LATCHED and replays as a transport/
            // edit action on the first un-gated frame after the box loses focus.
            // Drain the latch every gated frame so typed text can never fire
            // shortcuts later. (The latch is per-key, process-wide; we are the only
            // poller of these keys in this process.)
            static const int kEdgeKeys[] = { VK_SPACE, VK_RETURN, 'S', 'O', 'I', 'G', 'Z', 'Y',
                                             VK_DELETE, VK_ESCAPE, VK_UP, VK_DOWN, VK_LEFT, VK_RIGHT };
            for (int vk : kEdgeKeys) GetAsyncKeyState(vk);
        }

        // I-* fix: apply a finished search from searchWorker (see runSearch/searchWorker
        // above) - the UI thread only ever touches the small pending flag + result here,
        // never blocks on the engine round-trip itself.
        {
            SearchDone done; bool have = false;
            {
                std::lock_guard<std::mutex> lk(g_searchDoneMx);
                if (g_searchDonePending) { done = std::move(g_searchDoneResult); g_searchDonePending = false; have = true; }
            }
            if (have) {
                g_searching = false;
                endWork();   // pairs with beginWork("Searching...") in runSearch
                char msMsg[64]; snprintf(msMsg, sizeof(msMsg), " (%.0fms)", done.elapsedMs);
                if (!done.ok) { g_searchErr = (done.err.empty() ? "search failed" : done.err) + msMsg; g_searchMode.clear(); g_hits.clear(); }
                else { g_searchErr.clear(); g_searchMode = done.mode; g_searchNote = done.note + msMsg; g_hits = std::move(done.hits); }
                // A new result set means the old row index points at a different
                // quote - park the keyboard on the first hit, never on whatever
                // happened to be at that index before. Stamp the engine's own
                // order first so the relevance pill stays reversible, then apply
                // whichever sort is currently on.
                for (size_t i = 0; i < g_hits.size(); i++) g_hits[i].ord = (int)i;
                applyHitSort();
                g_hitScrollPending = false;
            }
        }

        // B-2: apply every transcribe finished since last frame - a targeted flip of
        // that one row's hasTranscript flag (not a full applyFolderView reload, which
        // would reset g_libSel/scroll position and regress B-6/B-4 for no reason).
        {
            std::deque<TranscribeDone> done;
            {
                std::lock_guard<std::mutex> lk(g_transcribeMx);
                done.swap(g_transcribeDoneQ);
            }
            for (auto& d : done) {
                if (d.ok) {
                    for (auto& v : g_videos) if (v.path == d.name) { v.hasTranscript = true; break; }
                    g_renderMsg = "Transcribed " + baseName(d.name);
                } else {
                    g_renderMsg = "Transcribe failed (" + baseName(d.name) + "): " + d.err;
                }
                g_renderMsgAt = nowSec();
            }
        }

        // E-13: apply every add_external finished since last frame - the engine reply
        // carries the full new TimelineView (same shape addHitToTimeline/loadTimelineView
        // already handle), so a dropped file just lands on the timeline like any other add.
        {
            std::deque<AddExternalDone> done;
            { std::lock_guard<std::mutex> lk(g_addExtMx); done.swap(g_addExtDoneQ); }
            for (auto& d : done) {
                if (d.ok && d.data.contains("clips")) {
                    loadTimelineView(d.data);
                    if (g_getCaptionsAfterAdd) { g_getCaptionsAfterAdd = false; g_renderMsg = "Added video - building captions..."; triggerGetCaptions(); }
                    else g_renderMsg = "Added dropped file to timeline";
                } else {
                    g_getCaptionsAfterAdd = false;   // add failed - do not strand the flag
                    g_renderMsg = "Add file failed: " + (d.err.empty() ? std::string("?") : d.err);
                }
                g_renderMsgAt = nowSec();
            }
        }

        // A-4 fix: apply every edit editWorker has finished since last frame, in the
        // exact order they were requested (never just the latest - a burst of splits
        // must land as that many real edits, I-6). Each completed reply already carries
        // the full "timeline" reload; the per-edit side effects that reload can't
        // express (the local track-1/"pip" mirror, curSec ripple compensation, and
        // E-1's post-split selection) are replayed here from the request's own snapshot.
        {
            std::deque<EditResult> done;
            { std::lock_guard<std::mutex> lk(g_editDoneMx); done.swap(g_editDone); }
            if (!done.empty()) editLog("UI drain: " + std::to_string(done.size()) + " replies to apply");
            for (auto& res : done) {
                editLog("drain loop entered, kind=" + std::to_string(res.req.kind) + " ok=" + (res.ok ? "1" : "0"));
                try {
                    // Release the in-flight gate (see g_editsInFlight) whether the engine
                    // accepted or rejected this edit - either way this id is resolved and
                    // the next keypress may target it (or its post-split successor) again.
                    if (res.req.kind >= 0 && res.req.kind <= 3) {
                        std::string rid = res.req.args.value("id", std::string());
                        if (!rid.empty()) g_editsInFlight.erase(rid);
                    }
                    // A promote request (add_clip + verb on a preview clip) resolved -
                    // success or failure, the next edit key may promote again.
                    if (res.req.promote) g_promoteInFlight = false;
                    editLog("drain: gate released, building reply log line");
                    std::string replyId = res.req.args.value("id", std::string());
                    editLog("drain: got id=" + replyId);
                    std::string newIdField = res.data.value("new_id", std::string());
                    editLog("drain: got new_id=" + newIdField);
                    editLog("REPLY verb=" + res.req.verb + " ok=" + (res.ok ? "1" : "0") +
                        " id=" + replyId + " new_id=" + newIdField);
                    if (!res.ok) continue;
                    if (res.data.contains("__timeline")) {
                        loadTimelineView(res.data["__timeline"]);
                        // I-6 verification bar (BUILD_1.md SS4-E-18): "split 20x rapidly, assert
                        // 0 jobs enqueued" - g_peaksJobsEnqueued only increments on a genuine
                        // cache-miss window (peaksRequest's peaksWindowFilled short-circuit).
                        // Logged here (once per edit reply, i.e. once per reload) so a live run
                        // with BECKY_REVIEW_EDIT_LOG set can grep this line and see the counter
                        // stop climbing once the reel's audio is warm.
                        editLog("loadTimelineView done, " + std::to_string(g_track[0].size()) +
                            " clips, peaksJobsEnqueued=" + std::to_string(g_peaksJobsEnqueued.load()) +
                            ", thumbJobsEnqueued=" + std::to_string(g_thumbJobsEnqueued.load()));
                    }
                    switch (res.req.kind) {
                    case 0: { // split
                        if (res.req.group) splitTrack(1, res.req.t);
                        std::string newId = res.data.value("new_id", std::string());
                        // Item 5 (round 4): after a split, select ONLY the RIGHT-of-
                        // playhead half. The engine (App.Split) keeps the original id
                        // on the LEFT half and gives the RIGHT half the new_id it
                        // returns, so selecting new_id alone - clearing everything
                        // else first - is exactly "the left half is de-selected".
                        if (!newId.empty()) { g_sel.clear(); g_sel.insert(newId); g_selAnchor = newId; emitSelect(); }
                        break;
                    }
                    case 1: // remove
                        if (res.req.group) deleteTrack(1, res.req.t);
                        rippleCurSec(curSec, res.req.rem);
                        break;
                    case 2: // trim out
                        if (res.req.group) { splitTrack(1, res.req.t); deleteTrack(1, res.req.t); }
                        rippleCurSec(curSec, res.req.rem);
                        break;
                    case 3: // trim in
                        if (res.req.group) { splitTrack(1, res.req.t); deleteTrack(1, res.req.t - 0.02); }
                        rippleCurSec(curSec, res.req.rem);
                        break;
                    case 4: default: // undo
                        break;
                    }
                    curSec = std::min(curSec, g_compDur);
                    if (!playing) lastComposed = -1;
                } catch (const std::exception& e) {
                    editLog(std::string("EXCEPTION in UI drain: ") + e.what());
                }
                editLog("drain loop iteration done");
            }
        }

        // Finished async engine verbs (add_clip, Render, Save, Export, ask,
        // apply_proposal) are delivered HERE, on the UI thread, together with
        // every other drain and BEFORE any UI code reads the model this frame.
        //
        // This used to sit in the middle of drawTimeline(), which runs at the END
        // of the frame - so a callback that swaps g_track out (add_clip's reload,
        // apply_proposal's) fired while drawTimeline was partway through reading
        // it. That was safe only by accident of where the call happened to sit.
        // Here there is nothing to be halfway through: the key handlers above have
        // finished with their Clip* pointers, and every panel below re-reads
        // g_track from scratch.
        drainAsync();
        stageMark("key-input+drainAsync");

        // WHERE THIS RUN OF PLAYBACK BEGAN (item 59). Detected centrally, as a
        // false->true transition, rather than assigned at each of the four places
        // that start playback (Space, the Play button, seekToSpan, playWholeVideo)
        // - a fifth one added later gets this for free instead of silently not
        // recording a start point. Read BEFORE the block below advances curSec, so
        // it is the frame he actually pressed play on and not one tick later.
        {
            static bool s_wasPlaying = false;
            if (playing && !s_wasPlaying) { g_playStartSec = curSec; clearScrubPreview(); }
            s_wasPlaying = playing;
        }

        // D-9 (step 6 rewrite): PLAYBACK follows the ENGINE's clock - in-process,
        // continuous, audio-master (samples actually consumed by WASAPI). The whole
        // time-pos observe/extrapolate/monotonic-guard dance existed because the old
        // mpv build reported position over a pipe at ~0.15s cadence; the engine's
        // clock is a function call, so it is simply read every frame.
        if (playing && !g_track[0].empty()) {
            if (!g_edlActive.load()) {
                engineReelEnter(curSec);
                g_capOsdShowing = false;
            } else {
                // An edit landed mid-playback: rebuild the engine's segment list and
                // resume at the same (already ripple-compensated) spot.
                if (edlTrackSig() != g_edlSigLoaded) { engineReelEnter(curSec); g_capOsdShowing = false; }
                else if (g_playRate != g_edlSpeedSet) {
                    engine::setRate(g_playRate);
                    g_edlSpeedSet = g_playRate;
                }
            }
            stageMark("reel-enter-or-edit");

            if (g_edlActive.load()) {
                double tp = engine::clockSec();
                if (tp >= 0) {
                    // tiny anti-jitter: never step BACK by less than half a frame
                    if (!(tp < curSec && curSec - tp < 0.017)) curSec = tp;
                } else {
                    curSec += dt * g_playRate;   // engine clock not up yet - keep moving
                }
            } else {
                curSec += dt * g_playRate;   // engine down: degrade to the old tick
            }
            stageMark("clock-sec");

            // E-10: below-threshold ranges are SKIPPED seamlessly during playback.
            if (g_thrOn) for (auto& r : g_quietRanges) if (curSec >= r.first && curSec < r.second) { curSec = r.second; engineReelSeek(curSec); break; }
            stageMark("quiet-range-skip");
            if (curSec >= g_compDur || engine::reelEnded()) {
                curSec = 0; engineReelSeek(0);
            }
            stageMark("loop-reseek");
        } else if (g_edlActive.load()) {
            // Stopped playing: hand the picture back to the frame-exact paused scrub path.
            engineReelExit();
            lastComposed = -1;      // force one exact recompose so the parked frame is frame-exact
            g_capOsdShowing = false;
            stageMark("reel-exit");
        }

        // Round 5 (Jordan): a preview MUST survive a pause. The old code tore the
        // preview down the instant playback stopped, which is what made the preview
        // window "go dark" on Space when there was no reel to fall back to, and made
        // pausing "return focus to the timeline". A preview is a transcript-browsing
        // mode: pause keeps the audition frame on screen (paused), Space resumes it,
        // clicking another quote auditions that one, and it only ENDS when he clicks
        // the timeline (handled in drawTimeline, which restores the reel and processes
        // the click) or does a real edit/add/load (loadTimelineView/applyAddClipDelta
        // already clear it). So there is deliberately no teardown-on-pause here.
        stageMark("tied-preview-restore");

        // P1 fix: never decode on the UI thread. Post the newest target to the decode
        // thread (non-blocking); the decode thread calls engineShowFrame, which draws
        // into the shared-texture ring engine.cpp owns - there is no frame buffer for
        // the UI thread to poll, only the pane's position/size is pushed to it each
        // frame below (center video pane block).
        // engineArmedOnce: engine::init() brings the decoder up asynchronously (device/
        // shader init takes tens of ms - see main()'s "Step 6" comment). On a paused/
        // static startup curSec never changes, so the ordinary "curSec != lastComposed"
        // gate fires exactly once, at t=0 - before the engine is necessarily ready yet -
        // and then never again, leaving the video pane permanently black even after the
        // engine comes up a moment later. Force exactly one extra dispatch the first
        // frame the engine reports available, so the pending clip always gets shown.
        bool engineReadyNow = engine::available();
        // I-5 fix: curSec churns every single frame during "playing" (main()'s own
        // dt-driven tick above) and during an active scrub-drag (g_gest.kind==1) -
        // both are continuous, so each gets a cheap keyframe seek (engineShowFrame's
        // comment has the full story on why "exact" every frame is unnecessary here).
        // The instant churn STOPS (this frame's continuous flag flips false vs. last
        // frame's), force one more dispatch even if curSec happens to be unchanged, so
        // the settle/release always lands an exact frame.
        bool composeContinuous = playing || g_gest.kind == 1;
        bool composeExact = !composeContinuous;
        bool composeSettling = wasComposeContinuous && !composeContinuous;
        // I-5 fix, part 2 (found live via the same scrub-log evidence, back when this
        // was mpv over a named pipe): a cheap keyframe seek still isn't FREE - the
        // decoder still has to do real work per seek, just less of it. This render loop
        // is otherwise uncapped (no vsync wait), so during playback/drag it was asking
        // for 200-1000+ seeks/sec. Throttling continuous dispatch to ~60/sec - matching
        // emitScrub's existing precedent for the exact same "seek flood" shape - caps
        // the request rate at what the decode thread can actually keep up with; a
        // settle/final dispatch (composeSettling) is never throttled. (The mpv-era
        // failure mode this originally guarded against - a named pipe's kernel buffer
        // silently absorbing a burst until mpv's IPC wedged - no longer exists post
        // step-6, but the same seek-cost argument still justifies the throttle.)
        // The scrub-preview (single-click on a cue/hit, item B) is a visual override
        // of this same dispatch, never a g_track[0] mutation - see previewSourceFrame's
        // comment. lastComposed deliberately does NOT track it (a preview frame is not
        // "the real curSec's frame"), so the instant it clears, curSec == lastComposed
        // would otherwise skip the real recompose forever; force one right here, in the
        // one place both flags are already in scope.
        static bool s_wasPreviewActive = false;
        if (s_wasPreviewActive && !g_scrubPreviewActive) lastComposed = -1;
        s_wasPreviewActive = g_scrubPreviewActive;
        if (g_edlActive.load()) {
            // D-9: the engine is PLAYING the reel itself - it owns position and the
            // painted frame. Dispatching scrub seeks here would drag playback backwards
            // every frame and re-create the I-5 seek flood. lastComposed is reset on
            // exit (see above), so the first paused frame still gets its exact recompose.
        } else if (g_scrubPreviewActive) {
            if (!g_scrubPreviewDispatched) {
                requestComposeSource(g_scrubPreviewSource, g_scrubPreviewSec, true);
                g_scrubPreviewDispatched = true;
            }
        } else if (composeContinuous && !composeSettling && nowSec() - lastComposeContinuousEmit < 0.016) {
            // skip this frame's dispatch - too soon since the last one
        } else if (!g_track[0].empty() && (curSec != lastComposed || (engineReadyNow && !engineArmedOnce) || composeSettling)) {
            requestCompose(curSec, composeExact); lastComposed = curSec;
            if (composeContinuous) lastComposeContinuousEmit = nowSec();
        }
        wasComposeContinuous = composeContinuous;
        if (engineReadyNow) engineArmedOnce = true;
        if (g_resize) { resizeD3D(); g_resize = false; }
        stageMark("playback-engine-block");

        ImGui_ImplDX11_NewFrame(); ImGui_ImplWin32_NewFrame(); ImGui::NewFrame();

        // #0 CRITICAL backstop (H-2/H-3 "degrade, never crash"): everything below this
        // point runs dozens of engineCall()-driven button/panel handlers that were NOT
        // individually try/catch-guarded (unlike editWorker's edit path). The undo-stack
        // bug already root-caused in this file proves the failure mode is real: one
        // uncaught json exception on this thread -> std::terminate -> abort ->
        // "becky-review.exe has stopped working" (ucrtbase.dll 0xC0000409), taking the
        // whole app down over a single bad engine reply. This wraps the whole frame's UI
        // logic so a single bad frame degrades (logged, ImGui frame still closed out via
        // Render() below) instead of killing the process Jordan is mid-edit in.
        try {

        // ---- top menu / status bar ----
        if (ImGui::BeginMainMenuBar()) {
            // Brand, matching the reference app's .brandbar: a neon diamond +
            // white "becky" + neon "review". #39FF14 is the accessibility
            // palette, not styling - do not tone it down.
            {
                // Item 14: the brand mark is now the SAME green robot that sits next to
                // "ask becky" (askBeckyMark), replacing the old neon diamond.
                askBeckyMark(ImGui::GetTextLineHeight());
            }
            ImGui::SameLine(0, 0); ImGui::TextUnformatted("becky");
            ImGui::SameLine(0, 0); ImGui::TextColored(ImVec4(0.224f, 1.0f, 0.078f, 1.0f), " review");
            // NOT TextDisabled. Both apps sit open side by side while he compares
            // them, so the "3" is the one token that has to read at a glance -
            // dimmed grey is the wrong colour for the only disambiguator on the bar.
            ImGui::SameLine(0, 0); ImGui::Text(" 3");
            ImGui::Separator();
            // Item 4: the Open Folder control is now an emoji-ONLY chip (just the folder
            // glyph, no "Open Folder" text) styled like the toolbar's emoji buttons - dark
            // fill + thin border via refBtn. Ctrl+O still opens it (handled by the key map).
            if (refBtn("\xF0\x9F\x93\x81##openfolder")) {   // folder emoji only
                // Native folder dialog via the engine (pick_folder verb on Windows).
                // Gap 4 fix: this was a 600s synchronous engineCall right on the menu
                // handler - the dialog itself runs in the ENGINE's own process, so this
                // thread was just blocked reading a pipe the whole time. Even with the
                // native picker legitimately open, OUR window's message pump wasn't
                // running, so Windows would show it as "Not Responding" - indexing a big
                // case folder afterward (see loadFolder's comment) could stretch that
                // for minutes. Same engineCallAsync shape as the toolbar buttons.
                engineCallAsync("pick_folder", {}, 600.0, "Opening folder...", [](const json& r) {
                    if (r.value("ok", false)) {
                        const json& d = r.contains("data") ? r["data"] : r;
                        if (d.value("picked", false) && d.contains("folder")) { g_folderErr.clear(); applyFolderView(d["folder"], std::string()); }
                    } else {
                        g_folderErr = r.value("error", std::string("pick_folder failed"));
                    }
                });
            }
            if (ImGui::IsItemHovered()) ImGui::SetTooltip("Open a case folder  (Ctrl+O)");
            // Item 3: the "%.2fs / %.0fs" playhead readout that used to sit next to Open
            // Folder was REMOVED - Jordan: redundant clutter (the timeline header already
            // shows clip count + duration, and the ruler shows the timecode).
            // ---- RIGHT of the bar: health flags, then WHICH FOLDER IS OPEN ----
            //
            // Right-aligned and drawn LAST so the left cluster's width can never push
            // it, and its own width can never push anything. The chip is a FILLED
            // drive-coloured badge, not text, because a line of grey path text in a
            // busy bar is exactly what he does not see.
            {
                const bool haveFolder = !g_folderRoot.empty();
                const std::string full = haveFolder ? g_folderRoot
                                                    : std::string("NO FOLDER OPEN - press Ctrl+O");
                const ImGuiStyle& st = ImGui::GetStyle();
                const float pad = st.FramePadding.x;
                const float barW = ImGui::GetWindowWidth();

                // ---- optional second badge: where a render would actually LAND ----
                // Mirrors the ENGINE's real decision chain (cmd/clip renderDirPath ->
                // reel.RenderDirFor): the first usable clip source NOT on the evidence
                // drive (E:) wins; an all-evidence timeline falls back to the browsed
                // folder if IT isn't evidence, else to the engine's temp workdir
                // (%TEMP%\becky-clip). When clips are SELECTED the selection decides -
                // Render Selection renders those sources, so the badge must follow
                // them, not the whole timeline's first clip. Shown only when the
                // destination drive differs from the folder he's browsing, so it is
                // silent normally and loud exactly when "renders go somewhere else"
                // is true - including the evidence-drive redirect the engine now does.
                std::string warnDir;
                if (haveFolder && !g_track[0].empty()) {
                    auto onEvidence = [](const std::string& s) {
                        return s.size() > 1 && s[1] == ':' && toupper((unsigned char)s[0]) == 'E';
                    };
                    std::string dest;
                    for (auto& c : g_track[0]) {
                        if (!g_sel.empty() && !g_sel.count(c.id)) continue;   // selection first
                        if (c.source.empty() || onEvidence(c.source)) continue;
                        size_t i = c.source.find_last_of("/\\");
                        if (i == std::string::npos) continue;
                        std::string dir = c.source.substr(0, i);
                        dest = (baseName(dir) == "Rendered") ? dir : dir + "\\Rendered";
                        break;
                    }
                    if (dest.empty()) {   // every usable source is on the evidence drive
                        if (!onEvidence(g_folderRoot)) dest = g_folderRoot + "\\Rendered";
                        else {
                            char tmp[MAX_PATH]; DWORD n = GetTempPathA(MAX_PATH, tmp);
                            dest = (n > 0 && n < MAX_PATH) ? std::string(tmp) + "becky-clip"
                                                           : std::string("C:\\becky-clip");
                        }
                    }
                    if (dest.size() > 1 && toupper((unsigned char)dest[0]) != toupper((unsigned char)g_folderRoot[0]))
                        warnDir = dest;
                }
                const std::string warnTxt = warnDir.empty() ? std::string()
                                                            : std::string("renders -> ") + warnDir.substr(0, 2);

                // measure everything, then place once
                const std::string shown = elideMiddle(full, (std::max)(140.0f, barW * 0.45f));
                const ImVec2 ts = ImGui::CalcTextSize(shown.c_str());
                const float chipW = ts.x + pad * 2;
                float x = barW - chipW - pad;
                if (!warnTxt.empty()) x -= ImGui::CalcTextSize(warnTxt.c_str()).x + pad * 3;
                if (!g_engine.alive)          x -= ImGui::CalcTextSize("ENGINE DOWN").x + pad * 2;
                if (!engine::available())   x -= ImGui::CalcTextSize("VIDEO DOWN").x + pad * 2;
                if (x > ImGui::GetCursorPosX()) ImGui::SetCursorPosX(x);

                if (!g_engine.alive) { ImGui::TextColored(ImVec4(1, 0.25f, 0.25f, 1), "ENGINE DOWN"); ImGui::SameLine(); }
                if (!engine::available()) { ImGui::TextColored(ImVec4(1, 0.25f, 0.25f, 1), "VIDEO DOWN"); ImGui::SameLine(); }

                // one badge painter, used for the warning and the folder chip
                auto badge = [&](const char* txt, uint32_t bg, const char* tip, const char* openPath) {
                    const ImVec2 t = ImGui::CalcTextSize(txt);
                    const ImVec2 p = ImGui::GetCursorScreenPos();
                    ImGui::InvisibleButton(txt, ImVec2(t.x + pad * 2, t.y));   // same height as a Text item
                    ImDrawList* dl = ImGui::GetWindowDrawList();
                    const ImVec2 a(p.x, p.y - 3), b(p.x + t.x + pad * 2, p.y + t.y + 3);
                    dl->AddRectFilled(a, b, bg, st.FrameRounding);
                    if (ImGui::IsItemHovered()) {
                        dl->AddRect(a, b, IM_COL32(255, 255, 255, 255), st.FrameRounding, 0, 2.0f);
                        ImGui::SetTooltip("%s", tip);
                    }
                    dl->AddText(ImVec2(p.x + pad, p.y), inkFor(bg), txt);
                    // Hand the PATH to ShellExecute, not "explorer.exe" + the path as
                    // an argument: unquoted, "X:\Case Files\Jan" splits at the space
                    // and Explorer opens Documents instead. His folders have spaces.
                    if (openPath && *openPath && ImGui::IsItemClicked())
                        ShellExecuteW(nullptr, L"open", utf8ToWide(openPath).c_str(), nullptr, nullptr, SW_SHOWNORMAL);
                };

                if (!warnTxt.empty()) {
                    const std::string tip = "Renders do NOT go to the folder you're browsing.\n"
                                            + std::string(g_sel.empty() ? "" : "(showing where the SELECTED clips render)\n")
                                            + "A render will land in:\n" + warnDir +
                                            "\n(click to open it in Explorer)";
                    badge(warnTxt.c_str(), driveColor(warnDir), tip.c_str(), warnDir.c_str());
                    ImGui::SameLine();
                }
                const uint32_t bg = haveFolder ? driveColor(g_folderRoot) : IM_COL32(0xDC, 0x14, 0x3C, 255);
                const std::string tip = haveFolder ? (full + "\n(click to open this folder in Explorer)")
                                                   : std::string("No case folder is open. Press Ctrl+O.");
                badge(shown.c_str(), bg, tip.c_str(), haveFolder ? g_folderRoot.c_str() : nullptr);
            }
            ImGui::EndMainMenuBar();
        }
        stageMark("menu-bar");

        // ---- LAYOUT: four panels that TILE, with no two claiming the same pixel.
        //
        // They used to be positioned independently and overlapped three ways at
        // once (seen on screen 2026-07-20, next to becky-review-native):
        //   * library and qa were FULL height while the timeline was FULL width,
        //     so the timeline's ruler was drawn underneath both side panels;
        //   * every panel started at y=0, which is UNDER the main menu bar, so
        //     each panel's own heading was hidden behind the menu;
        //   * the library was a flat 22% of the width - 281px at the default
        //     1280 - which sliced its own filenames and buttons mid-character
        //     ("2026-07-19_they_tried_t", "Smart (qmd)", "C").
        // Jordan reads the screen with difficulty; clipped text is not a cosmetic
        // problem for him, it is the difference between usable and not.
        //
        // One rect each, derived from one set of numbers, so they cannot drift
        // apart again. Side panels get a PIXEL FLOOR as well as a percentage so
        // they stay readable when the window is small.
        const float menuH = ImGui::GetFrameHeight();          // main menu bar
        const float availH = (float)g_H - menuH;
        const float qaW = (std::max)(300.0f, (float)g_W * 0.22f);
        // Floor raised by the ~30px header row added below (clip count + zoom
        // readout) so the lane keeps the same usable height it had before it:
        // laneH must clear 70 for thumbnails and lanesH must clear 90 for the
        // caption lane, both of which the row would otherwise push under at the
        // old 180 floor. Bites below a ~847px window height - including the
        // default 800px window - where the video pane loses about 12px.
        const float timelineH = (std::max)(212.0f, availH * 0.26f);
        const float topH = availH - timelineH;
        const float topY = menuH;
        // Item 7: the library pane used to be draggable to widen it - a regression
        // dropped it to a flat, non-adjustable 22% of the window, which sliced
        // long titles with no way to fix it. g_libW is the user-set width
        // (adjusted only by dragging the splitter below); clamped every frame so
        // a shrunk window can never hide the video pane or push the splitter
        // off-screen. -1 means "never dragged this session" -> the same default
        // percentage as before.
        const float libWDefault = (std::max)(320.0f, (float)g_W * 0.22f);
        static float g_libW = -1.0f;   // function-local static: persists frame to frame
        if (g_libW < 0.0f) g_libW = libWDefault;
        const float libWMin = 320.0f;
        const float libWMax = (std::max)(libWMin, (float)g_W - qaW - 240.0f);   // video pane keeps its own floor
        g_libW = (std::min)((std::max)(g_libW, libWMin), libWMax);
        const float libW = g_libW;
        const float vidW = (std::max)(240.0f, (float)g_W - libW - qaW);
        // The splitter is a THIN STRIP carved out of the boundary, not an overlay
        // on top of it - "library" stops splitHalf short of libW and "video"
        // starts splitHalf past it, so the strip is never covered by either
        // neighbour's window and the click can never land on the wrong one
        // regardless of ImGui's internal window z-order. (An earlier version
        // tried an OVERLAPPING 8px window instead and relied on submission order
        // to win the overlap - measured live: it never actually received a
        // click, submission order was not a reliable way to win a hit-test.)
        const float splitW = 14.0f, splitHalf = splitW * 0.5f;   // a generous grab width - he has impaired vision

        // ---- left panel: library / search / transcript (B, C) ----
        ImGui::SetNextWindowPos({ 0, topY }); ImGui::SetNextWindowSize({ libW - splitHalf, topH });
        if (ImGui::Begin("library", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus)) {
            bool libFocusedNow = ImGui::IsWindowFocused(ImGuiFocusedFlags_RootAndChildWindows);
            // Round 5c: "Library / Search" header removed (Jordan: redundant) - the search
            // box + file list speak for themselves, matching becky-review-native.
            // ONE search row, like the reference: [magnifier][box][smart pill][x].
            // Three same-size SmallButtons ("Search" / "Smart (qmd)" / "Clear") read
            // as three equal choices and the middle one got clipped at 320px.
            {
                const float S  = ImGui::GetIO().FontGlobalScale;
                const float fh = ImGui::GetFrameHeight();
                ImDrawList* dl = ImGui::GetWindowDrawList();

                ImVec2 mp = ImGui::GetCursorScreenPos();
                if (ImGui::InvisibleButton("##mag", ImVec2(fh, fh))) runSearch(g_smartSearch);
                bool mh = ImGui::IsItemHovered();
                // Item 2: the ACTUAL magnifier emoji (U+1F50D, reference &#128269;), color-
                // rendered from Segoe UI Emoji, replacing the old hand-drawn circle+handle.
                const char* magEmoji = "\xF0\x9F\x94\x8D";
                ImVec2 msz = ImGui::CalcTextSize(magEmoji);
                dl->AddText(ImVec2(mp.x + (fh - msz.x) * 0.5f, mp.y + (fh - msz.y) * 0.5f),
                            IM_COL32(255, 255, 255, 255), magEmoji);
                if (mh) ImGui::SetTooltip("search every transcript in this folder (Enter)");
                ImGui::SameLine(0, 6 * S);

                float pillW  = ImGui::CalcTextSize("smart").x + 22.0f * S;
                float clearW = fh;
                ImGui::SetNextItemWidth((std::max)(60.0f, ImGui::GetContentRegionAvail().x - pillW - clearW - 14.0f * S));
                if (ImGui::InputTextWithHint("##search", "search all transcripts", g_searchBuf, sizeof g_searchBuf,
                                             ImGuiInputTextFlags_EnterReturnsTrue))
                    runSearch(g_smartSearch);
                inputFocusBorder();
                ImGui::SameLine(0, 6 * S);

                // Checklist 20: a TOGGLE, blue when on, so single-word keyword search
                // stays one click away and Enter always runs the armed mode.
                if (pillButton("smart", g_smartSearch, IM_COL32(0x00, 0xAE, 0xEF, 255))) {
                    g_smartSearch = !g_smartSearch;
                    if (g_searchBuf[0]) runSearch(g_smartSearch);
                }
                if (ImGui::IsItemHovered())
                    ImGui::SetTooltip("%s", g_smartSearch ? "smart search ON - qmd finds meaning, not just the exact word"
                                                          : "smart search OFF - exact keyword match");
                ImGui::SameLine(0, 4 * S);

                ImVec2 xp = ImGui::GetCursorScreenPos();
                if (ImGui::InvisibleButton("##clr", ImVec2(clearW, fh)))
                    { g_searchBuf[0] = 0; g_hits.clear(); g_searchMode.clear(); g_searchErr.clear(); }
                bool xh = ImGui::IsItemHovered();
                ImU32 xc = xh ? IM_COL32(0xDC, 0x14, 0x3C, 255) : IM_COL32(150, 158, 170, 255);
                ImVec2 xm = ImVec2(xp.x + clearW * 0.5f, xp.y + fh * 0.5f);
                float xk = clearW * 0.22f;
                dl->AddLine(ImVec2(xm.x - xk, xm.y - xk), ImVec2(xm.x + xk, xm.y + xk), xc, 2.5f * S);
                dl->AddLine(ImVec2(xm.x - xk, xm.y + xk), ImVec2(xm.x + xk, xm.y - xk), xc, 2.5f * S);
                if (xh) ImGui::SetTooltip("clear the search");
            }
            ImGui::Separator();

            if (g_searching) {
                ImGui::TextColored(ImVec4(1, 0.85f, 0.2f, 1), "Searching...");
            } else if (!g_searchErr.empty()) {
                ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "%s", g_searchErr.c_str());
            } else if (!g_searchMode.empty()) {
                // ---- structured search hits (C-1/C-2/C-3/C-4/C-5) ----
                int playable = 0, transcriptOnly = 0;
                for (auto& h : g_hits) { if (h.transcriptOnly) transcriptOnly++; else playable++; }
                ImGui::TextDisabled("%d quotes - %d playable, %d transcript-only%s", (int)g_hits.size(), playable, transcriptOnly,
                    g_searchMode == "qmd" ? " (smart)" : "");
                if (!g_searchNote.empty()) ImGui::TextDisabled("%s", g_searchNote.c_str());
                // ---- THE THIRD SORT (items 18/19): "most relevant results at top" ----
                //
                // It lives HERE, on the hits header, and not beside the newest/Z-A
                // pills: those sort the VIDEO LIBRARY, and a video has no relevance
                // score - only a search hit does. This row is the hits view's own
                // count-plus-pills line, so it is the same control in the same place
                // for the list it can actually sort.
                //
                // A REAL CROWN, hand-drawn (crownButton, defined near drawLibraryCard) -
                // not a font glyph (Segoe MDL2 has none - all 1792 glyphs in the loaded
                // E700-EDFF range were checked) and not the word either: Jordan tested
                // the text-pill version live and corrected it, an icon is what he wants
                // here. Same InvisibleButton+ImDrawList technique the clear-search "X"
                // already uses, so a missing font glyph can never turn this into a
                // hollow square. Blue, as he asked, the same accent the "smart" pill wears.
                {
                    const float S = ImGui::GetIO().FontGlobalScale;
                    ImGui::SameLine(0, 8 * S);
                    if (crownButton(g_hitRelevance)) { g_hitRelevance = !g_hitRelevance; applyHitSort(); }
                    if (ImGui::IsItemHovered())
                        ImGui::SetTooltip("%s", g_hitRelevance
                            ? "BEST MATCHES FIRST - click to go back to the order the search returned"
                            : "sorted the way the search returned them - click for best matches first (most relevant)");
                }
                ImGui::Separator();
                // ---- KEYBOARD REACH ON HITS (items 68/76) ----
                // A hit row was mouse-only: no way to copy a filename, open its
                // folder, or add the clip without a double-click, every single
                // time. The video rows below have had all three for a while - hits
                // just never got the same treatment.
                //
                // Up/Down move g_hitSel and Enter does what double-click does.
                // The selection is DRAWN (Selectable's `selected`), not merely
                // tracked: an invisible focus is useless to him - he has to be
                // able to see which row Enter is about to act on.
                //
                // Same guard the library rows use and for the same reason: the
                // search box lives in this panel, so IsWindowFocused stays true
                // while typing in it. Without !WantTextInput, pressing Enter to
                // SUBMIT a search would also fire "add the selected hit".
                if (libFocusedNow && !ImGui::GetIO().WantTextInput && !g_hits.empty()) {
                    bool hitMoved = false;
                    if (ImGui::IsKeyPressed(ImGuiKey_DownArrow))
                        { g_hitSel = std::min((int)g_hits.size() - 1, g_hitSel + 1); g_hitScrollPending = true; hitMoved = true; }
                    if (ImGui::IsKeyPressed(ImGuiKey_UpArrow))
                        { g_hitSel = std::max(0, g_hitSel - 1); g_hitScrollPending = true; hitMoved = true; }
                    if ((ImGui::IsKeyPressed(ImGuiKey_Enter) || ImGui::IsKeyPressed(ImGuiKey_KeypadEnter)) &&
                        g_hitSel >= 0 && g_hitSel < (int)g_hits.size() && !g_hits[g_hitSel].transcriptOnly)
                        addHitToTimeline(g_hits[g_hitSel], curSec, playing, lastComposed);
                    // Item 5: arrow-key navigation plays whichever quote is now selected -
                    // same non-destructive path as a click (previewPlaySpan).
                    if (hitMoved && g_hitSel >= 0 && g_hitSel < (int)g_hits.size() && !g_hits[g_hitSel].transcriptOnly) {
                        Hit& hm = g_hits[g_hitSel];
                        previewPlaySpan(hm.source, hm.start, hm.end, curSec, playing, lastComposed);
                    }
                }
                ImGui::BeginChild("hits", { 0, 0 }, false);
                for (size_t i = 0; i < g_hits.size(); i++) {
                    Hit& h = g_hits[i];
                    ImGui::PushID((int)i);
                    std::string line = h.timecode + "  " + h.text;
                    if (h.transcriptOnly) {
                        ImGui::TextDisabled("%s", line.c_str());
                    } else {
                        // Corrected live (item B): single click PREVIEWS ONLY (no track
                        // mutation, no auto-play - previewSourceFrame, same as a cue click);
                        // double-click / Enter ADDS, inserted at the playhead, never
                        // destructively (addHitToTimeline -> addSpanToTimeline). The old
                        // single-click path (seekToSpan, startPlaying=true) replaced the
                        // WHOLE live edit reel with a one-clip audition - that was the bug.
                        // AllowOverlap so the "not yet indexed" icon drawn below (same
                        // technique as the library card's round "+") can steal its own
                        // click instead of always resolving to this row's Selectable.
                        // The icon's own rect is worked out HERE (before Selectable, from
                        // the same cursor pos/width Selectable is about to claim) so a
                        // click on the icon can be excluded from the row's click below -
                        // drawLibraryCard's "+" needs the same exclusion, just after the
                        // fact (its button decides via a returned struct field instead).
                        ImVec2 rowP0  = ImGui::GetCursorScreenPos();
                        float  rowW   = ImGui::GetContentRegionAvail().x;
                        float  rowH   = ImGui::GetTextLineHeightWithSpacing();
                        bool   showIdx = sourceIndexState(h.source) == IndexState::NotIndexed;
                        const float S = ImGui::GetIO().FontGlobalScale;
                        float idxD    = ImGui::GetTextLineHeight() * 0.8f;
                        ImVec2 idxBc  = ImVec2(rowP0.x + rowW - idxD * 0.5f - 4.0f * S, rowP0.y + rowH * 0.5f);
                        bool overIdx  = showIdx && ImGui::IsMouseHoveringRect(
                            ImVec2(idxBc.x - idxD * 0.5f, idxBc.y - idxD * 0.5f),
                            ImVec2(idxBc.x + idxD * 0.5f, idxBc.y + idxD * 0.5f));
                        ImGui::SetNextItemAllowOverlap();
                        if (ImGui::Selectable(line.c_str(), g_hitSel == (int)i, ImGuiSelectableFlags_AllowDoubleClick) && !overIdx) {
                            g_hitSel = (int)i;
                            if (ImGui::IsMouseDoubleClicked(ImGuiMouseButton_Left)) addHitToTimeline(h, curSec, playing, lastComposed);
                            // Item 4: single click PLAYS the quote's span (still never
                            // touches the real reel - see previewPlaySpan's comment).
                            else previewPlaySpan(h.source, h.start, h.end, curSec, playing, lastComposed);
                        }
                        // Right-click = the video rows' menu, on a hit. Right-click
                        // also MOVES the selection first, so the menu and the row
                        // Enter would act on can never be two different rows.
                        if (ImGui::IsItemClicked(ImGuiMouseButton_Right)) { g_hitSel = (int)i; ImGui::OpenPopup("hitctx"); }
                        if (ImGui::BeginPopup("hitctx")) {
                            // The popup is only as wide as its widest MENU ITEM, so a
                            // raw filename header just ran off the edge and got clipped
                            // - the head is the date he scans by and the tail is what
                            // tells near-duplicates apart, so a hard cut throws away
                            // the half that identifies the file. Middle-ellipsis to the
                            // menu's own width, full name on the tooltip.
                            std::string full = baseName(h.source);
                            ImGui::TextDisabled("%s", midEllipsis(full, ImGui::CalcTextSize("Open in File Browser").x).c_str());
                            if (ImGui::IsItemHovered()) ImGui::SetTooltip("%s", full.c_str());
                            ImGui::Separator();
                            if (ImGui::MenuItem("Add to Timeline")) addHitToTimeline(h, curSec, playing, lastComposed);
                            if (ImGui::MenuItem("Open in File Browser")) openInFileBrowser(h.source);
                            if (ImGui::MenuItem("Copy File Name")) ImGui::SetClipboardText(baseName(h.source).c_str());
                            if (ImGui::MenuItem("Copy Quote")) ImGui::SetClipboardText(h.text.c_str());
                            if (ImGui::MenuItem("Transcribe")) requestTranscribe(h.source, baseName(h.source));
                            if (showIdx && ImGui::MenuItem("Index for Search")) requestIndexSource(h.source);
                            ImGui::EndPopup();
                        }
                        if (g_hitSel == (int)i && g_hitScrollPending) { ImGui::SetScrollHereY(0.5f); g_hitScrollPending = false; }
                        if (ImGui::IsItemHovered()) ImGui::SetTooltip("%s\n%s", h.name.c_str(), h.date.c_str());
                        // 2026-06-30(2): "not yet indexed" icon, mirrors the library card's
                        // round "+" (drawLibraryCard) - hand-drawn, not a font glyph, so a
                        // missing Segoe MDL2 range can never regress this into a hollow
                        // square. Sits in this row's own right edge via AllowOverlap same as
                        // that button; click runs the same qmdindex convert the transcribe
                        // pipeline calls, without navigating away from the search results.
                        // Reuses idxBc/idxD/showIdx computed before the Selectable above,
                        // so the click-exclusion test and the drawn icon can never disagree
                        // about where the icon actually is.
                        if (showIdx) {
                            ImVec2 bc = idxBc;
                            float id_ = idxD;
                            ImGui::SetCursorScreenPos(ImVec2(bc.x - id_ * 0.5f, bc.y - id_ * 0.5f));
                            ImGui::SetNextItemAllowOverlap();
                            bool idxClicked = ImGui::InvisibleButton("##idx", ImVec2(id_, id_));
                            bool idxHov = ImGui::IsItemHovered();
                            ImDrawList* idl = ImGui::GetWindowDrawList();
                            float ir = id_ * 0.5f;
                            idl->AddCircleFilled(bc, idxHov ? ir : ir - 1.0f, IM_COL32(0x00, 0xAE, 0xEF, 255));
                            for (int k = 0; k < 3; k++) {
                                float ly = bc.y - ir * 0.35f + k * (ir * 0.35f);
                                float lw = ir * (0.35f + 0.18f * (float)k);
                                idl->AddLine(ImVec2(bc.x - lw, ly), ImVec2(bc.x + lw, ly), IM_COL32(0, 0, 0, 255), 1.6f * S);
                            }
                            if (idxHov) {
                                ImGui::SetMouseCursor(ImGuiMouseCursor_Hand);
                                ImGui::SetTooltip("not yet in the smart-search index - click to index now");
                            }
                            if (idxClicked) requestIndexSource(h.source);
                        }
                    }
                    ImGui::PopID();
                }
                ImGui::EndChild();
            } else if (!g_cueName.empty()) {
                // ---- flowing single-video transcript view (B-8/B15, audapolis pattern) ----
                // Continuous word-wrapped prose, not one bordered row per ASR segment.
                //
                // ImGui::TextWrapped() chained with SameLine() looked like the obvious
                // way to do this and is WRONG: ImGui computes ONE wrap width for a
                // TextWrapped call from wherever its FIRST line starts, and reuses that
                // same (narrow, off-margin) width for every wrapped continuation line of
                // THAT call - a cue starting mid-line via SameLine() that itself needed
                // 2+ lines rendered as a garbled single-word-per-line column (verified
                // with a real transcript, screenshot showed it). Laying out one WORD at
                // a time - draw it, then only NewLine() if the NEXT word would overflow
                // the child's actual width - keeps every wrapped line starting at the
                // real left margin, which is what real word-wrap requires.
                //
                // A timecode appears only at a real pause in speech (a paragraph break),
                // not repeated on every line. Each word is its own click target so
                // hovering/clicking anywhere in a cue's run of words seeks the player
                // there; the current search match is highlighted, not hidden - a real
                // "find", not a filter that deletes the rest of the document.
                // Item 11 (round 3): a GREEN back-arrow glyph, no "back" text - the
                // dim-gray word "back" was wrong per Jordan's own spec. Neon-green
                // ink on a near-black chip, matching the reference's .backbtn
                // exactly (green text/icon, neon-dim border, no fill until hover).
                {
                    ImGui::PushStyleColor(ImGuiCol_Button, ImVec4(0.04f, 0.04f, 0.04f, 1.0f));
                    ImGui::PushStyleColor(ImGuiCol_ButtonHovered, ImVec4(0.10f, 0.18f, 0.07f, 1.0f));
                    ImGui::PushStyleColor(ImGuiCol_ButtonActive, ImVec4(0.10f, 0.18f, 0.07f, 1.0f));
                    ImGui::PushStyleColor(ImGuiCol_Text, ImGui::ColorConvertU32ToFloat4(kPalette[0]));
                    if (fixedButton(ico(ICON_BACK "##cueback", "<##cueback"), { ico(ICON_BACK, "<") }))
                        { g_cueName.clear(); g_cues.clear(); g_cueErr.clear(); g_cueSel = -1; g_cueMulti.clear(); g_cueAnchor = -1; }
                    ImGui::PopStyleColor(4);
                    if (ImGui::IsItemHovered()) ImGui::SetTooltip("Back to the file list");
                }
                ImGui::SameLine(); ImGui::TextDisabled("%s", g_cueName.c_str());
                ImGui::InputTextWithHint("##within", "search within this transcript", g_withinBuf, sizeof g_withinBuf);
                inputFocusBorder();
                ImGui::SameLine();
                // Item 3c: auto-cut, placed here per the old app's layout (the button
                // sat immediately right of this same field). Runs on the video whose
                // transcript is open; needs at least one cue for its source path
                // (the transcript reply already carries it - g_cues[0].source).
                {
                    bool canCut = !g_cueName.empty() && !g_cues.empty();
                    if (!canCut) ImGui::BeginDisabled();
                    if (refBtn("auto-cut")) {
                        // Item 12: >1 quote selected -> auto-cut ONLY those quotes (restrict
                        // the kept segments to their ranges); 1 or none selected -> the whole
                        // video file.
                        std::vector<std::pair<double, double>> restrictRanges;
                        if (g_cueMulti.size() > 1)
                            for (int idx : g_cueMulti)
                                if (idx >= 0 && idx < (int)g_cues.size())
                                    restrictRanges.push_back({ g_cues[idx].start, g_cues[idx].end });
                        applyAutoCut(g_cueName, g_cues[0].source, curSec, lastComposed, restrictRanges);
                    }
                    if (!canCut) ImGui::EndDisabled();
                    if (ImGui::IsItemHovered())
                        ImGui::SetTooltip("Run becky-cut's silence detector on this video and drop the\nkeep segments onto the timeline for review (one Ctrl+Z undoes it).");
                }
                ImGui::Separator();
                if (!g_cueErr.empty()) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "%s", g_cueErr.c_str());
                // Item 5: Up/Down move g_cueSel (the visibly-highlighted cue, drawn
                // below) and Enter adds it - same libFocusedNow guard the hits/library
                // rows already use, so this can never fight the TIMELINE's own arrow-key
                // frame-stepping (that path is gated on the timeline window's own focus,
                // a completely different ImGui window than this left panel).
                if (libFocusedNow && !ImGui::GetIO().WantTextInput && !g_cues.empty()) {
                    // Item 8: the transcript now reads like a word-processor document, so
                    // navigation changed. LEFT/RIGHT step one quote (segment); DOWN jumps to
                    // the start of the NEXT paragraph; UP jumps to the start of the CURRENT
                    // paragraph, or - if already AT that start - to the start of the PREVIOUS
                    // paragraph. Paragraph breaks match the visible ones (cueParagraphStarts).
                    const int nCues = (int)g_cues.size();
                    if (g_cueSel < 0) g_cueSel = 0;
                    bool cueMoved = false;
                    if (ImGui::IsKeyPressed(ImGuiKey_RightArrow)) { g_cueSel = std::min(nCues - 1, g_cueSel + 1); cueMoved = true; }
                    if (ImGui::IsKeyPressed(ImGuiKey_LeftArrow))  { g_cueSel = std::max(0, g_cueSel - 1); cueMoved = true; }
                    if (ImGui::IsKeyPressed(ImGuiKey_DownArrow) || ImGui::IsKeyPressed(ImGuiKey_UpArrow)) {
                        std::vector<bool> para = cueParagraphStarts();
                        if (ImGui::IsKeyPressed(ImGuiKey_DownArrow)) {
                            int j = g_cueSel + 1; while (j < nCues && !para[j]) j++;
                            if (j < nCues) { g_cueSel = j; cueMoved = true; }
                        }
                        if (ImGui::IsKeyPressed(ImGuiKey_UpArrow)) {
                            int ps = g_cueSel; while (ps > 0 && !para[ps]) ps--;        // start of the current paragraph
                            if (ps < g_cueSel) { g_cueSel = ps; cueMoved = true; }      // not yet at it -> go there
                            else if (ps > 0) {                                          // already at it -> previous paragraph
                                int pp = ps - 1; while (pp > 0 && !para[pp]) pp--;
                                g_cueSel = pp; cueMoved = true;
                            }
                        }
                    }
                    if (cueMoved) { g_cueScrollPending = true; g_cueMulti.clear(); g_cueAnchor = g_cueSel; }
                    if (ImGui::IsKeyPressed(ImGuiKey_Enter) || ImGui::IsKeyPressed(ImGuiKey_KeypadEnter)) {
                        // Items 10/11: with a multi-selection, Enter adds ALL of it (merged);
                        // otherwise it adds the single selected quote.
                        if (!g_cueMulti.empty()) addCuesToTimeline(g_cueMulti, curSec, playing, lastComposed);
                        else if (g_cueSel >= 0 && g_cueSel < nCues) addCueToTimeline(g_cues[g_cueSel], curSec, playing, lastComposed);
                    }
                    // Arrow-key navigation auditions whichever quote is now selected.
                    if (cueMoved && g_cueSel >= 0 && g_cueSel < nCues) {
                        CueRow& cm = g_cues[g_cueSel];
                        previewPlaySpan(cm.source, cm.start, cm.end, curSec, playing, lastComposed);
                    }
                }
                ImGui::BeginChild("transcript", { 0, 0 }, false);
                std::string within(g_withinBuf);
                bool searchChanged = (within != g_withinLast);
                g_withinLast = within;
                bool scrolledToMatch = false;
                ImDrawList* dl = ImGui::GetWindowDrawList();
                // Item 10: the selected cue's highlight is a FILL, not a hairline
                // outline - but the fill has to sit BEHIND the words (which are
                // already submitted to `dl` before the cue's own bounding box is
                // even known - it only exists once every word has wrapped). A
                // draw-list splitter is the standard fix: words go on channel 1
                // (foreground) as before, the fill goes on channel 0 (background),
                // Merge puts channel 0 first regardless of submission order.
                ImDrawListSplitter cueSplit;
                cueSplit.Split(dl, 2);
                cueSplit.SetCurrentChannel(dl, 1);
                float spaceW = ImGui::CalcTextSize(" ").x;
                if (spaceW <= 0.0f) spaceW = 4.0f * ImGui::GetIO().FontGlobalScale;
                double lastEnd = -1000.0;
                // Item 6: NO timestamps at all was the actual complaint on a long
                // (5-hour livestream) transcript with few natural >1.5s pauses - one
                // "paragraph" could run the whole file with a single header at the
                // top. Force a header at least every kCueTimestampIntervalSec too, so
                // a long continuous take is still navigable to a rough time.
                static const double kCueTimestampIntervalSec = 180.0;   // "every few minutes"
                double lastTimestampAt = -1e18;
                for (size_t i = 0; i < g_cues.size(); i++) {
                    CueRow& c = g_cues[i];
                    ImGui::PushID((int)i);
                    // A real pause (>1.5s) reads as a paragraph break, like an actual
                    // transcript - never a boxed row per ASR segment.
                    bool newParagraph = (c.start - lastEnd > 1.5) || (c.start - lastTimestampAt >= kCueTimestampIntervalSec);
                    if (lastEnd > -999.0 && newParagraph) { ImGui::NewLine(); ImGui::NewLine(); }
                    lastEnd = c.end;
                    // Green, not dim gray: the reference app's every cue carries a
                    // neon timecode on the left - it is how he jumps the page, not
                    // decoration (BR3-VISUAL-SPEC rule 6).
                    if (newParagraph) { ImGui::TextColored(ImGui::ColorConvertU32ToFloat4(kPalette[0]), "%s", c.timecode.c_str()); ImGui::SameLine(0, 6); lastTimestampAt = c.start; }
                    bool isMatch = !within.empty() && ciContains(c.text, within);
                    bool cueHovered = false, cueClicked = false;
                    // Item 5: bounding box of every word this cue draws (may wrap
                    // across lines), so the selected cue can be outlined afterward
                    // without a filled overlay dimming the text underneath it.
                    ImVec2 cueMin(1e9f, 1e9f), cueMax(-1e9f, -1e9f);
                    size_t pos = 0, n = c.text.size();
                    while (pos < n) {
                        size_t wstart = c.text.find_first_not_of(' ', pos);
                        if (wstart == std::string::npos) break;
                        size_t wend = c.text.find(' ', wstart);
                        if (wend == std::string::npos) wend = n;
                        std::string word = c.text.substr(wstart, wend - wstart);
                        pos = wend;
                        ImVec2 sz = ImGui::CalcTextSize(word.c_str());
                        if (ImGui::GetContentRegionAvail().x < sz.x) ImGui::NewLine();
                        // Highlight the MATCHED WORD, not the whole cue - a "real find"
                        // (per the comment above) points at the hit, not a whole paragraph
                        // it happens to live in. Found live: searching "cheated" lit up
                        // all 15 words of the sentence it was in.
                        if (isMatch && ciContains(word, within)) {
                            ImVec2 p0 = ImGui::GetCursorScreenPos();
                            dl->AddRectFilled(p0, ImVec2(p0.x + sz.x, p0.y + sz.y), IM_COL32(0xFF, 0xD7, 0x00, 60), 2.0f);
                        }
                        ImGui::PushID((int)wstart);
                        ImVec2 wp0 = ImGui::GetCursorScreenPos();
                        ImGui::TextUnformatted(word.c_str());
                        ImVec2 wp1 = ImGui::GetItemRectMax();
                        cueMin.x = (std::min)(cueMin.x, wp0.x); cueMin.y = (std::min)(cueMin.y, wp0.y);
                        cueMax.x = (std::max)(cueMax.x, wp1.x); cueMax.y = (std::max)(cueMax.y, wp1.y);
                        if (ImGui::IsItemHovered()) cueHovered = true;
                        if (ImGui::IsItemClicked()) cueClicked = true;
                        ImGui::PopID();
                        ImGui::SameLine(0, spaceW);
                    }
                    // Item 11: the whole quote band is clickable, not just the glyphs -
                    // a click in the whitespace between words or in the line-wrap gap
                    // used to be silently ignored because only the individual word
                    // items had a hit test. Anywhere inside the cue's own bounding box
                    // counts, on top of the per-word hits already collected above.
                    if (!cueClicked && cueMax.x > cueMin.x &&
                        ImGui::IsMouseHoveringRect(cueMin, cueMax) &&
                        ImGui::IsMouseClicked(ImGuiMouseButton_Left)) {
                        cueClicked = true; cueHovered = true;
                    }
                    // Corrected live (item B): single click ANYWHERE in this cue's words
                    // PLAYS its span (item 4, round 2) - previewPlaySpan never touches
                    // the real g_track[0]. Double-click ADDS, inserted at the playhead,
                    // non-destructively (addCueToTimeline -> addSpanToTimeline). The old
                    // single-click path (seekToSpan) replaced the WHOLE live edit reel
                    // with a one-clip audition - "single-clicking a cue ADDS it to the
                    // timeline AND REPLACES the entire existing timeline", his words,
                    // #1 priority, and the reason this whole item exists.
                    if (cueClicked) {
                        ImGuiIO& gio = ImGui::GetIO();
                        if (gio.KeyCtrl) {
                            // Item 10: Ctrl+click TOGGLES this quote in the multi-selection.
                            if (g_cueMulti.count((int)i)) g_cueMulti.erase((int)i); else g_cueMulti.insert((int)i);
                            g_cueSel = (int)i; g_cueAnchor = (int)i;
                        } else if (gio.KeyShift && g_cueAnchor >= 0) {
                            // Item 11: Shift+click selects the whole range from the anchor to
                            // here, in either direction.
                            g_cueMulti.clear();
                            int lo = (std::min)(g_cueAnchor, (int)i), hi = (std::max)(g_cueAnchor, (int)i);
                            for (int k = lo; k <= hi; k++) g_cueMulti.insert(k);
                            g_cueSel = (int)i;
                        } else {
                            // Plain click: single-select + audition (clears any multi-selection).
                            g_cueMulti.clear(); g_cueAnchor = (int)i; g_cueSel = (int)i;
                            if (ImGui::IsMouseDoubleClicked(ImGuiMouseButton_Left)) addCueToTimeline(c, curSec, playing, lastComposed);
                            else previewPlaySpan(c.source, c.start, c.end, curSec, playing, lastComposed);
                        }
                    }
                    // Item 10: the selected cue is a real NEON GREEN fill (palette slot 0,
                    // #14FF39), not a white hairline outline - drawn on the splitter's
                    // background channel so it sits BEHIND the words already submitted
                    // above, and at low enough alpha the text stays readable on top.
                    bool cueSelected = g_cueMulti.empty() ? (g_cueSel == (int)i) : (g_cueMulti.count((int)i) > 0);
                    if (cueSelected && cueMax.x > cueMin.x) {
                        cueSplit.SetCurrentChannel(dl, 0);
                        dl->AddRectFilled(ImVec2(cueMin.x - 3, cueMin.y - 2), ImVec2(cueMax.x + 3, cueMax.y + 2),
                                          IM_COL32(0x14, 0xFF, 0x39, 70), 3.0f);
                        dl->AddRect(ImVec2(cueMin.x - 3, cueMin.y - 2), ImVec2(cueMax.x + 3, cueMax.y + 2),
                                    IM_COL32(0x14, 0xFF, 0x39, 220), 3.0f, 0, 1.5f);
                        cueSplit.SetCurrentChannel(dl, 1);
                    }
                    if (g_cueSel == (int)i && g_cueScrollPending) { ImGui::SetScrollHereY(0.3f); g_cueScrollPending = false; }
                    if (isMatch && searchChanged && !scrolledToMatch) { ImGui::SetScrollHereY(0.2f); scrolledToMatch = true; }
                    if (cueHovered) ImGui::SetTooltip("%s - click to play; double-click to add to the timeline", c.timecode.c_str());
                    ImGui::PopID();
                }
                cueSplit.Merge(dl);
                ImGui::EndChild();
            } else {
                // ---- video library list (B-1/B-3/B-4/B-5/B-6/B-7) ----
                // Checklist 18, reference .findhead: the count, then two sort pills.
                // The old control was ONE button cycling four hidden states - you had
                // to click it and read it to find out what it did.
                {
                    const float S = ImGui::GetIO().FontGlobalScale;
                    ImGui::Text("%d video%s", (int)g_videos.size(), g_videos.size() == 1 ? "" : "s");
                    ImGui::SameLine(0, 12 * S);
                    // Item 1: ONE sort button that CYCLES newest -> oldest -> A-Z -> Z-A
                    // (was two pills). Green-active like the reference's selected .sortbtn;
                    // the label always names the current order, a click advances to the next.
                    static const char* kSortNames[4] = { "newest", "oldest", "A-Z", "Z-A" };
                    const int sm = g_sortMode & 3;
                    if (pillButton(kSortNames[sm], true, IM_COL32(0x14, 0xFF, 0x39, 255)))
                        { g_sortMode = (sm + 1) & 3; sortLibrary(); }
                    if (ImGui::IsItemHovered())
                        ImGui::SetTooltip("Sort: %s  (click to cycle newest / oldest / A-Z / Z-A)", kSortNames[sm]);

                    // No folder open = nothing to transcribe. Rendering this live over
                    // an empty library invites a click that can only fail.
                    if (!g_videos.empty()) {
                        if (g_transcribeAllBusy) ImGui::BeginDisabled();
                        if (fixedButton(g_transcribeAllBusy ? "Transcribing all..." : "Transcribe all",
                                        { "Transcribe all", "Transcribing all..." })) {
                            g_transcribeAllBusy = true;
                            // Whole-folder ASR: minutes to hours, so it goes through
                            // engineCallAsync (never the UI thread) and its reply lands on
                            // the UI thread via drainAsync.
                            engineCallAsync("transcribe_all", json::object(), 7200.0, "Transcribing every video...",
                                [](const json& r) {
                                    g_transcribeAllBusy = false;
                                    // g_renderMsgAt must be set on BOTH paths: the status
                                    // line only shows a message less than 8s old, so an
                                    // untimestamped failure is a silent failure - he waits
                                    // an hour and is told nothing at all.
                                    if (!r.value("ok", false)) {
                                        g_renderMsg = "Transcribe all failed: " + r.value("error", std::string("unknown"));
                                        g_renderMsgAt = nowSec();
                                        return;
                                    }
                                    const json& d = r.contains("data") ? r["data"] : r;
                                    if (d.contains("folder")) applyFolderView(d["folder"], g_folderRoot);
                                    int okN = d.value("transcribed", 0), badN = d.value("failed", 0);
                                    g_renderMsg = "Transcribed " + std::to_string(okN) +
                                                  (badN ? (", " + std::to_string(badN) + " failed") : "");
                                    g_renderMsgAt = nowSec();
                                });
                        }
                        if (g_transcribeAllBusy) ImGui::EndDisabled();
                    }
                    // Its OWN row, not SameLine after "Transcribe all": at 320px the
                    // panel had no width left and the sentence was sliced mid-word
                    // ("+652 orphan transcri"), which is the exact failure this
                    // whole panel was rebuilt to stop.
                    if (g_orphanCount > 0) ImGui::TextDisabled("(+%d orphan transcripts)", g_orphanCount);
                }
                if (!g_folderErr.empty()) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "%s", g_folderErr.c_str());
                ImGui::Separator();
                if (g_videos.empty()) {
                    ImGui::TextDisabled("Ctrl+O to open a folder");
                } else if (libFocusedNow && !ImGui::GetIO().WantTextInput) {
                    if (ImGui::IsKeyPressed(ImGuiKey_DownArrow)) { g_libSel = std::min((int)g_videos.size() - 1, g_libSel + 1); g_libScrollPending = true; }
                    if (ImGui::IsKeyPressed(ImGuiKey_UpArrow)) { g_libSel = std::max(0, g_libSel - 1); g_libScrollPending = true; }
                }
                ImGui::BeginChild("videos", { 0, 0 }, false);
                // The colours the timeline already assigned per source, so a card
                // wears the same colour as its clips (checklist 37).
                // ponytail: rebuilt each frame - O(clips on the timeline), a few
                // hundred at most. Key it off a track revision counter only if a
                // reel ever gets big enough to measure.
                std::map<std::string, ImU32> srcCol;
                for (auto& c : g_track[0]) srcCol.emplace(baseName(c.source), IM_COL32(c.r, c.g, c.b, 255));

                // His real corpus is 2258 videos. Build only the cards actually on
                // screen - a card is more draw work than a Selectable was, and this
                // is a responsiveness requirement, not an optimisation.
                ImGuiListClipper clip;
                clip.Begin((int)g_videos.size(), libCardStride());
                if (g_libScrollPending && g_libSel >= 0) clip.IncludeItemByIndex(g_libSel);
                // A clipped-away row never calls BeginPopup, which closes its menu.
                // Keep the row with the open menu submitted no matter where it scrolls.
                if (g_libCtxIdx >= 0 && g_libCtxIdx < (int)g_videos.size()) clip.IncludeItemByIndex(g_libCtxIdx);
                while (clip.Step()) {
                    for (int i = clip.DisplayStart; i < clip.DisplayEnd; i++) {
                        VideoRow& v = g_videos[i];
                        ImGui::PushID(i);
                        bool inFlight;
                        { std::lock_guard<std::mutex> lk(g_transcribeMx); inFlight = g_transcribeInFlight.count(v.path) != 0; }
                        auto it = srcCol.find(baseName(v.path));
                        LibCardResult res = drawLibraryCard(v, g_libSel == i, g_libJustViewedIdx == i, inFlight,
                                                            it == srcCol.end() ? 0u : it->second);
                        // ONE selection model (B-4): mouse click sets the SAME index arrows move.
                        // Jordan tested live and corrected this: a SINGLE click must open the
                        // transcript (it was wrongly gated behind res.dbl - a double click was
                        // required). res.clicked already fires on a plain single click with no
                        // extra latency (LibCardResult's comment), so this is a one-line move,
                        // not a new double-click timer.
                        if (res.clicked) {
                            g_libSel = i;
                            if (ImGui::GetIO().KeyCtrl) {
                                // Item 9: Ctrl+click a video puts the ENTIRE video onto the
                                // timeline at the playhead (the engine probes its length and
                                // inserts the whole file), instead of opening its transcript.
                                endPreviewRestore(curSec, playing, lastComposed);
                                requestAddExternal(v.path, insertIndexAtPlayhead(curSec));
                            } else {
                                openTranscript(v.path); g_libJustViewedIdx = i;
                            }
                        }
                        if (res.plus) { g_libSel = i; requestTranscribe(v.path, v.name); }
                        if (res.robot) {
                            // Item 13: auto-cut this whole video, then caption the result with
                            // becky-subtitle - one pipeline, clips + captions onto the timeline.
                            g_libSel = i;
                            endPreviewRestore(curSec, playing, lastComposed);
                            applyAutoCut(v.name, v.path, curSec, lastComposed, {}, /*thenCaptions=*/true);
                        }
                        // Opened by the card's right-click (drawLibraryCard), same ID scope.
                        if (ImGui::BeginPopup("rowctx")) {
                            g_libCtxIdx = i;
                            g_libSel = i;
                            if (ImGui::MenuItem("Open in File Browser")) openInFileBrowser(v.path);
                            if (ImGui::MenuItem("Copy File Name")) ImGui::SetClipboardText(baseName(v.path).c_str());
                            // B-2: one-click local transcription (Parakeet, official-first) - never
                            // overwrites an original transcript (enforced engine-side). Disabled
                            // while this exact video is already transcribing (in-flight guard).
                            if (inFlight) ImGui::BeginDisabled();
                            if (ImGui::MenuItem(v.hasTranscript ? "Re-transcribe" : "Transcribe")) requestTranscribe(v.path, v.name);
                            if (inFlight) ImGui::EndDisabled();
                            // Item 27: "Get Captions" for a sidebar video = put the WHOLE video
                            // on the timeline, then build real TikTok captions for it with
                            // becky-subtitle (cut-snapped + phrase-broken), once the add lands.
                            ImGui::BeginDisabled(g_cliCutBusy.load());
                            if (ImGui::MenuItem("Get Captions")) {
                                endPreviewRestore(curSec, playing, lastComposed);
                                requestAddExternal(v.path, insertIndexAtPlayhead(curSec));
                                g_getCaptionsAfterAdd = true;
                            }
                            ImGui::EndDisabled();
                            ImGui::EndPopup();
                        } else if (g_libCtxIdx == i) {
                            g_libCtxIdx = -1;
                        }
                        // The last submitted item is the round button, which is vertically
                        // centred in the card - so this centres the CARD.
                        if (g_libSel == i && g_libScrollPending) { ImGui::SetScrollHereY(0.5f); g_libScrollPending = false; }
                        ImGui::PopID();
                    }
                }
                clip.End();
                ImGui::EndChild();
                // B-5: Space plays the selected row; Enter = double-click (open transcript).
                // Guarded on !WantTextInput (I-4 fix, found live this session): the search
                // box's InputText lives in this SAME panel, so IsWindowFocused(RootAndChild)
                // stays true while typing in it - without this guard, pressing Enter to submit
                // a keyword search ALSO fired "open transcript of selected row" (hijacking C-1
                // search-via-Enter every time), and Space in a query string ALSO played the
                // selected video mid-keystroke.
                if (libFocusedNow && !ImGui::GetIO().WantTextInput && g_libSel >= 0 && g_libSel < (int)g_videos.size()) {
                    if (ImGui::IsKeyPressed(ImGuiKey_Enter) || ImGui::IsKeyPressed(ImGuiKey_KeypadEnter)) { openTranscript(g_videos[g_libSel].path); g_libJustViewedIdx = g_libSel; }
                    if (ImGui::IsKeyPressed(ImGuiKey_Space)) playWholeVideo(g_videos[g_libSel].path, curSec, playing, lastComposed);
                }
            }
            g_libFocused = libFocusedNow;
        }
        ImGui::End();
        stageMark("left-panel");

        // ---- center video pane (step 6: the ENGINE's frame as a plain ImGui image) ----
        ImGui::SetNextWindowPos({ libW + splitHalf, topY }); ImGui::SetNextWindowSize({ vidW - splitHalf, topH });
        if (ImGui::Begin("video", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus)) {
            bool haveClip = !g_track[0].empty();
            int vw = 0, vh = 0;
            ID3D11ShaderResourceView* vsrv = engine::currentFrameSRV(g_dev, &vw, &vh);
            if (engine::available() && haveClip) {
                ImVec2 origin = ImGui::GetCursorScreenPos();
                ImVec2 avail = ImGui::GetContentRegionAvail();
                // Item 4 (round 4): the transport/tool toolbar MOVED to the timeline
                // header row, so the two button-row heights this used to reserve
                // below the video (GetFrameHeightWithSpacing()*2) are now dead space
                // wasting the preview. Reserve only what still draws here: the
                // curSec/dur readout line and the g_renderMsg status line - two text
                // lines - and give every other pixel back to the video pane.
                float ctrlH = ImGui::GetTextLineHeightWithSpacing() * 2;
                float videoH = std::max(0.0f, avail.y - ctrlH);
                if (vsrv && vw > 0 && vh > 0 && videoH > 8.0f) {
                    // letterbox-fit the frame into the pane (mpv used to do this
                    // inside its own surface; now it is two lines of math)
                    float sc = std::min(avail.x / (float)vw, videoH / (float)vh);
                    float fw = (float)vw * sc, fh = (float)vh * sc;
                    ImVec2 at{ origin.x + (avail.x - fw) * 0.5f, origin.y + (videoH - fh) * 0.5f };
                    ImGui::GetWindowDrawList()->AddRectFilled(origin, { origin.x + avail.x, origin.y + videoH }, IM_COL32(0, 0, 0, 255));
                    ImGui::GetWindowDrawList()->AddImage((ImTextureID)vsrv, at, { at.x + fw, at.y + fh });
                    // provenance overlay + captions, drawn by ImGui ON the frame
                    drawOverlayImGui(clipAtComp(0, curSec), at, { fw, fh }, sc);   // sc = fit scale = displayedH/sourceH (item 25)
                    // Item 1 (round 4): during a preview, g_caps deliberately still
                    // holds the REAL reel's captions (the lane must not change), so
                    // don't burn one over the audition frame at the preview time -
                    // just show the video, "only in the preview window".
                    if (g_capsOn && !g_inTiedPreview) drawCaptionsImGui(curSec, at, { fw, fh });
                } else {
                    ImGui::GetWindowDrawList()->AddRectFilled(origin, { origin.x + avail.x, origin.y + videoH }, IM_COL32(12, 12, 12, 255));
                }
                ImGui::Dummy({ avail.x, videoH });
                // Item 6: clicking the preview screen toggles play/pause - exactly the
                // same toggle the Play/Pause button below uses (stopPlayback's stock-
                // return semantics, item 59), so a click and the button never disagree
                // about where playback resumes/returns to. Gated on "didn't drag" -
                // measured live (VIDCLICK DIAG) that g_capMarginDrag is the WRONG
                // signal for that: the caption-drag code below sets it on ANY press in
                // the pane via a raw GetAsyncKeyState poll, real drag or not, so it was
                // permanently vetoing this toggle whenever a caption path was loaded.
                // The distance check below is this code's OWN, correct "did the user
                // actually drag" answer - it does not need the other gesture's flag.
                {
                    static bool s_vidPressed = false;
                    static ImVec2 s_vidPressPos;
                    bool vidHovered = ImGui::IsItemHovered();
                    if (vidHovered && ImGui::IsMouseClicked(ImGuiMouseButton_Left)) {
                        s_vidPressed = true; s_vidPressPos = ImGui::GetMousePos();
                    }
                    if (s_vidPressed && ImGui::IsMouseReleased(ImGuiMouseButton_Left)) {
                        ImVec2 rel = ImGui::GetMousePos();
                        float dx = rel.x - s_vidPressPos.x, dy = rel.y - s_vidPressPos.y;
                        if ((dx * dx + dy * dy) < 16.0f) {
                            if (playing) stopPlayback(curSec, playing, true);
                            else { playing = true; g_playingExt = true; }
                        }
                        s_vidPressed = false;
                    }
                }

                // ---- drag a caption UP or DOWN to place ALL of them ----
                // (kept on OS cursor polling - it already works and also fires when
                // an ImGui popup would otherwise eat the click)
                if (g_capsOn && !g_capPath.empty() && videoH > 32) {
                    POINT cp; GetCursorPos(&cp); ScreenToClient(g_hwnd, &cp);
                    bool inPane = cp.x >= (LONG)origin.x && cp.x <= (LONG)(origin.x + avail.x) &&
                                  cp.y >= (LONG)origin.y && cp.y <= (LONG)(origin.y + videoH);
                    bool btn = (GetAsyncKeyState(VK_LBUTTON) & 0x8000) != 0;
                    bool mine = GetForegroundWindow() == g_hwnd;
                    if (!g_capMarginDrag && btn && mine && inPane) {
                        g_capMarginDrag = true;
                        g_capMarginAtGrab = g_capMarginV;
                        g_capMarginGrabY = (double)cp.y;
                        g_capMarginUnitsPerPx = (double)CAP_ASS_H / (double)videoH;
                    } else if (g_capMarginDrag && btn) {
                        double dy = (double)cp.y - g_capMarginGrabY;
                        int m = g_capMarginAtGrab - (int)std::llround(dy * g_capMarginUnitsPerPx);
                        if (m < 0) m = 0;
                        if (m > CAP_ASS_H - 20) m = CAP_ASS_H - 20;
                        g_capMarginV = m;
                    } else if (g_capMarginDrag && !btn) {
                        g_capMarginDrag = false;
                        saveCapStyle();
                        g_renderMsg = "Caption placement saved (MarginV " + std::to_string(g_capMarginV) + ")";
                        g_renderMsgAt = nowSec();
                    }
                }
            } else {
                if (!engine::available()) {
                    ImGui::TextDisabled("video decode unavailable (engine failed to start - shell/library/timeline still work)");
                } else {
                    ImGui::TextDisabled("video pane - no clip loaded");
                }
            }
            // %.2f: a one-frame step (~0.033s) must visibly move this number (bug 7).
            ImGui::Text("%.2f / %.1f s", curSec, g_compDur);
            // Item 2 (round 3): the WHOLE transport/tool/action toolbar used to live
            // here, under the video pane, leaving a big dead strip above the timeline
            // that LOOKED clickable but wasn't - Jordan's #1 complaint ("deceptive").
            // It now lives in the timeline's OWN header row (.tlhead in the reference),
            // right next to the clip count + zoom - see drawTransportToolbar, called
            // from the "timeline" window below. Only the small curSec/duration readout
            // stays here, beside the frame it describes.
            if (!g_renderMsg.empty() && nowSec() - g_renderMsgAt < 8.0) ImGui::TextDisabled("%s", g_renderMsg.c_str());
        }
        ImGui::End();
        stageMark("center-video-pane");

        // ---- LEFT/CENTER SPLITTER (item 7): drag to resize the library pane ----
        // Occupies the strip "library" and "video" leave carved out between
        // them (see splitW/splitHalf above) - never overlapped by a neighbour,
        // so its click can never be lost to one.
        {
            ImGui::SetNextWindowPos({ libW - splitHalf, topY });
            ImGui::SetNextWindowSize({ splitW, topH });
            ImGui::SetNextWindowBgAlpha(0.0f);
            // ROOT CAUSE of the first version of this fix not responding to any
            // click, measured live: the default WindowPadding (~8px each side)
            // ate almost this entire 14px-wide window before the InvisibleButton
            // ever got laid out, so the real clickable area was a sliver a few
            // px wide and NOT centred where the drawn line is. Zero padding here
            // so the button fills the window exactly.
            ImGui::PushStyleVar(ImGuiStyleVar_WindowPadding, ImVec2(0, 0));
            if (ImGui::Begin("libsplitter", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize |
                              ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoScrollbar | ImGuiWindowFlags_NoBringToFrontOnFocus |
                              ImGuiWindowFlags_NoCollapse | ImGuiWindowFlags_NoSavedSettings)) {
                ImGui::InvisibleButton("##libsplit", ImVec2(splitW, topH));
                bool splitHov = ImGui::IsItemHovered(), splitAct = ImGui::IsItemActive();
                if (splitHov || splitAct) ImGui::SetMouseCursor(ImGuiMouseCursor_ResizeEW);
                if (splitAct) g_libW += ImGui::GetIO().MouseDelta.x;
                ImDrawList* sdl = ImGui::GetWindowDrawList();
                // Reference .vsplit: one bar, background --line #262626; neon (+ glow) on
                // hover/drag. ONE bar only now (the panel borders are gone).
                bool splitLit = splitHov || splitAct;
                ImU32 splitCol = splitLit ? IM_COL32(0x39, 0xFF, 0x14, 255) : IM_COL32(0x26, 0x26, 0x26, 255);
                float cx = ImGui::GetWindowPos().x + splitHalf;
                if (splitLit) sdl->AddLine(ImVec2(cx, topY + 4), ImVec2(cx, topY + topH - 4), IM_COL32(0x39, 0xFF, 0x14, 60), 6.0f); // glow
                sdl->AddLine(ImVec2(cx, topY + 4), ImVec2(cx, topY + topH - 4), splitCol, 2.0f);
            }
            ImGui::End();
            ImGui::PopStyleVar();
        }
        stageMark("lib-splitter");

        // ---- right panel: Q&A / ask-becky (G) ----
        ImGui::SetNextWindowPos({ libW + vidW, topY }); ImGui::SetNextWindowSize({ qaW, topH });
        if (ImGui::Begin("qa", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus)) {
            const ImVec4 neonV = ImGui::ColorConvertU32ToFloat4(kPalette[0]);   // #14FF39
            const ImVec4 warnV = ImVec4(0.95f, 0.85f, 0.45f, 1.0f);             // the work indicator's yellow

            // ---- header: robot mark + "ask becky" (reference: .chathead/.chattitle) ----
            askBeckyMark(ImGui::GetFontSize() * 1.25f);
            ImGui::SameLine(0.0f, 9.0f);
            ImGui::SetWindowFontScale(1.15f);
            ImGui::TextColored(neonV, "ask becky");
            ImGui::SetWindowFontScale(1.0f);
            ImGui::Separator();

            // ---- status card (reference: .intro) ----
            // Never blank and never optimistic: it names the backend the engine
            // actually reported, or says the engine is not answering.
            {
                const char* txt = g_backendSummary.empty()
                    ? "Checking which AI is connected..."
                    : g_backendSummary.c_str();
                ImVec4 edge = g_backendSummary.empty() ? ImVec4(0.5f, 0.5f, 0.5f, 1.0f)
                                                       : (g_backendOK ? neonV : warnV);
                float wrapW = ImGui::GetContentRegionAvail().x - ImGui::GetStyle().WindowPadding.x * 2.0f;
                float h = ImGui::CalcTextSize(txt, nullptr, false, wrapW).y + ImGui::GetStyle().WindowPadding.y * 2.0f;
                ImGui::PushStyleColor(ImGuiCol_ChildBg, ImVec4(0.05f, 0.07f, 0.05f, 1.0f));
                ImGui::PushStyleColor(ImGuiCol_Border, ImVec4(edge.x * 0.6f, edge.y * 0.6f, edge.z * 0.6f, 1.0f));
                ImGui::BeginChild("askstatus", ImVec2(0, h), ImGuiChildFlags_Border);
                ImGui::PushTextWrapPos(0.0f);
                ImGui::TextColored(g_backendOK ? ImVec4(0.86f, 0.86f, 0.86f, 1.0f) : warnV, "%s", txt);
                ImGui::PopTextWrapPos();
                ImGui::EndChild();
                ImGui::PopStyleColor(2);
            }

            // The chips + ask bar are PINNED to the foot of the panel, like the reference
            // and like every chat app - a control that drifts vertically with the amount of
            // content above it is a control he has to hunt for (same rule as fixedButton).
            // Height is measured from last frame's layout: exact, and one frame of lag on a
            // window resize is invisible.
            static float s_qaBottomH = 0.0f;
            float midH = -(s_qaBottomH > 0.0f ? s_qaBottomH : ImGui::GetFrameHeight() * 5.0f);
            ImGui::BeginChild("qamid", ImVec2(0, midH), ImGuiChildFlags_None);

            // H-5: "what becky is doing", passively. No buttons, nothing to click,
            // nothing that can steal focus from the timeline - Jordan keeps editing
            // while this fills in behind him. Hidden entirely when idle so it never
            // costs him vertical space (or reading effort) for nothing.
            {
                std::vector<Activity> recent;
                {
                    std::lock_guard<std::mutex> lk(g_activityMx);
                    size_t n = g_activityLog.size();
                    size_t from = n > 6 ? n - 6 : 0;   // newest few; the deque keeps 50
                    recent.assign(g_activityLog.begin() + (long)from, g_activityLog.end());
                }
                if (!recent.empty()) {
                    ImGui::TextDisabled("becky is working");
                    // WRAP. This panel is ~300px and these lines echo his own prompt back
                    // ("Thinking: compile every take where I said the intro line"), which
                    // ran off the edge mid-word. Text he needs is never truncated.
                    ImGui::PushTextWrapPos(0.0f);
                    for (auto it = recent.rbegin(); it != recent.rend(); ++it) {
                        // Colour carries the state so it reads at a glance without
                        // parsing words - "done" recedes, in-flight stands out.
                        ImVec4 col = (it->kind == "done") ? ImVec4(0.55f, 0.75f, 0.55f, 1.0f)
                                                          : ImVec4(0.95f, 0.85f, 0.45f, 1.0f);
                        ImGui::TextColored(col, "%s", it->text.c_str());
                        if (ImGui::IsItemHovered() && !it->source.empty())
                            ImGui::SetTooltip("%s (%s)", it->source.c_str(), it->kind.c_str());
                    }
                    ImGui::PopTextWrapPos();
                    ImGui::Separator();
                }
            }
            if (!g_cardsErr.empty()) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "%s", g_cardsErr.c_str());
            // The old "no review questions loaded" line is deliberately gone: with no
            // questions the panel now shows the status card, the chips and the ask bar -
            // something he can USE - instead of one grey sentence about an absence.
            if (!g_cards.empty()) {
                float cardsH = (std::min)((float)g_H * 0.45f, ImGui::GetContentRegionAvail().y * 0.62f);
                ImGui::BeginChild("cards", { 0, cardsH }, true);
                for (size_t i = 0; i < g_cards.size(); i++) {
                    QACard& c = g_cards[i];
                    ImGui::PushID((int)i);
                    ImVec4 col = ImGui::ColorConvertU32ToFloat4(cardColorFor(c.id));
                    ImGui::PushStyleColor(ImGuiCol_Text, col);
                    bool open = ImGui::CollapsingHeader(c.answered ? (c.question + "  [answered]").c_str() : c.question.c_str());
                    ImGui::PopStyleColor();
                    if (open) {
                        bool haveClip = false;
                        if (ImGui::SmallButton("Play tied clips")) {
                            // G-1: play EVERY clip tied to this answer, in order - not just the
                            // first match. Collect before mutating (seekToSpan-style track
                            // replacement clears g_track[0], so a live iterate-and-break would
                            // both skip later ties and corrupt the loop). Search the REAL reel
                            // (the pre-preview backup if one is already active), never a track
                            // that's already showing a different card's preview.
                            const std::vector<Clip>& realReel = g_inTiedPreview ? g_reelBeforePreview : g_track[0];
                            std::vector<Clip> tied;
                            for (auto& tc : realReel)
                                if (std::find(c.clipIDs.begin(), c.clipIDs.end(), tc.id) != c.clipIDs.end())
                                    tied.push_back(tc);
                            if (!tied.empty()) {
                                if (!g_inTiedPreview) { g_reelBeforePreview = g_track[0]; g_previewFrozenPlayhead = curSec; g_inTiedPreview = true; }
                                g_track[0].clear();
                                for (auto& tc : tied) g_track[0].push_back(tc);
                                packTrack(0); recomputeDur();
                                curSec = 0; playing = true; g_playingExt = true; lastComposed = -1;
                                g_quietDirty = true;
                                for (auto& tc : tied) peaksRequest(tc.source, tc.in - 1.0, tc.out + 5.0);
                                haveClip = true;
                            }
                        }
                        (void)haveClip;
                        ImGui::SameLine();
                        // CHECKLIST 25: "each clickable to type and submit an answer exactly
                        // like sending a chat". This retargets the ONE ask box below rather
                        // than growing a second input per card - one place the caret ever is.
                        if (ImGui::SmallButton("Answer this")) {
                            g_answerCardID = c.id; g_answerCardQ = c.question;
                            g_askBuf[0] = 0; g_askFocus = true;
                        }
                        if (!c.answer.empty()) ImGui::TextWrapped("answer: %s", c.answer.c_str());
                    }
                    ImGui::PopID();
                }
                ImGui::EndChild();
                ImGui::Separator();
            }

            // ---- the exchange (reference: .messages) ----
            // Wrapped, not TextDisabled: TextDisabled does not wrap, and this is the
            // question he just sent - clipping it at the panel edge hides what he asked.
            if (!g_askEcho.empty()) {
                ImGui::PushTextWrapPos(0.0f);
                ImGui::TextColored(ImGui::GetStyle().Colors[ImGuiCol_TextDisabled], "you: %s", g_askEcho.c_str());
                ImGui::PopTextWrapPos();
            }
            if (!g_askAnswer.empty()) ImGui::TextWrapped("%s", g_askAnswer.c_str());
            if (g_proposalPending) {
                ImGui::Separator();
                ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(1.0f, 0.85f, 0.25f, 1.0f));
                ImGui::TextWrapped("Proposed: %s", g_proposalPreview.c_str());
                ImGui::PopStyleColor();
                if (g_proposalDiff.is_array()) {
                    for (auto& dl : g_proposalDiff) {
                        std::string label = dl.value("label", std::string());
                        std::string before = dl.value("before", std::string());
                        std::string after = dl.value("after", std::string());
                        ImGui::BulletText("%s: %s -> %s", label.c_str(), before.c_str(), after.c_str());
                    }
                }
                // ASYNC. apply_proposal re-cuts the timeline server-side and took up to
                // 30s ON THE UI THREAD - the exact dead-window freeze engineCallAsync
                // exists to kill. The card is dismissed immediately (he sees his click
                // land) and the result arrives on the UI thread via drainAsync.
                if (ImGui::Button("Apply##proposal")) {
                    std::string pid = g_proposalID, prev = g_proposalPreview;
                    g_proposalPending = false; g_proposalID.clear();
                    engineCallAsync("apply_proposal", { {"id", pid} }, 120.0, "Applying becky's edit...",
                        [prev](const json& ar) {
                            if (ar.value("ok", false)) {
                                const json& d = ar.contains("data") ? ar["data"] : ar;
                                if (d.contains("timeline")) loadTimelineView(d["timeline"]);
                                g_askAnswer = "Applied: " + prev + " (Ctrl+Z reverts the whole pass)";
                            } else {
                                g_askAnswer = "Apply failed: " + ar.value("error", std::string("?"));
                            }
                        });
                }
                ImGui::SameLine();
                if (ImGui::Button("Reject##proposal")) {
                    std::string pid = g_proposalID;
                    g_askAnswer = "Rejected: " + g_proposalPreview;
                    g_proposalPending = false; g_proposalID.clear();
                    // A REAL label, never "": beginWork is newest-wins, so an empty one
                    // would blank the "becky is thinking..." text of an ask still in
                    // flight and leave an empty floating box on screen.
                    engineCallAsync("reject_proposal", { {"id", pid} }, 10.0,
                                    "Discarding that edit...", [](const json&) {});
                }
            }
            ImGui::EndChild();   // qamid

            // ================= pinned foot: chips + ask bar =================
            float footY0 = ImGui::GetCursorPosY();
            ImGui::Separator();

            // ---- suggestion chips (reference: .chip - neon pill, transparent fill) ----
            {
                ImGuiStyle& stl = ImGui::GetStyle();
                ImGui::PushStyleVar(ImGuiStyleVar_FrameRounding, ImGui::GetFrameHeight() * 0.5f);
                ImGui::PushStyleVar(ImGuiStyleVar_FrameBorderSize, 1.0f);
                ImGui::PushStyleColor(ImGuiCol_Button, ImVec4(0, 0, 0, 0));
                ImGui::PushStyleColor(ImGuiCol_ButtonHovered, ImVec4(neonV.x, neonV.y, neonV.z, 0.16f));
                ImGui::PushStyleColor(ImGuiCol_ButtonActive, ImVec4(neonV.x, neonV.y, neonV.z, 0.30f));
                ImGui::PushStyleColor(ImGuiCol_Text, neonV);
                ImGui::PushStyleColor(ImGuiCol_Border, ImVec4(neonV.x * 0.45f, neonV.y * 0.45f, neonV.z * 0.45f, 1.0f));
                float availW = ImGui::GetContentRegionAvail().x, used = 0.0f;
                for (int i = 0; i < 3; i++) {
                    float w = ImGui::CalcTextSize(kAskChipLabel[i]).x + stl.FramePadding.x * 2.0f;
                    if (i > 0 && used + stl.ItemSpacing.x + w <= availW) { ImGui::SameLine(); used += stl.ItemSpacing.x + w; }
                    else used = w;
                    // A chip FILLS the box and focuses it, never sends - he edits the
                    // wording before it costs a model turn. Short label on the chip so
                    // it fits the panel; the FULL prompt is what lands in the box.
                    if (ImGui::Button(kAskChipLabel[i])) {
                        snprintf(g_askBuf, sizeof g_askBuf, "%s", kAskChipPrompt[i]);
                        g_answerCardID.clear();
                        g_askFocus = true;
                    }
                    if (ImGui::IsItemHovered()) ImGui::SetTooltip("%s", kAskChipPrompt[i]);
                }
                ImGui::PopStyleColor(5);
                ImGui::PopStyleVar(2);
            }

            // ---- ask bar: input + green send (reference: .askbar / .sendbtn) ----
            if (!g_answerCardID.empty()) {
                ImGui::TextColored(neonV, "answering:");
                ImGui::SameLine();
                ImGui::TextDisabled("%s", g_answerCardQ.c_str());
                ImGui::SameLine();
                if (ImGui::SmallButton("cancel")) { g_answerCardID.clear(); g_answerCardQ.clear(); g_askBuf[0] = 0; }
            }
            {
                // MULTI-LINE, on its own row, Send underneath. A chip drops a 46-character
                // prompt in here; a single-line box ~209px wide would show under half of it
                // with no wrapping, i.e. he cannot read what he is about to send. Never
                // shrink a control on a user who cannot afford to squint at it.
                float inW = ImGui::GetContentRegionAvail().x;
                if (g_askFocus) { ImGui::SetKeyboardFocusHere(); g_askFocus = false; }
                bool submit = ImGui::InputTextMultiline("##ask", g_askBuf, sizeof g_askBuf,
                                                        ImVec2(inW, ImGui::GetFrameHeight() * 2.2f),
                                                        ImGuiInputTextFlags_EnterReturnsTrue);
                inputFocusBorder();
                if (g_askBuf[0] == 0) {
                    // Hint drawn by hand: InputTextMultiline has no WithHint variant.
                    ImVec2 mn = ImGui::GetItemRectMin();
                    ImGui::GetWindowDrawList()->AddText(
                        ImVec2(mn.x + ImGui::GetStyle().FramePadding.x, mn.y + ImGui::GetStyle().FramePadding.y),
                        ImGui::GetColorU32(ImGuiCol_TextDisabled),
                        g_answerCardID.empty() ? "ask becky..." : "type your answer...");
                }
                // Item 24: the send button = the reference's .sendbtn exactly - a GREEN chip
                // with the BLACK send-arrowhead GLYPH (U+27A4 "\xE2\x9E\xA4", already in the
                // atlas), NOT a hand-drawn triangle (which read as a play button). refBtnCore
                // draws it and gives it the same white hover glow as every other button.
                float sendW = ImGui::GetFrameHeight() * 1.6f;
                ImGui::PushStyleColor(ImGuiCol_Button, neonV);
                ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0, 0, 0, 1));
                if (refBtnCore("\xE2\x9E\xA4##send", sendW)) submit = true;
                if (ImGui::IsItemHovered()) ImGui::SetTooltip("Send");
                ImGui::PopStyleColor(2);

                // H-7: the forensic route - same box, second button. The query in the
                // box runs the WHOLE forensic pipeline (qmd recall + becky-judge LLM
                // pass + becky-hits reel build) through the ONE forensic_query verb,
                // and the resulting reel lands on the timeline as one undo span, with
                // the Q&A cards refreshed from the questions sidecar. Same wiring
                // shape as Apply##proposal above: async verb -> callback delivered on
                // the UI thread by drainAsync -> loadTimelineView. Hidden while the
                // box is retargeted to answer a Q&A card (that gesture owns the box);
                // disabled while a run is in flight (see g_forensicBusy). The H-5
                // activity feed narrates progress (started/progress/done) for free -
                // the engine already emits those events for this verb.
                if (g_answerCardID.empty()) {
                    ImGui::SameLine();
                    // High-contrast amber, deliberately distinct from the green Send:
                    // one button costs nothing, the other starts a minutes-long LLM
                    // pipeline - they must not look alike. (Accessibility rule: color
                    // is an aid here, never stripped.)
                    ImVec4 amber(1.0f, 0.72f, 0.18f, 1.0f);
                    float forW = ImGui::CalcTextSize("Forensic").x + ImGui::GetStyle().FramePadding.x * 2.0f;
                    ImGui::PushStyleColor(ImGuiCol_Button, amber);
                    ImGui::PushStyleColor(ImGuiCol_ButtonHovered, ImVec4(amber.x * 0.82f, amber.y * 0.82f, amber.z * 0.82f, 1.0f));
                    ImGui::PushStyleColor(ImGuiCol_ButtonActive, ImVec4(amber.x * 0.66f, amber.y * 0.66f, amber.z * 0.66f, 1.0f));
                    ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0, 0, 0, 1));
                    if (g_forensicBusy) ImGui::BeginDisabled();
                    bool fclick = ImGui::Button("Forensic", ImVec2(forW, 0));
                    if (g_forensicBusy) ImGui::EndDisabled();
                    ImGui::PopStyleColor(4);
                    if (ImGui::IsItemHovered())
                        ImGui::SetTooltip("Forensic search: recall + judge + reel.\nHits land on the timeline; can take minutes.\nThe activity feed above narrates progress.");
                    if (fclick && g_askBuf[0]) {
                        std::string q(g_askBuf);
                        g_askBuf[0] = 0;
                        g_askEcho = q;
                        g_askAnswer = "Forensic search running (recall + judge + reel)... this can take a few minutes.";
                        g_forensicBusy = true;
                        // 1800s: becky-judge alone is allowed 20 minutes for its LLM
                        // pass (BECKY_JUDGE_TIMEOUT), becky-hits 2 more. The timeout
                        // is the safety net, not the expectation - and it costs the
                        // UI nothing, the wait lives on engineCallAsync's own thread.
                        engineCallAsync("forensic_query", { {"query", q} }, 1800.0, "Forensic search...",
                            [](const json& r) {
                                g_forensicBusy = false;
                                if (r.value("ok", false)) {
                                    const json& d = r.contains("data") ? r["data"] : r;
                                    if (d.contains("timeline")) loadTimelineView(d["timeline"]);
                                    int n = d.value("clips", 0);
                                    g_askAnswer = "Forensic search done: " + std::to_string(n) +
                                                  " hit(s) on the timeline (one Ctrl+Z removes them all).";
                                    std::string note = d.value("note", std::string());
                                    if (!note.empty()) g_askAnswer += "\n(" + note + ")";
                                    // becky-hits writes a questions sidecar and the engine
                                    // loaded it; pull the fresh Q&A cards. In-memory verb,
                                    // measured-fast class of call (CONTINUE-HERE: median
                                    // 16ms, none stalls) - same as save_answer's refresh.
                                    refreshCards();
                                } else {
                                    g_askAnswer = "Forensic search failed: " + r.value("error", std::string("?"));
                                }
                            });
                    }
                }

                if (submit && g_askBuf[0]) {
                    std::string q(g_askBuf);
                    g_askBuf[0] = 0;
                    if (!g_answerCardID.empty()) {
                        // CHECKLIST 25: submitting an answer is the SAME gesture as sending a chat.
                        std::string aid = g_answerCardID, aq = g_answerCardQ;
                        g_answerCardID.clear(); g_answerCardQ.clear();
                        g_askEcho = q;
                        g_askAnswer = "Saving your answer...";
                        engineCallAsync("save_answer", { {"id", aid}, {"question", aq}, {"answer", q} },
                                        30.0, "Saving your answer...", [aq](const json& r) {
                            if (r.value("ok", false)) {
                                const json& d = r.contains("data") ? r["data"] : r;
                                if (d.contains("questions")) cardsFromJSON(d);
                                g_askAnswer = "Answer saved for: \"" + aq + "\". becky will route it into the wiki.";
                            } else {
                                g_askAnswer = "Could not save that answer: " + r.value("error", std::string("?"));
                            }
                        });
                    } else {
                        // ASYNC + 120s. This was engineCall("ask", ..., 30.0) straight on the UI
                        // thread: a Claude Code turn regularly runs 10-40s, so the window was
                        // dead (and often TIMED OUT at 30s on a good answer) every single ask.
                        // Off-thread the long timeout costs nothing.
                        g_askEcho = q;
                        g_askAnswer = "thinking...";
                        engineCallAsync("ask", { {"utterance", q} }, 120.0, "becky is thinking...",
                            [](const json& r) {
                                if (r.value("ok", false)) {
                                    const json& d = r.contains("data") ? r["data"] : r;
                                    g_askAnswer = d.value("preview_text", std::string());
                                    std::string note = d.value("note", std::string());
                                    if (g_askAnswer.empty()) g_askAnswer = note.empty() ? d.dump() : note;
                                    else if (!note.empty()) g_askAnswer += "\n(" + note + ")";
                                    // A mutating turn carries an id + at least one action - anything
                                    // else (a plain answer, a Tier-0 command that already ran) has
                                    // nothing to approve, so no card.
                                    std::string id = d.value("id", std::string());
                                    json actions = d.value("actions", json::array());
                                    if (!id.empty() && actions.is_array() && !actions.empty()) {
                                        g_proposalID = id;
                                        g_proposalPreview = d.value("preview_text", std::string("(edit proposed)"));
                                        g_proposalNote = note;
                                        g_proposalDiff = d.value("preview", json::array());
                                        g_proposalPending = true;
                                    } else {
                                        g_proposalPending = false;
                                    }
                                } else {
                                    g_askAnswer = r.value("error", std::string("ask failed"));
                                    g_proposalPending = false;
                                }
                            });
                    }
                }
            }
            s_qaBottomH = ImGui::GetCursorPosY() - footY0;
        }
        ImGui::End();
        stageMark("right-panel-qa");

        // ---- bottom timeline ----
        // ---- the >1s work indicator (feedback5) ----
        // Drawn LAST so it floats over every panel, and with no interaction of any
        // kind: no window decoration, no focus, no input capture. It must never be
        // a thing he has to dismiss or click past while editing. Bottom-right, out
        // of the way of the timeline he is working in.
        {
            std::string label; double since = 0; bool busy = false;
            {
                std::lock_guard<std::mutex> lk(g_workMx);
                busy = g_workCount > 0; label = g_workLabel; since = g_workSince;
            }
            // BECKY_REVIEW_WORKTEST=1 pins the indicator on so it can be SEEN
            // without waiting for a slow operation. This exists because the
            // obvious way to verify it - run a search - completes in 298ms on his
            // real 722-video library, i.e. correctly below the threshold, so the
            // honest check would otherwise be "I believe it works".
            if (getenv("BECKY_REVIEW_WORKTEST")) { busy = true; since = nowSec() - 3.0; label = "Self-test: work indicator"; }

            // The one-second threshold: below it an indicator is just flicker.
            if (busy && nowSec() - since > 1.0) {
                ImGui::SetNextWindowBgAlpha(0.62f);           // semi-transparent, as asked
                // Bottom-right of the UPPER half - i.e. resting just above the
                // timeline, never on it. The first version sat in the window's
                // bottom-right and covered the caption lane; "non-intrusive" cannot
                // mean "on top of the thing he is editing".
                ImGui::SetNextWindowPos({ (float)g_W - 20.0f, topY + topH - 12.0f }, ImGuiCond_Always, { 1.0f, 1.0f });
                if (ImGui::Begin("##work", nullptr,
                        ImGuiWindowFlags_NoDecoration | ImGuiWindowFlags_AlwaysAutoResize |
                        ImGuiWindowFlags_NoSavedSettings | ImGuiWindowFlags_NoFocusOnAppearing |
                        ImGuiWindowFlags_NoNav | ImGuiWindowFlags_NoInputs | ImGuiWindowFlags_NoMove)) {
                    double el = nowSec() - since;
                    ImGui::TextColored(ImVec4(0.95f, 0.85f, 0.45f, 1.0f), "%s", label.c_str());
                    // Indeterminate: none of these operations report real progress, and
                    // a fake percentage that stalls at 90% is worse than none. A moving
                    // bar says "alive", the elapsed count says how long - which is what
                    // he actually needs to decide whether to wait.
                    float t = (float)std::fmod(el, 1.6) / 1.6f;
                    ImVec2 p0 = ImGui::GetCursorScreenPos();
                    float bw = 220 * ImGui::GetIO().FontGlobalScale, bh = 6;
                    ImDrawList* d = ImGui::GetWindowDrawList();
                    d->AddRectFilled(p0, ImVec2(p0.x + bw, p0.y + bh), IM_COL32(255, 255, 255, 40), 3);
                    float sw = bw * 0.30f, sx = (bw + sw) * t - sw;
                    d->AddRectFilled(ImVec2(p0.x + std::max(0.0f, sx), p0.y),
                                     ImVec2(p0.x + std::min(bw, sx + sw), p0.y + bh),
                                     IM_COL32(242, 217, 115, 220), 3);
                    ImGui::Dummy(ImVec2(bw, bh));
                    ImGui::TextDisabled("%.0fs", el);
                }
                ImGui::End();
            }
        }

        ImGui::SetNextWindowPos({ 0, topY + topH });
        ImGui::SetNextWindowSize({ (float)g_W, timelineH });
        if (ImGui::Begin("timeline", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus)) {
            // CLICKING THE TIMELINE GIVES THE KEYBOARD BACK TO THE TIMELINE.
            //
            // Jordan reported this three separate times (feedback4, feedback6,
            // user-lag) and it was still broken. Type in the search box, click the
            // timeline, and every edit key was dead: an active InputText keeps
            // ImGui's WantCaptureKeyboard TRUE, and the whole timeline key block is
            // - correctly - gated on that, so typing can never split a clip. The
            // gate was right; nothing ever RELEASED it. He would click the timeline,
            // press S or Delete, and nothing happened, with no visible reason why.
            //
            // A click anywhere in this panel now moves window focus here, which
            // deactivates the InputText and drops WantCaptureKeyboard on the next
            // frame. Focus follows the click, the way it does in every NLE.
            if (ImGui::IsWindowHovered(ImGuiHoveredFlags_ChildWindows) &&
                (ImGui::IsMouseClicked(ImGuiMouseButton_Left) || ImGui::IsMouseClicked(ImGuiMouseButton_Right))) {
                ImGui::SetWindowFocus();
            }
            // ---- timeline header row (the reference app's .tlhead) ----
            // Left: "N clips - M:SS". Right of it at a FIXED x: the zoom readout.
            // Both existed in the WPF reference (#tlCount / #tZoom) and were the
            // only way to know at a glance that a reel actually loaded and how
            // deep the current zoom is.
            //
            // Round 5b: at 1:1 (scale 1.0) with the 16px font, (10,7) padding makes a
            // ~32px button - the reference's .btn height (7px vertical padding + 16px
            // line + 1px border). Matches becky-review-native.
            {
                ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2(10, 7));

                char durb[24]; fmtTime(g_compDur, durb, sizeof durb, false);
                size_t nclips = g_track[0].size();
                // NEON GREEN IS AN ACCESSIBILITY AID, not decoration - it is the
                // reference's --neon (#39ff14) and it is the one number he glances
                // at to confirm the reel is really loaded. Do not tone it down.
                ImGui::TextColored(ImVec4(0.224f, 1.0f, 0.078f, 1.0f), "%d clip%s - %s",
                                   (int)nclips, nclips == 1 ? "" : "s", durb);

                // THE ZOOM GROUP SITS AT A FIXED X, never SameLine() after the
                // variable-width count: "9 clips" vs "128 clips" would slide the
                // -/+ buttons sideways under his cursor between edits. Same
                // never-move rule fixedButton() exists for; fixedButton itself
                // cannot help here because the thing that changes width is the
                // TEXT BEFORE the buttons, not their labels.
                const float zoomX = ImGui::CalcTextSize("8888 clips - 88:88:88").x + 28.0f;
                const float bw = ImGui::GetFrameHeight();   // square, so both match

                ImGui::SameLine(zoomX);
                // -/+ do NOT do their own zoom math. They post the SAME request the
                // Up/Down keys post; drawTimeline drains it a few lines below via
                // applyWheel(), which is also the wheel's path. One zoom
                // implementation, three ways in.
                if (ImGui::Button("-##zoomout", ImVec2(bw, 0))) g_zoomReq = -1;
                if (ImGui::IsItemHovered())
                    ImGui::SetTooltip("Zoom the timeline out (also: Down arrow, or the wheel over the timeline)");

                ImGui::SameLine();
                char zb[24]; snprintf(zb, sizeof zb, "%.4g px/s", g_pps);
                ImGui::TextColored(ImVec4(0.78f, 0.81f, 0.85f, 1.0f), "%s", zb);

                // Slot sized for the WIDEST string %.4g can produce: 4 significant
                // digits plus a decimal point, e.g. "0.6613 px/s" at the 0.5 clamp.
                // "2000 px/s" is NOT the widest - every ordinary zoom step ("79.35",
                // "104.9", "183.5") is longer than it, and the button's opaque frame
                // would print over the last character of the number.
                ImGui::SameLine(zoomX + bw + ImGui::CalcTextSize("888888 px/s").x
                                + ImGui::GetStyle().ItemSpacing.x * 2.0f);
                if (ImGui::Button("+##zoomin", ImVec2(bw, 0))) g_zoomReq = 1;
                if (ImGui::IsItemHovered())
                    ImGui::SetTooltip("Zoom the timeline in (also: Up arrow, or the wheel over the timeline)");

                ImGui::PopStyleVar();
            }

            // ---- TRANSPORT / TOOL / ACTION TOOLBAR (item 2, round 3) ----
            // Moved HERE, into the timeline's own header row next to the clip count
            // and zoom control, from its old home under the video pane - matching the
            // reference app's single .tlhead row and killing the dead blank strip
            // that used to sit above the ruler (Jordan's #1 complaint, "deceptive").
            // Continues the SAME row the count/zoom above just drew.
            //
            // Item 3 (round 4): RIGHT-ALIGN the whole cluster to the window's right
            // edge - the reference's .tlspacer { flex:1 } sits between the count/zoom
            // and .transport/.tlactions and pushes them all the way right. ImGui is
            // immediate-mode so the cluster's total width isn't known until it has
            // been drawn: measure it on the PREVIOUS frame (s_toolbarW) and apply it
            // now. It self-corrects in one frame; at worst a width change (a selection
            // count appearing) is a single-frame nudge. Never push LEFT of where the
            // cluster naturally sits, so a too-narrow window keeps it left-aligned
            // instead of overlapping the count/zoom.
            static float s_toolbarW = 0.0f;
            ImGui::SameLine(0.0f, 18.0f);
            {
                float naturalX = ImGui::GetCursorPosX();
                float rightEdge = ImGui::GetWindowContentRegionMax().x;
                float startX = (s_toolbarW > 0.0f) ? std::max(naturalX, rightEdge - s_toolbarW) : naturalX;
                ImGui::SetCursorPosX(startX);
            }
            float toolbarStartScreenX = ImGui::GetCursorScreenPos().x;
            {
                if (fixedButton(playing ? ico(ICON_PAUSE "##play", "Pause##play") : ico(ICON_PLAY "##play", "Play##play"),
                                { ico(ICON_PAUSE, "Pause"), ico(ICON_PLAY, "Play") })) {
                    // Same rule as Space, via the same helper. This button used to
                    // just flip the flag, so the stock return (E-6) - and now the
                    // play-start return - happened on the KEY but not on the BUTTON.
                    if (playing) stopPlayback(curSec, playing, true);
                    else { playing = true; g_playingExt = true; }
                }
                if (ImGui::IsItemHovered()) ImGui::SetTooltip(playing ? "Pause" : "Play");
                ImGui::SameLine();
                // "|<<" was never a label, it was a puzzle. The skip-to-start glyph
                // says the same thing without being read.
                if (refBtn(ico(ICON_START "##home", "|<<"))) { curSec = 0; g_playingExt = playing; }
                if (ImGui::IsItemHovered()) ImGui::SetTooltip("Back to start");
                ImGui::SameLine();
            }
            {
                // Item 7 (round 3): shows the CURRENT rate - "1x" at rest, matching
                // the reference exactly - and CYCLES 1x -> 1.5x -> 2x on each click.
                // The old button always said the word "2x" even while playing at
                // 1x, which read as if double speed were already engaged. NO colour
                // change on this one (explicitly asked for) - it is a transport
                // control, not an on/off state like the toggles beside it.
                const char* speedLabel = g_playRate > 1.75 ? "2x##speed" : g_playRate > 1.25 ? "1.5x##speed" : "1x##speed";
                if (fixedButton(speedLabel, { "1x", "1.5x", "2x" }))
                    g_playRate = g_playRate > 1.75 ? 1.0 : (g_playRate > 1.25 ? 2.0 : 1.5);
                if (ImGui::IsItemHovered()) ImGui::SetTooltip("Playback speed - click to cycle 1x -> 1.5x -> 2x (Shift+Space plays at 2x)");
            }
            ImGui::SameLine();
            // EXTEND THE SELECTED CLIP BY ONE FRAME. Jordan cuts to the frame - "a
            // microsecond difference means you're cutting off consonants" - and
            // dragging an edge with the mouse cannot reliably land on a single frame
            // at any sane zoom. These two buttons move one edge by exactly one frame
            // OF THAT CLIP'S OWN SOURCE RATE (29.97 is not 30; a 30fps assumption
            // drifts a frame every 33 seconds). ONE set_trim per press = ONE Ctrl+Z
            // per press, deliberately.
            //
            // Item 13: FILLED blue (#00AEEF), matching the reference screenshot's
            // bold rounded blue pair - distinct from every other button on the row,
            // and from the running-figure/broom icons sitting right beside them.
            {
                Clip* sc = nullptr;
                for (auto& c : g_track[0]) if (g_sel.count(c.id)) { sc = &c; break; }
                bool canTrim = sc && !sc->id.empty() && !g_editsInFlight.count(sc->id);
                if (!canTrim) ImGui::BeginDisabled();
                double fps = sc ? sourceFps(sc->source) : 30.0;
                if (fps <= 0) fps = 30.0;
                const double oneFrame = 1.0 / fps;
                // Item 16: BLUE TEXT on the dark chip (matches the reference .tbtn2.extend),
                // NOT a solid blue fill. refBtnCore hovers it white-text + blue-border + white glow.
                ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0x00 / 255.0f, 0xAE / 255.0f, 0xEF / 255.0f, 1.0f));
                if (fixedButton("[\xE2\x97\x80##extl", { "[\xE2\x97\x80" }) && canTrim && sc->in > oneFrame) {   // [<| reference tExtendL
                    // Item 4 fix lives in loadTimelineView (see its comment) - this
                    // press no longer deselects the clip it just operated on.
                    EditReq req; req.verb = "set_trim";
                    req.args = { {"id", sc->id}, {"in", sc->in - oneFrame}, {"out", sc->out} };
                    req.kind = 2; req.t = curSec; req.group = g_group;
                    g_editsInFlight.insert(sc->id);
                    queueEdit(std::move(req));
                }
                if (ImGui::IsItemHovered()) ImGui::SetTooltip("Extend the selected clip one frame EARLIER (its own source rate)");
                ImGui::SameLine();
                if (fixedButton("\xE2\x96\xB6]##extr", { "\xE2\x96\xB6]" }) && canTrim) {   // |>] reference tExtendR
                    EditReq req; req.verb = "set_trim";
                    req.args = { {"id", sc->id}, {"in", sc->in}, {"out", sc->out + oneFrame} };
                    req.kind = 2; req.t = curSec; req.group = g_group;
                    g_editsInFlight.insert(sc->id);
                    queueEdit(std::move(req));
                }
                if (ImGui::IsItemHovered()) ImGui::SetTooltip("Extend the selected clip one frame LATER (its own source rate)");
                ImGui::PopStyleColor(1);
                if (!canTrim) ImGui::EndDisabled();
            }
            ImGui::SameLine();
            // Split at the playhead - the reference toolbar's scissors button. Same
            // split the 'S' key already does; duplicated inline rather than sharing
            // editT()/the key handler's own debounce state, which is scoped to that
            // handler's block - a few lines here is smaller than threading a new
            // helper through both call sites for one button.
            {
                if (refBtn(ico(ICON_SCISSORS "##split", "Split"))
                    && nowSec() - g_lastSplitQueued > kEditDebounceSec) {
                    double t = (playing && g_stockSec >= 0) ? g_stockSec : curSec;
                    Clip* c = clipAtComp(0, t);
                    bool noId = c && c->id.empty();
                    bool gated = c && !noId && g_editsInFlight.count(c->id);
                    if (c && noId && !g_promoteInFlight) {
                        double srcT = c->in + (t - c->compStart);
                        EditReq req; req.verb = "split"; req.args = { {"at", srcT} };
                        req.kind = 0; req.t = t; req.group = g_group;
                        req.promote = true; req.pSource = c->source; req.pIn = c->in; req.pOut = c->out; req.pLabel = c->label;
                        g_promoteInFlight = true;
                        g_lastSplitQueued = nowSec();
                        queueEdit(std::move(req));
                    } else if (c && !noId && !gated) {
                        double srcT = c->in + (t - c->compStart);
                        EditReq req; req.verb = "split"; req.args = { {"id", c->id}, {"at", srcT} };
                        req.kind = 0; req.t = t; req.group = g_group;
                        g_editsInFlight.insert(c->id);
                        g_lastSplitQueued = nowSec();
                        queueEdit(std::move(req));
                    }
                    if (!playing) lastComposed = -1;
                }
                if (ImGui::IsItemHovered()) ImGui::SetTooltip("Split clip at playhead (S)");
            }
            ImGui::SameLine();
            // D-5: screenshot the preview frame. Engine verb already existed
            // (grab_frame); only the button was missing.
            if (refBtn(ico(ICON_CAMERA "##shot", "Screenshot"))) {
                Clip* cur = nullptr;
                for (auto& c : g_track[0]) if (curSec >= c.compStart && curSec < c.compStart + (c.out - c.in)) { cur = &c; break; }
                if (!cur && !g_track[0].empty()) cur = &g_track[0].back();
                if (cur) {
                    // COPY the source and time out of the Clip before going async. The
                    // Clip* must never be captured: g_track is rebuilt by every edit
                    // reply, so a pointer into it is dangling by the time a reply lands.
                    std::string src = cur->source;
                    double srcT = cur->in + (curSec - cur->compStart);
                    engineCallAsync("grab_frame", { {"source", src}, {"t", srcT} }, 20.0,
                                    "Saving a screenshot...", [](const json& r) {
                        // r.value("data", json::object()) never vivifies a null; always safe.
                        // Item 7 (round 4): Jordan "couldn't find where the files get
                        // screenshotted to". Open the containing folder on save - the
                        // exact behaviour render/export/EDL already use - so the file's
                        // location is never a mystery again (it lands in the clip's
                        // render folder, <clip-stem>_<seconds>s.png).
                        if (r.value("ok", false)) {
                            std::string path = r.value("data", json::object()).value("path", std::string());
                            g_renderMsg = "Saved screenshot " + path;
                            if (!path.empty()) openInFileBrowser(path);
                        } else {
                            g_renderMsg = "Screenshot failed: " + r.value("error", std::string("?"));
                        }
                        g_renderMsgAt = nowSec();
                    });
                } else { g_renderMsg = "Screenshot failed: no clip at playhead"; g_renderMsgAt = nowSec(); }
            }
            if (ImGui::IsItemHovered()) ImGui::SetTooltip("Screenshot the frame at the playhead");
            ImGui::SameLine();
            // E-10 SKIP QUIET - during playback, everything under the loudness
            // threshold is SKIPPED seamlessly instead of played. Colour carries the
            // state as well as the shape: neon green with dark ink when armed (same
            // active state every other toggle uses), dim slate when off.
            {
                if (g_thrOn) {
                    ImGui::PushStyleColor(ImGuiCol_Button, ImGui::ColorConvertU32ToFloat4(kPalette[0]));
                    ImGui::PushStyleColor(ImGuiCol_Text,   ImVec4(0, 0, 0, 1));
                } else {
                    ImGui::PushStyleColor(ImGuiCol_Text,   ImVec4(0.59f, 0.63f, 0.69f, 1.0f));
                }
                bool pressed = fixedButton(ico(ICON_RUN "##thr", "Skip Quiet##thr"),
                                           { ico(ICON_RUN, "Skip Quiet") });
                ImGui::PopStyleColor(g_thrOn ? 2 : 1);
                if (pressed) {
                    g_thrOn = !g_thrOn;
                    g_quietDirty = true;      // force recomputeQuiet on the next frame
                    emitThreshold(true);
                }
                if (ImGui::IsItemHovered())
                    ImGui::SetTooltip("Skip quiet parts during playback: %s\nDrag the bar on the timeline to set the level.",
                                      g_thrOn ? "ON" : "OFF");
            }
            ImGui::SameLine();
            // The broomstick - sweeps every quiet span (at the SAME threshold the
            // bar above sets) out of the timeline for good.
            if (broomButton()) applyRemoveSilence(curSec, lastComposed);
            if (ImGui::IsItemHovered())
                ImGui::SetTooltip("Remove all silent parts from the timeline\n(uses the threshold bar's level - one Ctrl+Z undoes the whole sweep)");
            ImGui::SameLine();
            // Item 9: captions ON/OFF, GLYPH not the words "Captions: On/Off" - a
            // checkmark suffix + green tint when on, plain otherwise. Off hides both
            // the timeline caption lane and the preview overlay text.
            {
                const bool capOn = g_capsOn;
                if (capOn) {
                    ImGui::PushStyleColor(ImGuiCol_Button, ImGui::ColorConvertU32ToFloat4(kPalette[0]));
                    ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0, 0, 0, 1));
                }
                // Item 21: NO checkmark - the button just reads "captions". Green fill +
                // black text when ON (item 20), plain white-on-dark when off (the reference).
                if (fixedButton("captions##caps", { "captions" }))
                    g_capsOn = !g_capsOn;
                if (capOn) ImGui::PopStyleColor(2);
                if (ImGui::IsItemHovered()) ImGui::SetTooltip("Show/hide captions (timeline lane + preview overlay): %s", capOn ? "ON" : "OFF");
            }
            ImGui::SameLine();
            // Item 8: CLI-CUT captions - becky-subtitle.exe, NOT the Parakeet
            // per-clip transcript the toggle above falls back to. Blue (#00AEEF),
            // NOT the amber "Forensic" already uses - amber there means "runs an
            // LLM search pipeline"; reusing it here would read as "another
            // forensic thing" instead of "captions, just a better source".
            {
                bool busy = g_cliCutBusy.load();
                bool noClips = g_track[0].empty();
                if (busy || noClips) ImGui::BeginDisabled();
                // Item 16: "get captions" (was CLI-CUT) = BLUE TEXT on the dark chip (matches
                // the reference's blue action buttons), NOT the banned solid-blue fill.
                // refBtnCore hovers it white-text + blue-border + white glow.
                ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0x00 / 255.0f, 0xAE / 255.0f, 0xEF / 255.0f, 1.0f));
                if (fixedButton(busy ? "get captions...##clicut" : "get captions##clicut", { "get captions...", "get captions" }))
                    triggerGetCaptions();
                ImGui::PopStyleColor(1);
                if (busy || noClips) ImGui::EndDisabled();
                if (ImGui::IsItemHovered())
                    ImGui::SetTooltip("Build real TikTok-style captions with becky-subtitle: snapped to your\ncut points and phrase-broken - not just the raw Parakeet forensic transcript.");
            }
            ImGui::SameLine();
            // Item 9: 3-state provenance overlay (off / on-hidden-in-preview / on-
            // shown) - a GLYPH per state (x / eye / check), not the words "Overlay:
            // On (hidden)". Render always burns in whichever text "on-previewed"
            // would show, since Render.Enabled tracks mode!=0 (setOverlayMode).
            {
                // Round 5c: EXACTLY the reference's overlay button (app.js):
                //   off  (not rendering)      -> "overlay ✗" DIMMED  (x)
                //   on, not previewed         -> "overlay ✓" normal  (check)
                //   on AND previewed          -> "overlay \U0001F441" GREEN (eye emoji, color)
                // g_ovMode 0/1/2 == off / on-hidden / on-shown maps straight onto that.
                const char* ovLabel = g_ovMode == 0 ? "overlay \xE2\x9C\x97##ov"           // x
                                    : g_ovMode == 1 ? "overlay \xE2\x9C\x93##ov"           // check
                                                    : "overlay \xF0\x9F\x91\x81##ov";      // eye
                // CAPTURE the push count BEFORE the click. clicking calls setOverlayMode
                // which changes g_ovMode, so re-reading g_ovMode for the Pop would pop the
                // WRONG number -> a PushStyleColor/PopStyleColor imbalance -> ImGui crash
                // (found live: "clicking overlay crashes the app").
                int ovPushed = 0;
                if (g_ovMode == 0) {
                    ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0.54f, 0.54f, 0.56f, 1.0f)); // dimmed
                    ovPushed = 1;
                } else if (g_ovMode == 2) {
                    ImGui::PushStyleColor(ImGuiCol_Button, ImGui::ColorConvertU32ToFloat4(kPalette[0]));
                    ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0, 0, 0, 1));
                    ovPushed = 2;
                }
                if (fixedButton(ovLabel, { "overlay \xE2\x9C\x97", "overlay \xE2\x9C\x93", "overlay \xF0\x9F\x91\x81" }))
                    setOverlayMode((g_ovMode + 1) % 3);
                if (ovPushed) ImGui::PopStyleColor(ovPushed);
                if (ImGui::IsItemHovered())
                    ImGui::SetTooltip("Forensic lower-third overlay: %s",
                        g_ovMode == 0 ? "off" : g_ovMode == 1 ? "on (hidden in preview, still burns into export)" : "on (shown in preview)");
            }
            ImGui::SameLine();
            // NEW (round 3): the overlay's filename LINE, on/off - existed as state
            // (g_overlay.showFilename) with no button to toggle it. Green-when-on,
            // matching the reference's "name" pill exactly.
            {
                const bool nameOn = g_overlay.showFilename;
                if (nameOn) {
                    ImGui::PushStyleColor(ImGuiCol_Button, ImGui::ColorConvertU32ToFloat4(kPalette[0]));
                    ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0, 0, 0, 1));
                }
                if (refBtn("name##ovname")) g_overlay.showFilename = !g_overlay.showFilename;
                if (nameOn) ImGui::PopStyleColor(2);
                if (ImGui::IsItemHovered())
                    ImGui::SetTooltip("Include the filename line in the overlay (off = date/timecode/link only): %s", nameOn ? "ON" : "OFF");
            }
            ImGui::SameLine(0.0f, 18.0f);
            // Undo/Redo - ALWAYS enabled, deliberately. The engine owns both stacks
            // and reports neither depth over the bridge, so "grey it out when empty"
            // would mean GUESSING at emptiness.
            {
                if (refBtn(ico(ICON_UNDO "##undobtn", "Undo"))) queueUndo(curSec);
                if (ImGui::IsItemHovered()) ImGui::SetTooltip("Undo the last edit  (Ctrl+Z)");
                ImGui::SameLine();
                if (refBtn(ico(ICON_REDO "##redobtn", "Redo"))) queueRedo(curSec);
                if (ImGui::IsItemHovered()) ImGui::SetTooltip("Redo the edit you just undid  (Ctrl+Y or Ctrl+Shift+Z)");
                ImGui::SameLine();
            }
            // save / load - plain text, matching the reference exactly (no icon).
            if (refBtn("save##savereel")) {
                engineCallAsync("save_reel", { {"path", ""} }, 20.0, "Saving reel...", [](const json& r) {
                    g_renderMsg = r.value("ok", false) ? "Saved reel " + r.value("data", json::object()).value("path", std::string()) : "Save reel failed: " + r.value("error", std::string("?"));
                    g_renderMsgAt = nowSec();
                });
            }
            if (ImGui::IsItemHovered()) ImGui::SetTooltip("Save Reel");
            ImGui::SameLine();
            if (refBtn("load##loadreel")) {
                std::string picked = pickOpenReelFile(hwnd);
                if (!picked.empty()) {
                    std::string path = convertEditIfNeeded(picked);   // .txt/.xml Vegas/FCP export -> reel .json
                    if (!path.empty()) {
                        engineCallAsync("load_reel", { {"path", path} }, 30.0, "Loading reel...",
                                        [path, &curSec, &playing, &lastComposed](const json& r) {
                            if (r.value("ok", false)) { loadTimelineView(r.contains("data") ? r["data"] : r); curSec = 0; playing = false; g_playingExt = false; lastComposed = -1; loadCaptions(path); g_renderMsg = "Loaded reel " + baseName(path); }
                            else g_renderMsg = "Load reel failed: " + r.value("error", std::string("?"));
                            g_renderMsgAt = nowSec();
                        });
                    }
                }
            }
            if (ImGui::IsItemHovered()) ImGui::SetTooltip("Load Reel");
            ImGui::SameLine();
            // Item 6: render selection - GRAYED (not blue) with no count shown when
            // nothing is selected, matching the reference's disabled look exactly;
            // blue + white + the "(N)" count once clips ARE selected. An explicit
            // two-branch style, not just BeginDisabled's alpha-multiply on top of the
            // always-blue push this used to be - Jordan's own words, it read "too
            // loud for a rare action" even dimmed.
            {
                bool hasSel = !g_sel.empty();
                char selLabel[48];
                if (hasSel) snprintf(selLabel, sizeof selLabel, "render selection (%d)##rensel", (int)g_sel.size());
                else snprintf(selLabel, sizeof selLabel, "render selection##rensel");
                // Item 22: selected -> a NORMAL white-text-on-dark chip (refBtnCore hovers it
                // neon text + white glow); NOT a solid-blue fill (banned). Nothing selected ->
                // greyed + disabled, matching the reference's disabled render-selection look.
                if (!hasSel) {
                    ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0.42f, 0.42f, 0.45f, 1.0f));
                    ImGui::BeginDisabled();
                }
                if (fixedButton(selLabel, { "render selection (000)", "render selection" })) {
                    std::vector<std::string> ids(g_sel.begin(), g_sel.end());
                    engineCallAsync("export_selection", { {"ids", ids}, {"output", ""} }, 300.0,
                                    "Rendering the selected clips...", [](const json& r) {
                    if (r.value("ok", false)) {
                        const json& d = r.contains("data") ? r["data"] : r;
                        std::string caps = d.value("captions", std::string());
                        g_renderMsg = "Rendered " + d.value("mp4", std::string()) +
                                      (caps.empty() ? "  - NO captions (use export for a captioned file)"
                                                    : "  - captions burned in");
                        openInFileBrowser(d.value("mp4", std::string()));
                    } else g_renderMsg = "Render failed: " + r.value("error", std::string("?"));
                    g_renderMsgAt = nowSec();
                    });
                }
                if (!hasSel) { ImGui::EndDisabled(); ImGui::PopStyleColor(1); }
            }
            ImGui::SameLine();
            // Item 5: "Render" -> "export", GREEN (#39FF14) fill + BLACK text - the
            // primary action, matching the reference exactly.
            {
                ImGui::PushStyleColor(ImGuiCol_Button, ImGui::ColorConvertU32ToFloat4(kPalette[0]));
                ImGui::PushStyleColor(ImGuiCol_ButtonHovered, ImVec4(0.42f, 1.0f, 0.26f, 1.0f));
                ImGui::PushStyleColor(ImGuiCol_ButtonActive, ImVec4(0.16f, 0.72f, 0.06f, 1.0f));
                ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0, 0, 0, 1));
                // Item 18: draw via refBtn so export gets the same white outer glow on hover
                // as every other button (green fill + black text preserved).
                if (refBtn("export##doexport")) {
                    engineCallAsync("export", { {"output", ""} }, 300.0, "Rendering video...", [](const json& r) {
                    if (r.value("ok", false)) {
                        const json& d = r.contains("data") ? r["data"] : r;
                        std::string caps = d.value("captions", std::string());
                        g_renderMsg = "Rendered " + d.value("mp4", std::string()) +
                                      (caps.empty() ? "  - NO captions in this file"
                                                    : "  - captions burned in");
                        openInFileBrowser(d.value("mp4", std::string()));
                    } else g_renderMsg = "Render failed: " + r.value("error", std::string("?"));
                    g_renderMsgAt = nowSec();
                    });
                }
                ImGui::PopStyleColor(4);
            }
            // Export EDL - not in the reference's button row, but a working
            // Vegas/FCP-interchange feature nobody asked to remove; kept, just
            // relegated to a small trailing text button so it never competes with
            // the primary row the reference actually shows.
            ImGui::SameLine();
            if (refBtn("Export EDL##writeedl")) {
                engineCallAsync("write_edl", { {"output", ""} }, 30.0, "Writing EDL...", [](const json& r) {
                    if (r.value("ok", false)) { std::string p = r.value("data", json::object()).value("path", std::string()); g_renderMsg = "Wrote EDL " + p; openInFileBrowser(p); }
                    else g_renderMsg = "Export EDL failed: " + r.value("error", std::string("?"));
                    g_renderMsgAt = nowSec();
                });
            }
            // Item 3 (round 4): the last toolbar item just drew - its right edge minus
            // the cluster's start edge IS the cluster width the NEXT frame right-aligns
            // by. Measured in screen space (SetCursorPosX offsets don't distort a
            // width delta), stored in the static above.
            s_toolbarW = ImGui::GetItemRectMax().x - toolbarStartScreenX;

            // Item 1 fix (round 3): "previewing a quote still shows it on the
            // timeline" - the whole point of a preview is that the timeline must
            // NOT visibly change for its whole duration, not just snap back once
            // it ends. g_track[0]/g_compDur are swapped to the ONE-clip (or
            // tied-clips) audition for playback purposes elsewhere in the frame
            // (engineReelEnter reads g_track[0] directly - decoupling that is a
            // bigger change than this render fix needs); here, for THIS ONE DRAW
            // CALL ONLY, swap the REAL reel + duration + the playhead position it
            // had before the preview started back in, render that, then swap the
            // live preview state straight back so playback is completely
            // unaffected. Gesture handling is separately gated off by
            // g_inTiedPreview above (pressed/active/released), so this frozen
            // render is read-only, matching what it visually claims to be.
            if (g_inTiedPreview) {
                std::vector<Clip> livePreviewTrack = g_track[0];
                double livePreviewDur = g_compDur;
                g_track[0] = g_reelBeforePreview;
                recomputeDur();
                double frozenCur = std::min(g_previewFrozenPlayhead, g_compDur);
                drawTimeline(frozenCur, playing);
                if (g_inTiedPreview) {
                    // still auditioning: put the live preview clip back for playback
                    g_track[0] = livePreviewTrack;
                    g_compDur = livePreviewDur;
                } else {
                    // Round 5: a click on the timeline inside drawTimeline ENDED the
                    // audition (it cleared g_inTiedPreview and already selected/seeked
                    // on the reel, which is what g_track[0] now holds). Keep the reel,
                    // stop the audition, and adopt the clicked playhead position.
                    g_reelBeforePreview.clear();
                    playing = false; g_playingExt = false;
                    curSec = frozenCur;
                    lastComposed = -1; g_quietDirty = true;
                    for (auto& c : g_track[0]) peaksRequest(c.source, c.in - 1.0, c.out + 5.0);
                }
            } else {
                drawTimeline(curSec, playing);
            }
        }
        ImGui::End();
        stageMark("bottom-timeline");

        } catch (const std::exception& e) {
            crashLog(std::string("UI frame: caught ") + e.what() + " - frame degraded, not crashing");
        } catch (...) {
            crashLog("UI frame: caught non-std exception - frame degraded, not crashing");
        }

        ImGui::Render();
        stageMark("imgui-render");
        float clr[4] = { 0.06f, 0.07f, 0.09f, 1.0f };
        g_ctx->OMSetRenderTargets(1, &g_rtv, nullptr);
        g_ctx->ClearRenderTargetView(g_rtv, clr);
        ImGui_ImplDX11_RenderDrawData(ImGui::GetDrawData());
        g_swap->Present(0, 0);   // no driver vsync-wait (that busy-spun); DwmFlush at the loop top paces us
        stageMark("present");
    }

    if (g_frameTrace.is_open()) {
        g_frameTrace << "# total_frames=" << frameIdx << " stalls_over_100ms=" << g_frameTraceStalls << "\n";
        g_frameTrace.flush();
    }
    if (hiResTimerOn) timeEndPeriod(1);   // restore the system clock-interrupt rate; see the loop-top comment

    engineShutdown();
    engine::shutdown();
    // I-8: shut down the background pool; all worker threads join here.
    delete g_bgPool; g_bgPool = nullptr;
    ImGui_ImplDX11_Shutdown(); ImGui_ImplWin32_Shutdown(); ImGui::DestroyContext();
    if (g_rtv) g_rtv->Release(); if (g_swap) g_swap->Release(); if (g_ctx) g_ctx->Release(); if (g_dev) g_dev->Release();
    return 0;
}
