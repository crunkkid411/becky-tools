// engine.h - Becky Review 3 native video engine (mpv replacement).
// In-process libavcodec/D3D11VA decode + WASAPI audio; every call is a plain
// function call (no pipes, no child HWND, no subprocess).
// Proven piece by piece in native/becky-engine-probe (HANDOFF-VIDEO-ENGINE.md
// steps 2-5); this is the step-6 integration surface.
// ASCII only (house rule).
#pragma once
#include <cstdint>
#include <string>
#include <vector>

struct ID3D11Device;
struct ID3D11ShaderResourceView;

struct EngineSeg {
    std::string source; // absolute path
    double in = 0.0;    // source seconds
    double out = 0.0;
};

namespace engine {

// Bring up the decode device + threads. Never blocks on media. Returns false
// only when no video-capable GPU adapter exists (app degrades like mpv-down).
bool init();
void shutdown();
bool available();  // video path up
bool audioUp();    // audio device opened (degrade ladder 2 when false)

// SCRUB mode (replaces mpvSeekExact): show one exact frame of one source,
// paused, no audio. Latest-chasing: rapid calls supersede each other.
void showSource(const std::string& source, double srcSec);

// REEL mode (replaces mpvEdlEnter/mpvEdlExit/mpvEdlSeek): play the in-memory
// segment list with audio, starting at composition-seconds startSec.
void enterReel(const std::vector<EngineSeg>& segs, double fps, double startSec, double rate);
void exitReel();                 // pause + leave reel mode (frame stays up)
void seekReel(double compSec);   // jump while playing (audio follows)
void setRate(double rate);       // 1.0 = audio-master; != 1.0 plays video-only (silent)
bool reelActive();
bool reelEnded();                // playback hit the end of the reel
double clockSec();               // composition seconds while reel is active, else -1

// The current frame as a BGRA SRV usable on the APP's render device (shared
// texture opened lazily). May return null before the first frame decodes.
// w/h receive the video's display dimensions.
ID3D11ShaderResourceView* currentFrameSRV(ID3D11Device* appDev, int* w, int* h);

} // namespace engine
