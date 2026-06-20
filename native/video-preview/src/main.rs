//! becky-video-preview -- the native GPU video-preview sidecar (GUI-RULES.md §1).
//!
//! Driven by the Go engine over the NDJSON/stdio seam (GUI-RULES.md §2). Decodes
//! video frames with ffmpeg and renders them on the GPU via wgpu (no browser),
//! frame-accurate, for the AI-NLE preview surface.
//!
//! stdout is ONLY protocol JSON; ALL logs/diagnostics go to stderr.
//!
//! Run modes:
//!   becky-video-preview              -> stdio NDJSON server (the normal path)
//!   becky-video-preview --selftest   -> headless GPU proof, exit 0=PASS/1=FAIL
//!   becky-video-preview --window <p>  -> open the on-screen window directly

mod decode;
mod font;
mod gpu;
mod overlay;
mod pngio;
mod probe;
mod protocol;
mod selftest;
mod window;

use serde_json::{json, Value};
use std::io::{BufRead, Write};

use gpu::Gpu;
use probe::VideoInfo;
use protocol::{Event, Incoming, Response};

const VERSION: &str = env!("CARGO_PKG_VERSION");

fn ffmpeg_bin() -> String {
    std::env::var("BECKY_FFMPEG").unwrap_or_else(|_| "ffmpeg".to_string())
}
fn ffprobe_bin() -> String {
    std::env::var("BECKY_FFPROBE").unwrap_or_else(|_| "ffprobe".to_string())
}

fn main() {
    let args: Vec<String> = std::env::args().collect();

    // --selftest: headless GPU proof.
    if args.iter().any(|a| a == "--selftest") {
        match selftest::run(&ffmpeg_bin(), &ffprobe_bin()) {
            Ok(()) => {
                eprintln!("[selftest] PASS");
                std::process::exit(0);
            }
            Err(e) => {
                eprintln!("[selftest] FAIL: {e}");
                std::process::exit(1);
            }
        }
    }

    // --window <path>: open the on-screen window directly (Jordan-verified).
    if let Some(pos) = args.iter().position(|a| a == "--window") {
        let path = args.get(pos + 1).cloned().unwrap_or_default();
        if path.is_empty() {
            eprintln!("[video-preview] --window requires a video path");
            std::process::exit(2);
        }
        if let Err(e) = window::run(path, ffmpeg_bin(), ffprobe_bin()) {
            eprintln!("[video-preview] window error: {e}");
            std::process::exit(1);
        }
        return;
    }

    // Default: the NDJSON stdio server.
    serve();
}

/// The NDJSON/stdio server loop. One JSON object per line in/out; never crash on
/// bad input. The GPU device is created lazily on first frame so a probe-only
/// session works even on a GPU-less box (probe degrades, frame errors cleanly).
fn serve() {
    let stdin = std::io::stdin();
    let stdout = std::io::stdout();
    let mut out = stdout.lock();

    // Startup `ready` event.
    emit(
        &mut out,
        &Event::new(
            "ready",
            json!({"sidecar": "video-preview", "version": VERSION}),
        ),
    );

    let mut gpu: Option<Gpu> = None;

    for line in stdin.lock().lines() {
        let line = match line {
            Ok(l) => l,
            Err(e) => {
                eprintln!("[video-preview] stdin read error: {e}");
                break;
            }
        };
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }

        let msg: Incoming = match serde_json::from_str(trimmed) {
            Ok(m) => m,
            Err(e) => {
                // Bad input must NEVER crash; reply with an error if we can find
                // an id, else reply with an empty-id error.
                let id = extract_id(trimmed);
                let resp = Response::err(&id, format!("invalid JSON: {e}"));
                write_response(&mut out, &resp);
                continue;
            }
        };

        let resp = dispatch(&msg, &mut gpu);
        write_response(&mut out, &resp);
    }
}

/// Route a verb to its handler. Returns a Response with the same id.
fn dispatch(msg: &Incoming, gpu: &mut Option<Gpu>) -> Response {
    let id = msg.id.clone();
    match msg.name.as_str() {
        "ping" => Response::ok(&id, json!({"pong": true, "version": VERSION})),

        "video.open" => match arg_str(&msg.args, "path") {
            Some(path) => match probe::probe(&path, &ffprobe_bin()) {
                Ok(info) => Response::ok(&id, info.to_json()),
                Err(e) => Response::err(&id, e),
            },
            None => Response::err(&id, "video.open: missing 'path'"),
        },

        "video.frame" => handle_frame(&id, &msg.args, gpu, None),

        "video.overlay" => {
            let text = arg_str(&msg.args, "text").unwrap_or_default();
            handle_frame(&id, &msg.args, gpu, Some(text))
        }

        "video.window" => match arg_str(&msg.args, "path") {
            // The window blocks its own event loop; we can't run it inside the
            // stdio loop without taking over the process. Report honestly that
            // the on-screen window is launched via `--window <path>` (the engine
            // spawns a dedicated sidecar process for it).
            Some(path) => Response::ok(
                &id,
                json!({
                    "launch": "spawn a dedicated process: becky-video-preview --window <path>",
                    "path": path,
                    "note": "on-screen window runs its own blocking event loop; not driven over this stdio session"
                }),
            ),
            None => Response::err(&id, "video.window: missing 'path'"),
        },

        other => Response::err(&id, format!("unknown verb: {other}")),
    }
}

/// Shared decode->(overlay?)->GPU render->PNG path for frame/overlay verbs.
fn handle_frame(
    id: &str,
    args: &Value,
    gpu: &mut Option<Gpu>,
    overlay_text: Option<String>,
) -> Response {
    let path = match arg_str(args, "path") {
        Some(p) => p,
        None => return Response::err(id, "missing 'path'"),
    };
    let out_path = match arg_str(args, "out") {
        Some(p) => p,
        None => return Response::err(id, "missing 'out'"),
    };
    let time_sec = arg_f64(args, "timeSec").unwrap_or(0.0);

    // Probe to learn dimensions/fps (needed for the decode buffer + timecode).
    let info: VideoInfo = match probe::probe(&path, &ffprobe_bin()) {
        Ok(i) => i,
        Err(e) => return Response::err(id, format!("probe: {e}")),
    };

    // Lazily create the GPU device on first frame.
    if gpu.is_none() {
        match Gpu::new() {
            Ok(g) => {
                eprintln!("[video-preview] GPU ready: {}", g.backend);
                *gpu = Some(g);
            }
            Err(e) => return Response::err(id, format!("gpu init: {e}")),
        }
    }
    let g = gpu.as_ref().unwrap();

    // Decode the exact frame.
    let mut frame =
        match decode::decode_frame(&path, time_sec, info.width, info.height, &ffmpeg_bin()) {
            Ok(f) => f,
            Err(e) => return Response::err(id, format!("decode: {e}")),
        };

    // Optionally composite the forensic lower-third.
    if let Some(text) = &overlay_text {
        overlay::draw_lower_third(
            &mut frame.rgba,
            info.width,
            info.height,
            time_sec,
            info.fps,
            text,
        );
    }

    // Render on the GPU + read back.
    let pixels = match g.render_rgba(&frame.rgba, info.width, info.height) {
        Ok(p) => p,
        Err(e) => return Response::err(id, format!("render: {e}")),
    };

    // Write the PNG.
    if let Err(e) = pngio::save_rgba_png(&out_path, &pixels, info.width, info.height) {
        return Response::err(id, format!("png: {e}"));
    }

    let mut data = json!({
        "out": out_path,
        "width": info.width,
        "height": info.height,
        "timeSec": time_sec,
        "backend": g.backend,
    });
    if let Some(text) = overlay_text {
        data["overlay"] = json!(true);
        data["text"] = json!(text);
        data["timecode"] = json!(overlay::timecode(time_sec, info.fps));
    }
    Response::ok(id, data)
}

// --- small arg helpers ---

fn arg_str(args: &Value, key: &str) -> Option<String> {
    args.get(key)
        .and_then(|v| v.as_str())
        .map(|s| s.to_string())
}
fn arg_f64(args: &Value, key: &str) -> Option<f64> {
    args.get(key).and_then(|v| v.as_f64())
}

/// Best-effort id extraction from malformed input so an error reply still
/// correlates. Returns "" if none found.
fn extract_id(raw: &str) -> String {
    serde_json::from_str::<Value>(raw)
        .ok()
        .and_then(|v| v.get("id").and_then(|x| x.as_str()).map(String::from))
        .unwrap_or_default()
}

// --- stdout protocol writers (one line each, flushed) ---

fn write_response<W: Write>(out: &mut W, resp: &Response) {
    match serde_json::to_string(resp) {
        Ok(s) => {
            let _ = writeln!(out, "{s}");
            let _ = out.flush();
        }
        Err(e) => eprintln!("[video-preview] failed to serialize response: {e}"),
    }
}

fn emit<W: Write>(out: &mut W, ev: &Event) {
    match serde_json::to_string(ev) {
        Ok(s) => {
            let _ = writeln!(out, "{s}");
            let _ = out.flush();
        }
        Err(e) => eprintln!("[video-preview] failed to serialize event: {e}"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extract_id_from_partial() {
        assert_eq!(extract_id(r#"{"id":"abc","name":"x"}"#), "abc");
        assert_eq!(extract_id("not json"), "");
        assert_eq!(extract_id(r#"{"name":"x"}"#), "");
    }

    #[test]
    fn arg_helpers() {
        let v = json!({"path": "a.mp4", "timeSec": 1.5});
        assert_eq!(arg_str(&v, "path"), Some("a.mp4".to_string()));
        assert_eq!(arg_f64(&v, "timeSec"), Some(1.5));
        assert_eq!(arg_str(&v, "missing"), None);
    }

    #[test]
    fn unknown_verb_errors_with_id() {
        let msg = Incoming {
            kind: Some("command".into()),
            id: "7".into(),
            name: "video.bogus".into(),
            args: json!({}),
        };
        let mut gpu = None;
        let r = dispatch(&msg, &mut gpu);
        assert_eq!(r.id, "7");
        assert!(!r.ok);
        assert!(r.error.unwrap().contains("unknown verb"));
    }

    #[test]
    fn ping_responds_ok() {
        let msg = Incoming {
            kind: None,
            id: "1".into(),
            name: "ping".into(),
            args: json!({}),
        };
        let mut gpu = None;
        let r = dispatch(&msg, &mut gpu);
        assert!(r.ok);
        assert_eq!(r.id, "1");
    }

    #[test]
    fn frame_missing_args_errors_cleanly() {
        let msg = Incoming {
            kind: None,
            id: "2".into(),
            name: "video.frame".into(),
            args: json!({"path": "x.mp4"}), // no 'out'
        };
        let mut gpu = None;
        let r = dispatch(&msg, &mut gpu);
        assert!(!r.ok);
        assert!(r.error.unwrap().contains("out"));
    }
}
