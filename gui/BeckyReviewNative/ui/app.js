/* =============================================================================
   Becky Review - LEFT pane logic (vanilla JS, no libraries, no network).

   Runs inside WebView2. The CENTER column (#videoHole) is an EMPTY rect; a NATIVE
   mpv player owned by the C# host is overlaid on top of it - there is NO <video>
   here. Everything reaches the backend through the host bridge:

     page -> host : window.chrome.webview.postMessage(obj)   (structured-cloned)
     host -> page : window.chrome.webview.addEventListener('message', e => e.data)

   Message kinds (see HANDOFF-BECKY-REVIEW-APP.md + BeckyEngine.cs):
     OUT {t:"call", id, verb, args}    -> IN {t:"reply", id, reply:{ok,data,error}}
     OUT {t:"mpv", op:..., ...}         (load/seek/pause/frame/overlay - fire+forget)
     OUT {t:"videoRect", x, y, w, h}    (where to put the native video - rounded CSS px)
     IN  {t:"time", pos, dur}           (continuous playback position from mpv)
     IN  {t:"folder", reply}            (host pushed a FolderView after Pick folder)
   ========================================================================== */

(function () {
  'use strict';

  /* ----------------------------- constants -------------------------------- */
  var DEFAULT_PXPS = 8;    // default timeline scale (px per second) - see state.pxPerSec
  var ZOOM_MIN = 2;        // zoom clamp (px per second) - standard-NLE timeline zoom
  var ZOOM_MAX = 2000;     // high ceiling so the 10s default is a STARTING point, not a limit — zoom in to frame level
  var MINW = 96;           // base/cap for the min clip-block width; the live floor scales with zoom
  var MAX_ROWS = 3000;     // cap RENDERED quote rows (results are date-sorted, so this shows the newest first; the "Showing X of N" note tells the user when more exist)
  var CALL_TIMEOUT_MS = 35 * 60 * 1000;   // 35-minute safety timeout per spec
  var DBL_GUARD_MS = 220;  // single-click wait so a double-click can cancel it
  var RECT_THROTTLE_MS = 60;   // max rate we report the video-hole rect to the host

  // Timeline gesture tuning (CHANGE A/B/C):
  var DRAG_PX  = 6;     // a clip-body pointer must travel > this to become a REORDER drag (else it's a click=seek)
  var SNAP_PX  = 8;     // a seek snaps to a clip edge ONLY within this many px of it (tight, never the whole clip)
  var MIN_CLIP = 0.3;   // a clip's trimmed (in,out) window may never be shorter than this many seconds

  var CHIPS = [
    'find every threat to the host family',
    'compile every time he offered money for the cat',
    'turn the lower-third on'
  ];

  /* ----------------------------- app state -------------------------------- */
  var state = {
    folder: null,
    videos: [],            // Video[]
    orphanCount: 0,

    mode: 'files',         // 'files' | 'results'
    rows: [],              // SearchResult[] or Cue[] currently shown
    terms: [],             // whitespace-split query terms for <mark> highlighting
    headerText: '',
    query: '',

    activeResultKey: null, // which quote row is highlighted

    fileSort: { mode: 'date', desc: true },   // file list: mode date|name + direction (re-click flips)
    quoteSort: { mode: 'date', desc: true },  // search results: mode date|name|relevance + direction
    smartSearch: false,    // false = keyword grep (single words); true = qmd hybrid (meaning)
    fileScrollTop: 0,      // remembered file-list scroll offset (restored when "back" returns)
    cueMode: false,        // true when the results pane shows ONE video's transcript cues (not a search)
    cueAll: [],            // the full cue list for the open video (filtered by cueFilter when shown)
    cueFilter: '',         // "search within this transcript" text (cueMode only)
    viewVideoName: '',     // the video whose cues are shown (cueMode header)

    timeline: { clips: [], overlay: {}, duration_sec: 0 },
    // Overlay is a 3-state cycle (item 14): the lower-third can RENDER (burned into
    // the exported MP4) independently of whether it's shown in this PREVIEW. Default =
    // renders but hidden in preview (so the video isn't obscured while reviewing).
    overlayRender: true,   // burn the lower-third into the render (synced from the reel's overlay.enabled)
    overlayPreview: false, // show the lower-third in THIS mpv preview (UI-only; not persisted to the reel)
    // Playback threshold (item 16): when ON, playback seek-SKIPS the quiet stretches (a
    // faster review of a long clip) WITHOUT ever cutting the forensic evidence — the reel
    // is untouched. thresholdLevel (0..1) is the waveform amplitude below which a stretch
    // counts as "quiet"; the on-timeline bar drags it. OFF by default = zero impact.
    thresholdOn: false,
    thresholdLevel: 0.14,
    overlayShowName: true, // the overlay's filename line is optional (Date/TC/link always shown)
    pxPerSec: DEFAULT_PXPS, // timeline zoom: px per second (clamped ZOOM_MIN..ZOOM_MAX)

    activeSource: null,    // path currently loaded in mpv (a single source for preview, OR the timeline EDL)
    activeClipId: null,    // timeline clip under the playhead (for the playhead + overlay)
    playing: false,        // mpv's real play state (mirrors the host's {t:"play"} reports)
    edlPath: null,         // mpv EDL file = the WHOLE reel as one seamless source (gapless playback)
    edlVersion: -1,        // the tlVersion the loaded EDL was built for (-1 = none)
    tlVersion: 0,          // bumped on every timeline change so the EDL regenerates
    edlDur: 0,             // total compilation duration (sec) reported by the EDL
    edlInflight: null,     // in-flight EDL (re)generation promise so we never request it twice
    pos: 0, dur: 0,        // last {t:"time"} report
    playheadComp: 0,       // current COMPILATION position (active clip start_sec + offset) - drives split (CHANGE 4)
    // pauseReturnComp: where Pause snaps back to — the position playback STARTED
    // from, or wherever a clip-body click landed while playing (a bookmark for
    // "audition ahead without losing my place"; Enter commits the current spot
    // instead). autoScrollSuspended: once that bookmark diverges from the live
    // playhead, sticky-scroll stops following so the view doesn't yank away from
    // wherever the user deliberately clicked to look.
    pauseReturnComp: null,
    autoScrollSuspended: false,
    selectedClipId: null,  // anchor/primary selected clip (Shift-range anchor + extend-frame + playhead)
    selectedClipIds: [],   // ALL selected clips (Ctrl+click toggles, Shift+click ranges) - ripple-delete + render-selection targets

    transcribing: {},      // name -> true while a single transcribe runs
    transcribingAll: false,
    online: false          // false = local Gemma (default), true = Claude
  };

  var proposals = {};       // id -> Proposal awaiting approve/reject

  /* --------------------------- DOM references ----------------------------- */
  var $search      = document.getElementById('search');
  var $searchClear = document.getElementById('searchClear');
  var $smartToggle = document.getElementById('smartToggle');   // keyword vs qmd hybrid
  var $listScroll  = document.getElementById('listScroll');

  var $useClaude = document.getElementById('useClaude');
  var $messages  = document.getElementById('messages');
  var $chips     = document.getElementById('chips');
  var $ask       = document.getElementById('ask');
  var $send      = document.getElementById('send');

  var $tlCount   = document.getElementById('tlCount');
  var $tPlay     = document.getElementById('tPlay');
  var $tSpeed    = document.getElementById('tSpeed');     // 1× / 2× playback-speed toggle
  var $tSplit    = document.getElementById('tSplit');     // split clip at playhead (CHANGE 4)
  var $tScreenshot = document.getElementById('tScreenshot'); // screenshot the preview -> Screenshot_NNNN.png
  var $tThreshold  = document.getElementById('tThreshold');   // playback-threshold toggle (skip quiet parts)
  var $tTrimSilence = document.getElementById('tTrimSilence'); // cut out the quiet parts below the threshold
  var $tExtendL  = document.getElementById('tExtendL');   // extend selected clip 1 frame left (earlier)
  var $tExtendR  = document.getElementById('tExtendR');   // extend selected clip 1 frame right (later)
  var $tOverlay  = document.getElementById('tOverlay');
  var $tOverlayName = document.getElementById('tOverlayName');  // include the filename line in the overlay
  var $tUndo     = document.getElementById('tUndo');
  var $tRedo     = document.getElementById('tRedo');
  var $tSave     = document.getElementById('tSave');
  var $tLoad     = document.getElementById('tLoad');
  var $tRenderSel= document.getElementById('tRenderSel'); // render only the selected clips
  var $tExport   = document.getElementById('tExport');
  var $tZoomIn   = document.getElementById('tZoomIn');
  var $tZoomOut  = document.getElementById('tZoomOut');
  var $tZoom     = document.getElementById('tZoom');

  var videoHoleEl = document.getElementById('videoHole');
  var tlBodyEl    = document.querySelector('.tlbody');
  var rulerEl = document.getElementById('ruler');
  var trackEl = document.getElementById('track');
  var $toast  = document.getElementById('toast');

  // Persistent playhead element (re-appended after every track re-render).
  var playheadEl = document.createElement('div');
  playheadEl.id = 'playhead';
  playheadEl.style.display = 'none';

  // Secondary "playhead stock" (item 1): a 2nd, identical black bar marking
  // pauseReturnComp — where Pause snaps back to, and where cut/split act during
  // playback. It has NO white flag head (only the MOVING playhead does). Re-appended
  // each render like the playhead; positioned + blinked by updateStock.
  var stockEl = document.createElement('div');
  stockEl.id = 'stock';
  stockEl.style.display = 'none';

  // Playback-threshold layer (item 16): a full-width dim layer that greys the quiet
  // stretches, plus a draggable horizontal bar that sets the threshold. Both are
  // children of the track (so they scroll + scale with it); shown only when thresholdOn.
  var quietLayerEl = document.createElement('div');
  quietLayerEl.id = 'quietLayer';
  quietLayerEl.style.display = 'none';
  var thresholdBarEl = document.createElement('div');
  thresholdBarEl.id = 'threshold';
  thresholdBarEl.style.display = 'none';

  /* =======================================================================
     HOST BRIDGE
     ===================================================================== */
  var hasBridge = !!(window.chrome && window.chrome.webview);

  function post(msg) {
    // Every payload is a plain object so WebView2 can structured-clone it.
    if (hasBridge) { window.chrome.webview.postMessage(msg); }
  }

  // Pending call map: id -> resolve fn.
  var pending = new Map();
  var callSeq = 0;

  /* Non-intrusive "loading" indicator (BUSY_DELAY_MS-delayed, depth-counted so
     overlapping calls don't flicker it on/off): every beckyCall is wrapped with
     this, so ANY slow engine round-trip (EDL rebuild, search, folder open, export...)
     shows it automatically with no per-call-site wiring, and a fast one never does. */
  var BUSY_DELAY_MS = 1000;
  var $busyBar = document.getElementById('busyBar');
  var busyDepth = 0, busyTimer = null;
  function busyStart() {
    busyDepth++;
    if (busyDepth === 1) {
      clearTimeout(busyTimer);
      busyTimer = setTimeout(function () { if ($busyBar) { $busyBar.hidden = false; } }, BUSY_DELAY_MS);
    }
  }
  function busyEnd() {
    busyDepth = Math.max(0, busyDepth - 1);
    if (busyDepth === 0) {
      clearTimeout(busyTimer);
      if ($busyBar) { $busyBar.hidden = true; }
    }
  }

  /** Invoke a backend verb; resolves with the reply envelope {ok,data,error}. */
  function beckyCall(verb, args) {
    busyStart();
    return new Promise(function (resolve) {
      var id = 'ui' + (++callSeq) + '-' + Date.now();
      pending.set(id, function (reply) { busyEnd(); resolve(reply); });
      post({ t: 'call', id: id, verb: verb, args: args || {} });
      setTimeout(function () {
        if (pending.has(id)) {
          pending.delete(id);
          busyEnd();
          resolve({ ok: false, data: null, error: 'timeout' });
        }
      }, CALL_TIMEOUT_MS);
    });
  }

  /* mpv control helpers - all fire-and-forget plain objects. */
  function mpvSend(op, extra) {
    var m = { t: 'mpv', op: op };
    if (extra) { for (var k in extra) { if (Object.prototype.hasOwnProperty.call(extra, k)) m[k] = extra[k]; } }
    post(m);
  }
  function mpvPlay(file, at) { post({ t: 'mpv', op: 'play', file: file || '', at: at || 0 }); }
  function mpvLoadAt(file, at) { post({ t: 'mpv', op: 'loadAt', file: file || '', at: at || 0 }); } // load + seek, stay PAUSED (navigate, no autoplay)
  function mpvSeek(at)       { post({ t: 'mpv', op: 'seek', at: at || 0 }); }

  // Receive host -> page messages.
  if (hasBridge) {
    window.chrome.webview.addEventListener('message', function (e) {
      var m = e.data;
      if (!m || typeof m !== 'object') { return; }
      switch (m.t) {
        case 'reply': {
          var r = pending.get(m.id);
          if (r) { pending.delete(m.id); r(m.reply || { ok: false, data: null, error: 'empty reply' }); }
          break;
        }
        case 'time':   onTime(m.pos, m.dur); break;
        case 'play':   onPlayState(!!m.paused); break;
        case 'folder': onFolder(m.reply);    break;
        case 'screenshot': onScreenshot(m.path); break;
        case 'externalDrop': onExternalDrop(m.paths); break;   // OS files dropped onto the window (item 21)
        case 'timelineScrub': seekTimeline(m.comp, false); break;   // native timeline drag -> seek (same path as a DOM click)
        case 'timelineView': if (m.pxPerSec > 0) { state.pxPerSec = m.pxPerSec; if (typeof updateZoomLabel === 'function') { updateZoomLabel(); } } break;
      }
    });
  }

  /* =======================================================================
     VIDEO-HOLE GEOMETRY  (CHANGE 2 - REQUIRED)
     The center column (#videoHole) is left empty; the C# host overlays a NATIVE
     mpv surface on top of it. The host can only place that surface if we tell it
     where the hole is, so we post {t:"videoRect", x, y, w, h} (rounded CSS px) on
     load, on window resize, and whenever the hole itself resizes. Throttled to
     <= one message / RECT_THROTTLE_MS so layout churn can't flood the host.
     ===================================================================== */
  var rectTimer = null;
  var rectLastSent = 0;
  function postVideoRectNow() {
    if (!videoHoleEl) { return; }
    var r = videoHoleEl.getBoundingClientRect();
    post({ t: 'videoRect', x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), h: Math.round(r.height) });
  }
  function reportVideoRect() {
    var now = Date.now();
    var since = now - rectLastSent;
    if (since >= RECT_THROTTLE_MS) {
      rectLastSent = now;
      postVideoRectNow();
    } else if (!rectTimer) {
      rectTimer = setTimeout(function () {
        rectTimer = null;
        rectLastSent = Date.now();
        postVideoRectNow();
      }, RECT_THROTTLE_MS - since);
    }
  }
  window.addEventListener('resize', reportVideoRect);
  window.addEventListener('resize', function () { updateTrailingSpace(); });   // keep the past-the-end scroll room = one screen (item 19)
  if (window.ResizeObserver && videoHoleEl) {
    try { new ResizeObserver(reportVideoRect).observe(videoHoleEl); } catch (_) {}
  }

  /* =======================================================================
     NATIVE TIMELINE (Phase 1). The C# host draws a fast, GPU-composited timeline
     over the .tlbody rect (like the mpv pane over #videoHole). Toggle with the
     "native" button. When on, we push the reel + playhead + view to the host; it
     reports a compilation-seconds scrub back, which we feed to the SAME
     seekTimeline() a DOM click uses. The DOM timeline keeps rendering underneath
     (the native HWND draws on top).
     ponytail: Phase 2 skips the DOM build when native is on for the perf win at scale.
     ===================================================================== */
  var tlNativeBtn = document.getElementById('tlNative');
  var nativeTL = false;

  function tlScrollSec() { return tlBodyEl ? (tlBodyEl.scrollLeft / (state.pxPerSec || 1)) : 0; }

  function reportTimelineRect() {
    if (!nativeTL || !tlBodyEl) { return; }
    var r = tlBodyEl.getBoundingClientRect();
    post({ t: 'timelineRect', x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), h: Math.round(r.height) });
  }

  function pushTimelineReel() {
    if (!nativeTL) { return; }
    var clips = (state.timeline && state.timeline.clips) ? state.timeline.clips : [];
    var out = [];
    for (var i = 0; i < clips.length; i++) {
      var c = clips[i];
      out.push({ start: c.start_sec || 0, dur: c.dur_sec || 0, label: c.label || c.name || '' });
    }
    post({ t: 'timelineReel', clips: out, pxPerSec: state.pxPerSec, scroll: tlScrollSec() });
  }

  function pushTimelinePlayhead() {
    if (!nativeTL) { return; }
    post({ t: 'timelinePlayhead', comp: (typeof state.playheadComp === 'number') ? state.playheadComp : 0 });
  }

  function setNativeTL(on) {
    nativeTL = !!on;
    // Leave an EMPTY hole for the native pane (same contract as #videoHole): hide the DOM
    // timeline's content so the host's GPU pane shows in clear space. This is also the perf
    // win — the DOM stops building clip nodes while native is on.
    var tlin = document.querySelector('.tlinner');
    if (tlin) { tlin.style.visibility = nativeTL ? 'hidden' : ''; }
    if (tlNativeBtn) { tlNativeBtn.classList.toggle('on', nativeTL); }
    post({ t: 'timelineMode', on: nativeTL });
    if (nativeTL) { reportTimelineRect(); pushTimelineReel(); pushTimelinePlayhead(); }
  }

  // Repurposed: launch the real native editor (becky-timeline.exe) on the current reel as a
  // separate GPU window, instead of the old in-window HWND (which drew behind the WebUI).
  // ponytail: dormant setNativeTL/timelineMode plumbing left in place, just not driven here.
  if (tlNativeBtn) {
    tlNativeBtn.addEventListener('click', function () {
      var clips = (state.timeline && state.timeline.clips) || [];
      post({ t: 'openNativeTimeline', clips: clips.map(function (c) { return { source: c.source, in: c.in || 0, out: c.out || 0 }; }) });
    });
  }
  window.addEventListener('resize', reportTimelineRect);
  if (window.ResizeObserver && tlBodyEl) { try { new ResizeObserver(reportTimelineRect).observe(tlBodyEl); } catch (_) {} }
  if (tlBodyEl) { tlBodyEl.addEventListener('scroll', function () { if (nativeTL) { pushTimelineReel(); } }); }

  // Feed the native pane without editing renderTimeline/updatePlayhead directly: wrap them
  // (both are hoisted function declarations in this IIFE, so reassigning the name routes
  // every caller through the wrapper).
  var _origRenderTimeline = renderTimeline;
  renderTimeline = function () { var r = _origRenderTimeline.apply(this, arguments); if (nativeTL) { pushTimelineReel(); reportTimelineRect(); } return r; };
  var _origUpdatePlayhead = updatePlayhead;
  updatePlayhead = function () { var r = _origUpdatePlayhead.apply(this, arguments); if (nativeTL) { pushTimelinePlayhead(); } return r; };

  /* ---- draggable panel splitters (resize find | video | chat) ----------------
     Each splitter drags ONE outer column's width (a CSS var on #app); the center
     video hole is 1fr and follows. On every drag we re-report the hole rect so the
     native mpv pane keeps lining up over it. Widths clamp to 12%..48% so a column
     can never be dragged shut. This lets Jordan widen the file list to read long
     names, or the video, as he likes. */
  function setupSplitter(el, side) {
    if (!el) { return; }
    el.addEventListener('pointerdown', function (e) {
      e.preventDefault();
      var appEl = document.getElementById('app');
      function onMove(ev) {
        var w = appEl.clientWidth || window.innerWidth;
        var lo = w * 0.12, hi = w * 0.48;
        var px = (side === 'left') ? ev.clientX : (w - ev.clientX);
        px = Math.max(lo, Math.min(px, hi));
        appEl.style.setProperty(side === 'left' ? '--findw' : '--chatw', Math.round(px) + 'px');
        reportVideoRect();
      }
      function onUp() {
        document.removeEventListener('pointermove', onMove);
        document.removeEventListener('pointerup', onUp);
      }
      document.addEventListener('pointermove', onMove);
      document.addEventListener('pointerup', onUp);
    });
  }
  setupSplitter(document.getElementById('splitL'), 'left');
  setupSplitter(document.getElementById('splitR'), 'right');

  /* =======================================================================
     SMALL HELPERS
     ===================================================================== */
  function escapeHtml(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }
  function attr(s) { return escapeHtml(s); }

  function truncate(s, n) { s = String(s == null ? '' : s); return s.length > n ? s.slice(0, n - 1) + '…' : s; }

  function baseName(p) {
    p = String(p == null ? '' : p);
    var i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
    return i >= 0 ? p.slice(i + 1) : p;
  }

  /** "H:MM:SS" - the human display format. */
  function hms(sec) {
    sec = Math.max(0, Math.floor(sec || 0));
    var h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
    return h + ':' + String(m).padStart(2, '0') + ':' + String(s).padStart(2, '0');
  }

  /** "M:SS" - compact duration used in the timeline header / clip blocks. */
  function mmss(sec) {
    sec = Math.max(0, Math.round(sec || 0));
    var m = Math.floor(sec / 60), s = sec % 60;
    return m + ':' + String(s).padStart(2, '0');
  }

  /** "HH:MM:SS:FF" - SMPTE timecode (the host draws the real on-video overlay;
      this is only for any caption we might show locally). */
  function smpte(sec, fps) {
    fps = (fps && fps > 0) ? fps : 30;
    sec = Math.max(0, sec || 0);
    var whole = Math.floor(sec);
    var h = Math.floor(whole / 3600), m = Math.floor((whole % 3600) / 60), s = whole % 60;
    var f = Math.floor((sec - whole) * fps);
    var p2 = function (n) { return String(n).padStart(2, '0'); };
    return p2(h) + ':' + p2(m) + ':' + p2(s) + ':' + p2(f);
  }

  /** Wrap every case-insensitive occurrence of each query term in <mark>. */
  function highlight(text, terms) {
    text = String(text == null ? '' : text);
    if (!terms || !terms.length) { return escapeHtml(text); }
    var esc = terms
      .map(function (t) { return String(t).replace(/[.*+?^${}()|[\]\\]/g, '\\$&'); })
      .filter(Boolean);
    if (!esc.length) { return escapeHtml(text); }
    var re = new RegExp('(' + esc.join('|') + ')', 'gi');
    // split() with one capture group: odd indices are the matches.
    return text.split(re).map(function (part, i) {
      var safe = escapeHtml(part);
      return (i % 2 === 1) ? '<mark>' + safe + '</mark>' : safe;
    }).join('');
  }

  function rowKey(r, i) { return (r.source || '') + '|' + (r.start || 0) + '|' + i; }

  function clipById(id) {
    var clips = state.timeline.clips || [];
    for (var i = 0; i < clips.length; i++) { if (String(clips[i].id) === String(id)) return clips[i]; }
    return null;
  }
  function blockById(id) {
    var blocks = trackEl.querySelectorAll('.clip');
    for (var i = 0; i < blocks.length; i++) { if (blocks[i].dataset.id === String(id)) return blocks[i]; }
    return null;
  }
  function clipDur(c) {
    if (typeof c.dur_sec === 'number' && c.dur_sec > 0) return c.dur_sec;
    return Math.max(0, (c.out || 0) - (c.in || 0));
  }

  var toastTimer = null;
  function toast(msg) {
    $toast.textContent = msg;
    $toast.hidden = false;
    // force reflow so the transition runs
    void $toast.offsetWidth;
    $toast.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () {
      $toast.classList.remove('show');
      setTimeout(function () { $toast.hidden = true; }, 260);
    }, 2600);
  }

  /* =======================================================================
     FIND COLUMN - file list + search + transcript cues
     ===================================================================== */
  function emptyHTML(msg, big) {
    return '<div class="empty"><div class="big">' + (big || '&#128193;') + '</div><p>' + escapeHtml(msg) + '</p></div>';
  }

  function renderFind() {
    if (state.mode === 'results') { renderResults(); }
    else { renderFiles(); }
  }

  /* ---- file list (pre-search) ---- */
  function fileRowHTML(v) {
    var busy = !!state.transcribing[v.name];
    var btn;
    if (busy) {
      btn = '<button class="tbtn busy" disabled title="transcribing…"><span class="spin"></span></button>';
    } else if (v.has_transcript) {
      btn = '<button class="tbtn done" data-name="' + attr(v.name) + '" title="re-transcribe locally — writes a SEPARATE <name>_parakeet_transcription.srt; your original transcript is never touched">⟳</button>';
    } else {
      btn = '<button class="tbtn add" data-name="' + attr(v.name) + '" title="transcribe this video (local Parakeet ASR)">+</button>';
    }
    var sub = [v.date, v.person, v.location].filter(Boolean).join(' · ');
    // A green outline marks the transcript just viewed, so returning to the list with
    // "back" shows at a glance which video you were reading (item 18). state.viewVideoName
    // is the last transcript opened (onFileClick / openTranscriptAtTime).
    var lastViewed = (state.viewVideoName && v.name === state.viewVideoName) ? ' lastviewed' : '';
    // title= holds the FULL name so a long filename (the tail differentiates
    // duplicates) is always readable on hover even when the row ellipsises it.
    // draggable => drag the whole video onto the timeline (item 21).
    return '<div class="file' + lastViewed + '" draggable="true" data-name="' + attr(v.name) + '" title="' + attr(v.name) + '">' +
             '<div class="fmeta">' +
               '<div class="fname">' + escapeHtml(v.name) + '</div>' +
               (sub ? '<div class="fsub">' + escapeHtml(sub) + '</div>' : '') +
             '</div>' + btn +
           '</div>';
  }

  // sortedVideos applies the file-list sort. 'date' keeps the engine's order (newest
  // file first, by mtime — the default); 'name' sorts a COPY by name Z->A so the
  // engine's canonical order is never disturbed.
  function sortedVideos() {
    var s = state.fileSort;
    if (s.mode === 'name') {
      var v = state.videos.slice().sort(function (a, b) {
        return a.name < b.name ? -1 : (a.name > b.name ? 1 : 0);   // A -> Z base
      });
      return s.desc ? v.reverse() : v;   // desc = Z -> A (the default)
    }
    // date mode: the engine list is already newest-first (desc); asc = oldest-first.
    return s.desc ? state.videos : state.videos.slice().reverse();
  }

  // sortControlHTML renders the segmented sort toggle shared by the file list ('file')
  // and the search results ('quote'). Clicking the ACTIVE mode flips its direction
  // (newest<->oldest, Z-A<->A-Z); the quote list adds a crown = most-relevant.
  function sortControlHTML(kind) {
    var s = (kind === 'file') ? state.fileSort : state.quoteSort;
    var dateLbl = (s.mode === 'date' && !s.desc) ? 'oldest' : 'newest';
    var nameLbl = (s.mode === 'name' && !s.desc) ? 'A–Z' : 'Z–A';
    var html = '<span class="sortbar">' +
                 '<span class="sortlbl">sort</span>' +
                 '<button class="sortbtn' + (s.mode === 'date' ? ' on' : '') + '" data-sort="' + kind + ':date">' + dateLbl + '</button>' +
                 '<button class="sortbtn' + (s.mode === 'name' ? ' on' : '') + '" data-sort="' + kind + ':name">' + nameLbl + '</button>';
    if (kind === 'quote') {
      html += '<button class="sortbtn crown' + (s.mode === 'relevance' ? ' on' : '') + '" data-sort="quote:relevance" title="most relevant first" aria-label="sort by most relevant">&#128081;</button>';
    }
    return html + '</span>';
  }

  function renderFiles() {
    kbIndex = -1;   // rows are rebuilt -> reset the keyboard cursor
    if (!state.videos.length) {
      $listScroll.innerHTML = emptyHTML('Pick a case folder, then search.');
      return;
    }
    var head =
      '<div class="findhead">' +
        '<span class="findcount">' + state.videos.length + ' video' + (state.videos.length === 1 ? '' : 's') + '</span>' +
        sortControlHTML('file') +
        '<button class="btn small" data-act="transcribe-all"' + (state.transcribingAll ? ' disabled' : '') + '>' +
          (state.transcribingAll ? 'transcribing…' : 'Transcribe all') +
        '</button>' +
      '</div>';
    // Scroll handling: an IN-PLACE re-render (the file list was already showing —
    // e.g. a transcribe finished) keeps the current scroll so the list doesn't jump;
    // arriving from results/cues ("back") restores the offset saved when we left.
    var inPlace = !!$listScroll.querySelector('.filelist');
    var keep = inPlace ? $listScroll.scrollTop : (state.fileScrollTop || 0);
    $listScroll.innerHTML = head + '<div class="filelist">' + sortedVideos().map(fileRowHTML).join('') + '</div>';
    $listScroll.scrollTop = keep;
  }

  /* ---- ranked search results / transcript cues ---- */
  function qrowHTML(r, i) {
    var key = rowKey(r, i);
    var tonly = !!r.transcript_only || !r.source;
    var tc = r.timecode || hms(r.start);
    // In cue mode (one video's transcript) the source name is redundant — it's already
    // in the header — so drop the per-row filename for a minimal Descript-style list.
    var srcLine = state.cueMode ? '' :
      '<div class="qsrc">' + escapeHtml(r.name || baseName(r.source)) +
        (tonly ? ' <span class="qbadge">transcript only</span>' : '') + '</div>';
    return '<div class="qrow' + (tonly ? ' tonly' : '') + (state.activeResultKey === key ? ' active' : '') +
             '" draggable="true" data-idx="' + i + '" data-key="' + attr(key) + '">' +
             '<div class="qtc">' + escapeHtml(tc) + '</div>' +
             '<div class="qbody">' +
               '<div class="qtext">' + highlight(r.text || '', state.terms) + '</div>' +
               srcLine +
             '</div>' +
           '</div>';
  }

  // sortQuotes returns a sorted COPY of search results. 'date' keeps the engine's
  // order (newest file-date first — the default); 'name' sorts by source name Z->A
  // with same-file hits left chronological.
  function sortQuotes(rows, s) {
    if (s.mode === 'relevance') {
      var r = rows.slice().sort(function (a, b) { return (b.score || 0) - (a.score || 0); });  // high score first
      return s.desc ? r : r.reverse();
    }
    if (s.mode === 'name') {
      var n = rows.slice().sort(function (a, b) {
        var an = (a.name || baseName(a.source) || '').toLowerCase();
        var bn = (b.name || baseName(b.source) || '').toLowerCase();
        if (an !== bn) { return an < bn ? -1 : 1; }   // A -> Z base
        return (a.start || 0) - (b.start || 0);
      });
      return s.desc ? n.reverse() : n;   // desc = Z -> A (the default)
    }
    // date mode: engine order is newest-first; reverse for oldest-first.
    var d = rows.slice();
    return s.desc ? d : d.reverse();
  }

  // filterCues keeps the cues whose text contains every whitespace term of q (the
  // "search within this transcript" box). Empty q -> all cues.
  function filterCues(cues, q) {
    q = (q || '').trim().toLowerCase();
    if (!q) { return cues.slice(); }
    var terms = q.split(/\s+/).filter(Boolean);
    return cues.filter(function (c) {
      var t = (c.text || '').toLowerCase();
      for (var i = 0; i < terms.length; i++) { if (t.indexOf(terms[i]) < 0) { return false; } }
      return true;
    });
  }

  function cueHeaderText() {
    var total = state.cueAll.length;
    var shown = state.rows.length;
    if (state.cueFilter && state.cueFilter.trim()) {
      return state.viewVideoName + ' — ' + shown + ' of ' + total + ' line' + (total === 1 ? '' : 's');
    }
    return state.viewVideoName + ' — ' + total + ' line' + (total === 1 ? '' : 's');
  }

  // Restore focus + caret to the cue-search box after a re-render so typing a filter
  // is never interrupted (the box is recreated each render).
  function focusCueSearch() {
    var el = document.getElementById('cueSearch');
    if (!el) { return; }
    el.focus();
    var n = el.value.length;
    try { el.setSelectionRange(n, n); } catch (_) {}
  }

  function renderResults() {
    kbIndex = -1;   // rows are rebuilt -> reset the keyboard cursor
    var rows = state.rows || [];
    var head;
    if (state.cueMode) {
      // A clicked video's transcript cues: back + title over a "search within this
      // transcript" box. Highlight the within-transcript terms in the rows.
      state.terms = state.cueFilter ? state.cueFilter.trim().split(/\s+/).filter(Boolean) : [];
      head = '<div class="resultshead cue">' +
               '<div class="rhrow">' +
                 '<button class="backbtn" data-act="back-to-files" title="back to the video list" aria-label="back to the video list">&#8592;</button>' +
                 '<span class="rhtext">' + escapeHtml(state.headerText || '') + '</span>' +
               '</div>' +
               '<div class="rhrow tools">' +
                 '<input id="cueSearch" class="cuesearch" type="text" placeholder="search within this transcript…" autocomplete="off" spellcheck="false" value="' + attr(state.cueFilter) + '">' +
                 '<button class="btn small" data-act="autocut" title="auto-cut: drop the silent gaps and add the spoken segments of this video to the timeline">auto-cut</button>' +
               '</div>' +
             '</div>';
    } else {
      // A folder-wide search: back + count + the date/name sort toggle.
      head = '<div class="resultshead">' +
               '<button class="backbtn" data-act="back-to-files" title="back to the video list" aria-label="back to the video list">&#8592;</button>' +
               '<span class="rhtext">' + escapeHtml(state.headerText || '') + '</span>' +
               sortControlHTML('quote') +
             '</div>';
    }
    if (!rows.length) {
      $listScroll.innerHTML = head + emptyHTML(state.cueMode ? 'No lines match.' : 'No quotes match.', '&#128269;');
      if (state.cueMode) { focusCueSearch(); }
      return;
    }
    var shown = rows.slice(0, MAX_ROWS);
    var html = head + '<div class="qlist">' + shown.map(function (r, i) { return qrowHTML(r, i); }).join('') + '</div>';
    if (rows.length > shown.length) {
      html += '<div class="more">Showing ' + shown.length + ' of ' + rows.length + '. Refine your search to narrow it.</div>';
    }
    $listScroll.innerHTML = html;
    if (state.cueMode) { focusCueSearch(); }
  }

  function markActiveRow() {
    var rows = $listScroll.querySelectorAll('.qrow');
    for (var i = 0; i < rows.length; i++) {
      rows[i].classList.toggle('active', rows[i].dataset.key === state.activeResultKey);
    }
  }

  /* ---- search (debounced 200ms + on Enter) ---- */
  var searchTimer = null;
  async function doSearch(query) {
    query = (query || '').trim();
    // Leaving the file list to show results: remember where the list was scrolled so
    // "back" returns to the same place.
    if (state.mode === 'files') { state.fileScrollTop = $listScroll.scrollTop; }
    state.query = query;
    state.cueMode = false;   // a folder-wide search is never single-transcript cue mode
    if (!query) { state.mode = 'files'; renderFind(); return; }

    var smart = state.smartSearch;
    // Show a "Searching…" state the instant a non-empty search starts, so a slow or
    // failed search is never a silent blank.
    state.mode = 'results';
    state.rows = [];
    state.searchRaw = [];
    state.terms = [];
    state.activeResultKey = null;
    state.headerText = (smart ? 'Smart-searching for "' : 'Searching for "') + query + '"…';
    renderFind();

    var rep = await beckyCall(smart ? 'qmd_search' : 'search', { query: query });
    // a newer search may have superseded this one
    if (state.query !== query) { return; }
    if (!rep.ok) {
      state.mode = 'results'; state.rows = []; state.searchRaw = []; state.terms = [];
      state.headerText = 'Search failed' + (rep.error ? ': ' + rep.error : '');
      renderFind();
      return;
    }

    // Keyword search returns []SearchResult; smart (qmd) returns {results,mode,note}.
    var results, note = '', mode = '';
    if (smart) {
      var d = rep.data || {};
      results = Array.isArray(d.results) ? d.results : [];
      note = d.note || ''; mode = d.mode || '';
      state.quoteSort = { mode: 'relevance', desc: true };   // semantic results rank by relevance
    } else {
      results = Array.isArray(rep.data) ? rep.data : [];
      state.quoteSort = { mode: 'date', desc: true };        // keyword results default newest-first
    }
    var transcriptOnly = results.filter(function (r) { return r.transcript_only; }).length;
    var playable = results.length - transcriptOnly;

    state.mode = 'results';
    state.cueMode = false;
    state.searchRaw = results;                          // canonical, for re-sorting
    state.rows = sortQuotes(results, state.quoteSort);  // displayed = sorted view
    state.terms = query.split(/\s+/).filter(Boolean);
    state.activeResultKey = null;
    if (smart) {
      state.headerText = results.length + ' match' + (results.length === 1 ? '' : 'es') +
        ' for "' + query + '"' + (mode === 'hybrid' ? ' (smart)' : '') + (note ? ' — ' + note : '');
    } else {
      state.headerText = results.length + ' quotes across the transcripts for "' + query +
        '" (' + playable + ' playable, ' + transcriptOnly + ' transcript-only)';
    }
    renderFind();
  }

  /* ---- row interactions (single=play, double=add-clip; guarded) ---- */
  var rowClickTimer = null;
  function guardRowClick(row) {
    if (rowClickTimer) { return; }
    var idx = +row.dataset.idx, key = row.dataset.key;
    rowClickTimer = setTimeout(function () {
      rowClickTimer = null;
      onRowClick(idx, key);
    }, DBL_GUARD_MS);
  }

  function onRowClick(idx, key) {
    var r = state.rows[idx];
    if (!r) { return; }
    state.activeResultKey = key;
    kbIndex = idx;   // keep the keyboard cursor in sync so Up/Down resumes from here
    markActiveRow();
    if (r.transcript_only || !r.source) { return; }   // not playable: just select
    state.activeSource = r.source;
    state.activeClipId = null;                          // not a timeline clip
    updatePlayhead();
    mpvPlay(r.source, r.start || 0);
  }

  async function onRowDbl(idx) {
    var r = state.rows[idx];
    if (!r || r.transcript_only || !r.source) { return; }
    var inSec = r.start || 0;
    var outSec = (r.end != null && r.end > inSec) ? r.end : inSec + 4;
    // insert at the playhead (after the current clip), not always at the end.
    var rep = await beckyCall('add_clip', { source: r.source, in: inSec, out: outSec, label: r.text || '', at: insertIndexAtPlayhead() });
    if (rep.ok && rep.data) { applyTimeline(rep.data); toast('Added to timeline'); }
    else { toast('Could not add clip' + (rep.error ? ': ' + rep.error : '')); }
  }

  /* ---- file click -> show its cues + play from the start ---- */
  async function onFileClick(name) {
    var v = null;
    for (var i = 0; i < state.videos.length; i++) { if (state.videos[i].name === name) { v = state.videos[i]; break; } }
    if (!v) { return; }
    // Remember the file-list scroll so "back" returns to this video, not the top.
    if (state.mode === 'files') { state.fileScrollTop = $listScroll.scrollTop; }
    state.activeSource = v.path;
    state.activeClipId = null;
    updatePlayhead();
    mpvPlay(v.path, 0);

    var rep = await beckyCall('transcript', { name: name });
    var cues = (rep.ok && Array.isArray(rep.data)) ? rep.data : [];
    state.mode = 'results';
    state.cueMode = true;          // single-transcript view: enables "search within this transcript"
    state.viewVideoName = name;
    state.cueAll = cues;
    state.cueFilter = '';
    state.rows = cues.slice();
    state.terms = [];
    state.activeResultKey = null;
    state.headerText = cueHeaderText();
    renderFind();
  }

  /* ---- transcribe one / all ---- */
  async function onTranscribeClick(name) {
    state.transcribing[name] = true;
    if (state.mode === 'files') { renderFiles(); }
    var rep = await beckyCall('transcribe', { name: name });
    delete state.transcribing[name];
    if (rep.ok && rep.data && Array.isArray(rep.data.videos)) {
      state.videos = rep.data.videos;
      if (rep.data.root) { state.folder = rep.data.root; }
    } else {
      toast('Transcribe failed' + (rep.error ? ': ' + rep.error : ''));
    }
    if (state.mode === 'files') { renderFiles(); }
  }

  async function onTranscribeAll() {
    state.transcribingAll = true;
    if (state.mode === 'files') { renderFiles(); }
    var rep = await beckyCall('transcribe_all', {});
    state.transcribingAll = false;
    if (rep.ok && rep.data) {
      var d = rep.data;
      var folder = d.folder || state.folder;
      if (folder) {
        var fv = await beckyCall('open_folder', { folder: folder });
        if (fv.ok && fv.data) { applyFolder(fv.data); }
      }
      toast('Transcribed ' + (d.transcribed || 0) + (d.failed ? (', ' + d.failed + ' failed') : ''));
    } else {
      toast('Transcribe all failed' + (rep.error ? ': ' + rep.error : ''));
    }
    if (state.mode === 'files') { renderFiles(); }
  }

  /* ---- auto-cut: silence-cut the open video, add its spoken segments as clips ----
     Calls the engine's autocut_silence (which shells out to becky-cut --dry-run — it
     only DECIDES, never renders/writes the source), then appends each keep-segment to
     the timeline. Degrades to a toast when becky-cut isn't available or found nothing. */
  var autocutting = false;
  async function onAutoCut(name) {
    if (autocutting || !name) { return; }
    autocutting = true;
    toast('Auto-cutting ' + name + '…');
    try {
      var rep = await beckyCall('autocut_silence', { name: name });
      var segs = (rep.ok && rep.data && Array.isArray(rep.data.segments)) ? rep.data.segments : [];
      if (!segs.length) {
        toast((rep.data && rep.data.note) ? rep.data.note : 'Auto-cut found no spoken segments.');
        return;
      }
      var v = videoByName(name);
      if (!v) { toast('Video not found: ' + name); return; }
      var lastTl = null, added = 0;
      for (var i = 0; i < segs.length; i++) {
        var r = await beckyCall('add_clip', { source: v.path, in: segs[i].in, out: segs[i].out, label: '' });
        if (r.ok && r.data) { lastTl = r.data; added++; }
      }
      if (lastTl) { applyTimeline(lastTl); }
      toast('Auto-cut: added ' + added + ' clip' + (added === 1 ? '' : 's') + ' to the timeline');
    } finally {
      autocutting = false;
    }
  }

  /* ---- delegated clicks for the whole find list ---- */
  function backToFiles() {
    $search.value = ''; $searchClear.hidden = true;
    state.query = ''; state.activeResultKey = null;
    state.cueMode = false; state.cueFilter = ''; state.cueAll = [];
    state.mode = 'files';
    renderFind();   // restores state.fileScrollTop (returning, not in-place)
  }

  // Apply a sort toggle. spec is "file:date"|"file:name"|"quote:date"|"quote:name".
  function applySortChange(spec) {
    var parts = String(spec || '').split(':');
    var kind = parts[0], mode = parts[1];
    if (mode !== 'date' && mode !== 'name' && mode !== 'relevance') { return; }
    if (kind === 'file' && mode === 'relevance') { return; }   // relevance applies to search results only
    var s = (kind === 'file') ? state.fileSort : state.quoteSort;
    if (s.mode === mode) { s.desc = !s.desc; }                 // re-click the active mode flips direction
    else { s.mode = mode; s.desc = true; }                     // switch mode -> default direction
    if (kind === 'file') {
      renderFiles();
    } else {
      state.rows = sortQuotes(state.searchRaw || [], state.quoteSort);
      state.activeResultKey = null;
      renderResults();
    }
  }

  $listScroll.addEventListener('click', function (e) {
    var sort = e.target.closest('[data-sort]');
    if (sort) { applySortChange(sort.dataset.sort); return; }
    var back = e.target.closest('[data-act="back-to-files"]');
    if (back) { backToFiles(); return; }
    var autocut = e.target.closest('[data-act="autocut"]');
    if (autocut) { onAutoCut(state.viewVideoName); return; }
    var tbtn = e.target.closest('.tbtn');
    if (tbtn) { if (!tbtn.disabled) { e.stopPropagation(); onTranscribeClick(tbtn.dataset.name); } return; }
    var all = e.target.closest('[data-act="transcribe-all"]');
    if (all) { if (!all.disabled) { onTranscribeAll(); } return; }
    var file = e.target.closest('.file');
    if (file) { onFileClick(file.dataset.name); return; }
    var row = e.target.closest('.qrow');
    if (row) { guardRowClick(row); }
  });

  // "Search within this transcript" (cue mode): filter the open video's cues live.
  $listScroll.addEventListener('input', function (e) {
    if (e.target && e.target.id === 'cueSearch') {
      state.cueFilter = e.target.value;
      state.rows = filterCues(state.cueAll, state.cueFilter);
      state.activeResultKey = null;
      state.headerText = cueHeaderText();
      renderResults();   // focusCueSearch() restores the caret
    }
  });
  $listScroll.addEventListener('dblclick', function (e) {
    var row = e.target.closest('.qrow');
    if (!row) { return; }
    if (rowClickTimer) { clearTimeout(rowClickTimer); rowClickTimer = null; }   // cancel the pending single-click play
    onRowDbl(+row.dataset.idx);
  });

  /* ---- drag a video / quote from the LEFT PANEL onto the timeline (item 21) ----------
     Dragging any listed video (from any subfolder of the open case folder) onto the
     timeline adds the WHOLE video as a clip; dragging a search/cue row adds that quote's
     clip. This is an in-app HTML5 drag, so it composes with click-to-play / double-click.
     External OS files are NOT added here — the forensic model serves only the open case
     folder; bring outside footage in by opening/pointing the case folder at it. */
  var panelDragPayload = null;
  $listScroll.addEventListener('dragstart', function (e) {
    var file = e.target.closest('.file');
    var row = e.target.closest('.qrow');
    if (file) {
      panelDragPayload = { kind: 'video', name: file.dataset.name };
    } else if (row) {
      var r = state.rows[+row.dataset.idx];
      if (!r || r.transcript_only || !r.source) { e.preventDefault(); return; }   // not a playable clip
      panelDragPayload = { kind: 'quote', idx: +row.dataset.idx };
    } else { return; }
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'copy';
      try { e.dataTransfer.setData('text/plain', 'becky-clip'); } catch (_) {}   // some engines require data set
    }
  });
  $listScroll.addEventListener('dragend', function () { panelDragPayload = null; });

  var $timelineSection = document.querySelector('.timeline');
  if ($timelineSection) {
    $timelineSection.addEventListener('dragover', function (e) {
      if (!panelDragPayload) { return; }   // only our own panel drag; ignore anything else
      e.preventDefault();                  // preventDefault on dragover is what allows a drop
      if (e.dataTransfer) { e.dataTransfer.dropEffect = 'copy'; }
      $timelineSection.classList.add('droptarget');
    });
    $timelineSection.addEventListener('dragleave', function (e) {
      if (!$timelineSection.contains(e.relatedTarget)) { $timelineSection.classList.remove('droptarget'); }
    });
    $timelineSection.addEventListener('drop', function (e) {
      $timelineSection.classList.remove('droptarget');
      var p = panelDragPayload; panelDragPayload = null;
      if (!p) { return; }
      e.preventDefault();
      if (p.kind === 'quote') { onRowDbl(p.idx); }
      else if (p.kind === 'video') { addWholeVideo(p.name); }
    });
  }

  // addWholeVideo probes the source duration and appends the ENTIRE video as one clip.
  async function addWholeVideo(name) {
    var v = videoByName(name);
    if (!v || !v.path) { toast('Video not found: ' + name); return; }
    var out = 0;
    var pr = await beckyCall('probe', { source: v.path });
    if (pr.ok && pr.data && pr.data.duration > 0) { out = pr.data.duration; }
    if (out <= 0) { out = 3600; }   // unknown duration -> a generous window the user can trim back
    var rep = await beckyCall('add_clip', { source: v.path, in: 0, out: out, label: '', at: insertIndexAtPlayhead() });
    if (rep.ok && rep.data) { applyTimeline(rep.data); toast('Added ' + name + ' to the timeline'); }
    else { toast('Could not add video' + (rep.error ? ': ' + rep.error : '')); }
  }

  $search.addEventListener('input', function () {
    $searchClear.hidden = !$search.value;
    clearTimeout(searchTimer);
    searchTimer = setTimeout(function () { doSearch($search.value); }, 200);
  });
  $search.addEventListener('keydown', function (e) {
    if (e.key === 'Enter') { clearTimeout(searchTimer); doSearch($search.value); }
  });
  $searchClear.addEventListener('click', function () {
    $search.value = ''; $searchClear.hidden = true; $search.focus(); doSearch('');
  });

  // Smart toggle: flip keyword <-> qmd hybrid, then re-run the current query.
  if ($smartToggle) {
    $smartToggle.addEventListener('click', function () {
      state.smartSearch = !state.smartSearch;
      $smartToggle.classList.toggle('on', state.smartSearch);
      $smartToggle.setAttribute('aria-pressed', state.smartSearch ? 'true' : 'false');
      $search.placeholder = state.smartSearch
        ? 'smart search — find meaning, not just words…'
        : 'search the transcripts...';
      if (state.query) { doSearch(state.query); }
    });
  }

  /* =======================================================================
     FOLDER
     ===================================================================== */
  function applyFolder(fv) {
    if (!fv || typeof fv !== 'object') { return; }
    state.folder = fv.root || state.folder;
    state.videos = Array.isArray(fv.videos) ? fv.videos : [];
    state.orphanCount = fv.orphan_count || 0;
    state.mode = 'files';
    state.cueMode = false;
    state.fileScrollTop = 0;   // a freshly opened folder starts at the top
    $search.value = ''; $searchClear.hidden = true;
    renderFind();
  }
  function onFolder(reply) {
    if (reply && reply.ok && reply.data) { applyFolder(reply.data); }
    else { toast('Could not open folder' + (reply && reply.error ? ': ' + reply.error : '')); }
  }

  // insertIndexAtPlayhead returns the index a NEW clip should land at so it sits right
  // AFTER the clip under the playhead (pushing the rest back). Empty timeline / no
  // playhead / past the end -> -1 (append).
  function insertIndexAtPlayhead() {
    var clips = state.timeline.clips || [];
    if (!clips.length || typeof state.playheadComp !== 'number') { return -1; }
    var c = clipAtComp(state.playheadComp);
    if (!c) { return -1; }
    var idx = clipIndexById(c.id);
    return idx < 0 ? -1 : idx + 1;
  }

  // Files dropped from Windows Explorer onto the window (item 21). Each video is added as
  // a whole clip AT the playhead (like a panel add), authorized as an external file.
  async function onExternalDrop(paths) {
    if (!Array.isArray(paths) || !paths.length) { return; }
    var at = insertIndexAtPlayhead(), added = 0;
    for (var i = 0; i < paths.length; i++) {
      var rep = await beckyCall('add_external', { path: paths[i], at: at });
      if (rep.ok && rep.data) { applyTimeline(rep.data); added++; if (at >= 0) { at++; } }
    }
    toast(added ? ('Added ' + added + ' dropped video' + (added === 1 ? '' : 's')) : 'Could not add the dropped file');
  }

  /* =======================================================================
     CHAT COLUMN
     ===================================================================== */
  function setChatIntro(text) {
    var intro = $messages.querySelector('.intro');
    if (!intro) {
      intro = document.createElement('div');
      intro.className = 'intro';
      $messages.prepend(intro);
    }
    intro.textContent = text;
  }

  function addMsg(role, html) {
    var el = document.createElement('div');
    el.className = 'msg ' + role;
    el.innerHTML = html;
    $messages.appendChild(el);
    $messages.scrollTop = $messages.scrollHeight;
    return el;
  }
  function addUserMsg(t) { return addMsg('user', '<div class="bubble">' + escapeHtml(t) + '</div>'); }
  function addBeckyMsg(t, caption) {
    var cap = caption ? '<div class="caption">' + escapeHtml(caption) + '</div>' : '';
    return addMsg('becky', '<div class="bubble">' + escapeHtml(t) + '</div>' + cap);
  }

  function previewLineText(line) {
    if (typeof line === 'string') { return line; }
    if (line && (line.text || line.label)) { return line.text || line.label; }
    try { return JSON.stringify(line); } catch (_) { return String(line); }
  }
  function sourceText(s) {
    if (typeof s === 'string') { return s; }
    return (s && (s.name || s.source)) || '';
  }

  function renderProposal(p) {
    p = p || {};
    var cap = ['via ' + (p.tier || 'becky'), p.note || ''].filter(Boolean).join(' — ');
    addBeckyMsg(p.preview_text || '(no preview)', cap);

    if (!Array.isArray(p.actions) || !p.actions.length) { return; }
    proposals[p.id] = p;

    var previewLines = (Array.isArray(p.preview) ? p.preview : [])
      .map(function (line) { return '<div class="pv">' + escapeHtml(previewLineText(line)) + '</div>'; })
      .join('');
    var srcs = (Array.isArray(p.sources) && p.sources.length)
      ? '<div class="psrc">sources: ' + escapeHtml(p.sources.map(sourceText).filter(Boolean).join(', ')) + '</div>'
      : '';

    var card = document.createElement('div');
    card.className = 'card';
    card.dataset.pid = p.id;
    card.innerHTML =
      '<div class="card-actions-label">' + p.actions.length + ' action' + (p.actions.length === 1 ? '' : 's') + ' proposed</div>' +
      '<div class="card-preview">' + (previewLines || '<div class="pv dim">(no preview lines)</div>') + '</div>' +
      srcs +
      '<div class="card-actions">' +
        '<button class="approve" data-act="approve">✓ approve</button>' +
        '<button class="reject" data-act="reject">✗ reject</button>' +
      '</div>';
    $messages.appendChild(card);
    $messages.scrollTop = $messages.scrollHeight;
  }

  // approve / reject (delegated on the messages container)
  $messages.addEventListener('click', async function (e) {
    var btn = e.target.closest('.card-actions button');
    if (!btn) { return; }
    var card = btn.closest('.card');
    var pid = card.dataset.pid;
    var actions = card.querySelector('.card-actions');

    if (btn.dataset.act === 'approve') {
      card.querySelectorAll('button').forEach(function (b) { b.disabled = true; });
      var rep = await beckyCall('apply_proposal', { id: pid });
      if (rep.ok && rep.data) {
        if (rep.data.timeline) { applyTimeline(rep.data.timeline); }
        var n = Array.isArray(rep.data.exec_commands) ? rep.data.exec_commands.length : 0;
        card.classList.add('applied');
        actions.innerHTML = '<span class="done-tag">✓ applied' +
          (n ? ' (' + n + ' command' + (n === 1 ? '' : 's') + ')' : '') + '</span>';
      } else {
        card.querySelectorAll('button').forEach(function (b) { b.disabled = false; });
        toast('Apply failed' + (rep.error ? ': ' + rep.error : ''));
      }
    } else {
      await beckyCall('reject_proposal', { id: pid });
      card.classList.add('rejected');
      actions.innerHTML = '<span class="done-tag dim">✗ rejected</span>';
    }
    delete proposals[pid];
  });

  // chips
  function renderChips() {
    $chips.innerHTML = CHIPS.map(function (c) {
      return '<button class="chip" data-chip="' + attr(c) + '">' + escapeHtml(c) + '</button>';
    }).join('');
  }
  $chips.addEventListener('click', function (e) {
    var chip = e.target.closest('.chip');
    if (!chip) { return; }
    $ask.value = chip.dataset.chip;
    $ask.focus();
  });

  // ask
  async function sendAsk() {
    var text = $ask.value.trim();
    if (!text) { return; }
    if (state.activeQuestionId) { saveAnswer(text); return; }   // answering a review question, not asking becky
    $ask.value = '';
    addUserMsg(text);
    var rep = await beckyCall('ask', { utterance: text });
    if (!rep.ok) { addBeckyMsg('(could not reach becky: ' + (rep.error || 'error') + ')'); return; }
    renderProposal(rep.data || {});
  }
  $send.addEventListener('click', sendAsk);
  $ask.addEventListener('keydown', function (e) { if (e.key === 'Enter') { sendAsk(); } });

  // ---- human-review Q&A panel (engine: questions/save_answer; becky-hits sidecar) ----
  // A forensic hit-list can attach a "?" question to clips. becky-hits groups them into a
  // sidecar the engine pre-loads; here each question is a clickable card in the right
  // panel: click it -> its clips are selected + played in order so Jordan can answer, and
  // his answer is saved to _forensic_answers.json (an agent routes it into the wiki).
  var $questions = document.getElementById('questions');
  state.questions = [];
  state.activeQuestionId = null;

  function renderQuestions() {
    if (!$questions) { return; }
    var qs = state.questions || [];
    if (!qs.length) { $questions.hidden = true; $questions.innerHTML = ''; return; }
    $questions.hidden = false;
    var open = qs.filter(function (q) { return !q.answered; }).length;
    var html = '<div class="qhead">Review questions <span class="qcount">' + open + ' open</span></div>';
    for (var i = 0; i < qs.length; i++) {
      var q = qs[i];
      var rgb = hexToRgb(PALETTE[i % PALETTE.length]);
      var accent = 'rgb(' + rgb[0] + ',' + rgb[1] + ',' + rgb[2] + ')';
      var active = (String(state.activeQuestionId) === String(q.id));
      var n = (q.clip_ids || []).length;
      var meta = q.answered ? '✓ answered' : (n + ' clip' + (n === 1 ? '' : 's') + ' — click to watch');
      html += '<div class="qcard' + (q.answered ? ' answered' : '') + (active ? ' active' : '') +
              '" data-qid="' + attr(q.id) + '" style="border-left-color:' + accent + '">' +
                '<div class="qtext">' + escapeHtml(q.question) + '</div>' +
                '<div class="qmeta">' + meta + '</div>' +
                (q.answered && q.answer ? '<div class="qans">' + escapeHtml(q.answer) + '</div>' : '') +
              '</div>';
    }
    $questions.innerHTML = html;
  }

  if ($questions) {
    $questions.addEventListener('click', function (e) {
      var card = e.target.closest('.qcard');
      if (card) { selectQuestion(card.dataset.qid); }
    });
  }

  function selectQuestion(id) {
    var qs = state.questions || [], q = null, i;
    for (i = 0; i < qs.length; i++) { if (String(qs[i].id) === String(id)) { q = qs[i]; break; } }
    if (!q) { return; }
    state.activeQuestionId = String(id);
    // select the clips this question is about + play from the first, so he watches them in order
    var ids = (q.clip_ids || []).filter(function (c) { return !!clipById(c); }).map(String);
    if (ids.length) {
      state.selectedClipIds = ids;
      state.selectedClipId = ids[0];
      markSelectedClip();
      seekClipById(ids[0], true);
    }
    $ask.placeholder = 'answer: ' + q.question;
    $ask.focus();
    renderQuestions();
  }

  async function saveAnswer(text) {
    var id = state.activeQuestionId;
    var q = (state.questions || []).filter(function (x) { return String(x.id) === String(id); })[0];
    $ask.value = '';
    $ask.placeholder = 'ask becky...';
    state.activeQuestionId = null;
    var rep = await beckyCall('save_answer', { id: id, question: q ? q.question : '', answer: text });
    if (rep.ok && rep.data && rep.data.questions) { state.questions = rep.data.questions; renderQuestions(); }
    else { addBeckyMsg('(could not save answer: ' + (rep.error || 'error') + ')'); return; }
    addBeckyMsg('Answer saved' + (q ? ' for: "' + q.question + '"' : '') + '. It will be routed into the wiki.');
  }

  // use Claude toggle
  $useClaude.addEventListener('change', async function () {
    state.online = $useClaude.checked;
    var rep = await beckyCall('set_online', { on: state.online });
    if (rep.ok && rep.data && typeof rep.data.online === 'boolean') {
      state.online = rep.data.online;
      $useClaude.checked = state.online;
    }
    toast(state.online ? 'Using Claude (online)' : 'Using local Gemma');
  });

  /* =======================================================================
     TIMELINE
     ===================================================================== */
  function sumDur(clips) {
    var t = 0;
    for (var i = 0; i < clips.length; i++) { t += clipDur(clips[i]); }
    return t;
  }

  // resumeAt (optional): the compilation position to resume playback at when this
  // edit landed while playing, INSTEAD OF the live state.playheadComp. Needed by
  // multi-await callers (splitAtPlayhead awaits set_trim, then add_clip, then
  // reorder): during those round trips playback keeps advancing, so by the time
  // this runs state.playheadComp has drifted PAST the position the edit was
  // actually made at — resuming there can land in the NEXT clip entirely (the
  // "split skips playback to the next clip" bug). Callers that captured their
  // own frozen position pass it; everyone else keeps today's behavior.
  function applyTimeline(tl, resumeAt) {
    if (!tl || typeof tl !== 'object') { return; }
    var wasEmpty = !((state.timeline.clips) || []).length;   // to auto-fit the view on the FIRST clip
    state.timeline = {
      clips: Array.isArray(tl.clips) ? tl.clips : [],
      overlay: tl.overlay || {},
      duration_sec: typeof tl.duration_sec === 'number' ? tl.duration_sec : 0
    };
    state.tlVersion++;   // the timeline changed -> the seamless EDL must regenerate before next play/seek
    invalidateQuiet();   // clip positions/durations changed -> recompute the threshold's quiet stretches (item 16)
    state.overlayRender = !!(state.timeline.overlay && state.timeline.overlay.enabled);
    state.overlayShowName = state.timeline.overlay.show_filename !== false;   // init the filename toggle from the reel
    if (state.activeClipId != null && !clipById(state.activeClipId)) { state.activeClipId = null; }
    // Drop any selected ids whose clips no longer exist (after remove/undo/load).
    state.selectedClipIds = (state.selectedClipIds || []).filter(function (id) { return !!clipById(id); });
    if (state.selectedClipId != null && !clipById(state.selectedClipId)) { state.selectedClipId = null; }
    renderTimeline();
    // Loading clips into an empty timeline: fit the view to ~10s, or to the WHOLE
    // compilation if it's longer than 10s (so every clip is visible at once), and put
    // the playhead at the very beginning. Later edits leave his manual zoom alone.
    if (wasEmpty && state.timeline.clips.length) {
      var totalDur = state.timeline.duration_sec || sumDur(state.timeline.clips);
      fitTimelineZoom(Math.max(10, totalDur));
      state.activeClipId = state.timeline.clips[0].id;
      state.playheadComp = state.timeline.clips[0].start_sec || 0;
      updatePlayhead();
    } else if (state.playing && isTimelineLoaded()) {
      // A cut/trim/reorder/delete just landed WHILE the seamless timeline was
      // playing: tlVersion was bumped above, so the loaded EDL is now stale. Reload
      // it right away at the same compilation position so playback keeps going on
      // the EDITED timeline instead of silently drifting on the old one — that
      // drift is what "makes a cut while playing break the timeline". Prefer the
      // caller's frozen resumeAt over the live (possibly drifted) playheadComp.
      var at = (typeof resumeAt === 'number') ? resumeAt : (state.playheadComp || 0);
      state.playheadComp = at;
      seekTimeline(at, true);
    }
  }

  /* ---- zoom (CHANGE 5): one px-per-second scale drives clip widths + the ruler ---- */
  function minClipW() {
    // A SMALL floor so a clip stays visible + clickable at any zoom. The handles are
    // absolute overlays now and a click anywhere on the clip selects it, so a clip no
    // longer needs room for two flex handles. Keeping this floor small is what stops a
    // cut from shifting the following clips: with the old 96px floor, splitting any clip
    // whose halves fell under it doubled their width. Now only sub-second clips floor.
    return Math.max(3, Math.min(16, state.pxPerSec * 3));
  }
  function clipW(dur) { return Math.max(minClipW(), (dur || 0) * state.pxPerSec); }
  function updateZoomLabel() { if ($tZoom) { $tZoom.textContent = state.pxPerSec + ' px/s'; } }
  // setZoom changes the scale and keeps the point under anchorClientX (or the viewport
  // centre) fixed, so zoom grows/shrinks toward the cursor instead of the left edge.
  function setZoom(px, anchorClientX) {
    var v = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, Math.round(px)));
    if (v === state.pxPerSec) {
      // Rounding fixed point (the real "scroll-to-zoom stops working" bug): at a
      // small integer like 2 or 3, round(v*1.15) rounds right back down to v itself
      // — EVERY wheel tick after that computes the exact same no-op forever, no
      // matter how many times you scroll, until a bigger jump (the +/- buttons use
      // 1.5x) crosses the rounding boundary. Force the smallest real step instead
      // so a wheel tick can never get permanently stuck just above ZOOM_MIN.
      if (px > state.pxPerSec && state.pxPerSec < ZOOM_MAX) { v = state.pxPerSec + 1; }
      else if (px < state.pxPerSec && state.pxPerSec > ZOOM_MIN) { v = state.pxPerSec - 1; }
      else { updateZoomLabel(); return; }
    }
    var old = state.pxPerSec;
    var anchorX = null, contentX = 0;
    if (tlBodyEl) {
      var rect = tlBodyEl.getBoundingClientRect();
      anchorX = (typeof anchorClientX === 'number') ? (anchorClientX - rect.left) : (rect.width / 2);
      contentX = anchorX + tlBodyEl.scrollLeft;   // content-space x under the anchor
    }
    state.pxPerSec = v;
    renderTimeline();   // re-render clip widths + ruler at the new scale
    if (tlBodyEl && anchorX != null) {
      // ponytail: linear rescale of scrollLeft; the per-clip min-width floor makes this
      // slightly approximate for very short clips — fine for a zoom anchor.
      tlBodyEl.scrollLeft = contentX * (v / old) - anchorX;
    }
  }
  function zoomBy(factor) { setZoom(state.pxPerSec * factor); }
  function zoomAt(factor, clientX) { setZoom(state.pxPerSec * factor, clientX); }
  // The playhead's current CLIENT-space x (converting its content-space left, which
  // is relative to the scrollable track, through the current scroll offset) — so a
  // zoom can anchor on the playhead instead of wherever the mouse happens to be.
  function playheadClientX() {
    if (!tlBodyEl || playheadEl.style.display === 'none') { return null; }
    // tlBodyEl (the scroll viewport) sits at a FIXED screen position; trackEl (the
    // scrolled content) does not, so anchoring off trackEl's rect here double-counted
    // the scroll offset. playheadEl.style.left is content-space (see updatePlayhead);
    // subtract scrollLeft to land in tlBodyEl's own viewport space, then add its rect.
    var rect = tlBodyEl.getBoundingClientRect();
    var leftPx = parseFloat(playheadEl.style.left || '0');
    return rect.left + (leftPx - tlBodyEl.scrollLeft);
  }
  // fitTimelineZoom sets the scale so `seconds` fills the visible track width — the
  // default view (Jordan: a new project should show ~10s, not be zoomed way out; a
  // first clip longer than 10s expands to fit itself). setZoom clamps + re-renders.
  function fitTimelineZoom(seconds) {
    if (!tlBodyEl) { return; }
    var w = tlBodyEl.clientWidth || 0;
    if (w < 40) { return; }               // layout not ready yet — skip
    setZoom(w / Math.max(1, seconds || 10));
  }

  // Trailing scroll space: reserve ~one viewport-width of empty room to the RIGHT of the
  // last clip so you can always scroll a little past the end (item 19). Without it the
  // scrollable width ended exactly at the last clip, so its end "locked" against the
  // right edge and the only way to see empty space was to zoom ALL the way out until the
  // whole reel fit. Set as the track's right padding (clips keep their left offsets, so
  // playhead/scrub math is unaffected).
  function updateTrailingSpace() {
    if (!trackEl || !tlBodyEl) { return; }
    trackEl.style.paddingRight = Math.max(0, tlBodyEl.clientWidth || 0) + 'px';
  }

  /* Scale the TRACK grid lines to the current zoom (a faint line every 5s). The ruler
     now shows real labelled timecode ticks instead — see renderRulerTicks. */
  function applyTimelineScale() {
    var maj = state.pxPerSec * 5;
    trackEl.style.backgroundImage =
      'repeating-linear-gradient(90deg, transparent 0 ' + (maj - 1) + 'px, var(--line-2) ' + (maj - 1) + 'px ' + maj + 'px)';
  }

  // niceRulerStep: seconds between LABELLED ticks, chosen so labels sit >= ~64px apart
  // at the current zoom (so they never crowd, and spread out as you zoom in).
  function niceRulerStep(pxps) {
    var steps = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600];
    for (var i = 0; i < steps.length; i++) { if (steps[i] * pxps >= 64) { return steps[i]; } }
    return steps[steps.length - 1];
  }

  // renderRulerTicks fills the gray ruler with timecode marks at compilation time
  // s = 0, step, 2*step, ... across the whole reel. left = s * pxPerSec matches the
  // track's time grid. pointer-events on the ticks are off (CSS) so the ruler's
  // click/drag still works beneath them.
  function renderRulerTicks() {
    var clips = state.timeline.clips || [];
    var total = state.timeline.duration_sec || sumDur(clips);
    if (!total || total <= 0) { rulerEl.innerHTML = ''; return; }
    var step = niceRulerStep(state.pxPerSec);
    var useH = total >= 3600;
    var html = '';
    for (var s = 0; s <= total + 0.001; s += step) {
      var left = Math.round(s * state.pxPerSec);
      html += '<span class="rtick" style="left:' + left + 'px">' + (useH ? hms(s) : mmss(s)) + '</span>';
    }
    rulerEl.innerHTML = html;
  }

  /* A clip is a SOLID block with NO visible text (CHANGE 3): label + duration ride
     in the title= tooltip only. Keeps the two trim handles + a hover-only remove "x".
     The empty .cbody fills between the handles and forwards clicks to the scrubber. */
  // isClipSelected reports whether a clip id is in the multi-selection.
  function isClipSelected(id) {
    var ids = state.selectedClipIds || [];
    for (var i = 0; i < ids.length; i++) { if (String(ids[i]) === String(id)) { return true; } }
    return false;
  }

  // The standard becky colour order (Jordan's palette) — used wherever several things
  // are colour-coded (timeline clips by source, and Q&A later). Assigned in order of
  // first appearance so the first source is green, the second blue, etc.
  var PALETTE = ['#14FF39', '#00AEEF', '#DC143C', '#8A2BE2', '#FF57D1', '#FFD700', '#16F0EA', '#FF8C00'];
  function hexToRgb(h) {
    h = h.replace('#', '');
    return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
  }
  // sourceColorIndex maps a source path to a PALETTE slot, assigned the FIRST time the
  // source is seen and REMEMBERED for the whole project session (item 17). Colours are
  // handed out by a monotonic counter, NOT recomputed from the current clip order — so
  // deleting every clip of one video never shifts another video's colour (the old
  // recompute-from-order code did exactly that). A freshly LOADED reel resets the map
  // (see $tLoad) so a new project starts back at green.
  var sourceColorMap = {};   // source path -> palette index (session-persistent)
  var sourceColorNext = 0;   // next slot to hand out
  function sourceColorIndex(src) {
    var key = String(src || '');
    if (!(key in sourceColorMap)) {
      sourceColorMap[key] = sourceColorNext % PALETTE.length;
      sourceColorNext++;
    }
    return sourceColorMap[key];
  }
  function resetSourceColors() { sourceColorMap = {}; sourceColorNext = 0; }
  // clipColor tints the clip CENTRE with its source colour: faint when unselected,
  // SOLID/opaque when selected (the selection cue).
  function clipColor(src, selected) {
    var rgb = hexToRgb(PALETTE[sourceColorIndex(src)]);
    return 'rgba(' + rgb[0] + ',' + rgb[1] + ',' + rgb[2] + ',' + (selected ? 0.9 : 0.24) + ')';
  }
  // clipBorder outlines the clip in its OWN source colour (near-full opacity) when
  // unselected; a selected clip gets a white edge so it still reads as selected.
  function clipBorder(src, selected) {
    if (selected) { return '#ffffff'; }
    var rgb = hexToRgb(PALETTE[sourceColorIndex(src)]);
    return 'rgba(' + rgb[0] + ',' + rgb[1] + ',' + rgb[2] + ',0.95)';
  }

  function clipBlockHTML(clip) {
    var dur = clipDur(clip);
    var w = clipW(dur);
    var label = clip.label || (clip.source ? baseName(clip.source) : 'clip');
    var tip = truncate(label, 80) + '  (' + mmss(dur) + ')';
    var seld = isClipSelected(clip.id);
    var bord = clipBorder(clip.source, seld);   // border AND trim handles share this (never plain green)
    return '<div class="clip' + (seld ? ' selected' : '') + '" data-id="' + attr(clip.id) +
             '" style="width:' + w + 'px;background:' + clipColor(clip.source, seld) + ';border-color:' + bord + ';--clip-col:' + bord + '" title="' + attr(tip) + '">' +
             '<div class="cwave"></div>' +
             '<div class="rh rh-l" data-edge="l" title="trim in"></div>' +
             '<div class="cthumb"></div>' +
             '<div class="cbody"></div>' +
             '<button class="cx" data-act="remove" title="remove clip">×</button>' +
             '<div class="rh rh-r" data-edge="r" title="trim out"></div>' +
           '</div>';
  }

  // makeClipBlock builds ONE clip node from clipBlockHTML (so a new clip gets the full
  // markup + initial inline styling from the single source of truth).
  function makeClipBlock(clip) {
    var tmp = document.createElement('div');
    tmp.innerHTML = clipBlockHTML(clip);
    return tmp.firstChild;
  }

  // updateClipBlock patches an EXISTING clip node in place (width, colour, selection,
  // tooltip) WITHOUT touching its .cwave / .cthumb children — that is what keeps the
  // waveform + thumbnail ALIVE across an edit (the anti-flash, item 11). applyWaves /
  // applyThumbs refresh those only when the clip's own window actually changed.
  function updateClipBlock(block, clip) {
    var dur = clipDur(clip);
    var seld = isClipSelected(clip.id);
    var bord = clipBorder(clip.source, seld);
    block.classList.toggle('selected', seld);
    block.style.width = clipW(dur) + 'px';
    block.style.background = clipColor(clip.source, seld);
    block.style.borderColor = bord;
    block.style.setProperty('--clip-col', bord);
    var label = clip.label || (clip.source ? baseName(clip.source) : 'clip');
    block.title = truncate(label, 80) + '  (' + mmss(dur) + ')';
    refitWave(block, clip);   // keep the reused waveform at the right scale (no split/trim squish)
  }

  // refitWave re-crops a reused clip's waveform to its CURRENT window. The waveform SVG
  // is NEVER replaced on an edit (replacing innerHTML is the jarring flash) — cropWave
  // just moves the viewBox over the same SVG, which is instant and flicker-free.
  function refitWave(block, clip) {
    var el = block.querySelector('.cwave');
    if (el) { cropWave(el, clip); }
  }

  // reconcileTrack makes the track's .clip nodes match `clips` by REUSING existing nodes
  // (matched by data-id) instead of wiping trackEl.innerHTML (item 11). A surviving clip
  // keeps its DOM — and therefore its rendered waveform + thumbnail — through a trim /
  // cut / reorder / delete, so nothing goes blank-then-repaints (the "flash"). Only
  // genuinely new clips are created; removed clips are dropped; order is fixed up. The
  // stock + playhead (non-.clip children) are untouched here and re-appended by the caller.
  function reconcileTrack(clips) {
    var existing = {};
    var old = Array.prototype.slice.call(trackEl.querySelectorAll('.clip'));
    for (var i = 0; i < old.length; i++) { existing[old[i].dataset.id] = old[i]; }

    var wanted = {}, nodes = [];
    for (var j = 0; j < clips.length; j++) {
      var clip = clips[j], id = String(clip.id);
      wanted[id] = true;
      var block = existing[id];
      if (block) { updateClipBlock(block, clip); } else { block = makeClipBlock(clip); }
      nodes.push(block);
    }
    // drop clip nodes no longer present
    for (var k = 0; k < old.length; k++) {
      if (!wanted[old[k].dataset.id] && old[k].parentNode === trackEl) { trackEl.removeChild(old[k]); }
    }
    // order the clip nodes at the FRONT of the track, in clips order
    var ref = null;
    for (var m = 0; m < nodes.length; m++) {
      var node = nodes[m];
      var want = ref ? ref.nextSibling : trackEl.firstChild;
      if (node !== want) { trackEl.insertBefore(node, want); }
      ref = node;
    }
  }

  function renderTimeline() {
    var clips = state.timeline.clips || [];
    var dur = state.timeline.duration_sec || sumDur(clips);
    $tlCount.textContent = clips.length + ' clip' + (clips.length === 1 ? '' : 's') + ' · ' + mmss(dur);
    updateOverlayBtn();
    updateZoomLabel();
    applyTimelineScale();
    updateTrailingSpace();   // room to scroll past the last clip (item 19)
    renderRulerTicks();   // timecode marks in the gray ruler

    // Reconcile clip nodes by id instead of wiping innerHTML, so a surviving clip keeps
    // its waveform + thumbnail across an edit — no flash (item 11). The "no clips" hint
    // is a lone non-.clip child managed alongside the reconcile.
    var hint = trackEl.querySelector('.tlempty');
    if (!clips.length) {
      reconcileTrack([]);   // remove every clip block
      if (!hint) {
        hint = document.createElement('div');
        hint.className = 'tlempty';
        hint.textContent = 'No clips yet — double-click a quote to add one to the timeline.';
        trackEl.insertBefore(hint, trackEl.firstChild);
      }
    } else {
      if (hint && hint.parentNode === trackEl) { trackEl.removeChild(hint); }
      reconcileTrack(clips);
    }
    trackEl.appendChild(quietLayerEl); // playback-threshold dim layer (item 16); z-index keeps it under the bars
    trackEl.appendChild(stockEl);      // secondary stock (item 1) — under the moving playhead
    trackEl.appendChild(playheadEl);
    trackEl.appendChild(thresholdBarEl); // the draggable threshold bar (item 16), on top
    refreshClipGeom();           // cache clip left/width once so playback ticks don't reflow
    updatePlayhead();
    renderThreshold();           // reposition the threshold bar + dim rects at the new scale (item 16)
    updateRenderSelButton();     // keep the render-selection button in sync with the redraw
    prefetchSourceDurations();   // CHANGE C: warm each source's true duration so a resize is bounded immediately
    applyThumbs();               // paint each clip's cached first-frame thumbnail (async, cheap)
    applyWaves();                // paint each clip's cached audio waveform (async, windowed)
    applyProxies();              // build each visible clip's windowed scrub proxy (async, cached)
  }

  // Scrolling the timeline horizontally reveals more clips: fetch their thumb/waveform/
  // proxy then (debounced), so a big reel only ever spends ffmpeg on what's on screen.
  var mediaScrollTimer = null;
  if (tlBodyEl) {
    tlBodyEl.addEventListener('scroll', function () {
      clearTimeout(mediaScrollTimer);
      mediaScrollTimer = setTimeout(function () { applyThumbs(); applyWaves(); applyProxies(); }, 120);
    });
  }

  /* ---- timeline clip thumbnails -----------------------------------------------
     Each clip shows a tiny first-frame still (its in-point) so Jordan can tell at a
     glance which clip is which. The engine extracts a small CACHED jpeg once per
     (source, in) and returns it as a data: URI (no media server). We cache per key
     on the JS side too and request at most a couple at a time, so a busy timeline
     never spawns a storm of ffmpeg. A source with no thumbnail (no ffmpeg) just
     stays the plain neon slab — degrade, never block. */
  var thumbCache = {};      // "source@t" -> data URI ('' = known none, don't retry)
  var thumbInflight = {};   // "source@t" -> true while its request is in flight
  var thumbQueue = [];
  var thumbActive = 0;
  var THUMB_MAX = 2;        // max concurrent ffmpeg thumbnail grabs
  function thumbKey(src, t) { return (src || '') + '@' + (Math.round((t || 0) * 1000) / 1000); }
  function applyThumbs() {
    var blocks = trackEl.querySelectorAll('.clip');
    for (var i = 0; i < blocks.length; i++) {
      var b = blocks[i];
      var clip = clipById(b.dataset.id);
      if (!clip || !clip.source) { continue; }
      var key = thumbKey(clip.source, clip.in || 0);
      var cached = thumbCache[key];
      if (cached) {
        var cthumb = b.querySelector('.cthumb');
        if (cthumb && cthumb.dataset.thumbKey !== key) {
          cthumb.style.backgroundImage = 'url("' + cached + '")';
          cthumb.dataset.thumbKey = key;
        }
      } else if (cached === undefined && !thumbInflight[key] && clipVisible(clip.id)) {
        thumbInflight[key] = true;
        thumbQueue.push({ src: clip.source, t: clip.in || 0, key: key });
        pumpThumbs();
      }
    }
  }
  function pumpThumbs() {
    while (thumbActive < THUMB_MAX && thumbQueue.length) {
      var job = thumbQueue.shift();
      thumbActive++;
      (function (job) {
        beckyCall('thumb', { source: job.src, t: job.t }).then(function (rep) {
          thumbActive--;
          delete thumbInflight[job.key];
          thumbCache[job.key] = (rep && rep.ok && rep.data && rep.data.data) ? rep.data.data : '';
          applyThumbs();   // paint any rendered clip whose thumb just arrived
          pumpThumbs();
        });
      })(job);
    }
  }

  /* ---- per-clip audio waveform (engine 'peaks' verb; windowed to the clip) --------
     Same lazy/cached/throttled shape as thumbnails: the engine returns normalized
     amplitude buckets for the clip's OWN (source, in, out) window; we draw them ONCE
     as a stretch-to-fit SVG so zoom needs no redraw. Degrades to no waveform (empty
     peaks) when there's no audio/ffmpeg — never blocks. */
  var waveCache = {};       // key -> SVG string ('' = known none, don't retry)
  var peakData = {};        // key -> raw [0..1] peaks (drives the playback threshold, item 16)
  var waveInflight = {};
  var waveQueue = [];
  var waveActive = 0;
  var WAVE_MAX = 2;         // max concurrent ffmpeg peak extractions
  var WAVE_BUCKETS = 200;
  var WAVE_PAD = 6;         // seconds of extra waveform fetched each side, so trims/extends
                           // stay INSIDE the coverage and just crop (never refetch = no flash)
  function waveKey(src, inS, outS) {
    return (src || '') + '@' + (Math.round((inS || 0) * 1000) / 1000) + '-' + (Math.round((outS || 0) * 1000) / 1000);
  }
  // buildWaveSvg turns [0..1] peaks into a filled, center-mirrored waveform path in a
  // 0..(n-1) x 0..1 viewBox; preserveAspectRatio=none lets CSS stretch it to any width.
  function buildWaveSvg(peaks) {
    var n = peaks.length;
    if (n < 2) { return ''; }
    var pts = [];
    for (var i = 0; i < n; i++) { var a = Math.max(0, Math.min(1, peaks[i])); pts.push(i + ',' + (0.5 - a * 0.5).toFixed(3)); }
    for (var j = n - 1; j >= 0; j--) { var b = Math.max(0, Math.min(1, peaks[j])); pts.push(j + ',' + (0.5 + b * 0.5).toFixed(3)); }
    return '<svg viewBox="0 0 ' + (n - 1) + ' 1" preserveAspectRatio="none"><path d="M' + pts.join(' L') + ' Z"/></svg>';
  }
  // Waveforms are keyed by SOURCE and CROPPED per clip — the load-bearing anti-flash.
  // A source's waveform is fetched once for some window; any clip whose [in,out] falls
  // INSIDE that window reuses the SAME SVG, cropped to its portion. Editing (trim/split)
  // then only moves a viewBox — it never re-fetches or replaces the SVG, so there is no
  // "redraw the whole waveform" flash and no wrong-scale squish. A split's two halves
  // both crop from the parent's waveform (no fetch at all).
  var sourceWaves = {};     // source -> [{covIn, covOut, svg, n}] fetched waveforms (by window)

  // findCoveringWave returns the TIGHTEST cached waveform of `source` whose window fully
  // contains [inS,outS], or null.
  function findCoveringWave(source, inS, outS) {
    var list = sourceWaves[source];
    if (!list) { return null; }
    var best = null;
    for (var i = 0; i < list.length; i++) {
      var w = list[i];
      if (inS >= w.covIn - 1e-6 && outS <= w.covOut + 1e-6) {
        if (!best || (w.covOut - w.covIn) < (best.covOut - best.covIn)) { best = w; }
      }
    }
    return best;
  }
  // peaksForClip returns the raw [0..1] peaks over a clip's [in,out] — either the exact
  // window's peaks, or (for a clip whose waveform was seeded from a wider covering window,
  // e.g. a split half) that window's peaks sliced to the clip. Keeps the playback
  // threshold (item 16) accurate even when no per-clip peaks were ever fetched.
  function peaksForClip(c) {
    var src = c.source, cin = c.in || 0, cout = c.out || 0;
    var exact = peakData[waveKey(src, cin, cout)];
    if (exact && exact.length) { return exact; }
    var list = sourceWaves[src];
    if (!list) { return null; }
    for (var i = 0; i < list.length; i++) {
      var w = list[i];
      if (cin >= w.covIn - 1e-6 && cout <= w.covOut + 1e-6) {
        var full = peakData[waveKey(src, w.covIn, w.covOut)];
        if (full && full.length) {
          var od = w.covOut - w.covIn;
          var a = Math.floor((cin - w.covIn) / od * full.length);
          var b = Math.ceil((cout - w.covIn) / od * full.length);
          return full.slice(Math.max(0, a), Math.min(full.length, b));
        }
      }
    }
    return null;
  }
  // cropWave moves the .cwave SVG's viewBox to show exactly the clip's [in,out] slice of
  // its stored coverage window (set by setWave). No innerHTML change -> no flash.
  function cropWave(el, clip) {
    var svg = el.querySelector('svg');
    var covIn = parseFloat(el.dataset.waveCovIn), covOut = parseFloat(el.dataset.waveCovOut), N = parseFloat(el.dataset.waveN);
    if (!svg || !(N > 0) || !(covOut > covIn)) { return; }
    var od = covOut - covIn;
    var nin = Math.max(covIn, clip.in || 0), nout = Math.min(covOut, clip.out || 0);
    if (nout <= nin) { return; }
    var minX = (nin - covIn) / od * N;
    var wX = Math.max(0.01, (nout - nin) / od * N);
    svg.setAttribute('viewBox', minX.toFixed(3) + ' 0 ' + wX.toFixed(3) + ' 1');
  }
  // setWave installs a cached waveform (once) + records the window it covers, then crops
  // it to the clip. This is the ONLY place .cwave innerHTML is written for a clip.
  function setWave(el, wave, clip) {
    el.innerHTML = wave.svg;
    el.dataset.waveCovIn = String(wave.covIn);
    el.dataset.waveCovOut = String(wave.covOut);
    el.dataset.waveN = String(wave.n);
    cropWave(el, clip);
  }
  // waveCovers allows a small TOLERANCE past the covered window, so a frame-extend or a
  // tiny trim just clamps to the coverage edge (imperceptible) instead of refetching +
  // replacing the whole SVG (a flash). Only a MEANINGFUL extend beyond that re-fetches.
  function waveCovers(el, clip) {
    var covIn = parseFloat(el.dataset.waveCovIn), covOut = parseFloat(el.dataset.waveCovOut);
    var TOL = 0.5;
    return (covOut > covIn) && (clip.in || 0) >= covIn - TOL && (clip.out || 0) <= covOut + TOL;
  }

  function applyWaves() {
    var blocks = trackEl.querySelectorAll('.clip');
    for (var i = 0; i < blocks.length; i++) {
      var b = blocks[i];
      var clip = clipById(b.dataset.id);
      if (!clip || !clip.source) { continue; }
      var el = b.querySelector('.cwave');
      if (!el) { continue; }
      // Already has a waveform whose window COVERS this clip -> leave the SVG untouched
      // (refitWave crops it on edits). Never re-set an existing covering SVG: that
      // innerHTML replace is exactly the flash.
      if (el.querySelector('svg') && waveCovers(el, clip)) { continue; }
      // No usable SVG: reuse a cached waveform of this source that covers the window (a
      // split's new half, a re-add, a trim) -> crop it. No fetch, no flash.
      var cov = findCoveringWave(clip.source, clip.in || 0, clip.out || 0);
      if (cov) { setWave(el, cov, clip); continue; }
      // Otherwise fetch this window ONCE, PADDED each side (so later trims/extends stay
      // inside the coverage and just crop, never refetch = no flash). The right pad must
      // be clamped to the real source duration (ffmpeg returns fewer samples past the
      // end), so defer the fetch until the duration is probed — a fast, one-time wait.
      if (!sourceDur.has(clip.source)) {
        // Probe the duration ONCE, then re-run applyWaves when it lands. Attach the
        // re-run only when NO probe is in flight — attaching it to an already-pending
        // ensureSourceDuration (which returns an immediately-resolved promise) would
        // reschedule applyWaves every microtask and hang the page.
        if (!sourceDurPending[clip.source]) { ensureSourceDuration(clip.source).then(function () { applyWaves(); }); }
        continue;
      }
      var srcDur = knownSourceDuration(clip.source);
      var fin = Math.max(0, (clip.in || 0) - WAVE_PAD);
      var fout = (clip.out || 0) + WAVE_PAD;
      if (srcDur > 0) { fout = Math.min(fout, srcDur); }
      var key = waveKey(clip.source, fin, fout);
      if (waveCache[key] === '') { continue; }   // known no-audio for this window
      if (!waveInflight[key]) {
        waveInflight[key] = true;
        waveQueue.push({ src: clip.source, in: fin, out: fout, key: key });
        pumpWaves();
      }
    }
  }
  function pumpWaves() {
    while (waveActive < WAVE_MAX && waveQueue.length) {
      var job = waveQueue.shift();
      waveActive++;
      (function (job) {
        beckyCall('peaks', { source: job.src, in: job.in, out: job.out, buckets: WAVE_BUCKETS }).then(function (rep) {
          waveActive--;
          delete waveInflight[job.key];
          var peaks = (rep && rep.ok && rep.data && Array.isArray(rep.data.peaks)) ? rep.data.peaks : [];
          var svg = peaks.length ? buildWaveSvg(peaks) : '';
          waveCache[job.key] = svg;
          peakData[job.key] = peaks;      // keep raw peaks for the threshold's quiet-detection
          if (svg) {
            if (!sourceWaves[job.src]) { sourceWaves[job.src] = []; }
            sourceWaves[job.src].push({ covIn: job.in, covOut: job.out, svg: svg, n: peaks.length - 1 });
          }
          invalidateQuiet();              // fresh peaks -> recompute which stretches are quiet
          if (state.thresholdOn) { renderThreshold(); }
          applyWaves();
          pumpWaves();
        });
      })(job);
    }
  }

  /* ---- windowed scrub proxy (engine 'scrub_segment') --------------------------------
     For each ON-SCREEN clip, ask the engine to build (once, cached on disk) an
     intra-frame proxy of ONLY that clip's [in,out) window — the raw long-GOP source is
     what scrubs slowly. TimelineEDL then PREFERS a cached proxy (else the raw source, so
     it can never regress). We don't use the returned path here; the engine caches it and
     the EDL finds it. When proxies land, mark the loaded EDL stale so the next idle
     seek/play adopts them (never reloads mpv on its own — no scrub hitch, no blink). */
  var proxyRequested = {};   // window-key -> true (asked this session; the file is cached on disk)
  var proxyQueue = [];
  var proxyActive = 0;
  var PROXY_MAX = 1;         // one segment-transcode at a time — a background nicety, don't peg CPU
  // REVERTED (2026-07-02): building proxies for the WHOLE reel regardless of
  // visibility sounded good on paper, but every proxy that finishes calls
  // markEdlStaleWhenIdle() below, which invalidates the loaded EDL — so a
  // reel with many un-proxied clips turned into a marathon of background
  // builds that kept yanking the EDL out from under playback the whole time
  // ("timeline doesn't play at all while a new segment is building a proxy").
  // Back to viewport-gated: only build for what's actually on screen.
  function applyProxies() {
    var blocks = trackEl.querySelectorAll('.clip');
    for (var i = 0; i < blocks.length; i++) {
      var clip = clipById(blocks[i].dataset.id);
      if (!clip || !clip.source) { continue; }
      var key = waveKey(clip.source, clip.in || 0, clip.out || 0);   // same window key as the waveform
      if (proxyRequested[key] || !clipVisible(clip.id)) { continue; }
      proxyRequested[key] = true;
      proxyQueue.push({ src: clip.source, in: clip.in || 0, out: clip.out || 0 });
      pumpProxies();
    }
  }
  function pumpProxies() {
    while (proxyActive < PROXY_MAX && proxyQueue.length) {
      var job = proxyQueue.shift();
      proxyActive++;
      beckyCall('scrub_segment', { source: job.src, in: job.in, out: job.out }).then(function (rep) {
        proxyActive--;
        // Only invalidate the EDL once the WHOLE batch has drained, not after each
        // individual clip — with several clips queued, invalidating per-clip meant
        // the EDL kept getting yanked out from under playback for the entire
        // stretch while more proxies were still building ("timeline doesn't play
        // at all while a new segment is building a proxy").
        if (rep && rep.ok && rep.data && rep.data.path && proxyActive === 0 && !proxyQueue.length) {
          markEdlStaleWhenIdle();
        }
        pumpProxies();
      });
    }
  }
  var proxySettleTimer = null;
  function markEdlStaleWhenIdle() {
    clearTimeout(proxySettleTimer);
    proxySettleTimer = setTimeout(function () {
      // Only when idle (not playing, not mid-scrub): invalidate the loaded EDL so the
      // NEXT seek/play rebuilds it to adopt the freshly-built proxies. This never reloads
      // mpv on its own, so it can't interrupt playback or hitch an active scrub.
      if (!state.playing && !scrubbing && state.edlPath) { state.edlVersion = -1; }
    }, 1500);
  }

  /* ---- playback threshold (item 16): skip the quiet stretches during playback --------
     A view-faster aid for long clips. From each clip's cached waveform peaks, the
     stretches whose amplitude stays below thresholdLevel are "quiet"; during playback we
     SEEK past them (never cut the reel — the forensic evidence is untouched). The
     on-timeline bar drags thresholdLevel; the quiet stretches are dimmed. OFF by default
     => zero effect on normal playback. */
  var MIN_SKIP_SEC = 0.35;   // don't bother seeking past a quiet gap shorter than this
  var quietCache = null;     // memoized quiet intervals (compilation seconds); null = stale
  function invalidateQuiet() { quietCache = null; }

  // quietIntervals returns merged [start,end] compilation-time stretches below the
  // threshold and longer than MIN_SKIP_SEC. Memoized until threshold/clips/peaks change.
  function quietIntervals() {
    if (quietCache) { return quietCache; }
    var clips = state.timeline.clips || [], raw = [];
    for (var i = 0; i < clips.length; i++) {
      var c = clips[i];
      var peaks = peaksForClip(c);
      if (!peaks || !peaks.length) { continue; }   // no peaks yet -> treat as loud (never skip blindly)
      var n = peaks.length, dur = clipDur(c), start = c.start_sec || 0, runStart = -1;
      for (var b = 0; b <= n; b++) {
        var quiet = (b < n) && (peaks[b] < state.thresholdLevel);
        if (quiet && runStart < 0) { runStart = b; }
        else if (!quiet && runStart >= 0) {
          raw.push({ start: start + (runStart / n) * dur, end: start + (b / n) * dur });
          runStart = -1;
        }
      }
    }
    raw.sort(function (a, b) { return a.start - b.start; });
    var merged = [];
    for (var k = 0; k < raw.length; k++) {
      var last = merged[merged.length - 1];
      if (last && raw[k].start <= last.end + 0.06) { last.end = Math.max(last.end, raw[k].end); }
      else { merged.push({ start: raw[k].start, end: raw[k].end }); }
    }
    quietCache = merged.filter(function (iv) { return (iv.end - iv.start) >= MIN_SKIP_SEC; });
    return quietCache;
  }
  // quietEndAt returns the END of the quiet stretch covering comp (to seek past), or null.
  function quietEndAt(comp) {
    var iv = quietIntervals();
    for (var i = 0; i < iv.length; i++) {
      if (comp >= iv[i].start && comp < iv[i].end - 0.05) { return iv[i].end; }
    }
    return null;
  }
  // thresholdLevel <-> the bar's Y within the track's clip lane (top half = louder).
  function trackLane() {
    var h = trackEl.clientHeight || 180, pad = 8;
    return { top: pad, height: Math.max(1, h - 2 * pad) };
  }
  function thresholdBarY() {
    var l = trackLane();
    return l.top + (0.5 - state.thresholdLevel / 2) * l.height;   // t=0 -> lane centre; t=1 -> lane top
  }
  function levelFromY(y) {
    var l = trackLane();
    return Math.max(0.01, Math.min(1, (0.5 - (y - l.top) / l.height) * 2));
  }
  // compToPixel maps a compilation time to a track pixel via the ACTUAL clip geometry
  // (respecting each clip's min-width floor) — the SAME mapping the playhead uses. The
  // dim rects MUST use this, not comp*pxPerSec: whenever a short clip is drawn wider than
  // its true time (the min-width floor), a time-based position drifts from the clip it's
  // supposed to sit on, and the drift accumulates rightward — the exact "the dim doesn't
  // correspond to the waveform" inaccuracy.
  function compToPixel(comp) {
    var clip = clipAtComp(comp);
    if (!clip) { return comp * state.pxPerSec; }
    var g = clipGeom[clip.id];
    if (!g) { var b = blockById(clip.id); if (!b) { return comp * state.pxPerSec; } g = { left: b.offsetLeft, width: b.offsetWidth }; }
    var d = clipDur(clip);
    var frac = d > 0 ? (comp - (clip.start_sec || 0)) / d : 0;
    return g.left + Math.max(0, Math.min(1, frac)) * g.width;
  }

  // renderThreshold positions the bar + paints the dim rectangles over the quiet
  // stretches. Called on render, zoom, threshold change, and as peaks arrive.
  function renderThreshold() {
    if (!state.thresholdOn) { quietLayerEl.style.display = 'none'; thresholdBarEl.style.display = 'none'; return; }
    // Span the bar over the CLIPS only (not the empty trailing scroll space), so it reads
    // as a level ON the waveforms rather than a line across the whole timeline.
    var blocks = trackEl.querySelectorAll('.clip');
    var lastRight = blocks.length ? (blocks[blocks.length - 1].offsetLeft + blocks[blocks.length - 1].offsetWidth) : 0;
    thresholdBarEl.style.display = 'block';
    thresholdBarEl.style.top = thresholdBarY() + 'px';
    thresholdBarEl.style.width = lastRight + 'px';
    var iv = quietIntervals(), html = '';
    for (var i = 0; i < iv.length; i++) {
      var x = Math.round(compToPixel(iv[i].start));
      var w = Math.max(1, Math.round(compToPixel(iv[i].end) - x));
      html += '<div class="qdim" style="left:' + x + 'px;width:' + w + 'px"></div>';
    }
    quietLayerEl.innerHTML = html;
    quietLayerEl.style.display = 'block';
  }
  // Drag the threshold bar up/down to set the level.
  var thresholdDrag = false;
  thresholdBarEl.addEventListener('pointerdown', function (e) {
    if (!state.thresholdOn) { return; }
    e.preventDefault(); e.stopPropagation();
    thresholdDrag = true;
    try { thresholdBarEl.setPointerCapture(e.pointerId); } catch (_) {}
  });
  thresholdBarEl.addEventListener('pointermove', function (e) {
    if (!thresholdDrag) { return; }
    var rect = trackEl.getBoundingClientRect();
    state.thresholdLevel = levelFromY(e.clientY - rect.top);
    invalidateQuiet();
    renderThreshold();
  });
  function endThresholdDrag() { thresholdDrag = false; }
  thresholdBarEl.addEventListener('pointerup', endThresholdDrag);
  thresholdBarEl.addEventListener('pointercancel', endThresholdDrag);
  if ($tThreshold) {
    $tThreshold.addEventListener('click', function () {
      state.thresholdOn = !state.thresholdOn;
      $tThreshold.classList.toggle('on', state.thresholdOn);
      if ($tTrimSilence) { $tTrimSilence.disabled = !state.thresholdOn; }   // trim-silence needs the level set
      invalidateQuiet();
      renderThreshold();
      if (state.thresholdOn) { toast('Playback threshold on — quiet parts are skipped during playback (evidence is not cut)'); }
    });
  }

  // trimSilence REBUILDS the timeline as the LOUD segments of the current clips (dropping
  // everything below the threshold) — a REAL edit, one undoable step (Ctrl+Z restores it).
  // For basic editing, NOT forensics. Uses the same peaks + level the on-timeline
  // threshold shows.
  var trimming = false;
  async function trimSilence() {
    if (trimming) { return; }
    var clips = state.timeline.clips || [];
    if (!clips.length) { toast('Timeline is empty.'); return; }
    var MIN_KEEP = 0.3;   // drop loud runs shorter than this (clicks / noise)
    var specs = [];
    for (var i = 0; i < clips.length; i++) {
      var c = clips[i];
      var peaks = peaksForClip(c);
      if (!peaks || !peaks.length) {   // no peak data -> keep the whole clip (never blind-cut)
        specs.push({ source: c.source, in: c.in || 0, out: c.out || 0, label: c.label || '' });
        continue;
      }
      var n = peaks.length, dur = clipDur(c), inS = c.in || 0, runStart = -1;
      for (var b = 0; b <= n; b++) {
        var loud = (b < n) && (peaks[b] >= state.thresholdLevel);
        if (loud && runStart < 0) { runStart = b; }
        else if (!loud && runStart >= 0) {
          var s0 = inS + (runStart / n) * dur, s1 = inS + (b / n) * dur;
          if (s1 - s0 >= MIN_KEEP) { specs.push({ source: c.source, in: s0, out: s1, label: c.label || '' }); }
          runStart = -1;
        }
      }
    }
    if (!specs.length) { toast('Nothing above the threshold to keep.'); return; }
    trimming = true;
    try {
      var rep = await beckyCall('set_clips', { clips: specs });
      if (rep.ok && rep.data) {
        applyTimeline(rep.data);
        toast('Trimmed to the loud parts (' + specs.length + ' clip' + (specs.length === 1 ? '' : 's') + ') — Ctrl+Z to undo');
      } else { toast('Trim failed' + (rep.error ? ': ' + rep.error : '')); }
    } finally { trimming = false; }
  }
  if ($tTrimSilence) { $tTrimSilence.addEventListener('click', trimSilence); }

  function updateOverlayBtn() {
    // Three states (item 14): render-on/preview-off (default) | render-on/preview-on |
    // off. Green highlight = shown in THIS preview; ✓ = rendering, 👁 = also previewed,
    // ✗ = off entirely. The label swaps only the trailing glyph so its width stays put.
    var previewed = !!state.overlayPreview, renders = !!state.overlayRender;
    $tOverlay.classList.toggle('on', previewed);
    $tOverlay.classList.toggle('off', !renders);
    $tOverlay.textContent = !renders ? 'overlay ✗' : (previewed ? 'overlay 👁' : 'overlay ✓');
    $tOverlay.title = !renders
      ? 'Overlay OFF — not shown here, not burned into the render. Click to turn it back on.'
      : (previewed
          ? 'Overlay ON and shown in this preview (also burned into the render). Click to turn it off.'
          : 'Overlay ON — burned into the render, but hidden in this preview so it doesn’t cover the video. Click to also show it here.');
    if ($tOverlayName) { $tOverlayName.classList.toggle('on', state.overlayShowName !== false); }
  }

  /* ---- which clip/video supplies the overlay's static fields ---- */
  function activeMeta() {
    if (state.activeClipId != null) {
      var c = clipById(state.activeClipId);
      if (c && c.meta) { return { date: c.meta.date, link: c.meta.link, fps: c.meta.source_fps }; }
    }
    for (var i = 0; i < state.videos.length; i++) {
      if (state.videos[i].path === state.activeSource) {
        var v = state.videos[i];
        return { date: v.date, link: v.link, fps: v.source_fps };
      }
    }
    return {};
  }

  // Push the forensic lower-third's content to the host for the CURRENT clip. During
  // seamless EDL playback mpv has "timeline.edl" loaded, so we must send the real
  // clip's source filename + a tc_off (clip.in - clip.start_sec) so the overlay shows
  // the right name and the SOURCE timecode. For a single-source preview, tc_off = 0.
  var lastOverlayClipId = null;
  function sendOverlayUpdate() {
    var file = '', date = '', link = '', fps = 30, tcOff = 0, clipId = null;
    if (isTimelineLoaded()) {
      var clip = clipById(state.activeClipId) || clipAtComp(state.playheadComp || 0);
      if (clip) {
        file = clip.source || ''; date = clip.date || ''; link = clip.link || '';
        fps = clip.source_fps || 30; tcOff = (clip.in || 0) - (clip.start_sec || 0); clipId = clip.id;
      }
    } else {
      file = state.activeSource || '';
      var m = activeMeta(); date = m.date || ''; link = m.link || ''; fps = m.fps || 30;
    }
    lastOverlayClipId = clipId;
    mpvSend('overlay', { on: state.overlayPreview, file: file, date: date, link: link, fps: fps, tc_off: tcOff, showName: state.overlayShowName !== false });
  }

  /* ---- seamless timeline playback via an mpv EDL --------------------------------
     The whole reel loads as ONE mpv EDL (a virtual gapless source), so playing it
     plays exactly the trimmed clips back-to-back with NO per-clip reload and no
     blink; mpv plays to the end and holds the last frame. The position mpv reports
     IS the compilation position, so there is no per-clip mapping and no "advance"
     logic — that earlier hop-on-out code is what caused the one-frame-at-a-time bug. */
  function isTimelineLoaded() { return !!state.edlPath && state.activeSource === state.edlPath; }

  // The clip occupying compilation position comp (for the playhead + overlay).
  function clipAtComp(comp) {
    var clips = state.timeline.clips || [];
    for (var i = 0; i < clips.length; i++) {
      var c = clips[i], s = c.start_sec || 0, d = clipDur(c);
      if (comp >= s - 0.001 && comp < s + d) { return c; }
    }
    return clips.length ? clips[clips.length - 1] : null;   // at/after the end -> the last clip
  }

  // (re)generate the timeline EDL whenever the timeline changed; cached + in-flight-guarded.
  function ensureEdl() {
    if (state.edlPath && state.edlVersion === state.tlVersion) { return Promise.resolve(state.edlPath); }
    if (state.edlInflight) { return state.edlInflight; }
    var want = state.tlVersion;
    state.edlInflight = beckyCall('timeline_edl', {}).then(function (rep) {
      state.edlInflight = null;
      if (rep.ok && rep.data && rep.data.path) {
        state.edlPath = rep.data.path; state.edlVersion = want; state.edlDur = rep.data.duration || 0;
        return state.edlPath;
      }
      return null;
    });
    return state.edlInflight;
  }

  // Position the timeline at compilation second `comp`. play=true keeps/starts
  // playback; play=false holds the frame PAUSED (navigation). Reuses the loaded EDL
  // when it is current (a fast seek); otherwise (re)loads the fresh EDL ONCE — a
  // drag that fires many seeks before the load finishes coalesces to the latest
  // target (edlLoading guard), so a ruler scrub never reloads mpv repeatedly.
  var edlLoading = false, pendingSeek = null;
  // A paused seek (keyboard nav / click) races an in-flight {t:"time"} message that
  // was already queued from BEFORE mpvSeek was sent — it can arrive AFTER the fresh,
  // correct position report and silently overwrite it with the stale pre-seek value.
  // onTime ignores anything that disagrees with the seek we JUST asked for, for a
  // short settle window, while paused. Without this, Ctrl+Left/Right could snap back
  // right after landing, so the next press recomputed from the wrong spot and got
  // stuck re-finding the same boundary instead of walking further.
  var lastSeekTarget = null, lastSeekAt = 0;
  var SEEK_SETTLE_MS = 500, SEEK_TOL_SEC = 0.25;
  async function seekTimeline(comp, play) {
    if (!(state.timeline.clips || []).length) { return; }
    if (isTimelineLoaded() && state.edlVersion === state.tlVersion) {
      if (!play) { lastSeekTarget = comp; lastSeekAt = performance.now(); }
      mpvSeek(comp);                                  // already the current EDL -> just seek
      if (play && !state.playing) { mpvSend('resume'); }
      else if (!play && state.playing) { mpvSend('pause'); }
      return;
    }
    pendingSeek = { comp: comp, play: play };         // remember the latest target
    if (edlLoading) { return; }                       // a load is already running; it'll use pendingSeek
    edlLoading = true;
    var path = await ensureEdl();
    edlLoading = false;
    if (!path) { return; }
    var tgt = pendingSeek || { comp: comp, play: play };
    pendingSeek = null;
    state.activeSource = path;
    if (tgt.play) { mpvPlay(path, tgt.comp); } else { mpvLoadAt(path, tgt.comp); }
  }

  /* ---- playhead (driven by incoming {t:"time"} messages) ---- */
  function onTime(pos, dur) {
    state.pos = (typeof pos === 'number') ? pos : 0;
    state.dur = (typeof dur === 'number') ? dur : 0;
    if (isTimelineLoaded()) {
      if (!state.playing && lastSeekTarget != null) {
        if (performance.now() - lastSeekAt > SEEK_SETTLE_MS) {
          lastSeekTarget = null;                        // settle window elapsed either way
        } else if (Math.abs(state.pos - lastSeekTarget) > SEEK_TOL_SEC) {
          updatePlayhead();
          return;                                       // stale report — don't clobber playheadComp
        } else {
          lastSeekTarget = null;                         // this IS the seek landing — done watching
        }
      }
      state.playheadComp = state.pos;                 // EDL position IS the compilation position
      // Playback threshold (item 16): if playback has entered a quiet stretch, seek PAST
      // it (skip it) — a faster review, never a cut. Gated by thresholdOn so it's a no-op
      // otherwise. The seek settles at the stretch's end, where it's no longer quiet.
      if (state.thresholdOn && state.playing) {
        var qEnd = quietEndAt(state.pos);
        if (qEnd != null) {
          state.playheadComp = qEnd;
          mpvSeek(qEnd);
          updatePlayhead();
          return;
        }
      }
      var c = clipAtComp(state.pos);
      state.activeClipId = c ? c.id : null;
      // as the seamless compilation crosses a cut, refresh the overlay to the new clip's name + TC
      if (state.overlayPreview && state.activeClipId !== lastOverlayClipId) { sendOverlayUpdate(); }
    } else if (state.activeClipId != null) {
      var ac = clipById(state.activeClipId);
      if (ac) { state.playheadComp = (ac.start_sec || 0) + (state.pos - (ac.in || 0)); }
    }
    updatePlayhead();
  }

  // The host reports mpv's real pause state (a command, the spacebar, OR a click on
  // the video) — the single source of truth for whether playback is running.
  function onPlayState(paused) { state.playing = !paused; }

  // The host saved a screenshot of the current preview frame (Screenshot_NNNN.png
  // in the case folder's render/ dir, auto-incrementing — see MainWindow.TakeScreenshotAsync).
  function onScreenshot(path) {
    if (path) { toast('Screenshot saved: ' + baseName(path)); }
    else { toast('Screenshot failed'); }
  }

  // Play/pause for the ▶ button + spacebar. If there are timeline clips but the
  // seamless EDL isn't the current loaded source, START timeline playback from the
  // playhead (load the fresh EDL + play); otherwise just toggle what's loaded (the
  // EDL, or a search-result preview).
  function togglePlay() {
    var clips = state.timeline.clips || [];
    if (state.playing) {
      // Pausing snaps BACK to the bookmark (wherever this play session started, or
      // wherever a clip-body click landed while auditioning ahead) instead of
      // stopping wherever playback happens to be — that's what Enter is for.
      mpvSend('pause');
      if (typeof state.pauseReturnComp === 'number' && isTimelineLoaded()) {
        seekTimeline(state.pauseReturnComp, false);
      }
      state.autoScrollSuspended = false;
      return;
    }
    // Starting (or resuming) playback: this position becomes the NEW return point.
    state.pauseReturnComp = state.playheadComp;
    state.autoScrollSuspended = false;
    if (clips.length && (!isTimelineLoaded() || state.edlVersion !== state.tlVersion)) {
      seekTimeline(state.playheadComp || 0, true);
    } else {
      mpvSend('toggle');
    }
  }

  // Cached clip geometry (left/width px), refreshed once per render, so the playhead —
  // moved on every playback tick — never forces a layout by reading offsetLeft live.
  var clipGeom = {};
  function refreshClipGeom() {
    clipGeom = {};
    var blocks = trackEl.querySelectorAll('.clip');
    for (var i = 0; i < blocks.length; i++) {
      clipGeom[blocks[i].dataset.id] = { left: blocks[i].offsetLeft, width: blocks[i].offsetWidth };
    }
  }
  // clipVisible reports whether a clip block is in (or near) the horizontal viewport —
  // the gate that stops a big timeline from firing an ffmpeg thumb/waveform for EVERY
  // clip at once (the "storm"). Uses the cached geometry + a 300px prefetch margin.
  // Unknown geometry → true (safe: fetch rather than hide).
  function clipVisible(id) {
    var g = clipGeom[id];
    if (!g || !tlBodyEl) { return true; }
    var lo = tlBodyEl.scrollLeft - 300;
    var hi = tlBodyEl.scrollLeft + (tlBodyEl.clientWidth || 0) + 300;
    return (g.left + g.width) >= lo && g.left <= hi;
  }
  function updatePlayhead() {
    updateStock();   // keep the secondary stock positioned + blinking alongside the playhead
    var comp, clip;
    if (isTimelineLoaded()) {
      comp = state.playheadComp || 0; clip = clipAtComp(comp);
    } else if (state.activeClipId != null) {
      clip = clipById(state.activeClipId);
      comp = clip ? (clip.start_sec || 0) + (state.pos - (clip.in || 0)) : 0;
    } else {
      playheadEl.style.display = 'none'; return;
    }
    if (!clip) { playheadEl.style.display = 'none'; return; }
    var g = clipGeom[clip.id];
    if (!g) {                                   // cache miss (mid-resize / pre-paint) — read live
      var block = blockById(clip.id);
      if (!block) { playheadEl.style.display = 'none'; return; }
      g = { left: block.offsetLeft, width: block.offsetWidth };
    }
    var d = clipDur(clip);
    var frac = d > 0 ? (comp - (clip.start_sec || 0)) / d : 0;
    frac = Math.max(0, Math.min(1, frac));
    var leftPx = g.left + frac * g.width;
    playheadEl.style.left = leftPx + 'px';
    playheadEl.style.display = 'block';
    // The timeline stays PUT — it only jumps when the playhead reaches the
    // edge of what's visible, landing it back near the center, then holds
    // still again until the next edge. Not continuous re-centering (that
    // fights the view while you're trying to look at something else), and
    // never during an in-progress drag, or the content would shift out from
    // under the cursor while resizing/reordering/scrubbing.
    if (tlBodyEl && !resizing && !clipGesture && !scrubbing && !state.autoScrollSuspended) {
      var EDGE_MARGIN = 40;
      var viewL = tlBodyEl.scrollLeft, viewR = viewL + tlBodyEl.clientWidth;
      if (leftPx < viewL + EDGE_MARGIN || leftPx > viewR - EDGE_MARGIN) {
        tlBodyEl.scrollLeft = Math.max(0, leftPx - tlBodyEl.clientWidth / 2);
      }
    }
  }

  // updateStock positions the secondary playhead stock at pauseReturnComp and blinks it
  // (item 2) while the user is auditioning ahead during playback (autoScrollSuspended).
  // Same clip-geometry math as the playhead. Hidden when there's no timeline / no stock.
  function updateStock() {
    // When paused, the stock rides WITH the playhead — so arrow-key navigation (and any
    // paused seek) moves it too, and resuming returns to where you are. During playback
    // it stays put (or wherever you clicked ahead).
    if (!state.playing && typeof state.playheadComp === 'number' && isTimelineLoaded()) {
      state.pauseReturnComp = state.playheadComp;
    }
    var comp = state.pauseReturnComp;
    if (typeof comp !== 'number' || !isTimelineLoaded()) { stockEl.style.display = 'none'; return; }
    var clip = clipAtComp(comp);
    if (!clip) { stockEl.style.display = 'none'; return; }
    var g = clipGeom[clip.id];
    if (!g) {
      var block = blockById(clip.id);
      if (!block) { stockEl.style.display = 'none'; return; }
      g = { left: block.offsetLeft, width: block.offsetWidth };
    }
    var d = clipDur(clip);
    var frac = d > 0 ? (comp - (clip.start_sec || 0)) / d : 0;
    frac = Math.max(0, Math.min(1, frac));
    stockEl.style.left = (g.left + frac * g.width) + 'px';
    stockEl.style.display = 'block';
    // Blink only while auditioning ahead during playback; steady otherwise (paused, or
    // coincident with the playhead right after play-start / Enter).
    stockEl.classList.toggle('flashing', !!(state.playing && state.autoScrollSuspended));
  }

  /* ---- selection: toggle .selected + repaint each clip's opacity, no re-render ---- */
  function markSelectedClip() {
    var blocks = trackEl.querySelectorAll('.clip');
    for (var i = 0; i < blocks.length; i++) {
      var sel = isClipSelected(blocks[i].dataset.id);
      blocks[i].classList.toggle('selected', sel);
      var c = clipById(blocks[i].dataset.id);
      if (c) {
        var bcol = clipBorder(c.source, sel);
        blocks[i].style.background = clipColor(c.source, sel);   // opaque when selected
        blocks[i].style.borderColor = bcol;                       // own colour, or white when selected
        blocks[i].style.setProperty('--clip-col', bcol);          // trim handles match the clip (never plain green)
      }
    }
    updateRenderSelButton();
  }

  // The "render selection" button is ALWAYS present (item 12): grayed/disabled with no
  // selection, active + count-labelled when clips are selected. It used to be hidden
  // when nothing was selected, which shifted every other toolbar button sideways. A
  // reserved min-width (app.css) keeps its width constant so the count never nudges the
  // export button either.
  function updateRenderSelButton() {
    if (!$tRenderSel) { return; }
    var n = (state.selectedClipIds || []).length;
    $tRenderSel.disabled = n === 0;
    $tRenderSel.textContent = n > 0 ? ('render selection (' + n + ')') : 'render selection';
  }

  // Selection mutators. clearSelection / selectOnly / toggleInSelection / selectRange
  // all keep selectedClipId (the anchor/primary) and selectedClipIds (the full set)
  // consistent, then repaint the outlines + the render-selection button.
  function clearSelection() {
    state.selectedClipIds = [];
    state.selectedClipId = null;
    markSelectedClip();
  }
  function selectOnly(id) {
    state.selectedClipIds = [String(id)];
    state.selectedClipId = String(id);
    markSelectedClip();
  }
  function toggleInSelection(id) {
    id = String(id);
    var ids = state.selectedClipIds || [];
    if (isClipSelected(id)) {
      state.selectedClipIds = ids.filter(function (x) { return String(x) !== id; });
      if (String(state.selectedClipId) === id) {
        state.selectedClipId = state.selectedClipIds.length ? state.selectedClipIds[state.selectedClipIds.length - 1] : null;
      }
    } else {
      state.selectedClipIds = ids.concat([id]);
      state.selectedClipId = id;   // a Ctrl+click makes the clicked clip the new anchor
    }
    markSelectedClip();
  }
  // selectRange selects every clip between the anchor and id (inclusive) in timeline
  // order — Shift+click. Falls back to a single select when there is no anchor.
  function selectRange(id) {
    var clips = state.timeline.clips || [];
    var ai = -1, bi = -1;
    for (var i = 0; i < clips.length; i++) {
      if (String(clips[i].id) === String(state.selectedClipId)) { ai = i; }
      if (String(clips[i].id) === String(id)) { bi = i; }
    }
    if (ai < 0 || bi < 0) { selectOnly(id); return; }
    var lo = Math.min(ai, bi), hi = Math.max(ai, bi), out = [];
    for (var j = lo; j <= hi; j++) { out.push(String(clips[j].id)); }
    state.selectedClipIds = out;
    // anchor stays where it was so a second Shift+click re-ranges from the same point
    markSelectedClip();
  }

  async function onClipRemove(id) {
    var rep = await beckyCall('remove_clip', { id: id });
    if (rep.ok && rep.data) { applyTimeline(rep.data); }
  }

  // deleteSelectedClips ripple-deletes ALL selected clips. Bound to Delete/Backspace
  // AND Escape (Jordan wants two hotkeys for delete). The engine auto-ripples start_sec.
  async function deleteSelectedClips() {
    var ids = (state.selectedClipIds || []).slice();
    if (!ids.length) { return; }
    clearSelection();
    var okAny = false, lastTl = null;
    // A delete ripples subsequent clips left, which shifts the playhead's rendered
    // pixel position even though the user didn't navigate anywhere — that shift
    // could trip the sticky-scroll edge check and jump the view. Deleting should
    // only move the CLIPS (the ripple), never the viewport, so pin scrollLeft
    // across the reload and restore it (clamped in case the timeline got shorter).
    var keepScroll = tlBodyEl ? tlBodyEl.scrollLeft : null;

    // If the playhead sits on a clip that ISN'T being deleted, remember its id +
    // offset WITHIN that clip — the ripple shifts every later clip's start_sec, so
    // the playhead's old absolute compilation position no longer means "the same
    // spot in that clip" afterward. Re-anchor it once the ripple lands (item 6).
    var rippleClip = (typeof state.playheadComp === 'number') ? clipAtComp(state.playheadComp) : null;
    var rippleOffset = null;
    if (rippleClip && ids.indexOf(rippleClip.id) === -1) {
      rippleOffset = state.playheadComp - (rippleClip.start_sec || 0);
    }

    for (var i = 0; i < ids.length; i++) {
      var rep = await beckyCall('remove_clip', { id: ids[i] });   // server-side remove auto-ripples start_sec
      if (rep.ok && rep.data) { okAny = true; lastTl = rep.data; }
    }

    // Re-anchor the playhead to the SAME spot in its (now-shifted) clip. Compute it from
    // the FINAL timeline BEFORE applying it, so we can hand applyTimeline the correct
    // resume position: passing resumeAt makes a delete WHILE PLAYING keep playing from
    // the right place (item 6) instead of the stale pre-ripple position — the whole
    // point of "playback is not affected".
    var resumeAt = null, movedId = null;
    if (rippleOffset != null && lastTl && Array.isArray(lastTl.clips)) {
      for (var k = 0; k < lastTl.clips.length; k++) {
        if (String(lastTl.clips[k].id) === String(rippleClip.id)) {
          resumeAt = (lastTl.clips[k].start_sec || 0) + rippleOffset;
          movedId = lastTl.clips[k].id;
          break;
        }
      }
    }

    if (lastTl) { applyTimeline(lastTl, resumeAt == null ? undefined : resumeAt); }

    if (resumeAt != null) {
      state.playheadComp = resumeAt;
      if (movedId != null) { state.activeClipId = movedId; }
      // Re-sync the stock to the playhead so pause-after-delete returns somewhere sane
      // (the audition target, if any, was on a clip that may have just moved).
      state.pauseReturnComp = resumeAt;
      state.autoScrollSuspended = false;
      if (!state.playing) { seekTimeline(resumeAt, false); }
      updatePlayhead();
    }

    if (tlBodyEl && keepScroll != null) {
      tlBodyEl.scrollLeft = Math.min(keepScroll, tlBodyEl.scrollWidth - tlBodyEl.clientWidth);
    }
    if (!okAny) { toast('Could not remove clip'); }
  }

  /* ---- split / cut at the playhead (CHANGE 4): button + "s" key ----
     The clip under the COMPILATION playhead (start_sec <= playheadComp < start_sec+dur) is cut
     into two at the equivalent SOURCE time. There is no engine "split" verb, so we re-trim the
     left half (set_trim) then add the right half (add_clip) and reorder it to sit right after. */
  var splitting = false;
  async function splitAtPlayhead() {
    if (splitting) { return; }
    var clips = state.timeline.clips || [];
    if (!clips.length) { toast('Timeline is empty — add clips first.'); return; }
    // WHERE to cut: during playback, cut at the STOCK only if the user actually clicked
    // AHEAD (autoScrollSuspended) — otherwise cut at the LIVE moving playhead so rapid
    // live cuts land at successive positions, never stuck at one fixed spot (which
    // becomes a boundary after the first cut -> "too close to a clip edge" on every
    // one after). Paused, the playhead.
    var cutPos = (state.playing && state.autoScrollSuspended && typeof state.pauseReturnComp === 'number')
               ? state.pauseReturnComp
               : (typeof state.playheadComp === 'number' ? state.playheadComp : null);
    if (cutPos == null) { toast('Play or scrub to a point on the timeline first.'); return; }

    var clip = null;
    for (var i = 0; i < clips.length; i++) {
      var c = clips[i], s = c.start_sec || 0, d = clipDur(c);
      if (cutPos >= s && cutPos < s + d) { clip = c; break; }
    }
    if (!clip) { toast('No clip under the ' + (state.playing ? 'stock' : 'playhead') + '.'); return; }

    var srcSplit = (clip.in || 0) + (cutPos - (clip.start_sec || 0));
    // A split preserves the compilation timeline (same order + total duration), so the
    // LIVE playhead stays valid. While playing, resume there — pass undefined so
    // applyTimeline uses the CURRENT (post-await) live playhead — and the cut never
    // interrupts playback (item 4). Paused, applyTimeline doesn't re-seek and the marker
    // stays on the new cut edge.
    var resumeAt = state.playing ? undefined : cutPos;

    splitting = true;
    try {
      // ONE atomic engine edit: the whole split is a single undoable step, so Ctrl+Z
      // reverses it at once (it used to be set_trim + add_clip + reorder — three
      // separate undo steps, the item-8 bug). The engine rejects a cut too near an edge.
      var rep = await beckyCall('split', { id: clip.id, at: srcSplit });
      if (!rep.ok || !rep.data || !rep.data.timeline) {
        toast(rep.error ? ('Split: ' + rep.error) : 'Too close to a clip edge to split.');
        return;
      }
      applyTimeline(rep.data.timeline, resumeAt);
      // The new right half (the clip AFTER the cut) becomes the selection — what a
      // producer expects next. No success toast: the two clips are the confirmation.
      if (rep.data.new_id) { selectOnly(rep.data.new_id); }
    } finally {
      splitting = false;
    }
  }

  /* =======================================================================
     TIMELINE POINTER GESTURES (CHANGE A/B/C)

     ONE pointer model on the track tells three intents apart by WHERE the press
     lands and (for a clip body) by how far it then MOVES:

       .rh handle    -> RESIZE  (trim/extend this clip's OWN source window)
       clip BODY     -> PENDING : a CLICK (moves <= DRAG_PX) seeks + selects;
                                  a DRAG  (moves >  DRAG_PX) reorders the clip
       empty track   -> SCRUB   (free seek, same as the ruler)

     Pointer capture keeps a drag/scrub tracking even after it leaves the element,
     so a click-vs-drag is never lost mid-gesture. Clips keep draggable=false; this
     is all pointer events, so it composes cleanly with scrubbing.
     ===================================================================== */
  var resizing     = null;   // active RESIZE gesture (one clip's own in/out)
  var clipGesture  = null;   // pending CLIP-BODY gesture: click=seek OR drag=reorder
  var justResized  = false;  // suppress the click that fires right after a resize
  var justScrubbed = false;  // suppress the click that fires right after a scrub / drag

  /* ---- source-duration cache (CHANGE C) ----
     Each clip is an INDEPENDENT (in,out) window into its OWN source. Extending the
     right edge may reveal more of that source up to its TRUE duration, and NEVER
     clamps to a neighbouring timeline clip. The duration is looked up lazily via
     the probe verb and cached per source path, so the bound is ready on the next
     drag. Unknown/0 duration -> a generous ceiling, never a neighbour. */
  var sourceDur = new Map();    // source path -> duration (sec); 0 = unknown / not probe-able
  var sourceDurPending = {};    // source path -> true while a probe is in flight
  function knownSourceDuration(source) {
    var d = source ? sourceDur.get(source) : 0;
    return (typeof d === 'number' && d > 0) ? d : 0;
  }
  async function ensureSourceDuration(source) {
    if (!source || sourceDur.has(source) || sourceDurPending[source]) { return; }
    sourceDurPending[source] = true;
    var rep = await beckyCall('probe', { source: source });
    delete sourceDurPending[source];
    var d = (rep && rep.ok && rep.data && typeof rep.data.duration === 'number') ? rep.data.duration : 0;
    sourceDur.set(source, d > 0 ? d : 0);
  }
  function prefetchSourceDurations() {
    var clips = state.timeline.clips || [];
    for (var i = 0; i < clips.length; i++) { ensureSourceDuration(clips[i].source); }
  }
  // The most a clip's OUT may grow to: its source's true duration if known, else a
  // generous ceiling. CRITICAL: this depends only on the clip's OWN source.
  function maxOutFor(clip) {
    var d = knownSourceDuration(clip.source);
    return d > 0 ? d : (clip.out || 0) + 3600;
  }

  /* ---- RESIZE: trim/extend a clip's OWN source window (CHANGE C) ---- */
  function startResize(handle, e) {
    e.preventDefault();          // also stops any native HTML5 drag from starting
    e.stopPropagation();         // a handle never starts a scrub or a reorder
    var block = handle.closest('.clip');
    var clip = block && clipById(block.dataset.id);
    if (!clip) { return; }
    ensureSourceDuration(clip.source);   // warm THIS source's bound for the drag
    var w = block.offsetWidth;
    var dur = Math.max(0.001, clipDur(clip));
    // The waveform SVG covers [waveCovIn,waveCovOut] over viewBox 0..waveN; during the
    // drag we crop that COVERAGE to the new window (moveResize) instead of stretching it.
    var cwave = block.querySelector('.cwave');
    var waveSvg = cwave ? cwave.querySelector('svg') : null;
    resizing = {
      id: clip.id, edge: handle.dataset.edge, startX: e.clientX,
      block: block, clip: clip, pxPerSec: w / dur,
      origIn: clip.in || 0, origOut: clip.out || 0,
      newIn: clip.in || 0, newOut: clip.out || 0,
      waveSvg: waveSvg,
      waveCovIn: cwave ? parseFloat(cwave.dataset.waveCovIn) : NaN,
      waveCovOut: cwave ? parseFloat(cwave.dataset.waveCovOut) : NaN,
      waveN: cwave ? parseFloat(cwave.dataset.waveN) : 0
    };
    try { handle.setPointerCapture(e.pointerId); } catch (_) {}
  }
  function moveResize(e) {
    var r = resizing;
    var dSec = (e.clientX - r.startX) / r.pxPerSec;   // px -> sec via this block's own scale
    var nIn = r.origIn, nOut = r.origOut;
    if (r.edge === 'l') {
      // LEFT edge moves IN, bounded by THIS clip only: 0 <= in <= out - MIN_CLIP.
      nIn = Math.max(0, Math.min(r.origIn + dSec, r.origOut - MIN_CLIP));
    } else {
      // RIGHT edge moves OUT, bounded by THIS source's duration (NOT the next clip):
      // in + MIN_CLIP <= out <= sourceDuration.
      nOut = Math.max(r.origIn + MIN_CLIP, Math.min(r.origOut + dSec, maxOutFor(r.clip)));
    }
    r.newIn = nIn; r.newOut = nOut;
    var newDur = nOut - nIn;
    r.block.style.width = Math.max(minClipW(), newDur * r.pxPerSec) + 'px';   // live optimistic width
    // Crop the waveform to the visible [nIn,nOut] window (a viewBox sub-range) so it
    // reveals/hides at a CONSTANT scale — a real trim, not a time-stretch — and you can
    // land on a zero-crossing. renderTimeline rebuilds it full-width on release.
    if (r.waveSvg && r.waveN > 0 && r.waveCovOut > r.waveCovIn) {
      var od = r.waveCovOut - r.waveCovIn;
      var a = Math.max(r.waveCovIn, nIn), z = Math.min(r.waveCovOut, nOut);
      if (z > a) {
        var minX = (a - r.waveCovIn) / od * r.waveN;
        var wX = Math.max(0.01, (z - a) / od * r.waveN);
        r.waveSvg.setAttribute('viewBox', minX.toFixed(2) + ' 0 ' + wX.toFixed(2) + ' 1');
      }
    }
    r.block.title = (r.block.title || '').replace(/\s*\([^)]*\)\s*$/, '') + '  (' + mmss(newDur) + ')';
    updatePlayhead();
  }
  async function endResize() {
    if (!resizing) { return; }
    var r = resizing; resizing = null;
    justResized = true;
    setTimeout(function () { justResized = false; }, 350);
    var changed = Math.abs(r.newIn - r.origIn) > 0.001 || Math.abs(r.newOut - r.origOut) > 0.001;
    if (!changed) {
      // A CLICK on a handle (no drag) SELECTS its clip — so a handle is never just "in the
      // way" when zoomed out; you can still select the clip by clicking its edge. Then
      // re-render (reconcile -> refitWave re-crops the waveform to this clip's window).
      selectOnly(r.id);
      renderTimeline();
      return;
    }
    var rep = await beckyCall('set_trim', { id: r.id, in: r.newIn, out: r.newOut });
    if (rep.ok && rep.data) { applyTimeline(rep.data); } else { renderTimeline(); }
  }

  /* ---- REORDER drop indicator (CHANGE A): an insertion line between clips ---- */
  var dropmarkEl = document.createElement('div');
  dropmarkEl.className = 'dropmark';
  dropmarkEl.style.display = 'none';

  function clipIndexById(id) {
    var clips = state.timeline.clips || [];
    for (var i = 0; i < clips.length; i++) { if (String(clips[i].id) === String(id)) { return i; } }
    return -1;
  }
  function eventTrackX(e) { return e.clientX - trackEl.getBoundingClientRect().left; }
  // The reorder destination = how many OTHER clips sit left of the cursor centre.
  // That is exactly the engine's stable remove-then-insert index (App.Reorder).
  // excl is an ARRAY of clip ids to skip (the clip(s) being dragged). For a single drag
  // it's [id]; for a multi-selection drag (item 10) it's the whole group.
  function excludes(excl, id) {
    for (var i = 0; i < excl.length; i++) { if (String(excl[i]) === String(id)) { return true; } }
    return false;
  }
  function dropInsertIndex(excl, x) {
    var blocks = trackEl.querySelectorAll('.clip');
    var insert = 0;
    for (var i = 0; i < blocks.length; i++) {
      if (excludes(excl, blocks[i].dataset.id)) { continue; }
      if (x > blocks[i].offsetLeft + blocks[i].offsetWidth / 2) { insert++; }
    }
    return insert;
  }
  function positionDropmark(excl, x) {
    var others = [], all = trackEl.querySelectorAll('.clip');
    for (var i = 0; i < all.length; i++) { if (!excludes(excl, all[i].dataset.id)) { others.push(all[i]); } }
    var insert = dropInsertIndex(excl, x), leftPx;
    if (!others.length) { leftPx = 0; }
    else if (insert <= 0) { leftPx = others[0].offsetLeft - 2; }
    else if (insert >= others.length) {
      var last = others[others.length - 1];
      leftPx = last.offsetLeft + last.offsetWidth + 1;
    } else {
      var a = others[insert - 1], b = others[insert];
      leftPx = (a.offsetLeft + a.offsetWidth + b.offsetLeft) / 2 - 1;
    }
    dropmarkEl.style.left = Math.max(0, leftPx) + 'px';
    dropmarkEl.style.display = 'block';
  }

  /* ---- CLIP-BODY gesture: PENDING -> click (seek) OR drag (reorder) (CHANGE A) ---- */
  function startClipGesture(block, e) {
    e.preventDefault();                  // keep the drag clean (no text selection)
    var id = block.dataset.id;
    // If the pressed clip is part of a multi-selection, the WHOLE selection drags
    // together as one block (item 10). Captured on press — selection itself is set on
    // release / Ctrl / Shift, so this reads the state from BEFORE this gesture.
    var group = (isClipSelected(id) && (state.selectedClipIds || []).length > 1)
              ? (state.selectedClipIds || []).slice() : null;
    clipGesture = {
      id: id, block: block, startX: e.clientX, dragging: false,
      ctrl: e.ctrlKey || e.metaKey,      // Ctrl/Cmd+click = toggle in multi-selection
      shift: e.shiftKey,                 // Shift+click = select range from the anchor
      group: group                       // non-null => a group drag moves all of these
    };
    try { trackEl.setPointerCapture(e.pointerId); } catch (_) {}
  }
  function moveClipGesture(e) {
    var g = clipGesture;
    if (!g.dragging) {
      if (Math.abs(e.clientX - g.startX) <= DRAG_PX) { return; }   // below threshold -> still a click
      g.dragging = true;                                           // crossed it -> become a reorder drag
      var grp = g.group || [g.id];
      for (var i = 0; i < grp.length; i++) { var b = blockById(grp[i]); if (b) { b.classList.add('dragging'); } }
      trackEl.appendChild(dropmarkEl);
    }
    positionDropmark(g.group || [g.id], eventTrackX(e));
  }
  async function endClipGesture(e) {
    if (!clipGesture) { return; }
    var g = clipGesture; clipGesture = null;
    var grp = g.group || [g.id];
    for (var gi = 0; gi < grp.length; gi++) { var gb = blockById(grp[gi]); if (gb) { gb.classList.remove('dragging'); } }
    if (dropmarkEl.parentNode) { dropmarkEl.parentNode.removeChild(dropmarkEl); }
    dropmarkEl.style.display = 'none';
    justScrubbed = true;                              // eat the trailing click in BOTH cases
    setTimeout(function () { justScrubbed = false; }, 350);

    if (g.dragging) {
      // a DRAG happened -> reorder. `to` is the drop index among the clips NOT being moved.
      var to = dropInsertIndex(grp, eventTrackX(e));
      if (g.group && g.group.length > 1) {
        // Move the whole multi-selection as ONE block (item 10) — one undoable edit.
        var repM = await beckyCall('reorder_many', { ids: g.group, to: to });
        if (repM.ok && repM.data) { applyTimeline(repM.data); }
        else { renderTimeline(); toast('Could not move clips' + (repM.error ? ': ' + repM.error : '')); }
      } else {
        var from = clipIndexById(g.id);
        if (from >= 0 && to !== from) {               // only reorder when the target index truly differs
          var rep = await beckyCall('reorder', { id: g.id, to: to });
          if (rep.ok && rep.data) { applyTimeline(rep.data); }
          else { renderTimeline(); toast('Could not reorder' + (rep.error ? ': ' + rep.error : '')); }
        } else {
          renderTimeline();                           // no change -> just clear the drag visuals
        }
      }
    } else if (g.ctrl) {
      // Ctrl/Cmd+click: toggle this clip in the multi-selection; don't move the playhead.
      toggleInSelection(g.id);
    } else if (g.shift) {
      // Shift+click: select every clip from the anchor to here. When PAUSED, also move
      // the playhead to the clicked clip so the preview follows; while PLAYING, only
      // extend the selection — seekClipById(...,false) would pause, which it must not
      // do mid-playback (that was the "shift-click pauses playback" bug).
      selectRange(g.id);
      if (!state.playing) { seekClipById(g.id, false); }
    } else {
      // a plain CLICK (moved <= DRAG_PX) -> select ONLY this clip + seek to the exact spot.
      scrubTo(e, true);
    }
  }

  // seekClipById moves the playhead to a clip's start (PAUSED unless play=true).
  function seekClipById(id, play) {
    var c = clipById(id);
    if (!c) { return; }
    var comp = c.start_sec || 0;
    state.activeClipId = c.id;
    state.playheadComp = comp;
    seekTimeline(comp, !!play);
    updatePlayhead();
  }

  // seekClipEdge (Ctrl+Left/Right) jumps to the PREVIOUS/NEXT edit point across the
  // WHOLE timeline — every clip's start plus the very end of the last clip — not just
  // the current clip's own start/end (that got stuck re-landing on itself every time).
  function seekClipEdge(toEnd) {
    var clips = state.timeline.clips || [];
    if (!clips.length) { return; }
    var points = clips.map(function (c) { return c.start_sec || 0; });
    var last = clips[clips.length - 1];
    points.push((last.start_sec || 0) + clipDur(last));   // the compilation's very end
    points.sort(function (a, b) { return a - b; });
    var comp = state.playheadComp || 0;
    // SNAP the playhead onto the edit point it is sitting on (within EDGE_SNAP), then
    // step to the neighbour. The old "previous point strictly < comp - 0.01" got STUCK
    // whenever a seek landed a hair PAST the boundary (mpv's seek jitter): "< comp"
    // then re-found the SAME clip's own start. A fixed one-sided epsilon can't win —
    // an UNDERshoot would break the forward jump the same way. Snapping to the nearest
    // point absorbs jitter in BOTH directions. EDGE_SNAP (0.12) < MIN_CLIP/2, so a
    // position is ever within snap of at most ONE edit point (never the wrong one).
    var EDGE_SNAP = 0.12;
    var nearIdx = 0, nearDist = Infinity;
    for (var i = 0; i < points.length; i++) {
      var dd = Math.abs(points[i] - comp);
      if (dd < nearDist) { nearDist = dd; nearIdx = i; }
    }
    var target;
    if (nearDist <= EDGE_SNAP) {
      // sitting ON an edit point -> step to the adjacent one (clamped at the ends)
      target = toEnd ? points[Math.min(nearIdx + 1, points.length - 1)]
                     : points[Math.max(nearIdx - 1, 0)];
    } else if (toEnd) {
      target = points[points.length - 1];                 // between points -> next one up
      for (var j = 0; j < points.length; j++) { if (points[j] > comp) { target = points[j]; break; } }
    } else {
      target = points[0];                                 // between points -> next one down
      for (var k = points.length - 1; k >= 0; k--) { if (points[k] < comp) { target = points[k]; break; } }
    }
    state.activeClipId = (clipAtComp(target) || clips[0]).id;
    state.playheadComp = target;
    seekTimeline(target, false);
    updatePlayhead();
  }

  /* ---- keyboard navigation of the left-panel list (Up/Down + Enter) ----
     kbIndex is the keyboard cursor over the CURRENTLY rendered rows (files OR quote
     rows). It resets on every list re-render (renderFind) so it never points at a
     stale row. The panel must be focused (clicking a row focuses #listScroll). */
  var kbIndex = -1;
  function listRows() { return $listScroll.querySelectorAll('.file, .qrow'); }
  function listIsFocused() { return document.activeElement === $listScroll; }
  // The row already highlighted (a clicked quote is .active; the keyboard cursor is
  // .kbsel) so Up/Down can resume FROM the current selection instead of the top.
  function selectedRowIndex() {
    var rows = listRows();
    for (var i = 0; i < rows.length; i++) {
      if (rows[i].classList.contains('kbsel') || rows[i].classList.contains('active')) { return i; }
    }
    return -1;
  }
  function paintListSel() {
    var rows = listRows();
    for (var i = 0; i < rows.length; i++) { rows[i].classList.toggle('kbsel', i === kbIndex); }
    if (kbIndex >= 0 && rows[kbIndex]) { rows[kbIndex].scrollIntoView({ block: 'nearest' }); }
  }
  function moveListSelection(delta) {
    var rows = listRows();
    if (!rows.length) { return; }
    if (kbIndex < 0) { kbIndex = selectedRowIndex(); }   // resume from the selection, not the top
    kbIndex = (kbIndex < 0) ? (delta > 0 ? 0 : rows.length - 1)
                            : Math.max(0, Math.min(kbIndex + delta, rows.length - 1));
    // The keyboard cursor now owns the highlight — clear any leftover mouse-selected
    // row so the two never show independently (ONE selection at a time in the panel).
    if (state.activeResultKey != null) { state.activeResultKey = null; markActiveRow(); }
    paintListSel();
  }
  function activateListSelection() {
    var rows = listRows();
    var row = (kbIndex >= 0) ? rows[kbIndex] : null;
    if (!row) { return; }
    // Enter mirrors a double-click: a video opens its transcript, a quote is added to
    // the timeline (not merely previewed like a single click).
    if (row.classList.contains('file')) { onFileClick(row.dataset.name); }
    else { onRowDbl(+row.dataset.idx); }
  }
  // Clicking anywhere in the list (except an input/button) focuses the panel so the
  // Up/Down keys take over immediately.
  $listScroll.addEventListener('pointerdown', function (e) {
    if (!e.target.closest('input, button, [data-sort]')) { $listScroll.focus(); }
  });

  /* ---- the unified track pointer handlers ---- */
  trackEl.addEventListener('pointerdown', function (e) {
    if (e.button !== undefined && e.button !== 0) { return; }   // left button only
    blurChatField();   // selecting on the timeline returns keyboard focus from the Q&A answer box
    var h = e.target.closest('.rh');
    if (h) { startResize(h, e); return; }                       // 1) trim handle -> RESIZE
    if (e.target.closest('[data-act="remove"]')) { return; }    // 2) remove "x" -> the click handler
    if (!(state.timeline.clips || []).length) { return; }
    var block = e.target.closest('.clip');
    if (block) { startClipGesture(block, e); return; }          // 3) clip body -> click | drag
    // 4) empty track space (gaps / the ends) -> free scrub, exactly like the ruler.
    e.preventDefault();
    scrubbing = true;
    try { trackEl.setPointerCapture(e.pointerId); } catch (_) {}
    scrubTo(e, true, true);
  });

  trackEl.addEventListener('pointermove', function (e) {
    if (resizing)    { moveResize(e);      return; }
    if (clipGesture) { moveClipGesture(e); return; }
    if (scrubbing)   { scrubTo(e, false); }   // free scrub anywhere on the track
  });

  function endScrub() {
    if (!scrubbing) { return; }
    scrubbing = false;
    justScrubbed = true;                         // eat the click that follows a scrub gesture
    setTimeout(function () { justScrubbed = false; }, 350);
  }
  trackEl.addEventListener('pointerup', function (e) { endResize(); endClipGesture(e); endScrub(); });
  trackEl.addEventListener('pointercancel', function (e) { endResize(); endClipGesture(e); endScrub(); });

  /* ---- clip clicks: only the hover remove "x" (seek/drag are on pointer events) ---- */
  trackEl.addEventListener('click', function (e) {
    if (justResized || justScrubbed) { return; } // a resize / scrub / drag just happened - eat this click
    var remove = e.target.closest('[data-act="remove"]');
    if (remove) { e.stopPropagation(); onClipRemove(remove.closest('.clip').dataset.id); }
  });

  /* ---- seek mapping (shared by the ruler AND the track) ----
     CHANGE B: snap a seek to a clip edge ONLY within SNAP_PX of it; everywhere else
     inside a clip it seeks to the EXACT clicked position (no whole-clip snapping). */
  var scrubbing = false;
  function findClipAtX(x) {
    var blocks = trackEl.querySelectorAll('.clip');
    // 1) inside a clip: exact frac, snapping to an edge ONLY within SNAP_PX of it.
    for (var i = 0; i < blocks.length; i++) {
      var b = blocks[i], l = b.offsetLeft, w = b.offsetWidth, r = l + w;
      if (x >= l && x <= r) {
        var frac;
        if (x - l <= SNAP_PX) { frac = 0; }             // within 8px of IN  -> snap to in
        else if (r - x <= SNAP_PX) { frac = 1; }         // within 8px of OUT -> snap to out
        else { frac = (x - l) / Math.max(1, w); }        // otherwise the exact clicked position
        return { id: b.dataset.id, frac: frac };
      }
    }
    // 2) outside every clip (a gap or the empty ends): seek to the NEAREST edge.
    var best = null, bestDist = Infinity;
    for (var j = 0; j < blocks.length; j++) {
      var bb = blocks[j], ll = bb.offsetLeft, rr = ll + bb.offsetWidth;
      if (Math.abs(ll - x) < bestDist) { bestDist = Math.abs(ll - x); best = { id: bb.dataset.id, frac: 0 }; }
      if (Math.abs(rr - x) < bestDist) { bestDist = Math.abs(rr - x); best = { id: bb.dataset.id, frac: 1 }; }
    }
    return best;
  }
  function scrubTo(e, isStart, isRuler) {
    var rect = trackEl.getBoundingClientRect();   // ruler shares the track's left edge + width
    var x = e.clientX - rect.left;
    var hit = findClipAtX(x);
    if (!hit) { return; }
    var clip = clipById(hit.id);
    if (!clip) { return; }
    var comp = (clip.start_sec || 0) + hit.frac * clipDur(clip);   // the compilation position clicked

    if (state.playing && !isRuler) {
      // A clip-body click WHILE PLAYING moves the SECONDARY STOCK here — where Pause
      // snaps back to, and where cut/split act (items 3/4) — and SELECTS this clip so it
      // can be deleted WITHOUT interrupting playback (item 3). The live preview is left
      // untouched: it keeps running from wherever it already was. The stock blinks to
      // show it moved (item 2, via updateStock while autoScrollSuspended).
      state.pauseReturnComp = comp;
      state.autoScrollSuspended = true;
      selectOnly(clip.id);
      updateStock();
      return;
    }

    // Move the playhead (+ the coincident stock) to the click. A RULER click does this
    // WITHOUT changing the selected clip (item 5); a clip-body click also selects it.
    if (!isRuler) { selectOnly(clip.id); }
    state.activeClipId = clip.id;
    state.playheadComp = comp;
    state.pauseReturnComp = comp;
    state.autoScrollSuspended = false;    // a real navigation — resume following it
    // Navigate the SEAMLESS timeline (the mpv EDL): seek to comp, KEEPING whatever play
    // state was already true — clicking elsewhere while playing keeps playing from the
    // new spot; clicking while paused stays paused. (isStart kept for the signature.)
    seekTimeline(comp, state.playing);
    updatePlayhead();
  }
  // Ruler gesture (the gray bar above the clips): a CLICK places the playhead; a DRAG
  // pans the timeline sideways. Click-vs-drag is decided by movement, like the clip
  // body — below DRAG_PX it's a click (seek on pointerup), beyond it pans scrollLeft.
  var rulerPan = null;   // { startX, startScroll, panned }
  rulerEl.addEventListener('pointerdown', function (e) {
    if (e.button !== undefined && e.button !== 0) { return; }
    if (!(state.timeline.clips || []).length) { return; }
    blurChatField();   // scrubbing the ruler returns keyboard focus from the Q&A answer box
    e.preventDefault();
    rulerPan = { startX: e.clientX, startScroll: tlBodyEl ? tlBodyEl.scrollLeft : 0, panned: false };
    try { rulerEl.setPointerCapture(e.pointerId); } catch (_) {}
  });
  rulerEl.addEventListener('pointermove', function (e) {
    if (!rulerPan) { return; }
    var dx = e.clientX - rulerPan.startX;
    if (!rulerPan.panned && Math.abs(dx) <= DRAG_PX) { return; }   // still within click slop
    rulerPan.panned = true;
    rulerEl.classList.add('panning');
    if (tlBodyEl) { tlBodyEl.scrollLeft = rulerPan.startScroll - dx; }   // grab-and-drag pan
  });
  function endRulerGesture(e) {
    if (!rulerPan) { return; }
    var g = rulerPan; rulerPan = null;
    rulerEl.classList.remove('panning');
    if (!g.panned) { scrubTo(e, true, true); }   // a click (no real drag) -> move the playhead here
  }
  rulerEl.addEventListener('pointerup', endRulerGesture);
  rulerEl.addEventListener('pointercancel', function () { rulerPan = null; rulerEl.classList.remove('panning'); });

  /* ---- transport + reel actions ---- */
  $tPlay.addEventListener('click', function () { togglePlay(); });
  // (previous/next-frame BUTTONS removed — the Left/Right arrow shortcuts still frame-step.)
  if ($tSplit) { $tSplit.addEventListener('click', function () { splitAtPlayhead(); }); }  // CHANGE 4
  if ($tScreenshot) { $tScreenshot.addEventListener('click', function () { mpvSend('screenshot'); }); }

  /* ---- playback speed: 1× / 2× (button click + Shift+Space) ---- */
  var SPEEDS = [1, 1.5, 2];
  var playSpeed = 1;
  function setSpeed(v) {
    playSpeed = (SPEEDS.indexOf(v) >= 0) ? v : 1;
    mpvSend('speed', { value: playSpeed });   // mpv keeps pitch-corrected audio at >1×
    if ($tSpeed) {
      $tSpeed.textContent = playSpeed + '×';
      $tSpeed.classList.toggle('on', playSpeed !== 1);
    }
  }
  // cycleSpeed steps 1× -> 1.5× -> 2× -> 1× (the button + Shift+Space). It never
  // changes play/pause — only the speed.
  function cycleSpeed() {
    var i = SPEEDS.indexOf(playSpeed);
    setSpeed(SPEEDS[(i + 1) % SPEEDS.length]);
  }
  if ($tSpeed) { $tSpeed.addEventListener('click', cycleSpeed); }

  /* ---- undo / redo (engine-side history; Ctrl+Z / Ctrl+Shift+Z) ---- */
  async function undoTimeline() {
    var rep = await beckyCall('undo', {});
    if (rep.ok && rep.data) { applyTimeline(rep.data.timeline); }
  }
  async function redoTimeline() {
    var rep = await beckyCall('redo', {});
    if (rep.ok && rep.data) { applyTimeline(rep.data.timeline); }
  }
  if ($tUndo) { $tUndo.addEventListener('click', undoTimeline); }
  if ($tRedo) { $tRedo.addEventListener('click', redoTimeline); }

  /* ---- extend the SELECTED clip by one frame (left = earlier IN, right = later OUT) ----
     Reuses set_trim; one frame = 1/fps of the clip's own source. The right edge is
     capped at the source's true duration when known (never a neighbour). */
  function primarySelectedId() {
    if (state.selectedClipId != null) { return state.selectedClipId; }
    if ((state.selectedClipIds || []).length === 1) { return state.selectedClipIds[0]; }
    return null;
  }
  async function extendSelected(dir) {
    var id = primarySelectedId();
    var clip = id != null ? clipById(id) : null;
    if (!clip) { toast('Select a clip first.'); return; }
    ensureSourceDuration(clip.source);   // warm the cap for next time
    var fps = (clip.source_fps && clip.source_fps > 0) ? clip.source_fps : 30;
    var frame = 1 / fps;
    var nin = clip.in || 0, nout = clip.out || 0;
    if (dir < 0) {
      nin = Math.max(0, nin - frame);                       // grow the LEFT edge earlier
      if (nin >= nout - MIN_CLIP) { toast('Clip is already at its source start.'); return; }
    } else {
      nout = nout + frame;                                  // grow the RIGHT edge later
      var cap = knownSourceDuration(clip.source);
      if (cap > 0 && nout > cap) { toast('Clip is already at its source end.'); return; }
    }
    var rep = await beckyCall('set_trim', { id: id, in: nin, out: nout });
    if (rep.ok && rep.data) { applyTimeline(rep.data); }
    else { toast('Could not extend clip' + (rep.error ? ': ' + rep.error : '')); }
  }
  if ($tExtendL) { $tExtendL.addEventListener('click', function () { extendSelected(-1); }); }
  if ($tExtendR) { $tExtendR.addEventListener('click', function () { extendSelected(1); }); }

  /* ---- render ONLY the selected clips ---- */
  // Single click renders exactly as before. A double click ALSO reveals the
  // rendered file in Explorer once it's done — tracked via a flag (not a
  // click-delay guard) so the single-click path never gains latency, and a
  // renderingSel guard so the double-click's 2nd click event can't launch a
  // second concurrent render.
  var renderingSel = false, revealAfterRenderSel = false;
  if ($tRenderSel) {
    $tRenderSel.addEventListener('dblclick', function () { revealAfterRenderSel = true; });
    $tRenderSel.addEventListener('click', async function () {
      if (renderingSel) { return; }
      var ids = (state.selectedClipIds || []).slice();
      if (!ids.length) { toast('Select one or more clips first.'); return; }
      renderingSel = true;
      toast('Rendering selection…');
      try {
        var rep = await beckyCall('export_selection', { ids: ids });
        if (!rep.ok) { addBeckyMsg('Render selection failed' + (rep.error ? ': ' + rep.error : '')); toast('Render failed'); return; }
        var r = rep.data || {};
        var parts = [];
        if (r.mp4) { parts.push('MP4: ' + r.mp4); }
        if (r.duration_sec != null) { parts.push('Duration: ' + hms(r.duration_sec)); }
        if (r.clips != null) { parts.push('Clips: ' + r.clips); }
        if (r.output_mb != null) { parts.push('Size: ' + r.output_mb + ' MB'); }
        if (typeof r.audio_ok === 'boolean') { parts.push('Audio: ' + (r.audio_ok ? 'ok' : 'MISSING')); }
        if (r.note) { parts.push(r.note); }
        addBeckyMsg('Rendered ' + ids.length + ' selected clip' + (ids.length === 1 ? '' : 's') + '.\n' + parts.join('\n'));
        toast('Selection rendered');
        // Read the reveal flag AFTER the render finishes, not before: a real
        // double-click delivers click, click, THEN dblclick (DOM spec order), so
        // dblclick only sets the flag well after this handler already started —
        // reading it up front would always see the stale pre-dblclick value.
        if (revealAfterRenderSel && r.mp4) { revealFile(r.mp4); }
      } finally {
        renderingSel = false;
        revealAfterRenderSel = false;
      }
    });
  }

  /* ---- timeline zoom: buttons + mousewheel over the timeline ---- */
  if ($tZoomIn)  { $tZoomIn.addEventListener('click',  function () { zoomBy(1.5); }); }
  if ($tZoomOut) { $tZoomOut.addEventListener('click', function () { zoomBy(1 / 1.5); }); }
  if (tlBodyEl) {
    // PLAIN wheel ZOOMS the timeline toward the cursor; Ctrl/Cmd + wheel pans it
    // sideways. This handler is scoped to the timeline body only, so scrolling
    // anywhere else in the app is untouched.
    tlBodyEl.addEventListener('wheel', function (e) {
      if (e.ctrlKey || e.metaKey) {
        var d = (Math.abs(e.deltaY) >= Math.abs(e.deltaX)) ? e.deltaY : e.deltaX;
        if (d) { e.preventDefault(); tlBodyEl.scrollLeft += d; }   // Ctrl+wheel = horizontal pan
      } else {
        e.preventDefault();
        // plain wheel = zoom to the PLAYHEAD (Jordan's ask), falling back to the
        // cursor position when there's no playhead to anchor on yet (empty timeline).
        var anchorX = playheadClientX();
        zoomAt(e.deltaY < 0 ? 1.15 : 1 / 1.15, anchorX != null ? anchorX : e.clientX);
      }
    }, { passive: false });

    // Middle-mouse (scroll-wheel) button + drag = pan the timeline sideways, WITHOUT
    // touching clips (the clip gestures are left-button only). preventDefault stops the
    // browser's middle-click autoscroll.
    var midPan = null;
    tlBodyEl.addEventListener('pointerdown', function (e) {
      if (e.button !== 1) { return; }
      e.preventDefault();
      midPan = { x: e.clientX, scroll: tlBodyEl.scrollLeft };
      try { tlBodyEl.setPointerCapture(e.pointerId); } catch (_) {}
      tlBodyEl.style.cursor = 'grabbing';
    });
    tlBodyEl.addEventListener('pointermove', function (e) {
      if (midPan) { tlBodyEl.scrollLeft = midPan.scroll - (e.clientX - midPan.x); }
    });
    function endMidPan() { if (midPan) { midPan = null; tlBodyEl.style.cursor = ''; } }
    tlBodyEl.addEventListener('pointerup', endMidPan);
    tlBodyEl.addEventListener('pointercancel', endMidPan);
  }

  $tOverlay.addEventListener('click', async function () {
    // Cycle the 3 states (item 14): render-on/preview-off -> render-on/preview-on ->
    // off -> (back to) render-on/preview-off.
    if (state.overlayRender && !state.overlayPreview) {
      state.overlayPreview = true;                                   // a -> b: also show it here
    } else if (state.overlayRender && state.overlayPreview) {
      state.overlayRender = false; state.overlayPreview = false;     // b -> c: off entirely
    } else {
      state.overlayRender = true; state.overlayPreview = false;      // c -> a: back to render-only
    }
    updateOverlayBtn();
    // Persist ONLY the render flag (the reel's overlay.enabled). Preview is a UI-only
    // display toggle, never written to the reel. Don't re-applyTimeline (it would
    // needlessly invalidate the loaded EDL); the flag is persisted server-side.
    await beckyCall('set_overlay', { field: 'enabled', value: state.overlayRender });
    sendOverlayUpdate();   // push the CURRENT clip's name + source TC + preview on/off to mpv
  });

  // Toggle the OPTIONAL filename line (Date / ORIG TC / link are always shown). Persists
  // as the reel's ShowFilename so the RENDER honours it too, then refreshes the preview.
  if ($tOverlayName) {
    $tOverlayName.addEventListener('click', async function () {
      state.overlayShowName = !state.overlayShowName;
      updateOverlayBtn();
      await beckyCall('set_overlay', { field: 'filename', value: state.overlayShowName });
      sendOverlayUpdate();
    });
  }

  // Save/Load use a NATIVE file dialog from the host (save_dialog/load_dialog are
  // intercepted in MainWindow before the engine). The old window.prompt() froze the
  // UI: its modal rendered BEHIND the always-on-top native mpv surface, so the page's
  // JS blocked on a dialog the user could never see or dismiss. A real OS dialog also
  // means Jordan never has to type a full path.
  $tSave.addEventListener('click', async function () {
    if (!state.timeline.clips || !state.timeline.clips.length) { toast('Timeline is empty — nothing to save.'); return; }
    var dlg = await beckyCall('save_dialog', { default: state.folder ? (state.folder + '\\reel.reel.json') : 'reel.reel.json' });
    if (!dlg.ok || !dlg.data || !dlg.data.path) { return; }   // cancelled
    var rep = await beckyCall('save_reel', { path: dlg.data.path });
    if (rep.ok) { toast('Saved ' + ((rep.data && rep.data.path) || dlg.data.path)); }
    else { toast('Save failed' + (rep.error ? ': ' + rep.error : '')); }
  });

  $tLoad.addEventListener('click', async function () {
    var dlg = await beckyCall('load_dialog', { default: state.folder || '' });
    if (!dlg.ok || !dlg.data || !dlg.data.path) { return; }   // cancelled
    var rep = await beckyCall('load_reel', { path: dlg.data.path });
    if (rep.ok && rep.data) { resetSourceColors(); applyTimeline(rep.data); toast('Loaded reel'); }
    else { toast('Load failed' + (rep.error ? ': ' + rep.error : '')); }
  });

  // Same single-click-unchanged / double-click-also-reveals pattern as render selection.
  var exporting = false, revealAfterExport = false;
  $tExport.addEventListener('dblclick', function () { revealAfterExport = true; });
  $tExport.addEventListener('click', async function () {
    if (exporting) { return; }
    if (!state.timeline.clips || !state.timeline.clips.length) { toast('Timeline is empty — add clips first.'); return; }
    exporting = true;
    toast('Exporting…');
    try {
      var rep = await beckyCall('export', {});
      if (!rep.ok) { addBeckyMsg('Export failed' + (rep.error ? ': ' + rep.error : '')); toast('Export failed'); return; }
      var r = rep.data || {};
      var parts = [];
      if (r.mp4) { parts.push('MP4: ' + r.mp4); }
      if (r.duration_sec != null) { parts.push('Duration: ' + hms(r.duration_sec)); }
      if (r.clips != null) { parts.push('Clips: ' + r.clips); }
      if (r.output_mb != null) { parts.push('Size: ' + r.output_mb + ' MB'); }
      if (r.codec) { parts.push('Codec: ' + r.codec); }
      if (r.edl) { parts.push('EDL: ' + r.edl); }
      if (r.srt) { parts.push('SRT: ' + r.srt); }
      if (typeof r.audio_ok === 'boolean') { parts.push('Audio: ' + (r.audio_ok ? 'ok' : 'MISSING')); }
      if (r.note) { parts.push(r.note); }
      addBeckyMsg('Export complete.\n' + parts.join('\n'));
      toast('Export complete');
      // Read the reveal flag AFTER the export finishes — see the matching comment
      // in the render-selection handler above for why (DOM click/click/dblclick order).
      if (revealAfterExport && r.mp4) { revealFile(r.mp4); }
    } finally {
      exporting = false;
      revealAfterExport = false;
    }
  });

  /* =======================================================================
     CONTEXT MENUS (right-click) + focus return
     Clips + left-panel rows get "Open file in File Browser" (host reveal_file) and
     "Copy file name" (host copy_text, the VIDEO filename, not the transcript); a clip
     also gets "Open transcript in left panel" which jumps the list to the clip's time.
     ===================================================================== */
  // Clicking the timeline returns keyboard focus to it: blur the chat/answer field so
  // the timeline shortcuts (space, delete, arrows) work again. Also blurs the left
  // search list — a click is the highest-priority signal of intent, so clicking the
  // timeline must hand it keyboard focus even if a search row still "has" it, or
  // Space/Enter keep controlling the last-clicked quote instead of the timeline
  // (that's what made Play always resume the search-panel preview after adding a
  // quote to the timeline, until the ▶ button was clicked directly).
  // A click on the timeline is the highest-priority signal of intent, so it blurs
  // WHATEVER currently has focus — the search box, the chat box, the search list —
  // not a hardcoded subset. The earlier version only covered the chat field, then
  // the list; the search box was still missed until Jordan hit it directly.
  function blurChatField() {
    var el = document.activeElement;
    if (!el) { return; }
    if (el === $listScroll || el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') { el.blur(); }
  }

  var ctxEl = null;
  function closeContextMenu() {
    if (ctxEl && ctxEl.parentNode) { ctxEl.parentNode.removeChild(ctxEl); }
    ctxEl = null;
  }
  function showContextMenu(x, y, items) {
    closeContextMenu();
    items = (items || []).filter(Boolean);
    if (!items.length) { return; }
    ctxEl = document.createElement('div');
    ctxEl.className = 'ctxmenu';
    items.forEach(function (it) {
      var b = document.createElement('button');
      b.type = 'button';
      b.textContent = it.label;
      b.addEventListener('click', function (ev) { ev.stopPropagation(); closeContextMenu(); try { it.fn(); } catch (_) {} });
      ctxEl.appendChild(b);
    });
    document.body.appendChild(ctxEl);
    // clamp to the viewport so the menu never opens partly off-screen
    var r = ctxEl.getBoundingClientRect();
    ctxEl.style.left = Math.max(4, Math.min(x, window.innerWidth - r.width - 6)) + 'px';
    ctxEl.style.top = Math.max(4, Math.min(y, window.innerHeight - r.height - 6)) + 'px';
  }
  // dismiss on any outside press / scroll / resize / window blur (Escape is handled in
  // the global key handler so it doesn't also fire the delete shortcut).
  document.addEventListener('pointerdown', function (e) {
    if (ctxEl && !ctxEl.contains(e.target)) { closeContextMenu(); }
  }, true);
  window.addEventListener('blur', closeContextMenu);
  window.addEventListener('resize', closeContextMenu);
  if (tlBodyEl) { tlBodyEl.addEventListener('scroll', closeContextMenu); }
  $listScroll.addEventListener('scroll', closeContextMenu);

  // host-backed actions (see MainWindow reveal_file / copy_text)
  function revealFile(path) { if (path) { beckyCall('reveal_file', { path: path }); } }
  function copyFileName(path) {
    var name = baseName(path || '');   // the VIDEO file's name+extension, never the transcript
    if (name) { beckyCall('copy_text', { text: name }).then(function () { toast('Copied: ' + name); }); }
  }
  function videoByName(name) {
    for (var i = 0; i < state.videos.length; i++) { if (state.videos[i].name === name) { return state.videos[i]; } }
    return null;
  }

  // Open a source's transcript in the LEFT panel and land on the cue at `atSec` (the
  // clip's source in-point) — does NOT disturb the video/timeline that's playing.
  async function openTranscriptAtTime(source, atSec) {
    var name = baseName(source || '');
    var v = videoByName(name);
    if (!v) { toast('No indexed transcript for ' + name); return; }
    if (state.mode === 'files') { state.fileScrollTop = $listScroll.scrollTop; }
    var rep = await beckyCall('transcript', { name: v.name });
    var cues = (rep.ok && Array.isArray(rep.data)) ? rep.data : [];
    state.mode = 'results'; state.cueMode = true; state.viewVideoName = v.name;
    state.cueAll = cues; state.cueFilter = ''; state.rows = cues.slice(); state.terms = [];
    // the cue playing at atSec = the LAST cue whose start <= atSec (cues are chronological)
    var bestIdx = state.rows.length ? 0 : -1;
    for (var j = 0; j < state.rows.length; j++) {
      if ((state.rows[j].start || 0) <= (atSec || 0) + 0.001) { bestIdx = j; } else { break; }
    }
    state.activeResultKey = (bestIdx >= 0) ? rowKey(state.rows[bestIdx], bestIdx) : null;
    state.headerText = cueHeaderText();
    renderFind();
    if (state.activeResultKey) {
      var rows = $listScroll.querySelectorAll('.qrow');
      for (var k = 0; k < rows.length; k++) {
        if (rows[k].dataset.key === state.activeResultKey) { rows[k].scrollIntoView({ block: 'center' }); break; }
      }
    }
  }

  // right-click a timeline clip
  trackEl.addEventListener('contextmenu', function (e) {
    var block = e.target.closest('.clip');
    if (!block) { return; }
    var clip = clipById(block.dataset.id);
    if (!clip || !clip.source) { return; }
    e.preventDefault();
    var src = clip.source;
    showContextMenu(e.clientX, e.clientY, [
      { label: 'Open file in File Browser', fn: function () { revealFile(src); } },
      { label: 'Copy file name', fn: function () { copyFileName(src); } },
      { label: 'Open transcript in left panel', fn: function () { openTranscriptAtTime(src, clip.in || 0); } }
    ]);
  });

  // right-click a left-panel video row or quote row
  $listScroll.addEventListener('contextmenu', function (e) {
    var file = e.target.closest('.file');
    if (file) {
      var v = videoByName(file.dataset.name);
      if (!v || !v.path) { return; }
      e.preventDefault();
      showContextMenu(e.clientX, e.clientY, [
        { label: 'Open file in File Browser', fn: function () { revealFile(v.path); } },
        { label: 'Copy file name', fn: function () { copyFileName(v.path); } }
      ]);
      return;
    }
    var row = e.target.closest('.qrow');
    if (row) {
      var r = state.rows[+row.dataset.idx];
      if (!r || !r.source) { return; }   // transcript-only rows have no video file
      e.preventDefault();
      showContextMenu(e.clientX, e.clientY, [
        { label: 'Open file in File Browser', fn: function () { revealFile(r.source); } },
        { label: 'Copy file name', fn: function () { copyFileName(r.source); } }
      ]);
    }
  });

  /* =======================================================================
     GLOBAL KEYS (CHANGE 3/4/5) - guarded so typing in search/ask is untouched (CHANGE 8)
     ===================================================================== */
  function typingInField() {
    var el = document.activeElement;
    if (!el) { return false; }
    var tag = el.tagName;
    return tag === 'INPUT' || tag === 'TEXTAREA' || el.isContentEditable === true;
  }

  document.addEventListener('keydown', async function (e) {
    // A right-click context menu open: Escape closes it (and nothing else — so it never
    // also fires the delete-selected shortcut).
    if (e.key === 'Escape' && ctxEl) { e.preventDefault(); closeContextMenu(); return; }
    if (typingInField()) { return; }   // never hijack keys while the user is typing (Ctrl+Z in a field = native text undo)

    // Undo / Redo: Ctrl+Z, Ctrl+Shift+Z (and Ctrl+Y) for the timeline.
    if ((e.ctrlKey || e.metaKey) && (e.key === 'z' || e.key === 'Z')) {
      e.preventDefault();
      if (e.shiftKey) { redoTimeline(); } else { undoTimeline(); }
      return;
    }
    if ((e.ctrlKey || e.metaKey) && (e.key === 'y' || e.key === 'Y')) {
      e.preventDefault(); redoTimeline(); return;
    }

    // Up / Down = navigate the file/quote list (only when the left panel is focused);
    // Enter activates the highlighted row.
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && listIsFocused()) {
      e.preventDefault();
      moveListSelection(e.key === 'ArrowDown' ? 1 : -1);
      return;
    }
    if (e.key === 'Enter' && listIsFocused() && kbIndex >= 0) {
      e.preventDefault();
      activateListSelection();
      return;
    }
    if (e.key === 'Enter' && !listIsFocused()) {
      e.preventDefault();
      if (state.playing) {
        // Stop where it IS (commit the current position; no snap-back to a bookmark).
        mpvSend('pause');
        state.pauseReturnComp = state.playheadComp;
        state.autoScrollSuspended = false;
      } else {
        // Enter also STARTS playback from the current playhead (like Space).
        state.pauseReturnComp = state.playheadComp;
        state.autoScrollSuspended = false;
        if ((state.timeline.clips || []).length) { seekTimeline(state.playheadComp || 0, true); }
      }
      return;
    }
    // Up/Down elsewhere (the list isn't focused) = zoom the timeline in/out — a
    // clip or the timeline itself is the implied context once the list isn't
    // claiming these keys.
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      zoomBy(e.key === 'ArrowUp' ? 1.5 : 1 / 1.5);
      return;
    }

    // Left / Right = step the playhead one frame; Ctrl+Left / Ctrl+Right = jump to the
    // current clip's start / end.
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      e.preventDefault();
      var fwd = (e.key === 'ArrowRight');
      if (e.ctrlKey || e.metaKey) { seekClipEdge(fwd); }
      else { mpvSend('frame', { dir: fwd ? 1 : -1 }); }
      return;
    }

    // Space = play / pause; Shift+Space = play at 2× (Jordan's 2× shortcut). But if
    // the left panel is focused with a keyboard-selected row, Space plays THAT row
    // (mirrors a single click) instead of toggling the timeline.
    if (e.key === ' ') {
      e.preventDefault();
      if (listIsFocused() && kbIndex >= 0) {
        var selRows = listRows(), selRow = selRows[kbIndex];
        if (selRow) {
          if (selRow.classList.contains('file')) { onFileClick(selRow.dataset.name); }
          else { onRowClick(+selRow.dataset.idx, selRow.dataset.key); }
          return;
        }
      }
      // Shift+Space cycles the playback SPEED only — never starts/stops (even mid-play).
      if (e.shiftKey) { cycleSpeed(); return; }
      togglePlay();
      return;
    }
    // s = split clip at the playhead (CHANGE 4). Ignore Ctrl/Cmd+S so a save chord never splits.
    if ((e.key === 's' || e.key === 'S') && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      splitAtPlayhead();
      return;
    }
    // Delete / Backspace / Escape = ripple-delete ALL selected clips. Jordan wants Esc
    // to double as delete (two hotkeys for the same action).
    if ((e.key === 'Delete' || e.key === 'Backspace' || e.key === 'Escape') && (state.selectedClipIds || []).length) {
      e.preventDefault();
      deleteSelectedClips();
      return;
    }
  });

  /* =======================================================================
     BOOT
     ===================================================================== */
  async function boot() {
    renderChips();
    renderFind();       // empty hint until a folder loads
    renderTimeline();   // empty track

    // Tell the host where the native video pane goes - once now, once after layout settles.
    reportVideoRect();
    setTimeout(reportVideoRect, 200);

    var tl = await beckyCall('timeline', {});         // restore any existing timeline
    if (tl.ok && tl.data) { applyTimeline(tl.data); }
    // Empty project → default the view to ~10s (a restored timeline with clips was
    // already fitted by applyTimeline). Deferred so the track has laid out.
    setTimeout(function () { if (!(state.timeline.clips || []).length) { fitTimelineZoom(10); } }, 250);
    // Push the (default-on) overlay state to mpv so the lower-third is armed; it draws
    // once a clip is loaded/played.
    updateOverlayBtn();
    sendOverlayUpdate();

    // Human-review Q&A cards (pre-loaded by the engine from a hits sidecar, if any).
    var q = await beckyCall('questions', {});
    if (q.ok && q.data && q.data.questions && q.data.questions.length) {
      state.questions = q.data.questions;
      renderQuestions();
    }

    // Default the chat to LOCAL Gemma (Jordan's rule): the box starts unchecked and we
    // push that to the engine on boot, rather than adopting the engine's own default.
    // Checking "use Claude" switches to Claude Code.
    state.online = $useClaude.checked; // false by default
    await beckyCall('set_online', { on: state.online });

    var st = await beckyCall('status', {});           // chat intro line
    if (st.ok && st.data) {
      setChatIntro(st.data.summary || 'becky is ready. Pick a case folder, then search or ask.');
    } else {
      setChatIntro('becky is ready. Pick a case folder, then search or ask.');
    }
  }

  // Expose a tiny surface for the CDP self-verify loop (Step 7 of the handoff).
  window.beckyReview = {
    applyTimeline: applyTimeline,
    applyFolder: applyFolder,
    doSearch: doSearch,
    reportVideoRect: reportVideoRect,
    setZoom: setZoom,
    splitAtPlayhead: splitAtPlayhead,
    undo: undoTimeline,
    redo: redoTimeline,
    extendSelected: extendSelected,
    selectOnly: selectOnly,
    toggleInSelection: toggleInSelection,
    selectRange: selectRange,
    clearSelection: clearSelection,
    buildWaveSvg: buildWaveSvg,
    fitTimelineZoom: fitTimelineZoom,
    // exposed for the CDP self-verify loop (the new feedback-7 features)
    seekClipEdge: seekClipEdge,
    deleteSelectedClips: deleteSelectedClips,
    quietIntervals: quietIntervals,
    renderTimeline: renderTimeline,
    peakData: peakData,
    state: state
  };

  boot();
})();
