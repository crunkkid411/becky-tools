#!/usr/bin/env python
"""WHORETANA voice sidecar - ALL audio lives here (FastRTC VAD + sounddevice I/O).
No NAudio. The WPF GUI spawns this and exchanges NDJSON:

  control (stdin):  {"cmd":"config","brain":..,"mic":..,"voice":..}
                    {"cmd":"listen","on":true|false}
                    {"cmd":"say","text":..}    {"cmd":"text","text":..,"confirm":..}
                    {"cmd":"devices"}          {"cmd":"quit"}
  events (stdout):  {"type":"ready"}  {"type":"state","state":"idle|listening|thinking|speaking"}
                    {"type":"level","value":0..1}   {"type":"transcript","text":..}
                    {"type":"reply","text":..,"tool":..,"tier":..,"action":..,"need_confirm":..}
                    {"type":"devices","list":[{"index":..,"name":..}]}  {"type":"error","text":..}

Local brain  = FastRTC Silero VAD -> becky-transcribe -> becky-voice -> becky-tts (played here).
Gemini brain = google-genai Live realtime (needs GEMINI_API_KEY from the .env the GUI loaded).
"""
import sys, os, json, time, wave, tempfile, threading, subprocess
import numpy as np

SR = 16000
_emit_lock = threading.Lock()


def emit(o):
    with _emit_lock:
        sys.stdout.write(json.dumps(o) + "\n")
        sys.stdout.flush()


def log(*a):
    sys.stderr.write(" ".join(str(x) for x in a) + "\n")
    sys.stderr.flush()


HERE = os.path.dirname(os.path.abspath(__file__))


def find_repo():
    d = HERE
    for _ in range(9):
        if os.path.isdir(os.path.join(d, "becky-go")):
            return d
        nd = os.path.dirname(d)
        if nd == d:
            break
        d = nd
    return HERE


REPO = find_repo()
BIN = os.path.join(REPO, "becky-go", "bin")


def becky(tool):
    p = os.path.join(BIN, tool + ".exe")
    return p if os.path.exists(p) else tool


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
state = {"brain": "local", "mic": -1, "voice": "default", "listening": False}
_speaking = threading.Event()
_stop = threading.Event()
_listen_thread = None
_vad = None


def get_vad():
    global _vad
    if _vad is None:
        from fastrtc import get_silero_model
        _vad = get_silero_model()
    return _vad


# ---- becky tool calls --------------------------------------------------------
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
        block = max(256, int(sr * 0.05))
        st = sd.OutputStream(samplerate=sr, channels=1)
        st.start()
        i = 0
        while i < len(data) and not _stop.is_set():
            chunk = data[i:i + block]
            i += block
            if len(chunk):
                lvl = float(min(1.0, np.sqrt(np.mean(chunk ** 2)) * 3.2))
                emit({"type": "level", "value": lvl})
                st.write(chunk)
        st.stop(); st.close()
    except Exception as e:
        log("play err", e)
    finally:
        _speaking.clear()
        emit({"type": "level", "value": 0.0})
        emit({"type": "state", "state": "listening" if state["listening"] else "idle"})


def speak(text):
    if not text:
        return
    w = tts(text)
    if w:
        play_wav(w)


def handle_utterance(pcm):
    wav = os.path.join(tempfile.gettempdir(), "whoretana_utt.wav")
    with wave.open(wav, "wb") as wf:
        wf.setnchannels(1); wf.setsampwidth(2); wf.setframerate(SR)
        wf.writeframes(pcm.tobytes())
    emit({"type": "state", "state": "thinking"})
    heard = transcribe(wav)
    if not heard:
        emit({"type": "reply", "text": "I didn't catch that - check the mic in Settings.",
              "tier": "green", "action": "none"})
        speak("I didn't catch that. Pick your mic in settings.")
        return
    emit({"type": "transcript", "text": heard})
    rep = route(heard)
    emit({"type": "reply", "text": rep.get("text", ""), "tool": rep.get("tool", ""),
          "tier": rep.get("tier", "green"), "action": rep.get("action", "none"),
          "need_confirm": rep.get("need_confirm", False)})
    speak(rep.get("text", ""))


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
                                ostream.write(resp.data)
                            txt = getattr(resp, "text", None)
                            if txt:
                                emit({"type": "reply", "text": txt, "tier": "green", "action": "none"})
                        _speaking.clear(); emit({"type": "state", "state": "listening"})

                await asyncio.gather(send(), recv())
                istream.stop(); ostream.stop()

        asyncio.run(run())
    except Exception as e:
        emit({"type": "error", "text": "Gemini realtime error: " + str(e)})
    emit({"type": "state", "state": "idle"})


def start_listen():
    global _listen_thread
    if _listen_thread and _listen_thread.is_alive():
        return
    _stop.clear()
    target = gemini_loop if state["brain"] == "gemini" else listen_loop
    _listen_thread = threading.Thread(target=target, daemon=True)
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
            if state["listening"]:
                stop_listen(); start_listen()
        elif cmd == "listen":
            on = bool(msg.get("on", False))
            state["listening"] = on
            if on:
                start_listen()
            else:
                stop_listen(); emit({"type": "state", "state": "idle"})
        elif cmd == "say":
            threading.Thread(target=speak, args=(msg.get("text", ""),), daemon=True).start()
        elif cmd == "text":
            def _do(t=msg.get("text", ""), c=bool(msg.get("confirm", False))):
                emit({"type": "state", "state": "thinking"})
                rep = route(t, c)
                emit({"type": "reply", "text": rep.get("text", ""), "tool": rep.get("tool", ""),
                      "tier": rep.get("tier", "green"), "action": rep.get("action", "none"),
                      "need_confirm": rep.get("need_confirm", False)})
                speak(rep.get("text", ""))
            threading.Thread(target=_do, daemon=True).start()
        elif cmd == "devices":
            try:
                emit({"type": "devices", "list": list_devices()})
            except Exception as e:
                emit({"type": "error", "text": str(e)})
        elif cmd == "quit":
            stop_listen()
            break


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        emit({"type": "error", "text": "sidecar crashed: " + str(e)})
