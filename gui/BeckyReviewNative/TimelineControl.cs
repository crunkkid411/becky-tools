using System;
using System.Collections.Generic;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Windows.Forms;

namespace BeckyReviewNative;

/// <summary>
/// The native timeline — a single horizontal track of clips (Becky Review's real model),
/// drawn on the GPU-composited WinForms surface that overlays the page's #timelineHole, the
/// same way the mpv video pane overlays #videoHole. Replaces the DOM/SVG timeline whose cost
/// scaled with clip count (no virtualization). This one is O(visible): it only draws clips in
/// the visible time window, so it stays instant at any reel size (the lesson the ImGui spike
/// proved in native/timeline-bench).
///
/// Pure view + input: the page still owns the reel/edit/seek logic. This control is fed
/// clips + playhead + view (px/sec, scroll) and reports a compilation-seconds position when
/// the user scrubs; the page maps that to a file+seek exactly as a DOM-timeline click does.
///
/// Phase 2 adds accurate waveforms (DrawWave hook per clip) + edit affordances.
/// </summary>
public sealed class TimelineControl : Control
{
    public readonly record struct Clip(double Start, double Dur, string Label);

    private List<Clip> _clips = new();
    private double _pxPerSec = 8.0;    // zoom; page's state.pxPerSec
    private double _scrollSec = 0.0;   // left edge of the view in compilation seconds
    private double _playheadSec = 0.0;
    private double _totalSec = 0.0;

    private const int RulerH = 22;
    private const int TrackTop = RulerH + 2;

    // High-contrast dark palette (matches the app: #141414 ground, gold playhead, green edge).
    private static readonly Color Bg = Color.FromArgb(0x10, 0x10, 0x10);
    private static readonly Color RulerBg = Color.FromArgb(0x1A, 0x1A, 0x1A);
    private static readonly Color TrackBg = Color.FromArgb(0x0A, 0x0A, 0x0A);
    private static readonly Color TickCol = Color.FromArgb(0x60, 0x60, 0x60);
    private static readonly Color TextCol = Color.FromArgb(0xE8, 0xF4, 0xF6);
    private static readonly Color Playhead = Color.FromArgb(0xFF, 0xD7, 0x00);
    private static readonly Color ClipEdge = Color.FromArgb(0x39, 0xFF, 0x14);

    /// <summary>Raised (UI thread) with a COMPILATION-seconds position when the user scrubs.</summary>
    public event Action<double>? ScrubRequested;

    /// <summary>Raised when the user zooms/scrolls, so the page can keep its state in sync.</summary>
    public event Action<double, double>? ViewChanged; // (pxPerSec, scrollSec)

    public TimelineControl()
    {
        SetStyle(ControlStyles.OptimizedDoubleBuffer | ControlStyles.AllPaintingInWmPaint |
                 ControlStyles.UserPaint | ControlStyles.ResizeRedraw, true);
        BackColor = Bg;
    }

    public void SetClips(IReadOnlyList<Clip> clips)
    {
        _clips = new List<Clip>(clips);
        _totalSec = 0;
        foreach (var c in _clips) { if (c.Start + c.Dur > _totalSec) { _totalSec = c.Start + c.Dur; } }
        Invalidate();
    }

    public void SetView(double pxPerSec, double scrollSec)
    {
        if (pxPerSec > 0) { _pxPerSec = pxPerSec; }
        _scrollSec = Math.Max(0, scrollSec);
        Invalidate();
    }

    public void SetPlayhead(double sec)
    {
        if (Math.Abs(sec - _playheadSec) < 1e-4) { return; }
        _playheadSec = Math.Max(0, sec);
        Invalidate();
    }

    private int SecToX(double sec) => (int)Math.Round((sec - _scrollSec) * _pxPerSec);
    private double XToSec(int x) => _scrollSec + x / _pxPerSec;

    protected override void OnPaint(PaintEventArgs e)
    {
        var g = e.Graphics;
        g.SmoothingMode = SmoothingMode.None;
        g.Clear(Bg);
        int w = Width, h = Height;

        // track background
        using (var tb = new SolidBrush(TrackBg)) { g.FillRectangle(tb, 0, TrackTop, w, h - TrackTop); }

        double viewSecs = w / _pxPerSec;
        double viewEnd = _scrollSec + viewSecs;

        DrawRuler(g, w, viewEnd);

        // clips — VIRTUALIZED: only those overlapping [scrollSec, viewEnd]
        int trackH = h - TrackTop - 2;
        using var edgePen = new Pen(ClipEdge, 1f);
        using var lblBrush = new SolidBrush(TextCol);
        using var font = new Font("Segoe UI", 8.5f);
        var lblFmt = new StringFormat { Trimming = StringTrimming.EllipsisCharacter, FormatFlags = StringFormatFlags.NoWrap, LineAlignment = StringAlignment.Center };
        for (int i = 0; i < _clips.Count; i++)
        {
            var c = _clips[i];
            double cEnd = c.Start + c.Dur;
            if (cEnd < _scrollSec || c.Start > viewEnd) { continue; } // off-screen: skip
            int x1 = SecToX(c.Start), x2 = SecToX(cEnd);
            int cw = Math.Max(1, x2 - x1);
            var rect = new Rectangle(x1, TrackTop + 2, cw, trackH - 4);
            // distinct-but-calm per-clip fill; alternating shade keeps neighbours readable
            var fill = (i & 1) == 0 ? Color.FromArgb(0x24, 0x2B, 0x22) : Color.FromArgb(0x1C, 0x22, 0x1B);
            using (var fb = new SolidBrush(fill)) { g.FillRectangle(fb, rect); }
            g.DrawRectangle(edgePen, rect);
            // label (only if the clip is wide enough to bother — cheap guard)
            if (cw > 26 && !string.IsNullOrEmpty(c.Label))
            {
                var lr = new RectangleF(rect.X + 4, rect.Y, rect.Width - 8, rect.Height);
                g.DrawString(c.Label, font, lblBrush, lr, lblFmt);
            }
        }

        // playhead
        int px = SecToX(_playheadSec);
        if (px >= 0 && px <= w)
        {
            using var ph = new Pen(Playhead, 2f);
            g.DrawLine(ph, px, 0, px, h);
        }
    }

    private void DrawRuler(Graphics g, int w, double viewEnd)
    {
        using var rb = new SolidBrush(RulerBg);
        g.FillRectangle(rb, 0, 0, w, RulerH);
        // pick a "nice" second-step so ticks are ~80px apart
        double targetPx = 80;
        double step = targetPx / _pxPerSec;
        double[] nice = { 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600 };
        double chosen = nice[nice.Length - 1];
        foreach (var n in nice) { if (n >= step) { chosen = n; break; } }
        using var tick = new Pen(TickCol, 1f);
        using var tf = new Font("Consolas", 7.5f);
        using var tbrush = new SolidBrush(TickCol);
        double first = Math.Floor(_scrollSec / chosen) * chosen;
        for (double s = first; s <= viewEnd; s += chosen)
        {
            int x = SecToX(s);
            if (x < 0 || x > w) { continue; }
            g.DrawLine(tick, x, 0, x, RulerH);
            g.DrawString(Mmss(s), tf, tbrush, x + 2, 3);
        }
    }

    private static string Mmss(double sec)
    {
        if (sec < 0) { sec = 0; }
        int t = (int)Math.Round(sec);
        return $"{t / 60}:{t % 60:00}";
    }

    // --- input: click/drag = scrub (report compilation seconds); wheel = zoom ---

    private bool _dragging;

    protected override void OnMouseDown(MouseEventArgs e)
    {
        base.OnMouseDown(e);
        if (e.Button == MouseButtons.Left) { _dragging = true; Scrub(e.X); }
    }

    protected override void OnMouseMove(MouseEventArgs e)
    {
        base.OnMouseMove(e);
        if (_dragging) { Scrub(e.X); }
    }

    protected override void OnMouseUp(MouseEventArgs e)
    {
        base.OnMouseUp(e);
        _dragging = false;
    }

    private void Scrub(int x)
    {
        double comp = Math.Max(0, Math.Min(_totalSec, XToSec(x)));
        SetPlayhead(comp);
        ScrubRequested?.Invoke(comp);
    }

    protected override void OnMouseWheel(MouseEventArgs e)
    {
        base.OnMouseWheel(e);
        // zoom around the cursor: keep the time under the mouse fixed
        double atSec = XToSec(e.X);
        double factor = e.Delta > 0 ? 1.15 : 1 / 1.15;
        double newPx = Math.Max(0.2, Math.Min(200.0, _pxPerSec * factor));
        _scrollSec = Math.Max(0, atSec - e.X / newPx);
        _pxPerSec = newPx;
        ViewChanged?.Invoke(_pxPerSec, _scrollSec);
        Invalidate();
    }
}
