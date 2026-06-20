// audio_device.cpp - PortAudio output device implementation.
#include "audio_device.h"

#include <cctype>
#include <cstring>
#include <stdexcept>
#include <string>

#include "portaudio.h"
#include "protocol.h"

namespace becky {

namespace {

const char* host_api_name(PaHostApiTypeId type) {
    switch (type) {
        case paASIO: return "ASIO";
        case paWASAPI: return "WASAPI";
        case paDirectSound: return "DirectSound";
        case paMME: return "MME";
        case paWDMKS: return "WDM-KS";
        case paCoreAudio: return "CoreAudio";
        case paALSA: return "ALSA";
        case paJACK: return "JACK";
        case paPulseAudio: return "PulseAudio";
        default: return "Unknown";
    }
}

PaHostApiTypeId api_type_of(PaDeviceIndex dev) {
    const PaDeviceInfo* info = Pa_GetDeviceInfo(dev);
    if (!info) return paInDevelopment;
    const PaHostApiInfo* api = Pa_GetHostApiInfo(info->hostApi);
    return api ? api->type : paInDevelopment;
}

// Extract the most distinctive token from a device name to match the same physical
// interface across host APIs. Skips generic prefixes like "Line"/"Speakers"/"Out".
// e.g. "Line (Steinberg UR12 )" -> "Steinberg".
std::string distinctive_token(const std::string& name) {
    static const char* generic[] = {"line",     "speakers", "out",
                                    "output",   "headphones", "digital",
                                    "primary",  "sound"};
    // Tokenize on spaces and parentheses.
    std::string token;
    std::string best;
    auto is_generic = [&](std::string t) {
        for (char& c : t) c = static_cast<char>(::tolower(c));
        for (const char* g : generic)
            if (t == g) return true;
        return false;
    };
    auto consider = [&](const std::string& t) {
        if (t.size() >= 3 && !is_generic(t) && best.empty()) best = t;
    };
    for (char c : name) {
        if (c == ' ' || c == '(' || c == ')' || c == '-') {
            if (!token.empty()) {
                consider(token);
                token.clear();
            }
        } else {
            token += c;
        }
    }
    if (!token.empty()) consider(token);
    return best;
}

// Find an output device of host-API `want` whose name contains `family` (a
// distinctive token of the target interface). Returns paNoDevice if none.
PaDeviceIndex find_output_by_api(PaHostApiTypeId want, const std::string& family) {
    if (family.empty()) return paNoDevice;
    const PaDeviceIndex count = Pa_GetDeviceCount();
    for (PaDeviceIndex i = 0; i < count; ++i) {
        const PaDeviceInfo* info = Pa_GetDeviceInfo(i);
        if (!info || info->maxOutputChannels <= 0) continue;
        if (api_type_of(i) != want) continue;
        const std::string nm = info->name ? info->name : "";
        if (!nm.empty() && nm.find(family) != std::string::npos) return i;
    }
    return paNoDevice;
}

// PaStreamCallback trampoline with the exact signature PortAudio requires.
int pa_stream_callback(const void* /*input*/, void* output,
                       unsigned long frame_count,
                       const PaStreamCallbackTimeInfo* /*time_info*/,
                       PaStreamCallbackFlags /*status_flags*/, void* user_data) {
    static_cast<AudioDevice*>(user_data)->fill_block(
        static_cast<float*>(output), frame_count);
    return paContinue;
}

}  // namespace

AudioDevice::AudioDevice() {
    PaError err = Pa_Initialize();
    if (err == paNoError) {
        initialized_ = true;
    } else {
        init_error_ = Pa_GetErrorText(err);
        log_line(std::string("Pa_Initialize failed: ") + init_error_);
    }
}

AudioDevice::~AudioDevice() {
    if (stream_) {
        Pa_StopStream(stream_);
        Pa_CloseStream(stream_);
        stream_ = nullptr;
    }
    if (initialized_) {
        Pa_Terminate();
    }
}

json AudioDevice::list_devices() const {
    json out;
    if (!initialized_) {
        out["error"] = init_error_;
        out["devices"] = json::array();
        return out;
    }

    const PaDeviceIndex default_out = Pa_GetDefaultOutputDevice();
    out["default_output"] =
        (default_out == paNoDevice) ? -1 : static_cast<int>(default_out);

    const PaHostApiIndex default_api = Pa_GetDefaultHostApi();
    if (default_api >= 0) {
        const PaHostApiInfo* api = Pa_GetHostApiInfo(default_api);
        if (api) out["default_host_api"] = host_api_name(api->type);
    }

    bool any_asio = false;
    json devices = json::array();
    const PaDeviceIndex count = Pa_GetDeviceCount();
    for (PaDeviceIndex i = 0; i < count; ++i) {
        const PaDeviceInfo* info = Pa_GetDeviceInfo(i);
        if (!info) continue;
        if (info->maxOutputChannels <= 0) continue;  // output devices only
        const PaHostApiInfo* api = Pa_GetHostApiInfo(info->hostApi);
        const char* api_name = api ? host_api_name(api->type) : "Unknown";
        const bool asio = api && api->type == paASIO;
        if (asio) any_asio = true;
        devices.push_back(json{
            {"index", static_cast<int>(i)},
            {"name", info->name ? info->name : ""},
            {"host_api", api_name},
            {"max_output_channels", info->maxOutputChannels},
            {"default_sample_rate", info->defaultSampleRate},
            {"default_low_output_latency", info->defaultLowOutputLatency},
            {"is_asio", asio},
            {"is_default", static_cast<int>(i) == static_cast<int>(default_out)},
        });
    }
    out["devices"] = devices;
    out["asio_available"] = any_asio;
    return out;
}

json AudioDevice::open(const json& args) {
    if (!initialized_) {
        throw std::runtime_error("PortAudio not initialized: " + init_error_);
    }
    if (stream_) {
        Pa_StopStream(stream_);
        Pa_CloseStream(stream_);
        stream_ = nullptr;
        running_.store(false);
    }

    sample_rate_ = args.value("samplerate", 48000);
    buffer_frames_ = args.value("buffer", 256);

    PaDeviceIndex dev = paNoDevice;
    if (args.contains("device") && !args.at("device").is_null()) {
        dev = static_cast<PaDeviceIndex>(args.at("device").get<int>());
    } else {
        // No device given: pick the system default's interface (his UR12), but prefer
        // a lower-latency host API for the SAME interface, in order ASIO > WASAPI >
        // the system default (which is often MME and the highest-latency option).
        dev = Pa_GetDefaultOutputDevice();
        const PaDeviceInfo* def_info =
            (dev != paNoDevice) ? Pa_GetDeviceInfo(dev) : nullptr;
        const std::string def_name =
            def_info && def_info->name ? def_info->name : "";
        const std::string family = distinctive_token(def_name);

        PaDeviceIndex asio_dev = find_output_by_api(paASIO, family);
        PaDeviceIndex wasapi_dev = find_output_by_api(paWASAPI, family);
        if (asio_dev != paNoDevice) {
            dev = asio_dev;
            log_line("preferring ASIO for the default interface (low latency)");
        } else if (wasapi_dev != paNoDevice) {
            dev = wasapi_dev;
            log_line("preferring WASAPI for the default interface "
                     "(lower latency than MME)");
        }  // else: keep the system default device.
    }

    if (dev == paNoDevice) {
        throw std::runtime_error("no output device available");
    }
    const PaDeviceInfo* info = Pa_GetDeviceInfo(dev);
    if (!info) {
        throw std::runtime_error("invalid device index");
    }

    channels_ = info->maxOutputChannels >= 2 ? 2 : 1;
    device_index_ = static_cast<int>(dev);
    device_name_ = info->name ? info->name : "";
    const PaHostApiInfo* api = Pa_GetHostApiInfo(info->hostApi);
    host_api_ = api ? host_api_name(api->type) : "Unknown";

    PaStreamParameters out_params{};
    out_params.device = dev;
    out_params.channelCount = channels_;
    out_params.sampleFormat = paFloat32;
    out_params.suggestedLatency = info->defaultLowOutputLatency;
    out_params.hostApiSpecificStreamInfo = nullptr;

    PaStream* stream = nullptr;
    PaError err = Pa_OpenStream(
        &stream, nullptr, &out_params, static_cast<double>(sample_rate_),
        static_cast<unsigned long>(buffer_frames_), paClipOff,
        &pa_stream_callback, this);
    if (err != paNoError) {
        throw std::runtime_error(std::string("Pa_OpenStream failed: ") +
                                 Pa_GetErrorText(err));
    }
    stream_ = stream;

    log_line("opened " + device_name_ + " via " + host_api_ + " @ " +
             std::to_string(sample_rate_) + " Hz, buffer " +
             std::to_string(buffer_frames_));

    return json{
        {"device", device_index_},   {"name", device_name_},
        {"host_api", host_api_},      {"channels", channels_},
        {"samplerate", sample_rate_}, {"buffer", buffer_frames_},
    };
}

json AudioDevice::start() {
    if (!stream_) throw std::runtime_error("no stream open; call audio.open first");
    if (running_.load()) return json{{"running", true}};
    PaError err = Pa_StartStream(stream_);
    if (err != paNoError) {
        throw std::runtime_error(std::string("Pa_StartStream failed: ") +
                                 Pa_GetErrorText(err));
    }
    running_.store(true);
    emit_event("audio.started",
               json{{"device", device_name_}, {"host_api", host_api_}});
    return json{{"running", true}};
}

json AudioDevice::stop() {
    if (!stream_) return json{{"running", false}};
    if (running_.load()) {
        Pa_StopStream(stream_);
        running_.store(false);
        emit_event("audio.stopped", json::object());
    }
    return json{{"running", false}};
}

void AudioDevice::set_render_source(RenderSource src) {
    active_source_.store(nullptr);  // stop the callback reading during the swap
    render_source_ = std::move(src);
    active_source_.store(render_source_ ? &render_source_ : nullptr);
}

void AudioDevice::fill_block(float* output, unsigned long frames) {
    const int ch = channels_;
    RenderSource* src = active_source_.load();
    if (src && *src) {
        (*src)(output, frames, ch);
    } else {
        std::memset(output, 0, sizeof(float) * frames * ch);
    }
}

}  // namespace becky
