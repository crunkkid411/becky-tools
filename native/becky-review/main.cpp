// becky-review - the full-native single-window Becky Review (phases 3+4 start).
//
// Grown from native/becky-timeline (Dear ImGui + D3D11), video pane driven by mpv.
// ONE process owns the whole window - no WebView2, no WPF, no airspace:
//   left  = library / search / transcript (ImGui)
//   center = video pane (mpv, --wid child of our hwnd, hwdec GPU decode)
//   right = Q&A / ask-becky (ImGui)
//   bottom = native timeline (the seed's code, in-process instead of embedded)
//
// The Go engine (becky-review-engine.exe, the clip cmd's stdin/stdout bridge mode)
// is the ONE brain: folder index, search, qmd, reel/EDL, peaks, export,
// ask. This process is VIEW/CONTROLLER only - every edit routes to engine verbs,
// engine undo is THE undo. NDJSON seam = {"id","verb","args":{...}} -> {"id","reply":{ok,data,error}}.
//
// D-1 (2026-07-19): the video pane is mpv (runtime/mpv/mpv.exe, fetched via
// fetch-mpv.ps1), embedded as a genuine WS_CHILD hwnd via --wid, driven over its
// JSON IPC named pipe (see MpvEmbed below) - no libmpv linking, same subprocess+
// pipe pattern as the Go engine seam. Every UI-thread control loop (curSec, the
// dt-driven playhead, threshold-skip, stock playhead, edit application) is
// UNCHANGED from the prior GStreamer build: curSec stays the single authoritative
// clock and the decode thread is simply told "show this exact frame" via an mpv
// hr-seek instead of a GStreamer pull - a render-backend swap, not an architecture
// change. GStreamer itself stays linked and initialized (gstInitSEH) because
// peaksWorker still uses it for one-time per-source audio decode into the .bpk
// peak cache (E-2) - that pipeline is unrelated to the video player and untouched.
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <shellapi.h>
#include <commdlg.h>
#include <d3d11.h>
#include <wincodec.h> // E-11: WIC decodes the thumb verb's JPEG - native platform feature, no image lib dependency
#include "imgui.h"
#include "imgui_impl_win32.h"
#include "imgui_impl_dx11.h"
#include "json.hpp"

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
    g_editLog << nowSec() << " " << line << "\n"; g_editLog.flush();
}

// I-5 evidence trail, OPT-IN via BECKY_REVIEW_SCRUB_LOG=<path> (unset = zero
// overhead, no file touched). Logs every requestCompose() call (UI thread, one
// per frame whose curSec changed) and every composeOnDecodeThread() completion
// (decode thread, the actual mpv seek) with wall-clock timestamps, so "a new
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
// false; every GStreamer call site below (peaksWorker, decodeWorker/composeOnDecodeThread,
// the video pane draw, shutdown) checks it first, so the window still opens (shell, library,
// timeline, search all work) and the video pane shows a plain "video decode unavailable"
// note instead of the whole app dying before CreateWindow ever runs.
static std::atomic<bool> g_gstAvailable{ false };
static int gstInitSEH(int argc, char** argv) {
    __try {
        gst_init(&argc, &argv);
        // FIX (cycle-4, root-caused via isolated repro): GStreamer/GLib lazily create
        // GLib's internal "pool-spawner" thread-pool manager on the FIRST pipeline state
        // change anywhere in the process. If that first-ever call happens on a thread
        // already in THREAD_MODE_BACKGROUND_BEGIN - which every peaksWorker decode thread
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

// --------------- D-1: mpv embedded video pane (--wid child hwnd over JSON IPC) ---------------
// Same subprocess+pipe shape as the Go engine seam (CreateProcessW + a pipe reader
// thread), except mpv's IPC pipe is a NAMED pipe it creates itself (--input-ipc-
// server) rather than inherited stdio handles. #0-style guard: if mpv.exe is
// missing, CreateProcess fails, or the pipe never comes up, g_mpvAvailable stays
// false and the video pane shows a plain degrade note instead of hanging or
// crashing - mirrors gstInitSEH/g_gstAvailable exactly, just for a subprocess
// failure mode instead of a native in-process one (no SEH needed here: CreateProcess
// failing is an ordinary Win32 return value, not a hardware exception).
static std::atomic<bool> g_mpvAvailable{ false };
static HWND g_mpvHwnd = nullptr;
static PROCESS_INFORMATION g_mpvProc{};
static HANDLE g_mpvPipe = INVALID_HANDLE_VALUE;       // write side (mpvWriteLine only)
static HANDLE g_mpvPipeRead = INVALID_HANDLE_VALUE;   // read side (mpvReaderThread only)
static std::string g_mpvPipeName;
static std::mutex g_mpvWriteMx;
static std::string g_mpvLoadedSource;   // which source file mpv currently has open (fwslash'd)
// D-9: g_mpvLoadedSource is written by the decode thread (mpvSeekExact) AND by the
// UI thread when it hands mpv the whole-reel EDL (mpvEdlEnter) - guard it.
static std::mutex g_mpvSrcMx;

// --------------- D-9: mpv actually PLAYS (this is where the audio was missing) ---------------
// The bug Jordan hit: mpv was launched --pause=yes and NOTHING ever unpaused it, so it
// was only ever a frame SCRUBBER - the app simulated playback itself (curSec += dt) and
// commanded a per-frame hr-seek. A paused mpv decodes stills and emits NO AUDIO, which is
// the entire "Becky Review 3 has no audio" report. Fix (mirroring becky-timeline's split,
// where mpv is the preview and genuinely plays): hand mpv the whole reel as an EDL and let
// it PLAY, then sync the app clock FROM mpv's time-pos instead of commanding it. A/V sync
// and the source's true rate (29.97 = 30000/1001) then come from mpv's own clock for free.
// g_edlActive lives up here (not with the EDL code below, which needs g_track) so
// mpvSeekExact can cheaply refuse to fight playback with scrub seeks.
static std::atomic<bool> g_edlActive{ false };
static std::atomic<double> g_mpvTimePos{ -1.0 };
static const int kObsTimePos = 77;   // observe_property id for time-pos
// D-4's 2x playback rate. Moved up here from the view/gesture block (it used to sit next
// to g_playingExt) purely so the EDL playback code below can pass it to mpv as "speed" -
// same variable, same meaning, just declared before its first use now.
static double g_playRate = 1.0;

// I-5/I-8 root cause (found live this session via BECKY_REVIEW_SCRUB_LOG): this
// used to be a plain synchronous WriteFile with no timeout. mpv's named pipe has
// a finite kernel buffer; if mpv falls behind draining it (a burst of seek
// commands, or the whole app doing 200+ requestCompose dispatches/sec on an
// uncapped render loop), the buffer fills and WriteFile blocks FOREVER once it
// does. decodeWorker is the ONLY thread that ever calls this, so one blocked
// write wedges it permanently: curSec/the UI keep working, but the video pane
// silently freezes on its last frame for the rest of the session - no crash, no
// error, reproduced live twice (once after ~2s of simulated playback, again
// after ~150 keyframe seeks even with the I-5 throttle in main() already in
// place). g_mpvPipe is opened FILE_FLAG_OVERLAPPED (mpvConnectOne) specifically
// so this can bound the wait: a write that doesn't land within 250ms is
// cancelled and dropped rather than blocking - safe because requestCompose
// always posts the LATEST wanted position (P1/P5 coalescing), so a dropped
// stale command is harmless; the next successful write catches up.
static bool mpvWriteLine(const std::string& line) {
    if (g_mpvPipe == INVALID_HANDLE_VALUE) return false;
    std::lock_guard<std::mutex> lk(g_mpvWriteMx);
    std::string s = line; s += "\n";
    OVERLAPPED ov{};
    ov.hEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
    if (!ov.hEvent) return false;
    DWORD written = 0;
    bool ok = WriteFile(g_mpvPipe, s.data(), (DWORD)s.size(), &written, &ov) != 0;
    if (!ok) {
        if (GetLastError() == ERROR_IO_PENDING) {
            DWORD w = WaitForSingleObject(ov.hEvent, 250);
            if (w == WAIT_OBJECT_0) {
                ok = GetOverlappedResult(g_mpvPipe, &ov, &written, FALSE) != 0;
            } else {
                CancelIoEx(g_mpvPipe, &ov);
                crashLog("mpv: IPC write timed out (250ms) - command dropped, not blocking decodeWorker forever");
            }
        } else {
            ok = false;
        }
    }
    CloseHandle(ov.hEvent);
    return ok;
}
static bool mpvCommand(const json& cmdArr) {
    json j; j["command"] = cmdArr;
    return mpvWriteLine(j.dump());
}
// D-9: mpv's own playback clock. time-pos arrives as an unsolicited "property-change"
// event on whichever connection issued the observe_property - that is g_mpvPipe
// (everything goes out through mpvWriteLine), so mpvWriteSideDrainThread is what
// actually feeds this; mpvReaderThread feeds it too, harmlessly, so neither drain
// thread has to care which one mpv picked.
static void mpvHandleIpcLine(const std::string& line) {
    if (line.find("time-pos") == std::string::npos) return;   // cheap reject - most lines are command acks
    try {
        json j = json::parse(line);
        if (j.value("event", std::string()) != "property-change") return;
        if (j.value("name", std::string()) != "time-pos") return;
        auto it = j.find("data");
        if (it != j.end() && it->is_number()) g_mpvTimePos.store(it->get<double>());
    } catch (...) {
        // Garbage/partial line - ignore. A drain thread must never throw: it is the
        // thread that detects mpv dying, and losing it silently freezes the pane.
    }
}
// Splits a raw pipe chunk into whole lines, carrying the partial tail to the next read
// (IPC replies are newline-delimited but arrive chunked, so a naive per-chunk parse
// would drop or corrupt roughly every event that straddles a boundary).
static void mpvFeedIpcChunk(std::string& acc, const char* buf, size_t n) {
    acc.append(buf, n);
    size_t pos;
    while ((pos = acc.find('\n')) != std::string::npos) {
        mpvHandleIpcLine(acc.substr(0, pos));
        acc.erase(0, pos + 1);
    }
    if (acc.size() > (1u << 20)) acc.clear();   // runaway guard - never grow unbounded
}
// Reader thread: drains mpv's IPC replies/events (we don't need to correlate
// request ids - every command here is fire-and-forget). Its real job is
// detecting mpv exiting/crashing so the video pane degrades visibly instead of
// silently freezing on the last frame it ever showed. Reads on g_mpvPipeRead, a
// DUPLICATE of the connect handle: a synchronous named-pipe HANDLE shared between
// a thread parked in a blocking ReadFile and another thread calling WriteFile
// deadlocks the writer on the handle's own I/O lock (observed live - the loadfile
// command never reached mpv until this split) - two HANDLE values on the same
// pipe instance, one per direction, is the fix (mirrors the engine seam's
// separate hin/hout pipes, just via DuplicateHandle instead of two CreatePipes).
static void mpvReaderThread() {
    t_threadTag = "mpvReader";
    char buf[8192];
    std::string acc;
    for (;;) {
        DWORD n = 0;
        if (!ReadFile(g_mpvPipeRead, buf, sizeof buf, &n, nullptr) || n == 0) break;
        mpvFeedIpcChunk(acc, buf, n);
    }
    crashLog("mpv: IPC pipe closed (mpv exited) - video decode disabled, window still open");
    g_mpvAvailable.store(false);
}
// I-5/I-8 root cause, part 2 (found live re-testing the WriteFile-timeout fix
// above: EVERY write still timed out at exactly the 250ms bound, not just under
// a torture-test burst - too consistent to be mpv genuinely slow). mpv's JSON IPC
// sends a reply for every command back on the SAME connection that sent it -
// g_mpvPipe (the write connection) and g_mpvPipeRead (the read connection,
// mpvReaderThread above) are two SEPARATE client connections (see
// mpvConnectThread's comment), so a reply to a "seek"/"loadfile" sent on
// g_mpvPipe comes back on g_mpvPipe, not g_mpvPipeRead - and nothing was ever
// draining it. Hundreds of unread replies fill that connection's own pipe
// buffer; once full, mpv's write-the-reply-back call blocks, which (mpv
// handles one client connection on one thread) stops it from ever reading our
// NEXT command - the real reason mpvWriteLine's WriteFile stopped landing.
// This mirrors mpvReaderThread exactly, just on the write connection, using an
// OVERLAPPED read since g_mpvPipe was opened FILE_FLAG_OVERLAPPED for the
// timeout fix (a plain synchronous ReadFile on an overlapped handle with no
// OVERLAPPED struct fails immediately, it does not block).
static void mpvWriteSideDrainThread() {
    t_threadTag = "mpvWriteDrain";
    char buf[8192];
    std::string acc;
    OVERLAPPED ov{}; ov.hEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
    if (!ov.hEvent) return;
    for (;;) {
        DWORD n = 0;
        BOOL ok = ReadFile(g_mpvPipe, buf, sizeof buf, &n, &ov);
        if (!ok) {
            if (GetLastError() != ERROR_IO_PENDING) break;
            if (!GetOverlappedResult(g_mpvPipe, &ov, &n, TRUE)) break;
        }
        if (n == 0) break;
        mpvFeedIpcChunk(acc, buf, n);   // D-9: this is the connection time-pos arrives on
        ResetEvent(ov.hEvent);
    }
    CloseHandle(ov.hEvent);
}
static HANDLE mpvConnectOne(bool overlapped) {
    HANDLE h = INVALID_HANDLE_VALUE;
    DWORD flags = overlapped ? FILE_FLAG_OVERLAPPED : 0;
    for (int attempt = 0; attempt < 50 && h == INVALID_HANDLE_VALUE; attempt++) {
        h = CreateFileA(g_mpvPipeName.c_str(), GENERIC_READ | GENERIC_WRITE, 0, nullptr, OPEN_EXISTING, flags, nullptr);
        if (h == INVALID_HANDLE_VALUE) Sleep(100);
    }
    return h;
}
// Connects to the IPC pipe mpv creates a moment after launch (retried off the UI
// thread so a slow/failed mpv startup can never block window-open or the render
// loop - the exact P1 lesson this file already learned from gst_init). Opens TWO
// independent client connections (mpv's named-pipe IPC server accepts multiple
// simultaneous clients, one pipe instance each) rather than sharing one HANDLE
// across the permanently-blocking reader and the writer: a DuplicateHandle of one
// synchronous connection was tried first and still deadlocked the writer, so the
// write side gets its own real connection instead - proven live via a manual
// second-connection test that loaded a file cleanly while the first connection
// was stuck. On success, becomes the reader thread.
static void mpvConnectThread() {
    t_threadTag = "mpvConnect";
    g_mpvPipeRead = mpvConnectOne(false);
    if (g_mpvPipeRead == INVALID_HANDLE_VALUE) {
        crashLog("mpv: IPC read-pipe connect failed after 5s - video decode disabled, window still open");
        return;
    }
    // I-5/I-8 fix: OVERLAPPED so mpvWriteLine can bound its wait instead of a
    // synchronous WriteFile blocking forever when mpv falls behind (see its comment).
    g_mpvPipe = mpvConnectOne(true);
    if (g_mpvPipe == INVALID_HANDLE_VALUE) {
        crashLog("mpv: IPC write-pipe connect failed after 5s - video decode disabled, window still open");
        CloseHandle(g_mpvPipeRead); g_mpvPipeRead = INVALID_HANDLE_VALUE;
        return;
    }
    g_mpvAvailable.store(true);
    crashLog("mpv: launched + IPC connected, video decode available");
    std::thread(mpvWriteSideDrainThread).detach();
    mpvReaderThread();
}
static bool mpvLaunch(HWND parent) {
    static const wchar_t* kClass = L"beckyMpvHost";
    WNDCLASSEXW wc{ sizeof wc };
    wc.lpfnWndProc = DefWindowProcW;
    wc.hInstance = GetModuleHandle(nullptr);
    wc.lpszClassName = kClass;
    wc.hbrBackground = (HBRUSH)GetStockObject(BLACK_BRUSH);
    RegisterClassExW(&wc);
    g_mpvHwnd = CreateWindowExW(0, kClass, L"", WS_CHILD, 0, 0, 16, 16, parent, nullptr, wc.hInstance, nullptr);
    if (!g_mpvHwnd) { crashLog("mpv: child hwnd create failed - video decode disabled, window still open"); return false; }

    std::string exe = "X:/AI-2/becky-tools/native/becky-review/runtime/mpv/mpv.exe";
    if (!std::ifstream(exe)) {
        crashLog("mpv: mpv.exe not found at " + exe + " (run fetch-mpv.ps1) - video decode disabled, window still open");
        return false;
    }
    char pipeName[64]; snprintf(pipeName, sizeof pipeName, "beckyreviewmpv%lu", (unsigned long)GetCurrentProcessId());
    g_mpvPipeName = std::string("\\\\.\\pipe\\") + pipeName;

    std::wstring wex(exe.begin(), exe.end());
    wchar_t wpipe[64]; MultiByteToWideChar(CP_UTF8, 0, pipeName, -1, wpipe, 64);
    std::wstring cmd = L"\"" + wex + L"\""
        L" --wid=" + std::to_wstring((unsigned long long)(uintptr_t)g_mpvHwnd) +
        L" --input-ipc-server=\\\\.\\pipe\\" + wpipe +
        L" --hr-seek=yes --hwdec=auto-safe --keep-open=yes --idle=yes"
        L" --force-window=yes --no-osc --osc=no --sub-auto=no --sid=no"
        L" --no-config --pause=yes --no-terminal --really-quiet"
        // mpv must ignore the mouse entirely: the app drives it over IPC only, and
        // the caption placement drag happens ON TOP of this window (see the video
        // pane block). Without this, mpv's own default bindings would react to the
        // very clicks that drag is made of.
        L" --input-cursor=no --input-vo-keyboard=no"
        L" --cache=yes --demuxer-readahead-secs=20";

    STARTUPINFOW si{ sizeof si }; si.dwFlags = STARTF_USESHOWWINDOW; si.wShowWindow = SW_HIDE;
    if (!CreateProcessW(nullptr, &cmd[0], nullptr, nullptr, FALSE, CREATE_NO_WINDOW, nullptr, nullptr, &si, &g_mpvProc)) {
        crashLog("mpv: CreateProcess failed - video decode disabled, window still open");
        DestroyWindow(g_mpvHwnd); g_mpvHwnd = nullptr;
        return false;
    }
    std::thread(mpvConnectThread).detach();
    return true;
}
// Non-blocking: called from the decode thread (same dispatch site the old
// GStreamer pull used). Atomic loadfile+start= on a source change (never
// load-then-seek - the exact race this file already root-caused once for
// search-hit clicks); a plain exact seek when only the position moved within
// the already-loaded source (the common case during scrub/playhead-tick).
// I-5 fix (root-caused live this session via BECKY_REVIEW_SCRUB_LOG): mpv's
// "exact" seek flag is a real decode-forward-from-keyframe operation, not a
// cheap pointer move - sending one every frame during a scrub-drag or the
// app's own simulated playback (curSec += dt each frame, see main()'s
// "playing" tick) floods mpv's IPC command queue faster than it can decode
// them. mpvWriteLine's WriteFile is a plain synchronous named-pipe write with
// no timeout, held under g_mpvWriteMx - once that pipe's kernel buffer fills
// because mpv is still busy on an earlier "exact" seek, WriteFile blocks
// FOREVER, wedging decodeWorker (and, via the shared mutex, any other mpv
// command) permanently: curSec/the UI keep working, but the video pane freezes
// on its last frame for the rest of the session, silently, with no crash and
// no log - live-reproduced by a 25-step scrub-drag after ~2s of playback
// (BECKY_REVIEW_FRAME_TRACE showed the UI thread never stalled; the DECODE
// side of BECKY_REVIEW_SCRUB_LOG simply stopped appearing forever). BUILD_1.md
// I-5 already specifies the fix: "keyframe seek while dragging, exact on
// release" - cheap keyframe seeks can't back up mpv's queue the way exact
// seeks can. main() now passes exact=false for every continuous churn
// (playing, or an active scrub-drag) and exact=true only once things settle
// (paused, single click-to-seek, frame-step, or the frame right after a drag
// releases/playback stops) - the same distinction generalized to both
// contract lines that flood curSec continuously, not just the literal drag.
static void mpvSeekExact(const std::string& source, double srcSec, bool exact) {
    if (!g_mpvAvailable.load()) return;
    // D-9: while mpv is PLAYING the reel EDL it owns the position - a scrub seek here
    // would yank playback backwards every frame and re-create exactly the IPC flood
    // I-5 root-caused. main() also gates compose dispatch on this; belt and braces.
    if (g_edlActive.load()) return;
    std::string src = source; fwslash(src);
    std::lock_guard<std::mutex> lk(g_mpvSrcMx);
    if (src != g_mpvLoadedSource) {
        // pause=yes is NOT redundant with the launch flag (D-9): once playback has
        // genuinely unpaused mpv for the reel EDL, a later scrub loadfile comes back
        // PLAYING unless it says otherwise - observed live (paused app, mpv happily
        // rolling on through the raw source with audio). Scrubbing is by definition a
        // paused, frame-exact operation, so pin it here rather than relying on state.
        char startOpt[64]; snprintf(startOpt, sizeof startOpt, "start=%.6f,pause=yes", srcSec);
        mpvCommand(json::array({ "loadfile", src, "replace", 0, std::string(startOpt) }));
        mpvCommand(json::array({ "set_property", "pause", true }));
        g_mpvLoadedSource = src;
    } else {
        mpvCommand(json::array({ "seek", srcSec, "absolute", exact ? "exact" : "keyframes" }));
    }
}

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
static std::map<std::string, std::shared_ptr<Peaks>> g_peaks;
static std::mutex g_peaksMx;
static std::mutex g_decMx; static std::condition_variable g_decCv; static int g_decActive = 0;
static std::atomic<bool> g_busyHint{ false };
// E-18/I-6 instrumentation: counts every job actually PUSHED onto a Peaks.jobs
// deque (peaksRequest below) - not decode work, the enqueue itself. BUILD_1.md's
// verification bar for I-6 is literally "split 20x rapidly, assert 0 jobs
// enqueued"; this is the counter that assertion reads (see peaksRequest's
// already-filled short-circuit, which is what keeps it at 0 once a source's
// audio is warm).
static std::atomic<uint64_t> g_peaksJobsEnqueued{ 0 };
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
    bool v2 = ok && memcmp(magic, "BPK2", 4) == 0;
    bool v1 = ok && memcmp(magic, "BPK1", 4) == 0;
    if (ok && (v1 || v2)) {
        std::lock_guard<std::mutex> lk(P.mx);
        sizeArrays(P, dur);
        if (v2) {
            uint32_t secN = 0;
            ok = fread(&secN, 4, 1, f) == 1 && secN <= P.secFilled.size();
            if (ok && secN) ok = fread(P.secFilled.data(), 1, secN, f) == secN;
        } else {
            std::fill(P.secFilled.begin(), P.secFilled.end(), 1);
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
    fwrite("BPK2", 1, 4, f);
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
static void decodeWindow(Peaks& P, GstElement* pipe, GstElement* sink, double a, double b) {
    if (!gst_element_seek(pipe, 1.0, GST_FORMAT_TIME,
        (GstSeekFlags)(GST_SEEK_FLAG_FLUSH | GST_SEEK_FLAG_ACCURATE),
        GST_SEEK_TYPE_SET, (gint64)(a * GST_SECOND),
        GST_SEEK_TYPE_SET, (gint64)(b * GST_SECOND))) return;
    for (;;) {
        GstSample* smp = gst_app_sink_try_pull_sample(GST_APP_SINK(sink), GST_SECOND);
        if (!smp) {
            if (gst_app_sink_is_eos(GST_APP_SINK(sink))) break;
            GstBus* bus = gst_element_get_bus(pipe);
            GstMessage* m = gst_bus_pop_filtered(bus, GST_MESSAGE_ERROR);
            gst_object_unref(bus);
            if (m) { gst_message_unref(m); break; }
            continue;
        }
        GstBuffer* buf = gst_sample_get_buffer(smp);
        GstClockTime pts = GST_BUFFER_PTS(buf);
        GstMapInfo mi;
        if (GST_CLOCK_TIME_IS_VALID(pts) && gst_buffer_map(buf, &mi, GST_MAP_READ)) {
            const int16_t* sm = (const int16_t*)mi.data;
            size_t ns = mi.size / 2;
            uint64_t samplePos = (uint64_t)((double)pts / GST_SECOND * kPeakRate);
            std::lock_guard<std::mutex> lk(P.mx);
            size_t firstBin = (size_t)(samplePos / kSpb), lastBin = firstBin;
            for (size_t i = 0; i < ns; i++) {
                size_t bin = (size_t)((samplePos + i) / kSpb);
                if (bin >= P.bins) break;
                int8_t q = (int8_t)(sm[i] >> 8);
                if (q < P.n0[bin]) P.n0[bin] = q;
                if (q > P.x0[bin]) P.x0[bin] = q;
                lastBin = bin;
            }
            pyramidRegion(P, firstBin, lastBin + 1);
            gst_buffer_unmap(buf, &mi);
        }
        gst_sample_unref(smp);
    }
    {
        std::lock_guard<std::mutex> lk(P.mx);
        for (size_t s = (size_t)std::ceil(a); s + 1 <= (size_t)std::floor(b) && s < P.secFilled.size(); s++)
            P.secFilled[s] = 1;
        P.dirty = true;
    }
    g_fillEpoch.fetch_add(1);
}
static void peaksWorker(std::shared_ptr<Peaks> P) {
    t_threadTag = "peaksWorker";
    // Spec 3.4 P3: CPU priority alone is not enough - a background thread doing disk
    // I/O can still stall the OS cursor. BACKGROUND_BEGIN also drops I/O + memory
    // priority (this is the documented fix for FB9's "even my mouse lags" bug).
    if (!SetThreadPriority(GetCurrentThread(), THREAD_MODE_BACKGROUND_BEGIN))
        SetThreadPriority(GetCurrentThread(), THREAD_PRIORITY_BELOW_NORMAL);
    if (loadPeaksCache(*P)) g_fillEpoch.fetch_add(1);
    // #0 CRITICAL: GStreamer never initialized - do not touch any gst_* call, it is unsafe.
    if (!g_gstAvailable.load()) { P->failed = true; return; }
    GError* uerr = nullptr;
    char* uri = gst_filename_to_uri(P->source.c_str(), &uerr);
    if (!uri) { if (uerr) g_error_free(uerr); P->failed = true; return; }
    char desc[2600];
    snprintf(desc, sizeof desc,
        "uridecodebin uri=\"%s\" caps=\"audio/x-raw\" expose-all-streams=false ! "
        "audioconvert ! audioresample ! audio/x-raw,format=S16LE,channels=1,rate=%d ! "
        "appsink name=as sync=false",
        uri, kPeakRate);
    g_free(uri);
    GError* e = nullptr;
    GstElement* pipe = gst_parse_launch(desc, &e);
    if (!pipe || e) { if (e) g_error_free(e); P->failed = true; return; }
    GstElement* sink = gst_bin_get_by_name(GST_BIN(pipe), "as");
    if (!P->ready) {
        gst_element_set_state(pipe, GST_STATE_PAUSED);
        if (gst_element_get_state(pipe, nullptr, nullptr, 20 * GST_SECOND) == GST_STATE_CHANGE_FAILURE) {
            P->failed = true;
            gst_element_set_state(pipe, GST_STATE_NULL);
            gst_object_unref(sink); gst_object_unref(pipe);
            return;
        }
        gint64 d = 0;
        if (gst_element_query_duration(pipe, GST_FORMAT_TIME, &d) && d > 0) {
            std::lock_guard<std::mutex> lk(P->mx);
            sizeArrays(*P, (double)d / GST_SECOND);
        } else {
            P->failed = true;
            gst_element_set_state(pipe, GST_STATE_NULL);
            gst_object_unref(sink); gst_object_unref(pipe);
            return;
        }
    }
    gst_element_set_state(pipe, GST_STATE_PLAYING);
    g_fillEpoch.fetch_add(1);
    std::unique_lock<std::mutex> lk(P->mx);
    for (;;) {
        if (P->jobs.empty()) {
            if (P->dirty) { savePeaksCache(*P); }
            P->cv.wait_for(lk, std::chrono::seconds(2));
            if (P->jobs.empty()) continue;
        }
        auto job = P->jobs.front(); P->jobs.pop_front();
        // I-6 dedup: mark this window in-flight WHILE still holding the lock, so
        // a peaksRequest arriving between the pop above and the re-lock below
        // (a split's reload can land at any point in that window - decodeWindow
        // itself can take real wall-clock time) sees it as "already being
        // handled" instead of finding it in neither `jobs` nor `secFilled` and
        // re-pushing a brand-new duplicate. Cleared right after this job's runs
        // are decoded, below.
        P->inFlight = job;
        double a = std::max(0.0, job.first), b = std::min(P->duration, job.second);
        std::vector<std::pair<double, double>> runs;
        double runA = -1;
        for (size_t s = (size_t)a; s <= (size_t)b && s < P->secFilled.size(); s++) {
            bool filled = P->secFilled[s] != 0;
            if (!filled && runA < 0) runA = std::max(a, (double)s);
            if ((filled || s == (size_t)b) && runA >= 0) { runs.push_back({ runA, std::min(b, (double)s + 1) }); runA = -1; }
        }
        if (runA >= 0) runs.push_back({ runA, b });
        lk.unlock();
        for (auto& r : runs) {
            if (r.second - r.first < 0.01) continue;
            {
                std::unique_lock<std::mutex> g(g_decMx);
                g_decCv.wait(g, [] { return g_decActive < (g_busyHint.load() ? 1 : 2); });
                g_decActive++;
            }
            try {
                decodeWindow(*P, pipe, sink, r.first, r.second);
            } catch (const std::exception& e) {
                crashLog(std::string("peaksWorker decodeWindow: caught ") + e.what() + " - window skipped, not crashing");
            } catch (...) {
                crashLog("peaksWorker decodeWindow: caught non-std exception - window skipped, not crashing");
            }
            {
                std::lock_guard<std::mutex> g(g_decMx);
                g_decActive--;
            }
            g_decCv.notify_one();
        }
        lk.lock();
        P->inFlight = { -1.0, -1.0 };
        // cycle 19 real-corpus finding: did this job's range actually gain any
        // decoded seconds? A window whose audio is unseekable/gapped (confirmed
        // live on a partially-downloaded livestream .mkv - see Peaks::stuckAttempts
        // above) reaches here every single time with the EXACT same range still
        // unfilled - decodeWindow hit no error to catch, it simply produced zero
        // samples. Left unchecked, drawWave's once-a-second "still missing" retry
        // (main.cpp drawWave) re-requests this same window forever - a real,
        // slow-but-truly-unbounded job-enqueue growth, distinct from (and not
        // fixed by) the split-time dedup above. Give up after kMaxStuckAttempts
        // instead of retrying forever - same "degrade, never hang" contract as
        // any other decode failure.
        {
            size_t s0 = (size_t)std::ceil(a), s1 = std::min(P->secFilled.size(), (size_t)std::floor(b));
            bool nowFilled = true;
            for (size_t s = s0; s < s1; s++) if (!P->secFilled[s]) { nowFilled = false; break; }
            if (nowFilled) P->stuckAttempts = 0;
            else if (++P->stuckAttempts >= kMaxStuckAttempts) {
                P->failed = true;
                lk.unlock();
                crashLog("peaksWorker: giving up on " + baseName(P->source) + " - window [" +
                    std::to_string(a) + "," + std::to_string(b) + "] never filled after " +
                    std::to_string(kMaxStuckAttempts) + " attempts (likely corrupt/gapped media) - "
                    "waveform disabled for this source, not retrying forever");
                gst_element_set_state(pipe, GST_STATE_NULL);
                gst_object_unref(sink); gst_object_unref(pipe);
                return;
            }
        }
    }
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
    std::thread(peaksWorker, P).detach();
    return P;
}
// True if every second in [a,b) is already decoded (P.secFilled) - a pure cache
// hit, nothing left for peaksWorker to do. Caller must hold P.mx.
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
    P->cv.notify_one();
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
static bool g_ovShowingInMpv = false; // whether mpv currently has an osd-overlay up

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
// Pushes (or clears) the preview overlay into mpv via its "osd-overlay" IPC
// command (ass-events format) - mpv owns the video's own compositor surface (its
// --wid child hwnd paints independently of our D3D11/ImGui surface), so this is
// the only way preview text can actually appear ON the frame rather than being
// drawn under it by ImGui. Fire-and-forget, same as every other mpvCommand call
// in this file: a failure (old mpv build, IPC hiccup) just leaves the preview
// showing no overlay, never a crash - render stays ground truth regardless.
static void mpvClearOverlay() {
    if (!g_ovShowingInMpv) return;
    mpvCommand(json::array({ "osd-overlay", 9001, "none", "", 0, 0, 0 }));
    g_ovShowingInMpv = false;
}
static void mpvUpdateOverlay(const Clip* cur) {
    static std::string s_lastAss;
    if (g_ovMode != 2 || !cur || !g_mpvAvailable.load()) { mpvClearOverlay(); return; }
    std::vector<std::string> lines = overlayLines(*cur);
    if (lines.empty()) { mpvClearOverlay(); return; }
    std::string body;
    for (size_t i = 0; i < lines.size(); i++) {
        if (i) body += "\\N";
        body += assEscape(lines[i]);
    }
    const char* anchor = (g_overlay.position == "top") ? "\\an7" : "\\an1";
    std::string ass = std::string("{") + anchor + "\\fs28\\b1\\bord2\\shad0\\1c&HFFFFFF&\\3c&H000000&}" + body;
    if (ass == s_lastAss && g_ovShowingInMpv) return; // unchanged text - skip the IPC round trip
    s_lastAss = ass;
    mpvCommand(json::array({ "osd-overlay", 9001, "ass-events", ass, 0, 0, 0 }));
    g_ovShowingInMpv = true;
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
    if (m != 2) mpvClearOverlay();
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
// can never back up behind stale decode work; only the body (an mpv IPC command instead
// of a GStreamer pull) changed for D-1.
static std::mutex g_decReqMx;
static std::condition_variable g_decReqCv;
static std::string g_decReqSource;
static double g_decReqSrcSec = 0, g_decReqCompT = -1;
static bool g_decReqExact = true;
static bool g_decReqPending = false;
static bool g_decQuit = false;

static void composeOnDecodeThread(const std::string& source, double srcSec, double compT, bool exact) {
    double t0 = nowSec();
    mpvSeekExact(source, srcSec, exact);
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
// array scan over g_track[0], no I/O) and hands it to the decode thread; never touches
// mpv's pipe directly from the UI thread. `exact` is false for continuous churn (playing,
// or an active scrub-drag) and true once it settles - see mpvSeekExact's comment for why
// this distinction is load-bearing, not cosmetic (I-5).
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

// --------------- D-9: REAL playback with AUDIO - the reel handed to mpv as an EDL ---------------
// Why an EDL and not 88 hand-driven seeks: mpv natively supports an EDL (a playlist of
// in/out segments across files) as a single virtual file. Writing the reel as one lets
// mpv play the WHOLE edit seamlessly, with audio and correct A/V sync, and makes EDL time
// identical to composition time - so curSec maps 1:1 onto mpv's time-pos with no mapping
// layer. Verified against runtime/mpv/mpv.exe (v0.41.0) on the 88-cut post_constantly reel
// before this was written: duration 150s (matches the summed clip lengths), audio track
// present, and the per-process Windows peak meter reads real signal.
//
// SCOPE, deliberately: the EDL is used for PLAYBACK ONLY. Paused scrubbing keeps the
// existing per-clip atomic loadfile+start= path untouched, because that is what makes
// frame-exact editing and cut-point snapping work today and it is not worth risking.
// Entering/leaving playback is the only thing that reloads.
static std::string g_edlPath;
static uint64_t g_edlSigLoaded = 0;
static double g_edlSeekTarget = -1;   // >=0 while a seek is issued but mpv hasn't reported it yet
static double g_edlSeekAt = 0;
static double g_edlSpeedSet = -1;

// FNV-1a over (source, in, out) of every clip: detects a mid-playback edit so the EDL can
// be rebuilt. A hash, not a string, because this runs every frame during playback.
static uint64_t edlTrackSig() {
    uint64_t h = 1469598103934665603ull;
    auto mix = [&h](const void* p, size_t n) {
        const unsigned char* b = (const unsigned char*)p;
        for (size_t i = 0; i < n; i++) { h ^= b[i]; h *= 1099511628211ull; }
    };
    for (auto& c : g_track[0]) { mix(c.source.data(), c.source.size()); mix(&c.in, sizeof c.in); mix(&c.out, sizeof c.out); }
    return h;
}

static bool edlWrite(std::string& outPath) {
    if (g_track[0].empty()) return false;
    if (g_edlPath.empty()) {
        char tmp[MAX_PATH]; DWORD n = GetTempPathA(MAX_PATH, tmp);
        std::string dir = (n > 0 && n < MAX_PATH) ? std::string(tmp, n) : std::string(".\\");
        g_edlPath = dir + "becky-review-" + std::to_string((unsigned long)GetCurrentProcessId()) + ".edl";
    }
    std::string body = "# mpv EDL v0\n";
    char b[64];
    for (auto& c : g_track[0]) {
        double len = c.out - c.in;
        if (len <= 0) continue;
        std::string src = c.source; fwslash(src);
        // %<byteLen>%<path> is mpv's EDL quoting - mandatory here because Windows paths
        // can contain the ',' that otherwise separates the fields.
        snprintf(b, sizeof b, "%%%d%%", (int)src.size());
        body += b; body += src;
        snprintf(b, sizeof b, ",%.6f,%.6f\n", c.in, len);
        body += b;
    }
    // BINARY on purpose. A text-mode ofstream writes CRLF on Windows, and mpv's EDL
    // header match then fails outright - "Failed to recognize file format", reproduced
    // directly against runtime/mpv/mpv.exe before this was written. Do not "clean this up".
    std::ofstream f(g_edlPath, std::ios::binary | std::ios::trunc);
    if (!f) return false;
    f.write(body.data(), (std::streamsize)body.size());
    f.close();
    outPath = g_edlPath;
    return true;
}

// Hands mpv the current reel and starts it PLAYING at compT. Also used to re-enter after a
// mid-playback edit (rebuild + resume at the same spot). Degrades silently if mpv is down
// or the EDL can't be written - main() then falls back to the old simulated tick.
static void mpvEdlEnter(double compT) {
    if (!g_mpvAvailable.load() || g_track[0].empty()) return;
    std::string path;
    if (!edlWrite(path)) { crashLog("mpv: EDL write failed - falling back to simulated playback (no audio)"); return; }
    fwslash(path);
    static bool s_observed = false;
    if (!s_observed) { mpvCommand(json::array({ "observe_property", kObsTimePos, "time-pos" })); s_observed = true; }
    if (compT < 0) compT = 0;
    char opt[160];
    snprintf(opt, sizeof opt, "start=%.6f,pause=no,speed=%.4f", compT, g_playRate);
    {
        std::lock_guard<std::mutex> lk(g_mpvSrcMx);
        // Leave the EDL path here so that on exit the next scrub compose sees a DIFFERENT
        // source and does its normal atomic loadfile+start= back onto the real clip - the
        // frame-exact paused path needs no special-casing at all.
        g_mpvLoadedSource = path;
    }
    mpvCommand(json::array({ "loadfile", path, "replace", 0, std::string(opt) }));
    mpvCommand(json::array({ "set_property", "pause", false }));   // belt and braces vs. the per-file option
    g_edlSigLoaded = edlTrackSig();
    g_edlSpeedSet = g_playRate;
    g_mpvTimePos.store(-1.0);
    g_edlSeekTarget = compT; g_edlSeekAt = nowSec();
    g_edlActive.store(true);
}

static void mpvEdlExit() {
    if (!g_edlActive.load()) return;
    g_edlActive.store(false);
    mpvCommand(json::array({ "set_property", "pause", true }));
    g_mpvTimePos.store(-1.0);
    g_edlSigLoaded = 0;
    g_edlSeekTarget = -1;
}

static void mpvEdlSeek(double compT) {
    if (!g_edlActive.load()) return;
    mpvCommand(json::array({ "seek", compT, "absolute", "exact" }));
    g_edlSeekTarget = compT; g_edlSeekAt = nowSec();
    g_mpvTimePos.store(-1.0);
}

// --------------- NDJSON out to the engine is over the subprocess; UI sends via engineCall ---------------
// --------------- view + gesture state ---------------
static HWND g_hwnd = nullptr;
static double g_pps = 60;
static double g_scrollSec = 0;
static bool g_visible = true;
static bool g_playingExt = false;
// (g_playRate - D-4's 2x playback - now lives up with the mpv globals; see D-9.)
static double g_stockSec = -1;
static bool g_stockFlash = false;
static double g_lastUserScroll = 0;
static std::set<std::string> g_sel;
static std::string g_selAnchor;
static bool g_thrOn = false;
static double g_thrLevel = 0.14;
static bool g_quietDirty = true;
static int g_quietEpochSeen = -1;
static std::vector<std::pair<double, double>> g_quietRanges;
static double g_lastQuietEmit = 0, g_lastThrEmit = 0;

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

static void emitScrub(double t, bool final_) {
    double n = nowSec();
    if (!final_ && n - g_lastScrubEmit < 0.016) return;
    g_lastScrubEmit = n;
    // Route through the engine so the playhead is the single source of truth.
    json r = engineCall("seek", { {"t", t}, {"quiet", final_ ? false : true} }, 2.0);
    (void)r;
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
    std::thread([ids] { json r = engineCall("set_select", { {"ids", ids} }, 2.0); (void)r; }).detach();
}
static void emitThreshold(bool final_) {
    double n = nowSec();
    if (!final_ && n - g_lastThrEmit < 0.1) return;
    g_lastThrEmit = n;
    json r = engineCall("set_threshold", { {"on", g_thrOn}, {"level", g_thrLevel} }, 2.0);
    (void)r;
}
static void recomputeQuiet() {
    g_quietRanges.clear();
    if (!g_thrOn) return;
    std::vector<std::pair<double, double>> raw;
    for (auto& c : g_track[0]) {
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
    std::sort(raw.begin(), raw.end());
    for (auto& r : raw) {
        if (!g_quietRanges.empty() && r.first <= g_quietRanges.back().second + 0.06)
            g_quietRanges.back().second = std::max(g_quietRanges.back().second, r.second);
        else g_quietRanges.push_back(r);
    }
    g_quietRanges.erase(std::remove_if(g_quietRanges.begin(), g_quietRanges.end(),
        [](const std::pair<double, double>& r) { return r.second - r.first < 0.35; }), g_quietRanges.end());
}

// Load a TimelineView (from engine "timeline" verb) into the native track.
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
    g_sel.clear();
    // Windowed waveform decode: only what's on the timeline, newest first. (FB9 fix: keyed by SOURCE.)
    for (auto& c : g_track[0]) peaksRequest(c.source, c.in - 1.0, c.out + 5.0);
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
}

// --------------- D3D11 display ---------------
// (the video FRAME itself is no longer a D3D11 texture ImGui draws - mpv paints
// directly into its own --wid child hwnd, see MpvEmbed above; this swapchain is
// only the ImGui/UI surface now.)
static ID3D11Device* g_dev = nullptr; static ID3D11DeviceContext* g_ctx = nullptr; static IDXGISwapChain* g_swap = nullptr;
static ID3D11RenderTargetView* g_rtv = nullptr;
static int g_W = 1280, g_H = 800;
static bool g_resize = false;

static void createRTV() {
    ID3D11Texture2D* bb = nullptr;
    g_swap->GetBuffer(0, __uuidof(ID3D11Texture2D), (void**)&bb);
    if (bb) { g_dev->CreateRenderTargetView(bb, nullptr, &g_rtv); bb->Release(); }
}
static bool CreateD3D(HWND h) {
    DXGI_SWAP_CHAIN_DESC sd = {};
    sd.BufferCount = 2; sd.BufferDesc.Width = g_W; sd.BufferDesc.Height = g_H;
    sd.BufferDesc.Format = DXGI_FORMAT_R8G8B8A8_UNORM;
    sd.BufferUsage = DXGI_USAGE_RENDER_TARGET_OUTPUT; sd.OutputWindow = h;
    sd.SampleDesc.Count = 1; sd.Windowed = TRUE;
    sd.SwapEffect = DXGI_SWAP_EFFECT_FLIP_DISCARD;
    if (FAILED(D3D11CreateDeviceAndSwapChain(nullptr, D3D_DRIVER_TYPE_HARDWARE, nullptr, 0, nullptr, 0,
        D3D11_SDK_VERSION, &sd, &g_swap, &g_dev, nullptr, &g_ctx))) return false;
    createRTV();
    return true;
}
static void resizeD3D() {
    if (!g_swap || !g_ctx) return;
    if (g_rtv) { g_rtv->Release(); g_rtv = nullptr; }
    g_swap->ResizeBuffers(0, g_W, g_H, DXGI_FORMAT_UNKNOWN, 0);
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
static std::string thumbKey(const std::string& source, double t) {
    char buf[40]; snprintf(buf, sizeof buf, "@%.1f", t);
    return source + buf;
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
static void requestThumb(const std::string& source, double t) {
    std::string key = thumbKey(source, t);
    {
        std::lock_guard<std::mutex> lk(g_thumbMx);
        if (g_thumbInFlight.count(key)) return;
        g_thumbInFlight.insert(key);
    }
    std::thread([source, t, key] {
        t_threadTag = "thumbWorker";
        ThumbDone d; d.key = key;
        try {
            json r = engineCall("thumb", { {"source", source}, {"t", t} }, 15.0);
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
    }).detach();
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
static ThumbTex* getThumb(const std::string& source, double t) {
    std::string key = thumbKey(source, t);
    auto it = g_thumbCache.find(key);
    if (it != g_thumbCache.end()) return &it->second;
    requestThumb(source, t);
    return nullptr;
}

// E-13: WM_DROPFILES only captures client-space drop data here - the real work
// (path filtering, screen->timeline-seconds conversion via g_scrollSec/g_pps,
// and the add_external engine call) happens once per frame in drawTimeline,
// same "WndProc stays a thin OS-message forwarder" pattern as g_resize/g_W/g_H.
struct PendingDrop { std::vector<std::string> paths; int clientX = 0, clientY = 0; };
static std::vector<PendingDrop> g_pendingDrops;
static void requestAddExternal(const std::string& path, int at); // defined below, near requestTranscribe

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
static const ImU32 COL_RULERTX  = IM_COL32(160, 166, 178, 255);
static const ImU32 COL_TICK     = IM_COL32(80, 86, 98, 255);
static const ImU32 COL_TICKMIN  = IM_COL32(52, 57, 66, 255);
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
static const ImU32 COL_QUIETDIM = IM_COL32(0, 0, 0, 110);

static void fmtTime(double s, char* out, size_t n, bool subSec) {
    if (s < 0) s = 0;
    int t = (int)s, h = t / 3600, m = (t % 3600) / 60, sec = t % 60;
    if (subSec) { int d = (int)((s - t) * 10); if (h) snprintf(out, n, "%d:%02d:%02d.%d", h, m, sec, d); else snprintf(out, n, "%d:%02d.%d", m, sec, d); }
    else if (h) snprintf(out, n, "%d:%02d:%02d", h, m, sec);
    else snprintf(out, n, "%d:%02d", m, sec);
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

// loadCaptions points the lane at "<reel stem>.srt". A missing file is NOT an error
// (the reel simply has not been captioned yet) - the lane still appears and says so,
// which is how Jordan finds out he needs to run becky-subtitle.
static void loadCapStyle();   // defined just below - needs g_capPath, which this sets
static void loadCaptions(const std::string& reelPath) {
    g_caps.clear(); g_capErr.clear(); g_capPath.clear();
    g_capSel = -1; g_capEdit = -1; g_capEditFocus = false;
    if (reelPath.empty()) return;
    std::string p = reelPath; fwslash(p);
    size_t dot = p.find_last_of('.'), slash = p.find_last_of('/');
    if (dot != std::string::npos && (slash == std::string::npos || dot > slash)) p = p.substr(0, dot);
    p += ".srt";
    g_capPath = p;
    loadCapStyle();                        // this reel's saved vertical placement
    std::ifstream f(p);
    if (!f.good()) { g_capErr = "no captions yet - run becky-subtitle on this reel"; return; }
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
    if (g_capPath.empty()) return;
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
}

// The caption under the playhead, drawn ON the video at the placement the burn-in
// will use - so the thing Jordan drags is the thing he gets. mpv owns the video
// surface (its --wid child hwnd paints independently of our D3D11/ImGui surface),
// so an ImGui overlay physically cannot appear over the frame; osd-overlay is the
// only route, exactly as the provenance overlay already does. That one is id 9001,
// this is 9002, so the two never clobber each other.
//
// The ASS canvas is declared 384x288 because that is the PlayRes ffmpeg's SRT-to-ASS
// conversion uses (ff_ass_subtitle_header_default) - which makes MarginV, FontSize
// and Outline mean the SAME thing here as in becky-subtitle's force_style, rather
// than an eyeballed lookalike. mpv fits that canvas to the pane, so for footage that
// fills the pane vertically (portrait clips in this wide pane - the normal case) the
// preview height is exact. Letterboxed footage (source WIDER than the pane) would sit
// slightly low, since the canvas then spans the black bars too.
static bool g_capOsdShowing = false;
static void mpvClearCaptionOsd() {
    if (!g_capOsdShowing) return;
    mpvCommand(json::array({ "osd-overlay", 9002, "none", "", 0, 0, 0 }));
    g_capOsdShowing = false;
}
static void mpvUpdateCaptionOsd(double t) {
    static std::string s_lastAss;
    if (g_capPath.empty() || g_caps.empty() || !g_mpvAvailable.load()) { mpvClearCaptionOsd(); return; }
    const Caption* cur = nullptr;
    for (auto& c : g_caps) if (t >= c.start && t < c.end) { cur = &c; break; }
    // Mid-drag there must always be a caption on screen to judge the placement by,
    // even when the playhead has landed in a gap between two cues.
    if (!cur && g_capMarginDrag) {
        double best = 1e18;
        for (auto& c : g_caps) {
            double d = t < c.start ? c.start - t : (t > c.end ? t - c.end : 0);
            if (d < best) { best = d; cur = &c; }
        }
    }
    if (!cur) { mpvClearCaptionOsd(); s_lastAss.clear(); return; }
    std::string body;
    { // keep a wrapped cue's own line break; \N is the ASS hard break
        std::string line;
        for (char ch : cur->text) {
            if (ch == '\n') { body += assEscape(line) + "\\N"; line.clear(); }
            else if (ch != '\r') line += ch;
        }
        body += assEscape(line);
    }
    char hdr[128];
    snprintf(hdr, sizeof hdr, "{\\an2\\pos(%d,%d)\\fs12\\bord1\\shad0\\1c&HFFFFFF&\\3c&H000000&}",
             CAP_ASS_W / 2, CAP_ASS_H - g_capMarginV);
    std::string ass = std::string(hdr) + body;
    if (ass == s_lastAss && g_capOsdShowing) return;   // unchanged - skip the IPC round trip
    s_lastAss = ass;
    mpvCommand(json::array({ "osd-overlay", 9002, "ass-events", ass, CAP_ASS_W, CAP_ASS_H, 0 }));
    g_capOsdShowing = true;
}

// Forward decls (defined later, with the library/panel state they need) so the
// timeline's right-click clip menu (E-14) can reach them.
static void openInFileBrowser(const std::string& path);
static void openTranscript(const std::string& fullVideoPath);

static void drawTimeline(double& curSec, bool& playing) {
    ImDrawList* dl = ImGui::GetWindowDrawList();
    ImVec2 p = ImGui::GetCursorScreenPos();
    float availW = ImGui::GetContentRegionAvail().x;
    float availH = ImGui::GetContentRegionAvail().y;
    if (availW < 16 || availH < 44) return;
    float tlX = p.x, tlW = availW;
    float rulerH = 22, sbH = 12, gap = 4;
    int lanes = 1;
    float lanesH = availH - rulerH - sbH - gap * 2;
    // The caption lane sits directly UNDER the clip lane and inside the same
    // InvisibleButton below, so one gesture handler drives both. With no reel
    // loaded (g_capPath empty) capH/capGap are 0 and the layout is byte-identical
    // to the pre-caption one.
    bool showCaps = !g_capPath.empty() && lanesH > 90;
    float capH = showCaps ? 36.0f : 0.0f;
    float capGap = showCaps ? 4.0f : 0.0f;
    float laneH = lanesH - capH - capGap;
    if (laneH < 24) laneH = 24;
    float aY = p.y + rulerH + gap;
    float capY = aY + laneH + capGap;
    float bot = capY + capH;
    float sbY = bot + gap;

    dl->AddRectFilled(p, ImVec2(p.x + tlW, sbY + sbH), COL_BG);

    ImGui::SetCursorScreenPos(p);
    ImGui::InvisibleButton("tl", ImVec2(tlW, bot - p.y));
    bool hovered = ImGui::IsItemHovered();
    bool pressed = ImGui::IsItemActivated();
    bool active = ImGui::IsItemActive();
    bool released = ImGui::IsItemDeactivated();
    ImGuiIO& io = ImGui::GetIO();
    float mx = io.MousePos.x, my = io.MousePos.y;

    auto xToSec = [&](float x) { return std::max(0.0, g_scrollSec + (x - tlX) / g_pps); };
    auto secToX = [&](double s) { return tlX + (float)((s - g_scrollSec) * g_pps); };

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
    g_scrollSec = std::min(g_scrollSec, maxScroll);

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
            float hw = std::min(10.0f, (x1 - x0) / 4);
            if ((x1 - x0) > 20 && x - x0 <= hw) zone = 4;
            else if ((x1 - x0) > 20 && x1 - x <= hw) zone = 5;
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
        }
        ImGui::EndPopup();
    }

    if (pressed) {
        int idx, zone;
        g_gest = Gesture{};
        g_gest.pressX = mx; g_gest.ctrl = io.KeyCtrl; g_gest.shiftK = io.KeyShift;
        if (onThresholdBar(mx, my)) {
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
            g_gest.gIn = curSec;
            emitScrub(curSec, false);
        }
    }

    if (active && g_gest.kind != 0) {
        if (g_gest.kind == 1) {
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            if (std::abs(curSec - g_gest.gIn) > 1e-9) { g_gest.gIn = curSec; emitScrub(curSec, false); }
        } else if (g_gest.kind == 7) {
            float y = std::max(thrLaneTop, std::min(thrLaneBot, my));
            double frac = (thrLaneBot - y) / std::max(1.0f, thrLaneBot - thrLaneTop);
            g_thrLevel = frac <= 0.002 ? 0.0 : std::pow(10.0, (kThrFloorDb + frac * -kThrFloorDb) / 20.0);
            g_quietDirty = true;
            emitThreshold(false);
        } else if (g_gest.kind == 2 && std::abs(mx - g_gest.pressX) > 4) {
            g_gest.kind = 3; g_gest.dragged = true;
            if (g_gest.group.empty()) g_gest.group.push_back(g_gest.idx);
        } else if (g_gest.kind == 4 && g_gest.idx >= 0 && g_gest.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g_gest.idx];
            double edgeComp = snapComp(xToSec(mx), g_pps, curSec, g_gest.idx);
            double nIn = c.in + (edgeComp - c.compStart);
            nIn = std::max(0.0, std::min(nIn, c.out - 0.05));
            g_gest.gIn = nIn; g_gest.gOut = c.out;
        } else if (g_gest.kind == 5 && g_gest.idx >= 0 && g_gest.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g_gest.idx];
            double edgeComp = snapComp(xToSec(mx), g_pps, curSec, g_gest.idx);
            double nOut = c.in + (edgeComp - c.compStart);
            auto pk = peaksGet(c.source);
            double srcDur = (pk && pk->ready) ? pk->duration : 0;
            if (srcDur > 0.1) nOut = std::min(nOut, srcDur);
            nOut = std::max(nOut, c.in + 0.05);
            g_gest.gIn = c.in; g_gest.gOut = nOut;
        } else if (g_gest.kind == 8 && std::abs(mx - g_gest.pressX) > 4) {
            g_gest.kind = 11; g_gest.dragged = true;   // body press became a MOVE
        } else if (g_gest.kind == 11) {
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
        } else if (g_gest.kind == 9) {
            double t = quantToFrame(capSnapCut(xToSec(mx)));
            double lim = quantToFrame(g_gest.gOut - 1.0 / reelFps());   // never shorter than one frame
            g_gest.gIn = std::max(0.0, std::min(t, lim));
        } else if (g_gest.kind == 10) {
            double t = quantToFrame(capSnapCut(xToSec(mx)));
            double lim = quantToFrame(g_gest.gIn + 1.0 / reelFps());
            g_gest.gOut = std::max(t, lim);
        }
    }

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
                double t = std::max(0.0, std::min(xToSec(mx), g_compDur));
                if (!g_playingExt) { curSec = t; emitScrub(curSec, true); }
                else { g_stockSec = t; g_stockFlash = true; }
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
                if (g.group.size() > 1) {
                    json ids = json::array();
                    for (auto& c : moved) ids.push_back(c.id);
                    json r = engineCall("reorder_many", { {"ids", ids}, {"to", to} }, 4.0); (void)r;
                } else {
                    json r = engineCall("reorder", { {"id", moved[0].id}, {"to", to} }, 4.0); (void)r;
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
                json r = engineCall("set_trim", { {"id", c.id}, {"in", c.in}, {"out", c.out} }, 4.0); (void)r;
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
        } else if ((g.kind == 9 || g.kind == 10 || g.kind == 11) && g.idx >= 0 && g.idx < (int)g_caps.size()) {
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
    for (double s = t0; s <= g_scrollSec + viewDur + step; s += step) {
        float x = secToX(s);
        if (x < tlX - 60 || x > tlX + tlW + 60) continue;
        dl->AddLine(ImVec2(x, p.y + 6), ImVec2(x, p.y + rulerH), COL_TICK);
        char b[24]; fmtTime(s, b, sizeof b, step < 1.0);
        dl->AddText(ImVec2(x + 3, p.y + 3), COL_RULERTX, b);
        for (int m = 1; m < 5; m++) {
            float xm = secToX(s + step * m / 5.0);
            dl->AddLine(ImVec2(xm, p.y + rulerH - 5), ImVec2(xm, p.y + rulerH), COL_TICKMIN);
        }
    }

    dl->AddRectFilled(ImVec2(tlX, aY), ImVec2(tlX + tlW, aY + laneH), COL_LANE, 3);

    if (g_track[0].empty()) {
        const char* msg = "timeline empty - load a reel from the engine";
        ImVec2 ts = ImGui::CalcTextSize(msg);
        dl->AddText(ImVec2(tlX + (tlW - ts.x) / 2, aY + (laneH - ts.y) / 2), IM_COL32(120, 128, 140, 255), msg);
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
        ImU32 fill = IM_COL32(c.r, c.g, c.b, selected ? 232 : 62);
        if (inDrag) fill = (fill & 0x00FFFFFF) | 0x60000000;
        dl->AddRectFilled(ImVec2(x0 + 1, aY + 1), ImVec2(x1 - 1, aY + laneH - 1), fill, 3);
        float vx0 = std::max(x0 + 1, tlX), vx1 = std::min(x1 - 1, tlX + tlW);
        if (vx1 > vx0 && wy1 - wy0 > 6)
            drawWave(dl, c.source, cin, cout, x0, vx0, vx1, wy0, wy1, g_pps,
                     inDrag ? COL_WAVEDIM : (selected ? IM_COL32(255, 255, 255, 190) : COL_WAVE));
        ImU32 brd = selected ? IM_COL32(255, 255, 255, 255) : IM_COL32(c.r, c.g, c.b, 242);
        dl->AddRect(ImVec2(x0 + 1, aY + 1), ImVec2(x1 - 1, aY + laneH - 1), brd, 3, 0, selected ? 2.0f : 1.0f);
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
            ThumbTex* tt = getThumb(c.source, cin);
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
            bool ghost = (g_gest.kind == 9 || g_gest.kind == 10 || g_gest.kind == 11) && (int)i == g_gest.idx;
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

    if (g_stockSec >= 0) {
        float sx = secToX(g_stockSec);
        if (sx >= tlX - 2 && sx <= tlX + tlW + 2) {
            bool wht = g_stockFlash && std::fmod(nowSec(), 0.8) >= 0.4;
            dl->AddLine(ImVec2(sx, p.y + 4), ImVec2(sx, bot), wht ? IM_COL32(255, 255, 255, 255) : COL_PLAYHEAD, 2.0f);
        }
    }

    float px = secToX(curSec);
    if (px >= tlX - 2 && px <= tlX + tlW + 2) {
        dl->AddLine(ImVec2(px, p.y + 2), ImVec2(px, bot), COL_PLAYHEAD, 2.0f);
        float fw = 8, ftop = p.y + 1, fmid = p.y + 13, ftip = p.y + 20;
        dl->AddRectFilled(ImVec2(px - fw, ftop), ImVec2(px + fw, fmid), COL_PHFLAG);
        dl->AddTriangleFilled(ImVec2(px - fw, fmid), ImVec2(px + fw, fmid), ImVec2(px, ftip), COL_PHFLAG);
        dl->AddRect(ImVec2(px - fw, ftop), ImVec2(px + fw, fmid), IM_COL32(0, 0, 0, 115));
        dl->AddLine(ImVec2(px - 2.5f, ftop + 2), ImVec2(px - 2.5f, fmid - 2), COL_PHGRIP, 2.0f);
        dl->AddLine(ImVec2(px + 2.5f, ftop + 2), ImVec2(px + 2.5f, fmid - 2), COL_PHGRIP, 2.0f);
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
        // ImGui restores the pre-edit text into the buffer itself when Escape is
        // pressed, so committing on deactivation covers Enter, Escape AND click-away
        // through one path - Escape just commits the unchanged original, i.e. cancels.
        if (enter || ImGui::IsItemDeactivated()) {
            std::string nt = g_capEditBuf;
            if (nt != g_caps[g_capEdit].text) { g_caps[g_capEdit].text = nt; saveCaptions(); }
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
struct VideoRow { std::string path, name, date; bool hasTranscript = false; };
static std::vector<VideoRow> g_videos;
static std::string g_folderRoot;
static int g_orphanCount = 0;
static std::string g_folderErr;

// Sort mode for the library list (B-3): 0=date-newest,1=date-oldest,2=name-AZ,3=name-ZA
static int g_sortMode = 0;
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
    std::thread([path, baseName] {
        t_threadTag = "transcribeWorker";
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
    std::thread([path, at] {
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
    }).detach();
}

// ---- search state ----
static char g_searchBuf[256] = { 0 };
struct Hit {
    std::string source, name, date, text, timecode;
    double start = 0, end = 0, score = 0;
    bool transcriptOnly = false;
};
static std::vector<Hit> g_hits;
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
static char g_withinBuf[128] = { 0 };     // search-within-this-transcript

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

// ---- render/export toolbar requests (done on engine, shown in-window) ----
static std::string g_renderMsg;           // last render outcome (plain language)
static double g_renderMsgAt = 0;

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
// Native Win32 "Open" dialog filtered to reel .json files first (F-2 Load dialog).
static std::string pickOpenReelFile(HWND owner) {
    wchar_t file[MAX_PATH] = L"";
    OPENFILENAMEW ofn = {};
    ofn.lStructSize = sizeof ofn;
    ofn.hwndOwner = owner;
    ofn.lpstrFilter = L"Reel files (*.json)\0*.json\0All files\0*.*\0";
    ofn.lpstrFile = file;
    ofn.nMaxFile = MAX_PATH;
    ofn.Flags = OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST;
    ofn.lpstrTitle = L"Load Reel";
    if (!GetOpenFileNameW(&ofn)) return {};
    std::string s = wideToUtf8(file);
    fwslash(s);
    return s;
}

// seekToSpan puts ONE clip [a,b) of source on the (local) track and repositions
// the playhead to it, atomically (no load-then-seek race). D-3: a transcript/
// library click navigates PAUSED; a search-hit click / Play / Space starts
// playback (startPlaying=true) — shared by C-4 (search hit) and B-8 (cue click).
static void seekToSpan(const std::string& source, double a, double b, bool startPlaying,
                        double& curSec, bool& playing, double& lastComposed) {
    Clip cl; cl.in = a; cl.out = (b > a + 0.05) ? b : a + 0.05;
    cl.source = source; cl.label = baseName(source); cl.r = 220; cl.g = 30; cl.b = 60;
    g_track[0].clear(); g_track[0].push_back(cl);
    packTrack(0); recomputeDur();
    curSec = 0; playing = startPlaying; g_playingExt = playing; lastComposed = -1;
    g_quietDirty = true; peaksRequest(source, a - 1.0, b + 5.0);
}
// playWholeVideo puts a video's WHOLE span on the track (B-5 "spacebar plays the
// selected row"). Duration comes from the engine probe; an unprobe-able source
// degrades to a generous cap rather than blocking playback.
static void playWholeVideo(const std::string& path, double& curSec, bool& playing, double& lastComposed) {
    json pr = engineCall("probe", { {"source", path} }, 8.0);
    double dur = 0;
    if (pr.value("ok", false)) { const json& d = pr.contains("data") ? pr["data"] : pr; dur = d.value("duration", 0.0); }
    if (dur <= 0) dur = 3600;
    seekToSpan(path, 0.0, dur, true, curSec, playing, lastComposed);
}

// openTranscript opens a video's transcript (B-8) and remembers which row was viewed.
static void openTranscript(const std::string& fullVideoPath) {
    std::string name = baseName(fullVideoPath);
    if (g_cueName == name) return;       // already open
    g_cueErr.clear();
    json r = engineCall("transcript", { {"name", name} }, 25.0);
    if (!r.value("ok", false)) { g_cueErr = r.value("error", std::string("transcript unavailable")); g_cues.clear(); g_cueName = name; return; }
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
    g_cueName = name;
}

// render Q&A cards from the engine `questions` verb (G-1).
static void refreshCards() {
    g_cardsErr.clear();
    json r = engineCall("questions", {}, 8.0);
    if (!r.value("ok", false)) { g_cardsErr = r.value("error", std::string("questions unavailable")); g_cards.clear(); return; }
    const json& d = r.contains("data") ? r["data"] : r;
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
    {
        std::lock_guard<std::mutex> lk(g_searchQMx);
        g_searchQ.clear();
        g_searchQ.push_back({ q, qmd, nowSec() });
    }
    g_searchQCv.notify_one();
}

// add a search-hit's span as a clip to the timeline (C-4 double-click). The
// engine is authoritative on success ("clips" = a TimelineView, same shape as
// the "timeline" verb); a degraded/failed engine call still responds locally
// so the UI never silently no-ops.
static void addHitToTimeline(const Hit& h) {
    double a = h.start, b = (h.end > a + 0.05) ? h.end : a + 0.05;
    std::string label = baseName(h.source);
    // I-2 measurement: wall-clock the add_clip round trip (always-on, crash.log -
    // one line per add, negligible cost) so "<200ms, proxy building never gates
    // the add" is a grepped number, not a claim - same pattern as I-4's search
    // timing (see searchWorker). AddClipAt (becky-go/cmd/clip/app.go) is a pure
    // in-memory reel-list edit - no proxy/peaks work runs synchronously inside it.
    double t0 = nowSec();
    json r = engineCall("add_clip", { {"source", h.source}, {"in", a}, {"out", b}, {"label", label} }, 6.0);
    crashLog("I-2 add_clip source=" + label + " elapsedMs=" + std::to_string((nowSec() - t0) * 1000.0));
    if (r.value("ok", false) && r.contains("data") && r["data"].contains("clips")) {
        loadTimelineView(r["data"]);
        return;
    }
    Clip cl; cl.in = a; cl.out = b; cl.source = h.source; cl.label = label;
    cl.r = 220; cl.g = 30; cl.b = 60;
    g_track[0].push_back(cl); packTrack(0); recomputeDur();
    g_quietDirty = true; peaksRequest(h.source, a - 1.0, b + 5.0);
}

// --------------- main ---------------
int main(int argc, char** argv) {
    crashLogInit();
    crashLog("=== becky-review starting ===");
    editLogInit();
    frameTraceInit();
    scrubLogInit();
    // #0 CRITICAL: SEH-guarded - a gst_init crash must never take the window down with it.
    // GStreamer is only used by peaksWorker now (one-time per-source audio decode into
    // the .bpk peak cache, E-2) - the video player is mpv (D-1, launched after the
    // window exists, below).
    g_gstAvailable.store(gstInitSEH(argc, argv) != 0);
    if (g_gstAvailable.load()) crashLog("gst_init: OK, waveform decode available");
    else crashLog("gst_init: FAILED or crashed (caught) - waveform decode disabled, window still opening");
    std::thread(decodeWorker).detach();   // P1 fix: owns the mpv IPC dispatch, off the UI thread
    std::thread(editWorker).detach();     // A-4 fix: owns split/delete/trim/undo engine round-trips, off the UI thread
    std::thread(searchWorker).detach();   // I-* fix: owns search/qmd_search engine round-trips, off the UI thread
    g_engine.lastError.clear();
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
    }

    double curSec = 0;
    if (g_track[0].empty()) {
        // demo reel so the window is never blank (proves the pipeline without a folder).
        // Was pointed at "timeline-bench" (multi-GB source footage, never named proxyA.mp4
        // there) - the real 120s bench proxy lives in "ges-bench". Guarded on existence so a
        // missing file leaves the timeline empty (visibly "no reel loaded") instead of showing
        // dead clips with no video/waveform.
        const char* demoSrc = "X:/AI-2/becky-tools/native/ges-bench/proxyA.mp4";
        std::ifstream demoCheck(demoSrc, std::ios::binary);
        if (demoCheck.good()) {
            struct Seg { double in, out; }; Seg segs[] = { {5,25}, {40,60}, {75,95}, {100,115} };
            double cs = 0; for (auto& s : segs) { g_track[0].push_back({ s.in, s.out, cs, "clip", demoSrc, "" }); cs += s.out - s.in; }
        }
    }
    recomputeDur(); relabel(0); relabel(1);
    for (auto& c : g_track[0]) peaksRequest(c.source, c.in - 1.0, c.out + 5.0);

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
    // D3D/mpv came up and the render loop ran - but WS_VISIBLE was never set, so
    // double-clicking the desktop button produced a live process and NOTHING on
    // screen. Only the first call is special-cased, so the second one always wins.
    ShowWindow(hwnd, SW_SHOWMAXIMIZED);
    ShowWindow(hwnd, SW_SHOWMAXIMIZED);
    UpdateWindow(hwnd);

    // D-1: launch mpv AFTER the window is visible - CreateProcess itself is fast, but
    // the IPC pipe connect retries on its own thread (mpvLaunch only spawns; it never
    // blocks here), so a slow/missing mpv.exe can never delay the window Jordan sees.
    if (!mpvLaunch(hwnd)) crashLog("mpv: launch failed - video decode disabled, window still opening");

    IMGUI_CHECKVERSION(); ImGui::CreateContext(); ImGui::GetIO().IniFilename = nullptr; ImGui::StyleColorsDark();
    ImGui::GetIO().FontGlobalScale = 1.2f;
    ImGui_ImplWin32_Init(hwnd); ImGui_ImplDX11_Init(g_dev, g_ctx);

    double lastComposed = -1; bool playing = false;
    bool mpvArmedOnce = false;   // forces one dispatch the instant mpv finishes its async connect (see below)
    bool wasComposeContinuous = false;   // I-5: was the PREVIOUS frame mid-churn (playing/dragging)?
    double lastComposeContinuousEmit = -1;   // I-5: last time a continuous-churn compose dispatched (throttle to ~60/s)
    LARGE_INTEGER fq, prev; QueryPerformanceFrequency(&fq); QueryPerformanceCounter(&prev);
    bool run = true;
    long frameIdx = 0; double traceT0 = nowSec();
    while (run) {
        MSG msg; while (PeekMessage(&msg, nullptr, 0, 0, PM_REMOVE)) { TranslateMessage(&msg); DispatchMessage(&msg); if (msg.message == WM_QUIT) run = false; }
        if (!run) break;
        LARGE_INTEGER now; QueryPerformanceCounter(&now);
        double dt = (double)(now.QuadPart - prev.QuadPart) / fq.QuadPart; prev = now;
        frameTraceTick(++frameIdx, nowSec() - traceT0, dt * 1000.0);

        g_busyHint = playing || g_gest.kind != 0;

        if (!g_visible) { Sleep(30); continue; }

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
                if (GetKeyState(VK_SHIFT) & 0x8000) {
                    // D-4: Shift+Space toggles 2x playback rate (not play state).
                    g_playRate = (g_playRate > 1.5) ? 1.0 : 2.0;
                } else {
                    playing = !playing; g_playingExt = playing;
                    if (!playing && g_stockSec >= 0) { curSec = g_stockSec; g_stockSec = -1; g_stockFlash = false; }
                }
            }
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
            if (GetAsyncKeyState('S') & 1) {
                double t = editT();
                Clip* c = clipAtComp(0, t);
                bool noId = c && c->id.empty();
                bool gated = c && !noId && g_editsInFlight.count(c->id);
                editLog("EDGE S clip=" + (c ? c->id : std::string("none")) + " gated=" + (gated ? "1" : "0") + (noId ? " (preview-only, no engine id)" : ""));
                if (c && !noId && !gated) {
                    double srcT = c->in + (t - c->compStart);
                    EditReq req; req.verb = "split"; req.args = { {"id", c->id}, {"at", srcT} };
                    req.kind = 0; req.t = t; req.group = g_group;
                    g_editsInFlight.insert(c->id);
                    queueEdit(std::move(req));
                    editLog("QUEUE split id=" + c->id);
                }
                if (!playing) lastComposed = -1;
            }
            if (GetAsyncKeyState(VK_DELETE) & 1) {
                double t = editT();
                Clip* c = clipAtComp(0, t);
                bool noId = c && c->id.empty();
                bool gated = c && !noId && g_editsInFlight.count(c->id);
                editLog("EDGE DEL clip=" + (c ? c->id : std::string("none")) + " gated=" + (gated ? "1" : "0") + (noId ? " (preview-only, no engine id)" : ""));
                if (c && !noId && !gated) {
                    auto rem = std::make_pair(c->compStart, c->out - c->in);
                    EditReq req; req.verb = "remove_clip"; req.args = { {"id", c->id} };
                    req.kind = 1; req.t = t; req.group = g_group; req.rem = rem;
                    g_editsInFlight.insert(c->id);
                    queueEdit(std::move(req));
                    editLog("QUEUE remove_clip id=" + c->id);
                }
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
                if (c && !noId && !gated && srcT > c->in + 0.05 && srcT < c->out - 0.05) {
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
                if (c && !noId && !gated && srcT > c->in + 0.05 && srcT < c->out - 0.05) {
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
            if (GetAsyncKeyState('Z') & 1) { if (GetKeyState(VK_CONTROL) & 0x8000) {
                // Debounced: with the blocking engineCall() this replaced, a physical
                // keypress could never queue a second "undo" before the first's round
                // trip finished - the block was an accidental throttle. Non-blocking
                // removes that throttle, and undo is the one edit where a spurious
                // extra call is destructive (it walks past the intended edit into
                // whatever came before it, observed live this session: a single split
                // plus two Ctrl+Z presses emptied the whole demo reel, not just the
                // split - see COULD NOT DO, not fully root-caused). 250ms is far under
                // any real double-tap but absorbs a same/next-frame double edge.
                double n = nowSec();
                if (n - g_lastUndoQueued > 0.25) {
                    g_lastUndoQueued = n;
                    editLog("EDGE CTRLZ");
                    // req.args MUST be json::object(), not {} - "json x = {}" prefers the
                    // default ctor over the initializer-list ctor for an empty brace list,
                    // producing JSON null. The drain loop below (~line 2280) unconditionally
                    // calls res.req.args.value("id", ...) for every completed edit including
                    // undo; a null args threw json::type_error.306 there on EVERY undo (caught,
                    // logged, silently swallowed), which skipped the rest of that drain
                    // iteration - including the loadTimelineView(__timeline) call - so undo's
                    // engine round-trip succeeded but the visible timeline was NEVER refreshed
                    // to match it. Found live via a 5-minute rapid split/undo stress drive
                    // (BECKY_REVIEW_EDIT_LOG showed "EXCEPTION in UI drain: cannot use value()
                    // with null" on every single Ctrl+Z) - this is almost certainly the real
                    // cause of the earlier "two Ctrl+Z emptied the whole reel" oddity, not just
                    // a walked-past-the-edit undo bug.
                    EditReq req; req.verb = "undo"; req.args = json::object(); req.kind = 4; req.t = curSec;
                    queueEdit(std::move(req));
                }
            } }
            bool ctrlDown = (GetKeyState(VK_CONTROL) & 0x8000) != 0;
            if (GetAsyncKeyState(VK_LEFT) & 1) {
                if (ctrlDown) {
                    // E-4: jump to the previous clip boundary anywhere on the timeline.
                    double best = 0; bool found = false;
                    for (auto& c : g_track[0]) if (c.compStart < curSec - 0.01 && (!found || c.compStart > best)) { best = c.compStart; found = true; }
                    if (found) { curSec = best; playing = false; g_playingExt = false; }
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
                    double best = g_compDur; bool found = false;
                    for (auto& c : g_track[0]) { double end = c.compStart + (c.out - c.in); if (end > curSec + 0.01 && (!found || end < best)) { best = end; found = true; } }
                    if (found) { curSec = best; playing = false; g_playingExt = false; }
                } else {
                    Clip* c = clipAtComp(0, curSec);
                    double fps = sourceFps(c ? c->source : std::string());
                    curSec = std::min(g_compDur, curSec + (1.0 / fps)); playing = false; g_playingExt = false;
                }
            }
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
                char msMsg[64]; snprintf(msMsg, sizeof(msMsg), " (%.0fms)", done.elapsedMs);
                if (!done.ok) { g_searchErr = (done.err.empty() ? "search failed" : done.err) + msMsg; g_searchMode.clear(); g_hits.clear(); }
                else { g_searchErr.clear(); g_searchMode = done.mode; g_searchNote = done.note + msMsg; g_hits = std::move(done.hits); }
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
                    g_renderMsg = "Added dropped file to timeline";
                } else {
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
                            " clips, peaksJobsEnqueued=" + std::to_string(g_peaksJobsEnqueued.load()));
                    }
                    switch (res.req.kind) {
                    case 0: { // split
                        if (res.req.group) splitTrack(1, res.req.t);
                        std::string newId = res.data.value("new_id", std::string());
                        if (!newId.empty()) { g_sel.insert(newId); g_selAnchor = newId; emitSelect(); }
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

        // D-9: PLAYBACK. This used to be the whole of "playing": a simulated clock
        // (curSec += dt) driving per-frame hr-seeks into a permanently paused mpv - which
        // is precisely why there was no audio. Now mpv genuinely plays the reel (EDL) and
        // this block FOLLOWS mpv's clock instead of commanding it, so audio exists and A/V
        // sync + the source's true frame rate come from mpv rather than a wall clock.
        if (playing && !g_track[0].empty()) {
            if (!g_edlActive.load()) {
                mpvEdlEnter(curSec);
                // mpv drops OSD overlays when a new file is loaded, but the caption code
                // skips its IPC push while it believes the same text is already up - so
                // the caption would silently vanish for the rest of the cue. Forget that
                // belief across every (re)load so the next frame re-pushes it.
                g_capOsdShowing = false;
            } else {
                // An edit landed mid-playback (split/trim/delete/reorder, or the G-1 tied
                // preview swapping the reel out): the EDL mpv holds is stale, so rebuild
                // and resume at the same spot. curSec has already been ripple-compensated
                // by the edit drain above, so re-entering here stays on the right frame.
                if (edlTrackSig() != g_edlSigLoaded) { mpvEdlEnter(curSec); g_capOsdShowing = false; }
                else if (g_playRate != g_edlSpeedSet) {
                    mpvCommand(json::array({ "set_property", "speed", g_playRate }));
                    g_edlSpeedSet = g_playRate;
                }
            }

            if (g_edlActive.load()) {
                double tp = g_mpvTimePos.load();
                // Right after a seek/load, mpv keeps reporting the OLD position for a few
                // frames. Hold the requested spot until its clock catches up (or 500ms
                // passes) so the playhead never visibly snaps backwards then forwards.
                bool seekPending = g_edlSeekTarget >= 0 &&
                    (tp < 0 || (std::abs(tp - g_edlSeekTarget) > 1.0 && nowSec() - g_edlSeekAt < 0.5));
                if (seekPending) curSec = g_edlSeekTarget;
                else {
                    g_edlSeekTarget = -1;
                    if (tp >= 0) {
                        // mpv pushes time-pos on its own cadence (measured ~0.15s), so taking
                        // it raw makes the playhead visibly trail the picture and step rather
                        // than glide. mpv stays the authority - every new value re-anchors -
                        // and dt only smooths BETWEEN its updates. Clamped so that if mpv
                        // stops reporting (stall/EOF) the playhead can't run away from it.
                        static double s_lastTp = -1, s_tpAt = 0;
                        if (tp != s_lastTp) { s_lastTp = tp; s_tpAt = nowSec(); }
                        double ext = (nowSec() - s_tpAt) * g_playRate;
                        if (ext > 0.5) ext = 0.5;
                        curSec = tp + ext;
                    } else curSec += dt * g_playRate;   // mpv hasn't reported yet - keep moving
                }
            } else {
                curSec += dt * g_playRate;   // mpv down / EDL unwritable: degrade to the old tick
            }

            // E-10: below-threshold ranges are SKIPPED seamlessly during playback,
            // never just dimmed-and-played (FB7: "the single biggest breakthrough").
            // Now that mpv owns the position, the skip has to move MPV, not just curSec.
            if (g_thrOn) for (auto& r : g_quietRanges) if (curSec >= r.first && curSec < r.second) { curSec = r.second; mpvEdlSeek(curSec); break; }
            if (curSec >= g_compDur) {
                curSec = 0; mpvEdlSeek(0);
                // --keep-open=yes pauses mpv at EOF, so looping has to un-pause it too.
                if (g_edlActive.load()) mpvCommand(json::array({ "set_property", "pause", false }));
            }
        } else if (g_edlActive.load()) {
            // Stopped playing: hand the picture back to the frame-exact paused scrub path.
            mpvEdlExit();
            lastComposed = -1;      // force one exact recompose so the parked frame is frame-exact
            g_capOsdShowing = false; // the recompose reloads the real clip - re-push the caption
        }

        // G-1 "Play tied clips" preview ends the instant playback stops (pause, arrow
        // step, or reaching a boundary handler that pauses) - restore the real reel
        // that was showing before the preview so the timeline never sits corrupted.
        if (g_inTiedPreview && !playing) {
            g_track[0] = g_reelBeforePreview;
            g_reelBeforePreview.clear();
            g_inTiedPreview = false;
            packTrack(0); recomputeDur();
            curSec = std::min(curSec, g_compDur);
            lastComposed = -1; g_quietDirty = true;
            for (auto& c : g_track[0]) peaksRequest(c.source, c.in - 1.0, c.out + 5.0);
        }

        // P1 fix: never decode on the UI thread. Post the newest target to the decode
        // thread (non-blocking); the decode thread issues the mpv seek and mpv paints
        // its own child hwnd directly - there is no frame buffer for the UI thread to
        // poll anymore (D-1: mpv owns the pixels), only the pane's position/size is
        // pushed to it each frame below (center video pane block).
        // mpvArmedOnce: mpv connects to its IPC pipe on its OWN thread, asynchronously
        // (mpvLaunch never blocks the UI thread - see its comment). On a paused/static
        // startup curSec never changes, so the ordinary "curSec != lastComposed" gate
        // fires exactly once, at t=0 - before mpv is necessarily connected yet - and
        // then never again, leaving the video pane permanently black even after mpv
        // comes up a moment later. Force exactly one extra dispatch the first frame
        // mpv reports available, so the pending clip always gets shown.
        bool mpvReadyNow = g_mpvAvailable.load();
        // I-5 fix: curSec churns every single frame during "playing" (main()'s own
        // dt-driven tick above) and during an active scrub-drag (g_gest.kind==1) -
        // both are continuous, so each gets a cheap keyframe seek (mpvSeekExact's
        // comment has the full story on why "exact" every frame can permanently
        // wedge the decode thread). The instant churn STOPS (this frame's continuous
        // flag flips false vs. last frame's), force one more dispatch even if curSec
        // happens to be unchanged, so the settle/release always lands an exact frame.
        bool composeContinuous = playing || g_gest.kind == 1;
        bool composeExact = !composeContinuous;
        bool composeSettling = wasComposeContinuous && !composeContinuous;
        // I-5 fix, part 2 (found live via the same scrub-log evidence): a cheap
        // keyframe seek still isn't FREE - mpv still has to do real decode work per
        // seek, just less of it. This render loop is otherwise uncapped (no vsync
        // wait), so during playback/drag it was still asking mpv for 200-1000+
        // seeks/sec; the named pipe's kernel buffer absorbs a burst (WriteFile looks
        // instant for a while) before mpv falls far enough behind to fill it, so the
        // SAME permanent wedge (mpvWriteLine's WriteFile, no timeout) reappeared
        // ~149 decodes in during a live re-test. Throttling continuous dispatch to
        // ~60/sec - matching emitScrub's existing precedent for the exact same
        // "engine seek" flood - caps the request rate at what mpv can actually keep
        // up with; a settle/final dispatch (composeSettling) is never throttled.
        if (g_edlActive.load()) {
            // D-9: mpv is PLAYING the reel itself - it owns the position and paints its own
            // frames. Dispatching scrub seeks here would drag playback backwards every frame
            // and re-create the I-5 IPC flood. lastComposed is reset on exit (see above), so
            // the first paused frame still gets its exact recompose.
        } else if (composeContinuous && !composeSettling && nowSec() - lastComposeContinuousEmit < 0.016) {
            // skip this frame's dispatch - too soon since the last one
        } else if (!g_track[0].empty() && (curSec != lastComposed || (mpvReadyNow && !mpvArmedOnce) || composeSettling)) {
            requestCompose(curSec, composeExact); lastComposed = curSec;
            if (composeContinuous) lastComposeContinuousEmit = nowSec();
        }
        wasComposeContinuous = composeContinuous;
        if (mpvReadyNow) mpvArmedOnce = true;
        if (g_resize) { resizeD3D(); g_resize = false; }

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
            ImGui::Text("Becky Review (native)");
            ImGui::Separator();
            if (ImGui::MenuItem("Open Folder...", "Ctrl+O")) {
                // Native folder dialog via the engine (pick_folder verb on Windows).
                json r = engineCall("pick_folder", {}, 600.0);   // PickFolder also indexes (see loadFolder note)
                if (r.value("ok", false)) {
                    const json& d = r.contains("data") ? r["data"] : r;
                    if (d.value("picked", false) && d.contains("folder")) { g_folderErr.clear(); applyFolderView(d["folder"], std::string()); }
                } else {
                    g_folderErr = r.value("error", std::string("pick_folder failed"));
                }
            }
            ImGui::Text("%.1fs / %.0fs", curSec, g_compDur);
            if (!g_engine.alive) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "  ENGINE DOWN");
            if (!g_mpvAvailable.load()) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "  MPV DOWN");
            ImGui::EndMainMenuBar();
        }

        // ---- left panel: library / search / transcript (B, C) ----
        ImGui::SetNextWindowPos({ 0, 0 }); ImGui::SetNextWindowSize({ (float)g_W * 0.22f, (float)g_H });
        if (ImGui::Begin("library", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus)) {
            bool libFocusedNow = ImGui::IsWindowFocused(ImGuiFocusedFlags_RootAndChildWindows);
            ImGui::Text("Library / Search");
            ImGui::Separator();
            bool submitted = ImGui::InputText("##search", g_searchBuf, sizeof g_searchBuf, ImGuiInputTextFlags_EnterReturnsTrue);
            if (submitted) runSearch(false);
            if (ImGui::SmallButton("Search")) runSearch(false);
            ImGui::SameLine();
            if (ImGui::SmallButton("Smart (qmd)")) runSearch(true);
            ImGui::SameLine();
            if (ImGui::SmallButton("Clear")) { g_searchBuf[0] = 0; g_hits.clear(); g_searchMode.clear(); g_searchErr.clear(); }
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
                ImGui::Separator();
                ImGui::BeginChild("hits", { 0, 0 }, false);
                for (size_t i = 0; i < g_hits.size(); i++) {
                    Hit& h = g_hits[i];
                    ImGui::PushID((int)i);
                    std::string line = h.timecode + "  " + h.text;
                    if (h.transcriptOnly) {
                        ImGui::TextDisabled("%s", line.c_str());
                    } else {
                        // click plays at the verbatim timestamp (C-4); double-click adds to timeline.
                        if (ImGui::Selectable(line.c_str(), false, ImGuiSelectableFlags_AllowDoubleClick)) {
                            if (ImGui::IsMouseDoubleClicked(ImGuiMouseButton_Left)) addHitToTimeline(h);
                            else seekToSpan(h.source, h.start, h.end, true, curSec, playing, lastComposed);
                        }
                        if (ImGui::IsItemHovered()) ImGui::SetTooltip("%s\n%s", h.name.c_str(), h.date.c_str());
                    }
                    ImGui::PopID();
                }
                ImGui::EndChild();
            } else if (!g_cueName.empty()) {
                // ---- flowing single-video transcript view (B-8) ----
                if (ImGui::SmallButton("< Back")) { g_cueName.clear(); g_cues.clear(); g_cueErr.clear(); }
                ImGui::SameLine(); ImGui::TextDisabled("%s", g_cueName.c_str());
                ImGui::InputTextWithHint("##within", "search within this transcript", g_withinBuf, sizeof g_withinBuf);
                ImGui::Separator();
                if (!g_cueErr.empty()) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "%s", g_cueErr.c_str());
                ImGui::BeginChild("transcript", { 0, 0 }, false);
                std::string within(g_withinBuf);
                for (auto& c : g_cues) {
                    if (!within.empty() && c.text.find(within) == std::string::npos) continue;
                    std::string line = c.timecode + "  " + c.text;
                    // click a cue -> player seeks there, PAUSED (D-3/B-8).
                    if (ImGui::Selectable(line.c_str())) seekToSpan(c.source, c.start, c.end, false, curSec, playing, lastComposed);
                }
                ImGui::EndChild();
            } else {
                // ---- video library list (B-1/B-3/B-4/B-5/B-6/B-7) ----
                const char* sortLabel = g_sortMode == 0 ? "Date (newest)" : g_sortMode == 1 ? "Date (oldest)" : g_sortMode == 2 ? "Name A-Z" : "Name Z-A";
                if (ImGui::SmallButton(sortLabel)) { g_sortMode = (g_sortMode + 1) % 4; sortLibrary(); }
                if (g_orphanCount > 0) { ImGui::SameLine(); ImGui::TextDisabled("(+%d orphan transcripts)", g_orphanCount); }
                if (!g_folderErr.empty()) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "%s", g_folderErr.c_str());
                ImGui::Separator();
                if (g_videos.empty()) {
                    ImGui::TextDisabled("Ctrl+O to open a folder");
                } else if (libFocusedNow && !ImGui::GetIO().WantTextInput) {
                    if (ImGui::IsKeyPressed(ImGuiKey_DownArrow)) { g_libSel = std::min((int)g_videos.size() - 1, g_libSel + 1); g_libScrollPending = true; }
                    if (ImGui::IsKeyPressed(ImGuiKey_UpArrow)) { g_libSel = std::max(0, g_libSel - 1); g_libScrollPending = true; }
                }
                ImGui::BeginChild("videos", { 0, 0 }, false);
                for (int i = 0; i < (int)g_videos.size(); i++) {
                    VideoRow& v = g_videos[i];
                    ImGui::PushID(i);
                    bool sel = (g_libSel == i);
                    bool inFlight;
                    { std::lock_guard<std::mutex> lk(g_transcribeMx); inFlight = g_transcribeInFlight.count(v.path) != 0; }
                    std::string label = v.name + (inFlight ? "  (transcribing...)" : v.hasTranscript ? "" : "  [no transcript]");
                    // ONE selection model (B-4): mouse click sets the SAME index arrows move.
                    if (ImGui::Selectable(label.c_str(), sel, ImGuiSelectableFlags_AllowDoubleClick)) {
                        g_libSel = i;
                        if (ImGui::IsMouseDoubleClicked(ImGuiMouseButton_Left)) { openTranscript(v.path); g_libJustViewedIdx = i; }
                    }
                    if (g_libJustViewedIdx == i) {
                        ImGui::GetWindowDrawList()->AddRect(ImGui::GetItemRectMin(), ImGui::GetItemRectMax(), IM_COL32(0x14, 0xFF, 0x39, 255));
                    }
                    if (ImGui::BeginPopupContextItem("rowctx")) {
                        g_libSel = i;
                        if (ImGui::MenuItem("Open in File Browser")) openInFileBrowser(v.path);
                        if (ImGui::MenuItem("Copy File Name")) ImGui::SetClipboardText(baseName(v.path).c_str());
                        // B-2: one-click local transcription (Parakeet, official-first) - never
                        // overwrites an original transcript (enforced engine-side). Disabled
                        // while this exact video is already transcribing (in-flight guard).
                        if (inFlight) ImGui::BeginDisabled();
                        if (ImGui::MenuItem(v.hasTranscript ? "Re-transcribe" : "Transcribe")) requestTranscribe(v.path, v.name);
                        if (inFlight) ImGui::EndDisabled();
                        ImGui::EndPopup();
                    }
                    if (g_libSel == i && g_libScrollPending) { ImGui::SetScrollHereY(0.5f); g_libScrollPending = false; }
                    ImGui::PopID();
                }
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

        // ---- center video pane (D-1: mpv --wid child hwnd, not an ImGui image) ----
        ImGui::SetNextWindowPos({ (float)g_W * 0.22f, 0 }); ImGui::SetNextWindowSize({ (float)g_W * 0.56f, (float)g_H * 0.78f });
        if (ImGui::Begin("video", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus)) {
            bool haveClip = !g_track[0].empty();
            if (g_mpvHwnd && g_mpvAvailable.load() && haveClip) {
                // mpv paints this rect itself (GPU hwdec, its own compositor surface) -
                // the app only has to keep the child hwnd positioned/sized to match the
                // pane and reserve that space in the ImGui layout below it.
                ImVec2 origin = ImGui::GetCursorScreenPos();
                ImVec2 avail = ImGui::GetContentRegionAvail();
                // Reserve room for BOTH control rows below the preview (Play/|<</2x/Overlay/
                // Render/Render Selection, then Screenshot/Save Reel/Load Reel/Export EDL) plus
                // the curSec/dur text line and the g_renderMsg status line - reserving only ONE
                // row here (the bug found live this session) let the video Dummy eat the space
                // row 2 and g_renderMsg needed, silently clipping them outside the fixed-height
                // "video" window: Render/Transcribe/Screenshot never had a visible success/fail
                // readout, which is exactly why B-2 could never be confirmed complete on screen.
                float ctrlH = ImGui::GetTextLineHeightWithSpacing() * 2 + ImGui::GetFrameHeightWithSpacing() * 2;
                float videoH = std::max(0.0f, avail.y - ctrlH);
                POINT pt{ (LONG)origin.x, (LONG)origin.y }; ScreenToClient(g_hwnd, &pt);
                MoveWindow(g_mpvHwnd, pt.x, pt.y, (int)avail.x, (int)videoH, TRUE);
                ShowWindow(g_mpvHwnd, SW_SHOWNA);
                ImGui::Dummy({ avail.x, videoH });
                // D-6: push/refresh the provenance overlay into mpv's own surface for
                // whichever clip is under the playhead right now - every frame is cheap
                // (mpvUpdateOverlay no-ops unless the mode is "shown" or the text changed).
                mpvUpdateOverlay(clipAtComp(0, curSec));
                // The caption itself, at this reel's saved vertical placement.
                mpvUpdateCaptionOsd(curSec);

                // ---- drag a caption UP or DOWN to place ALL of them ----
                // mpv's --wid child hwnd belongs to another PROCESS, so it swallows
                // every mouse message over the video: ImGui never sees a click there
                // and an InvisibleButton over the pane would never fire. Polling the
                // OS cursor + button is the whole mechanism (mpv is launched with
                // --input-cursor=no so it ignores the very same clicks).
                if (!g_capPath.empty() && videoH > 32) {
                    POINT cp; GetCursorPos(&cp); ScreenToClient(g_hwnd, &cp);
                    bool inPane = cp.x >= (LONG)origin.x && cp.x <= (LONG)(origin.x + avail.x) &&
                                  cp.y >= (LONG)origin.y && cp.y <= (LONG)(origin.y + videoH);
                    bool btn = (GetAsyncKeyState(VK_LBUTTON) & 0x8000) != 0;
                    bool mine = GetForegroundWindow() == g_hwnd;
                    if (!g_capMarginDrag && btn && mine && inPane) {
                        g_capMarginDrag = true;
                        g_capMarginAtGrab = g_capMarginV;
                        g_capMarginGrabY = (double)cp.y;
                        // The 288-tall ASS canvas is fitted to the pane's height, so
                        // one screen pixel is 288/videoH of a MarginV unit.
                        g_capMarginUnitsPerPx = (double)CAP_ASS_H / (double)videoH;
                    } else if (g_capMarginDrag && btn) {
                        // Drag UP (negative dy) must RAISE the captions, and MarginV
                        // grows upward from the bottom edge - hence the minus.
                        double dy = (double)cp.y - g_capMarginGrabY;
                        int m = g_capMarginAtGrab - (int)std::llround(dy * g_capMarginUnitsPerPx);
                        if (m < 0) m = 0;
                        if (m > CAP_ASS_H - 20) m = CAP_ASS_H - 20;   // keep it on screen
                        g_capMarginV = m;
                    } else if (g_capMarginDrag && !btn) {
                        g_capMarginDrag = false;
                        saveCapStyle();          // one write per drag, not per frame
                        g_renderMsg = "Caption placement saved (MarginV " + std::to_string(g_capMarginV) + ")";
                        g_renderMsgAt = nowSec();
                    }
                }
            } else {
                if (g_mpvHwnd) ShowWindow(g_mpvHwnd, SW_HIDE);
                if (!g_mpvAvailable.load()) {
                    ImGui::TextDisabled("video decode unavailable (mpv failed to start - shell/library/timeline still work)");
                } else {
                    ImGui::TextDisabled("video pane (mpv) - no clip loaded");
                }
                mpvClearOverlay();
                mpvClearCaptionOsd();
            }
            ImGui::Text("%.1f / %.1f s", curSec, g_compDur);
            if (ImGui::Button(playing ? "Pause##play" : "Play##play")) { playing = !playing; g_playingExt = playing; }
            ImGui::SameLine();
            if (ImGui::Button("|<<")) { curSec = 0; g_playingExt = playing; }
            ImGui::SameLine();
            if (g_playRate > 1.5) ImGui::PushStyleColor(ImGuiCol_Button, ImVec4(0.2f, 0.5f, 0.9f, 1));
            if (ImGui::Button("2x")) g_playRate = (g_playRate > 1.5) ? 1.0 : 2.0;
            if (g_playRate > 1.5) ImGui::PopStyleColor();
            ImGui::SameLine();
            // D-6: 3-state provenance overlay toggle (off / on-hidden-in-preview DEFAULT /
            // on-previewed) - clicking cycles it; render always burns in whichever text
            // "on-previewed" would show, since Render.Enabled tracks mode!=0 (setOverlayMode).
            {
                const char* ovLabel = g_ovMode == 0 ? "Overlay: Off##ov"
                                    : g_ovMode == 1 ? "Overlay: On (hidden)##ov"
                                                    : "Overlay: On (shown)##ov";
                if (ImGui::Button(ovLabel)) setOverlayMode((g_ovMode + 1) % 3);
            }
            ImGui::SameLine();
            // F-3/F-4: naming (clips_SOURCE_NNNN.mp4 / clips_compilation_NNNN.mp4,
            // never overwrites) is entirely engine-side (renderReel) - the button
            // just calls export with no output path and shows what came back.
            if (ImGui::Button("Render")) {
                json r = engineCall("export", { {"output", ""} }, 300.0);
                if (r.value("ok", false)) {
                    const json& d = r.contains("data") ? r["data"] : r;
                    g_renderMsg = "Rendered " + d.value("mp4", std::string());
                    openInFileBrowser(d.value("mp4", std::string()));
                } else g_renderMsg = "Render failed: " + r.value("error", std::string("?"));
                g_renderMsgAt = nowSec();
            }
            ImGui::SameLine();
            char selLabel[40]; snprintf(selLabel, sizeof selLabel, "Render Selection (%d)", (int)g_sel.size());
            if (g_sel.empty()) ImGui::BeginDisabled();
            if (ImGui::Button(selLabel)) {
                std::vector<std::string> ids(g_sel.begin(), g_sel.end());
                json r = engineCall("export_selection", { {"ids", ids}, {"output", ""} }, 300.0);
                if (r.value("ok", false)) {
                    const json& d = r.contains("data") ? r["data"] : r;
                    g_renderMsg = "Rendered " + d.value("mp4", std::string());
                    openInFileBrowser(d.value("mp4", std::string()));
                } else g_renderMsg = "Render failed: " + r.value("error", std::string("?"));
                g_renderMsgAt = nowSec();
            }
            if (g_sel.empty()) ImGui::EndDisabled();
            // D-5/F-2/F-5: screenshot + save/load reel + EDL export. Engine verbs already
            // existed (grab_frame/save_reel/load_reel/write_edl); only the buttons were missing.
            if (ImGui::Button("Screenshot")) {
                Clip* cur = nullptr;
                for (auto& c : g_track[0]) if (curSec >= c.compStart && curSec < c.compStart + (c.out - c.in)) { cur = &c; break; }
                if (!cur && !g_track[0].empty()) cur = &g_track[0].back();
                if (cur) {
                    double srcT = cur->in + (curSec - cur->compStart);
                    json r = engineCall("grab_frame", { {"source", cur->source}, {"t", srcT} }, 20.0);
                    // Same bug class root-caused for "undo" above (line ~281): r["data"] on a
                    // reply that omits "data" vivifies a null, and .value() on that null throws
                    // uncaught on the UI thread -> std::terminate -> abort (ucrtbase.dll 0xC0000409).
                    // r.value("data", json::object()) never vivifies; always safe.
                    g_renderMsg = r.value("ok", false) ? "Saved " + r.value("data", json::object()).value("path", std::string()) : "Screenshot failed: " + r.value("error", std::string("?"));
                } else g_renderMsg = "Screenshot failed: no clip at playhead";
                g_renderMsgAt = nowSec();
            }
            ImGui::SameLine();
            if (ImGui::Button("Save Reel")) {
                json r = engineCall("save_reel", { {"path", ""} }, 20.0);
                g_renderMsg = r.value("ok", false) ? "Saved reel " + r.value("data", json::object()).value("path", std::string()) : "Save reel failed: " + r.value("error", std::string("?"));
                g_renderMsgAt = nowSec();
            }
            ImGui::SameLine();
            if (ImGui::Button("Load Reel")) {
                std::string path = pickOpenReelFile(hwnd);
                if (!path.empty()) {
                    json r = engineCall("load_reel", { {"path", path} }, 30.0);
                    if (r.value("ok", false)) { loadTimelineView(r.contains("data") ? r["data"] : r); curSec = 0; playing = false; g_playingExt = false; lastComposed = -1; loadCaptions(path); g_renderMsg = "Loaded reel " + baseName(path); }
                    else g_renderMsg = "Load reel failed: " + r.value("error", std::string("?"));
                    g_renderMsgAt = nowSec();
                }
            }
            ImGui::SameLine();
            if (ImGui::Button("Export EDL")) {
                json r = engineCall("write_edl", { {"output", ""} }, 30.0);
                if (r.value("ok", false)) { std::string p = r.value("data", json::object()).value("path", std::string()); g_renderMsg = "Wrote EDL " + p; openInFileBrowser(p); }
                else g_renderMsg = "Export EDL failed: " + r.value("error", std::string("?"));
                g_renderMsgAt = nowSec();
            }
            if (!g_renderMsg.empty() && nowSec() - g_renderMsgAt < 8.0) ImGui::TextDisabled("%s", g_renderMsg.c_str());
        }
        ImGui::End();

        // ---- right panel: Q&A / ask-becky (G) ----
        ImGui::SetNextWindowPos({ (float)g_W * 0.78f, 0 }); ImGui::SetNextWindowSize({ (float)g_W * 0.22f, (float)g_H });
        if (ImGui::Begin("qa", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus)) {
            ImGui::Text("Q&A / Ask-Becky");
            ImGui::Separator();
            if (!g_cardsErr.empty()) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "%s", g_cardsErr.c_str());
            if (g_cards.empty()) {
                ImGui::TextDisabled("no review questions loaded");
            } else {
                ImGui::BeginChild("cards", { 0, (float)g_H * 0.45f }, true);
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
                                if (!g_inTiedPreview) { g_reelBeforePreview = g_track[0]; g_inTiedPreview = true; }
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
                        if (!c.answer.empty()) ImGui::TextWrapped("answer: %s", c.answer.c_str());
                    }
                    ImGui::PopID();
                }
                ImGui::EndChild();
            }
            ImGui::Separator();
            static char askBuf[512] = { 0 };
            if (ImGui::InputTextMultiline("##ask", askBuf, sizeof askBuf, { -1, (float)g_H * 0.15f }, ImGuiInputTextFlags_EnterReturnsTrue)) {
                std::string q(askBuf);
                if (!q.empty()) {
                    json r = engineCall("ask", { {"utterance", q} }, 30.0);
                    if (r.value("ok", false)) {
                        json d = r["data"];
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
                    askBuf[0] = 0;
                }
            }
            ImGui::TextWrapped("%s", g_askAnswer.c_str());
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
                if (ImGui::Button("Apply##proposal")) {
                    json ar = engineCall("apply_proposal", { {"id", g_proposalID} }, 30.0);
                    if (ar.value("ok", false)) {
                        json d = ar["data"];
                        if (d.contains("timeline")) loadTimelineView(d["timeline"]);
                        g_askAnswer = "Applied: " + g_proposalPreview + " (Ctrl+Z reverts the whole pass)";
                    } else {
                        g_askAnswer = "Apply failed: " + ar.value("error", std::string("?"));
                    }
                    g_proposalPending = false;
                    g_proposalID.clear();
                }
                ImGui::SameLine();
                if (ImGui::Button("Reject##proposal")) {
                    engineCall("reject_proposal", { {"id", g_proposalID} }, 10.0);
                    g_askAnswer = "Rejected: " + g_proposalPreview;
                    g_proposalPending = false;
                    g_proposalID.clear();
                }
            }
        }
        ImGui::End();

        // ---- bottom timeline ----
        ImGui::SetNextWindowPos({ 0, (float)g_H * 0.78f });
        ImGui::SetNextWindowSize({ (float)g_W, (float)g_H * 0.22f });
        if (ImGui::Begin("timeline", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus)) {
            drawTimeline(curSec, playing);
        }
        ImGui::End();

        } catch (const std::exception& e) {
            crashLog(std::string("UI frame: caught ") + e.what() + " - frame degraded, not crashing");
        } catch (...) {
            crashLog("UI frame: caught non-std exception - frame degraded, not crashing");
        }

        ImGui::Render();
        float clr[4] = { 0.06f, 0.07f, 0.09f, 1.0f };
        g_ctx->OMSetRenderTargets(1, &g_rtv, nullptr);
        g_ctx->ClearRenderTargetView(g_rtv, clr);
        ImGui_ImplDX11_RenderDrawData(ImGui::GetDrawData());
        g_swap->Present(1, 0);
    }

    if (g_frameTrace.is_open()) {
        g_frameTrace << "# total_frames=" << frameIdx << " stalls_over_100ms=" << g_frameTraceStalls << "\n";
        g_frameTrace.flush();
    }

    engineShutdown();
    if (g_mpvPipe != INVALID_HANDLE_VALUE) CloseHandle(g_mpvPipe);
    if (g_mpvPipeRead != INVALID_HANDLE_VALUE) CloseHandle(g_mpvPipeRead);
    if (g_mpvProc.hProcess) { TerminateProcess(g_mpvProc.hProcess, 0); WaitForSingleObject(g_mpvProc.hProcess, 1500); CloseHandle(g_mpvProc.hProcess); CloseHandle(g_mpvProc.hThread); }
    if (g_mpvHwnd) DestroyWindow(g_mpvHwnd);
    ImGui_ImplDX11_Shutdown(); ImGui_ImplWin32_Shutdown(); ImGui::DestroyContext();
    if (g_rtv) g_rtv->Release(); if (g_swap) g_swap->Release(); if (g_ctx) g_ctx->Release(); if (g_dev) g_dev->Release();
    return 0;
}
