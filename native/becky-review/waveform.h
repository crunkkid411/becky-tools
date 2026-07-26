#pragma once
// waveform.h - peaks decode pipeline (BgWorkPool, Peaks, BPK3 cache, ffmpeg decode).
// Extracted from main.cpp (P4 module split, 2026-07-25). No logic changes.

#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <string>
#include <vector>
#include <map>
#include <set>
#include <mutex>
#include <atomic>
#include <deque>
#include <functional>
#include <memory>
#include <cstdint>
#include <condition_variable>
#include <algorithm>
#include <thread>

// --------------- utilities defined in main.cpp (linkage opened for the split) ---------------
extern thread_local const char* t_threadTag;
double nowSec();
void crashLog(const std::string& line);
void editLog(const std::string& line);
std::string baseName(const std::string& p);
std::wstring utf8ToWide(const std::string& s);

// --------------- constants ---------------
inline constexpr int    kSpb = 64;
inline constexpr int    kPeakRate = 48000;
inline constexpr double kBinsPerSec = (double)kPeakRate / kSpb;
inline constexpr int    kMaxStuckAttempts = 8;
inline constexpr double kDecodeWindowTimeoutSec = 15.0;

// --------------- Peaks ---------------
// BPK4 (2026-07-25): full 16-bit min/max peaks. The old BPK3 format truncated
// samples to 8 bits (>> 8), destroying everything below -48dBFS - quiet speech
// like the word "I" vanished from the waveform display and silence detection.
// 16-bit gives full 96dB dynamic range; cache size doubles (~1.5KB/sec) but is
// still trivial even for multi-hour sources.
struct Peaks {
    std::mutex mx;
    std::vector<int16_t> n0, x0, n1, x1, n2, x2;
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

// --------------- function declarations (needed by BgWorkPool::loop) ---------------
std::shared_ptr<Peaks> peaksGet(const std::string& source);
bool peaksProcessBatch(std::shared_ptr<Peaks> P);

// ===== I-8 / §3.4 P3: Bounded background worker pool =====
// All peaks decode (ffmpeg audio -> .bpk min/max) runs through a fixed pool
// of N = max(1, physical_cores / 2) threads in Windows BACKGROUND processing
// mode. This prevents the FB9 failure mode: 100+ concurrent decode threads
// (one per unique source file) from saturating disk I/O and stalling even the
// OS cursor during a cold folder load.
//
// Workers process pending source jobs from a shared FIFO. A source currently
// being processed is tracked so no two workers decode the same source
// simultaneously - the per-source Peaks.jobs/secFilled/inFlight dedup stays
// intact (only one thread touches a Peaks at a time).
//
// Also handles thumbnails (requestThumb) and external file probes
// (requestAddExternal) through the same pool, so those also benefit from the
// concurrency cap.
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
    BgWorkPool();
    ~BgWorkPool();
    /// Queue a source's pending peaks jobs for processing. Dedup'd: if the
    /// source is already active or queued, this is a no-op.
    void wakeSource(const std::string& s);
    /// Submit a one-shot job (thumbnail, add_external, etc.).
    void submit(std::function<void()> f);
private:
    void loop();
};

// --------------- shared globals (defined in waveform.cpp) ---------------
extern BgWorkPool* g_bgPool;
extern std::atomic<int> g_fillEpoch;
extern std::atomic<bool> g_busyHint;
extern std::atomic<uint64_t> g_peaksJobsEnqueued;
extern std::atomic<uint64_t> g_thumbJobsEnqueued;
extern std::atomic<size_t> g_trackClipCountForLog;

// --------------- public API ---------------
std::shared_ptr<Peaks> peaksEnsure(const std::string& source);
void peaksRequest(const std::string& source, double a, double b);
bool runPipeCapture(const std::string& cmd8, double deadlineSec,
                    const std::function<void(const uint8_t*, size_t)>& onData);
