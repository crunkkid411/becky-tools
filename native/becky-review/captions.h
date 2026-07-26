#pragma once
// captions.h - Caption track module (SRT parsing, editing, undo/redo, anchoring, OSD drawing).
// Extracted from main.cpp (P4 module split, 2026-07-25). No logic changes.

#include "imgui.h"
#include <string>
#include <vector>
#include <set>
#include <map>
#include <deque>
#include <mutex>
#include <atomic>
#include <cstdint>
#include "json.hpp"
using json = nlohmann::json;

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

// --------------- caption types ---------------
struct CapWord { std::string word; double start = 0, end = 0; };
struct Caption { double start = 0, end = 0; std::string text; std::string clipId; double srcIn = 0, srcOut = 0; std::vector<CapWord> words; };

// --------------- constants ---------------
extern const int CAP_ASS_H;         // ff_ass_subtitle_header_default PlayResY
extern const int CAP_ASS_W;         // ...and PlayResX

// --------------- caption globals (defined in captions.cpp) ---------------
extern std::vector<Caption> g_caps;
extern bool g_capOsdShowing;
extern int g_capMarginV;
extern bool g_capMarginDrag;
extern int g_capMarginAtGrab;
extern double g_capMarginGrabY;
extern double g_capMarginUnitsPerPx;
extern std::string g_capPath;
extern std::string g_capErr;
extern int g_capSel;
extern int g_capEdit;
extern char g_capEditBuf[1024];
extern bool g_capEditFocus;
extern bool g_capEditSnapped;
extern std::atomic<bool> g_cliCutBusy;
extern bool g_getCaptionsAfterAdd;

// --------------- globals from main.cpp that captions needs ---------------
extern std::vector<Clip> g_track[2];
extern bool g_capsOn;
extern std::string g_renderMsg;
extern double g_renderMsgAt;

// --------------- functions from main.cpp that captions needs ---------------
Clip* clipAtComp(int tr, double t);
double sourceFps(const std::string& source);
void imguiOutlinedText(ImDrawList* dl, ImVec2 pos, float fontSize, const char* text);
std::string baseName(const std::string& p);

// --------------- caption functions (defined in captions.cpp) ---------------
double reelFps();
double quantToFrame(double t);
void rebuildDerivedCaptions();
bool captionTryUndo();
bool captionTryRedo();
void loadCaptions(const std::string& reelPath);
void triggerGetCaptions();
std::string reelPathForVideo(const std::string& videoPath);
void drawCaptionsImGui(double t, ImVec2 origin, ImVec2 size);
void pushCapUndo();
void saveCaptions();
void saveCapStyle();
void reanchorCap(Caption& cap);
size_t capTokenCount(const std::string& s);
size_t nthSpaceIndex(const std::string& s, int k);

// --------------- pending caption splits (P2 caption-split fix, 2026-07-25) ---------------
// When a clip is split (manually or by the AI agent), the edit drain pushes the
// split info here. rebuildDerivedCaptions processes these ONCE: divides the ONE
// caption at the split point using word-level timestamps, then both halves fill
// their clip entirely. This is the ONLY path that uses word timestamps to split
// a caption - the general reproject gate protects manually-tuned captions.
struct PendingCaptionSplit { std::string parentId; std::string newId; double splitSrcT; };
extern std::vector<PendingCaptionSplit> g_pendingCaptionSplits;
