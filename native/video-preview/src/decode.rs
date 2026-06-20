//! Single-frame decode via the system `ffmpeg` (already on PATH).
//!
//! `ffmpeg -ss <t> -i <path> -frames:v 1 -f rawvideo -pix_fmt rgba -` writes one
//! frame of tightly-packed RGBA8 to stdout. We seek with `-ss` BEFORE `-i` for a
//! fast keyframe seek, then it decodes forward to the exact frame (ffmpeg's
//! input-seek is frame-accurate for the output frame it emits). The raw buffer
//! is then uploaded to a wgpu texture by the gpu module.
//!
//! We deliberately do NOT link libav* — calling ffmpeg.exe avoids a native build
//! dependency (GUI-RULES.md: native deps are detected, not assumed).

use std::io::Read;
use std::path::Path;
use std::process::{Command, Stdio};

/// A decoded RGBA8 frame, tightly packed (no row padding), width*height*4 bytes.
pub struct RawFrame {
    // width/height mirror the probe and document the buffer shape; callers pass
    // the probed dims to the GPU, so these are part of the struct's contract even
    // though the hot path reads `rgba`.
    #[allow(dead_code)]
    pub width: u32,
    #[allow(dead_code)]
    pub height: u32,
    pub rgba: Vec<u8>,
}

/// Decode exactly one frame at `time_sec`. `width`/`height` come from a prior
/// probe so we know the expected buffer size. `ffmpeg_bin` overrides the binary.
pub fn decode_frame(
    path: &str,
    time_sec: f64,
    width: u32,
    height: u32,
    ffmpeg_bin: &str,
) -> Result<RawFrame, String> {
    if !Path::new(path).exists() {
        return Err(format!("file not found: {path}"));
    }
    if width == 0 || height == 0 {
        return Err("decode: zero dimensions (probe first)".into());
    }

    // Clamp negative / non-finite seeks to 0.
    let t = if time_sec.is_finite() && time_sec > 0.0 {
        time_sec
    } else {
        0.0
    };

    let mut child = Command::new(ffmpeg_bin)
        .args([
            "-v",
            "error",
            "-ss",
            &format!("{t:.6}"),
            "-i",
            path,
            "-frames:v",
            "1",
            "-f",
            "rawvideo",
            "-pix_fmt",
            "rgba",
            "-",
        ])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| format!("failed to launch ffmpeg ({ffmpeg_bin}): {e}"))?;

    let mut rgba = Vec::with_capacity((width as usize) * (height as usize) * 4);
    child
        .stdout
        .take()
        .ok_or("ffmpeg stdout unavailable")?
        .read_to_end(&mut rgba)
        .map_err(|e| format!("reading ffmpeg frame: {e}"))?;

    let status = child
        .wait()
        .map_err(|e| format!("waiting on ffmpeg: {e}"))?;

    let expected = (width as usize) * (height as usize) * 4;
    if rgba.len() < expected {
        // Capture stderr to explain WHY (e.g. seek past EOF emitted nothing).
        let mut errbuf = String::new();
        if let Some(mut s) = child.stderr.take() {
            let _ = s.read_to_string(&mut errbuf);
        }
        return Err(format!(
            "ffmpeg produced {} bytes, expected {} ({}x{} rgba); status={:?}; stderr={}",
            rgba.len(),
            expected,
            width,
            height,
            status.code(),
            errbuf.trim()
        ));
    }
    // ffmpeg can occasionally emit a trailing extra frame's bytes if the muxer
    // is loose; keep only the first full frame.
    rgba.truncate(expected);

    Ok(RawFrame {
        width,
        height,
        rgba,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_missing_file() {
        let r = decode_frame("does-not-exist-xyz.mp4", 1.0, 640, 480, "ffmpeg");
        assert!(r.is_err());
    }

    #[test]
    fn rejects_zero_dims() {
        let r = decode_frame("anything.mp4", 1.0, 0, 0, "ffmpeg");
        assert!(r.is_err());
    }
}
