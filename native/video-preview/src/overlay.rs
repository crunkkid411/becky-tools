//! The forensic lower-third (GUI-RULES.md / becky-clip "reel" convention).
//!
//! Draws a translucent black bar across the bottom of the frame, then renders the
//! running ORIGINAL-file timecode (HH:MM:SS:FF) and a caption line over it using
//! the built-in bitmap font. This is composited into the RGBA buffer on the CPU
//! BEFORE the GPU upload -- the GPU still does the actual frame draw + readback;
//! the overlay is deterministic pixel compositing on the decoded frame. (A true
//! GPU text shader is the documented follow-up in build-notes.md.)
//!
//! Forensic intent: a detective needs the timecode of the ORIGINAL source at the
//! displayed moment, not a timeline position. We compute it from the seek time +
//! fps so the burned-in stamp matches the source.

use crate::font;

/// Format seconds as a frame-accurate SMPTE-style timecode HH:MM:SS:FF.
pub fn timecode(time_sec: f64, fps: f64) -> String {
    let t = if time_sec.is_finite() && time_sec > 0.0 {
        time_sec
    } else {
        0.0
    };
    let fps = if fps.is_finite() && fps > 0.0 {
        fps
    } else {
        30.0
    };
    let fpr = (fps.round() as u64).max(1); // frames per second (rounded for FF)
    let total_frames = (t * fps).round() as u64;
    let frame = total_frames % fpr;
    let total_secs = total_frames / fpr;
    let secs = total_secs % 60;
    let mins = (total_secs / 60) % 60;
    let hours = total_secs / 3600;
    format!("{hours:02}:{mins:02}:{secs:02}:{frame:02}")
}

/// Composite the lower-third into a tightly-packed RGBA8 buffer in place.
/// `text` is the caption (filename / person / location, caller's choice).
pub fn draw_lower_third(
    rgba: &mut [u8],
    width: u32,
    height: u32,
    time_sec: f64,
    fps: f64,
    text: &str,
) {
    let w = width as usize;
    let h = height as usize;
    if w == 0 || h == 0 || rgba.len() < w * h * 4 {
        return;
    }

    // Bar geometry: bottom ~18% of the frame, min 40px, clamped to frame height.
    let bar_h = ((h as f64 * 0.18).round() as usize).max(40).min(h);
    let bar_top = h - bar_h;

    // 1) Translucent black bar (alpha ~0.55 over existing pixels).
    let bar_alpha = 0.55_f32;
    for y in bar_top..h {
        for x in 0..w {
            let i = (y * w + x) * 4;
            for c in 0..3 {
                let bg = rgba[i + c] as f32;
                rgba[i + c] = (bg * (1.0 - bar_alpha)) as u8; // blend toward black
            }
            rgba[i + 3] = 255;
        }
    }

    // 2) A neon-green top edge on the bar (becky brand accent #39FF14).
    if bar_top < h {
        for x in 0..w {
            let i = (bar_top * w + x) * 4;
            rgba[i] = 0x39;
            rgba[i + 1] = 0xFF;
            rgba[i + 2] = 0x14;
            rgba[i + 3] = 255;
        }
    }

    // Choose a glyph scale that fits the bar comfortably (two text rows).
    let usable = bar_h.saturating_sub(8);
    let row_h = (usable / 2).max(1); // two rows: timecode, then caption
    let scale = (row_h / (font::GLYPH_H + 2)).max(1);

    let pad = 6usize;
    let mut canvas = Canvas { rgba, w, h };
    let tc = timecode(time_sec, fps);
    // Row 1: timecode in bright neon green.
    draw_text(
        &mut canvas,
        pad,
        bar_top + 4,
        &tc,
        scale,
        [0x39, 0xFF, 0x14],
    );
    // Row 2: caption in white.
    let cap_y = bar_top + 4 + (font::GLYPH_H + 2) * scale + 2;
    if !text.is_empty() {
        draw_text(&mut canvas, pad, cap_y, text, scale, [0xFF, 0xFF, 0xFF]);
    }
}

/// A mutable RGBA8 draw surface (buffer + dimensions), so glyph drawing takes a
/// single destination instead of three loose args.
struct Canvas<'a> {
    rgba: &'a mut [u8],
    w: usize,
    h: usize,
}

/// Draw a string with the bitmap font at integer scale, clipping to the frame.
fn draw_text(canvas: &mut Canvas, x0: usize, y0: usize, text: &str, scale: usize, color: [u8; 3]) {
    let (w, h) = (canvas.w, canvas.h);
    let advance = (font::GLYPH_W + 1) * scale; // 1px inter-glyph gap
    let mut cx = x0;
    for ch in text.chars() {
        let bmp = font::glyph(ch);
        for (ry, rowbits) in bmp.iter().enumerate() {
            for col in 0..font::GLYPH_W {
                // bit 4 is the leftmost pixel.
                let on = (rowbits >> (font::GLYPH_W - 1 - col)) & 1 == 1;
                if !on {
                    continue;
                }
                // Expand the lit pixel by `scale`.
                for sy in 0..scale {
                    for sx in 0..scale {
                        let px = cx + col * scale + sx;
                        let py = y0 + ry * scale + sy;
                        if px < w && py < h {
                            let i = (py * w + px) * 4;
                            canvas.rgba[i] = color[0];
                            canvas.rgba[i + 1] = color[1];
                            canvas.rgba[i + 2] = color[2];
                            canvas.rgba[i + 3] = 255;
                        }
                    }
                }
            }
        }
        cx += advance;
        if cx >= w {
            break; // ran off the right edge
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn timecode_is_frame_accurate() {
        assert_eq!(timecode(0.0, 30.0), "00:00:00:00");
        assert_eq!(timecode(1.0, 30.0), "00:00:01:00");
        // 1.5s @ 30fps = frame 45 = 1s + 15 frames.
        assert_eq!(timecode(1.5, 30.0), "00:00:01:15");
        // 1 hour + 2 min + 3 sec exactly.
        assert_eq!(timecode(3723.0, 25.0), "01:02:03:00");
    }

    #[test]
    fn timecode_handles_bad_fps() {
        // fps 0 -> defaults to 30; should not panic / divide by zero.
        let s = timecode(2.0, 0.0);
        assert!(s.starts_with("00:00:02"));
    }

    #[test]
    fn lower_third_darkens_bottom_and_lights_pixels() {
        let (w, h) = (64u32, 64u32);
        let mut buf = vec![200u8; (w * h * 4) as usize]; // light gray frame
        for px in buf.chunks_mut(4) {
            px[3] = 255;
        }
        draw_lower_third(&mut buf, w, h, 1.0, 30.0, "TEST");
        // A pixel in the top region is untouched (still 200).
        let top_i = ((2 * w as usize) + 2) * 4;
        assert_eq!(buf[top_i], 200);
        // The bar region is darkened (well below 200).
        let bottom_i = (((h as usize - 2) * w as usize) + 2) * 4;
        assert!(buf[bottom_i] < 150, "bar not darkened: {}", buf[bottom_i]);
        // Some neon-green pixel exists (the edge or timecode).
        let has_green = buf
            .chunks(4)
            .any(|p| p[0] == 0x39 && p[1] == 0xFF && p[2] == 0x14);
        assert!(has_green, "expected neon-green overlay pixels");
    }

    #[test]
    fn handles_zero_dims_gracefully() {
        let mut buf: Vec<u8> = vec![];
        draw_lower_third(&mut buf, 0, 0, 1.0, 30.0, "X"); // must not panic
    }
}
