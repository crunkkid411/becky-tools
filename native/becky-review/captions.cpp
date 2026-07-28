// captions.cpp - Caption track module (SRT parsing, editing, undo/redo, anchoring, OSD drawing).
// Extracted from main.cpp (P4 module split, 2026-07-25). No logic changes.
#include "captions.h"
#include "engine_seam.h"
#include "waveform.h"
#include <algorithm>
#include <cctype>
#include <cmath>
#include <cstdio>

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
double reelFps() {
    for (auto& c : g_track[0]) if (c.srcFps > 1.0) return c.srcFps;
    if (!g_track[0].empty()) return sourceFps(g_track[0][0].source);
    return 30.0;
}
double quantToFrame(double t) {
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
// A caption is BOUND TO A CLIP INSTANCE (Jordan, 2026-07-24: "captions need to
// stick with the clip when i rearrange... expand or shorten a clip, the captions
// for that clip should expand to match"). start/end are the DERIVED absolute
// compilation times used for drawing + the .srt render burn-in; clipId + srcIn/srcOut
// are the ANCHOR - the caption's span in its clip's SOURCE time. reprojectCaptions()
// recomputes start/end from the clip's current compStart/in after every timeline
// reload, so a caption follows its clip through reorder/trim/split with zero extra
// work, and a per-caption edit (drag/resize/retype/merge/split) survives the reload
// because it lives on the caption, keyed to the clip, not on a flat absolute .srt.
// CapWord is one word of a caption in SOURCE seconds. Carried so a caption SPLIT
// lands the cut between words by their REAL timing (Jordan 2026-07-24: "we KNOW the
// word level timestamps and it needs to split the captions accordingly"), not a
// character position guessed from a time fraction. Empty for a loaded .srt (no
// word timing) - the split falls back to the fraction guess there.
std::vector<Caption> g_caps;
std::vector<PendingCaptionSplit> g_pendingCaptionSplits;
// Clip ids whose captions have already been seeded (from a sidecar .srt or the
// source transcript). A clip is seeded exactly once; after that, the ABSENCE of a
// caption on it is a deliberate user removal (issue 5: "the user chooses to remove
// SOME of them - that's a creative decision") and must NOT be re-seeded.
static std::set<std::string> g_capSeededClips;
// Dormant captions: captions whose clip was deleted (PROJ_GONE). Kept so an undo
// that brings the clip back also restores its captions (instead of re-seeding from
// the transcript with word-level timing). Pruned when a new reel is loaded.
static std::vector<Caption> g_capsDormant;
// Applied caption splits: the ORIGINAL caption saved before a split divided it,
// keyed by the two clip ids the split produced. When the right clip (newId)
// disappears while the parent survives - i.e. the split was UNDONE - both halves
// are removed and the original is restored verbatim. Pruned on new reel load.
struct AppliedCaptionSplit { std::string parentId, newId; double splitSrcT; Caption original; };
static std::vector<AppliedCaptionSplit> g_appliedCapSplits;
std::string g_capPath;        // the .srt on disk; "" = no reel loaded, lane hidden
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
std::string g_capErr;         // plain-language load/save problem, shown in the lane
int  g_capSel = -1;           // selected caption (white border)
int  g_capEdit = -1;          // caption whose text is being typed, -1 = none
char g_capEditBuf[1024] = { 0 };
bool g_capEditFocus = false;  // one-shot: put the keyboard in the box next frame
bool g_capEditSnapped = false; // an undo snapshot was taken for THIS typing session (issue 7)

// ---- caption <-> clip anchoring (issues 1-5) --------------------------------
static Clip* clipById(const std::string& id) {
    if (id.empty()) return nullptr;
    for (auto& c : g_track[0]) if (c.id == id) return &c;
    return nullptr;
}
enum ProjResult { PROJ_VISIBLE, PROJ_HIDDEN, PROJ_GONE };
// projectCap recomputes a caption's absolute start/end from its clip's CURRENT
// compStart+trim. VISIBLE = its source span overlaps the clip; HIDDEN = a trim
// pushed the whole span outside [in,out] (kept, drawn as zero-width, restored when
// the clip is re-lengthened - non-destructive); GONE = the clip was deleted.
static ProjResult projectCap(Caption& cap) {
    Clip* c = clipById(cap.clipId);
    if (!c) return PROJ_GONE;
    double a = std::max(cap.srcIn, c->in), b = std::min(cap.srcOut, c->out);
    if (b <= a) { cap.start = cap.end = c->compStart; return PROJ_HIDDEN; }
    cap.start = c->compStart + (a - c->in);
    cap.end   = c->compStart + (b - c->in);
    return PROJ_VISIBLE;
}
// rebindCapToCoveringClip follows a caption to whatever CURRENT clip (same source)
// now holds its source span, when its own bound clip no longer does. After a clip
// SPLIT the right half becomes a NEW clip: a caption bound to the original clip whose
// span moved into that half would otherwise project HIDDEN (zero-width) - it neither
// shows nor follows, which read as "the right-side captions vanished" and wrote
// stacked zero-duration cues to the .srt (Jordan 2026-07-24). Re-binding makes it
// follow the split exactly like the clips do. Returns true if it moved.
static bool rebindCapToCoveringClip(Caption& c) {
    Clip* orig = clipById(c.clipId);
    if (!orig) return false;
    double mid = (c.srcIn + c.srcOut) * 0.5;
    for (auto& cl : g_track[0]) {
        if (cl.id != c.clipId && cl.source == orig->source && mid >= cl.in && mid < cl.out) {
            c.clipId = cl.id;
            return true;
        }
    }
    return false;
}
// capNormalize mirrors internal/subs.normalize: only ? and ! survive as punctuation
// (. , ; : are dropped), whitespace collapses, lowercased.
static std::string capNormalize(const std::string& in) {
    std::string s; s.reserve(in.size());
    bool sp = false;
    for (char c : in) {
        if (c == '.' || c == ',' || c == ';' || c == ':') continue;
        if (c == ' ' || c == '\t' || c == '\n' || c == '\r') { if (!s.empty()) sp = true; continue; }
        if (sp) { s += ' '; sp = false; }
        s += (char)std::tolower((unsigned char)c);
    }
    return s;
}
static std::string joinWords(const std::vector<CapWord>& ws) {
    std::string j;
    for (auto& w : ws) { if (!j.empty()) j += " "; j += w.word; }
    return j;
}
// captionForClipWindow divides a cue to the words that fall inside [clip.in, clip.out]
// (source time) and rebuilds its text, so a cue spanning a cut becomes the correct
// HALF on each clip instead of the whole cue duplicated onto both (Jordan 2026-07-24:
// "it just duplicated so both clips have the same caption"). Returns false when the
// cue has no per-word timing (an official .srt) - the caller keeps the old whole-cue-
// clipped behaviour then. out.text is empty when this clip holds none of the words.
static bool captionForClipWindow(const Caption& cue, const Clip& clip, Caption& out) {
    if (cue.words.empty()) return false;
    out = Caption{};
    out.clipId = clip.id;
    for (auto& wd : cue.words)
        if (wd.end > clip.in && wd.start < clip.out) out.words.push_back(wd);
    if (out.words.empty()) return true;
    out.srcIn = out.words.front().start;
    out.srcOut = out.words.back().end;
    out.text = capNormalize(joinWords(out.words));
    return true;
}
// reanchorCap derives a caption's SOURCE span from its CURRENT absolute start/end
// against the clip it now sits on - called after a drag/resize so the edit survives
// the next reload. Anchors to the clip under the caption's MIDPOINT (a caption
// dragged mostly onto a new clip becomes that clip's).
void reanchorCap(Caption& cap) {
    Clip* c = clipAtComp(0, (cap.start + cap.end) * 0.5);
    if (!c) c = clipById(cap.clipId);
    if (!c) return;
    cap.clipId = c->id;
    cap.srcIn  = c->in + (cap.start - c->compStart);
    cap.srcOut = c->in + (cap.end   - c->compStart);
    if (cap.srcOut < cap.srcIn) std::swap(cap.srcIn, cap.srcOut);
}

// capTokenCount counts the space-separated words in a normalized caption line; used
// to confirm the carried word timings still line up with the text before trusting
// them for a word-aware split.
size_t capTokenCount(const std::string& s) {
    size_t n = 0; bool in = false;
    for (char c : s) { if (c == ' ') in = false; else if (!in) { in = true; n++; } }
    return n;
}
// nthSpaceIndex returns the char index of the k-th space (1-based). Splitting the
// text there puts exactly k words on the left. npos if there are fewer than k.
size_t nthSpaceIndex(const std::string& s, int k) {
    int seen = 0;
    for (size_t i = 0; i < s.size(); i++) if (s[i] == ' ' && ++seen == k) return i;
    return std::string::npos;
}

// ---- caption undo/redo (issue 7) --------------------------------------------
// Native-only stack (see the forward-decl comment at queueUndo). A snapshot of the
// WHOLE lane is taken before every caption edit; Ctrl+Z restores the newest one.
struct CapSnapshot { std::vector<Caption> caps; std::set<std::string> seeded; double at = 0; };
static std::vector<CapSnapshot> g_capUndo, g_capRedo;
static bool g_lastUndoWasCaption = false;
static const size_t kCapUndoMax = 200;
void pushCapUndo() {
    CapSnapshot s; s.caps = g_caps; s.seeded = g_capSeededClips; s.at = nowSec();
    g_capUndo.push_back(std::move(s));
    if (g_capUndo.size() > kCapUndoMax) g_capUndo.erase(g_capUndo.begin());
    g_capRedo.clear();
}
bool captionTryUndo() {
    if (g_capUndo.empty()) return false;
    // Only claim the undo when the caption edit is at least as recent as the last
    // CLIP edit; otherwise fall through so the engine undoes its clip edit first.
    if (g_capUndo.back().at < g_lastEngineEditAt) return false;
    CapSnapshot cur; cur.caps = g_caps; cur.seeded = g_capSeededClips; cur.at = nowSec();
    g_capRedo.push_back(std::move(cur));
    CapSnapshot s = std::move(g_capUndo.back()); g_capUndo.pop_back();
    g_caps = std::move(s.caps); g_capSeededClips = std::move(s.seeded);
    g_capSel = -1; g_capEdit = -1; g_capEditFocus = false;
    saveCaptions();
    g_lastUndoWasCaption = true;
    editLog("CAP undo");
    return true;
}
bool captionTryRedo() {
    // Redo pairs with the most recent undo: only redo a caption if the last undo
    // was a caption undo, so Ctrl+Y after a clip undo still redoes the clip.
    if (!g_lastUndoWasCaption || g_capRedo.empty()) return false;
    CapSnapshot cur; cur.caps = g_caps; cur.seeded = g_capSeededClips; cur.at = nowSec();
    g_capUndo.push_back(std::move(cur));
    CapSnapshot s = std::move(g_capRedo.back()); g_capRedo.pop_back();
    g_caps = std::move(s.caps); g_capSeededClips = std::move(s.seeded);
    g_capSel = -1; g_capEdit = -1; g_capEditFocus = false;
    saveCaptions();
    editLog("CAP redo");
    return true;
}

// ONE vertical placement for the whole reel - Jordan: "Simply dragging a caption up
// or down should affect all captions vertical placement. horzontal placement is fine
// how it is (centered)". So: no per-caption position, and no horizontal control at all.
//
// The number is becky-subtitle's MarginV (internal/subs/style.go) - the distance up
// from the bottom edge, in the 384x288 canvas ffmpeg's SRT-to-ASS conversion uses.
// 90 of 288 is the shipped default, i.e. about 30% up from the bottom.
const int  CAP_ASS_H = 288;         // ff_ass_subtitle_header_default PlayResY
const int  CAP_ASS_W = 384;         // ...and PlayResX
int    g_capMarginV = 90;           // subs.DefaultStyle().MarginV
bool   g_capMarginDrag = false;     // a vertical drag is live over the video pane
int    g_capMarginAtGrab = 90;
double g_capMarginGrabY = 0;
double g_capMarginUnitsPerPx = 1.0; // screen pixels -> MarginV units, set at grab

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
// anchorLoadedCaptions binds each just-parsed flat .srt cue to the clip under it and
// records its source-time span, so a hand-edited / becky-subtitle caption FOLLOWS
// its clip through later reorder/trim/split (issues 1-2). It then marks every clip
// seeded: the sidecar is the COMPLETE caption set, so refresh must not also add raw
// transcript captions on clips the sidecar happened to leave blank.
static void anchorLoadedCaptions() {
    for (auto& cap : g_caps) {
        reanchorCap(cap);
        // Midpoint fell in a gap / a hair past a clip edge: bind by largest time
        // OVERLAP instead, so no sidecar cue is ever left unanchored (an unbound
        // cue cannot follow its clip through a reorder - it reads as deleted).
        if (cap.clipId.empty()) {
            Clip* best = nullptr; double bestOv = 0;
            for (auto& cl : g_track[0]) {
                double d = cl.out - cl.in;
                double ov = std::min(cap.end, cl.compStart + d) - std::max(cap.start, cl.compStart);
                if (ov > bestOv) { bestOv = ov; best = &cl; }
            }
            if (best) {
                cap.clipId = best->id;
                cap.srcIn  = best->in + std::max(0.0, cap.start - best->compStart);
                cap.srcOut = best->in + std::min(best->out - best->in, cap.end - best->compStart);
                if (cap.srcOut <= cap.srcIn) cap.srcOut = cap.srcIn + 0.5;
            } else {
                editLog("CAP anchor MISS (no clip overlaps) '" + cap.text.substr(0, 24) + "'");
            }
        }
    }
    for (auto& c : g_track[0]) g_capSeededClips.insert(c.id);
}
void loadCaptions(const std::string& reelPath) {
    g_caps.clear(); g_capErr.clear(); g_capPath.clear();
    g_capSel = -1; g_capEdit = -1; g_capEditFocus = false;
    g_capSidecar = false;
    // New reel: forget the previous reel's seeded clips + caption undo history + dormant.
    g_capSeededClips.clear(); g_capUndo.clear(); g_capRedo.clear(); g_lastUndoWasCaption = false;
    g_capsDormant.clear();
    g_appliedCapSplits.clear();
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
        // No sidecar yet: derive the lane from each clip's own source transcript -
        // now via the caption_chunks verb, which runs the SAME pace-based chunker
        // becky-subtitle uses (pause-driven, 22-char only as a last resort, no gaps),
        // so an un-captioned clip shows proper TikTok captions, not raw transcript.
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
    if (g_capSidecar) anchorLoadedCaptions();   // bind the good captions to their clips
    else rebuildDerivedCaptions();
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
std::atomic<bool> g_cliCutBusy{ false };
// Item 27: set when the video-row "Get Captions" put a whole video on the timeline and now
// wants captions built for it - consumed once the add_external reply lands (drainAsync).
bool g_getCaptionsAfterAdd = false;
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
            // --review=false: the LLM regroup pass is OFF (Jordan 2026-07-24 "pause the
            // llm step"). The deterministic pace chunker now honors every rule on his real
            // footage - breaks at ?/! (even across cuts), matches the speaker's pauses,
            // keeps phrases whole, a HARD 22-char cap, single-word lines allowed - verified
            // 0 mid-line ?/! and 0 over-22 lines on 27_5_millionaires. The model was
            // regrouping by meaning and re-introducing the exact defects he kept flagging;
            // deterministic is faster, free, and correct. Re-enable only if he asks.
            std::string cmd = "\"" + exe + "\" --reel \"" + reelPath + "\" --review=false --out \"" + srtOut + "\"";
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
// reelPathForVideo is the STABLE, per-video name of a video's saved auto-cut reel:
// "<video without extension>.reel.json", right beside the source. The robot
// pipeline saves there and the library card looks there, so "pull up the cut I
// already made" is one click and no re-analysis (Jordan 2026-07-24).
std::string reelPathForVideo(const std::string& videoPath) {
    size_t dot = videoPath.find_last_of('.'), slash = videoPath.find_last_of("/\\");
    if (dot != std::string::npos && (slash == std::string::npos || dot > slash))
        return videoPath.substr(0, dot) + ".reel.json";
    return videoPath + ".reel.json";
}

void triggerGetCaptions() {
    if (g_cliCutBusy.load() || g_track[0].empty()) return;
    g_cliCutBusy.store(true);
    // When the WHOLE timeline is one video's cut (the robot / get-captions case), save
    // the reel BESIDE that video under its stable per-video name, so the card can find
    // and reload it later. A mixed reel keeps the engine's default location.
    std::string savePath;
    {
        std::string src = g_track[0][0].source; bool single = !src.empty();
        for (auto& c : g_track[0]) if (c.source != src) { single = false; break; }
        if (single) savePath = reelPathForVideo(src);
    }
    engineCallAsync("save_reel", { {"path", savePath} }, 20.0, "Saving reel for captions...", [](const json& r) {
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
void saveCapStyle() {
    std::string p = capStylePath();
    if (p.empty()) return;
    std::ofstream f(p, std::ios::binary | std::ios::trunc);
    if (!f.good()) { g_capErr = "could not save caption placement to " + p; return; }
    f << "{\"margin_v\": " << g_capMarginV << "}\n";
}

// saveCaptions rewrites the whole .srt in time order after any edit. SRT is
// conventionally time-ordered and a drag can reorder cues, so it sorts - and then
// repairs g_capSel so the white border stays on the caption the user is holding.
void saveCaptions() {
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
// rebuildDerivedCaptions is the ONE post-reload caption refresh (kept its old name
// so every caller - loadTimelineView, applyAddClipDelta, seekToSpan, the transcript
// arrival - stays wired). It no longer CLEARS + re-derives from scratch (that threw
// away every per-caption edit and never followed a sidecar's captions at all).
// Instead it REPROJECTS each caption through its anchored clip (so it follows a
// reorder/trim/split) and SEEDS a clip's captions from its source transcript exactly
// ONCE - after that, an empty clip is a deliberate removal, not a re-seed target.
void rebuildDerivedCaptions() {
    // Split-undo restore: a recorded split was UNDONE when its RIGHT clip (newId)
    // vanished AND the parent re-extended past the split point (undo rejoins the
    // halves). A manual DELETE of the right half also removes newId, but the parent
    // still ENDS at the cut - that is an edit, not an undo, and must NOT restore
    // (found live: it resurrected the pre-split caption and orphaned every chunk
    // after the cut as zero-width ghosts). Remove both halves and put the ORIGINAL
    // caption back exactly as it was before the split.
    if (!g_appliedCapSplits.empty()) {
        for (auto it = g_appliedCapSplits.begin(); it != g_appliedCapSplits.end();) {
            Clip* pc = clipById(it->parentId);
            bool newAlive = clipById(it->newId) != nullptr;
            bool parentCovers = pc && pc->out > it->splitSrcT + 0.001;   // undone: parent spans the cut again
            if (pc && !newAlive && parentCovers) {
                const double eps = 1e-6;
                // Drop both halves: they sit INSIDE the original caption's source
                // span, on either of the two ids. Neighbour captions that merely
                // REBOUND to newId after the clip split (their span is outside the
                // original's) are NOT halves - rebind them back to the parent.
                g_caps.erase(std::remove_if(g_caps.begin(), g_caps.end(), [&](const Caption& c) {
                    return (c.clipId == it->newId || c.clipId == it->parentId) &&
                           c.srcIn >= it->original.srcIn - eps && c.srcOut <= it->original.srcOut + eps;
                }), g_caps.end());
                for (auto& c : g_caps)
                    if (c.clipId == it->newId) c.clipId = it->parentId;
                // A half that already went dormant (a rebuild ran between the undo
                // and now) must not resurrect later; a rebound neighbour that went
                // dormant rebinds back to the surviving parent.
                g_capsDormant.erase(std::remove_if(g_capsDormant.begin(), g_capsDormant.end(),
                    [&](const Caption& c) { return c.clipId == it->newId &&
                        c.srcIn >= it->original.srcIn - eps && c.srcOut <= it->original.srcOut + eps; }), g_capsDormant.end());
                for (auto& c : g_capsDormant)
                    if (c.clipId == it->newId) c.clipId = it->parentId;
                g_caps.push_back(it->original);
                it = g_appliedCapSplits.erase(it);
            } else if (!pc && !newAlive) {
                it = g_appliedCapSplits.erase(it);   // whole clip gone - nothing to restore into
            } else {
                // Both alive (split still standing), or right half manually deleted
                // (parent still ends at the cut): keep the record - an undo can still
                // bring the deleted half back and, later, genuinely undo the split.
                ++it;
            }
        }
    }
    // Restore dormant captions whose clip came back (undo of a delete).
    if (!g_capsDormant.empty()) {
        std::vector<Caption> stillDormant;
        for (auto& dc : g_capsDormant) {
            if (clipById(dc.clipId)) {
                g_caps.push_back(dc);   // clip is back - re-activate
                g_capSeededClips.insert(dc.clipId);  // don't re-seed from transcript
            } else {
                stillDormant.push_back(dc);
            }
        }
        g_capsDormant.swap(stillDormant);
    }
    // Prune the seeded set to CURRENT clips + dormant clips: a deleted clip that
    // comes back via Ctrl+Z keeps its seeded status (its dormant captions restore).
    // A clip with NO dormant captions and not present is truly gone.
    if (!g_capSeededClips.empty()) {
        std::set<std::string> present;
        for (auto& c : g_track[0]) present.insert(c.id);
        for (auto& dc : g_capsDormant) present.insert(dc.clipId);  // dormant clips stay seeded
        std::set<std::string> keepSeed;
        for (auto& id : g_capSeededClips) if (present.count(id)) keepSeed.insert(id);
        g_capSeededClips.swap(keepSeed);
    }
    // 0. Process pending caption splits (from manual S-key or AI agent splits).
    // ONLY the ONE caption spanning the split point is divided, AT the split point.
    // It keeps its own outer boundaries (srcIn/srcOut) - neighbouring caption
    // chunks on the same clip are NEVER touched (Jordan: "the cut point should be
    // where the captions are split, and the only other thing affected should be
    // which word appears on each side of the cut"). Word timestamps decide word
    // assignment only; they never re-time the caption edges.
    if (!g_pendingCaptionSplits.empty()) {
        for (auto& ps : g_pendingCaptionSplits) {
            Clip* leftClip = clipById(ps.parentId);
            Clip* rightClip = clipById(ps.newId);
            if (!leftClip || !rightClip) continue;
            // Find the caption on the parent (left) clip that spans the split point.
            for (auto& cap : g_caps) {
                if (cap.clipId != ps.parentId) continue;
                if (cap.srcOut <= ps.splitSrcT || cap.srcIn >= ps.splitSrcT) continue;
                // Save the pre-split original so an undo can restore it verbatim.
                g_appliedCapSplits.push_back({ ps.parentId, ps.newId, ps.splitSrcT, cap });
                if (g_appliedCapSplits.size() > 200) g_appliedCapSplits.erase(g_appliedCapSplits.begin());
                const double origSrcIn = cap.srcIn, origSrcOut = cap.srcOut;
                std::string leftText, rightText;
                if (!cap.words.empty()) {
                    // Divide words at the split point (word midpoint determines side).
                    std::vector<CapWord> leftWords, rightWords;
                    for (auto& wd : cap.words) {
                        double mid = (wd.start + wd.end) * 0.5;
                        if (mid < ps.splitSrcT) leftWords.push_back(wd);
                        else rightWords.push_back(wd);
                    }
                    leftText  = leftWords.empty()  ? std::string() : capNormalize(joinWords(leftWords));
                    rightText = rightWords.empty() ? std::string() : capNormalize(joinWords(rightWords));
                } else {
                    // No word timing (sidecar .srt): divide text by time fraction.
                    double dur = origSrcOut - origSrcIn;
                    double frac = (dur > 0) ? (ps.splitSrcT - origSrcIn) / dur : 0.5;
                    std::vector<std::string> toks;
                    { std::string tok;
                      for (char ch : cap.text) {
                          if (ch == ' ' || ch == '\n' || ch == '\r') { if (!tok.empty()) { toks.push_back(tok); tok.clear(); } }
                          else tok += ch;
                      }
                      if (!tok.empty()) toks.push_back(tok); }
                    int leftN = (int)(toks.size() * frac + 0.5);
                    if (leftN < 0) leftN = 0;
                    if (leftN > (int)toks.size()) leftN = (int)toks.size();
                    for (int ti = 0; ti < (int)toks.size(); ti++) {
                        if (ti < leftN) { if (!leftText.empty()) leftText += " "; leftText += toks[ti]; }
                        else { if (!rightText.empty()) rightText += " "; rightText += toks[ti]; }
                    }
                }
                if (rightText.empty()) {
                    // All words are left of the cut: caption stays on the parent,
                    // clipped to end AT the cut. Nothing lands on the right clip.
                    cap.srcOut = ps.splitSrcT;
                    cap.words.clear();
                } else if (leftText.empty()) {
                    // All words are right of the cut: the whole caption MOVES to the
                    // right clip, starting AT the cut.
                    cap.clipId = ps.newId;
                    cap.srcIn = ps.splitSrcT;
                    cap.words.clear();
                } else {
                    // Words on both sides: left half keeps its own srcIn, ends at the
                    // cut; right half starts at the cut, keeps the original srcOut.
                    cap.text = leftText;
                    cap.srcOut = ps.splitSrcT;
                    cap.words.clear();
                    Caption rc;
                    rc.clipId = ps.newId;
                    rc.text = rightText;
                    rc.srcIn = ps.splitSrcT;
                    rc.srcOut = origSrcOut;
                    g_caps.push_back(rc);
                }
                g_capSeededClips.insert(ps.newId);   // don't re-seed from transcript
                break;   // only one caption per split point
            }
        }
        g_pendingCaptionSplits.clear();
    }
    // 1. Reproject existing captions; drop only those whose clip was deleted.
    std::vector<Caption> kept;
    kept.reserve(g_caps.size());
    for (auto& cap : g_caps) {
        if (cap.clipId.empty()) {
            // Unanchored cue (sidecar anchoring missed it - its midpoint fell in a
            // gap or a hair past a clip edge at load time). LATE-ANCHOR it now by
            // its absolute midpoint so it follows drags like every other caption,
            // instead of being stranded "as drawn" forever (found live: reorder
            // moved the clip, the stranded cues stayed behind and read as deleted).
            Caption c = cap;
            reanchorCap(c);
            if (c.clipId.empty()) { kept.push_back(cap); continue; }  // still nothing under it: leave as drawn
            editLog("CAP late-anchor '" + c.text.substr(0, 24) + "' -> clip " + c.clipId);
            if (projectCap(c) != PROJ_GONE) kept.push_back(c);
            else { g_capsDormant.push_back(c); editLog("CAP -> dormant (late-anchor) '" + c.text.substr(0, 24) + "'"); }
            continue;
        }
        Caption c = cap;
        // Word-level split: ONLY when a clip split lands INSIDE a caption's word
        // span AND the caption is still derived (words match text). The gate protects
        // manually-adjusted captions (whose text no longer matches the raw word join)
        // from being re-windowed on every rebuild - those were tuned by hand to fill
        // every frame, and the 6-month-dialed auto-cut cadence is ground truth.
        // The exception (splitting at the word boundary) is handled separately below
        // via g_pendingCaptionSplits - only the ONE caption at a fresh split point
        // gets divided, and both halves fill their clip entirely.
        Clip* bnd = clipById(c.clipId);
        if (bnd && !c.words.empty() && capNormalize(joinWords(c.words)) == c.text) {
            bool spans = false;
            for (auto& wd : c.words)
                if (wd.start < bnd->in || wd.end > bnd->out) { spans = true; break; }
            if (spans) {
                Caption d;
                if (captionForClipWindow(c, *bnd, d) && !d.text.empty()) c = d;
            }
        }
        ProjResult pr = projectCap(c);
        if (pr == PROJ_HIDDEN && rebindCapToCoveringClip(c)) {
            pr = projectCap(c);                                  // follow a clip split into its new half
            if (pr == PROJ_VISIBLE) g_capSeededClips.insert(c.clipId);  // it holds captions now - don't ALSO re-seed this clip from the transcript (that was the duplicate cues)
        }
        if (pr != PROJ_GONE) kept.push_back(c);
        else {
            g_capsDormant.push_back(c);   // clip deleted: keep dormant for undo restore
            editLog("CAP -> dormant (clip " + c.clipId + " gone) '" + c.text.substr(0, 24) + "'");
        }
    }
    // 2. Seed captions for clips not yet seeded, from their source transcript.
    bool waiting = false;
    for (auto& clip : g_track[0]) {
        if (g_capSeededClips.count(clip.id)) continue;
        std::string name = baseName(clip.source);
        auto it = g_srcCues.find(name);
        if (it == g_srcCues.end()) {
            if (!g_srcCuesInFlight.count(name)) {
                g_srcCuesInFlight.insert(name);
                // caption_chunks, NOT transcript: the pace-based (pause-driven) chunker
                // becky-subtitle uses - 22 chars only as a last resort, phrases kept
                // whole, no gaps. So an un-captioned clip shows proper TikTok captions
                // instead of the raw Parakeet transcript (long lines + speech gaps).
                engineCallAsync("caption_chunks", { {"name", name} }, 25.0, "loading captions",
                    [name](const json& r) {
                        g_srcCuesInFlight.erase(name);
                        if (!r.value("ok", false)) {
                            // NOT cached: usually boot ordering (the forensic launcher
                            // loads the reel before open_folder indexes the folder).
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
                                Caption cp; cp.srcIn = q.value("start", 0.0); cp.srcOut = q.value("end", 0.0);
                                cp.text = q.value("text", std::string());
                                if (q.contains("words") && q["words"].is_array())
                                    for (auto& wj : q["words"])
                                        cp.words.push_back({ wj.value("word", std::string()), wj.value("start", 0.0), wj.value("end", 0.0) });
                                if (cp.srcOut > cp.srcIn && !cp.text.empty()) cues.push_back(cp);
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
            // q.srcIn/q.srcOut are the cue's SOURCE times (from the transcript).
            if (q.srcOut <= clip.in || q.srcIn >= clip.out) continue;   // outside this clip's window
            Caption cp;
            if (captionForClipWindow(q, clip, cp)) {
                if (cp.text.empty()) continue;        // this clip holds none of the cue's words
            } else {                                   // no per-word timing (official .srt): whole cue, clipped
                cp.clipId = clip.id;
                cp.srcIn  = std::max(q.srcIn, clip.in);
                cp.srcOut = std::min(q.srcOut, clip.out);
                cp.text   = q.text;
            }
            if (projectCap(cp) == PROJ_VISIBLE) kept.push_back(cp);
        }
        g_capSeededClips.insert(clip.id);
    }
    g_caps = std::move(kept);
    std::sort(g_caps.begin(), g_caps.end(),
              [](const Caption& a, const Caption& b) { return a.start < b.start; });
    // Energy alignment pass (2026-07-25): snap each caption's start/end boundaries
    // to the nearest audio energy onset/offset in the BPK3 waveform data. ASR
    // timestamps are typically 200-500ms off; energy onset detection pinpoints
    // speech start within 10-20ms. This is the "second pass" that makes captions
    // frame-accurate to the actual audio rather than the ASR's approximation.
    // Only adjusts when BPK3 data is available AND the correction is within 300ms
    // (larger corrections likely mean the ASR boundary is correct and the energy
    // edge belongs to a different utterance).
    // GATED on !g_capSidecar: sidecar captions were already snapped to cut points
    // by becky-subtitle - energy alignment would shift them AWAY from the cuts
    // (the cut IS the ground truth, not the audio onset).
    if (!g_capSidecar) {
        const double maxShift = 0.300;   // max correction window (300ms)
        const double onsetThr = 0.08;    // energy must rise above this to count as onset
        for (auto& cap : g_caps) {
            if (cap.clipId.empty()) continue;
            Clip* bnd = clipById(cap.clipId);
            if (!bnd) continue;
            auto pk = peaksGet(bnd->source);
            if (!pk || !pk->ready || pk->failed || pk->bins == 0) continue;
            std::lock_guard<std::mutex> lk(pk->mx);
            double bps = kBinsPerSec;   // 750.0

            // Find energy onset nearest to srcIn (search backward for the rising edge).
            {
                long long centerBin = (long long)((cap.srcIn + pk->avSkew) * bps);
                long long searchBins = (long long)(maxShift * bps);  // ±300ms in bins
                long long bestBin = centerBin;
                double bestDist = maxShift + 1;
                for (long long b = centerBin - searchBins; b <= centerBin + searchBins / 2; b++) {
                    if (b < 1 || b >= (long long)pk->x0.size()) continue;
                    // Rising edge: previous bin quiet, this bin loud.
                    float prev = std::max(std::abs((float)pk->n0[b-1]), std::abs((float)pk->x0[b-1])) / 32767.0f;
                    float cur  = std::max(std::abs((float)pk->n0[b]),   std::abs((float)pk->x0[b]))   / 32767.0f;
                    if (prev < onsetThr && cur >= onsetThr) {
                        double binSec = (double)b / bps - pk->avSkew;
                        double dist = std::abs(binSec - cap.srcIn);
                        if (dist < bestDist) { bestDist = dist; bestBin = b; }
                    }
                }
                if (bestDist <= maxShift) {
                    double newSrcIn = (double)bestBin / bps - pk->avSkew;
                    // Snap to frame grid (29.97fps).
                    double fps = reelFps();
                    if (fps > 1.0) newSrcIn = std::floor(newSrcIn * fps) / fps;  // snap LEFT (earlier)
                    double delta = newSrcIn - cap.srcIn;
                    cap.srcIn = newSrcIn;
                    cap.start += delta;   // shift composition time by the same amount
                }
            }
            // Find energy offset nearest to srcOut (search forward for the falling edge).
            {
                long long centerBin = (long long)((cap.srcOut + pk->avSkew) * bps);
                long long searchBins = (long long)(maxShift * bps);
                long long bestBin = centerBin;
                double bestDist = maxShift + 1;
                for (long long b = centerBin - searchBins / 2; b <= centerBin + searchBins; b++) {
                    if (b < 1 || b >= (long long)pk->x0.size()) continue;
                    // Falling edge: this bin loud, next bin quiet.
                    float cur  = std::max(std::abs((float)pk->n0[b]),   std::abs((float)pk->x0[b]))   / 32767.0f;
                    float next = std::max(std::abs((float)pk->n0[b+1]), std::abs((float)pk->x0[b+1])) / 32767.0f;
                    if (cur >= onsetThr && next < onsetThr) {
                        double binSec = (double)(b + 1) / bps - pk->avSkew;
                        double dist = std::abs(binSec - cap.srcOut);
                        if (dist < bestDist) { bestDist = dist; bestBin = b + 1; }
                    }
                }
                if (bestDist <= maxShift) {
                    double newSrcOut = (double)bestBin / bps - pk->avSkew;
                    // Snap to frame grid (29.97fps).
                    double fps = reelFps();
                    if (fps > 1.0) newSrcOut = std::ceil(newSrcOut * fps) / fps;  // snap RIGHT (later)
                    double delta = newSrcOut - cap.srcOut;
                    cap.srcOut = newSrcOut;
                    cap.end += delta;
                }
            }
        }
    }
    // Selection/edit indices point into the just-rebuilt vector; reset so a stale
    // index can't select the wrong cue after a clip edit reshuffled the lane.
    g_capSel = -1; g_capEdit = -1; g_capEditFocus = false;
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
bool g_capOsdShowing = false;
void drawCaptionsImGui(double t, ImVec2 origin, ImVec2 size) {
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
