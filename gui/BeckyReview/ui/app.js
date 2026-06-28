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
  var ZOOM_MAX = 120;
  var MINW = 96;           // base/cap for the min clip-block width; the live floor scales with zoom
  var MAX_ROWS = 400;      // cap rendered quote rows (search can return many)
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

    timeline: { clips: [], overlay: {}, duration_sec: 0 },
    overlayOn: false,
    pxPerSec: DEFAULT_PXPS, // timeline zoom: px per second (clamped ZOOM_MIN..ZOOM_MAX)

    activeSource: null,    // path currently loaded in mpv
    activeClipId: null,    // timeline clip whose source is playing (for the playhead)
    pos: 0, dur: 0,        // last {t:"time"} report
    playheadComp: 0,       // current COMPILATION position (active clip start_sec + offset) - drives split (CHANGE 4)
    selectedClipId: null,  // last-selected timeline clip - target for ripple delete via Del/Esc (CHANGE 5)

    transcribing: {},      // name -> true while a single transcribe runs
    transcribingAll: false,
    online: false          // false = local Gemma (default), true = Claude
  };

  var proposals = {};       // id -> Proposal awaiting approve/reject

  /* --------------------------- DOM references ----------------------------- */
  var $search      = document.getElementById('search');
  var $searchClear = document.getElementById('searchClear');
  var $listScroll  = document.getElementById('listScroll');

  var $useClaude = document.getElementById('useClaude');
  var $messages  = document.getElementById('messages');
  var $chips     = document.getElementById('chips');
  var $ask       = document.getElementById('ask');
  var $send      = document.getElementById('send');

  var $tlCount   = document.getElementById('tlCount');
  var $tPlay     = document.getElementById('tPlay');
  var $tFrameBack= document.getElementById('tFrameBack');
  var $tFrameFwd = document.getElementById('tFrameFwd');
  var $tSplit    = document.getElementById('tSplit');     // split clip at playhead (CHANGE 4)
  var $tOverlay  = document.getElementById('tOverlay');
  var $tSave     = document.getElementById('tSave');
  var $tLoad     = document.getElementById('tLoad');
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

  /** Invoke a backend verb; resolves with the reply envelope {ok,data,error}. */
  function beckyCall(verb, args) {
    return new Promise(function (resolve) {
      var id = 'ui' + (++callSeq) + '-' + Date.now();
      pending.set(id, resolve);
      post({ t: 'call', id: id, verb: verb, args: args || {} });
      setTimeout(function () {
        if (pending.has(id)) {
          pending.delete(id);
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
        case 'folder': onFolder(m.reply);    break;
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
  if (window.ResizeObserver && videoHoleEl) {
    try { new ResizeObserver(reportVideoRect).observe(videoHoleEl); } catch (_) {}
  }

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
    // title= holds the FULL name so a long filename (the tail differentiates
    // duplicates) is always readable on hover even when the row ellipsises it.
    return '<div class="file" data-name="' + attr(v.name) + '" title="' + attr(v.name) + '">' +
             '<div class="fmeta">' +
               '<div class="fname">' + escapeHtml(v.name) + '</div>' +
               (sub ? '<div class="fsub">' + escapeHtml(sub) + '</div>' : '') +
             '</div>' + btn +
           '</div>';
  }

  function renderFiles() {
    if (!state.videos.length) {
      $listScroll.innerHTML = emptyHTML('Pick a case folder, then search.');
      return;
    }
    var head =
      '<div class="findhead">' +
        '<span>' + state.videos.length + ' video' + (state.videos.length === 1 ? '' : 's') + '</span>' +
        '<button class="btn small" data-act="transcribe-all"' + (state.transcribingAll ? ' disabled' : '') + '>' +
          (state.transcribingAll ? 'transcribing…' : 'Transcribe all') +
        '</button>' +
      '</div>';
    $listScroll.innerHTML = head + '<div class="filelist">' + state.videos.map(fileRowHTML).join('') + '</div>';
  }

  /* ---- ranked search results / transcript cues ---- */
  function qrowHTML(r, i) {
    var key = rowKey(r, i);
    var tonly = !!r.transcript_only || !r.source;
    var tc = r.timecode || hms(r.start);
    return '<div class="qrow' + (tonly ? ' tonly' : '') + (state.activeResultKey === key ? ' active' : '') +
             '" data-idx="' + i + '" data-key="' + attr(key) + '">' +
             '<div class="qtc">' + escapeHtml(tc) + '</div>' +
             '<div class="qbody">' +
               '<div class="qtext">' + highlight(r.text || '', state.terms) + '</div>' +
               '<div class="qsrc">' + escapeHtml(r.name || baseName(r.source)) +
                 (tonly ? ' <span class="qbadge">transcript only</span>' : '') +
               '</div>' +
             '</div>' +
           '</div>';
  }

  function renderResults() {
    var rows = state.rows || [];
    // The back control lives INSIDE the sticky header so it stays reachable even
    // after scrolling a long cue list — needed because clicking a VIDEO (not
    // searching) shows its cues with no search box to clear (CHANGE: go-back).
    var html = '<div class="resultshead">' +
                 '<button class="backbtn" data-act="back-to-files" title="back to the video list">&#8249; all videos</button>' +
                 '<span class="rhtext">' + escapeHtml(state.headerText || '') + '</span>' +
               '</div>';
    if (!rows.length) {
      $listScroll.innerHTML = html + emptyHTML('No quotes match.', '&#128269;');
      return;
    }
    var shown = rows.slice(0, MAX_ROWS);
    html += '<div class="qlist">' + shown.map(function (r, i) { return qrowHTML(r, i); }).join('') + '</div>';
    if (rows.length > shown.length) {
      html += '<div class="more">Showing ' + shown.length + ' of ' + rows.length + '. Refine your search to narrow it.</div>';
    }
    $listScroll.innerHTML = html;
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
    state.query = query;
    if (!query) { state.mode = 'files'; renderFind(); return; }

    // CHANGE 1: show a "Searching…" state the instant a non-empty search starts, so a slow
    // or failed search is never a silent blank. The post-await logic below replaces this.
    state.mode = 'results';
    state.rows = [];
    state.terms = [];
    state.activeResultKey = null;
    state.headerText = 'Searching for "' + query + '"…';
    renderFind();

    var rep = await beckyCall('search', { query: query });
    // a newer search may have superseded this one
    if (state.query !== query) { return; }
    if (!rep.ok) {
      state.mode = 'results'; state.rows = []; state.terms = [];
      state.headerText = 'Search failed' + (rep.error ? ': ' + rep.error : '');
      renderFind();
      return;
    }
    var results = Array.isArray(rep.data) ? rep.data : [];
    var transcriptOnly = results.filter(function (r) { return r.transcript_only; }).length;
    var playable = results.length - transcriptOnly;

    state.mode = 'results';
    state.rows = results;
    state.terms = query.split(/\s+/).filter(Boolean);
    state.activeResultKey = null;
    state.headerText = results.length + ' quotes across the transcripts for "' + query +
      '" (' + playable + ' playable, ' + transcriptOnly + ' transcript-only)';
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
    var rep = await beckyCall('add_clip', { source: r.source, in: inSec, out: outSec, label: r.text || '' });
    if (rep.ok && rep.data) { applyTimeline(rep.data); toast('Added to timeline'); }
    else { toast('Could not add clip' + (rep.error ? ': ' + rep.error : '')); }
  }

  /* ---- file click -> show its cues + play from the start ---- */
  async function onFileClick(name) {
    var v = null;
    for (var i = 0; i < state.videos.length; i++) { if (state.videos[i].name === name) { v = state.videos[i]; break; } }
    if (!v) { return; }
    state.activeSource = v.path;
    state.activeClipId = null;
    updatePlayhead();
    mpvPlay(v.path, 0);

    var rep = await beckyCall('transcript', { name: name });
    var cues = (rep.ok && Array.isArray(rep.data)) ? rep.data : [];
    state.mode = 'results';
    state.rows = cues;
    state.terms = [];
    state.activeResultKey = null;
    state.headerText = name + ' — ' + cues.length + ' line' + (cues.length === 1 ? '' : 's');
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

  /* ---- delegated clicks for the whole find list ---- */
  function backToFiles() {
    $search.value = ''; $searchClear.hidden = true;
    state.query = ''; state.activeResultKey = null;
    state.mode = 'files';
    renderFind();
  }

  $listScroll.addEventListener('click', function (e) {
    var back = e.target.closest('[data-act="back-to-files"]');
    if (back) { backToFiles(); return; }
    var tbtn = e.target.closest('.tbtn');
    if (tbtn) { if (!tbtn.disabled) { e.stopPropagation(); onTranscribeClick(tbtn.dataset.name); } return; }
    var all = e.target.closest('[data-act="transcribe-all"]');
    if (all) { if (!all.disabled) { onTranscribeAll(); } return; }
    var file = e.target.closest('.file');
    if (file) { onFileClick(file.dataset.name); return; }
    var row = e.target.closest('.qrow');
    if (row) { guardRowClick(row); }
  });
  $listScroll.addEventListener('dblclick', function (e) {
    var row = e.target.closest('.qrow');
    if (!row) { return; }
    if (rowClickTimer) { clearTimeout(rowClickTimer); rowClickTimer = null; }   // cancel the pending single-click play
    onRowDbl(+row.dataset.idx);
  });

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

  /* =======================================================================
     FOLDER
     ===================================================================== */
  function applyFolder(fv) {
    if (!fv || typeof fv !== 'object') { return; }
    state.folder = fv.root || state.folder;
    state.videos = Array.isArray(fv.videos) ? fv.videos : [];
    state.orphanCount = fv.orphan_count || 0;
    state.mode = 'files';
    $search.value = ''; $searchClear.hidden = true;
    renderFind();
  }
  function onFolder(reply) {
    if (reply && reply.ok && reply.data) { applyFolder(reply.data); }
    else { toast('Could not open folder' + (reply && reply.error ? ': ' + reply.error : '')); }
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
    $ask.value = '';
    addUserMsg(text);
    var rep = await beckyCall('ask', { utterance: text });
    if (!rep.ok) { addBeckyMsg('(could not reach becky: ' + (rep.error || 'error') + ')'); return; }
    renderProposal(rep.data || {});
  }
  $send.addEventListener('click', sendAsk);
  $ask.addEventListener('keydown', function (e) { if (e.key === 'Enter') { sendAsk(); } });

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

  function applyTimeline(tl) {
    if (!tl || typeof tl !== 'object') { return; }
    state.timeline = {
      clips: Array.isArray(tl.clips) ? tl.clips : [],
      overlay: tl.overlay || {},
      duration_sec: typeof tl.duration_sec === 'number' ? tl.duration_sec : 0
    };
    state.overlayOn = !!(state.timeline.overlay && state.timeline.overlay.enabled);
    if (state.activeClipId != null && !clipById(state.activeClipId)) { state.activeClipId = null; }
    if (state.selectedClipId != null && !clipById(state.selectedClipId)) { state.selectedClipId = null; }  // CHANGE 5
    renderTimeline();
  }

  /* ---- zoom (CHANGE 5): one px-per-second scale drives clip widths + the ruler ---- */
  function minClipW() {
    // The floor keeps a clip grabbable (two 8px trim handles + a body) and SCALES with
    // zoom, but is capped at MINW so zooming still spreads the timeline.
    return Math.max(24, Math.min(MINW, state.pxPerSec * 4));
  }
  function clipW(dur) { return Math.max(minClipW(), (dur || 0) * state.pxPerSec); }
  function updateZoomLabel() { if ($tZoom) { $tZoom.textContent = state.pxPerSec + ' px/s'; } }
  function setZoom(px) {
    var v = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, Math.round(px)));
    if (v === state.pxPerSec) { updateZoomLabel(); return; }
    state.pxPerSec = v;
    renderTimeline();   // re-render clip widths + ruler at the new scale
  }
  function zoomBy(factor) { setZoom(state.pxPerSec * factor); }

  /* Scale the ruler ticks + the track grid lines to the current zoom
     (1-second minor ticks, 5-second major ticks). */
  function applyTimelineScale() {
    var s = state.pxPerSec, maj = s * 5;
    rulerEl.style.backgroundImage =
      'repeating-linear-gradient(90deg, var(--line) 0 1px, transparent 1px ' + s + 'px),' +
      'repeating-linear-gradient(90deg, var(--line-2) 0 1px, transparent 1px ' + maj + 'px)';
    trackEl.style.backgroundImage =
      'repeating-linear-gradient(90deg, transparent 0 ' + (maj - 1) + 'px, var(--line-2) ' + (maj - 1) + 'px ' + maj + 'px)';
  }

  /* A clip is a SOLID block with NO visible text (CHANGE 3): label + duration ride
     in the title= tooltip only. Keeps the two trim handles + a hover-only remove "x".
     The empty .cbody fills between the handles and forwards clicks to the scrubber. */
  function clipBlockHTML(clip) {
    var dur = clipDur(clip);
    var w = clipW(dur);
    var label = clip.label || (clip.source ? baseName(clip.source) : 'clip');
    var tip = truncate(label, 80) + '  (' + mmss(dur) + ')';
    var sel = (String(clip.id) === String(state.selectedClipId)) ? ' selected' : '';   // CHANGE 5
    return '<div class="clip' + sel + '" data-id="' + attr(clip.id) + '" style="width:' + w + 'px" title="' + attr(tip) + '">' +
             '<div class="rh rh-l" data-edge="l" title="trim in"></div>' +
             '<div class="cbody"></div>' +
             '<button class="cx" data-act="remove" title="remove clip">×</button>' +
             '<div class="rh rh-r" data-edge="r" title="trim out"></div>' +
           '</div>';
  }

  function renderTimeline() {
    var clips = state.timeline.clips || [];
    var dur = state.timeline.duration_sec || sumDur(clips);
    $tlCount.textContent = clips.length + ' clip' + (clips.length === 1 ? '' : 's') + ' · ' + mmss(dur);
    updateOverlayBtn();
    updateZoomLabel();
    applyTimelineScale();

    if (!clips.length) {
      trackEl.innerHTML = '<div class="tlempty">No clips yet — double-click a quote to add one to the timeline.</div>';
    } else {
      trackEl.innerHTML = clips.map(clipBlockHTML).join('');
    }
    trackEl.appendChild(playheadEl);
    updatePlayhead();
    prefetchSourceDurations();   // CHANGE C: warm each source's true duration so a resize is bounded immediately
    applyThumbs();               // paint each clip's cached first-frame thumbnail (async, cheap)
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
        var cbody = b.querySelector('.cbody');
        if (cbody && cbody.dataset.thumbKey !== key) {
          cbody.style.backgroundImage = 'url("' + cached + '")';
          cbody.dataset.thumbKey = key;
        }
      } else if (cached === undefined && !thumbInflight[key]) {
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

  function updateOverlayBtn() {
    $tOverlay.classList.toggle('on', !!state.overlayOn);
    $tOverlay.textContent = state.overlayOn ? 'overlay ✓' : 'overlay';
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

  /* ---- playhead (driven by incoming {t:"time"} messages) ---- */
  function onTime(pos, dur) {
    state.pos = (typeof pos === 'number') ? pos : 0;
    state.dur = (typeof dur === 'number') ? dur : 0;
    // CHANGE 4: when a timeline clip is the active source, the SOURCE pos maps to a COMPILATION
    // position: clip.start_sec + (sourcePos - clip.in). Stored so "split at playhead" knows where.
    if (state.activeClipId != null) {
      var ac = clipById(state.activeClipId);
      if (ac) { state.playheadComp = (ac.start_sec || 0) + (state.pos - (ac.in || 0)); }
    }
    updatePlayhead();
  }
  function updatePlayhead() {
    var id = state.activeClipId;
    if (id == null) { playheadEl.style.display = 'none'; return; }
    var block = blockById(id), clip = clipById(id);
    if (!block || !clip) { playheadEl.style.display = 'none'; return; }
    var dur = clipDur(clip);
    if (dur <= 0) { playheadEl.style.display = 'none'; return; }
    var frac = (state.pos - (clip.in || 0)) / dur;
    frac = Math.max(0, Math.min(1, frac));
    playheadEl.style.left = (block.offsetLeft + frac * block.offsetWidth) + 'px';
    playheadEl.style.display = 'block';
  }

  /* ---- selection outline (CHANGE 5): toggle .selected on existing blocks without a re-render ---- */
  function markSelectedClip() {
    var blocks = trackEl.querySelectorAll('.clip');
    for (var i = 0; i < blocks.length; i++) {
      blocks[i].classList.toggle('selected', blocks[i].dataset.id === String(state.selectedClipId));
    }
  }

  async function onClipRemove(id) {
    var rep = await beckyCall('remove_clip', { id: id });
    if (rep.ok && rep.data) { applyTimeline(rep.data); }
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
    var ph = (typeof state.playheadComp === 'number') ? state.playheadComp : null;
    if (ph == null) { toast('Play or scrub to a point on the timeline first.'); return; }

    var clip = null;
    for (var i = 0; i < clips.length; i++) {
      var c = clips[i], s = c.start_sec || 0, d = clipDur(c);
      if (ph >= s && ph < s + d) { clip = c; break; }
    }
    if (!clip) { toast('No clip under the playhead.'); return; }

    var srcSplit = (clip.in || 0) + (ph - (clip.start_sec || 0));
    // must land strictly inside the clip, with a >= 0.1s margin on each side
    if (srcSplit <= (clip.in || 0) + 0.1 || srcSplit >= (clip.out || 0) - 0.1) {
      toast('Playhead is too close to a clip edge to split.');
      return;
    }

    splitting = true;
    try {
      var leftId = clip.id;
      var repL = await beckyCall('set_trim', { id: leftId, in: clip.in || 0, out: srcSplit });
      if (!repL.ok) { toast('Split failed' + (repL.error ? ': ' + repL.error : '')); return; }
      var repR = await beckyCall('add_clip', { source: clip.source, in: srcSplit, out: clip.out || 0, label: clip.label || '' });
      if (!repR.ok || !repR.data) {
        if (repL.data) { applyTimeline(repL.data); }
        toast('Split failed' + (repR.error ? ': ' + repR.error : ''));
        return;
      }
      applyTimeline(repR.data);

      // add_clip appends to the END; move the new right half to just after the left half.
      var now = state.timeline.clips || [];
      var newClip = now.length ? now[now.length - 1] : null;   // appended clip is last
      var leftIdx = -1;
      for (var j = 0; j < now.length; j++) { if (String(now[j].id) === String(leftId)) { leftIdx = j; break; } }
      if (newClip && leftIdx >= 0 && String(newClip.id) !== String(leftId)) {
        var repO = await beckyCall('reorder', { id: newClip.id, to: leftIdx + 1 });
        if (repO.ok && repO.data) { applyTimeline(repO.data); }
      }
      toast('Split clip');
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
    resizing = {
      id: clip.id, edge: handle.dataset.edge, startX: e.clientX,
      block: block, clip: clip, pxPerSec: w / dur,
      origIn: clip.in || 0, origOut: clip.out || 0,
      newIn: clip.in || 0, newOut: clip.out || 0
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
    r.block.title = (r.block.title || '').replace(/\s*\([^)]*\)\s*$/, '') + '  (' + mmss(newDur) + ')';
    updatePlayhead();
  }
  async function endResize() {
    if (!resizing) { return; }
    var r = resizing; resizing = null;
    justResized = true;
    setTimeout(function () { justResized = false; }, 350);
    var changed = Math.abs(r.newIn - r.origIn) > 0.001 || Math.abs(r.newOut - r.origOut) > 0.001;
    if (!changed) { renderTimeline(); return; }   // snap back to the committed widths
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
  function dropInsertIndex(id, x) {
    var blocks = trackEl.querySelectorAll('.clip');
    var insert = 0;
    for (var i = 0; i < blocks.length; i++) {
      if (blocks[i].dataset.id === String(id)) { continue; }
      if (x > blocks[i].offsetLeft + blocks[i].offsetWidth / 2) { insert++; }
    }
    return insert;
  }
  function positionDropmark(id, x) {
    var others = [], all = trackEl.querySelectorAll('.clip');
    for (var i = 0; i < all.length; i++) { if (all[i].dataset.id !== String(id)) { others.push(all[i]); } }
    var insert = dropInsertIndex(id, x), leftPx;
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
    clipGesture = { id: block.dataset.id, block: block, startX: e.clientX, dragging: false };
    try { trackEl.setPointerCapture(e.pointerId); } catch (_) {}
  }
  function moveClipGesture(e) {
    var g = clipGesture;
    if (!g.dragging) {
      if (Math.abs(e.clientX - g.startX) <= DRAG_PX) { return; }   // below threshold -> still a click
      g.dragging = true;                                           // crossed it -> become a reorder drag
      g.block.classList.add('dragging');
      trackEl.appendChild(dropmarkEl);
    }
    positionDropmark(g.id, eventTrackX(e));
  }
  async function endClipGesture(e) {
    if (!clipGesture) { return; }
    var g = clipGesture; clipGesture = null;
    if (g.block) { g.block.classList.remove('dragging'); }
    if (dropmarkEl.parentNode) { dropmarkEl.parentNode.removeChild(dropmarkEl); }
    dropmarkEl.style.display = 'none';
    justScrubbed = true;                              // eat the trailing click in BOTH cases
    setTimeout(function () { justScrubbed = false; }, 350);

    if (g.dragging) {
      // a DRAG happened -> reorder, but only when the target index truly differs.
      var to = dropInsertIndex(g.id, eventTrackX(e));
      var from = clipIndexById(g.id);
      if (from >= 0 && to !== from) {
        var rep = await beckyCall('reorder', { id: g.id, to: to });
        if (rep.ok && rep.data) { applyTimeline(rep.data); toast('Reordered clip'); }
        else { renderTimeline(); toast('Could not reorder' + (rep.error ? ': ' + rep.error : '')); }
      } else {
        renderTimeline();                             // no change -> just clear the drag visuals
      }
    } else {
      // a CLICK (moved <= DRAG_PX) -> seek the playhead + select the clip.
      scrubTo(e, true);
    }
  }

  /* ---- the unified track pointer handlers ---- */
  trackEl.addEventListener('pointerdown', function (e) {
    if (e.button !== undefined && e.button !== 0) { return; }   // left button only
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
    scrubTo(e, true);
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
  function scrubTo(e, isStart) {
    var rect = trackEl.getBoundingClientRect();   // ruler shares the track's left edge + width
    var x = e.clientX - rect.left;
    var hit = findClipAtX(x);
    if (!hit) { return; }
    var clip = clipById(hit.id);
    if (!clip) { return; }
    var offset = (clip.in || 0) + hit.frac * clipDur(clip);
    state.activeClipId = clip.id;
    state.selectedClipId = clip.id;                // CHANGE 5: scrubbing/clicking also selects that clip
    state.playheadComp = (clip.start_sec || 0) + hit.frac * clipDur(clip);  // CHANGE 4: keep comp pos exact
    markSelectedClip();
    // Navigating the timeline NEVER auto-plays (that's the ▶/space job). A new source
    // loads + seeks and holds the frame PAUSED; the same source just seeks, and a FRESH
    // click/scrub-start (isStart) also pauses so a click can never start playback.
    // (Search-result clicks still play — that's mpvPlay, a separate path.)
    if (state.activeSource !== clip.source) {
      state.activeSource = clip.source;
      mpvLoadAt(clip.source, offset);             // new source: load + seek, stay paused
    } else {
      mpvSeek(offset);                            // same source already loaded: exact seek
      if (isStart) { mpvSend('pause'); }          // a fresh click/scrub never auto-plays
    }
    updatePlayhead();
  }
  rulerEl.addEventListener('pointerdown', function (e) {
    if (!(state.timeline.clips || []).length) { return; }
    scrubbing = true;
    try { rulerEl.setPointerCapture(e.pointerId); } catch (_) {}
    scrubTo(e, true);
  });
  rulerEl.addEventListener('pointermove', function (e) { if (scrubbing) { scrubTo(e, false); } });
  rulerEl.addEventListener('pointerup', function () { scrubbing = false; });
  rulerEl.addEventListener('pointercancel', function () { scrubbing = false; });

  /* ---- transport + reel actions ---- */
  $tPlay.addEventListener('click', function () { mpvSend('toggle'); });
  $tFrameBack.addEventListener('click', function () { mpvSend('frame', { dir: -1 }); });
  $tFrameFwd.addEventListener('click', function () { mpvSend('frame', { dir: 1 }); });
  if ($tSplit) { $tSplit.addEventListener('click', function () { splitAtPlayhead(); }); }  // CHANGE 4

  /* ---- timeline zoom: buttons + mousewheel over the timeline ---- */
  if ($tZoomIn)  { $tZoomIn.addEventListener('click',  function () { zoomBy(1.5); }); }
  if ($tZoomOut) { $tZoomOut.addEventListener('click', function () { zoomBy(1 / 1.5); }); }
  if (tlBodyEl) {
    // CHANGE 2: PLAIN wheel over the timeline now zooms (up = in, down = out); no modifier needed
    // (Ctrl+wheel zooms too). preventDefault stops the page/timeline from scrolling instead.
    tlBodyEl.addEventListener('wheel', function (e) {
      e.preventDefault();
      zoomBy(e.deltaY < 0 ? 1.15 : 1 / 1.15);
    }, { passive: false });
  }

  $tOverlay.addEventListener('click', async function () {
    state.overlayOn = !state.overlayOn;
    updateOverlayBtn();
    var rep = await beckyCall('set_overlay', { field: 'enabled', value: state.overlayOn });
    if (rep.ok && rep.data) { applyTimeline(rep.data); }   // re-syncs the stored overlay state
    var meta = activeMeta();
    mpvSend('overlay', {
      on: state.overlayOn,
      file: state.activeSource || '',
      date: meta.date || '',
      link: meta.link || '',
      fps: meta.fps || 30
    });
  });

  $tSave.addEventListener('click', async function () {
    var def = state.folder ? (state.folder + '\\reel.json') : 'reel.json';
    var path = window.prompt('Save reel as (full path):', def);
    if (!path) { return; }
    var rep = await beckyCall('save_reel', { path: path });
    if (rep.ok) { toast('Saved ' + ((rep.data && rep.data.path) || path)); }
    else { toast('Save failed' + (rep.error ? ': ' + rep.error : '')); }
  });

  $tLoad.addEventListener('click', async function () {
    var def = state.folder ? (state.folder + '\\reel.json') : 'reel.json';
    var path = window.prompt('Load reel from (full path):', def);
    if (!path) { return; }
    var rep = await beckyCall('load_reel', { path: path });
    if (rep.ok && rep.data) { applyTimeline(rep.data); toast('Loaded reel'); }
    else { toast('Load failed' + (rep.error ? ': ' + rep.error : '')); }
  });

  $tExport.addEventListener('click', async function () {
    if (!state.timeline.clips || !state.timeline.clips.length) { toast('Timeline is empty — add clips first.'); return; }
    toast('Exporting…');
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
    if (typingInField()) { return; }   // never hijack keys while the user is typing (CHANGE 8)

    // Space = play / pause (CHANGE 3)
    if (e.key === ' ') {
      e.preventDefault();
      mpvSend('toggle');
      return;
    }
    // s = split clip at the playhead (CHANGE 4)
    if (e.key === 's' || e.key === 'S') {
      e.preventDefault();
      splitAtPlayhead();
      return;
    }
    // Delete / Escape = ripple-delete the selected clip (CHANGE 5)
    if ((e.key === 'Delete' || e.key === 'Escape') && state.selectedClipId != null) {
      e.preventDefault();
      var id = state.selectedClipId;
      state.selectedClipId = null;
      var rep = await beckyCall('remove_clip', { id: id });   // server-side remove auto-ripples start_sec
      if (rep.ok && rep.data) { applyTimeline(rep.data); toast('Removed clip'); }
      else { toast('Could not remove clip' + (rep.error ? ': ' + rep.error : '')); }
      markSelectedClip();
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
    state: state
  };

  boot();
})();
