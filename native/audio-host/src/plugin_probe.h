// plugin_probe.h - crash-isolated single-plugin probe for vst.scan.
//
// Some third-party VST3 plugins fault (access violation) during module load in a
// headless host, and an SEH fault cannot be caught by C++ try/catch (and MinGW g++
// has no __try/__except). So scanning isolates EACH plugin in a short-lived child
// process: `becky-audio-host --probe <path>` loads one plugin and prints its JSON;
// if it crashes, only the child dies and the parent records it as crashed+skipped.
// This is how serious hosts (Reaper, the SDK validator) handle bad plugins.
//
// Called by: src/vst_host.cpp (VstHost::scan -> probe_plugin_subprocess) and
//            src/main.cpp (--probe -> probe_plugin_inproc for the child).
#pragma once

#include <string>
#include <vector>

namespace becky {

struct ProbedClass {
    std::string name;
    std::string category;
    std::string vendor;
    std::string version;
};

struct ProbeResult {
    bool loadable = false;
    bool crashed = false;  // child process died (SEH fault, etc.)
    std::string name;
    std::string error;
    std::vector<ProbedClass> classes;  // audio-effect classes only
};

// IN-PROCESS load of one plugin (used by the --probe child). Catches C++ exceptions;
// an SEH fault here crashes THIS (child) process, which the parent detects.
ProbeResult probe_plugin_inproc(const std::string& path);

// Serialize a ProbeResult to the JSON line the --probe child prints on stdout.
// (Declared here so main.cpp's --probe handler can use it; returns nlohmann json.)
std::string probe_result_to_line(const ProbeResult& r);

// Run `self_exe --probe <path>` as a child and parse its JSON. A non-zero/abnormal
// exit => crashed=true. Falls back to in-process if `self_exe` is empty.
ProbeResult probe_plugin_subprocess(const std::string& self_exe,
                                    const std::string& path);

}  // namespace becky
