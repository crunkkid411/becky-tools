#pragma once
// ui_panels.h - library / search / transcript / Q&A panel state and helpers.
// Extracted from main.cpp (P4 module split, 2026-07-25). No logic changes.

#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <shellapi.h>
#include <commdlg.h>
#include "imgui.h"
#include <string>
#include <vector>
#include <map>
#include <set>
#include <deque>
#include <mutex>
#include <condition_variable>
#include <thread>
#include <algorithm>
#include <fstream>
#include <cstring>
#include <cctype>
#include <cmath>
#include <cstdint>
#include "json.hpp"
using json = nlohmann::json;
#include "captions.h"     // Clip, g_track, rebuildDerivedCaptions, triggerGetCaptions, reelPathForVideo
#include "waveform.h"     // Peaks, peaksGet, peaksRequest, g_bgPool, BgWorkPool
#include "engine_seam.h"  // engineCall, engineCallAsync, beginWork, endWork, crashLog, nowSec
#include "timeline_draw.h" // loadTimelineView, packTrack, recomputeDur, clearScrubPreview, g_compDur

// ICON_BROOM needed by broomButton (the rest of the ICON_* macros stay in main.cpp)
#ifndef ICON_BROOM
#define ICON_BROOM    "\xF0\x9F\xA7\xB9" // U+1F9F9 broom - COLOR emoji (Trim silence)
#endif

// --------------- globals from main.cpp that ui_panels uses ---------------
extern std::vector<Clip> g_reelBeforePreview;
extern double g_previewFrozenPlayhead;
void paintClipFromKnownSource(Clip& cl);
void applyAddClipDelta(const json& d);
const char* ico(const char* iconLabel, const char* textLabel);

// --------------- structs ---------------
struct VideoRow {
    std::string path, name, date; bool hasTranscript = false;
    // WHEN THE FOOTAGE WAS SHOT (unix seconds), resolved by the engine from the
    // best source it has: a .beckymeta.json sidecar, else a date token in the
    // filename, else the file's own date. `date` above is the same instant as
    // text, shown on the card - so he can SEE the order is right instead of
    // taking a shuffled list on faith.
    //
    // Do NOT sort by the raw file mtime. On a copied evidence drive that is the
    // COPY date, and sorting by it put January footage above May footage.
    long long recorded = 0;
    // #4 (Jordan 2026-07-24): true when a saved auto-cut reel exists beside this
    // video (reelPathForVideo). The blue robot then LOADS that cut in one click
    // instead of re-analysing. Set on folder load.
    bool hasSavedCut = false;
    // B-1 card display cache: the middle-ellipsised name and the width it was
    // measured at. Recomputed only when the panel width changes, so a 2258-video
    // corpus costs zero CalcTextSize work per frame while the panel is still.
    std::string disp; float dispW = -1.0f;
};
struct TranscribeDone { std::string name; bool ok = false; std::string err; };
struct AddExternalDone { bool ok = false; std::string err; json data; };
struct Hit {
    std::string source, name, date, text, timecode;
    double start = 0, end = 0, score = 0;
    bool transcriptOnly = false;
    // The order the ENGINE returned this hit in (it sorts by date). Kept so the
    // relevance sort below is REVERSIBLE without re-running the search - one int
    // per hit instead of a second copy of the whole result set.
    int ord = 0;
};
struct SearchReq { std::string query; bool qmd = false; double t0 = 0; };
struct SearchDone { bool ok = false; std::string mode, note, err, query; std::vector<Hit> hits; double elapsedMs = 0; };
struct CueRow { std::string source, name, text, timecode; double start = 0, end = 0; };
struct QACard {
    std::string id, question, answer;
    std::vector<std::string> clipIDs;
    bool answered = false;
};
struct LibCardResult { bool clicked = false, dbl = false, plus = false, robot = false; };

// --------------- library state ---------------
extern std::vector<VideoRow> g_videos;
extern std::string g_folderRoot;
extern int g_orphanCount;
extern std::string g_folderErr;
extern int g_sortMode;
extern bool g_transcribeAllBusy;
extern int g_libCtxIdx;
extern int g_libSel;
extern bool g_libScrollPending;
extern bool g_libFocused;
extern int g_libJustViewedIdx;

// ---- B-2: one-click local transcription ----
extern std::mutex g_transcribeMx;
extern std::set<std::string> g_transcribeInFlight;
extern std::deque<TranscribeDone> g_transcribeDoneQ;

// E-13: add_external
extern std::mutex g_addExtMx;
extern std::deque<AddExternalDone> g_addExtDoneQ;

// ---- search state ----
extern char g_searchBuf[256];
extern bool g_smartSearch;
extern std::vector<Hit> g_hits;
extern int g_hitSel;
extern bool g_hitScrollPending;
extern bool g_hitRelevance;
extern std::string g_searchMode;
extern std::string g_searchNote;
extern std::string g_searchErr;
extern bool g_searching;
extern std::mutex g_searchDoneMx;
extern bool g_searchDonePending;
extern SearchDone g_searchDoneResult;

// ---- transcript view (B-8) ----
extern std::vector<CueRow> g_cues;
extern std::string g_cueName;
extern std::string g_cueErr;
extern int g_cueSel;
extern std::set<int> g_cueMulti;
extern int g_cueAnchor;
extern bool g_cueScrollPending;
extern char g_withinBuf[128];
extern std::string g_withinLast;
extern int g_withinMatchIdx;
extern int g_withinMatchCount;

// ---- Q&A cards (G-1) ----
extern std::vector<QACard> g_cards;
extern std::string g_cardsErr;
extern std::string g_askAnswer;
extern std::string g_proposalID;
extern std::string g_proposalPreview;
extern std::string g_proposalNote;
extern json g_proposalDiff;
extern bool g_proposalPending;
extern std::map<std::string, uint32_t> g_cardColor;
extern const uint32_t kPalette[8];

// ---- ask-becky panel state ----
extern char g_askBuf[512];
extern bool g_askFocus;
extern std::string g_askEcho;
extern std::string g_backendSummary;
extern bool g_backendOK;
extern std::string g_answerCardID;
extern std::string g_answerCardQ;
extern bool g_forensicBusy;
extern const char* kAskChipLabel[3];
extern const char* kAskChipPrompt[3];

// --------------- functions defined in ui_panels.cpp ---------------
void requestTranscribe(const std::string& path, const std::string& baseName);
void requestAddExternal(const std::string& path, int at);
void applyHitSort();
bool ciContains(const std::string& hay, const std::string& needle);
uint32_t cardColorFor(const std::string& id);
void drawRobotMark(ImDrawList* d, float x, float y0, float h, ImU32 col);
void askBeckyMark(float h);
bool sendArrowButton(ImVec2 size);
uint32_t driveColor(const std::string& path);
ImU32 inkFor(uint32_t bg);
std::string elideMiddle(const std::string& s, float maxW);
void sortLibrary();
std::string midEllipsis(const std::string& s, float maxW);
bool pillButton(const char* label, bool on, ImU32 accent);
bool crownButton(bool on);
bool broomButton();
float libCardHeight();
float libCardStride();
LibCardResult drawLibraryCard(VideoRow& v, bool selected, bool justViewed,
                              bool inFlight, ImU32 accent);
void rememberFolder(const std::string& folder);
std::string recallFolder();
void applyFolderView(const json& d, const std::string& fallbackRoot);
bool loadFolder(const std::string& folder);
void openInFileBrowser(const std::string& path);
std::string pickOpenReelFile(HWND owner);
// Modern Explorer-style folder chooser (see the definition). "" = cancelled.
std::string pickCaseFolderNative(HWND owner, const std::string& startDir);
bool hasExtCI(const std::string& path, const char* ext);
std::string convertEditIfNeeded(const std::string& path);
void seekToSpan(const std::string& source, double a, double b, bool startPlaying,
                double& curSec, bool& playing, double& lastComposed);
void previewPlaySpan(const std::string& source, double a, double b,
                     double& curSec, bool& playing, double& lastComposed);
bool endPreviewRestore(double& curSec, bool& playing, double& lastComposed);
void playWholeVideo(const std::string& path, double& curSec, bool& playing, double& lastComposed);
void openTranscript(const std::string& fullVideoPath);
void cardsFromJSON(const json& d);
void refreshCards();
void searchWorker();
void runSearch(bool qmd);
int insertIndexAtPlayhead(double curSec);
void addSpanToTimeline(const std::string& source, double a, double b, const std::string& label,
                       double& curSec, bool& playing, double& lastComposed);
void addHitToTimeline(const Hit& h, double& curSec, bool& playing, double& lastComposed);
void addCueToTimeline(const CueRow& c, double& curSec, bool& playing, double& lastComposed);
void addCuesToTimeline(const std::set<int>& sel, double& curSec, bool& playing, double& lastComposed);
std::vector<bool> cueParagraphStarts();
void applyAutoCut(const std::string& name, const std::string& source, double& curSec, double& lastComposed,
                  std::vector<std::pair<double, double>> restrictRanges = {}, bool thenCaptions = false);
