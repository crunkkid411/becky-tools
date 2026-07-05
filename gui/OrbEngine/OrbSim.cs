using System;

namespace OrbEngine
{
    // Renderer-agnostic orb simulation (spec section 2). The v1 dendrite/halo particle
    // cloud, extruded to a thin z shell; while Speaking, particles are recruited onto
    // blendshape-deformed samples of a hidden face mesh, mouth first (spec 2.5).
    // Deterministic: one seeded RNG, time only advances via dt (no DateTime/TickCount).
    // No per-frame allocation: fixed struct-of-arrays + preallocated output buffers.
    public sealed class OrbSim
    {
        private const byte RFree = 0, RMigrating = 1, RAttached = 2, RReleasing = 3;
        private const int BranchCount = 26;      // v1 dendrite filaments
        private const float Bound = 1.55f;       // hard safety clamp (< test bound 1.6)
        private const float TwoPi = 6.2831853f;

        private readonly int _n;
        private readonly Random _rnd;
        private readonly FaceMesh _mesh = FaceMesh.Shared;

        // particles (struct-of-arrays)
        private readonly float[] _px, _py, _pz, _prevX, _prevY, _vx, _vy, _vz;
        private readonly float[] _homeR, _homeA, _homeZ, _phase, _bright, _size, _timer, _shimmer;
        private readonly int[] _sample;
        private readonly byte[] _role;
        private int _seeking;                    // Migrating + Attached

        private readonly ParticleView[] _viewBuf;
        private readonly MeshHintVertex[] _meshBuf;
        private int _meshCount;
        private readonly EyeGlow[] _eyeBuf = new EyeGlow[2];
        private int _eyeCount;

        private readonly float[] _sampCurX, _sampCurY, _sampCurZ;   // deformed + yawed
        private readonly float[] _vertCurX, _vertCurY, _vertCurZ;
        private readonly float[] _w = new float[FaceMesh.Channels];
        private readonly float[] _wTarget = new float[FaceMesh.Channels];
        private readonly float[] _gate = new float[6];              // Mouth Jaw Cheeks Nose Brows Forehead
        private readonly double[] _cum = new double[FaceMesh.RegionCount];

        private double _t;
        private OrbMode _mode = OrbMode.Idle;
        private double _speakStart = -1e9, _speakEnd = -1e9;
        private float _rippleT, _speech, _faceVis, _inward, _swirl;
        private float _yawCos = 1f, _yawSin;
        private float _reveal, _revealDur = 1f, _revealHold;
        private int _revealPhase;                // 0 off, 1 ramp, 2 hold, 3 decay
        private float _blinkT, _blinkNext = -1f;

        // emergence choreography (spec 2.5): start, ramp, cap per region gate
        private static readonly float[] GateStart = { 0f, 0.25f, 0.55f, 0.75f, 0.90f, 1.00f };
        private static readonly float[] GateRamp = { 0.35f, 0.50f, 0.60f, 0.60f, 0.80f, 0.80f };
        private static readonly float[] GateCap = { 1f, 1f, 0.9f, 0.8f, 0.35f, 0.25f };
        // dissolve in REVERSE order (forehead first), mouth lingers +0.3 s
        private static readonly float[] GateReleaseDelay = { 0.30f, 0.24f, 0.18f, 0.12f, 0.06f, 0f };
        // recruitment weight by Region (spec 2.4): Mouth 3, Jaw/Cheek/Nose 1, Brow/Forehead 0.5, Eye 0
        private static readonly float[] RecruitW = { 0f, 3f, 1f, 1f, 1f, 1f, 0f, 0f, 0.5f, 0.5f, 0.5f };

        public OrbSim(int particleCount = 2600, int seed = 0x0BECCA)
        {
            if (particleCount < 1) throw new ArgumentOutOfRangeException(nameof(particleCount));
            _n = particleCount;
            _rnd = new Random(seed);

            _px = new float[_n]; _py = new float[_n]; _pz = new float[_n];
            _prevX = new float[_n]; _prevY = new float[_n];
            _vx = new float[_n]; _vy = new float[_n]; _vz = new float[_n];
            _homeR = new float[_n]; _homeA = new float[_n]; _homeZ = new float[_n];
            _phase = new float[_n]; _bright = new float[_n]; _size = new float[_n];
            _timer = new float[_n]; _shimmer = new float[_n];
            _sample = new int[_n]; _role = new byte[_n];
            _viewBuf = new ParticleView[_n];
            _meshBuf = new MeshHintVertex[_mesh.TriCount * 3];

            int sc = FaceMesh.SampleCount;
            _sampCurX = new float[sc]; _sampCurY = new float[sc]; _sampCurZ = new float[sc];
            Array.Copy(_mesh.SX, _sampCurX, sc); Array.Copy(_mesh.SY, _sampCurY, sc); Array.Copy(_mesh.SZ, _sampCurZ, sc);
            int vc = _mesh.VertCount;
            _vertCurX = new float[vc]; _vertCurY = new float[vc]; _vertCurZ = new float[vc];
            Array.Copy(_mesh.VX, _vertCurX, vc); Array.Copy(_mesh.VY, _vertCurY, vc); Array.Copy(_mesh.VZ, _vertCurZ, vc);

            BuildHomes();
        }

        public void Update(in OrbInput input, float dt)
        {
            if (!(dt > 0f)) return;              // also rejects NaN
            if (dt > 0.1f) dt = 0.1f;            // clamp after a stall (v1 behavior)
            _t += dt;

            _rippleT = MathF.Max(0f, _rippleT - dt / 0.8f);
            if (input.Mode != _mode)
            {
                _rippleT = 1f;                   // ripple on ANY mode change (spec D9)
                if (input.Mode == OrbMode.Speaking) _speakStart = _t;
                if (_mode == OrbMode.Speaking) _speakEnd = _t;
                _mode = input.Mode;
            }

            _speech += (Clamp01(input.SpeechLevel) - _speech) * Rate(dt, 6f);
            float fvTarget = _mode == OrbMode.Speaking ? Math.Clamp(0.18f + 0.5f * _speech, 0f, 0.6f) : 0f;
            _faceVis += (fvTarget - _faceVis) * Rate(dt, 4f);
            _inward += ((_mode == OrbMode.Thinking ? 0.25f : 0f) - _inward) * Rate(dt, 3f);
            _swirl += dt * 4.8f * _inward;       // eased inward swirl while Thinking

            float yaw = 0.10472f * MathF.Sin((float)_t * (TwoPi / 9f));   // idle sway +/-6 deg, ~9 s
            _yawCos = MathF.Cos(yaw); _yawSin = MathF.Sin(yaw);

            UpdateGates(dt);
            UpdateReveal(in input, dt);
            UpdateWeights(in input, dt);

            bool faceActive = _seeking > 0 || _reveal > 0.001f || AnyGate();
            if (faceActive) DeformFace();

            UpdateParticles(in input, dt);
            EmitMesh(faceActive);
            EmitEyes();
        }

        public ReadOnlySpan<ParticleView> Particles => _viewBuf;
        public ReadOnlySpan<MeshHintVertex> MeshVertices => new ReadOnlySpan<MeshHintVertex>(_meshBuf, 0, _meshCount);
        public ReadOnlySpan<EyeGlow> Eyes => new ReadOnlySpan<EyeGlow>(_eyeBuf, 0, _eyeCount);
        public float RippleT => _rippleT;
        public float FaceVisibility => _faceVis;

        // ---- internals (test hooks via InternalsVisibleTo) ------------------
        internal float GateMouth => _gate[0];
        internal float GateJaw => _gate[1];
        internal float GateCheeks => _gate[2];
        internal float GateNose => _gate[3];
        internal float GateBrows => _gate[4];
        internal float GateForehead => _gate[5];
        internal float GateEyes => _reveal;
        internal float BudgetF => _mode == OrbMode.Speaking ? 0.35f + 0.25f * _faceVis : 0f;
        internal int SeekingCount => _seeking;
        internal int Count => _n;
        internal bool AllFreeOrReleasing()
        {
            for (int i = 0; i < _n; i++)
                if (_role[i] == RMigrating || _role[i] == RAttached) return false;
            return true;
        }

        // ---- setup -----------------------------------------------------------
        // v1 OrbControl.BuildParticles polar home layout, preserved (spec 2.4),
        // extruded to a thin z shell of +/-0.35.
        private void BuildHomes()
        {
            for (int i = 0; i < _n; i++)
            {
                float homeA, homeR, bright;
                if (i % 5 < 3)   // ~60% live on dendrite filaments, denser near core
                {
                    int b = i % BranchCount;
                    float along = MathF.Pow((float)_rnd.NextDouble(), 0.6f);
                    float spread = 0.10f * ((float)_rnd.NextDouble() - 0.5f);
                    homeA = b * TwoPi / BranchCount + spread + along * 0.5f;
                    homeR = 0.12f + along * 0.82f;
                    bright = 0.45f + 0.55f * (1f - along);
                }
                else             // core/halo scatter
                {
                    homeA = (float)_rnd.NextDouble() * TwoPi;
                    homeR = MathF.Pow((float)_rnd.NextDouble(), 0.5f) * 0.95f;
                    bright = 0.30f + 0.50f * (float)_rnd.NextDouble();
                }
                _homeA[i] = homeA; _homeR[i] = homeR; _bright[i] = bright;
                _phase[i] = (float)_rnd.NextDouble() * TwoPi;
                _size[i] = 0.6f + 0.9f * (float)_rnd.NextDouble();
                _homeZ[i] = ((float)_rnd.NextDouble() - 0.5f) * 0.7f;
                _px[i] = homeR * MathF.Cos(homeA);
                _py[i] = homeR * MathF.Sin(homeA);
                _pz[i] = _homeZ[i];
                _prevX[i] = _px[i]; _prevY[i] = _py[i];
            }
        }

        // ---- state -----------------------------------------------------------
        private void UpdateGates(float dt)
        {
            if (_mode == OrbMode.Speaking)
            {
                float e = (float)(_t - _speakStart);
                for (int i = 0; i < 6; i++)
                {
                    float rise = GateCap[i] * Smooth01((e - GateStart[i]) / GateRamp[i]);
                    if (rise > _gate[i]) _gate[i] = rise;   // monotonic while speaking, no snap on re-entry
                }
            }
            else
            {
                float e = (float)(_t - _speakEnd);
                for (int i = 0; i < 6; i++)
                    if (e > GateReleaseDelay[i])
                        _gate[i] = MathF.Max(0f, _gate[i] - dt * GateCap[i] / 0.8f);
            }
        }

        private void UpdateReveal(in OrbInput input, float dt)
        {
            if (input.EyeRevealPulse)
            {
                float dur = input.EyeRevealDuration;
                _revealDur = dur > 0.05f ? MathF.Min(dur, 5f) : 1f;   // NaN/junk -> default 1 s
                _revealPhase = 1;
            }
            switch (_revealPhase)
            {
                case 1:
                    _reveal += dt / 0.2f;
                    if (_reveal >= 1f) { _reveal = 1f; _revealPhase = 2; _revealHold = _revealDur; }
                    break;
                case 2:
                    _revealHold -= dt;
                    if (_revealHold <= 0f) _revealPhase = 3;
                    break;
                case 3:
                    _reveal -= dt / 0.4f;
                    if (_reveal <= 0f) { _reveal = 0f; _revealPhase = 0; }
                    break;
            }
        }

        private void UpdateWeights(in OrbInput input, float dt)
        {
            float blinkW = 0f;
            if (_reveal > 0.1f)   // auto-blink only while eyes are revealed (spec 2.3)
            {
                if (_blinkNext < 0f) _blinkNext = 2.5f + 2.5f * (float)_rnd.NextDouble();
                _blinkNext -= dt;
                if (_blinkNext <= 0f) { _blinkT = 0.18f; _blinkNext = 2.5f + 2.5f * (float)_rnd.NextDouble(); }
            }
            else _blinkNext = -1f;
            if (_blinkT > 0f)
            {
                _blinkT = MathF.Max(0f, _blinkT - dt);
                float p = 1f - _blinkT / 0.18f;
                blinkW = 1f - MathF.Abs(2f * p - 1f);   // quick close-open triangle
            }

            _wTarget[FaceMesh.ChJawOpen] = Clamp01(input.Jaw);
            _wTarget[FaceMesh.ChFunnel] = Clamp01(input.Funnel);
            _wTarget[FaceMesh.ChPucker] = Clamp01(input.Pucker);
            _wTarget[FaceMesh.ChSmile] = Clamp01(input.Smile);
            // ponytail: MouthClose deltas are precomputed but 2.3 maps no input to them in v2
            _wTarget[FaceMesh.ChMouthClose] = 0f;
            _wTarget[FaceMesh.ChBlinkL] = blinkW;
            _wTarget[FaceMesh.ChBlinkR] = blinkW;
            _wTarget[FaceMesh.ChBrowUp] = 0.6f * _reveal;
            float r = Rate(dt, 14f);
            for (int c = 0; c < FaceMesh.Channels; c++) _w[c] += (_wTarget[c] - _w[c]) * r;
        }

        private void DeformFace()
        {
            FaceMesh m = _mesh;
            for (int s = 0; s < FaceMesh.SampleCount; s++)
            {
                float x = m.SX[s], y = m.SY[s], z = m.SZ[s];
                int off = s * FaceMesh.Channels * 3;
                for (int c = 0; c < FaceMesh.Channels; c++)
                {
                    float w = _w[c];
                    if (w > 1e-4f)
                    {
                        int o = off + c * 3;
                        x += w * m.SDelta[o]; y += w * m.SDelta[o + 1]; z += w * m.SDelta[o + 2];
                    }
                }
                _sampCurX[s] = x * _yawCos + z * _yawSin;
                _sampCurY[s] = y;
                _sampCurZ[s] = z * _yawCos - x * _yawSin;
            }
            for (int v = 0; v < m.VertCount; v++)
            {
                float x = m.VX[v], y = m.VY[v], z = m.VZ[v];
                int off = v * FaceMesh.Channels * 3;
                for (int c = 0; c < FaceMesh.Channels; c++)
                {
                    float w = _w[c];
                    if (w > 1e-4f)
                    {
                        int o = off + c * 3;
                        x += w * m.VDelta[o]; y += w * m.VDelta[o + 1]; z += w * m.VDelta[o + 2];
                    }
                }
                _vertCurX[v] = x * _yawCos + z * _yawSin;
                _vertCurY[v] = y;
                _vertCurZ[v] = z * _yawCos - x * _yawSin;
            }
        }

        // ---- particles ---------------------------------------------------------
        private void UpdateParticles(in OrbInput input, float dt)
        {
            float mic = Clamp01(input.MicLevel);
            float listen = _mode == OrbMode.Listening ? mic : 0f;
            float f = _mode == OrbMode.Speaking ? 0.35f + 0.25f * _faceVis : 0f;
            int targetSeek = (int)(f * _n);

            if (_mode == OrbMode.Speaking && _seeking < targetSeek)
                Recruit(Math.Min(targetSeek - _seeking, Math.Max(1, (int)(_n * dt * 1.5f))));
            if (_seeking > targetSeek)
            {
                int excess = _seeking - targetSeek;
                for (int i = 0; i < _n && excess > 0; i++)
                    if (_role[i] == RAttached || _role[i] == RMigrating) { StartRelease(i); excess--; }
            }

            float tf = (float)_t;
            float freeLerp = Rate(dt, 3.7f);        // v1 LerpIdle 0.06/frame @ 60 fps
            float relDamp = 1f - Rate(dt, 1.5f);

            for (int i = 0; i < _n; i++)
            {
                _prevX[i] = _px[i]; _prevY[i] = _py[i];
                byte role = _role[i];

                if (role == RMigrating || role == RAttached)
                {
                    int si = _sample[i];
                    if (GateOf(_mesh.SReg[si]) < 0.02f) { StartRelease(i); role = RReleasing; }
                    else if (role == RAttached)
                    {
                        _timer[i] -= dt;            // attached shed after 1.5-4 s
                        if (_timer[i] <= 0f) { StartRelease(i); role = RReleasing; }
                    }
                }

                if (role == RFree)
                {
                    float ph = _phase[i];
                    float a = _homeA[i] + 0.05f * MathF.Sin(tf * 0.3f + ph) + _swirl;
                    float rad = _homeR[i] + 0.018f * MathF.Sin(tf * 0.8f + ph);
                    rad *= 1f + 0.55f * listen * (0.6f + 0.4f * MathF.Sin(tf * 9f + ph));   // v1 mic push
                    rad *= 1f - _inward;
                    float tx = rad * MathF.Cos(a), ty = rad * MathF.Sin(a);
                    float tz = _homeZ[i] + 0.05f * MathF.Sin(tf * 0.5f + ph);
                    _px[i] += (tx - _px[i]) * freeLerp;
                    _py[i] += (ty - _py[i]) * freeLerp;
                    _pz[i] += (tz - _pz[i]) * freeLerp;
                }
                else if (role == RMigrating)
                {
                    int si = _sample[i];
                    float ex = _sampCurX[si] - _px[i], ey = _sampCurY[si] - _py[i], ez = _sampCurZ[si] - _pz[i];
                    _vx[i] += (14f * ex - 8f * _vx[i]) * dt;   // seek spring, stiffness 14 damping 8
                    _vy[i] += (14f * ey - 8f * _vy[i]) * dt;
                    _vz[i] += (14f * ez - 8f * _vz[i]) * dt;
                    _px[i] += _vx[i] * dt; _py[i] += _vy[i] * dt; _pz[i] += _vz[i] * dt;
                    if (ex * ex + ey * ey + ez * ez < 0.0004f)
                    {
                        _role[i] = RAttached;
                        _timer[i] = 1.5f + 2.5f * (float)_rnd.NextDouble();
                        _shimmer[i] = 1f;           // convergence shimmer (spec D9)
                        _vx[i] = _vy[i] = _vz[i] = 0f;
                    }
                }
                else if (role == RAttached)
                {
                    int si = _sample[i];
                    float ph = _phase[i];
                    _px[i] = _sampCurX[si] + 0.008f * MathF.Sin(tf * 7f + ph);
                    _py[i] = _sampCurY[si] + 0.008f * MathF.Sin(tf * 6.3f + ph * 1.7f);
                    _pz[i] = _sampCurZ[si] + 0.008f * MathF.Sin(tf * 5.1f + ph * 2.3f);
                }
                else   // Releasing: drift outward 0.5-1.2 s, then Free
                {
                    _timer[i] -= dt;
                    _px[i] += _vx[i] * dt; _py[i] += _vy[i] * dt; _pz[i] += _vz[i] * dt;
                    _vx[i] *= relDamp; _vy[i] *= relDamp; _vz[i] *= relDamp;
                    if (_timer[i] <= 0f) { _role[i] = RFree; _vx[i] = _vy[i] = _vz[i] = 0f; }
                }

                if (_px[i] > Bound) _px[i] = Bound; else if (_px[i] < -Bound) _px[i] = -Bound;
                if (_py[i] > Bound) _py[i] = Bound; else if (_py[i] < -Bound) _py[i] = -Bound;
                if (_pz[i] > Bound) _pz[i] = Bound; else if (_pz[i] < -Bound) _pz[i] = -Bound;
                if (_shimmer[i] > 0f) _shimmer[i] = MathF.Max(0f, _shimmer[i] - dt / 0.35f);

                float b = _bright[i] * (0.78f + 0.28f * MathF.Sin(tf * 4f + _phase[i]));
                if (_role[i] == RAttached) b *= 1.15f;
                b = Clamp01(b + 0.6f * _shimmer[i]);
                _viewBuf[i] = new ParticleView
                {
                    X = _px[i], Y = _py[i], Z = _pz[i],
                    PrevX = _prevX[i], PrevY = _prevY[i],
                    Brightness = b, Size = _size[i],
                };
            }
        }

        // Promote Free particles; each picks a sample with probability
        // proportional to regionGate x regionWeight (spec 2.4).
        private void Recruit(int want)
        {
            double total = 0;
            for (int r = 0; r < FaceMesh.RegionCount; r++)
            {
                total += RecruitW[r] * GateOf((Region)r) * _mesh.RegionSamples[r].Length;
                _cum[r] = total;
            }
            if (total <= 1e-9) return;
            for (int k = 0; k < want; k++)
            {
                double pick = _rnd.NextDouble() * total;
                int reg = 0;
                while (reg < FaceMesh.RegionCount - 1 && pick > _cum[reg]) reg++;
                int[] list = _mesh.RegionSamples[reg];
                if (list.Length == 0) continue;
                int p = FindFree();
                if (p < 0) return;
                _role[p] = RMigrating;
                _sample[p] = list[_rnd.Next(list.Length)];
                _timer[p] = 0f;
                _seeking++;
            }
        }

        private int FindFree()
        {
            int start = _rnd.Next(_n);
            for (int s = 0; s < _n; s++)
            {
                int i = start + s;
                if (i >= _n) i -= _n;
                if (_role[i] == RFree) return i;
            }
            return -1;
        }

        private void StartRelease(int i)
        {
            _seeking--;
            _role[i] = RReleasing;
            _timer[i] = 0.5f + 0.7f * (float)_rnd.NextDouble();
            int si = _sample[i];
            _vx[i] = _mesh.SNX[si] * 0.4f;   // drift out along the surface normal
            _vy[i] = _mesh.SNY[si] * 0.4f;
            _vz[i] = _mesh.SNZ[si] * 0.4f;
        }

        // ---- outputs -----------------------------------------------------------
        private void EmitMesh(bool faceActive)
        {
            _meshCount = 0;
            if (!faceActive) return;
            FaceMesh m = _mesh;
            int k = 0;
            for (int t = 0; t < m.TriCount; t++)
            {
                int a = m.TA[t], b = m.TB[t], c = m.TC[t];
                float g = (GateOf(m.VReg[a]) + GateOf(m.VReg[b]) + GateOf(m.VReg[c])) * (1f / 3f);
                if (g <= 0.02f) continue;   // emergence cull (spec 2.6)
                float ax = _vertCurX[a], ay = _vertCurY[a], az = _vertCurZ[a];
                float bx = _vertCurX[b], by = _vertCurY[b], bz = _vertCurZ[b];
                float cx = _vertCurX[c], cy = _vertCurY[c], cz = _vertCurZ[c];
                float ux = bx - ax, uy = by - ay, uz = bz - az;
                float wx = cx - ax, wy = cy - ay, wz = cz - az;
                float nx = uy * wz - uz * wy, ny = uz * wx - ux * wz, nz = ux * wy - uy * wx;
                float len = MathF.Sqrt(nx * nx + ny * ny + nz * nz);
                if (len < 1e-9f) continue;
                float fres = 1f - MathF.Abs(nz) / len;   // rim lighting: edges glow
                fres *= fres;
                float alpha = g * fres * _faceVis * 0.18f;
                _meshBuf[k++] = new MeshHintVertex { X = ax, Y = ay, Alpha = alpha };
                _meshBuf[k++] = new MeshHintVertex { X = bx, Y = by, Alpha = alpha };
                _meshBuf[k++] = new MeshHintVertex { X = cx, Y = cy, Alpha = alpha };
            }
            _meshCount = k;
        }

        private void EmitEyes()
        {
            _eyeCount = 0;
            if (_reveal <= 0.01f) return;
            FaceMesh m = _mesh;
            float pulse = m.EyeRadius * (1.2f + 0.15f * MathF.Sin((float)_t * 3f));
            _eyeBuf[0] = new EyeGlow { X = m.EyeLX * _yawCos + m.EyeLZ * _yawSin, Y = m.EyeLY, Radius = pulse, Intensity = _reveal };
            _eyeBuf[1] = new EyeGlow { X = m.EyeRX * _yawCos + m.EyeRZ * _yawSin, Y = m.EyeRY, Radius = pulse, Intensity = _reveal };
            _eyeCount = 2;
        }

        // ---- helpers -------------------------------------------------------------
        private float GateOf(Region r) => r switch
        {
            Region.Mouth => _gate[0],
            Region.Jaw => _gate[1],
            Region.CheekL or Region.CheekR => _gate[2],
            Region.Nose => _gate[3],
            Region.BrowL or Region.BrowR => _gate[4],
            Region.Forehead => _gate[5],
            Region.EyeL or Region.EyeR => _reveal,   // eyes gate = EyeReveal only (spec 2.5)
            _ => 0f,
        };

        private bool AnyGate()
        {
            for (int i = 0; i < 6; i++) if (_gate[i] > 0.0005f) return true;
            return false;
        }

        private static float Rate(float dt, float k) => 1f - MathF.Exp(-k * dt);
        private static float Clamp01(float v) => v < 0f ? 0f : (v > 1f ? 1f : v);   // NaN -> 0
        private static float Smooth01(float x)
        {
            if (!(x > 0f)) return 0f;
            if (x >= 1f) return 1f;
            return x * x * (3f - 2f * x);
        }
    }
}
