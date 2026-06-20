//! `video.open` — probe a video with the system `ffprobe` (already on PATH).
//!
//! Returns width/height/fps/durationSec/frames. We do NOT link ffmpeg libs;
//! shelling out to the binary avoids a build-time native dependency and is the
//! robust path the spec calls for.

use serde_json::Value;
use std::path::Path;
use std::process::Command;

#[derive(Debug, Clone)]
pub struct VideoInfo {
    pub width: u32,
    pub height: u32,
    pub fps: f64,
    pub duration_sec: f64,
    pub frames: u64,
}

impl VideoInfo {
    pub fn to_json(&self) -> Value {
        serde_json::json!({
            "width": self.width,
            "height": self.height,
            "fps": self.fps,
            "durationSec": self.duration_sec,
            "frames": self.frames,
        })
    }
}

/// Parse an ffprobe rational like "30000/1001" or "25/1" into f64.
fn parse_rational(s: &str) -> Option<f64> {
    let s = s.trim();
    if s.is_empty() || s == "0/0" {
        return None;
    }
    if let Some((n, d)) = s.split_once('/') {
        let n: f64 = n.trim().parse().ok()?;
        let d: f64 = d.trim().parse().ok()?;
        if d == 0.0 {
            return None;
        }
        Some(n / d)
    } else {
        s.parse().ok()
    }
}

/// Probe a media file. `ffprobe_bin` lets the caller override (default "ffprobe").
pub fn probe(path: &str, ffprobe_bin: &str) -> Result<VideoInfo, String> {
    if !Path::new(path).exists() {
        return Err(format!("file not found: {path}"));
    }

    let out = Command::new(ffprobe_bin)
        .args([
            "-v",
            "error",
            "-select_streams",
            "v:0",
            "-show_entries",
            "stream=width,height,avg_frame_rate,r_frame_rate,nb_frames,duration:format=duration",
            "-of",
            "json",
            path,
        ])
        .output()
        .map_err(|e| format!("failed to launch ffprobe ({ffprobe_bin}): {e}"))?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        return Err(format!("ffprobe failed: {}", stderr.trim()));
    }

    let v: Value = serde_json::from_slice(&out.stdout)
        .map_err(|e| format!("ffprobe json parse error: {e}"))?;

    let stream = v
        .get("streams")
        .and_then(|s| s.get(0))
        .ok_or("no video stream found")?;

    let width = stream
        .get("width")
        .and_then(|x| x.as_u64())
        .ok_or("missing width")? as u32;
    let height = stream
        .get("height")
        .and_then(|x| x.as_u64())
        .ok_or("missing height")? as u32;

    // Prefer avg_frame_rate; fall back to r_frame_rate.
    let fps = stream
        .get("avg_frame_rate")
        .and_then(|x| x.as_str())
        .and_then(parse_rational)
        .or_else(|| {
            stream
                .get("r_frame_rate")
                .and_then(|x| x.as_str())
                .and_then(parse_rational)
        })
        .unwrap_or(0.0);

    // Duration may live on the stream or only on the container format.
    let duration_sec = stream
        .get("duration")
        .and_then(|x| x.as_str())
        .and_then(|s| s.parse::<f64>().ok())
        .or_else(|| {
            v.get("format")
                .and_then(|f| f.get("duration"))
                .and_then(|x| x.as_str())
                .and_then(|s| s.parse::<f64>().ok())
        })
        .unwrap_or(0.0);

    // nb_frames is often "N/A" for streamed/variable content; derive from
    // duration*fps when absent so the field is still useful.
    let frames = stream
        .get("nb_frames")
        .and_then(|x| x.as_str())
        .and_then(|s| s.parse::<u64>().ok())
        .filter(|&n| n > 0)
        .unwrap_or_else(|| {
            if duration_sec > 0.0 && fps > 0.0 {
                (duration_sec * fps).round() as u64
            } else {
                0
            }
        });

    Ok(VideoInfo {
        width,
        height,
        fps,
        duration_sec,
        frames,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rational_parses_ntsc_and_integer() {
        assert!((parse_rational("30000/1001").unwrap() - 29.970_03).abs() < 1e-4);
        assert_eq!(parse_rational("25/1"), Some(25.0));
        assert_eq!(parse_rational("30"), Some(30.0));
    }

    #[test]
    fn rational_rejects_degenerate() {
        assert_eq!(parse_rational("0/0"), None);
        assert_eq!(parse_rational(""), None);
        assert_eq!(parse_rational("5/0"), None);
    }
}
