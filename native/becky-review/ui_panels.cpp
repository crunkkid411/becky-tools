#include "ui_panels.h"
// --------------- left panel: library / search / transcript (ImGui) ---------------
// The left panel is the LIBRARY: a scrollable list of the open folder's videos
// (with transcript pairing), a search box whose hits render as structured rows
// (verbatim .srt timecode, playable clip), and a flowing single-video
// transcript view (audapolis pattern) reachable by Enter/double-click on a row.

// ---- library state ----
std::vector<VideoRow> g_videos;
std::string g_folderRoot;
int g_orphanCount = 0;
std::string g_folderErr;

// Sort mode for the library list (B-3): 0=date-newest,1=date-oldest,2=name-AZ,3=name-ZA
int g_sortMode = 0;
// "Transcribe all" is in flight (UI thread only: set on click, cleared in the
// drainAsync callback, which also runs on the UI thread).
bool g_transcribeAllBusy = false;
// The library row whose right-click menu is open. The list is clipped, so
// without this the menu vanishes the moment its row scrolls off screen.
int g_libCtxIdx = -1;
// ONE selection model (B-4): a single selected index shared by mouse + arrows.
int g_libSel = -1;
bool g_libScrollPending = false;   // keyboard nav just moved g_libSel; scroll it into view
bool g_libFocused = false;        // library window (or a child) had focus last frame
int g_libJustViewedIdx = -1;      // green outline for the just-viewed video (B-6)

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
std::mutex g_transcribeMx;
std::set<std::string> g_transcribeInFlight; // video paths currently transcribing
std::deque<TranscribeDone> g_transcribeDoneQ;
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
void requestTranscribe(const std::string& path, const std::string& baseName) {
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
std::mutex g_addExtMx;
std::deque<AddExternalDone> g_addExtDoneQ;
void requestAddExternal(const std::string& path, int at) {
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
char g_searchBuf[256] = { 0 };
// Checklist 20: qmd is a persistent TOGGLE (the reference's "smart" pill), not a
// second submit button - so Enter always runs the mode he can see is armed.
bool g_smartSearch = false;
std::vector<Hit> g_hits;
// Which hit row the keyboard is on (items 68/76). Mirrors g_libSel for the video
// rows, including the "scroll it into view after an arrow key" flag.
int g_hitSel = -1;
bool g_hitScrollPending = false;
// Items 18/19: the third sort, "most relevant results at top". The engine already
// returns a score on every hit and sorts by date, so this is purely a client-side
// re-sort. Sticky across searches - having to re-assert it every query would make
// it useless.
bool g_hitRelevance = false;
void applyHitSort() {
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
std::string g_searchMode;         // "" | "keyword" | "qmd"
std::string g_searchNote;         // qmd note / degradation note
std::string g_searchErr;
bool g_searching = false;          // C-5 "Searching..." state

// I-* fix (found live this session via the frame-trace CSV, BECKY_REVIEW_FRAME_TRACE):
// runSearch() used to call engineCall("search"/"qmd_search", ...) directly on the UI
// thread. Against the real corpus (10,000 quotes for a common word like "the") that
// round trip took over FIVE SECONDS - a single frame's "dt" spiked to 5131ms in the
// trace, a dead, unresponsive window for that whole span (Present() never runs while
// blocked inside engineCall). Every edit (S/Del/O/I/Z) was already made async via
// editWorker/g_editQ (see A-4) specifically to avoid this; search never got the same
// treatment. Fixed the same way: search runs on its own worker thread; the UI thread
// only ever touches g_searchPending/g_searchDone under their own small mutex.
std::deque<SearchReq> g_searchQ;
std::mutex g_searchQMx; std::condition_variable g_searchQCv;
bool g_searchQuit = false;
std::mutex g_searchDoneMx;
bool g_searchDonePending = false;
SearchDone g_searchDoneResult;

// ---- transcript view (B-8) ----
std::vector<CueRow> g_cues;
std::string g_cueName;             // which video's transcript is open
std::string g_cueErr;
// Item 5: a selected-cue state (visibly highlighted) and Up/Down keyboard nav
// through the open transcript, mirroring g_hitSel/g_hitScrollPending exactly.
int g_cueSel = -1;
// Items 10/11: Ctrl/Shift multi-selection of transcript quotes. g_cueMulti holds the
// selected cue indices (a std::set, so it iterates in ascending order); g_cueAnchor is the
// shift-range pivot. Empty = plain single-select (g_cueSel) with its audition-on-click.
std::set<int> g_cueMulti;
int g_cueAnchor = -1;
bool g_cueScrollPending = false;
char g_withinBuf[128] = { 0 };     // search-within-this-transcript
std::string g_withinLast;          // last frame's search text, to fire the
                                           // auto-scroll-to-first-match only on change
int g_withinMatchIdx = 0;          // index of the currently-focused match (0-based)
int g_withinMatchCount = 0;        // total number of matching cues for the current term
// case-insensitive "find" - a real word processor's search never makes you match
// the ASR's exact capitalization to find a word you know is in the transcript.
bool ciContains(const std::string& hay, const std::string& needle) {
    auto it = std::search(hay.begin(), hay.end(), needle.begin(), needle.end(),
        [](unsigned char a, unsigned char b) { return std::tolower(a) == std::tolower(b); });
    return it != hay.end();
}

// ---- Q&A cards (G-1) ----
std::vector<QACard> g_cards;
std::string g_cardsErr;
std::string g_askAnswer;           // last ask-becky reply (G-3)
// H-6: a mutating "ask" turn returns a Proposal (id + preview + diff), not a
// direct edit - nothing lands on the timeline until the human hits Apply.
// This is the small inline card the adversarial review found missing: without
// it apply_edit_batch (H-4) and applyActions' one-undo-span fix (H-6 Go side)
// were unreachable from the chat - "ask" just dumped JSON text and threw the
// proposal away. G-3 rules out a heavy dialog ("no apply/reject friction
// wall"), so this stays two small buttons inline, never a modal.
std::string g_proposalID;
std::string g_proposalPreview;
std::string g_proposalNote;
json g_proposalDiff = json::array();  // Proposal.Preview: []{label,before,after}
bool g_proposalPending = false;
// palette assignment for cards (G-4), persistent by id
std::map<std::string, uint32_t> g_cardColor;
const uint32_t kPalette[8] = {
    IM_COL32(0x14,0xFF,0x39,255), IM_COL32(0x00,0xAE,0xEF,255), IM_COL32(0xDC,0x14,0x3C,255),
    IM_COL32(0x8A,0x2B,0xE2,255), IM_COL32(0xFF,0x57,0xD1,255), IM_COL32(0xFF,0xD7,0x00,255),
    IM_COL32(0x16,0xF0,0xEA,255), IM_COL32(0xFF,0x8C,0x00,255),
};
uint32_t cardColorFor(const std::string& id) {
    auto it = g_cardColor.find(id);
    if (it != g_cardColor.end()) return it->second;
    uint32_t c = kPalette[(g_cardColor.size()) % 8];
    g_cardColor[id] = c; return c;
}

// ---- ask-becky panel state (matches gui/BeckyReviewNative ui/index.html .chat) ----
char g_askBuf[512] = { 0 };   // was a function-static inside the frame; the chips
                                     // and the Q&A cards both need to write it, so it is
                                     // file scope now.
bool g_askFocus = false;      // "put the caret in the ask box next frame"
std::string g_askEcho;        // the question he last sent, echoed above the answer
std::string g_backendSummary; // engine `status` -> one plain sentence
bool g_backendOK = false;     // any backend live? drives the status card's colour
std::string g_answerCardID;   // non-empty => the ask box is answering THIS card
std::string g_answerCardQ;
// H-7: one forensic run at a time. The judge stage is an LLM pass that can take
// minutes; a double-click must never start two pipelines over the same folder
// (they would race on the same _forensic_hits.json / reel artifacts). Set on
// click, cleared in the completion callback (which drainAsync delivers on the
// UI thread, so a plain bool is enough - no atomics needed).
bool g_forensicBusy = false;

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
const char* kAskChipLabel[3] = { "compile my takes", "cut dead air", "lower-third on" };
const char* kAskChipPrompt[3] = {
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
void drawRobotMark(ImDrawList* d, float x, float y0, float h, ImU32 col) {
    float w = h * 0.86f, y = y0 + h * 0.20f;
    d->AddLine({ x + w * 0.5f, y }, { x + w * 0.5f, y - h * 0.14f }, col, 2.0f);
    d->AddCircleFilled({ x + w * 0.5f, y - h * 0.16f }, h * 0.08f, col);
    d->AddRect({ x, y }, { x + w, y + h * 0.62f }, col, h * 0.15f, 0, 2.0f);
    d->AddRectFilled({ x + w * 0.20f, y + h * 0.20f }, { x + w * 0.38f, y + h * 0.35f }, col, 1.5f);
    d->AddRectFilled({ x + w * 0.62f, y + h * 0.20f }, { x + w * 0.80f, y + h * 0.35f }, col, 1.5f);
    d->AddLine({ x + w * 0.28f, y + h * 0.48f }, { x + w * 0.72f, y + h * 0.48f }, col, 2.0f);
}
void askBeckyMark(float h) {
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
bool sendArrowButton(ImVec2 size) {
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
uint32_t driveColor(const std::string& path) {
    char d = path.empty() ? '?' : path[0];
    if (d >= 'a' && d <= 'z') d = (char)(d - 'a' + 'A');
    if (d < 'A' || d > 'Z') return IM_COL32(0x8A, 0x8A, 0x8A, 255);   // UNC / relative
    return kPalette[(d - 'A') % 8];
}
// Black or white ink, whichever actually READS on that chip. kPalette spans gold
// through blueviolet; one fixed ink colour is illegible on half of it. Threshold
// 105 was checked against all 8 palette entries plus the grey fallback.
ImU32 inkFor(uint32_t bg) {
    int r = bg & 0xFF, g = (bg >> 8) & 0xFF, b = (bg >> 16) & 0xFF;   // IM_COL32 packs R,G,B,A low->high
    return ((r * 299 + g * 587 + b * 114) / 1000 > 105) ? IM_COL32(0, 0, 0, 255)
                                                        : IM_COL32(255, 255, 255, 255);
}
// Trim the MIDDLE of a path to fit maxW, keeping the two load-bearing ends: the
// DRIVE LETTER and the actual folder name. A plain right-truncating ellipsis would
// eat the folder name; a left one would eat the drive. Both are the point.
// Cached, because this runs every frame and CalcTextSize per candidate is not free.
std::string elideMiddle(const std::string& s, float maxW) {
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
void sortLibrary() {
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
std::string midEllipsis(const std::string& s, float maxW) {
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
bool pillButton(const char* label, bool on, ImU32 accent) {
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
bool crownButton(bool on) {
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
bool broomButton() {
    return ImGui::Button(ico(ICON_BROOM "##broom", "Trim Silence##broom"));
}


// ONE number, so the card and the list clipper can never disagree about row height.
float libCardHeight() {
    const float S = ImGui::GetIO().FontGlobalScale;
    return 10.0f * S * 2.0f + ImGui::GetTextLineHeight() * 2.0f + 4.0f * S;
}
float libCardStride() { return libCardHeight() + 8.0f * ImGui::GetIO().FontGlobalScale; }

// One card. `accent` is the colour this video's clips already wear on the timeline
// (0 = none on the timeline yet). Returns what the user did; the CALLER performs
// the actions, so this helper needs nothing declared later in the file.
LibCardResult drawLibraryCard(VideoRow& v, bool selected, bool justViewed,
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
        // GREEN when a saved auto-cut already exists (one click = LOAD it, no
        // re-analysis); BLUE when there is none yet (one click = auto-cut + caption).
        const bool saved = v.hasSavedCut;
        const ImU32 col = saved ? (rhov ? IM_COL32(0x5B, 0xFF, 0x77, 255) : IM_COL32(0x14, 0xFF, 0x39, 255))
                                : (rhov ? IM_COL32(0x33, 0xC2, 0xF2, 255) : IM_COL32(0x00, 0xAE, 0xEF, 255));
        const float rh = btnD * 0.92f;
        drawRobotMark(dl, bc2.x - rh * 0.86f * 0.5f, bc2.y - rh * 0.5f, rh, col);
        if (saved) {
            // A down-chevron badge = "load this saved cut down onto the timeline".
            const float k = btnD * 0.15f;
            const ImVec2 b(bc2.x + btnD * 0.30f, bc2.y - btnD * 0.30f);
            dl->AddCircleFilled(b, k + 3.0f * S, IM_COL32(10, 12, 14, 255));
            dl->AddLine(ImVec2(b.x - k, b.y - k * 0.4f), ImVec2(b.x, b.y + k * 0.7f), IM_COL32(0x14, 0xFF, 0x39, 255), 2.2f * S);
            dl->AddLine(ImVec2(b.x + k, b.y - k * 0.4f), ImVec2(b.x, b.y + k * 0.7f), IM_COL32(0x14, 0xFF, 0x39, 255), 2.2f * S);
        }
        if (rhov) {
            ImGui::SetMouseCursor(ImGuiMouseCursor_Hand);
            ImGui::SetTooltip("%s", saved
                ? "Load the saved auto-cut of this video (clips + captions) - already analysed. Right-click to re-run."
                : "Auto-cut this video AND caption it (becky-subtitle) - clips + captions onto the timeline");
        }
    }

    // Advance EXACTLY one stride so ImGuiListClipper's fixed item height matches.
    ImGui::SetCursorScreenPos(ImVec2(p0.x, p1.y + 8.0f * S));
    return res;
}

// Remember the last opened folder across launches (A-3): a tiny sidecar file,
// not the registry — cheap, and this app has no other persisted settings yet.
std::string lastFolderStatePath() {
    const char* base = getenv("LOCALAPPDATA");
    std::string dir = std::string(base ? base : ".") + "\\becky";
    CreateDirectoryA(dir.c_str(), nullptr);
    return dir + "\\becky_review_last_folder.txt";
}
void rememberFolder(const std::string& folder) {
    std::ofstream f(lastFolderStatePath(), std::ios::trunc);
    if (f) f << folder;
}
std::string recallFolder() {
    std::ifstream f(lastFolderStatePath());
    std::string s; if (f) std::getline(f, s);
    return s;
}
// applyFolderView loads a FolderView (from open_folder or pick_folder) into the
// local library list and remembers it as the last-opened folder.
void applyFolderView(const json& d, const std::string& fallbackRoot) {
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
            row.hasSavedCut = !row.path.empty() && std::ifstream(reelPathForVideo(row.path)).good();
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
bool loadFolder(const std::string& folder) {
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
void openInFileBrowser(const std::string& path) {
    std::wstring arg = L"/select,\"" + utf8ToWide(path) + L"\"";
    ShellExecuteW(nullptr, L"open", L"explorer.exe", arg.c_str(), nullptr, SW_SHOWNORMAL);
}
std::string wideToUtf8(const std::wstring& w) {
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
std::string pickOpenReelFile(HWND owner) {
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

bool hasExtCI(const std::string& path, const char* ext) {
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
std::string convertEditIfNeeded(const std::string& path) {
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
void seekToSpan(const std::string& source, double a, double b, bool startPlaying,
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
void previewPlaySpan(const std::string& source, double a, double b,
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
bool endPreviewRestore(double& curSec, bool& playing, double& lastComposed) {
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
void playWholeVideo(const std::string& path, double& curSec, bool& playing, double& lastComposed) {
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
void openTranscript(const std::string& fullVideoPath) {
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
void cardsFromJSON(const json& d) {
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
void refreshCards() {
    g_cardsErr.clear();
    json r = engineCall("questions", {}, 8.0);
    if (!r.value("ok", false)) { g_cardsErr = r.value("error", std::string("questions unavailable")); g_cards.clear(); return; }
    cardsFromJSON(r.contains("data") ? r["data"] : r);
}

// parse one search reply (shared by keyword + qmd) into a flat Hit list.
void parseSearchReply(bool qmd, const json& d, std::vector<Hit>& out) {
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
void searchWorker() {
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
void runSearch(bool qmd) {
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
int insertIndexAtPlayhead(double curSec) {
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
void addSpanToTimeline(const std::string& source, double a, double b, const std::string& label,
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
    // Move the playhead to the BEGINNING of where the new clip will land, so
    // Jordan immediately sees what he just added (not the middle of the previous
    // clip). Deterministic: the new clip's compStart is the sum of durations of
    // all clips before index `at`.
    {
        double newStart = 0;
        for (int k = 0; k < at && k < (int)g_track[0].size(); k++)
            newStart += g_track[0][k].out - g_track[0][k].in;
        curSec = newStart;
        lastComposed = -1;   // force recompose at the new position
    }
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
void addHitToTimeline(const Hit& h, double& curSec, bool& playing, double& lastComposed) {
    addSpanToTimeline(h.source, h.start, h.end, baseName(h.source), curSec, playing, lastComposed);
}
void addCueToTimeline(const CueRow& c, double& curSec, bool& playing, double& lastComposed) {
    addSpanToTimeline(c.source, c.start, c.end, baseName(c.source), curSec, playing, lastComposed);
}
// Items 10/11: add a MULTI-SELECTION of transcript quotes in ONE undo. CONSECUTIVE selected
// cues (adjacent indices, same source) merge into a SINGLE clip - the video is continuous
// there. A SKIPPED quote (a gap in the selected indices) breaks the run, so the next cue
// becomes a SEPARATE clip and the omission shows as a cut on the timeline (Jordan's rule).
// Everything is inserted to the LEFT of the playhead as one set_clips edit (one Ctrl+Z).
void addCuesToTimeline(const std::set<int>& sel, double& curSec, bool& playing, double& lastComposed) {
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
std::vector<bool> cueParagraphStarts() {
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
// triggerGetCaptions is declared in captions.h
void applyAutoCut(const std::string& name, const std::string& source, double& curSec, double& lastComposed,
                         std::vector<std::pair<double, double>> restrictRanges, bool thenCaptions) {
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
