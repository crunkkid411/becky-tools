//! Write a tightly-packed RGBA8 buffer to a PNG file (the `image` crate, MIT).
//! Used to materialize the GPU-rendered frame for `video.frame` / `video.overlay`
//! and for the headless self-test proof.

use std::path::Path;

/// Save `rgba` (width*height*4, tightly packed) as an 8-bit RGBA PNG at `out`.
pub fn save_rgba_png(out: &str, rgba: &[u8], width: u32, height: u32) -> Result<(), String> {
    let expected = (width as usize) * (height as usize) * 4;
    if rgba.len() < expected {
        return Err(format!(
            "png: buffer too small ({} < {})",
            rgba.len(),
            expected
        ));
    }
    // Ensure the parent dir exists (the engine may pass a fresh subfolder path).
    if let Some(parent) = Path::new(out).parent() {
        if !parent.as_os_str().is_empty() {
            std::fs::create_dir_all(parent)
                .map_err(|e| format!("png: cannot create dir {}: {e}", parent.display()))?;
        }
    }
    let buf = image::RgbaImage::from_raw(width, height, rgba[..expected].to_vec())
        .ok_or("png: RgbaImage::from_raw failed (size mismatch)")?;
    buf.save(out)
        .map_err(|e| format!("png: save {out} failed: {e}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_short_buffer() {
        let r = save_rgba_png("x.png", &[0u8; 10], 64, 64);
        assert!(r.is_err());
    }

    #[test]
    fn writes_and_reads_back_dimensions() {
        let dir = std::env::temp_dir().join("becky-vp-pngtest");
        let _ = std::fs::create_dir_all(&dir);
        let out = dir.join("t.png");
        let out_s = out.to_string_lossy().to_string();
        let (w, h) = (8u32, 6u32);
        let buf = vec![123u8; (w * h * 4) as usize];
        save_rgba_png(&out_s, &buf, w, h).unwrap();
        let img = image::open(&out_s).unwrap();
        assert_eq!(img.width(), w);
        assert_eq!(img.height(), h);
        let _ = std::fs::remove_file(&out);
    }
}
