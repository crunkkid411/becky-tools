//! Headless self-test (the REQUIRED proof, GUI-RULES.md §3 verification).
//!
//! Generates a synthetic clip with ffmpeg, runs the real `video.open` +
//! `video.frame` (+ `video.overlay`) path through the GPU, and asserts the PNGs
//! exist with the right dimensions and are NON-BLANK. Prints PASS/FAIL to stderr.
//! Exit code 0 = PASS, 1 = FAIL -- so a CI/launcher can gate on it.

use std::path::Path;
use std::process::Command;

use crate::decode;
use crate::gpu::Gpu;
use crate::overlay;
use crate::pngio;
use crate::probe;

const TEST_W: u32 = 640;
const TEST_H: u32 = 480;

/// Run the self-test. Returns Ok(()) on PASS, Err(reason) on FAIL.
pub fn run(ffmpeg_bin: &str, ffprobe_bin: &str) -> Result<(), String> {
    let dir = std::env::temp_dir().join("becky-video-preview-selftest");
    std::fs::create_dir_all(&dir).map_err(|e| format!("mkdir {}: {e}", dir.display()))?;
    let clip = dir.join("test.mp4");
    let frame_png = dir.join("frame_t1.png");
    let overlay_png = dir.join("overlay_t1.png");
    let clip_s = clip.to_string_lossy().to_string();

    eprintln!("[selftest] scratch dir: {}", dir.display());

    // 1) Generate a 2s testsrc clip (has motion + color, so frames are non-blank).
    eprintln!("[selftest] generating test clip via ffmpeg testsrc...");
    let gen = Command::new(ffmpeg_bin)
        .args([
            "-v",
            "error",
            "-y",
            "-f",
            "lavfi",
            "-i",
            &format!("testsrc=duration=2:size={TEST_W}x{TEST_H}:rate=30"),
            "-pix_fmt",
            "yuv420p",
            &clip_s,
        ])
        .status()
        .map_err(|e| format!("failed to launch ffmpeg ({ffmpeg_bin}): {e}"))?;
    if !gen.success() {
        return Err(format!(
            "ffmpeg test-clip generation exited {:?}",
            gen.code()
        ));
    }
    if !clip.exists() {
        return Err("test clip was not created".into());
    }

    // 2) video.open (probe).
    eprintln!("[selftest] probing (video.open)...");
    let info = probe::probe(&clip_s, ffprobe_bin)?;
    eprintln!(
        "[selftest]   -> {}x{} @ {:.3}fps, {:.3}s, {} frames",
        info.width, info.height, info.fps, info.duration_sec, info.frames
    );
    if info.width != TEST_W || info.height != TEST_H {
        return Err(format!(
            "probe dims wrong: got {}x{}, want {TEST_W}x{TEST_H}",
            info.width, info.height
        ));
    }

    // 3) Init the headless GPU.
    eprintln!("[selftest] initializing headless wgpu device...");
    let gpu = Gpu::new()?;
    eprintln!("[selftest]   -> GPU backend: {}", gpu.backend);

    // 4) video.frame at t=1.0 -> PNG via the GPU.
    eprintln!("[selftest] decode+render frame at t=1.0 (video.frame)...");
    let frame = decode::decode_frame(&clip_s, 1.0, info.width, info.height, ffmpeg_bin)?;
    let rendered = gpu.render_rgba(&frame.rgba, info.width, info.height)?;
    pngio::save_rgba_png(
        &frame_png.to_string_lossy(),
        &rendered,
        info.width,
        info.height,
    )?;
    assert_png_ok(&frame_png, info.width, info.height)?;
    eprintln!("[selftest]   -> wrote {}", frame_png.display());

    // 5) video.overlay at t=1.0 -> PNG (forensic lower-third composited).
    eprintln!("[selftest] decode+overlay+render at t=1.0 (video.overlay)...");
    let mut frame2 = decode::decode_frame(&clip_s, 1.0, info.width, info.height, ffmpeg_bin)?;
    overlay::draw_lower_third(
        &mut frame2.rgba,
        info.width,
        info.height,
        1.0,
        info.fps,
        "becky selftest // test.mp4",
    );
    let rendered2 = gpu.render_rgba(&frame2.rgba, info.width, info.height)?;
    pngio::save_rgba_png(
        &overlay_png.to_string_lossy(),
        &rendered2,
        info.width,
        info.height,
    )?;
    assert_png_ok(&overlay_png, info.width, info.height)?;
    // The overlay PNG must contain becky neon-green pixels (proof the bar/text drew).
    assert_has_neon_green(&overlay_png)?;
    eprintln!("[selftest]   -> wrote {}", overlay_png.display());

    Ok(())
}

/// Assert a PNG exists, has the expected dimensions, and is not a flat blank.
fn assert_png_ok(path: &Path, w: u32, h: u32) -> Result<(), String> {
    if !path.exists() {
        return Err(format!("expected PNG not found: {}", path.display()));
    }
    let img =
        image::open(path).map_err(|e| format!("cannot decode PNG {}: {e}", path.display()))?;
    if img.width() != w || img.height() != h {
        return Err(format!(
            "PNG dims wrong: got {}x{}, want {w}x{h}",
            img.width(),
            img.height()
        ));
    }
    // Non-blank check: a real frame has color variance. A flat fill (all-black /
    // all-one-color) means the GPU path silently produced nothing.
    let rgba = img.to_rgba8();
    let px = rgba.as_raw();
    if px.len() < 4 {
        return Err("PNG has no pixels".into());
    }
    let first = [px[0], px[1], px[2]];
    let mut differs = false;
    let mut max = [0u8; 3];
    let mut min = [255u8; 3];
    for chunk in px.chunks(4) {
        for c in 0..3 {
            max[c] = max[c].max(chunk[c]);
            min[c] = min[c].min(chunk[c]);
            if chunk[c] != first[c] {
                differs = true;
            }
        }
    }
    let spread = (0..3)
        .map(|c| max[c] as i32 - min[c] as i32)
        .max()
        .unwrap_or(0);
    if !differs || spread < 16 {
        return Err(format!(
            "PNG looks blank (no color variance, spread={spread}) -- GPU render produced a flat image"
        ));
    }
    Ok(())
}

/// Assert the image contains the becky neon-green accent (proves overlay drew).
fn assert_has_neon_green(path: &Path) -> Result<(), String> {
    let img = image::open(path).map_err(|e| format!("cannot decode {}: {e}", path.display()))?;
    let rgba = img.to_rgba8();
    let found = rgba
        .as_raw()
        .chunks(4)
        .any(|p| p[0] == 0x39 && p[1] == 0xFF && p[2] == 0x14);
    if !found {
        return Err("overlay PNG missing neon-green forensic bar pixels".into());
    }
    Ok(())
}
