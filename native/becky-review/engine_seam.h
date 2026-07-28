#pragma once
// engine_seam.h - Go engine seam (subprocess NDJSON, edit worker, async verbs, logging).
// Extracted from main.cpp (P4 module split, 2026-07-25). No logic changes.

#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <string>
#include <vector>
#include <map>
#include <set>
#include <deque>
#include <mutex>
#include <atomic>
#include <condition_variable>
#include <thread>
#include <functional>
#include <fstream>
#include <csignal>
#include <iomanip>
#include <chrono>
#include <cstdint>
#include "json.hpp"
using json = nlohmann::json;

// --------------- utilities defined in main.cpp (linkage opened for the split) ---------------
extern thread_local const char* t_threadTag;
double nowSec();
void fwslash(std::string& s);
void beginWork(const std::string& label);
void endWork();

// --------------- logging (defined in this module) ---------------
void crashLog(const std::string& line);
void editLog(const std::string& line);

// --------------- Go engine seam (subprocess, NDJSON over stdin/stdout) ---------------
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

// H-5 activity struct
struct Activity {
    std::string kind;    // "started" | "progress" | "done"
    std::string source;  // which verb produced it, e.g. "ask", "apply_edit_batch"
    std::string text;    // one line of plain language, already human-readable
    double at = 0;
};

// --------------- EDIT WORKER structs ---------------
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
struct PendingEdit { int kind; double t; };

// --------------- async verb ---------------
struct AsyncReply {
    json r;
    std::function<void(const json&)> cb;
};

// --------------- extern globals ---------------
extern Engine g_engine;
extern std::deque<Activity> g_activityLog;
extern std::mutex g_activityMx;
extern std::deque<EditReq> g_editQ;
extern std::mutex g_editQMx;
extern std::condition_variable g_editQCv;
extern std::deque<EditResult> g_editDone;
extern std::mutex g_editDoneMx;
extern bool g_editQuit;
extern std::set<std::string> g_editsInFlight;
extern bool g_promoteInFlight;
extern std::deque<PendingEdit> g_editsPending;
extern const size_t kMaxPendingEdits;
extern double g_lastEngineEditAt;
extern std::mutex g_asyncMx;
extern std::deque<AsyncReply> g_asyncQ;
extern double g_stageT;
extern const char* g_stageName;
extern std::ofstream g_frameTrace;
extern long g_frameTraceStalls;
extern std::mutex g_crashLogMx;

// --------------- function declarations ---------------
bool engineStart();
json engineCall(const std::string& verb, const json& args, double timeoutSec = 20.0);
void engineShutdown();
void engineReader();
void queueEdit(EditReq req);
void editWorker();
void engineCallAsync(const std::string& verb, json args, double timeoutSec,
                     const std::string& label, std::function<void(const json&)> cb);
void drainAsync();
void editLogInit();
void scrubLogInit();
void scrubLog(const std::string& line);
void frameTraceInit();
void crashLogInit();
void crashLogPeriodicFlush();
void crashLogImmediate(const std::string& line);
void crashLogFlushLocked();
void stageMark(const char* name);
void frameTraceTick(long frameIdx, double tSec, double deltaMs);
void engineLog(const std::string& s);
