// wav_writer.h - minimal 16-bit PCM WAV writer for offline render output.
// Header-only, no dependencies. Part of becky-audio-host.
//
// Called by: src/vst_host.cpp (render verb) and exercised by src/main.cpp --selftest.
#pragma once

#include <cstdint>
#include <fstream>
#include <string>
#include <vector>

namespace becky {

// Write interleaved float samples (range [-1,1]) to a 16-bit PCM WAV file.
// Returns true on success. Clamps out-of-range samples.
inline bool write_wav_pcm16(const std::string& path,
                            const std::vector<float>& interleaved,
                            int channels,
                            int sample_rate) {
    if (channels <= 0 || sample_rate <= 0) {
        return false;
    }
    std::ofstream out(path, std::ios::binary);
    if (!out) {
        return false;
    }

    const uint32_t num_samples = static_cast<uint32_t>(interleaved.size());
    const uint16_t bits_per_sample = 16;
    const uint16_t block_align =
        static_cast<uint16_t>(channels * (bits_per_sample / 8));
    const uint32_t byte_rate = static_cast<uint32_t>(sample_rate) * block_align;
    const uint32_t data_bytes = num_samples * (bits_per_sample / 8);
    const uint32_t riff_size = 36 + data_bytes;

    auto put_u32 = [&out](uint32_t v) {
        unsigned char b[4] = {static_cast<unsigned char>(v & 0xff),
                              static_cast<unsigned char>((v >> 8) & 0xff),
                              static_cast<unsigned char>((v >> 16) & 0xff),
                              static_cast<unsigned char>((v >> 24) & 0xff)};
        out.write(reinterpret_cast<const char*>(b), 4);
    };
    auto put_u16 = [&out](uint16_t v) {
        unsigned char b[2] = {static_cast<unsigned char>(v & 0xff),
                              static_cast<unsigned char>((v >> 8) & 0xff)};
        out.write(reinterpret_cast<const char*>(b), 2);
    };

    out.write("RIFF", 4);
    put_u32(riff_size);
    out.write("WAVE", 4);

    out.write("fmt ", 4);
    put_u32(16);  // fmt chunk size
    put_u16(1);   // PCM
    put_u16(static_cast<uint16_t>(channels));
    put_u32(static_cast<uint32_t>(sample_rate));
    put_u32(byte_rate);
    put_u16(block_align);
    put_u16(bits_per_sample);

    out.write("data", 4);
    put_u32(data_bytes);

    for (float s : interleaved) {
        if (s > 1.0f) s = 1.0f;
        if (s < -1.0f) s = -1.0f;
        int32_t v = static_cast<int32_t>(s * 32767.0f);
        if (v > 32767) v = 32767;
        if (v < -32768) v = -32768;
        put_u16(static_cast<uint16_t>(static_cast<int16_t>(v)));
    }

    return out.good();
}

}  // namespace becky
