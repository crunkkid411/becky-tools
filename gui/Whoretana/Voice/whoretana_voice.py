#!/usr/bin/env python
"""WHORETANA voice sidecar v2 - ALL audio lives here (FastRTC VAD + sounddevice I/O).
The WPF GUI spawns this and exchanges NDJSON (contract = BUILD-SPEC section 4):

  control (stdin):  {"cmd":"config","brain":..,"mic":..,"voice":..,"hands_free":..,
                     "local_escalation":..,"warm_local":..}
                    {"cmd":"listen","on":true|false}   {"cmd":"standby","on":true|false}
                    {"cmd":"say","text":..}    {"cmd":"text","text":..,"confirm":..}
                    {"cmd":"devices"}          {"cmd":"quit"}
  events (stdout):  {"type":"ready"}  {"type":"state","state":"idle|listening|thinking|speaking"}
                    {"type":"level","value":0..1}   {"type":"transcript","text":..}
                    {"type":"reply","text":..,"tool":..,"tier":..,"action":..,"need_confirm":..}
                    {"type":"viseme","jaw":..,"funnel":..,"pucker":..,"smile":..}
                    {"type":"wake"}  {"type":"special","kind":"eye_reveal","dur":..}
                    {"type":"brain","name":"router|gemma4|gemini"}
                    {"type":"devices","list":[..]}  {"type":"error","text":..}

Brain ladder: VAD -> becky-transcribe -> becky-voice (layer 1) -> no-match escalates to
llama-server Gemma 4 E4B (layer 2, brain_local.py) -> brain="gemini" = Gemini Live (layer 3).
Speech out: trace clip if the line is in traces/trace-dataset.json (traces.py), else becky-tts.
"""
import sys, os, json, time, re, wave, tempfile, threading, subprocess
import numpy as np

import traces
import brain_local
from traces import becky

SR = 16000
_emit_lock = threading.Lock()


def emit(o):
    with _emit_lock:
        sys.stdout.write(json.dumps(o) + "\n")
        sys.stdout.flush()


def log(*a):
    sys.stderr.write(" ".join(str(x) for x in a) + "\n")
    sys.stderr.flush()


# ---- device enumeration (one-shot) -------------------------------------------
def list_devices():
    import sounddevice as sd
    devs = []
    for i, d in enumerate(sd.query_devices()):
        if d.get("max_input_channels", 0) > 0:
            devs.append({"index": i, "name": d["name"]})
    return devs


if "--list-devices" in sys.argv:
    try:
        emit({"devices": list_devices()})
    except Exception as e:
        emit({"devices": [], "error": str(e)})
    sys.exit(0)


# ---- shared state ------------------------------------------------------------
state = {"brain": "local", "mic": -1, "voice": "default", "listening": False,
         "standby": False, "wake_until": 0.0,
         "hands_free": False, "local_escalation": True, "warm_local": False}
_speaking = threading.Event()
_stop = threading.Event()
_listen_thread = None
_vad = None
TRACES = traces.TraceStore()


def get_vad():
    global _vad
    if _vad is None:
        from fastrtc import get_silero_model
        _vad = get_silero_model()
    return _vad


# ---- wake gate (spec 3.3) ------------------------------------------------------
WAKE_WORDS = ("whoretana", "hortana", "cortana", "fontana", "montana",
              "whore tana", "who retana")
WAKE_MAX = 0.34  # normalized levenshtein threshold
# ponytail: transcribe-and-fuzzy-match; openWakeWord ONNX if wake latency ever annoys


def _lev(a, b):
    if a == b:
        return 0
    prev = list(range(len(b) + 1))
    for i, ca in enumerate(a, 1):
        cur = [i]
        for j, cb in enumerate(b, 1):
            cur.append(min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (ca != cb)))
        prev = cur
    return prev[-1]


def wake_match(text):
    """(matched, payload_after_wake_word) - first 1-2 tokens fuzzed against WAKE_WORDS."""
    toks = re.sub(r"[^a-z0-9 ]+", " ", (text or "").lower()).split()
    for n in (2, 1):
        if len(toks) < n:
            continue
        cand = " ".join(toks[:n])
        for w in WAKE_WORDS:
            if _lev(cand, w) / max(len(cand), len(w), 1) <= WAKE_MAX:
                return True, " ".join(toks[n:])
    return False, ""


# ---- visemes (spec 3.5, audio-feature driven) ----------------------------------
def visemes(chunk, sr):
    """chunk = float32 mono. jaw from RMS, funnel/pucker from spectral centroid."""
    if len(chunk) == 0:
        return {"jaw": 0.0, "funnel": 0.0, "pucker": 0.0, "smile": 0.15}
    jaw = float(min(1.0, np.sqrt(np.mean(chunk.astype(np.float64) ** 2)) * 2.2))
    spec = np.abs(np.fft.rfft(chunk))
    freqs = np.fft.rfftfreq(len(chunk), 1.0 / sr)
    m = freqs <= 4000.0
    band, f = spec[m], freqs[m]
    tot = float(band.sum())
    cn = min(1.0, max(0.0, float((band * f).sum()) / tot / 4000.0)) if tot > 1e-9 else 0.0
    funnel = max(0.0, min(1.0, (0.55 - cn) * 2.0)) * jaw
    return {"jaw": jaw, "funnel": funnel, "pucker": 0.5 * funnel, "smile": 0.15}


def emit_visemes(chunk, sr):
    v = visemes(chunk, sr)
    emit({"type": "viseme", "jaw": round(v["jaw"], 3), "funnel": round(v["funnel"], 3),
          "pucker": round(v["pucker"], 3), "smile": v["smile"]})


def emit_visemes_zero():
    emit({"type": "viseme", "jaw": 0.0, "funnel": 0.0, "pucker": 0.0, "smile": 0.0})


# ---- becky tool calls ----------------------------------------------------------
def transcribe(wav_path):
    try:
        r = subprocess.run([becky("becky-transcribe"), wav_path, "--format", "txt"],
                           capture_output=True, text=True, encoding="utf-8", timeout=120)
        return (r.stdout or "").strip()
    except Exception as e:
        log("transcribe err", e)
        return ""


def route(text, confirm=False):
    intent = json.dumps({"type": "intent", "text": text, "target": "",
                         "pack": "default", "confirm": confirm}) + "\n"
    try:
        r = subprocess.run([becky("becky-voice")], input=intent,
                           capture_output=True, text=True, encoding="utf-8", timeout=180)
        for line in reversed((r.stdout or "").splitlines()):
            line = line.strip()
            if line.startswith("{"):
                try:
                    return json.loads(line)
                except Exception:
                    pass
    except Exception as e:
        log("route err", e)
    try:  # fallback: becky-ask single-shot
        r = subprocess.run([becky("becky-ask"), "--question", text],
                           capture_output=True, text=True, encoding="utf-8", timeout=240)
        return {"text": (r.stdout or "").strip() or "I couldn't reach my tools.",
                "tier": "green", "action": "none"}
    except Exception:
        return {"text": "I couldn't reach my tools right now.", "tier": "green", "action": "none"}


def tts(text):
    try:
        out = os.path.join(tempfile.gettempdir(), "whoretana_say.wav")
        txt = os.path.join(tempfile.gettempdir(), "whoretana_reply.txt")
        with open(txt, "w", encoding="utf-8") as f:
            f.write(text)
        args = [becky("becky-tts"), "--in", txt, "--out", out]
        v = state.get("voice", "default")
        if v and v != "default":
            args += ["--voice", v]
        subprocess.run(args, capture_output=True, text=True, encoding="utf-8", timeout=240)
        return out if os.path.exists(out) else None
    except Exception as e:
        log("tts err", e)
        return None


def play_wav(path):
    import sounddevice as sd
    import soundfile as sf
    try:
        data, sr = sf.read(path, dtype="float32")
        if getattr(data, "ndim", 1) > 1:
            data = data.mean(axis=1)
        _speaking.set()
        emit({"type": "state", "state": "speaking"})
        block = max(256, int(sr * 0.05))  # ~50 ms -> viseme stream at ~20 Hz
        st = sd.OutputStream(samplerate=sr, channels=1)
        st.start()
        i = 0
        while i < len(data) and not _stop.is_set():
            chunk = data[i:i + block]
            i += block
            if len(chunk):
                lvl = float(min(1.0, np.sqrt(np.mean(chunk ** 2)) * 3.2))
                emit({"type": "level", "value": lvl})
                emit_visemes(chunk, sr)
                st.write(chunk)
        st.stop(); st.close()
    except Exception as e:
        log("play err", e)
    finally:
        _speaking.clear()
        emit_visemes_zero()
        emit({"type": "level", "value": 0.0})
        emit({"type": "state", "state": "listening" if state["listening"] or state["standby"] else "idle"})


def speak(text):
    if not text:
        return
    w = tts(text)
    if w:
        play_wav(w)


def _speak_pick(pick):
    """Play a traces.py pick: pre-rendered wav if present, else live TTS (spec 3.4)."""
    if not pick:
        return
    if pick.get("special"):
        emit({"type": "special", "kind": pick["special"]["kind"], "dur": pick["special"]["dur"]})
    if pick.get("wav"):
        play_wav(pick["wav"])
    elif pick.get("text"):
        speak(pick["text"])


def speak_reply(rep):
    if rep.get("text"):
        _speak_pick(traces.choose_speech(rep, TRACES))


# ---- routing ladder (spec 3.2) --------------------------------------------------
YES_WORDS = ("y", "yes", "yeah", "do it", "go", "confirm", "ok", "okay")  # mirrors MainWindow.IsYes
_confirm = {"kind": None, "text": ""}  # pending yes-gate: "router" re-route or "gemma" resume


def _is_yes(text):
    return (text or "").strip().lower().rstrip(".!") in YES_WORDS


def should_escalate(rep):
    return bool(state.get("local_escalation", True)) and brain_local.is_no_match(rep)


def escalate_local(text, confirm=False):
    warm = None
    if not brain_local.health():
        warm = lambda: _speak_pick(
            TRACES.pick_by_id("brain.warming")
            or {"text": "Give me a sec, waking the big brain.", "wav": None, "special": None})
    try:
        if not brain_local.ensure_server(after_spawn=warm):
            return {"text": "Big brain wouldn't wake up. Try again in a minute.",
                    "tier": "green", "action": "none"}
        return brain_local.escalate(text, hands_free=bool(state.get("hands_free")),
                                    confirm=confirm)
    except Exception as e:
        log("escalate err", e)
        return None


def _resume_confirm():
    """User said yes to a pending need_confirm: re-route (router) or resume (gemma)."""
    kind, orig = _confirm["kind"], _confirm["text"]
    _confirm["kind"] = None
    if kind == "gemma":
        emit({"type": "brain", "name": "gemma4"})
        return escalate_local(orig, confirm=True)
    if kind == "router":
        return route(orig, confirm=True)
    return None


def process_text(text, confirm=False):
    emit({"type": "state", "state": "thinking"})
    rep = _resume_confirm() if _confirm["kind"] and (confirm or _is_yes(text)) else None
    if rep is None:
        rep = route(text, confirm)
        if should_escalate(rep):
            emit({"type": "brain", "name": "gemma4"})
            rep = escalate_local(text) or rep
    if rep.get("need_confirm") or rep.get("action") == "await_confirm":
        _confirm["kind"] = "gemma" if brain_local.has_pending() else "router"
        _confirm["text"] = text
    else:
        _confirm["kind"] = None
    emit({"type": "reply", "text": rep.get("text", ""), "tool": rep.get("tool", ""),
          "tier": rep.get("tier", "green"), "action": rep.get("action", "none"),
          "need_confirm": rep.get("need_confirm", False)})
    speak_reply(rep)


def handle_utterance(pcm):
    wav = os.path.join(tempfile.gettempdir(), "whoretana_utt.wav")
    with wave.open(wav, "wb") as wf:
        wf.setnchannels(1); wf.setsampwidth(2); wf.setframerate(SR)
        wf.writeframes(pcm.tobytes())
    if state["standby"]:  # spec 3.3: wake-gate every utterance
        heard = transcribe(wav)
        if not heard:
            return
        hit, payload = wake_match(heard)
        if hit and payload:
            emit({"type": "wake"})
            emit({"type": "transcript", "text": payload})
            process_text(payload)
        elif hit:
            emit({"type": "wake"})
            state["wake_until"] = time.time() + 12.0
            _speak_pick(TRACES.pick_by_id("wake.ack")
                        or {"text": "Yeah?", "wav": None, "special": None})
        elif time.time() < state.get("wake_until", 0.0):
            emit({"type": "transcript", "text": heard})
            process_text(heard)
        return  # non-match in standby -> ignored
    emit({"type": "state", "state": "thinking"})
    heard = transcribe(wav)
    if not heard:
        emit({"type": "reply", "text": "I didn't catch that - check the mic in Settings.",
              "tier": "green", "action": "none"})
        speak("I didn't catch that. Pick your mic in settings.")
        return
    emit({"type": "transcript", "text": heard})
    process_text(heard)


# ---- local listen loop (FastRTC Silero VAD + sounddevice) --------------------
def listen_loop():
    import sounddevice as sd
    from fastrtc import SileroVadOptions
    try:
        vad = get_vad()
    except Exception as e:
        emit({"type": "error", "text": "VAD load failed: " + str(e)})
        emit({"type": "state", "state": "idle"})
        return
    opts = SileroVadOptions()
    mic = None if state["mic"] is None or state["mic"] < 0 else state["mic"]
    bd = 0.25
    block = int(SR * bd)
    buf, silence, in_speech = [], 0.0, False
    emit({"type": "state", "state": "listening"})
    try:
        with sd.InputStream(device=mic, samplerate=SR, channels=1, dtype="int16", blocksize=block) as st:
            while not _stop.is_set():
                audio, _ = st.read(block)
                a = audio.reshape(-1).astype(np.int16)
                rms = float(min(1.0, np.sqrt(np.mean((a.astype(np.float32) / 32768.0) ** 2)) * 4.5))
                emit({"type": "level", "value": rms})
                if _speaking.is_set():     # don't capture our own TTS
                    continue
                try:
                    dur, _chunks = vad.vad((SR, a), opts)
                except Exception:
                    dur = 0.2 if rms > 0.06 else 0.0   # fallback to energy if VAD hiccups
                speech = dur > 0.05
                if speech:
                    in_speech = True; silence = 0.0; buf.append(a)
                elif in_speech:
                    silence += bd; buf.append(a)
                    if silence > 0.8:
                        pcm = np.concatenate(buf) if buf else np.array([], dtype=np.int16)
                        buf, in_speech, silence = [], False, 0.0
                        if len(pcm) > SR * 0.3:
                            handle_utterance(pcm)
                        emit({"type": "state", "state": "listening"})
    except Exception as e:
        emit({"type": "error", "text": "mic error: " + str(e)})
    emit({"type": "state", "state": "idle"})


# ---- gemini realtime (needs GEMINI_API_KEY) ----------------------------------
def gemini_loop():
    key = os.environ.get("GEMINI_API_KEY") or os.environ.get("GOOGLE_API_KEY")
    if not key:
        emit({"type": "error", "text": "No GEMINI_API_KEY in .env - add it in Settings, then switch brain."})
        emit({"type": "state", "state": "idle"})
        return
    try:
        import asyncio
        import sounddevice as sd
        from google import genai
        from google.genai import types
        model = os.environ.get("BECKY_GEMINI_MODEL", "gemini-2.5-flash-native-audio-preview-09-2025")
        client = genai.Client(api_key=key)
        cfg = {"response_modalities": ["AUDIO"]}
        emit({"type": "brain", "name": "gemini"})

        async def run():
            mic = None if state["mic"] is None or state["mic"] < 0 else state["mic"]
            emit({"type": "state", "state": "listening"})
            async with client.aio.live.connect(model=model, config=cfg) as session:
                outq = asyncio.Queue()

                def mic_cb(indata, frames, t, status):
                    try:
                        outq.put_nowait(bytes(indata))
                    except Exception:
                        pass

                istream = sd.RawInputStream(samplerate=16000, channels=1, dtype="int16",
                                            blocksize=1600, device=mic, callback=mic_cb)
                ostream = sd.RawOutputStream(samplerate=24000, channels=1, dtype="int16")
                istream.start(); ostream.start()

                async def send():
                    while not _stop.is_set():
                        data = await outq.get()
                        rms = float(min(1.0, np.sqrt(np.mean((np.frombuffer(data, np.int16).astype(np.float32) / 32768.0) ** 2)) * 4.5))
                        emit({"type": "level", "value": rms})
                        await session.send_realtime_input(audio=types.Blob(data=data, mime_type="audio/pcm;rate=16000"))

                async def recv():
                    while not _stop.is_set():
                        async for resp in session.receive():
                            if getattr(resp, "data", None):
                                _speaking.set(); emit({"type": "state", "state": "speaking"})
                                arr = np.frombuffer(resp.data, np.int16)
                                lvl = float(min(1.0, np.sqrt(np.mean((arr.astype(np.float32) / 32768.0) ** 2)) * 3.2)) if len(arr) else 0.0
                                emit({"type": "level", "value": lvl})
                                emit_visemes(arr.astype(np.float32) / 32768.0, 24000)
                                ostream.write(resp.data)
                            txt = getattr(resp, "text", None)
                            if txt:
                                emit({"type": "reply", "text": txt, "tier": "green", "action": "none"})
                        _speaking.clear()
                        emit_visemes_zero()
                        emit({"type": "state", "state": "listening"})

                await asyncio.gather(send(), recv())
                istream.stop(); ostream.stop()

        asyncio.run(run())
    except Exception as e:
        emit({"type": "error", "text": "Gemini realtime error: " + str(e)})
    emit({"type": "state", "state": "idle"})


def start_listen(force_local=False):
    global _listen_thread
    if _listen_thread and _listen_thread.is_alive():
        return
    _stop.clear()
    use_gemini = state["brain"] == "gemini" and not force_local and not state["standby"]
    _listen_thread = threading.Thread(target=gemini_loop if use_gemini else listen_loop, daemon=True)
    _listen_thread.start()


def stop_listen():
    _stop.set()
    t = _listen_thread
    if t and t.is_alive():
        t.join(timeout=2.0)
    _stop.clear()


# ---- main control loop -------------------------------------------------------
def main():
    emit({"type": "ready"})
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except Exception:
            continue
        cmd = msg.get("cmd")
        if cmd == "config":
            state["brain"] = msg.get("brain", state["brain"])
            state["mic"] = msg.get("mic", state["mic"])
            state["voice"] = msg.get("voice", state["voice"])
            for k in ("hands_free", "local_escalation", "warm_local"):
                if k in msg:
                    state[k] = bool(msg[k])
            if state["warm_local"] and msg.get("warm_local"):
                threading.Thread(target=brain_local.ensure_server, daemon=True).start()
            if state["listening"]:
                stop_listen(); start_listen()
        elif cmd == "listen":
            on = bool(msg.get("on", False))
            state["listening"] = on
            if on:
                start_listen()
            elif not state["standby"]:
                stop_listen(); emit({"type": "state", "state": "idle"})
        elif cmd == "standby":
            on = bool(msg.get("on", False))
            state["standby"] = on
            state["wake_until"] = 0.0
            if on:
                start_listen(force_local=True)  # wake gate needs the local VAD loop
            elif not state["listening"]:
                stop_listen(); emit({"type": "state", "state": "idle"})
        elif cmd == "say":
            threading.Thread(target=speak, args=(msg.get("text", ""),), daemon=True).start()
        elif cmd == "text":
            threading.Thread(target=process_text,
                             args=(msg.get("text", ""), bool(msg.get("confirm", False))),
                             daemon=True).start()
        elif cmd == "devices":
            try:
                emit({"type": "devices", "list": list_devices()})
            except Exception as e:
                emit({"type": "error", "text": str(e)})
        elif cmd == "quit":
            stop_listen()
            brain_local.shutdown()  # only kills llama-server if WE spawned it
            break


if __name__ == "__main__":
    if "--selftest" in sys.argv:
        import selftest
        sys.exit(selftest.run())
    try:
        main()
    except Exception as e:
        emit({"type": "error", "text": "sidecar crashed: " + str(e)})
