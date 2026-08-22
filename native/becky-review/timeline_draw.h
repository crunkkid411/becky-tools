#pragma once
// timeline_draw.h - the timeline surface (drawTimeline, clip rendering, ruler,
// gestures, Ctrl+arrow edit-point stepping). Extracted from main.cpp (P4 module
// split). No logic changes - pure move + headers.

#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <d3d11.h>
#include "imgui.h"
#include <string>
#include <vector>
#include <set>
#include <map>
#include <deque>
#include <utility>
#include <cstdint>
#include <cstddef>
#include "json.hpp"
using json = nlohmann::json;
#include "captions.h"     // Clip, Caption, g_track, g_caps, caption globals + functions
#include "waveform.h"     // Peaks, peaksGet, g_fillEpoch
#include "engine_seam.h"  // engineCall, engineCallAsync, editLog, crashLog, g_lastEngineEditAt

// --------------- the timeline surface: colour palette ---------------
// (moved verbatim from main.cpp; inline constexpr so both main.cpp and
// timeline_draw.cpp share one definition with no logic change)
inline constexpr ImU32 COL_BG       = IM_COL32(16, 18, 22, 255);
inline constexpr ImU32 COL_LANE     = IM_COL32(24, 27, 33, 255);
// Round 4, item 2: the ruler is DARK like the rest of the timeline - NOT a gray
// band. Jordan, comparing to becky-review-buttons-correct.JPG: "you've made the
// entire thing gray ... the timeline is divided from the buttons by a THIN GRAY
// LINE". Measured off that reference: the toolbar->timeline divider is one ~#3E3F41
// hairline; the ruler/track below it are dark; the tick labels are LIGHT gray so
// they read on the dark ruler (this reverses round 3's gray #676767 band, which is
// exactly the "entire thing gray" he rejected - newest request wins).
inline constexpr ImU32 COL_TLDIVIDER = IM_COL32(62, 63, 65, 255);   // #3E3F41 toolbar|timeline hairline
inline constexpr ImU32 COL_RULERTX  = IM_COL32(214, 216, 222, 255); // BRIGHT labels on the dark ruler (round 5: was too dim/small)
inline constexpr ImU32 COL_TICK     = IM_COL32(120, 122, 128, 255); // major tick, clearly visible on dark
inline constexpr ImU32 COL_TICKMIN  = IM_COL32(64, 66, 72, 255);    // minor tick, dim
inline constexpr ImU32 COL_CLIP     = IM_COL32(38, 56, 84, 255);
inline constexpr ImU32 COL_CLIPBRD  = IM_COL32(255, 255, 255, 70);
inline constexpr ImU32 COL_WAVE     = IM_COL32(255, 255, 255, 128);
inline constexpr ImU32 COL_WAVEDIM  = IM_COL32(255, 255, 255, 60);
inline constexpr ImU32 COL_PLAYHEAD = IM_COL32(0, 0, 0, 255);
inline constexpr ImU32 COL_PHFLAG   = IM_COL32(255, 255, 255, 255);
inline constexpr ImU32 COL_PHGRIP   = IM_COL32(58, 58, 58, 255);
inline constexpr ImU32 COL_DROPMARK = IM_COL32(255, 210, 0, 255);
inline constexpr ImU32 COL_LABEL    = IM_COL32(235, 238, 245, 235);
inline constexpr ImU32 COL_PIP      = IM_COL32(0, 160, 96, 255);
inline constexpr ImU32 COL_THRBAR   = IM_COL32(255, 120, 70, 235);
// Caption lane - deliberately AMBER so it reads as a different kind of thing from
// the blue clip lane at a glance. High contrast on purpose (accessibility aid).
inline constexpr ImU32 COL_CAPLANE  = IM_COL32(28, 24, 18, 255);
inline constexpr ImU32 COL_CAP      = IM_COL32(96, 68, 16, 255);
inline constexpr ImU32 COL_CAPSEL   = IM_COL32(168, 118, 20, 255);
inline constexpr ImU32 COL_CAPBRD   = IM_COL32(255, 190, 60, 255);
inline constexpr ImU32 COL_CAPTX    = IM_COL32(255, 240, 208, 255);
inline constexpr ImU32 COL_CAPCUT   = IM_COL32(255, 255, 255, 46);
// Item 3 root cause (round 2): detection AND the seamless skip during playback
// were BOTH already correct - proven live with a synthetic loud/silence/loud
// clip (the skip landed exactly on the silent span, confirmed by playhead
// position vs elapsed wall time). Jordan, live, on the crimson experiment:
// the plain semi-transparent black is CORRECT and reads fine, precisely
// because it sits on top of already-colourful clips - reverted to the
// original.
inline constexpr ImU32 COL_QUIETDIM = IM_COL32(0, 0, 0, 110);

// --------------- structs shared with main.cpp ---------------
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
struct ThumbTex { ID3D11ShaderResourceView* srv = nullptr; int w = 0, h = 0; };
// E-13: WM_DROPFILES only captures client-space drop data here - the real work
// (path filtering, screen->timeline-seconds conversion via g_scrollSec/g_pps,
// and the add_external engine call) happens once per frame in drawTimeline,
// same "WndProc stays a thin OS-message forwarder" pattern as g_resize/g_W/g_H.
struct PendingDrop { std::vector<std::string> paths; int clientX = 0, clientY = 0; };

// Timeline markers (Ctrl+M): a compilation-timeline position + typed note. The
// engine's reel is the source of truth (add_marker/set_marker verbs; the
// TimelineView reply carries "markers") - this is the UI mirror, refreshed by
// loadTimelineView the same way clips are.
struct TimelineMarker { double at = 0; std::string label; };
extern std::vector<TimelineMarker> g_markers;
// >= 0: a Ctrl+M request for drawTimeline to open the marker-note editor at this
// comp time (the key handler lives in main.cpp's key block, the popup here).
extern double g_markerReqAt;

// --------------- globals from main.cpp that the timeline surface uses ---------------
extern double g_compDur;
extern double g_pps;
extern int g_zoomReq;
extern double g_scrollSec;
extern bool g_playingExt;
extern double g_stockSec;
extern bool g_stockFlash;
extern double g_lastUserScroll;
extern std::set<std::string> g_sel;
extern std::string g_selAnchor;
extern bool g_thrOn;
extern double g_thrLevel;
extern bool g_quietDirty;
extern int g_quietEpochSeen;
extern std::vector<std::pair<double, double>> g_quietRanges;
extern bool g_inTiedPreview;
extern Gesture g_gest;
extern std::vector<PendingDrop> g_pendingDrops;
extern std::string g_renderMsg;
extern double g_renderMsgAt;

// --------------- functions from main.cpp that the timeline surface calls ---------------
void packTrack(int tr);
void recomputeDur();
void clearScrubPreview();
void engineReelSeek(double compT);
void emitScrub(double t, bool final_);
bool emitView();
void emitSelect();
void emitThreshold(bool final_);
void recomputeQuiet();
void pushOverlayDefaults();
void loadTimelineView(const json& tv);
void drainThumbs();
ThumbTex* getThumb(const std::string& source);
void requestAddExternal(const std::string& path, int at);
bool hasExtCI(const std::string& path, const char* ext);
std::string convertEditIfNeeded(const std::string& path);
void openInFileBrowser(const std::string& path);
void openTranscript(const std::string& fullVideoPath);
void drawWave(ImDrawList* dl, const std::string& source, double cin, double cout,
              float clipX0, float wx0, float wx1, float wy0, float wy1, double pps, ImU32 col);
bool clipPreparing(const Clip& c);
double snapComp(double t, double pps, double curSec, int exclIdx, float px = 8.0f);
void fmtTime(double s, char* out, size_t n, bool subSec);
double rulerStep(double pps);

// --------------- timeline surface functions (defined in timeline_draw.cpp) ---------------
void collectBoundaries(std::vector<double>& out);
bool nextBoundary(double from, double& hit);
bool prevBoundary(double from, double& hit);
void drawTimeline(double& curSec, bool& playing);
