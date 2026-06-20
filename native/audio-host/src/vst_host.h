// vst_host.h - VST3 plugin hosting (verbs vst.scan/load/param.*/note.*/editor.open/render).
//
// Uses the MIT VST3 SDK hosting helpers (Module/PlugProvider/HostProcessData). Loads a
// .vst3 module, instantiates component + controller, sets up 32-bit float processing,
// activates busses, and runs the processor either offline (render -> WAV) or against the
// live device callback (audio.start).
//
// Called by: src/main.cpp (verb dispatch).
#pragma once

#include <map>
#include <memory>
#include <string>

#include "../third_party/nlohmann/json.hpp"

namespace becky {

using json = nlohmann::json;

// Opaque per-instance state lives in the .cpp (PImpl) so this header stays SDK-free.
struct VstInstance;

class VstHost {
public:
    VstHost();
    ~VstHost();

    VstHost(const VstHost&) = delete;
    VstHost& operator=(const VstHost&) = delete;

    // verb vst.scan {dir?} -> {dir, count, plugins:[{name,path,category,...}]}.
    json scan(const json& args);

    // verb vst.load {path, samplerate?} -> {instanceId, name, params, hasEditor, ...}.
    json load(const json& args);

    // verb vst.param.list {instanceId} -> {instanceId, params:[...]}.
    json param_list(const json& args);

    // verb vst.param.set {instanceId, paramId, value}.
    json param_set(const json& args);

    // verb note.on {instanceId, pitch, velocity}.
    json note_on(const json& args);

    // verb note.off {instanceId, pitch}.
    json note_off(const json& args);

    // verb vst.editor.open {instanceId} -> best-effort native editor window.
    json editor_open(const json& args);

    // verb render {instanceId?, path?, events?, durationSec, sampleRate?, out, ...}.
    // OFFLINE: no device. Loads a plugin if `path` is given (else uses instanceId),
    // runs its processor for durationSec applying note/param events, writes a WAV,
    // returns {out, frames, channels, sampleRate, peak, rms, nonSilent}.
    json render(const json& args);

    // Default VST3 scan directory for the platform.
    static std::string default_vst3_dir();

    // Path to this executable, used to spawn `--probe` children for crash-isolated
    // scanning. If empty, scan falls back to in-process loading.
    void set_self_exe(const std::string& p) { self_exe_ = p; }

private:
    VstInstance* require(int instance_id);

    int next_id_ = 1;
    std::string self_exe_;
    std::map<int, std::unique_ptr<VstInstance>> instances_;
};

}  // namespace becky
