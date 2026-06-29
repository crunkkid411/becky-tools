using System;
using System.Diagnostics;
using System.Windows;
using System.Windows.Media;
using SkiaSharp;
using SkiaSharp.Views.Desktop;
using SkiaSharp.Views.WPF;

namespace Whoretana.Orb
{
    public enum OrbMode { Idle, Listening, Speaking }

    // The hero. A cloud of additive glowing particles inside rotating reticle/gear
    // rings, with a datamosh glitch pass. Three states:
    //   Idle      - ambient slow drift, gentle breathing, slow rings.
    //   Listening - particles pulse OUTWARD with the mic level ("it hears me").
    //   Speaking  - particles re-arrange into an emergent FACE; the mouth opens with
    //               the speech level (lip-sync). Glitch is heaviest here, which is
    //               exactly what hides the rough artwork (per Jordan's brief / head3.gif).
    // Self-ticks via CompositionTarget.Rendering; software-rendered by SKElement (no GL
    // context, no server). Tunables are consts so the feel can be dialed without hunting.
    public sealed class OrbControl : SKElement
    {
        // ---- tunables -------------------------------------------------------
        private const int ParticleCount = 900;
        private const int BranchCount = 26;          // dendrite filaments
        private const float LerpIdle = 0.06f;        // how fast particles chase their target
        private const float LerpFace = 0.14f;        // snappier when forming the face
        private const float ListenPush = 0.55f;      // outward expansion at full mic level
        private const float MouthMaxOpen = 0.20f;    // fraction of radius the lower lip drops

        // feature tags for the speaking face
        private enum Feature { Halo, EyeL, EyeR, LipUpper, LipLower, Brow }

        private struct Particle
        {
            public float HomeR, HomeA;       // ambient polar home (0..1 radius, radians)
            public float Phase;              // drift phase
            public float Bright;             // base brightness 0..1
            public float Size;               // base sprite scale
            public float Cx, Cy;            // current position in unit space (-1..1)
            public float FaceX, FaceY;      // target when forming the face (unit space)
            public Feature Feat;
            public int Branch;
        }

        private readonly Particle[] _p = new Particle[ParticleCount];
        private readonly Random _rnd = new Random(0xBECCA);
        private SKImage? _glow;             // soft dot sprite, drawn additively
        private SKSurface? _scene;          // offscreen the orb draws into, then glitched onto screen
        private int _sceneW, _sceneH;

        private readonly Stopwatch _clock = new Stopwatch();
        private double _t;                  // seconds
        private double _last;
        private float _faceAmount;          // 0 ambient .. 1 full face
        private float _ring0, _ring1, _ring2; // ring rotations

        // glitch envelope
        private float _glitch;             // 0..1 current intensity
        private double _nextBurst;

        public OrbMode Mode { get; set; } = OrbMode.Idle;
        public float MicLevel { get; set; }     // 0..1, set by the audio engine
        public float SpeechLevel { get; set; }  // 0..1, set while speaking (lip-sync)

        public OrbControl()
        {
            BuildParticles();
            Loaded += (_, __) => { _clock.Restart(); _last = 0; CompositionTarget.Rendering += OnFrame; };
            Unloaded += (_, __) => { CompositionTarget.Rendering -= OnFrame; };
        }

        private void OnFrame(object? sender, EventArgs e)
        {
            double now = _clock.Elapsed.TotalSeconds;
            double dt = now - _last;
            if (dt <= 0) return;
            if (dt > 0.1) dt = 0.1;     // clamp after a stall
            _last = now;
            Advance(dt);
            InvalidateVisual();
        }

        // ---- particle layout ------------------------------------------------
        private void BuildParticles()
        {
            for (int i = 0; i < ParticleCount; i++)
            {
                ref Particle p = ref _p[i];
                // ~60% live on dendrite filaments, the rest are core/halo scatter.
                if (i % 5 < 3)
                {
                    int b = i % BranchCount;
                    float along = (float)Math.Pow(_rnd.NextDouble(), 0.6); // denser near core
                    float spread = 0.10f * (float)(_rnd.NextDouble() - 0.5);
                    p.HomeA = (float)(b * Math.PI * 2 / BranchCount) + spread + along * 0.5f;
                    p.HomeR = 0.12f + along * 0.82f;
                    p.Branch = b;
                    p.Bright = 0.45f + 0.55f * (1f - along);
                }
                else
                {
                    p.HomeA = (float)(_rnd.NextDouble() * Math.PI * 2);
                    p.HomeR = (float)Math.Pow(_rnd.NextDouble(), 0.5) * 0.95f;
                    p.Branch = -1;
                    p.Bright = 0.30f + 0.50f * (float)_rnd.NextDouble();
                }
                p.Phase = (float)(_rnd.NextDouble() * Math.PI * 2);
                p.Size = 0.6f + 0.9f * (float)_rnd.NextDouble();

                // start where home is
                p.Cx = p.HomeR * (float)Math.Cos(p.HomeA);
                p.Cy = p.HomeR * (float)Math.Sin(p.HomeA);

                AssignFace(ref p, i);
            }
        }

        // Decide which particles build which facial feature, and where they land.
        private void AssignFace(ref Particle p, int i)
        {
            double r = _rnd.NextDouble();
            float jx = (float)(_rnd.NextDouble() - 0.5);
            float jy = (float)(_rnd.NextDouble() - 0.5);
            if (r < 0.12)
            {
                p.Feat = Feature.EyeL;
                p.FaceX = -0.34f + 0.13f * jx;
                p.FaceY = -0.26f + 0.10f * jy;
            }
            else if (r < 0.24)
            {
                p.Feat = Feature.EyeR;
                p.FaceX = 0.34f + 0.13f * jx;
                p.FaceY = -0.26f + 0.10f * jy;
            }
            else if (r < 0.30)
            {
                p.Feat = Feature.Brow;
                p.FaceX = (i % 2 == 0 ? -0.34f : 0.34f) + 0.20f * jx;
                p.FaceY = -0.44f + 0.05f * jy;
            }
            else if (r < 0.50)
            {
                // mouth: split into upper and lower lip rows (lip-sync target)
                bool upper = (i % 2 == 0);
                p.Feat = upper ? Feature.LipUpper : Feature.LipLower;
                p.FaceX = 0.0f + 0.40f * (float)(_rnd.NextDouble() - 0.5) * 2f;
                p.FaceY = 0.34f + 0.04f * jy;   // baseline; opening applied per-frame
            }
            else
            {
                p.Feat = Feature.Halo;
                float a = (float)(_rnd.NextDouble() * Math.PI * 2);
                float rr = 0.55f + 0.45f * (float)_rnd.NextDouble();
                p.FaceX = rr * (float)Math.Cos(a);
                p.FaceY = rr * (float)Math.Sin(a);
            }
        }

        // ---- per-frame animation -------------------------------------------
        private void Advance(double dt)
        {
            _t += dt;
            _ring0 += (float)(dt * 0.16);
            _ring1 -= (float)(dt * 0.10);
            _ring2 += (float)(dt * 0.26);

            // ease faceAmount toward 1 when speaking, 0 otherwise
            float faceTarget = (Mode == OrbMode.Speaking) ? 1f : 0f;
            _faceAmount += (faceTarget - _faceAmount) * (float)Math.Min(1.0, dt * 3.5);

            // glitch envelope: a low ambient shimmer + bursts; much hotter while speaking.
            float baseGlitch = Mode == OrbMode.Speaking ? 0.35f : (Mode == OrbMode.Listening ? 0.12f : 0.05f);
            if (_t >= _nextBurst)
            {
                _glitch = Math.Max(_glitch, Mode == OrbMode.Speaking ? 0.9f : 0.5f);
                _nextBurst = _t + 0.25 + _rnd.NextDouble() * (Mode == OrbMode.Speaking ? 0.9 : 2.6);
            }
            _glitch += (baseGlitch - _glitch) * (float)Math.Min(1.0, dt * 4.0);

            float mic = Clamp01(MicLevel);
            float listen = Mode == OrbMode.Listening ? mic : 0f;
            float open = MouthMaxOpen * (0.25f + 0.75f * Clamp01(SpeechLevel)) * _faceAmount;
            float lerp = _faceAmount > 0.2f ? LerpFace : LerpIdle;

            for (int i = 0; i < ParticleCount; i++)
            {
                ref Particle p = ref _p[i];

                // ambient target = home + slow organic drift
                float drift = 0.018f * (float)Math.Sin(_t * 0.8 + p.Phase);
                float a = p.HomeA + 0.05f * (float)Math.Sin(_t * 0.3 + p.Phase);
                float rad = p.HomeR + drift;
                // listening pushes everything outward and adds a shell pulse
                rad *= 1f + ListenPush * listen * (0.6f + 0.4f * (float)Math.Sin(_t * 9 + p.Phase));
                float ax = rad * (float)Math.Cos(a);
                float ay = rad * (float)Math.Sin(a);

                // face target (with live mouth opening on the lower lip)
                float fx = p.FaceX, fy = p.FaceY;
                if (p.Feat == Feature.LipLower) fy += open;
                else if (p.Feat == Feature.LipUpper) fy -= open * 0.25f;

                float tx = Lerp(ax, fx, _faceAmount);
                float ty = Lerp(ay, fy, _faceAmount);

                p.Cx += (tx - p.Cx) * lerp;
                p.Cy += (ty - p.Cy) * lerp;
            }
        }

        // ---- rendering ------------------------------------------------------
        protected override void OnPaintSurface(SKPaintSurfaceEventArgs e)
        {
            var canvas = e.Surface.Canvas;
            canvas.Clear(SKColors.Transparent);

            int w = e.Info.Width, h = e.Info.Height;
            if (w <= 0 || h <= 0) return;
            EnsureScene(w, h);
            EnsureGlow();

            float cx = w * 0.5f, cy = h * 0.5f;
            float R = Math.Min(w, h) * 0.42f;

            // draw the orb into the offscreen scene
            var s = _scene!.Canvas;
            s.Clear(SKColors.Transparent);
            DrawRings(s, cx, cy, R);
            DrawParticles(s, cx, cy, R);
            _scene.Canvas.Flush();

            using var img = _scene.Snapshot();
            CompositeWithGlitch(canvas, img, w, h);
            DrawScanlines(canvas, w, h);
        }

        private void DrawParticles(SKCanvas s, float cx, float cy, float R)
        {
            if (_glow == null) return;
            using var paint = new SKPaint { BlendMode = SKBlendMode.Plus, FilterQuality = SKFilterQuality.Low };
            float mic = Clamp01(MicLevel);
            float boost = 1f + (Mode == OrbMode.Listening ? mic * 0.8f : 0f) + (Mode == OrbMode.Speaking ? 0.2f : 0f);

            // central bloom: a soft cyan core that breathes (and swells with audio) so the
            // cloud reads as a bright neural mass, not scattered dots (matches the brief).
            float breathe = 0.85f + 0.15f * (float)Math.Sin(_t * 1.1);
            float swell = 1f + 0.5f * (Mode == OrbMode.Listening ? mic : (Mode == OrbMode.Speaking ? Clamp01(SpeechLevel) : 0f));
            float bloomR = R * 0.62f * breathe * swell;
            using (var bp = new SKPaint { BlendMode = SKBlendMode.Plus })
            {
                bp.ColorFilter = SKColorFilter.CreateBlendMode(new SKColor(0x2A, 0xC8, 0xFF, 70), SKBlendMode.Modulate);
                s.DrawImage(_glow, new SKRect(cx - bloomR, cy - bloomR, cx + bloomR, cy + bloomR), bp);
                float coreR = R * 0.26f * breathe;
                bp.ColorFilter = SKColorFilter.CreateBlendMode(new SKColor(0x9A, 0xF2, 0xFF, 90), SKBlendMode.Modulate);
                s.DrawImage(_glow, new SKRect(cx - coreR, cy - coreR, cx + coreR, cy + coreR), bp);
            }

            for (int i = 0; i < ParticleCount; i++)
            {
                ref Particle p = ref _p[i];
                float x = cx + p.Cx * R;
                float y = cy + p.Cy * R;
                float tw = (0.8f - Math.Min(0.75f, (float)Math.Sqrt(p.Cx * p.Cx + p.Cy * p.Cy)) * 0.5f);
                float b = Clamp01(p.Bright * boost * (0.78f + 0.28f * (float)Math.Sin(_t * 4 + p.Phase)));
                float sz = (2.8f + p.Size * 4.0f) * (0.85f + 0.45f * b);

                // cyan core; brighter particles trend toward near-white
                byte g = (byte)(185 + 70 * b);
                var col = new SKColor(
                    (byte)(55 + 165 * b),   // a little R so hot points whiten
                    g,
                    255,
                    (byte)(70 + 185 * b * tw));
                paint.ColorFilter = SKColorFilter.CreateBlendMode(col, SKBlendMode.Modulate);
                float half = sz;
                s.DrawImage(_glow, new SKRect(x - half, y - half, x + half, y + half), paint);
            }
            paint.ColorFilter = null;
        }

        private void DrawRings(SKCanvas s, float cx, float cy, float R)
        {
            // three reticle rings: a wide soft glow stroke + a crisp dashed stroke + ticks.
            DrawRing(s, cx, cy, R * 1.06f, _ring0, 56, 0.55f);
            DrawRing(s, cx, cy, R * 0.86f, _ring1, 40, 0.40f);
            DrawRing(s, cx, cy, R * 0.66f, _ring2, 28, 0.30f);
        }

        private void DrawRing(SKCanvas s, float cx, float cy, float r, float rot, int ticks, float alpha)
        {
            byte a = (byte)(alpha * 255);
            var glow = new SKColor(0x22, 0xE8, 0xFF, (byte)(a * 0.5f));
            var line = new SKColor(0x8C, 0xF6, 0xFF, a);

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

        // Composite the rendered orb onto the screen. When glitching, shift horizontal
        // bands and split the cyan/red channels - the datamosh look that hides the face's
        // rough edges while the lips stay readable.
        private void CompositeWithGlitch(SKCanvas dst, SKImage img, int w, int h)
        {
            if (_glitch < 0.04f)
            {
                dst.DrawImage(img, 0, 0);
                return;
            }
            int bands = 14 + (int)(_glitch * 18);
            float bh = (float)h / bands;
            using var p = new SKPaint { FilterQuality = SKFilterQuality.None };
            using var rp = new SKPaint { BlendMode = SKBlendMode.Plus,
                ColorFilter = SKColorFilter.CreateBlendMode(new SKColor(0xFF, 0x33, 0x66, 200), SKBlendMode.Modulate) };
            for (int i = 0; i < bands; i++)
            {
                float y0 = i * bh;
                var src = new SKRect(0, y0, w, y0 + bh + 1);
                float shift = 0;
                if (_rnd.NextDouble() < 0.35 * _glitch)
                    shift = (float)((_rnd.NextDouble() - 0.5) * 2 * (8 + 40 * _glitch));
                var dstr = new SKRect(shift, y0, w + shift, y0 + bh + 1);
                dst.DrawImage(img, src, dstr, p);
                if (shift != 0 && _rnd.NextDouble() < 0.6)
                {
                    var dr2 = new SKRect(shift + 6 * _glitch, y0, w + shift + 6 * _glitch, y0 + bh + 1);
                    dst.DrawImage(img, src, dr2, rp);   // accent-channel tear
                }
            }
        }

        private void DrawScanlines(SKCanvas c, int w, int h)
        {
            using var p = new SKPaint { Color = new SKColor(0, 0, 0, 60), StrokeWidth = 1 };
            for (int y = 0; y < h; y += 3) c.DrawLine(0, y, w, y, p);
            // faint moving grain
            if (_glitch > 0.1f)
            {
                using var gp = new SKPaint { Color = new SKColor(0x22, 0xE8, 0xFF, (byte)(40 * _glitch)) };
                int dots = (int)(_glitch * 220);
                for (int i = 0; i < dots; i++)
                    c.DrawRect((float)(_rnd.NextDouble() * w), (float)(_rnd.NextDouble() * h), 2, 2, gp);
            }
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

        private void EnsureScene(int w, int h)
        {
            if (_scene != null && _sceneW == w && _sceneH == h) return;
            _scene?.Dispose();
            _scene = SKSurface.Create(new SKImageInfo(w, h, SKColorType.Rgba8888, SKAlphaType.Premul));
            _sceneW = w; _sceneH = h;
        }

        private static float Clamp01(float v) => v < 0 ? 0 : (v > 1 ? 1 : v);
        private static float Lerp(float a, float b, float t) => a + (b - a) * t;
    }
}
