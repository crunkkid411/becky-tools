/* becky-clip frontend — drives the page via the single window.beckyCall bridge
   (SPEC-BECKY-CLIP §9). The core loop: open folder → search/read cues → click a
   result to seek+play that exact moment → double-click to append a clip → toggle
   the forensic lower-third → export. The right panel is the becky assistant
   (propose-then-apply: it shows a proposal, nothing mutates until ✓).

   Everything heavy is in the Go backend; this file only renders + wires events.
   It never reads disk directly — all data arrives as the {ok,data,error} envelope
   from beckyCall. */

(function () {
  "use strict";

  // ---- bridge ----
  // call(verb, args) → resolves to the verb's data, or throws the backend error.
  async function call(verb, args) {
    if (typeof window.beckyCall !== "function") {
      throw new Error("backend bridge not available (run the windows GUI build)");
    }
    const raw = await window.beckyCall(verb, args ? JSON.stringify(args) : "");
    let env;
    try { env = JSON.parse(raw); } catch (e) { throw new Error("bad bridge reply"); }
    if (!env.ok) throw new Error(env.error || "command failed");
    return env.data;
  }

  // ---- DOM ----
  const $ = (id) => document.getElementById(id);
  const vid = $("vid");
  const overlay = $("overlay");
  const results = $("results");
  const timeline = $("timeline");
  const chat = $("chat");

  // ---- state ----
  const state = {
    folder: "",
    videos: [],
    activeVideo: null,   // currently-previewing video {path,name,...}
    overlayOn: false,
    timeline: { clips: [], overlay: {}, duration_sec: 0 },
    base: "",
    transcribing: {},    // name → true while ASR runs (guards double-runs)
    transcribingAll: false,
  };

  // ---------- toast ----------
  let toastEl;
  function toast(msg, isErr) {
    if (!toastEl) {
      toastEl = document.createElement("div");
      toastEl.id = "toast";
      document.body.appendChild(toastEl);
    }
    toastEl.textContent = msg;
    toastEl.className = isErr ? "err show" : "show";
    clearTimeout(toastEl._t);
    toastEl._t = setTimeout(() => { toastEl.className = ""; }, isErr ? 5000 : 2600);
  }

  // ---------- timecode ----------
  // SMPTE-ish "HH:MM:SS:FF" for the running ORIGINAL-file timecode (verification
  // anchor). fps defaults to the active video's source_fps or 30.
  function smpte(sec, fps) {
    if (!(sec >= 0)) sec = 0;
    fps = fps > 0 ? Math.round(fps) : 30;
    const total = Math.round(sec * fps);
    const f = total % fps;
    const s = Math.floor(total / fps) % 60;
    const m = Math.floor(total / (fps * 60)) % 60;
    const h = Math.floor(total / (fps * 3600));
    const p = (n) => String(n).padStart(2, "0");
    return `${p(h)}:${p(m)}:${p(s)}:${p(f)}`;
  }
  function mmss(sec) {
    if (!(sec >= 0)) sec = 0;
    const t = Math.round(sec);
    return `${Math.floor(t / 60)}:${String(t % 60).padStart(2, "0")}`;
  }

  // ---------- folder + videos ----------
  async function openFolder(path) {
    try {
      const fv = await call("open_folder", { folder: path });
      applyFolderView(fv);
    } catch (e) { toast(e.message, true); }
  }

  // applyFolderView renders an indexed case folder (shared by the path-based
  // open and the native picker): update state, draw the video chips, prompt the
  // user to search, play a video, or make a transcript.
  function applyFolderView(fv) {
    if (!fv) return;
    state.folder = fv.root;
    state.videos = fv.videos || [];
    renderVideoPicker();
    const need = state.videos.filter((v) => !v.has_transcript).length;
    const hint = need
      ? `${need} of ${state.videos.length} have no transcript yet. Click a video to play it, hit ⊕ to transcribe one, or “⊕ Transcribe all”.`
      : `Search the transcripts, or click a video to play it and read its cues.`;
    results.innerHTML = `<div class="empty-hint"><div class="big">🔎</div>
      <p>${state.videos.length} video(s) indexed.<br>${escapeHtml(hint)}</p></div>`;
    toast(`opened ${state.videos.length} video(s)`);
  }

  // renderVideoPicker draws one chip per video. Clicking the chip PLAYS the video
  // (raw — no transcript needed) and loads its cues if any. Each chip also carries
  // a small ⊕ "transcribe" button so a detective can make a transcript on the spot.
  function renderVideoPicker() {
    const vp = $("video-picker");
    vp.innerHTML = "";
    if (!state.videos.length) { vp.classList.add("hidden"); return; }
    vp.classList.remove("hidden");

    const head = document.createElement("div");
    head.className = "vp-head";
    const need = state.videos.filter((v) => !v.has_transcript).length;
    head.innerHTML = `<span class="vp-count">${state.videos.length} video${state.videos.length === 1 ? "" : "s"}</span>`;
    const allBtn = document.createElement("button");
    allBtn.className = "vp-all";
    allBtn.id = "btn-transcribe-all";
    allBtn.textContent = state.transcribingAll ? "transcribing…" : "⊕ Transcribe all";
    allBtn.disabled = state.transcribingAll || need === 0;
    allBtn.title = need === 0 ? "every video already has a transcript" : "run ASR on every video that has no transcript";
    allBtn.onclick = transcribeAll;
    head.appendChild(allBtn);
    vp.appendChild(head);

    state.videos.forEach((v) => {
      const chip = document.createElement("div");
      chip.className = "vchip" + (v.has_transcript ? " has-tr" : "");
      chip.dataset.name = v.name;
      const busy = !!state.transcribing[v.name];

      const play = document.createElement("button");
      play.className = "vchip-play";
      play.innerHTML = `<span class="dot"></span><span class="vname">${escapeHtml(v.name)}</span>`;
      play.title = v.has_transcript ? "play this video + read its cues" : "play this video (no transcript yet)";
      play.onclick = () => openVideo(v);
      chip.appendChild(play);

      const tr = document.createElement("button");
      tr.className = "vchip-tr" + (busy ? " busy" : "");
      tr.textContent = busy ? "…" : (v.has_transcript ? "↻" : "⊕");
      tr.disabled = busy || state.transcribingAll;
      tr.title = busy ? "transcribing…" : (v.has_transcript ? "re-transcribe (overwrites the .srt)" : "transcribe this video (local Parakeet ASR)");
      tr.onclick = (e) => { e.stopPropagation(); transcribeVideo(v.name); };
      chip.appendChild(tr);

      vp.appendChild(chip);
    });
  }

  // openVideo: the chip click. PLAY the raw video immediately (decoupled from any
  // transcript), mark the chip active, then load its cues if it has a transcript.
  async function openVideo(v) {
    setActiveChip(v.name);
    state.activeVideo = v;
    previewAt({ source: v.path, name: v.name, start: 0 });
    if (v.has_transcript) loadCues(v.name);
    else showTranscribeCTA(v.name);
  }

  // loadCues fills the results list with a video's transcript cues (no preview
  // change). Used after a chip click and after a transcribe completes.
  async function loadCues(name) {
    try {
      const cues = await call("transcript", { name });
      if (!cues || !cues.length) { showTranscribeCTA(name); return; }
      renderResults(cues);
      toast(`${cues.length} cues`);
    } catch (e) { toast(e.message, true); }
  }

  // showTranscribeCTA turns the dead-end "no cues" state into the single most
  // important action: a big, inviting "Transcribe this video" button.
  function showTranscribeCTA(name) {
    const busy = !!state.transcribing[name];
    results.innerHTML =
      `<div class="empty-hint cta-wrap"><div class="big">📝</div>` +
      `<p>No transcript yet for<br><b>${escapeHtml(name)}</b>.</p>` +
      `<button class="cta-transcribe" id="cta-tr" ${busy ? "disabled" : ""}>` +
      (busy ? "transcribing…" : "⊕ Transcribe this video") + `</button>` +
      `<p class="cta-sub">Local Parakeet ASR — this can take a minute.</p></div>`;
    const btn = $("cta-tr");
    if (btn && !busy) btn.onclick = () => transcribeVideo(name);
  }

  function setActiveChip(name) {
    document.querySelectorAll(".vchip").forEach((c) =>
      c.classList.toggle("active", c.dataset.name === name));
  }

  // ---------- transcribe (ASR) ----------
  // transcribeVideo runs ASR on one video. ASR is slow (tens of seconds to
  // minutes), so we show a clear in-progress state and guard against double-runs.
  // On success the returned FolderView re-renders the picker (now has_transcript)
  // and we auto-load that video's fresh cues.
  async function transcribeVideo(name) {
    if (state.transcribing[name] || state.transcribingAll) return;
    state.transcribing[name] = true;
    renderVideoPicker();
    if (!state.activeVideo || state.activeVideo.name === name) showTranscribeCTA(name);
    toast(`transcribing ${name}… (local Parakeet ASR, this can take a minute)`);
    try {
      const fv = await call("transcribe", { name });
      delete state.transcribing[name];
      if (fv) { state.folder = fv.root; state.videos = fv.videos || []; }
      renderVideoPicker();
      setActiveChip(name);
      toast(`transcribed ${name}`);
      loadCues(name);
    } catch (e) {
      delete state.transcribing[name];
      renderVideoPicker();
      showTranscribeCTA(name);
      toast("transcribe failed: " + e.message, true);
    }
  }

  // transcribeAll runs ASR on every video lacking a transcript, with progress.
  async function transcribeAll() {
    if (state.transcribingAll) return;
    const pending = state.videos.filter((v) => !v.has_transcript).length;
    if (!pending) { toast("every video already has a transcript"); return; }
    state.transcribingAll = true;
    renderVideoPicker();
    toast(`transcribing ${pending} video(s)… (local Parakeet ASR, this can take a while)`);
    try {
      const r = await call("transcribe_all", {});
      state.transcribingAll = false;
      if (r && r.folder) { state.folder = r.folder.root; state.videos = r.folder.videos || []; }
      renderVideoPicker();
      const done = r ? (r.transcribed || 0) : 0;
      const failed = r ? (r.failed || 0) : 0;
      toast(`transcribed ${done}, failed ${failed}`, failed > 0);
      (r && r.errors || []).forEach((e) => addBotMessage(`⚠ ${e.name}: ${e.error}`));
    } catch (e) {
      state.transcribingAll = false;
      renderVideoPicker();
      toast("transcribe all failed: " + e.message, true);
    }
  }

  // ---------- search ----------
  let searchTimer;
  function onSearchInput() {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(runSearch, 180);
  }
  async function runSearch() {
    const q = $("search").value.trim();
    if (!q) return;
    if (!state.folder) { toast("open a case folder first", true); return; }
    try {
      const hits = await call("search", { query: q });
      if (!hits || !hits.length) {
        results.innerHTML = `<div class="empty-hint"><div class="big">∅</div>
          <p>No transcript matches for “${escapeHtml(q)}”.</p></div>`;
        return;
      }
      renderResults(hits, q);
    } catch (e) { toast(e.message, true); }
  }

  // ---------- results list (click → seek, dblclick → add) ----------
  function renderResults(items, query) {
    results.innerHTML = "";
    items.forEach((it) => {
      const el = document.createElement("button");
      el.className = "result";
      el.innerHTML =
        `<div class="meta"><span class="tc">${it.timecode}</span>` +
        `<span class="src">${escapeHtml(it.name)}</span></div>` +
        `<div class="txt">${highlight(it.text, query)}</div>`;
      el.onclick = () => { selectResult(el); previewAt(it); };
      el.ondblclick = () => addClipFrom(it);
      results.appendChild(el);
    });
  }
  function selectResult(el) {
    document.querySelectorAll(".result").forEach((r) => r.classList.remove("sel"));
    el.classList.add("sel");
  }

  // ---------- preview ----------
  // previewAt loads the source (asking the backend for a web-playable URL/proxy)
  // and seeks to the cue start, then plays — the "click a quote → it plays" loop.
  async function previewAt(it) {
    const v = findVideo(it.name) || { name: it.name, path: it.source };
    try {
      const r = await call("media_url", { source: it.source || it.name });
      const url = r.url;
      const sameSource = state.activeVideo && state.activeVideo.path === v.path;
      state.activeVideo = v;
      $("preview-empty").classList.add("hidden");
      $("t-src").textContent = v.name;
      if (!sameSource || vid.getAttribute("src") !== url) {
        vid.setAttribute("src", url);
        vid.load();
      }
      const seek = () => { vid.currentTime = it.start || 0; vid.play().catch(() => {}); };
      if (vid.readyState >= 1) seek();
      else vid.addEventListener("loadedmetadata", seek, { once: true });
      if (r.note) toast(r.note);
    } catch (e) { toast(e.message, true); }
  }

  // The lower-third runs off the playing source's own timecode (the original-file
  // verification anchor): as the preview plays, ov-tc ticks ORIG TC at source fps.
  function tickOverlay() {
    if (!state.overlayOn || !state.activeVideo) return;
    const v = state.activeVideo;
    const ov = state.timeline.overlay || {};
    const fps = v.source_fps || 0;
    $("ov-l1").textContent = ov.show_filename === false ? "" : v.name;
    $("ov-l2").textContent = ov.show_timecode === false ? "" : ("ORIG TC  " + smpte(vid.currentTime || 0, fps));
    const meta = [];
    if (ov.show_date !== false && v.date) meta.push(v.date);
    if (ov.show_person !== false && v.person) meta.push(v.person);
    if (ov.show_location !== false && v.location) meta.push(v.location);
    if (ov.show_link === true && v.link) meta.push(v.link);
    $("ov-l3").textContent = meta.join("   ·   ");
  }

  vid.addEventListener("timeupdate", () => {
    $("t-time").textContent = mmss(vid.currentTime || 0);
    tickOverlay();
  });
  vid.addEventListener("error", () => {
    if (vid.getAttribute("src")) toast("this clip's codec may need a proxy (ffmpeg)", true);
  });

  // ---------- add clip ----------
  async function addClipFrom(it) {
    try {
      const tl = await call("add_clip", {
        source: it.source || it.name,
        in: it.start || 0,
        out: it.end || (it.start || 0) + 4,
        label: it.text || "",
      });
      applyTimeline(tl);
      toast("added clip");
    } catch (e) { toast(e.message, true); }
  }

  // ---------- timeline ----------
  function applyTimeline(tl) {
    if (!tl) return;
    state.timeline = tl;
    renderTimeline();
    if (tl.overlay && typeof tl.overlay.enabled === "boolean") setOverlayUI(tl.overlay.enabled);
    tickOverlay();
  }

  function renderTimeline() {
    const clips = state.timeline.clips || [];
    $("tl-stats").textContent = `${clips.length} clip${clips.length === 1 ? "" : "s"} · ${mmss(state.timeline.duration_sec)}`;
    timeline.innerHTML = "";
    if (!clips.length) {
      timeline.innerHTML = `<div class="tl-empty">double-click a result to add a clip →</div>`;
      return;
    }
    clips.forEach((c, i) => {
      const el = document.createElement("div");
      el.className = "clip";
      el.draggable = true;
      el.dataset.id = c.id;
      el.dataset.index = i;
      el.innerHTML =
        `<span class="c-idx">${i + 1}</span>` +
        `<div class="c-name">${escapeHtml(c.name)}</div>` +
        `<div class="c-label">${escapeHtml(c.label || mmss(c.in) + "–" + mmss(c.out))}</div>` +
        `<span class="c-dur">${mmss(c.dur_sec)}</span>` +
        `<span class="c-x" title="remove">✕</span>`;
      el.querySelector(".c-x").onclick = (e) => { e.stopPropagation(); removeClip(c.id); };
      el.onclick = () => previewAt({ source: c.source, name: c.name, start: c.in, end: c.out });
      wireDrag(el);
      timeline.appendChild(el);
    });
  }

  async function removeClip(id) {
    try { applyTimeline(await call("remove_clip", { id })); }
    catch (e) { toast(e.message, true); }
  }

  // drag-to-reorder
  let dragId = null;
  function wireDrag(el) {
    el.addEventListener("dragstart", () => { dragId = el.dataset.id; el.classList.add("drag"); });
    el.addEventListener("dragend", () => { el.classList.remove("drag"); clearDragOver(); });
    el.addEventListener("dragover", (e) => { e.preventDefault(); el.classList.add("dragover"); });
    el.addEventListener("dragleave", () => el.classList.remove("dragover"));
    el.addEventListener("drop", async (e) => {
      e.preventDefault(); el.classList.remove("dragover");
      if (dragId == null || dragId === el.dataset.id) return;
      const to = parseInt(el.dataset.index, 10);
      try { applyTimeline(await call("reorder", { id: dragId, to })); }
      catch (err) { toast(err.message, true); }
      dragId = null;
    });
  }
  function clearDragOver() { document.querySelectorAll(".clip.dragover").forEach((c) => c.classList.remove("dragover")); }

  // ---------- overlay toggle ----------
  async function toggleOverlay() {
    const next = !state.overlayOn;
    try {
      const tl = await call("set_overlay", { field: "enabled", value: next });
      state.timeline = tl;
    } catch (e) { /* still toggle the preview locally */ }
    setOverlayUI(next);
    tickOverlay();
  }
  function setOverlayUI(on) {
    state.overlayOn = on;
    overlay.classList.toggle("hidden", !on);
    const pos = (state.timeline.overlay && state.timeline.overlay.position) || "bottom";
    overlay.setAttribute("data-pos", pos);
    $("btn-overlay").classList.toggle("on", on);
  }

  // ---------- export / frame / save / load ----------
  async function doExport() {
    if (!(state.timeline.clips || []).length) { toast("add a clip first", true); return; }
    toast("rendering… this can take a moment");
    try {
      const r = await call("export", {});
      toast("exported: " + r.mp4);
      addBotMessage(`Exported the compilation (${r.clips} clips, ${mmss(r.duration_sec)}, ${r.codec}).\n${r.mp4}` +
        (r.note ? `\n${r.note}` : ""));
    } catch (e) { toast("export failed: " + e.message, true); }
  }
  async function grabFrame() {
    if (!state.activeVideo) { toast("play something first", true); return; }
    try {
      const r = await call("grab_frame", { source: state.activeVideo.path, t: vid.currentTime || 0 });
      toast("frame: " + r.path);
    } catch (e) { toast(e.message, true); }
  }
  async function saveReel() {
    try { const r = await call("save_reel", {}); toast("saved: " + r.path); }
    catch (e) { toast(e.message, true); }
  }
  async function loadReel() {
    const p = prompt("reel .json path to load:");
    if (!p) return;
    try { applyTimeline(await call("load_reel", { path: p })); toast("loaded reel"); }
    catch (e) { toast(e.message, true); }
  }

  // ---------- becky ----------
  async function ask(utterance) {
    const u = (utterance || $("ask").value).trim();
    if (!u) return;
    $("ask").value = "";
    clearIntro();
    addUserMessage(u);
    const thinking = addBotMessage("thinking…");
    try {
      const p = await call("ask", { utterance: u });
      thinking.remove();
      renderProposal(p);
    } catch (e) {
      thinking.remove();
      addBotMessage("⚠ " + e.message);
    }
  }

  function renderProposal(p) {
    const hasActions = (p.actions || []).length > 0;
    if (p.preview_text) addBotMessage(p.preview_text, p.tier, p.note);
    if (!hasActions) {
      if (p.note && !p.preview_text) addBotMessage(p.note, p.tier);
      runExecCommands(p.exec_commands);
      return;
    }

    const card = document.createElement("div");
    card.className = "proposal";
    let diff = "";
    (p.preview || []).forEach((d) => {
      diff += `<div class="diff">${escapeHtml(d.label)}` +
        (d.after ? ` → <span class="after">${escapeHtml(d.after)}</span>` : "") + `</div>`;
    });
    card.innerHTML =
      `<div class="p-text">${escapeHtml(p.preview_text || "Apply this change?")}</div>` +
      diff +
      `<div class="p-actions"><button class="p-yes">✓ apply</button><button class="p-no">✗ reject</button></div>`;
    card.querySelector(".p-yes").onclick = async () => {
      card.querySelector(".p-actions").innerHTML = "<span class='tier'>applying…</span>";
      try {
        const res = await call("apply_proposal", { id: p.id });
        applyTimeline(res.timeline);
        runExecCommands(res.exec_commands);
        card.querySelector(".p-actions").innerHTML = "<span class='tier'>✓ applied</span>";
      } catch (e) {
        card.querySelector(".p-actions").innerHTML = "<span class='tier'>⚠ " + escapeHtml(e.message) + "</span>";
      }
    };
    card.querySelector(".p-no").onclick = async () => {
      try { await call("reject_proposal", { id: p.id }); } catch (e) {}
      card.querySelector(".p-actions").innerHTML = "<span class='tier'>✗ rejected</span>";
    };
    chat.appendChild(card);
    chat.scrollTop = chat.scrollHeight;
  }

  // runExecCommands surfaces the deterministic shell-outs the assistant proposed
  // (search / find_quotes). The window build can't spawn from JS, so for a search
  // we run the equivalent in-process search to populate the results panel.
  function runExecCommands(cmds) {
    (cmds || []).forEach((c) => {
      if (c.bin === "becky-search") {
        const q = c.args.length && c.args[0] && c.args[0][0] !== "-" ? c.args[0] : "";
        if (q) { $("search").value = q; runSearch(); }
      }
    });
  }

  function clearIntro() { const i = chat.querySelector(".ul-intro"); if (i) i.remove(); }
  function addUserMessage(t) { return pushMsg("user", t); }
  function addBotMessage(t, tier, note) {
    const el = pushMsg("bot", t);
    if (tier) { const b = document.createElement("div"); b.className = "tier"; b.textContent = tier; el.prepend(b); }
    if (note) { const n = document.createElement("div"); n.className = "note"; n.textContent = note; el.appendChild(n); }
    return el;
  }
  function pushMsg(cls, text) {
    const el = document.createElement("div");
    el.className = "msg " + cls;
    el.appendChild(document.createTextNode(text));
    chat.appendChild(el);
    chat.scrollTop = chat.scrollHeight;
    return el;
  }

  // ---------- helpers ----------
  function findVideo(name) { return state.videos.find((v) => v.name === name) || null; }
  function escapeHtml(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }
  function highlight(text, query) {
    const safe = escapeHtml(text);
    if (!query) return safe;
    const terms = query.replace(/"/g, "").split(/\s+/).filter((t) => t.length > 1);
    let out = safe;
    terms.forEach((t) => {
      const re = new RegExp("(" + t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + ")", "ig");
      out = out.replace(re, "<mark style='background:#39ff14;color:#000'>$1</mark>");
    });
    return out;
  }

  // ---------- wire up ----------
  function wire() {
    // Open folder = the native OS folder dialog (Windows FolderBrowserDialog via
    // the backend). If the picker isn't available (non-Windows / exec error) we
    // fall back to a typed path so the button is never dead.
    $("btn-open").onclick = async () => {
      try {
        const r = await call("pick_folder", {});
        if (r && r.picked) { applyFolderView(r.folder); return; }
        if (r && r.picked === false) return; // user cancelled the dialog
      } catch (e) { /* picker unavailable — fall through to a path prompt */ }
      const p = prompt("Case folder path:", state.folder || "");
      if (p) openFolder(p.trim());
    };
    $("search").addEventListener("input", onSearchInput);
    $("search").addEventListener("keydown", (e) => { if (e.key === "Enter") runSearch(); });
    $("btn-play").onclick = () => { if (vid.paused) vid.play().catch(() => {}); else vid.pause(); };
    $("btn-overlay").onclick = toggleOverlay;
    $("btn-grab").onclick = grabFrame;
    $("btn-export").onclick = doExport;
    $("btn-save").onclick = saveReel;
    $("btn-load").onclick = loadReel;
    $("btn-ask").onclick = () => ask();
    $("ask").addEventListener("keydown", (e) => { if (e.key === "Enter") ask(); });
    $("online").addEventListener("change", async (e) => {
      try { await call("set_online", { on: e.target.checked }); toast(e.target.checked ? "online: frontier model enabled" : "offline"); }
      catch (err) { toast(err.message, true); }
    });
    document.querySelectorAll(".ex").forEach((b) => b.onclick = () => ask(b.textContent));

    bootstrap();
    maybeAutodrive();
  }

  // maybeAutodrive runs the core loop unattended when launched with ?demo=<folder>
  // (the GUI sets this from BECKY_CLIP_DEMO) so a screenshot shows a populated UI:
  // open folder → search → click first result (seek+play) → overlay on → add clip.
  async function maybeAutodrive() {
    const params = new URLSearchParams(location.search);
    const folder = params.get("demo");
    if (!folder) return;
    try {
      await openFolder(folder);
      await sleep(250);
      $("search").value = "Penguin";
      await runSearch();
      await sleep(250);
      const first = document.querySelector(".result");
      if (first) {
        first.click();          // select + seek + play
        await sleep(500);
      }
      if (!state.overlayOn) await toggleOverlay();   // show the forensic lower-third
      // add the first two results to the timeline
      const rs = document.querySelectorAll(".result");
      for (let i = 0; i < Math.min(2, rs.length); i++) { rs[i].dispatchEvent(new Event("dblclick")); await sleep(150); }
      // seed a becky exchange so the right panel shows the chat UX
      clearIntro();
      addUserMessage("compile every time he offered money for the cat");
      // optionally run a real export so the screenshot shows the done state
      if (params.get("export") === "1") { await sleep(200); await doExport(); }
    } catch (e) { /* demo is best-effort */ }
  }
  function sleep(ms) { return new Promise((r) => setTimeout(r, ms)); }

  // bootstrap reflects any state the backend already holds (e.g. a folder opened
  // via argv before the window showed) without changing scope.
  async function bootstrap() {
    if (typeof window.beckyBase === "function") {
      try { state.base = await window.beckyBase(); } catch (e) {}
    }
    try { applyTimeline(await call("timeline", {})); } catch (e) {}
    // If a folder was already opened before the window showed (a path passed on
    // argv / drag-onto-exe / a shortcut), render it so the detective lands straight
    // in their case instead of an empty panel. reindex returns the current folder
    // view (or an empty one when nothing is open, in which case we keep the hint).
    try {
      const fv = await call("reindex", {});
      if (fv && (fv.videos || []).length) applyFolderView(fv);
    } catch (e) {}
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", wire);
  else wire();
})();
