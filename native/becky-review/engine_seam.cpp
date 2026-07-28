// engine_seam.cpp - Go engine seam (subprocess NDJSON, edit worker, async verbs, logging).
// Extracted from main.cpp (P4 module split, 2026-07-25). No logic changes.
#include "engine_seam.h"

// --------------- Go engine seam (subprocess, NDJSON over stdin/stdout) ---------------
// Spawned once at boot: becky-review-engine.exe (clip cmd, bridge mode). One warm
// process = the folder index + transcript cache stay hot (the whole point of the engine).
Engine g_engine;

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
std::deque<Activity> g_activityLog;
std::mutex g_activityMx;

bool engineStart() {
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
void engineReader() {
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
json engineCall(const std::string& verb, const json& args, double timeoutSec) {
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
void engineShutdown() {
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
std::deque<EditReq> g_editQ;
std::mutex g_editQMx; std::condition_variable g_editQCv;
std::deque<EditResult> g_editDone;
std::mutex g_editDoneMx;
bool g_editQuit = false;
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
std::set<std::string> g_editsInFlight;
// One preview-clip promotion (add_clip + verb, see EditReq.promote) in flight at
// a time. UI-thread-only, like g_editsInFlight: set when a promote request is
// queued, cleared when its reply drains. Keeps key-spam on an unregistered clip
// from stacking N add_clips of the same span. ponytail: one global gate, not a
// per-span map - promotions are rare (first edit on a preview) and serialized
// through the FIFO editWorker anyway.
bool g_promoteInFlight = false;
// P2 (2026-07-25): edits that were GATED (clip id already in-flight) are queued
// here instead of being silently dropped. After each timeline reload (drain loop),
// pending edits are re-evaluated at their stored position against the FRESH track
// state - the post-split clip id is naturally correct because loadTimelineView
// rebuilt g_track[0]. This means rapid S presses produce rapid splits: the first
// resolves, the track reloads, and the pending re-fire hits the new clip at that
// same position. Max depth 8 (a burst of 8 gated edits is already superhuman).
std::deque<PendingEdit> g_editsPending;
const size_t kMaxPendingEdits = 8;

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
void editLogInit() {
    if (const char* p = getenv("BECKY_REVIEW_EDIT_LOG")) g_editLog.open(p, std::ios::app);
}
void editLog(const std::string& line) {
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
void scrubLogInit() {
    if (const char* p = getenv("BECKY_REVIEW_SCRUB_LOG")) g_scrubLog.open(p, std::ios::app);
}
void scrubLog(const std::string& line) {
    if (!g_scrubLog.is_open()) return;
    std::lock_guard<std::mutex> lk(g_scrubLogMx);
    g_scrubLog << nowSec() << " " << line << "\n"; g_scrubLog.flush();
}

// I-9 evidence trail, OPT-IN via BECKY_REVIEW_FRAME_TRACE=<path> (unset = zero
// overhead, no file touched). Every prior cycle's I-9/I-7 claim was a spot-check
// or a log-timestamp inference; this is a per-frame wall-clock CSV so "no >100ms
// stall for 5 minutes" is a number anyone can grep, not a narrative.
std::ofstream g_frameTrace;
long g_frameTraceStalls = 0;
void frameTraceInit() {
    if (const char* p = getenv("BECKY_REVIEW_FRAME_TRACE")) {
        g_frameTrace.open(p, std::ios::app);
        if (g_frameTrace.is_open()) g_frameTrace << "frame,tSec,deltaMs,stall\n";
    }
}
void frameTraceTick(long frameIdx, double tSec, double deltaMs) {
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
double g_stageT = 0;
const char* g_stageName = "frame-top";
void stageMark(const char* name) {
    double t = nowSec() * 1000.0;
    double d = t - g_stageT;
    if (d > 80.0) crashLogImmediate(std::string("STAGE SLOW [") + g_stageName + " -> " + name + "] " +
                            std::to_string(d) + "ms");
    g_stageT = t; g_stageName = name;
}

// Always-on crash diagnostic (no env gate - this is a safety net, not an opt-in
// trace). BUFFERED (2026-07-25): lines accumulate in memory and flush every 500ms
// from the render loop (crashLogPeriodicFlush) or immediately on critical events
// (TERMINATE, SIGABRT, STAGE SLOW). This eliminates the per-call disk flush that
// was causing 50-100 synchronous I/O stalls per second during rapid editing.
static std::ofstream g_crashLog;
std::mutex g_crashLogMx;
static std::string g_crashBuf;   // buffered lines, flushed periodically
static double g_lastCrashFlush = 0;
void crashLogFlushLocked() {
    if (!g_crashLog.is_open() || g_crashBuf.empty()) return;
    g_crashLog << g_crashBuf;
    g_crashLog.flush();
    g_crashBuf.clear();
    g_lastCrashFlush = nowSec();
}
void crashLog(const std::string& line) {
    std::lock_guard<std::mutex> lk(g_crashLogMx);
    if (!g_crashLog.is_open()) return;
    g_crashBuf += std::to_string(nowSec()) + " [tid " + std::to_string(GetCurrentThreadId()) + " " + t_threadTag + "] " + line + "\n";
}
// Immediate flush variant for critical diagnostics (stage timer, terminate handler).
void crashLogImmediate(const std::string& line) {
    std::lock_guard<std::mutex> lk(g_crashLogMx);
    if (!g_crashLog.is_open()) return;
    g_crashBuf += std::to_string(nowSec()) + " [tid " + std::to_string(GetCurrentThreadId()) + " " + t_threadTag + "] " + line + "\n";
    crashLogFlushLocked();
}
// Called from the render loop every frame; flushes if 500ms have elapsed.
void crashLogPeriodicFlush() {
    std::lock_guard<std::mutex> lk(g_crashLogMx);
    if (nowSec() - g_lastCrashFlush > 0.5) crashLogFlushLocked();
}
// Bridge for engine.cpp (crashLog above is static by design).
void engineLog(const std::string& s) { crashLog(s); }
void crashLogInit() {
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
        crashLogImmediate(std::string("TERMINATE - ") + msg);
        std::abort();
    });
    // Bug-1 forensics: the recurring 0xc0000409 faults INSIDE ucrtbase!abort
    // (export-map-verified against fault offset 0x7286e), yet crash.log stayed
    // empty - so abort() is being reached WITHOUT std::terminate (a direct CRT
    // abort from a third-party DLL, or a noexcept violation). UCRT's abort()
    // raises SIGABRT before it fastfails, and ucrtbase.dll is one shared CRT
    // for every module in the process, so this hook is the one place that sees
    // those too. Log the thread tag + raw stack (module+offset, resolvable via
    // becky-review.map) then let the crash proceed - flight recorder, not rescue.
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
        crashLogImmediate(s);
    });
}

// nowSec() of the last engine (CLIP) edit, so Ctrl+Z can tell whether the most
// recent thing to undo is a clip edit (engine "undo" verb) or a CAPTION edit
// (native-only, its own stack) - see captionTryUndo. A plain double, never a
// stack, so it can never desync or interfere with the engine's own undo history.
double g_lastEngineEditAt = -1;
void queueEdit(EditReq req) {
    // undo/redo are not NEW edits - they must not move the "last engine edit" mark,
    // or a Ctrl+Z would flip to undoing captions after undoing one clip edit.
    if (req.verb != "undo" && req.verb != "redo") g_lastEngineEditAt = nowSec();
    std::lock_guard<std::mutex> lk(g_editQMx);
    g_editQ.push_back(std::move(req));
    g_editQCv.notify_one();
}
// Worker thread: pops one request at a time (FIFO) and blocks ONLY this
// thread on the engine round-trip - the UI thread is never touched. Requests
// are processed strictly in enqueue order, so Ctrl+Z after a burst of splits
// always undoes the correct (latest) one.
void editWorker() {
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
std::mutex g_asyncMx;
std::deque<AsyncReply> g_asyncQ;

void engineCallAsync(const std::string& verb, json args, double timeoutSec,
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
void drainAsync() {
    std::deque<AsyncReply> q;
    { std::lock_guard<std::mutex> lk(g_asyncMx); q.swap(g_asyncQ); }
    for (auto& a : q) {
        if (!a.cb) continue;
        try { a.cb(a.r); } catch (...) {}   // a bad callback must not kill the frame
    }
}
