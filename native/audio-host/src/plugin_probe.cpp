// plugin_probe.cpp - crash-isolated single-plugin probe (see plugin_probe.h).
#include "plugin_probe.h"

#include <array>
#include <cstdio>
#include <string>

#include "pluginterfaces/vst/ivstaudioprocessor.h"
#include "pluginterfaces/vst/ivstcomponent.h"
#include "public.sdk/source/vst/hosting/module.h"

#include "../third_party/nlohmann/json.hpp"

using namespace Steinberg;
using namespace Steinberg::Vst;
using json = nlohmann::json;

namespace becky {

// ---------------------------------------------------------------------------
// In-process load (the --probe child runs this; a fault kills the child only).
// ---------------------------------------------------------------------------
ProbeResult probe_plugin_inproc(const std::string& path) {
    ProbeResult r;
    try {
        std::string err;
        auto mod = VST3::Hosting::Module::create(path, err);
        if (!mod) {
            r.loadable = false;
            r.error = err.empty() ? "module load returned null" : err;
            return r;
        }
        r.name = mod->getName();
        const auto factory = mod->getFactory();
        for (const auto& ci : factory.classInfos()) {
            if (ci.category() != kVstAudioEffectClass) continue;
            ProbedClass pc;
            pc.name = ci.name();
            pc.category = ci.subCategoriesString();
            pc.vendor = ci.vendor();
            pc.version = ci.version();
            r.classes.push_back(std::move(pc));
        }
        r.loadable = true;
    } catch (const std::exception& e) {
        r = ProbeResult{};
        r.error = std::string("exception during load: ") + e.what();
    } catch (...) {
        r = ProbeResult{};
        r.error = "unknown error during load";
    }
    return r;
}

namespace {

ProbeResult from_json(const json& j) {
    ProbeResult r;
    r.loadable = j.value("loadable", false);
    r.name = j.value("name", "");
    r.error = j.value("error", "");
    if (j.contains("classes") && j["classes"].is_array()) {
        for (const auto& c : j["classes"]) {
            ProbedClass pc;
            pc.name = c.value("name", "");
            pc.category = c.value("category", "");
            pc.vendor = c.value("vendor", "");
            pc.version = c.value("version", "");
            r.classes.push_back(std::move(pc));
        }
    }
    return r;
}

}  // namespace

std::string probe_result_to_line(const ProbeResult& r) {
    json classes = json::array();
    for (const auto& c : r.classes) {
        classes.push_back(json{{"name", c.name},
                               {"category", c.category},
                               {"vendor", c.vendor},
                               {"version", c.version}});
    }
    json j{{"loadable", r.loadable},
           {"name", r.name},
           {"error", r.error},
           {"classes", classes}};
    return j.dump();
}

// ---------------------------------------------------------------------------
// Subprocess probe: run `self_exe --probe <path>` and parse the one JSON line it
// prints on stdout. Abnormal exit / no parsable line => crashed.
// ---------------------------------------------------------------------------
#if defined(_WIN32)
#include <windows.h>

ProbeResult probe_plugin_subprocess(const std::string& self_exe,
                                    const std::string& path) {
    if (self_exe.empty()) return probe_plugin_inproc(path);

    ProbeResult r;

    SECURITY_ATTRIBUTES sa{};
    sa.nLength = sizeof(sa);
    sa.bInheritHandle = TRUE;
    HANDLE rd = nullptr, wr = nullptr;
    if (!CreatePipe(&rd, &wr, &sa, 0)) {
        return probe_plugin_inproc(path);
    }
    SetHandleInformation(rd, HANDLE_FLAG_INHERIT, 0);

    // Build a quoted command line: "self" --probe "path"
    std::string cmd = "\"" + self_exe + "\" --probe \"" + path + "\"";
    std::string mutable_cmd = cmd;  // CreateProcess may modify the buffer.

    STARTUPINFOA si{};
    si.cb = sizeof(si);
    si.dwFlags = STARTF_USESTDHANDLES;
    si.hStdOutput = wr;
    si.hStdError = GetStdHandle(STD_ERROR_HANDLE);  // child diagnostics passthrough
    si.hStdInput = GetStdHandle(STD_INPUT_HANDLE);
    PROCESS_INFORMATION pi{};

    BOOL ok = CreateProcessA(nullptr, mutable_cmd.data(), nullptr, nullptr, TRUE,
                             CREATE_NO_WINDOW, nullptr, nullptr, &si, &pi);
    CloseHandle(wr);  // parent keeps only the read end
    if (!ok) {
        CloseHandle(rd);
        return probe_plugin_inproc(path);
    }

    std::string out;
    std::array<char, 4096> buf;
    DWORD got = 0;
    while (ReadFile(rd, buf.data(), static_cast<DWORD>(buf.size()), &got, nullptr) &&
           got > 0) {
        out.append(buf.data(), got);
    }
    CloseHandle(rd);

    WaitForSingleObject(pi.hProcess, 30000);  // 30s safety timeout
    DWORD exit_code = 1;
    GetExitCodeProcess(pi.hProcess, &exit_code);
    if (exit_code == STILL_ACTIVE) {
        TerminateProcess(pi.hProcess, 1);
        exit_code = 1;
    }
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);

    // Find a JSON object line in the child's stdout.
    bool parsed = false;
    size_t start = 0;
    while (start < out.size()) {
        size_t nl = out.find('\n', start);
        std::string line = out.substr(start, nl == std::string::npos
                                                  ? std::string::npos
                                                  : nl - start);
        start = (nl == std::string::npos) ? out.size() : nl + 1;
        if (line.empty()) continue;
        try {
            json j = json::parse(line);
            r = from_json(j);
            parsed = true;
        } catch (...) {
        }
    }

    if (exit_code != 0 || !parsed) {
        ProbeResult c;
        c.loadable = false;
        c.crashed = true;
        c.error = parsed ? "child exited nonzero"
                         : "plugin faulted during load/inspect (skipped)";
        // Keep the name if the child managed to report one before dying.
        if (parsed && !r.name.empty()) c.name = r.name;
        return c;
    }
    return r;
}

#else  // POSIX: fork+exec via popen.

ProbeResult probe_plugin_subprocess(const std::string& self_exe,
                                    const std::string& path) {
    if (self_exe.empty()) return probe_plugin_inproc(path);
    std::string cmd = "\"" + self_exe + "\" --probe \"" + path + "\" 2>/dev/null";
    FILE* p = popen(cmd.c_str(), "r");
    if (!p) return probe_plugin_inproc(path);
    std::string out;
    char buf[4096];
    size_t got;
    while ((got = fread(buf, 1, sizeof(buf), p)) > 0) out.append(buf, got);
    int rc = pclose(p);
    ProbeResult r;
    bool parsed = false;
    size_t start = 0;
    while (start < out.size()) {
        size_t nl = out.find('\n', start);
        std::string line = out.substr(
            start, nl == std::string::npos ? std::string::npos : nl - start);
        start = (nl == std::string::npos) ? out.size() : nl + 1;
        if (line.empty()) continue;
        try {
            r = from_json(json::parse(line));
            parsed = true;
        } catch (...) {
        }
    }
    if (rc != 0 || !parsed) {
        ProbeResult c;
        c.crashed = true;
        c.error = "plugin faulted during load/inspect (skipped)";
        return c;
    }
    return r;
}

#endif

}  // namespace becky
