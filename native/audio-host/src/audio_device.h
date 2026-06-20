// audio_device.h - PortAudio output device control (verbs audio.devices/open/start/stop).
//
// Owns the output stream lifecycle. The realtime callback pulls float frames from
// an optional registered render source (a loaded VST instance) so LIVE plugin audio
// reaches the device; with no source registered it outputs silence.
//
// Called by: src/main.cpp (verb dispatch) and src/vst_host.cpp (set_render_source).
#pragma once

#include <atomic>
#include <functional>
#include <string>

#include "../third_party/nlohmann/json.hpp"

namespace becky {

using json = nlohmann::json;

// A render callback: fill `out` (interleaved, channels*frames floats) for the next
// block. Must be realtime-safe (no locks/alloc/IO).
using RenderSource =
    std::function<void(float* out, unsigned long frames, int channels)>;

class AudioDevice {
public:
    AudioDevice();
    ~AudioDevice();

    AudioDevice(const AudioDevice&) = delete;
    AudioDevice& operator=(const AudioDevice&) = delete;

    // True if Pa_Initialize() succeeded in the ctor.
    bool ok() const { return initialized_; }
    const std::string& init_error() const { return init_error_; }

    // verb audio.devices -> {default_output, default_host_api, devices:[...]}.
    json list_devices() const;

    // verb audio.open {device?, samplerate?, buffer?}. Picks the system default
    // output (his UR12) when device is unset; prefers an ASIO device when ASIO is
    // compiled in. Returns the chosen config or throws std::runtime_error.
    json open(const json& args);

    // verb audio.start / audio.stop.
    json start();
    json stop();

    // Register/clear the realtime render source (e.g. a loaded VST instance).
    void set_render_source(RenderSource src);

    bool is_open() const { return stream_ != nullptr; }
    bool is_running() const { return running_.load(); }

    // Realtime: fill `output` from the active render source (or silence). Public so the
    // PaStreamCallback trampoline (in the .cpp, where portaudio.h is in scope) can call
    // it; not part of the verb API.
    void fill_block(float* output, unsigned long frames);

private:
    bool initialized_ = false;
    std::string init_error_;
    void* stream_ = nullptr;  // PaStream*
    int channels_ = 2;
    int sample_rate_ = 48000;
    int buffer_frames_ = 256;
    int device_index_ = -1;
    std::string device_name_;
    std::string host_api_;
    std::atomic<bool> running_{false};

    RenderSource render_source_;
    std::atomic<RenderSource*> active_source_{nullptr};
};

}  // namespace becky
