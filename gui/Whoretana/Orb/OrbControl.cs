using System;
using System.Diagnostics;
using System.Windows;
using System.Windows.Media;
using SkiaSharp;
using SkiaSharp.Views.Desktop;
using SkiaSharp.Views.WPF;

namespace Whoretana.Orb
{
    // Mirrors OrbEngine.OrbMode value-for-value (cast by int). Kept here so
    // MainWindow's `using Whoretana.Orb; Orb.Mode = OrbMode.Speaking;` compiles untouched.
    public enum OrbMode { Idle, Listening, Thinking, Speaking }

    // v2: thin SkiaSharp VIEW over OrbEngine.OrbSim (spec 2.8). The engine owns ALL
    // motion - particle attraction, face emergence choreography, visemes, eye reveal,
    // idle sway, listening push, thinking swirl. This class owns only colors, glow
    // sprites, rings, scanlines, mesh hint, trails and the ripple ring. The v1
    // datamosh composite is DELETED (D9: emergence, not corruption).
    public sealed class OrbControl : SKElement
    {
        // palette (spec 2.8 - no purple). Danger kept public for future error states.
        private static readonly SKColor[] ModeColor =
        {
            new SKColor(0x22, 0xE8, 0xFF),   // Idle
            new SKColor(0x3C, 0xFF, 0xC8),   // Listening
            new SKColor(0x2A, 0x9D, 0xFF),   // Thinking
            new SKColor(0x8C, 0xF6, 0xFF),   // Speaking (hot core)
        };
        public static readonly SKColor Danger = new SKColor(0xFF, 0x33, 0x66);

        private readonly OrbEngine.OrbSim _sim = new OrbEngine.OrbSim();
        private readonly Stopwatch _clock = new Stopwatch();
        private double _last;
        private double _t;                    // view-local time (breathing; engine owns twinkle)
        private float _ring0, _ring1, _ring2; // ring rotations (rings stay view-side)

        private float _jaw, _funnel, _pucker, _smile;
        private bool _eyePulse;
        private float _eyeDur;

        private float _cr, _cg, _cb;          // eased palette - nothing snaps
        private SKImage? _glow;

        public OrbMode Mode { get; set; } = OrbMode.Idle;
        public float MicLevel { get; set; }     // 0..1, set by the audio engine
        public float SpeechLevel { get; set; }  // 0..1, set while speaking (lip-sync)

        public void SetVisemes(float jaw, float funnel, float pucker, float smile)
        {
            _jaw = Clamp01(jaw); _funnel = Clamp01(funnel);
            _pucker = Clamp01(pucker); _smile = Clamp01(smile);
        }

        public void TriggerEyeReveal(float seconds)
        {
            _eyePulse = true;
            _eyeDur = seconds;
        }

        public OrbControl()
        {
            var c = ModeColor[0];
            _cr = c.Red; _cg = c.Green; _cb = c.Blue;
            Loaded += (_, __) => { _clock.Restart(); _last = 0; CompositionTarget.Rendering += OnFrame; };
            Unloaded += (_, __) => { CompositionTarget.Rendering -= OnFrame; };
        }

        private void OnFrame(object? sender, EventArgs e)
        {
            double now = _clock.Elapsed.TotalSeconds;
            double dt = now - _last;
            if (dt <= 0) return;
            _last = now;
            float d = (float)Math.Min(dt, 0.1);
            _t += d;

            var input = new OrbEngine.OrbInput
            {
                Mode = (OrbEngine.OrbMode)(int)Mode,
                MicLevel = Clamp01(MicLevel),
                SpeechLevel = Clamp01(SpeechLevel),
                Jaw = _jaw, Funnel = _funnel, Pucker = _pucker, Smile = _smile,
                EyeRevealPulse = _eyePulse,
                EyeRevealDuration = _eyeDur,
            };
            _eyePulse = false;
            _sim.Update(in input, (float)dt);   // engine clamps dt > 0.1 itself

            // rings stay view-side; Thinking = +30% ring speed (spec 2.4)
            float spd = Mode == OrbMode.Thinking ? 1.3f : 1f;
            _ring0 += d * 0.16f * spd;
            _ring1 -= d * 0.10f * spd;
            _ring2 += d * 0.26f * spd;

            var target = ModeColor[(int)Mode];
            float k = Math.Min(1f, d * 4f);
            _cr += (target.Red - _cr) * k;
            _cg += (target.Green - _cg) * k;
            _cb += (target.Blue - _cb) * k;

            InvalidateVisual();
        }

        // ---- rendering ------------------------------------------------------
        protected override void OnPaintSurface(SKPaintSurfaceEventArgs e)
        {
            var canvas = e.Surface.Canvas;
            canvas.Clear(SKColors.Transparent);

            int w = e.Info.Width, h = e.Info.Height;
            if (w <= 0 || h <= 0) return;
            EnsureGlow();

            float cx = w * 0.5f, cy = h * 0.5f;
            float R = Math.Min(w, h) * 0.42f;

            DrawRings(canvas, cx, cy, R);
            DrawMeshHint(canvas, cx, cy, R);
            DrawParticles(canvas, cx, cy, R);
            DrawEyes(canvas, cx, cy, R);
            DrawRipple(canvas, cx, cy, R);
            DrawScanlines(canvas, w, h);
        }

        private SKColor BaseColor(byte alpha = 255) => new SKColor((byte)_cr, (byte)_cg, (byte)_cb, alpha);

        private SKColor Toward(float t, byte alpha) => new SKColor(
            (byte)(_cr + (255 - _cr) * t),
            (byte)(_cg + (255 - _cg) * t),
            (byte)(_cb + (255 - _cb) * t),
            alpha);

        private void DrawParticles(SKCanvas s, float cx, float cy, float R)
        {
            if (_glow == null) return;
            using var paint = new SKPaint { BlendMode = SKBlendMode.Plus, FilterQuality = SKFilterQuality.Low };
            float mic = Clamp01(MicLevel);
            // view owns the mic/speech brightness boost (engine Brightness has twinkle + shimmer)
            float boost = 1f + (Mode == OrbMode.Listening ? mic * 0.8f : 0f) + (Mode == OrbMode.Speaking ? 0.2f : 0f);

            // central bloom: soft breathing core so the cloud reads as a neural mass
            float breathe = 0.85f + 0.15f * (float)Math.Sin(_t * 1.1);
            float swell = 1f + 0.5f * (Mode == OrbMode.Listening ? mic : (Mode == OrbMode.Speaking ? Clamp01(SpeechLevel) : 0f));
            float bloomR = R * 0.62f * breathe * swell;
            using (var bp = new SKPaint { BlendMode = SKBlendMode.Plus })
            {
                bp.ColorFilter = SKColorFilter.CreateBlendMode(BaseColor(70), SKBlendMode.Modulate);
                s.DrawImage(_glow, new SKRect(cx - bloomR, cy - bloomR, cx + bloomR, cy + bloomR), bp);
                float coreR = R * 0.26f * breathe;
                bp.ColorFilter = SKColorFilter.CreateBlendMode(Toward(0.6f, 90), SKBlendMode.Modulate);
                s.DrawImage(_glow, new SKRect(cx - coreR, cy - coreR, cx + coreR, cy + coreR), bp);
            }

            var parts = _sim.Particles;
            for (int i = 0; i < parts.Length; i++)
            {
                ref readonly var p = ref parts[i];
                float x = cx + p.X * R;
                float y = cy + p.Y * R;
                float dist = (float)Math.Sqrt(p.X * p.X + p.Y * p.Y);
                float tw = 0.8f - Math.Min(0.75f, dist) * 0.5f;
                float b = Clamp01(p.Brightness * boost);
                float sz = (2.8f + p.Size * 4.0f) * (0.85f + 0.45f * b);

                var col = Toward(0.8f * b, (byte)(70 + 185 * b * tw));
                var f = SKColorFilter.CreateBlendMode(col, SKBlendMode.Modulate);

                // trail: fading ghost at the previous position (spec 2.4)
                float px = cx + p.PrevX * R;
                float py = cy + p.PrevY * R;
                float dx = x - px, dy = y - py;
                if (dx * dx + dy * dy > 2f)
                {
                    using var gf = SKColorFilter.CreateBlendMode(col.WithAlpha((byte)(col.Alpha * 0.35f)), SKBlendMode.Modulate);
                    paint.ColorFilter = gf;
                    float gh = sz * 0.8f;
                    s.DrawImage(_glow, new SKRect(px - gh, py - gh, px + gh, py + gh), paint);
                }

                paint.ColorFilter = f;
                s.DrawImage(_glow, new SKRect(x - sz, y - sz, x + sz, y + sz), paint);
                paint.ColorFilter = null;
                f.Dispose();
            }
        }

        // Mesh hint: additive rim-lit triangles from the engine, alpha already
        // premultiplied engine-side (gate * fresnel * FaceVisibility * 0.18).
        private void DrawMeshHint(SKCanvas s, float cx, float cy, float R)
        {
            var verts = _sim.MeshVertices;
            if (verts.Length == 0) return;
            var pts = new SKPoint[verts.Length];
            var cols = new SKColor[verts.Length];
            for (int i = 0; i < verts.Length; i++)
            {
                pts[i] = new SKPoint(cx + verts[i].X * R, cy + verts[i].Y * R);
                cols[i] = Toward(0.35f, (byte)(Clamp01(verts[i].Alpha) * 255));
            }
            using var v = SKVertices.CreateCopy(SKVertexMode.Triangles, pts, cols);
            using var paint = new SKPaint { Color = SKColors.White, BlendMode = SKBlendMode.Plus, IsAntialias = true };
            s.DrawVertices(v, SKBlendMode.Modulate, paint);
        }

        private void DrawEyes(SKCanvas s, float cx, float cy, float R)
        {
            if (_glow == null) return;
            var eyes = _sim.Eyes;
            if (eyes.Length == 0) return;
            using var paint = new SKPaint { BlendMode = SKBlendMode.Plus };
            for (int i = 0; i < eyes.Length; i++)
            {
                ref readonly var eye = ref eyes[i];
                float x = cx + eye.X * R;
                float y = cy + eye.Y * R;
                float r = eye.Radius * R;
                float inten = Clamp01(eye.Intensity);
                paint.ColorFilter = SKColorFilter.CreateBlendMode(Toward(0.3f, (byte)(150 * inten)), SKBlendMode.Modulate);
                s.DrawImage(_glow, new SKRect(x - r * 2f, y - r * 2f, x + r * 2f, y + r * 2f), paint);
                paint.ColorFilter = SKColorFilter.CreateBlendMode(Toward(0.85f, (byte)(230 * inten)), SKBlendMode.Modulate);
                s.DrawImage(_glow, new SKRect(x - r, y - r, x + r, y + r), paint);
            }
            paint.ColorFilter = null;
        }

        // Expanding ring on any mode change; engine drives RippleT 1 -> 0 over 0.8 s.
        private void DrawRipple(SKCanvas s, float cx, float cy, float R)
        {
            float rt = _sim.RippleT;
            if (rt <= 0.02f) return;
            float rr = R * (0.55f + 0.65f * (1f - rt));
            using var p = new SKPaint
            {
                Style = SKPaintStyle.Stroke,
                StrokeWidth = 2.5f + 4f * (1f - rt),
                Color = Toward(0.4f, (byte)(150 * rt)),
                IsAntialias = true,
                BlendMode = SKBlendMode.Plus,
                MaskFilter = SKMaskFilter.CreateBlur(SKBlurStyle.Normal, 4),
            };
            s.DrawCircle(cx, cy, rr, p);
        }

        private void DrawRings(SKCanvas s, float cx, float cy, float R)
        {
            DrawRing(s, cx, cy, R * 1.06f, _ring0, 56, 0.55f);
            DrawRing(s, cx, cy, R * 0.86f, _ring1, 40, 0.40f);
            DrawRing(s, cx, cy, R * 0.66f, _ring2, 28, 0.30f);
        }

        private void DrawRing(SKCanvas s, float cx, float cy, float r, float rot, int ticks, float alpha)
        {
            byte a = (byte)(alpha * 255);
            var glow = BaseColor((byte)(a * 0.5f));
            var line = Toward(0.55f, a);

            using (var gp = new SKPaint { Style = SKPaintStyle.Stroke, StrokeWidth = 6, Color = glow,
                       IsAntialias = true, BlendMode = SKBlendMode.Plus,
                       MaskFilter = SKMaskFilter.CreateBlur(SKBlurStyle.Normal, 5) })
                s.DrawCircle(cx, cy, r, gp);

            // segmented (gear-like) arc
            using (var dp = new SKPaint { Style = SKPaintStyle.Stroke, StrokeWidth = 1.6f, Color = line,
                       IsAntialias = true, PathEffect = SKPathEffect.CreateDash(new[] { r * 0.18f, r * 0.10f }, rot * r) })
                s.DrawCircle(cx, cy, r, dp);

            // tick marks
            using var tp = new SKPaint { Style = SKPaintStyle.Stroke, StrokeWidth = 1.2f, Color = line, IsAntialias = true };
            for (int i = 0; i < ticks; i++)
            {
                float ang = rot + (float)(i * Math.PI * 2 / ticks);
                float c = (float)Math.Cos(ang), sn = (float)Math.Sin(ang);
                float r0 = r - 5, r1 = r + (i % 6 == 0 ? 11 : 5);
                s.DrawLine(cx + c * r0, cy + sn * r0, cx + c * r1, cy + sn * r1, tp);
            }
        }

        private void DrawScanlines(SKCanvas c, int w, int h)
        {
            using var p = new SKPaint { Color = new SKColor(0, 0, 0, 60), StrokeWidth = 1 };
            for (int y = 0; y < h; y += 3) c.DrawLine(0, y, w, y, p);
        }

        // ---- resources ------------------------------------------------------
        private void EnsureGlow()
        {
            if (_glow != null) return;
            const int N = 48;
            var info = new SKImageInfo(N, N, SKColorType.Rgba8888, SKAlphaType.Premul);
            using var surf = SKSurface.Create(info);
            var c = surf.Canvas;
            c.Clear(SKColors.Transparent);
            using var shader = SKShader.CreateRadialGradient(
                new SKPoint(N / 2f, N / 2f), N / 2f,
                new[] { new SKColor(255, 255, 255, 255), new SKColor(255, 255, 255, 0) },
                new[] { 0f, 1f }, SKShaderTileMode.Clamp);
            using var p = new SKPaint { Shader = shader, IsAntialias = true };
            c.DrawCircle(N / 2f, N / 2f, N / 2f, p);
            _glow = surf.Snapshot();
        }

        private static float Clamp01(float v) => v < 0 ? 0 : (v > 1 ? 1 : v);
    }
}
