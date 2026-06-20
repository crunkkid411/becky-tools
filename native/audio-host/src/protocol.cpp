// protocol.cpp - implementation of the NDJSON stdio seam.
#include "protocol.h"

#include <cstdio>
#include <iostream>
#include <mutex>

namespace becky {

namespace {
// Serializing all stdout writes keeps one JSON object per line even when a
// worker thread emits an event concurrently with a response.
std::mutex& stdout_mutex() {
    static std::mutex m;
    return m;
}
}  // namespace

void emit_line(const json& obj) {
    // dump() with no indent => single line. error_handler=replace so an odd byte
    // can never throw here.
    const std::string line =
        obj.dump(-1, ' ', false, json::error_handler_t::replace);
    std::lock_guard<std::mutex> lock(stdout_mutex());
    std::fwrite(line.data(), 1, line.size(), stdout);
    std::fputc('\n', stdout);
    std::fflush(stdout);
}

void emit_response_ok(const std::string& id, const json& data) {
    emit_line(
        json{{"type", "response"}, {"id", id}, {"ok", true}, {"data", data}});
}

void emit_response_err(const std::string& id, const std::string& error) {
    emit_line(
        json{{"type", "response"}, {"id", id}, {"ok", false}, {"error", error}});
}

void emit_event(const std::string& name, const json& data) {
    emit_line(json{{"type", "event"}, {"name", name}, {"data", data}});
}

void log_line(const std::string& msg) {
    std::cerr << "[audio-host] " << msg << "\n";
    std::cerr.flush();
}

}  // namespace becky
