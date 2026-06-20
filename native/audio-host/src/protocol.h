// protocol.h - the NDJSON-over-stdio seam (GUI-RULES.md §2).
//
// One UTF-8 JSON object per line. stdout is ONLY protocol; ALL logging -> stderr.
// Called by: src/main.cpp (the read/dispatch loop) and src/vst_host.cpp / src/audio_device.cpp
// (which emit responses + events through the free functions here).
#pragma once

#include <string>

#include "../third_party/nlohmann/json.hpp"

namespace becky {

using json = nlohmann::json;

// Serialize one JSON object as a single line to stdout and flush. Thread-safe.
// This is the ONLY function permitted to write to stdout.
void emit_line(const json& obj);

// Emit a success response carrying `data` for the request `id`.
void emit_response_ok(const std::string& id, const json& data);

// Emit a failure response carrying `error` for the request `id`.
void emit_response_err(const std::string& id, const std::string& error);

// Emit an async event (engine -> front-end).
void emit_event(const std::string& name, const json& data);

// Log a diagnostic line to stderr (never stdout). Prefixed "[audio-host] ".
void log_line(const std::string& msg);

}  // namespace becky
