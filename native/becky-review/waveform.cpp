// waveform.cpp - peaks decode pipeline (BgWorkPool, Peaks, BPK3 cache, ffmpeg decode).
// Extracted from main.cpp (P4 module split, 2026-07-25). No logic changes.
#include "waveform.h"
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <thread>

// --------------- file-local state ---------------
static std::map<std::string, std::shared_ptr<Peaks>> g_peaks;
static std::mutex g_peaksMx;
static std::mutex g_decMx; static std::condition_variable g_decCv; static int g_decActive = 0;

// --------------- shared globals ---------------
BgWorkPool* g_bgPool = nullptr;
std::atomic<int> g_fillEpoch{ 0 };
std::atomic<bool> g_busyHint{ false };
std::atomic<uint64_t> g_peaksJobsEnqueued{ 0 };
std::atomic<uint64_t> g_thumbJobsEnqueued{ 0 };
std::atomic<size_t> g_trackClipCountForLog{ 0 };

// --------------- BgWorkPool ---------------
BgWorkPool::BgWorkPool() {
    SYSTEM_INFO si; GetSystemInfo(&si);
    N = std::max(1, (int)si.dwNumberOfProcessors / 2);
    for (int i = 0; i < N; i++)
        workers.emplace_back([this]{ loop(); });
}
BgWorkPool::~BgWorkPool() {
    { std::lock_guard lk(mx); stop = true; cv.notify_all(); }
    for (auto& w : workers) if (w.joinable()) w.join();
}
void BgWorkPool::wakeSource(const std::string& s) {
    if (s.empty()) return;
    std::lock_guard lk(mx);
    if (stop || active.count(s) || pendingSet.count(s)) return;
    pending.push_back(s);
    pendingSet.insert(s);
    cv.notify_one();
}
void BgWorkPool::submit(std::function<void()> f) {
    std::lock_guard lk(mx);
    extras.push_back(std::move(f));
    cv.notify_one();
}
void BgWorkPool::loop() {
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

// --------------- internal helpers ---------------
static uint64_t fnv1a64(const std::string& s) {
    uint64_t h = 1469598103934665603ULL;
    for (unsigned char c : s) { h ^= c; h *= 1099511628211ULL; }
    return h;
}
std::wstring utf8ToWide(const std::string& s) {
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
    P.n0.assign(P.bins, 32767); P.x0.assign(P.bins, -32768);
    P.n1.assign(P.bins / 16 + 1, 32767); P.x1.assign(P.bins / 16 + 1, -32768);
    P.n2.assign(P.bins / 256 + 1, 32767); P.x2.assign(P.bins / 256 + 1, -32768);
    P.secFilled.assign((size_t)duration + 2, 0);
    P.ready = true;
}
static void pyramidRegion(Peaks& P, size_t a, size_t b) {
    if (b > P.bins) b = P.bins;
    if (b <= a) return;
    for (size_t i = a >> 4; i <= ((b - 1) >> 4) && i < P.n1.size(); i++) {
        int16_t mn = 32767, mx = -32768;
        size_t s0 = i << 4, s1 = std::min(P.bins, s0 + 16);
        for (size_t k = s0; k < s1; k++) { mn = std::min(mn, P.n0[k]); mx = std::max(mx, P.x0[k]); }
        P.n1[i] = mn; P.x1[i] = mx;
    }
    for (size_t i = a >> 8; i <= ((b - 1) >> 8) && i < P.n2.size(); i++) {
        int16_t mn = 32767, mx = -32768;
        size_t s0 = i << 4, s1 = std::min(P.n1.size(), s0 + 16);
        for (size_t k = s0; k < s1; k++) { mn = std::min(mn, P.n1[k]); mx = std::max(mx, P.x1[k]); }
        P.n2[i] = mn; P.x2[i] = mx;
    }
}

// --------------- BPK4 cache (16-bit peaks) ---------------
static bool loadPeaksCache(Peaks& P) {
    FILE* f = nullptr; fopen_s(&f, P.cachePath.c_str(), "rb");
    if (!f) return false;
    char magic[4]; uint32_t spb = 0, rate = 0; uint64_t count = 0; double dur = 0;
    bool ok = fread(magic, 1, 4, f) == 4
        && fread(&spb, 4, 1, f) == 1 && spb == (uint32_t)kSpb
        && fread(&rate, 4, 1, f) == 1 && rate == (uint32_t)kPeakRate
        && fread(&count, 8, 1, f) == 1 && count < (1ULL << 32)
        && fread(&dur, 8, 1, f) == 1 && dur > 0;
    // BPK4: full 16-bit min/max peaks. BPK3 (8-bit) caches are rejected and
    // rebuilt - the 8-bit truncation destroyed quiet speech below -48dBFS.
    bool v4 = ok && memcmp(magic, "BPK4", 4) == 0;
    if (ok && v4) {
        std::lock_guard<std::mutex> lk(P.mx);
        sizeArrays(P, dur);
        {
            uint32_t secN = 0;
            ok = fread(&secN, 4, 1, f) == 1 && secN <= P.secFilled.size();
            if (ok && secN) ok = fread(P.secFilled.data(), 1, secN, f) == secN;
        }
        if (ok) {
            std::vector<int16_t> buf((size_t)count * 2);
            ok = count == 0 || fread(buf.data(), sizeof(int16_t), buf.size(), f) == buf.size();
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
    fwrite("BPK4", 1, 4, f);
    uint32_t spb = kSpb, rate = kPeakRate; uint64_t count = P.bins; double dur = P.duration;
    uint32_t secN = (uint32_t)P.secFilled.size();
    fwrite(&spb, 4, 1, f); fwrite(&rate, 4, 1, f); fwrite(&count, 8, 1, f); fwrite(&dur, 8, 1, f);
    fwrite(&secN, 4, 1, f);
    fwrite(P.secFilled.data(), 1, secN, f);
    std::vector<int16_t> buf(P.bins * 2);
    for (size_t i = 0; i < P.bins; i++) { buf[i * 2] = P.n0[i]; buf[i * 2 + 1] = P.x0[i]; }
    fwrite(buf.data(), sizeof(int16_t), buf.size(), f);
    fclose(f);
    P.dirty = false;
}

// --------------- ffmpeg decode ---------------
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
bool runPipeCapture(const std::string& cmd8, double deadlineSec,
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
            int16_t q = s;   // BPK4: full 16-bit sample, no truncation
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

// --------------- batch processing ---------------
bool peaksProcessBatch(std::shared_ptr<Peaks> P) {
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

// --------------- public API ---------------
std::shared_ptr<Peaks> peaksGet(const std::string& source) {
    std::lock_guard<std::mutex> lk(g_peaksMx);
    auto it = g_peaks.find(source);
    return it == g_peaks.end() ? nullptr : it->second;
}
std::shared_ptr<Peaks> peaksEnsure(const std::string& source) {
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
void peaksRequest(const std::string& source, double a, double b) {
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
