// vst_host.cpp - VST3 hosting implementation built on the MIT VST3 SDK helpers.
#include "vst_host.h"

#include <algorithm>
#include <cctype>
#include <cmath>
#include <cstring>
#include <stdexcept>
#include <vector>

// VST3 SDK hosting + interfaces.
#include "pluginterfaces/base/funknownimpl.h"
#include "pluginterfaces/base/ipluginbase.h"
#include "pluginterfaces/gui/iplugview.h"
#include "pluginterfaces/vst/ivstaudioprocessor.h"
#include "pluginterfaces/vst/ivstcomponent.h"
#include "pluginterfaces/vst/ivsteditcontroller.h"
#include "pluginterfaces/vst/ivstevents.h"
#include "pluginterfaces/vst/ivstprocesscontext.h"
#include "public.sdk/source/vst/hosting/eventlist.h"
#include "public.sdk/source/vst/hosting/hostclasses.h"
#include "public.sdk/source/vst/hosting/module.h"
#include "public.sdk/source/vst/hosting/parameterchanges.h"
#include "public.sdk/source/vst/hosting/plugprovider.h"
#include "public.sdk/source/vst/hosting/processdata.h"
#include "public.sdk/source/vst/utility/stringconvert.h"
#include "public.sdk/source/vst/vstpresetfile.h"

#include "plugin_probe.h"
#include "protocol.h"
#include "wav_writer.h"

#if SMTG_OS_WINDOWS
#include <windows.h>
#endif

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

using namespace Steinberg;
using namespace Steinberg::Vst;

namespace becky {

namespace {

// One shared HostApplication context for the lifetime of the host process.
HostApplication& host_context() {
    static HostApplication app;
    return app;
}

void ensure_plugin_context() {
    static bool set = [] {
        PluginContextFactory::instance().setPluginContext(&host_context());
        return true;
    }();
    (void)set;
}

std::string title_to_string(const String128& s) {
    return StringConvert::convert(s, 128);
}

}  // namespace

//------------------------------------------------------------------------
// Per-instance state (PImpl): module, component, controller, processor, prepared
// HostProcessData, and pending note/param events.
struct VstInstance {
    int id = 0;
    std::string path;
    std::string name;
    FUID component_uid;  // processor (IComponent) class id, for .vstpreset save/load
    VST3::Hosting::Module::Ptr module;
    IPtr<PlugProvider> provider;
    IPtr<IComponent> component;
    IPtr<IEditController> controller;
    IPtr<IAudioProcessor> processor;

    int sample_rate = 48000;
    int block_size = 512;
    int out_channels = 2;
    bool processing = false;
    bool has_editor = false;

    HostProcessData process_data;
    EventList event_list{256};
    ParameterChanges in_params;
    ParameterChanges out_params;

    std::vector<Event> pending_events;

    ~VstInstance() {
        if (processor && processing) {
            processor->setProcessing(false);
        }
        if (component) {
            component->setActive(false);
        }
        process_data.unprepare();
    }
};

//------------------------------------------------------------------------
VstHost::VstHost() { ensure_plugin_context(); }
VstHost::~VstHost() = default;

//------------------------------------------------------------------------
std::string VstHost::default_vst3_dir() {
#if SMTG_OS_WINDOWS
    return "C:\\Program Files\\Common Files\\VST3";
#elif SMTG_OS_MACOS
    return "/Library/Audio/Plug-Ins/VST3";
#else
    return "/usr/lib/vst3";
#endif
}

//------------------------------------------------------------------------
VstInstance* VstHost::require(int instance_id) {
    auto it = instances_.find(instance_id);
    if (it == instances_.end()) {
        throw std::runtime_error("unknown instanceId " +
                                 std::to_string(instance_id));
    }
    return it->second.get();
}

//------------------------------------------------------------------------
json VstHost::scan(const json& args) {
    std::string dir = args.value("dir", default_vst3_dir());
    // recursive default ON: vendors (FabFilter, Neural DSP, Arturia...) nest plugins
    // in subfolders, so a top-level-only scan misses most of a real library.
    const bool recursive = args.value("recursive", true);
    json plugins = json::array();
    std::vector<std::string> paths;

#if SMTG_OS_WINDOWS
    auto ends_with_vst3 = [](const std::string& s) {
        if (s.size() < 5) return false;
        std::string tail = s.substr(s.size() - 5);
        for (char& c : tail) c = static_cast<char>(::tolower(c));
        return tail == ".vst3";
    };
    // Iterative directory walk: a "*.vst3" entry is a plugin (file or bundle dir,
    // not descended into); any other directory is a vendor folder to recurse.
    std::vector<std::string> stack{dir};
    int depth_budget = recursive ? 4 : 1;  // bound recursion
    std::vector<int> depth{0};
    while (!stack.empty()) {
        std::string cur = stack.back();
        stack.pop_back();
        int cur_depth = depth.back();
        depth.pop_back();

        std::string pattern = cur;
        if (!pattern.empty() && pattern.back() != '\\' && pattern.back() != '/')
            pattern += "\\";
        pattern += "*";
        WIN32_FIND_DATAA fd{};
        HANDLE h = FindFirstFileA(pattern.c_str(), &fd);
        if (h == INVALID_HANDLE_VALUE) continue;
        do {
            std::string fn = fd.cFileName;
            if (fn == "." || fn == "..") continue;
            std::string full = cur;
            if (!full.empty() && full.back() != '\\' && full.back() != '/')
                full += "\\";
            full += fn;
            const bool is_dir =
                (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) != 0;
            if (ends_with_vst3(fn)) {
                paths.push_back(full);  // plugin (file or bundle)
            } else if (is_dir && cur_depth + 1 < depth_budget) {
                stack.push_back(full);
                depth.push_back(cur_depth + 1);
            }
        } while (FindNextFileA(h, &fd));
        FindClose(h);
    }
#else
    for (const auto& p : VST3::Hosting::Module::getModulePaths()) {
        if (p.find(dir) == 0) paths.push_back(p);
    }
#endif

    int crashed_count = 0;
    for (const auto& full : paths) {
        json entry{{"path", full}};
        // Crash-isolated: each plugin loads in a child process; a faulting plugin is
        // reported as crashed+skipped, not fatal (see plugin_probe).
        ProbeResult pr = probe_plugin_subprocess(self_exe_, full);
        json classes = json::array();
        for (const auto& c : pr.classes) {
            classes.push_back(json{
                {"name", c.name},
                {"category", c.category},
                {"vendor", c.vendor},
                {"version", c.version},
            });
        }
        entry["name"] = pr.name.empty() ? full : pr.name;
        entry["loadable"] = pr.loadable;
        entry["classes"] = classes;
        if (pr.crashed) {
            entry["crashed"] = true;
            ++crashed_count;
        }
        if (!pr.error.empty()) entry["error"] = pr.error;
        if (!classes.empty()) entry["category"] = classes[0].value("category", "");
        plugins.push_back(entry);
    }
    if (crashed_count > 0) {
        log_line("scan: " + std::to_string(crashed_count) +
                 " plugin(s) faulted on load and were skipped");
    }

    return json{{"dir", dir},
                {"count", plugins.size()},
                {"crashed", crashed_count},
                {"plugins", plugins}};
}

//------------------------------------------------------------------------
namespace {

// Load a module + instantiate the first audio-effect class, set up 32-bit float
// processing, activate busses, start processing. Throws on failure.
void instantiate(VstInstance& inst, const std::string& path, int sample_rate,
                 int block_size) {
    std::string err;
    inst.module = VST3::Hosting::Module::create(path, err);
    if (!inst.module) {
        throw std::runtime_error("cannot load module: " +
                                 (err.empty() ? path : err));
    }
    inst.path = path;

    const auto factory = inst.module->getFactory();
    VST3::Hosting::ClassInfo chosen;
    bool found = false;
    for (const auto& ci : factory.classInfos()) {
        if (ci.category() == kVstAudioEffectClass) {
            chosen = ci;
            found = true;
            break;
        }
    }
    if (!found) throw std::runtime_error("no audio-effect class in module");
    inst.name = chosen.name();
    // Component (processor) class id: needed to write/verify a .vstpreset stream.
    inst.component_uid = FUID::fromTUID(chosen.ID().data());

    inst.provider = owned(new PlugProvider(factory, chosen, true));
    if (!inst.provider->initialize()) {
        throw std::runtime_error("PlugProvider initialize failed");
    }
    inst.component = inst.provider->getComponentPtr();
    inst.controller = inst.provider->getControllerPtr();
    if (!inst.component) throw std::runtime_error("no IComponent");
    inst.processor = FUnknownPtr<IAudioProcessor>(inst.component);
    if (!inst.processor) {
        throw std::runtime_error("component is not an IAudioProcessor");
    }

    inst.sample_rate = sample_rate;
    inst.block_size = block_size;

    if (inst.processor->canProcessSampleSize(kSample32) != kResultOk) {
        throw std::runtime_error("plugin cannot process 32-bit float");
    }
    ProcessSetup setup{kRealtime, kSample32, block_size,
                       static_cast<SampleRate>(sample_rate)};
    if (inst.processor->setupProcessing(setup) != kResultOk) {
        throw std::runtime_error("setupProcessing failed");
    }

    auto activate_all = [&](MediaType mt, BusDirection dir) {
        int32 count = inst.component->getBusCount(mt, dir);
        for (int32 i = 0; i < count; ++i)
            inst.component->activateBus(mt, dir, i, true);
    };
    activate_all(kAudio, kInput);
    activate_all(kAudio, kOutput);
    activate_all(kEvent, kInput);
    activate_all(kEvent, kOutput);

    inst.out_channels = 2;
    if (inst.component->getBusCount(kAudio, kOutput) > 0) {
        BusInfo bi{};
        if (inst.component->getBusInfo(kAudio, kOutput, 0, bi) == kResultOk) {
            inst.out_channels = std::max<int32>(1, bi.channelCount);
        }
    }

    if (inst.component->setActive(true) != kResultOk) {
        throw std::runtime_error("setActive(true) failed");
    }
    if (inst.processor->setProcessing(true) != kResultOk) {
        log_line("warning: setProcessing(true) returned non-ok for " + inst.name);
    }
    inst.processing = true;

    inst.process_data.prepare(*inst.component, block_size, kSample32);
    inst.process_data.inputParameterChanges = &inst.in_params;
    inst.process_data.outputParameterChanges = &inst.out_params;
    inst.process_data.inputEvents = &inst.event_list;

    if (inst.controller) {
        IPlugView* view = inst.controller->createView(ViewType::kEditor);
        if (view) {
            inst.has_editor = true;
            view->release();
        }
    }
}

json param_info_array(IEditController* controller) {
    json params = json::array();
    if (!controller) return params;
    int32 count = controller->getParameterCount();
    for (int32 i = 0; i < count && i < 512; ++i) {
        ParameterInfo pi{};
        if (controller->getParameterInfo(i, pi) != kResultOk) continue;
        params.push_back(json{
            {"id", static_cast<uint32_t>(pi.id)},
            {"title", title_to_string(pi.title)},
            {"units", title_to_string(pi.units)},
            {"default", pi.defaultNormalizedValue},
            {"stepCount", pi.stepCount},
            {"current", controller->getParamNormalized(pi.id)},
        });
    }
    return params;
}

}  // namespace

//------------------------------------------------------------------------
json VstHost::load(const json& args) {
    if (!args.contains("path") || !args.at("path").is_string()) {
        throw std::runtime_error("vst.load requires a string 'path'");
    }
    const std::string path = args.at("path").get<std::string>();
    const int sr = args.value("samplerate", 48000);
    const int bs = args.value("buffer", 512);

    auto inst = std::make_unique<VstInstance>();
    inst->id = next_id_++;
    instantiate(*inst, path, sr, bs);

    json out{
        {"instanceId", inst->id},
        {"name", inst->name},
        {"path", inst->path},
        {"outChannels", inst->out_channels},
        {"sampleRate", inst->sample_rate},
        {"hasEditor", inst->has_editor},
        {"params", param_info_array(inst->controller)},
    };
    instances_[inst->id] = std::move(inst);
    return out;
}

//------------------------------------------------------------------------
json VstHost::param_list(const json& args) {
    const int id = args.value("instanceId", 0);
    VstInstance* inst = require(id);
    return json{{"instanceId", id},
                {"params", param_info_array(inst->controller)}};
}

//------------------------------------------------------------------------
json VstHost::param_set(const json& args) {
    const int id = args.value("instanceId", 0);
    VstInstance* inst = require(id);
    if (!args.contains("paramId")) {
        throw std::runtime_error("vst.param.set requires paramId");
    }
    const ParamID pid = static_cast<ParamID>(args.at("paramId").get<uint32_t>());
    const double value = args.value("value", 0.0);

    if (inst->controller) inst->controller->setParamNormalized(pid, value);
    int32 index = 0;
    IParamValueQueue* q = inst->in_params.addParameterData(pid, index);
    if (q) {
        int32 pt = 0;
        q->addPoint(0, value, pt);
    }
    return json{{"instanceId", id},
                {"paramId", static_cast<uint32_t>(pid)},
                {"value", value}};
}

//------------------------------------------------------------------------
json VstHost::note_on(const json& args) {
    const int id = args.value("instanceId", 0);
    VstInstance* inst = require(id);
    Event e{};
    e.type = Event::kNoteOnEvent;
    e.sampleOffset = 0;
    e.noteOn.channel = static_cast<int16>(args.value("channel", 0));
    e.noteOn.pitch = static_cast<int16>(args.value("pitch", 60));
    e.noteOn.velocity = static_cast<float>(args.value("velocity", 0.8));
    e.noteOn.noteId = -1;
    inst->pending_events.push_back(e);
    return json{{"instanceId", id}, {"pitch", e.noteOn.pitch}, {"queued", true}};
}

//------------------------------------------------------------------------
json VstHost::note_off(const json& args) {
    const int id = args.value("instanceId", 0);
    VstInstance* inst = require(id);
    Event e{};
    e.type = Event::kNoteOffEvent;
    e.sampleOffset = 0;
    e.noteOff.channel = static_cast<int16>(args.value("channel", 0));
    e.noteOff.pitch = static_cast<int16>(args.value("pitch", 60));
    e.noteOff.velocity = static_cast<float>(args.value("velocity", 0.0));
    e.noteOff.noteId = -1;
    inst->pending_events.push_back(e);
    return json{{"instanceId", id}, {"pitch", e.noteOff.pitch}, {"queued", true}};
}

//------------------------------------------------------------------------
json VstHost::editor_open(const json& args) {
    const int id = args.value("instanceId", 0);
    VstInstance* inst = require(id);
    if (!inst->controller) {
        throw std::runtime_error("plugin has no edit controller");
    }
    IPlugView* view = inst->controller->createView(ViewType::kEditor);
    if (!view) {
        throw std::runtime_error("plugin provides no editor (IPlugView)");
    }
    // Honest scope: this is a headless control-plane sidecar with no UI run-loop or
    // parent HWND/message pump, which IPlugView::attached requires. Owning + pumping a
    // real editor window is a Phase-3 GUI task that belongs in the Gio shell. We report
    // the editor exists + its requested size rather than fake an attached window.
    ViewRect rect{};
    bool has_size = view->getSize(&rect) == kResultOk;
    view->release();
    return json{
        {"instanceId", id},
        {"hasEditor", true},
        {"attached", false},
        {"reason",
         "editor exists but needs a window+run-loop owner (Gio shell, Phase 3); "
         "this control-plane sidecar has no message pump"},
        {"width", has_size ? (rect.right - rect.left) : 0},
        {"height", has_size ? (rect.bottom - rect.top) : 0},
    };
}

//------------------------------------------------------------------------
namespace {

// Format a FUID as the 32-char ASCII class id used in a .vstpreset header, for
// diagnostics/traceability (matches FUID::toString length).
std::string fuid_to_string(const FUID& uid) {
    char buf[64] = {0};
    uid.toString(buf);
    return std::string(buf);
}

// Run one silent process block so any parameter changes queued by vst.param.set
// (which live in inst.in_params until consumed) are baked into the component's DSP
// state. Without this, component->getState() in vst.state.save would miss live
// param edits that a render WOULD reflect. Safe no-op if nothing is queued.
void flush_pending_params(VstInstance& inst) {
    if (!inst.processor || !inst.processing) return;
    const int32 block = std::min<int32>(64, inst.block_size);
    inst.process_data.numSamples = block;
    inst.event_list.clear();
    inst.process_data.inputEvents = &inst.event_list;
    // Zero inputs (effects) + outputs so the block is silent.
    for (int32 b = 0; b < inst.process_data.numInputs; ++b) {
        AudioBusBuffers& ib = inst.process_data.inputs[b];
        for (int32 c = 0; c < ib.numChannels; ++c)
            if (ib.channelBuffers32[c])
                std::memset(ib.channelBuffers32[c], 0, sizeof(float) * block);
    }
    for (int32 b = 0; b < inst.process_data.numOutputs; ++b) {
        AudioBusBuffers& ob = inst.process_data.outputs[b];
        for (int32 c = 0; c < ob.numChannels; ++c)
            if (ob.channelBuffers32[c])
                std::memset(ob.channelBuffers32[c], 0, sizeof(float) * block);
    }
    inst.processor->process(inst.process_data);  // best-effort; consumes in_params
    inst.in_params.clearQueue();
}

}  // namespace

//------------------------------------------------------------------------
// verb vst.state.save: write the loaded plugin's component + controller state to a
// .vstpreset-format file via the SDK PresetFile helper. Works for ANY VST3.
json VstHost::state_save(const json& args) {
    if (!args.contains("out") || !args.at("out").is_string()) {
        throw std::runtime_error("vst.state.save requires a string 'out' path");
    }
    const std::string out_path = args.at("out").get<std::string>();

    std::unique_ptr<VstInstance> temp;
    VstInstance* inst = nullptr;
    if (args.contains("instanceId")) {
        inst = require(args.at("instanceId").get<int>());
    } else if (args.contains("path") && args.at("path").is_string()) {
        temp = std::make_unique<VstInstance>();
        temp->id = -1;
        instantiate(*temp, args.at("path").get<std::string>(),
                    args.value("samplerate", 48000), args.value("buffer", 512));
        inst = temp.get();
    } else {
        throw std::runtime_error("vst.state.save requires 'instanceId' or 'path'");
    }
    if (!inst->component) throw std::runtime_error("instance has no component");

    // Bake any queued param edits into the component before snapshotting its state.
    flush_pending_params(*inst);

    IBStream* stream = FileStream::open(out_path.c_str(), "wb");
    if (!stream) {
        throw std::runtime_error("cannot open '" + out_path + "' for writing");
    }
    const bool ok = PresetFile::savePreset(stream, inst->component_uid,
                                           inst->component, inst->controller);
    stream->release();
    if (!ok) {
        throw std::runtime_error(
            "PresetFile::savePreset failed (plugin refused to serialize state)");
    }

    return json{
        {"out", out_path},
        {"name", inst->name},
        {"classId", fuid_to_string(inst->component_uid)},
        {"saved", true},
    };
}

//------------------------------------------------------------------------
// verb vst.state.load: read a .vstpreset-format file and apply it via
// setComponentState (-> processor) + the controller's setState, BEFORE the next
// render. setState must run while the component is inactive, so we deactivate,
// apply, then reactivate. Accepts 'instanceId' (apply to an existing instance) or
// 'path' (instantiate a NEW persistent instance, apply, return its id).
json VstHost::state_load(const json& args) {
    if (!args.contains("file") || !args.at("file").is_string()) {
        throw std::runtime_error("vst.state.load requires a string 'file' path");
    }
    const std::string file = args.at("file").get<std::string>();

    std::unique_ptr<VstInstance> created;
    VstInstance* inst = nullptr;
    bool is_new = false;
    if (args.contains("instanceId")) {
        inst = require(args.at("instanceId").get<int>());
    } else if (args.contains("path") && args.at("path").is_string()) {
        created = std::make_unique<VstInstance>();
        created->id = next_id_++;
        instantiate(*created, args.at("path").get<std::string>(),
                    args.value("samplerate", 48000), args.value("buffer", 512));
        inst = created.get();
        is_new = true;
    } else {
        throw std::runtime_error("vst.state.load requires 'instanceId' or 'path'");
    }
    if (!inst->component) throw std::runtime_error("instance has no component");

    IBStream* stream = FileStream::open(file.c_str(), "rb");
    if (!stream) {
        throw std::runtime_error("cannot open '" + file + "' for reading");
    }

    // setState/setComponentState must be applied in an inactive state.
    const bool was_processing = inst->processing;
    if (inst->processor && was_processing) inst->processor->setProcessing(false);
    inst->component->setActive(false);
    inst->processing = false;

    // loadPreset verifies the stream's class id matches inst->component_uid, then
    // calls component->setState (reaches the processor — same object) and the
    // controller's setState (kComponentState applied to the controller too).
    const bool ok = PresetFile::loadPreset(stream, inst->component_uid,
                                           inst->component, inst->controller);
    stream->release();

    // Reactivate regardless, so a load failure leaves a usable instance.
    if (inst->component->setActive(true) != kResultOk) {
        throw std::runtime_error("setActive(true) failed after state load");
    }
    if (inst->processor) inst->processor->setProcessing(true);
    inst->processing = true;

    if (!ok) {
        throw std::runtime_error(
            "PresetFile::loadPreset failed: the file's class id does not match '" +
            inst->name + "' (" + fuid_to_string(inst->component_uid) +
            "), or the plugin refused the state");
    }

    json out{
        {"name", inst->name},
        {"classId", fuid_to_string(inst->component_uid)},
        {"loaded", true},
        {"applied", true},
    };
    if (is_new) {
        out["instanceId"] = inst->id;
        out["params"] = param_info_array(inst->controller);
        instances_[inst->id] = std::move(created);
    } else {
        out["instanceId"] = inst->id;
    }
    return out;
}

//------------------------------------------------------------------------
json VstHost::render(const json& args) {
    const double duration = args.value("durationSec", 2.0);
    const int sr = args.value("sampleRate", 48000);
    const int bs = args.value("buffer", 512);
    if (!args.contains("out") || !args.at("out").is_string()) {
        throw std::runtime_error("render requires a string 'out' path");
    }
    const std::string out_path = args.at("out").get<std::string>();

    std::unique_ptr<VstInstance> temp;
    VstInstance* inst = nullptr;
    if (args.contains("instanceId")) {
        inst = require(args.at("instanceId").get<int>());
        if (inst->sample_rate != sr || inst->block_size != bs) {
            instantiate(*inst, inst->path, sr, bs);
        }
    } else if (args.contains("path") && args.at("path").is_string()) {
        temp = std::make_unique<VstInstance>();
        temp->id = -1;
        instantiate(*temp, args.at("path").get<std::string>(), sr, bs);
        inst = temp.get();
    } else {
        throw std::runtime_error("render requires 'instanceId' or 'path'");
    }

    const int ch = inst->out_channels;
    const long total_frames = std::max<long>(1, static_cast<long>(duration * sr));

    const bool is_instrument = inst->component->getBusCount(kEvent, kInput) > 0;
    const bool is_effect = !is_instrument &&
                           inst->component->getBusCount(kAudio, kInput) > 0;

    // Build the event list (absolute sample offsets).
    std::vector<Event> events;
    if (args.contains("events") && args.at("events").is_array()) {
        for (const auto& ev : args.at("events")) {
            Event e{};
            const std::string t = ev.value("type", "");
            e.sampleOffset =
                static_cast<int32>(ev.value("timeSec", 0.0) * sr);
            if (t == "noteOn") {
                e.type = Event::kNoteOnEvent;
                e.noteOn.channel = static_cast<int16>(ev.value("channel", 0));
                e.noteOn.pitch = static_cast<int16>(ev.value("pitch", 60));
                e.noteOn.velocity = static_cast<float>(ev.value("velocity", 0.8));
                e.noteOn.noteId = -1;
            } else if (t == "noteOff") {
                e.type = Event::kNoteOffEvent;
                e.noteOff.channel = static_cast<int16>(ev.value("channel", 0));
                e.noteOff.pitch = static_cast<int16>(ev.value("pitch", 60));
                e.noteOff.velocity = 0.0f;
                e.noteOff.noteId = -1;
            } else {
                continue;
            }
            events.push_back(e);
        }
    } else if (is_instrument) {
        Event on{};
        on.type = Event::kNoteOnEvent;
        on.sampleOffset = 0;
        on.noteOn.pitch = 60;  // C4
        on.noteOn.velocity = 0.9f;
        on.noteOn.noteId = -1;
        events.push_back(on);
        Event off{};
        off.type = Event::kNoteOffEvent;
        off.sampleOffset = static_cast<int32>(total_frames * 0.8);
        off.noteOff.pitch = 60;
        off.noteOff.noteId = -1;
        events.push_back(off);
    }
    for (const auto& e : inst->pending_events) events.push_back(e);
    inst->pending_events.clear();

    ProcessContext ctx{};
    ctx.state = ProcessContext::kPlaying;
    ctx.sampleRate = sr;
    ctx.tempo = 120.0;
    ctx.timeSigNumerator = 4;
    ctx.timeSigDenominator = 4;
    inst->process_data.processContext = &ctx;

    std::vector<float> interleaved;
    interleaved.reserve(static_cast<size_t>(total_frames) * ch);

    double peak = 0.0;
    double sumsq = 0.0;
    long done = 0;
    long event_cursor = 0;
    long sample_phase = 0;
    const double test_freq = 220.0;  // A3 test tone fed to effects

    while (done < total_frames) {
        const int32 block =
            static_cast<int32>(std::min<long>(bs, total_frames - done));
        inst->process_data.numSamples = block;
        ctx.continousTimeSamples = done;
        ctx.projectTimeSamples = done;

        inst->event_list.clear();
        while (event_cursor < static_cast<long>(events.size())) {
            const long abs_off = events[event_cursor].sampleOffset;
            if (abs_off >= done && abs_off < done + block) {
                Event e = events[event_cursor];
                e.sampleOffset = static_cast<int32>(abs_off - done);
                inst->event_list.addEvent(e);
                ++event_cursor;
            } else if (abs_off < done) {
                ++event_cursor;
            } else {
                break;
            }
        }

        if (is_effect) {
            for (int32 b = 0; b < inst->process_data.numInputs; ++b) {
                AudioBusBuffers& in = inst->process_data.inputs[b];
                for (int32 c = 0; c < in.numChannels; ++c) {
                    float* buf = in.channelBuffers32[c];
                    if (!buf) continue;
                    for (int32 s = 0; s < block; ++s) {
                        buf[s] = 0.25f *
                                 static_cast<float>(std::sin(
                                     2.0 * M_PI * test_freq *
                                     (sample_phase + s) / sr));
                    }
                }
            }
        }

        for (int32 b = 0; b < inst->process_data.numOutputs; ++b) {
            AudioBusBuffers& ob = inst->process_data.outputs[b];
            for (int32 c = 0; c < ob.numChannels; ++c) {
                if (ob.channelBuffers32[c])
                    std::memset(ob.channelBuffers32[c], 0,
                                sizeof(float) * block);
            }
        }

        if (inst->processor->process(inst->process_data) != kResultOk) {
            throw std::runtime_error("processor->process returned error");
        }

        const float* src[16] = {nullptr};
        int got = 0;
        if (inst->process_data.numOutputs > 0) {
            AudioBusBuffers& ob = inst->process_data.outputs[0];
            for (int32 c = 0; c < ob.numChannels && c < 16; ++c) {
                src[c] = ob.channelBuffers32[c];
                ++got;
            }
        }
        for (int32 s = 0; s < block; ++s) {
            for (int c = 0; c < ch; ++c) {
                float v = (c < got && src[c]) ? src[c][s] : 0.0f;
                interleaved.push_back(v);
                const double a = std::fabs(v);
                if (a > peak) peak = a;
                sumsq += static_cast<double>(v) * v;
            }
        }

        inst->in_params.clearQueue();
        done += block;
        sample_phase += block;
    }

    if (!write_wav_pcm16(out_path, interleaved, ch, sr)) {
        throw std::runtime_error("failed to write WAV: " + out_path);
    }

    const size_t n = interleaved.size();
    const double rms = n ? std::sqrt(sumsq / static_cast<double>(n)) : 0.0;
    const double peak_db = peak > 0 ? 20.0 * std::log10(peak) : -144.0;
    const double rms_db = rms > 0 ? 20.0 * std::log10(rms) : -144.0;
    const bool non_silent = peak > 1e-5;

    log_line("render " + inst->name + " -> " + out_path +
             "  frames=" + std::to_string(total_frames) +
             " peak=" + std::to_string(peak_db) + "dB rms=" +
             std::to_string(rms_db) + "dB nonSilent=" +
             (non_silent ? "yes" : "no"));

    return json{
        {"out", out_path},      {"name", inst->name},
        {"frames", total_frames}, {"channels", ch},
        {"sampleRate", sr},     {"peak", peak},
        {"peakDb", peak_db},    {"rms", rms},
        {"rmsDb", rms_db},      {"nonSilent", non_silent},
        {"isEffect", is_effect}, {"isInstrument", is_instrument},
    };
}

}  // namespace becky
