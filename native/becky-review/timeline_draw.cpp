// timeline_draw.cpp - the timeline surface (drawTimeline, clip rendering, ruler,
// gestures, Ctrl+arrow edit-point stepping). Extracted from main.cpp (P4 module
// split). No logic changes - pure move + headers.
#include "timeline_draw.h"
#include "ui_panels.h"   // g_searchMode, g_searchErr, openTranscript
#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cctype>
#include <thread>
#include <exception>

// ---- Ctrl+Left / Ctrl+Right: step to the previous/next EDIT POINT ----
//
// Jordan marked this CRITICAL and noted "we've tried to fix this several times".
// The reason it kept coming back is that the two directions were separate loops
// searching DIFFERENT things: Ctrl+Left scanned only clip STARTS (c.compStart)
// while Ctrl+Right scanned only clip ENDS, and BOTH looked at g_track[0] alone.
// So the playhead could not reach a boundary that existed only on track 1 or in
// the caption lane, the two directions disagreed about where the edit points
// were, and neither could land on 0 or the very end of the timeline - which is
// what "sticks at the clip edge" feels like in the hand.
//
// One list, built once, used by both directions. Every clip edge on EVERY track,
// plus the two ends of the timeline. Now Ctrl+Left and Ctrl+Right are exact
// inverses by construction, which is the property that was missing - not any
// single off-by-one.
//
// CAPTION edges are deliberately NOT in this list. The first version included
// them and I drove it: three Ctrl+Right presses advanced 0.8s, because 179
// captions subdivide the 88 clips. Stepping caption-by-caption makes crossing
// the timeline slower, which is the opposite of the complaint. Ctrl+arrow means
// CLIP edit points, the way Vegas does it.
//
// eps is one frame at 60fps: a boundary closer than that to the playhead is the
// one we are standing on, not one to jump to, so holding the key walks instead
// of sticking.
void collectBoundaries(std::vector<double>& out) {
    out.clear();
    out.push_back(0.0);
    if (g_compDur > 0) out.push_back(g_compDur);
    for (int tr = 0; tr < 2; tr++)
        for (auto& c : g_track[tr]) {
            out.push_back(c.compStart);
            out.push_back(c.compStart + (c.out - c.in));
        }
    std::sort(out.begin(), out.end());
    out.erase(std::unique(out.begin(), out.end(),
                          [](double a, double b) { return std::fabs(a - b) < 1e-6; }),
              out.end());
}

bool nextBoundary(double from, double& hit) {
    static std::vector<double> b; collectBoundaries(b);
    const double eps = 1.0 / 60.0;
    for (double t : b) if (t > from + eps) { hit = t; return true; }
    return false;
}

bool prevBoundary(double from, double& hit) {
    static std::vector<double> b; collectBoundaries(b);
    const double eps = 1.0 / 60.0;
    for (auto it = b.rbegin(); it != b.rend(); ++it) if (*it < from - eps) { hit = *it; return true; }
    return false;
}

// Item 31: a CLOSED-HAND (grab) cursor for "I am moving something". ImGui/Win32 has no
// closed-hand cursor (IDC_HAND is the POINTING hand), so hide the OS cursor and hand-draw
// a small fist - palm + four curled knuckles + a thumb - on the foreground draw list at
// the pointer. White fill + dark outline so it reads on any timeline colour.
void drawGrabCursor() {
    ImGui::SetMouseCursor(ImGuiMouseCursor_None);
    ImVec2 m = ImGui::GetMousePos();
    ImDrawList* dl = ImGui::GetForegroundDrawList();
    const ImU32 fill = IM_COL32(240, 240, 245, 255), line = IM_COL32(20, 20, 24, 255);
    const float s = 9.0f;
    ImVec2 a(m.x - s * 0.8f, m.y - s * 0.15f), b(m.x + s * 0.9f, m.y + s);
    dl->AddRectFilled(a, b, fill, s * 0.45f);                 // the fist body
    dl->AddRect(a, b, line, s * 0.45f, 0, 1.5f);
    for (int i = 0; i < 4; i++) {                             // four curled-finger knuckles
        float kx = a.x + (b.x - a.x) * (0.22f + i * 0.19f);
        dl->AddCircleFilled(ImVec2(kx, a.y), s * 0.26f, fill);
        dl->AddCircle(ImVec2(kx, a.y), s * 0.26f, line, 0, 1.2f);
    }
    dl->AddCircleFilled(ImVec2(a.x, m.y + s * 0.4f), s * 0.28f, fill);   // thumb
    dl->AddCircle(ImVec2(a.x, m.y + s * 0.4f), s * 0.28f, line, 0, 1.2f);
}

void drawTimeline(double& curSec, bool& playing) {
    ImDrawList* dl = ImGui::GetWindowDrawList();
    ImVec2 p = ImGui::GetCursorScreenPos();
    float availW = ImGui::GetContentRegionAvail().x;
    float availH = ImGui::GetContentRegionAvail().y;
    if (availW < 16 || availH < 44) return;
    float tlX = p.x, tlW = availW;
    float rulerH = 24, sbH = 12, gap = 4;   // round 5b: 24px == the reference's .ruler height
    int lanes = 1;
    float lanesH = availH - rulerH - sbH - gap * 2;
    // The caption lane sits directly UNDER the clip lane and inside the same
    // InvisibleButton below, so one gesture handler drives both. With no reel
    // loaded (g_capPath empty) capH/capGap are 0 and the layout is byte-identical
    // to the pre-caption one.
    // A-1: derived captions (no sidecar, no reel) still get a lane - the lane
    // shows whenever there ARE captions or a reel is loaded to explain itself on.
    bool showCaps = g_capsOn && (!g_capPath.empty() || !g_caps.empty()) && lanesH > 90;
    float capH = showCaps ? 36.0f : 0.0f;
    float capGap = showCaps ? 4.0f : 0.0f;
    float laneH = lanesH - capH - capGap;
    if (laneH < 24) laneH = 24;
    float aY = p.y + rulerH + gap;
    float capY = aY + laneH + capGap;
    float bot = capY + capH;
    float sbY = bot + gap;

    dl->AddRectFilled(p, ImVec2(p.x + tlW, sbY + sbH), COL_BG);
    // Item 2 (round 4): NO gray ruler band. The whole timeline (ruler included) is
    // the dark COL_BG fill above; the toolbar is divided from it by ONE thin gray
    // hairline at the very top (the reference's only visible divider), and a fainter
    // dark rule under the ruler sets the timecodes off from the clips.
    dl->AddLine(ImVec2(p.x, p.y + 0.5f), ImVec2(p.x + tlW, p.y + 0.5f), COL_TLDIVIDER, 1.0f);
    dl->AddLine(ImVec2(p.x, p.y + rulerH), ImVec2(p.x + tlW, p.y + rulerH), IM_COL32(26, 26, 26, 255), 1.0f);

    ImGui::SetCursorScreenPos(p);
    ImGui::InvisibleButton("tl", ImVec2(tlW, bot - p.y));
    // Item 8 (round 2): this ONE giant button covers the whole timeline,
    // caption lane included, and is submitted before the caption-edit
    // InputText further down - so without this, every click meant to place
    // a caret or double-click-select a word inside an open caption edit box
    // was being claimed by "tl" first (a normal ImGui click-priority rule:
    // the button that's ALREADY submitted sees itself as hovered before a
    // later widget at the same position exists yet), and dispatched as a
    // timeline click/scrub instead - found live: a second click meant for
    // the caret moved the playhead and silently closed the edit box.
    // AllowOverlap is the same fix the library card's round "+" button
    // already uses for exactly this shape of problem: it lets a LATER
    // widget at an overlapping position still win hover/click priority.
    ImGui::SetItemAllowOverlap();
    bool hovered = ImGui::IsItemHovered();
    // NORMAL POINTER over the timeline. Jordan asked for the I-beam on 2026-06-30
    // (feedback1, replacing the hand) and then REVERSED that later - he wants the
    // ordinary arrow back. Newest instruction wins, so do not "restore" the I-beam
    // by citing the older feedback file. Leaving the cursor unset here means ImGui
    // keeps ImGuiMouseCursor_Arrow, which is exactly what he asked for.
    (void)hovered;
    // Item 1 fix (round 3): a preview audition swaps g_track[0] for a one-clip
    // (or tied-clips) reel WHILE THE REAL REEL IS FROZEN AND SHOWN INSTEAD (see the
    // drawTimeline call site, which swaps the real reel/duration/playhead back in
    // for this render).
    //
    // Round 5: clicking the timeline while an audition is playing is exactly the
    // gesture that means "I'm done previewing, take me back to the reel". Because the
    // frozen render already put the REAL reel into g_track[0] for this call, ending
    // the preview here (clear g_inTiedPreview) makes the very same click fall through
    // to the normal gesture below and select/seek on the real reel - one click both
    // exits the audition and acts. The call site notices g_inTiedPreview flipped and
    // keeps the reel instead of restoring the preview clip.
    if (ImGui::IsItemActivated() && g_inTiedPreview) g_inTiedPreview = false;
    bool pressed = ImGui::IsItemActivated() && !g_inTiedPreview;
    bool active = ImGui::IsItemActive() && !g_inTiedPreview;
    bool released = ImGui::IsItemDeactivated() && !g_inTiedPreview;
    ImGuiIO& io = ImGui::GetIO();
    float mx = io.MousePos.x, my = io.MousePos.y;

    auto xToSec = [&](float x) { return std::max(0.0, g_scrollSec + (x - tlX) / g_pps); };
    auto secToX = [&](double s) { return tlX + (float)((s - g_scrollSec) * g_pps); };

    // Item 8 (round 2): SetItemAllowOverlap (above) is not enough on its own -
    // "tl" still computes its OWN pressed/active/released every frame from its
    // OWN hover test, independent of whatever gets submitted later, so a click
    // meant for the open caption-edit InputText was still dispatched as a
    // capHit() body-drag gesture (confirmed live: mx/my landing squarely
    // inside the edit box still logged a capHit press, kind 8, and the box
    // closed instead of placing a caret). The InputText itself needs NO help
    // to receive the click and place the caret/select a word - that is stock
    // ImGui InputText behaviour - it only needs "tl" to not ALSO react to the
    // same click and stomp the edit box. Recompute the exact same edit-box
    // rect the render code below uses and suppress "tl"'s three flags when a
    // press/release lands inside it while a caption is being edited.
    if (g_capEdit >= 0 && g_capEdit < (int)g_caps.size()) {
        float cx0 = secToX(g_caps[g_capEdit].start), cx1 = secToX(g_caps[g_capEdit].end);
        float ecx0 = std::max(cx0, tlX), ecx1 = std::min(cx1, tlX + tlW);
        if (ecx1 - ecx0 < 220) ecx1 = std::min(tlX + tlW, ecx0 + 220);
        if (ecx1 - ecx0 < 80) { ecx0 = tlX; ecx1 = std::min(tlX + tlW, tlX + 220); }
        if (mx >= ecx0 && mx <= ecx1 && my >= capY && my <= capY + capH) {
            pressed = false; active = false; released = false;
        }
    }

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
            // A reel (.json) or an edit export (.txt Vegas EDL / .xml Final Cut) dropped
            // in loads as the WHOLE TIMELINE, same as the Load Reel button - same fix as
            // that button's filter (Jordan drags his Vegas export straight in). Takes
            // priority over the per-clip video-insert loop below and only the first such
            // file in the drop is used, matching the WPF app's OnWebDrop.
            bool loadedEdit = false;
            for (auto& path : d.paths) {
                if (!hasExtCI(path, ".json") && !hasExtCI(path, ".txt") && !hasExtCI(path, ".xml")) continue;
                std::string rp = convertEditIfNeeded(path);
                if (!rp.empty()) {
                    // cycle 18 review's THE ONE THING (item 2 of 2): this was still a
                    // synchronous 30s engineCall on the UI thread - dropping a reel/EDL
                    // onto the window froze exactly like the Load Reel button used to
                    // before cycle 18 (main.cpp:1055's comment). curSec/playing are
                    // drawTimeline's own reference params (bound to main()'s locals,
                    // alive for the process lifetime), so capturing them by reference is
                    // exactly as safe as the button fix.
                    engineCallAsync("load_reel", { {"path", rp} }, 30.0, "Loading reel...",
                                    [rp, &curSec, &playing](const json& r) {
                        if (r.value("ok", false)) {
                            loadTimelineView(r.contains("data") ? r["data"] : r);
                            // NB: no lastComposed reset here (unlike the Load Reel button) -
                            // that variable is local to main()'s loop, out of scope in
                            // drawTimeline; playing=false already makes main()'s own
                            // "if (!playing) lastComposed = -1" catch it next frame.
                            curSec = 0; playing = false; g_playingExt = false;
                            loadCaptions(rp); g_renderMsg = "Loaded reel " + baseName(rp);
                        } else g_renderMsg = "Load reel failed: " + r.value("error", std::string("?"));
                        g_renderMsgAt = nowSec();
                    });
                }
                loadedEdit = true;
                break;
            }
            if (loadedEdit) continue;
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
    // (drainAsync used to be called HERE. It is not anymore - see main()'s drain
    // block. Delivering async replies from the MIDDLE of drawTimeline meant a
    // callback like add_clip's or apply_proposal's could replace g_track while
    // this function was halfway through reading it. It happened not to crash only
    // because no live reference to g_track survived across this exact line - a
    // property nobody could see, that any future edit above this point would have
    // silently broken. Model mutations now land with every other drain, BEFORE
    // the frame reads anything.)

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
    // Keyboard zoom (item 48): same path as the wheel, so the two can never
    // disagree about anchoring or limits. Two notches per press - one is barely
    // perceptible and he would have to hammer the key.
    if (g_zoomReq != 0) { applyWheel((float)g_zoomReq * 2.0f, false, zoomAnchorX()); g_zoomReq = 0; }

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
    // A DELETE MUST NOT DRAG THE VIEW SIDEWAYS UNDER HIM (items 96/106, stated
    // twice). This clamp used to run unconditionally every frame, so the moment
    // an edit shortened the reel maxScroll dropped and the whole timeline slid
    // left while he was working in it — he loses his place mid-edit, which for
    // someone editing at speed is worse than the wasted pixels it was saving.
    //
    // Now it only intervenes when the view has scrolled past EVERYTHING and is
    // showing nothing at all, and never mid-gesture. Sitting slightly past the
    // end of a shortened reel is normal NLE behaviour; being teleported is not.
    if (g_gest.kind == 0 && g_scrollSec > g_compDur) {
        g_scrollSec = maxScroll;
    }

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
            // Item 1 (round 2): the trim gesture itself was never gone (kind 4/5
            // below, set_trim on release) - it was just a 10px hairline, easy to
            // miss by a few pixels and land on "select the clip" or the neighbour's
            // edge instead (measured live: a drag that started 10px past the real
            // boundary silently grabbed the WRONG clip). Widened to 16px - still
            // capped at width/4 so a short clip keeps SOME body left to click, and
            // the width gate raised from 20 to 40 so two 16px zones on a tiny clip
            // can never swallow its whole body.
            float hw = std::min(16.0f, (x1 - x0) / 4);
            if ((x1 - x0) > 40 && x - x0 <= hw) zone = 4;
            else if ((x1 - x0) > 40 && x1 - x <= hw) zone = 5;
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
    static int s_capCtxIdx = -1;   // right-clicked caption (issues 4/5: glue + remove)
    if (hovered && ImGui::IsMouseClicked(ImGuiMouseButton_Right)) {
        int idx, zone;
        // A caption right-click is tested FIRST - the caption lane sits below the clip
        // lane, so its y-range is unambiguous and capHit already gates on it.
        if (capHit(mx, my, idx, zone)) {
            // Jordan (2026-07-24): right-click a caption = GLUE TO NEXT, immediately.
            // No popup, no questions. Merge this caption with the one that starts right
            // after it ON THE SAME CLIP, so "because" + "of that" becomes one line. The
            // words are carried along so a later split can still divide it by timing.
            g_capSel = idx;
            (void)s_capCtxIdx;
            int nextI = -1; double nextStart = 1e18;
            double myStart = g_caps[idx].start;
            for (size_t i = 0; i < g_caps.size(); i++)
                if ((int)i != idx && g_caps[i].clipId == g_caps[idx].clipId &&
                    g_caps[i].start >= myStart && g_caps[i].start < nextStart) {
                    nextStart = g_caps[i].start; nextI = (int)i;
                }
            if (nextI >= 0) {
                pushCapUndo();
                Caption& a = g_caps[idx];
                Caption& b = g_caps[nextI];
                if (!a.text.empty() && !b.text.empty()) a.text += " ";
                a.text += b.text;
                a.end = std::max(a.end, b.end);
                a.srcOut = std::max(a.srcOut, b.srcOut);          // same clip: extend a's source span
                for (auto& wd : b.words) a.words.push_back(wd);   // keep word timings for a later re-split
                g_caps.erase(g_caps.begin() + nextI);
                saveCaptions();
            }
        }
        else if (clipHit(mx, my, idx, zone)) { s_ctxIdx = idx; ImGui::OpenPopup("clipctx"); }
    }
    if (ImGui::BeginPopup("clipctx")) {
        if (s_ctxIdx >= 0 && s_ctxIdx < (int)g_track[0].size()) {
            Clip& c = g_track[0][s_ctxIdx];
            ImGui::TextDisabled("%s", c.label.c_str());
            ImGui::Separator();
            if (ImGui::MenuItem("Open in File Browser")) openInFileBrowser(c.source);
            if (ImGui::MenuItem("Copy File Name")) ImGui::SetClipboardText(baseName(c.source).c_str());
            if (ImGui::MenuItem("Open Transcript")) { g_searchMode.clear(); g_searchErr.clear(); openTranscript(c.source); }
            ImGui::Separator();
            // Item 27: build REAL TikTok-style captions (becky-subtitle: cut-snapped +
            // phrase-broken) for the whole timeline, not the raw Parakeet transcript.
            ImGui::BeginDisabled(g_cliCutBusy.load());
            if (ImGui::MenuItem("Get Captions")) triggerGetCaptions();
            ImGui::EndDisabled();
        }
        ImGui::EndPopup();
    }

    if (pressed) {
        // A real click on the real timeline means "back to the real timeline" -
        // drop any single-click cue/hit preview that might be showing (item B).
        clearScrubPreview();
        int idx, zone;
        g_gest = Gesture{};
        g_gest.pressX = mx; g_gest.ctrl = io.KeyCtrl; g_gest.shiftK = io.KeyShift;
        if (my < aY && std::abs(mx - secToX(curSec)) <= 10.0f) {
            // Item 8, corrected live: grabbing the PLAYHEAD HANDLE ITSELF must SCRUB
            // (drag = the frame follows the cursor), never pan - panning was eating
            // the one gesture an editor expects to work everywhere: drag the
            // playhead. Hit test is a little wider than the drawn flag (fw=8 in the
            // playhead draw block below) for an easy grab. Same mechanics as an
            // empty-track click-drag (kind 1): pauses, scrubs frame-exact.
            g_gest.kind = 1;
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            playing = false; g_playingExt = false;
            // Item 2: same stale-stock fix as the paused clip-click below - grabbing
            // the real playhead and dragging it must not leave an old stock flag
            // parked behind, or the visible playhead reads as "stuck" at two places.
            g_stockSec = -1; g_stockFlash = false;
            g_gest.gIn = curSec;
            emitScrub(curSec, false);
        } else if (my < aY) {
            // RULER BAND (items 28/29). Jordan reversed the earlier "drag pans" design:
            // a CLICK moves the PLAYHEAD there instantly, and DRAG SCRUBS the playhead -
            // panning is the MIDDLE mouse button's job (s_midPan above) and works great.
            // If the timeline is PLAYING, the click/drag SEEKS and KEEPS PLAYING from the
            // new spot (engine jumps, audio follows); paused, it is a frame-exact reposition.
            // (Grabbing the playhead HANDLE itself is kind 1 above and already works - "do
            // not break the playhead body".)
            g_gest.kind = 11;
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            g_gest.gIn = curSec;                      // drag throttle baseline (was the scroll pos)
            if (g_playingExt) { g_stockSec = curSec; engineReelSeek(curSec); }
            else { g_stockSec = -1; g_stockFlash = false; emitScrub(curSec, false); }
        } else if (onThresholdBar(mx, my)) {
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
            // Item 2: same stale-stock fix - an empty-timeline click/scrub is also a
            // deliberate reposition while stopped.
            g_stockSec = -1; g_stockFlash = false;
            g_gest.gIn = curSec;
            emitScrub(curSec, false);
        }
    }

    if (active && g_gest.kind != 0) {
        if (g_gest.kind == 1) {
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            if (std::abs(curSec - g_gest.gIn) > 1e-9) { g_gest.gIn = curSec; emitScrub(curSec, false); }
        } else if (g_gest.kind == 11) {
            // Item 29: DRAG SCRUBS the playhead (NOT pan). It follows the cursor; seeks the
            // engine while playing (keeps playing), frame-exact recompose while paused.
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            if (std::abs(curSec - g_gest.gIn) > 1e-9) {
                g_gest.gIn = curSec;
                if (g_playingExt) { g_stockSec = curSec; engineReelSeek(curSec); }
                else { g_stockSec = -1; emitScrub(curSec, false); }
            }
        } else if (g_gest.kind == 7) {
            float y = std::max(thrLaneTop, std::min(thrLaneBot, my));
            double frac = (thrLaneBot - y) / std::max(1.0f, thrLaneBot - thrLaneTop);
            g_thrLevel = frac <= 0.002 ? 0.0 : std::pow(10.0, (kThrFloorDb + frac * -kThrFloorDb) / 20.0);
            g_quietDirty = true;
            emitThreshold(false);
        } else if (g_gest.kind == 2 && std::abs(mx - g_gest.pressX) > 4) {
            g_gest.kind = 3; g_gest.dragged = true;
            if (g_gest.group.empty()) g_gest.group.push_back(g_gest.idx);
        } else if (g_gest.kind == 4 && g_gest.idx >= 0 && g_gest.idx < (int)g_track[0].size()
                   && std::abs(mx - g_gest.pressX) > 4) {
            // Item 3 fix (round 3): a plain CLICK on the trim handle (no real drag)
            // must never trim a frame off the clip. gIn/gOut start out equal to
            // c.in/c.out at press (see clipHit above); this block used to recompute
            // them on the very first "active" frame regardless of movement, and
            // snapComp's pixel->second->frame-snap round trip can land a hair off
            // c.in even with a motionless mouse - enough to clear the release
            // handler's 0.001 no-op check and commit a phantom 1-frame trim. Same
            // DRAG_PX=4 slop every other click-vs-drag gesture here already uses
            // (see kind==2 -> kind==3 promotion above); below it, this stays a click.
            Clip& c = g_track[0][g_gest.idx];
            double edgeComp = snapComp(xToSec(mx), g_pps, curSec, g_gest.idx);
            double nIn = c.in + (edgeComp - c.compStart);
            nIn = std::max(0.0, std::min(nIn, c.out - 0.05));
            g_gest.gIn = nIn; g_gest.gOut = c.out;
        } else if (g_gest.kind == 5 && g_gest.idx >= 0 && g_gest.idx < (int)g_track[0].size()
                   && std::abs(mx - g_gest.pressX) > 4) {
            // Same click-vs-drag guard as kind==4 above, right-edge handle.
            Clip& c = g_track[0][g_gest.idx];
            double edgeComp = snapComp(xToSec(mx), g_pps, curSec, g_gest.idx);
            double nOut = c.in + (edgeComp - c.compStart);
            auto pk = peaksGet(c.source);
            double srcDur = (pk && pk->ready) ? pk->duration : 0;
            if (srcDur > 0.1) nOut = std::min(nOut, srcDur);
            nOut = std::max(nOut, c.in + 0.05);
            g_gest.gIn = c.in; g_gest.gOut = nOut;
        } else if (g_gest.kind == 8 && std::abs(mx - g_gest.pressX) > 4) {
            // 12, NOT 11. Dragging a caption used to promote the gesture to kind
            // 11 - which is ALSO the ruler-pan kind added later in the file's
            // life. Because the pan branch is tested FIRST in this chain, the
            // caption-move branch below became unreachable: dragging a caption
            // PANNED THE TIMELINE and the cue never moved. Two features, one
            // number; the newer one silently ate the older one.
            g_gest.kind = 12; g_gest.dragged = true;   // body press became a MOVE
        } else if (g_gest.kind == 12) {
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
        } else if (g_gest.kind == 9 && std::abs(mx - g_gest.pressX) > 4) {
            // Item 30: only trim once the mouse has actually DRAGGED > 4px - the same guard
            // the clip edges (kinds 4/5) use. Without it a bare CLICK on the start edge
            // quantized to a neighbouring frame and the release committed it, shaving one
            // frame off the caption (the identical bug we already fixed for clips).
            double t = quantToFrame(capSnapCut(xToSec(mx)));
            double lim = quantToFrame(g_gest.gOut - 1.0 / reelFps());   // never shorter than one frame
            g_gest.gIn = std::max(0.0, std::min(t, lim));
        } else if (g_gest.kind == 10 && std::abs(mx - g_gest.pressX) > 4) {
            double t = quantToFrame(capSnapCut(xToSec(mx)));
            double lim = quantToFrame(g_gest.gIn + 1.0 / reelFps());
            g_gest.gOut = std::max(t, lim);
        }
    }

    // Item 31: show the closed-hand (grab) cursor while dragging a clip to a new slot
    // (kind 3) or middle-mouse panning the timeline - the "I'm moving this" feedback.
    if (g_gest.kind == 3 || s_midPan) drawGrabCursor();

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
                // REVERSED, live, 2026-07-23 (item 9): the earlier "a clip-body click
                // never moves the playhead" rule cost him the ability to click a clip
                // to work on it. His corrected word: PAUSED, a clip-body click DOES
                // move the playhead to the click - the click is where he wants to work.
                // PLAYING, it still only sets the STOCK (unchanged) - moving the live
                // playhead mid-playback would disrupt it, and the stock is where edit
                // keys apply and where Space returns to (E-6).
                if (g_playingExt) {
                    g_stockSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
                    g_stockFlash = true;
                } else {
                    curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
                    // Item 2 (round 2): a STOPPED clip-click moved curSec but left any
                    // earlier g_stockSec exactly where it was - the stock draws its OWN
                    // flag (the solid black one, right above the white real playhead in
                    // the draw code), so a stale stock from an earlier ruler click or
                    // playhead drag kept showing as a playhead that "didn't move", even
                    // though curSec (the white flag) had. Jordan, verbatim: "the
                    // playhead body remains where i last clicked the playhead". The
                    // stock's whole purpose is a MID-PLAYBACK return point (see its
                    // declaration comment) - there is no playback running here, so
                    // clearing it is correct, not a workaround.
                    g_stockSec = -1; g_stockFlash = false;
                    emitScrub(curSec, true);
                }
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
                g_lastEngineEditAt = nowSec();   // a CLIP edit (drag-reorder bypasses queueEdit); keeps Ctrl+Z caption-vs-clip ordering right
                g_track[0] = rest; packTrack(0); recomputeDur();
                // A-1: the reorder is optimistic-local (no timeline reload), so the
                // derived caption lane must follow the clips RIGHT NOW - found live:
                // dragging the blue clip first left every caption at its old position.
                rebuildDerivedCaptions();
                // cycle 18 review's gap 4: local state (g_track[0], above) is already
                // updated optimistically, so this engine sync - like emitSelect/
                // emitThreshold - is best-effort telemetry the UI must never block a
                // frame on. A drag-reorder during a fast multi-select edit burst (E-8/
                // I-7/I-9) is exactly the case a 4s stall on the UI thread would hit.
                if (g.group.size() > 1) {
                    json ids = json::array();
                    for (auto& c : moved) ids.push_back(c.id);
                    int toArg = to;
                    try {
                        std::thread([ids, toArg] { json r = engineCall("reorder_many", { {"ids", ids}, {"to", toArg} }, 4.0); (void)r; }).detach();
                    } catch (const std::exception& e) {
                        editLog(std::string("reorder_many: thread spawn failed, skipping sync: ") + e.what());
                    }
                } else {
                    std::string movedId = moved[0].id; int toArg = to;
                    try {
                        std::thread([movedId, toArg] { json r = engineCall("reorder", { {"id", movedId}, {"to", toArg} }, 4.0); (void)r; }).detach();
                    } catch (const std::exception& e) {
                        editLog(std::string("reorder: thread spawn failed, skipping sync: ") + e.what());
                    }
                }
            }
            g_quietDirty = true;
        } else if ((g.kind == 4 || g.kind == 5) && g.idx >= 0 && g.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g.idx];
            if (std::abs(g.gIn - c.in) > 0.001 || std::abs(g.gOut - c.out) > 0.001) {
                g_lastEngineEditAt = nowSec();   // a CLIP trim (drag bypasses queueEdit) - see reorder above
                c.in = g.gIn; c.out = g.gOut;
                packTrack(0); recomputeDur();
                if (curSec > g_compDur) curSec = g_compDur;
                g_quietDirty = true;
                rebuildDerivedCaptions();   // A-1: same reason as the reorder above
                // Same fix as reorder/reorder_many above: local state is already
                // updated, so this is best-effort telemetry, never a UI-thread stall.
                {
                    std::string trimId = c.id; double trimIn = c.in, trimOut = c.out;
                    try {
                        std::thread([trimId, trimIn, trimOut] { json r = engineCall("set_trim", { {"id", trimId}, {"in", trimIn}, {"out", trimOut} }, 4.0); (void)r; }).detach();
                    } catch (const std::exception& e) {
                        editLog(std::string("set_trim: thread spawn failed, skipping sync: ") + e.what());
                    }
                }
            } else {
                // Jordan (2026-07-24): a plain CLICK that landed in a clip's edge zone
                // but NEVER dragged is NAVIGATION, not a trim. It only reached kind 4/5
                // because the cursor was near the cut - being close to (or right on) the
                // cut must never swallow the click. So do exactly what a clip-BODY click
                // does: select the clip AND move the playhead to the click (paused), or
                // set the stock (playing). Mirrors the kind==2 release path above so the
                // two can't drift.
                g_sel.clear(); g_sel.insert(c.id); g_selAnchor = c.id;
                emitSelect();
                if (g_playingExt) {
                    g_stockSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
                    g_stockFlash = true;
                } else {
                    curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
                    g_stockSec = -1; g_stockFlash = false;
                    emitScrub(curSec, true);
                }
            }
        } else if (g.kind == 8 && g.idx >= 0 && g.idx < (int)g_caps.size()) {
            // A caption CLICK (pressed and released without dragging) opens the
            // inline text box on that cue - "click and type the correct caption".
            g_capSel = g.idx; g_capEdit = g.idx; g_capEditFocus = true;
            g_capEditSnapped = false;   // issue 7: snapshot on the FIRST keystroke of this session
            std::string t = g_caps[g.idx].text;
            for (auto& ch : t) if (ch == '\n' || ch == '\r') ch = ' ';
            snprintf(g_capEditBuf, sizeof g_capEditBuf, "%s", t.c_str());
        } else if ((g.kind == 9 || g.kind == 10 || g.kind == 12) && g.idx >= 0 && g.idx < (int)g_caps.size()) {
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
                pushCapUndo();                       // issue 7: a drag/resize is undoable
                double oldStart = cp.start, oldEnd = cp.end;
                cp.start = g.gIn; cp.end = g.gOut;
                reanchorCap(cp);                     // issues 1-2/5: rebind to the clip so the move sticks
                // RIPPLE (Jordan 2026-07-24): a shared edge belongs to BOTH captions.
                // Dragging this caption's START pulls the previous caption's END with it;
                // dragging its END pushes the next caption's START. So nudging one
                // boundary fixes both and never opens a gap or overlap between them.
                if (g.kind == 9) {                   // start edge moved
                    for (auto& nb : g_caps)
                        if (&nb != &cp && nb.start < cp.start && std::abs(nb.end - oldStart) < 0.03) {
                            nb.end = cp.start; reanchorCap(nb); break;
                        }
                } else if (g.kind == 10) {           // end edge moved
                    for (auto& nb : g_caps)
                        if (&nb != &cp && nb.end > cp.end && std::abs(nb.start - oldEnd) < 0.03) {
                            nb.start = cp.end; reanchorCap(nb); break;
                        }
                }
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
    // Each tick is t0 + k*step from an INTEGER k, never `s += step` in a loop -
    // accumulating a double step (0.1 has no exact binary value) drifts a
    // fraction of a step further off with every iteration, and by the far edge
    // of a long timeline a tick that should be exactly on the step lands just
    // under it - which is what fed the duplicate-label bug fmtTime just fixed.
    // Computing every tick straight from t0 keeps the error bounded to one
    // rounding step, not an accumulating one.
    long long nTicks = (long long)std::ceil((g_scrollSec + viewDur + step - t0) / step) + 1;
    double frameDur = 1.0 / reelFps();
    // Round 5: per-FRAME ticks were flooding the ruler ("excessive hash marks") because
    // they switched on as soon as a frame was 4px wide - at a normal working zoom that
    // is a tick every few pixels. The reference ruler uses a handful of even
    // subdivisions per label until you are zoomed in FAR enough that individual frames
    // are genuinely spaced out; require >= 12px per frame before showing them.
    bool frameTicks = reelFps() > 1.0 && g_pps * frameDur >= 12.0;
    for (long long k = 0; k < nTicks; k++) {
        double s = t0 + (double)k * step;
        float x = secToX(s);
        if (x < tlX - 60 || x > tlX + tlW + 60) continue;
        dl->AddLine(ImVec2(x, p.y + 6), ImVec2(x, p.y + rulerH), COL_TICK);
        char b[24]; fmtTime(s, b, sizeof b, step < 1.0);
        dl->AddText(ImVec2(x + 3, p.y + 3), COL_RULERTX, b);
        if (frameTicks) {
            // Zoomed in enough that a frame is a real, clickable width (>= 4px):
            // Jordan cut every clip by hand in Vegas, one frame at a time, and
            // reads these minor ticks AS frame marks - so at this zoom they must
            // BE frame boundaries (multiples of 1/fps from comp time 0, the same
            // grid quantToFrame snaps to), not the old meaningless step/5 split.
            long long f = (long long)std::ceil(s / frameDur);
            while (f * frameDur <= s + 1e-9) f++;
            for (; f * frameDur < s + step - 1e-9; f++) {
                float xm = secToX((double)f * frameDur);
                dl->AddLine(ImVec2(xm, p.y + rulerH - 5), ImVec2(xm, p.y + rulerH), COL_TICKMIN);
            }
        } else {
            for (int m = 1; m < 5; m++) {
                float xm = secToX(s + step * m / 5.0);
                dl->AddLine(ImVec2(xm, p.y + rulerH - 5), ImVec2(xm, p.y + rulerH), COL_TICKMIN);
            }
        }
    }

    dl->AddRectFilled(ImVec2(tlX, aY), ImVec2(tlX + tlW, aY + laneH), COL_LANE, 3);

    if (g_track[0].empty()) {
        // Name the gesture that actually fills it. Double-clicking a search hit
        // adds that clip to the timeline (addHitToTimeline) - that is how the reel
        // gets BUILT, and "load a reel from the engine" told him about the other
        // path only. Wording follows the reference's .tlempty hint.
        const char* msg = "timeline empty - double-click a quote in the search results to add clips, or use Load Reel";
        ImVec2 ts = ImGui::CalcTextSize(msg);
        // Brightened from (120,128,140) - 4.3:1 on the near-black lane - to about
        // 8:1. It is the ONLY message on screen when nothing else is, so it is the
        // one that must not need effort to read.
        dl->AddText(ImVec2(tlX + (tlW - ts.x) / 2, aY + (laneH - ts.y) / 2), IM_COL32(178, 186, 200, 255), msg);
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
        // Jordan cuts butt-joined clips with ZERO gap in Vegas - the reel data
        // proves it (compStart of clip i+1 equals clip i's compStart+dur exactly).
        // But every clip used to get a 1px inset on BOTH sides unconditionally,
        // so two touching clips always left a 2px unfilled seam (the lane
        // background showing through), and rounding all 4 corners on both clips
        // widened that seam further with a rounded notch on each side - together
        // reading as the "visible dark gap between clips that are supposed to be
        // butt-joined" he flagged. An edge that touches a real neighbour now
        // draws flush (no inset, no rounding on that side); only an edge facing
        // actual empty timeline (or a real gap) keeps the 1px inset + rounding.
        bool touchesPrev = !ghost && i > 0 &&
            std::abs((g_track[0][i - 1].compStart + (g_track[0][i - 1].out - g_track[0][i - 1].in)) - drawStart) < 1e-4;
        bool touchesNext = !ghost && i + 1 < g_track[0].size() &&
            std::abs((drawStart + drawDur) - g_track[0][i + 1].compStart) < 1e-4;
        float fx0 = x0 + (touchesPrev ? 0.0f : 1.0f), fx1 = x1 - (touchesNext ? 0.0f : 1.0f);
        ImDrawFlags rf = ImDrawFlags_RoundCornersNone;
        if (!touchesPrev) rf |= ImDrawFlags_RoundCornersLeft;
        if (!touchesNext) rf |= ImDrawFlags_RoundCornersRight;
        // SELECTION = OPAQUE FILL, NEVER AN OUTLINE. Jordan, verbatim: "Pleae
        // remove the yellow outline around the selected clip" [feedback2], and
        // a clip's border must match its own colour [feedback4]. The clip
        // colours are his ACCESSIBILITY AID - he identifies a clip by its
        // colour at a glance - so selection has to read THROUGH that colour by
        // going solid, not by drawing a different colour on top of it.
        ImU32 fill = IM_COL32(c.r, c.g, c.b, selected ? 255 : 62);
        // P3: preview clips (no engine id) render at reduced opacity so Jordan
        // can instantly tell them from real, engine-backed clips.
        bool isPreview = c.id.empty();
        if (isPreview) fill = IM_COL32(c.r, c.g, c.b, selected ? 200 : 42);
        if (inDrag) fill = (fill & 0x00FFFFFF) | 0x60000000;
        dl->AddRectFilled(ImVec2(fx0, aY + 1), ImVec2(fx1, aY + laneH - 1), fill, 3, rf);
        float vx0 = std::max(fx0, tlX), vx1 = std::min(fx1, tlX + tlW);
        if (vx1 > vx0 && wy1 - wy0 > 6) {
            drawWave(dl, c.source, cin, cout, x0, vx0, vx1, wy0, wy1, g_pps,
                     inDrag ? COL_WAVEDIM : (selected ? IM_COL32(255, 255, 255, 190) : COL_WAVE));
            // A source whose audio decode FAILED (most often: no audio track at
            // all - silent screen captures, the ges-bench demo proxy) used to
            // draw an unlabeled flat block, indistinguishable from "waveforms
            // are broken" - that exact ambiguity was escalated as a regression
            // on 2026-07-22 and cost a diagnostic session. Name the state, once,
            // dim, only when there is room: a labeled degrade is legible, a
            // silent one looks like a bug. (peaksGet here is the same per-clip
            // per-frame cost class clipPreparing below already pays.)
            if (auto pkf = peaksGet(c.source); pkf && pkf->failed && wy1 - wy0 > 14 && vx1 - vx0 > 96) {
                const char* nam = "no audio / no waveform";
                ImVec2 nts = ImGui::CalcTextSize(nam);
                dl->AddText(ImVec2(vx0 + 6, (wy0 + wy1 - nts.y) * 0.5f), IM_COL32(158, 166, 180, 170), nam);
            }
        }
        // The border ALWAYS matches the clip's own colour - no white ring on the
        // selected clip. The opaque fill above is what says "selected".
        ImU32 brd = IM_COL32(c.r, c.g, c.b, isPreview ? 140 : 242);
        dl->AddRect(ImVec2(fx0, aY + 1), ImVec2(fx1, aY + laneH - 1), brd, 3, rf, 1.0f);
        // P3: dashed bottom border for preview clips - the visual cue that this
        // clip is not yet registered with the engine.
        if (isPreview && fx1 - fx0 > 12) {
            ImU32 dash = IM_COL32(c.r, c.g, c.b, 200);
            float by = aY + laneH - 2;
            for (float dx = fx0 + 2; dx < fx1 - 4; dx += 8.0f)
                dl->AddLine(ImVec2(dx, by), ImVec2(std::min(dx + 4.0f, fx1 - 2), by), dash, 1.5f);
        }
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
            ThumbTex* tt = getThumb(c.source);
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
            bool ghost = (g_gest.kind == 9 || g_gest.kind == 10 || g_gest.kind == 12) && (int)i == g_gest.idx;
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

    // 2026-07-03: "Add a second Playhead Stock (the black bar)... 2 identical
    // black bars, but only one of them has the white playhead that moves." The
    // stock used to draw as a plain 2px line - wrong SHAPE, not just wrong
    // color. It now draws the SAME flag geometry (rect + triangle tip) as the
    // moving playhead below, just solid black instead of white, so at rest the
    // two read as identical bars and only the real playhead's cap is white.
    // The slow black/white flash after a manual mid-playback move is preserved
    // unchanged - a separate, wanted behavior.
    if (g_stockSec >= 0) {
        float sx = secToX(g_stockSec);
        if (sx >= tlX - 2 && sx <= tlX + tlW + 2) {
            // Item 6 (round 4): the stock is a PLAIN BAR with NO flag head - only the
            // real moving playhead below carries the white flag (CSS #stock vs
            // #playhead). Drawing the stock as a full flag made a "phantom" second
            // playhead Jordan rejected. The BAR itself blinks black<->white when it
            // was moved during playback (CSS #stock.flashing / stockBlink 0.8s);
            // black (COL_PLAYHEAD) at rest.
            bool wht = g_stockFlash && std::fmod(nowSec(), 0.8) >= 0.4;
            ImU32 barCol = wht ? IM_COL32(255, 255, 255, 255) : COL_PLAYHEAD;
            dl->AddLine(ImVec2(sx, p.y + 2), ImVec2(sx, bot), barCol, 2.0f);
        }
    }

    float px = secToX(curSec);
    if (px >= tlX - 2 && px <= tlX + tlW + 2) {
        dl->AddLine(ImVec2(px, p.y + 2), ImVec2(px, bot), COL_PLAYHEAD, 2.0f);
        float fw = 8, ftop = p.y + 1, fmid = p.y + 13, ftip = p.y + 20;
        dl->AddRectFilled(ImVec2(px - fw, ftop), ImVec2(px + fw, fmid), COL_PHFLAG);
        dl->AddTriangleFilled(ImVec2(px - fw, fmid), ImVec2(px + fw, fmid), ImVec2(px, ftip), COL_PHFLAG);
        dl->AddRect(ImVec2(px - fw, ftop), ImVec2(px + fw, fmid), IM_COL32(0, 0, 0, 115));
        // 2026-07-03: "add 2 tiny vertical hashmarks inside the white part of the
        // playhead" (his reference photo, playhead.JPG - 2 small dark ticks with
        // a real gap between them). Filled rects, not thin AddLine strokes: at
        // this size 2 nearly-touching antialiased lines blur into one blob.
        dl->AddRectFilled(ImVec2(px - 4.0f, ftop + 3), ImVec2(px - 2.0f, fmid - 2), COL_PHGRIP);
        dl->AddRectFilled(ImVec2(px + 2.0f, ftop + 3), ImVec2(px + 4.0f, fmid - 2), COL_PHGRIP);
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
        // Item 32: reflect the typed text in the PREVIEW in realtime. drawCaptionsImGui
        // reads g_caps[i].text, so update the in-memory caption EVERY frame while typing
        // (Jordan: "when manually editing captions, they should update in realtime" - no
        // more clicking away first). Only WRITE the .srt (saveCaptions) on commit, never
        // per keystroke. ImGui restores the buffer to the original on Escape, so this same
        // live-write reverts the text on cancel too.
        std::string live = g_capEditBuf;
        for (auto& ch : live) if (ch == '\n' || ch == '\r') ch = ' ';
        if (live != g_caps[g_capEdit].text) {
            // Issue 7: snapshot ONCE per typing session, before the first change, so
            // Ctrl+Z (after the box closes) reverses the whole retype. In-box Ctrl+Z
            // is ImGui's own char-level undo (the box has keyboard capture).
            if (!g_capEditSnapped) { pushCapUndo(); g_capEditSnapped = true; }
            g_caps[g_capEdit].text = live;
        }
        if (enter || ImGui::IsItemDeactivated()) {
            saveCaptions();          // persist the final text to the .srt on commit
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
