using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;

namespace OrbEngine
{
    internal enum Region : byte
    {
        Neutral = 0, Mouth, Jaw, Nose, CheekL, CheekR, EyeL, EyeR, BrowL, BrowR, Forehead
    }

    // MediaPipe canonical face model (Assets\canonical_face_model.obj, Apache-2.0),
    // normalized to unit space (y DOWN, +z toward viewer, face height = H), with
    // region tags and precomputed procedural blendshape deltas (spec 2.2/2.3) for
    // the 468 mesh vertices and 4096 area-weighted surface samples.
    // Built once, shared read-only by every OrbSim.
    internal sealed class FaceMesh
    {
        public const int SampleCount = 4096;
        public const int Channels = 8;
        public const int ChJawOpen = 0, ChMouthClose = 1, ChFunnel = 2, ChPucker = 3,
                         ChSmile = 4, ChBlinkL = 5, ChBlinkR = 6, ChBrowUp = 7;
        public const float H = 1.15f;
        public const int RegionCount = 11;

        public int VertCount, TriCount;
        public float[] VX = null!, VY = null!, VZ = null!;
        public int[] TA = null!, TB = null!, TC = null!;
        public Region[] VReg = null!;
        public float[] VDelta = null!;                       // [vert * Channels * 3]
        public float[] SX = null!, SY = null!, SZ = null!;   // sample base positions
        public float[] SNX = null!, SNY = null!, SNZ = null!;// sample triangle normals
        public Region[] SReg = null!;
        public float[] SDelta = null!;                       // [sample * Channels * 3]
        public int[][] RegionSamples = null!;                // sample indices per Region
        public float EyeLX, EyeLY, EyeLZ, EyeRX, EyeRY, EyeRZ, EyeRadius;

        private static readonly Lazy<FaceMesh> Inst = new(() => new FaceMesh());
        public static FaceMesh Shared => Inst.Value;

        private float _chinX, _chinY, _chinZ;
        private float _lipMidX, _lipMidY, _lipMidZ, _lipUpperY;
        private float _cornLX, _cornLY, _cornLZ, _cornRX, _cornRY, _cornRZ;
        private float _eyeLcX, _eyeLcY, _eyeLcZ, _eyeRcX, _eyeRcY, _eyeRcZ; // outer eye corners
        private float _noseX, _noseY, _noseZ, _bridgeX, _bridgeY, _bridgeZ;
        private float _eyeLineY, _browTopY;

        private FaceMesh()
        {
            LoadObj();
            Normalize();
            ClassifyVertices();
            ComputeVertexDeltas();
            BuildSamples();
        }

        private void LoadObj()
        {
            using Stream s = typeof(FaceMesh).Assembly
                .GetManifestResourceStream("OrbEngine.Assets.canonical_face_model.obj")
                ?? throw new InvalidOperationException("embedded canonical_face_model.obj missing");
            using var rd = new StreamReader(s);
            var vx = new List<float>(500); var vy = new List<float>(500); var vz = new List<float>(500);
            var ta = new List<int>(1000); var tb = new List<int>(1000); var tc = new List<int>(1000);
            string? line;
            while ((line = rd.ReadLine()) != null)
            {
                if (line.StartsWith("v ", StringComparison.Ordinal))
                {
                    var p = line.Split(' ', StringSplitOptions.RemoveEmptyEntries);
                    vx.Add(float.Parse(p[1], CultureInfo.InvariantCulture));
                    vy.Add(float.Parse(p[2], CultureInfo.InvariantCulture));
                    vz.Add(float.Parse(p[3], CultureInfo.InvariantCulture));
                }
                else if (line.StartsWith("f ", StringComparison.Ordinal))
                {
                    var p = line.Split(' ', StringSplitOptions.RemoveEmptyEntries);
                    if (p.Length != 4) throw new InvalidOperationException("non-triangle face: " + line);
                    ta.Add(FaceIndex(p[1])); tb.Add(FaceIndex(p[2])); tc.Add(FaceIndex(p[3]));
                }
            }
            VertCount = vx.Count; TriCount = ta.Count;
            if (VertCount < 400 || TriCount < 800)
                throw new InvalidOperationException($"unexpected mesh size {VertCount}v/{TriCount}t");
            VX = vx.ToArray(); VY = vy.ToArray(); VZ = vz.ToArray();
            TA = ta.ToArray(); TB = tb.ToArray(); TC = tc.ToArray();

            static int FaceIndex(string tok)
            {
                int slash = tok.IndexOf('/');
                return int.Parse(slash >= 0 ? tok[..slash] : tok, CultureInfo.InvariantCulture) - 1;
            }
        }

        // Center at mid-face, uniform-scale so forehead-top -> chin = H, y flipped DOWN.
        // Anchor indices are MediaPipe canonical landmarks, verified geometrically per
        // spec 2.2; a failed check re-derives the anchor from geometry.
        private void Normalize()
        {
            for (int i = 0; i < VertCount; i++) VY[i] = -VY[i];   // OBJ +y up -> unit y down

            float minX = float.MaxValue, maxX = float.MinValue, minY = float.MaxValue,
                  maxY = float.MinValue, minZ = float.MaxValue, maxZ = float.MinValue;
            int argMaxY = 0, argMinY = 0, argMaxZ = 0;
            for (int i = 0; i < VertCount; i++)
            {
                if (VX[i] < minX) minX = VX[i];
                if (VX[i] > maxX) maxX = VX[i];
                if (VY[i] < minY) { minY = VY[i]; argMinY = i; }
                if (VY[i] > maxY) { maxY = VY[i]; argMaxY = i; }
                if (VZ[i] < minZ) minZ = VZ[i];
                if (VZ[i] > maxZ) { maxZ = VZ[i]; argMaxZ = i; }
            }

            int chin = 152, forehead = 10, nose = 1;
            float rangeY = maxY - minY;
            if (VY[chin] < maxY - 0.02f * rangeY) chin = argMaxY;         // chin = max y (y down)
            if (VY[forehead] > minY + 0.02f * rangeY) forehead = argMinY; // forehead top = min y
            int[] anchorSet = { 1, 13, 14, 61, 291, 152, 10, 33, 263, 168 };
            float bestZ = float.MinValue;
            foreach (int a in anchorSet) if (VZ[a] > bestZ) bestZ = VZ[a];
            if (VZ[nose] < bestZ - 1e-4f) nose = argMaxZ;                 // nose tip = max z among anchors

            float scale = H / (VY[chin] - VY[forehead]);
            float cx = 0.5f * (minX + maxX);
            float cy = 0.5f * (VY[chin] + VY[forehead]);
            float cz = 0.5f * (minZ + maxZ);
            for (int i = 0; i < VertCount; i++)
            {
                VX[i] = (VX[i] - cx) * scale;
                VY[i] = (VY[i] - cy) * scale;
                VZ[i] = (VZ[i] - cz) * scale;
            }

            _chinX = VX[chin]; _chinY = VY[chin]; _chinZ = VZ[chin];
            _noseX = VX[nose]; _noseY = VY[nose]; _noseZ = VZ[nose];
            _bridgeX = VX[168]; _bridgeY = VY[168]; _bridgeZ = VZ[168];
            _lipUpperY = VY[13];
            _lipMidX = 0.5f * (VX[13] + VX[14]);
            _lipMidY = 0.5f * (VY[13] + VY[14]);
            _lipMidZ = 0.5f * (VZ[13] + VZ[14]);

            int cornA = 61, cornB = 291;
            if (VX[cornA] > VX[cornB]) (cornA, cornB) = (cornB, cornA);   // L = screen left (x < 0)
            _cornLX = VX[cornA]; _cornLY = VY[cornA]; _cornLZ = VZ[cornA];
            _cornRX = VX[cornB]; _cornRY = VY[cornB]; _cornRZ = VZ[cornB];

            int eyeA = 33, eyeB = 263;
            if (VX[eyeA] > VX[eyeB]) (eyeA, eyeB) = (eyeB, eyeA);
            _eyeLcX = VX[eyeA]; _eyeLcY = VY[eyeA]; _eyeLcZ = VZ[eyeA];
            _eyeRcX = VX[eyeB]; _eyeRcY = VY[eyeB]; _eyeRcZ = VZ[eyeB];
            _eyeLineY = 0.5f * (_eyeLcY + _eyeRcY);
            _browTopY = _eyeLineY - 0.22f * H;
        }

        // Region bands per spec 2.2 (distances relative to H). First match wins.
        private Region Classify(float x, float y, float z)
        {
            if (Dist(x, y, z, _lipMidX, _lipMidY, _lipMidZ) < 0.16f * H) return Region.Mouth;
            if (y > _lipMidY && Dist(x, y, z, _chinX, _chinY, _chinZ) < 0.30f * H) return Region.Jaw;
            if (Dist(x, y, z, _eyeLcX, _eyeLcY, _eyeLcZ) < 0.13f * H) return Region.EyeL;
            if (Dist(x, y, z, _eyeRcX, _eyeRcY, _eyeRcZ) < 0.13f * H) return Region.EyeR;
            if (y < _eyeLineY - 0.02f * H && y > _browTopY)   // band just above the eyes
            {
                if (MathF.Abs(x - 0.75f * _eyeLcX) < 0.17f * H) return Region.BrowL;
                if (MathF.Abs(x - 0.75f * _eyeRcX) < 0.17f * H) return Region.BrowR;
            }
            if (Dist(x, y, z, _noseX, _noseY, _noseZ) < 0.18f * H
             || Dist(x, y, z, _bridgeX, _bridgeY, _bridgeZ) < 0.10f * H) return Region.Nose;
            if (y < _browTopY) return Region.Forehead;
            if (y > _eyeLineY) return x < 0f ? Region.CheekL : Region.CheekR;
            return Region.Neutral;   // temple band
        }

        private void ClassifyVertices()
        {
            VReg = new Region[VertCount];
            float lx = 0, ly = 0, lz = 0, rx = 0, ry = 0, rz = 0;
            int ln = 0, rn = 0;
            for (int i = 0; i < VertCount; i++)
            {
                VReg[i] = Classify(VX[i], VY[i], VZ[i]);
                if (VReg[i] == Region.EyeL) { lx += VX[i]; ly += VY[i]; lz += VZ[i]; ln++; }
                else if (VReg[i] == Region.EyeR) { rx += VX[i]; ry += VY[i]; rz += VZ[i]; rn++; }
            }
            if (ln > 0) { EyeLX = lx / ln; EyeLY = ly / ln; EyeLZ = lz / ln; }
            else { EyeLX = _eyeLcX; EyeLY = _eyeLcY; EyeLZ = _eyeLcZ; }
            if (rn > 0) { EyeRX = rx / rn; EyeRY = ry / rn; EyeRZ = rz / rn; }
            else { EyeRX = _eyeRcX; EyeRY = _eyeRcY; EyeRZ = _eyeRcZ; }

            float dSum = 0; int dN = 0;
            for (int i = 0; i < VertCount; i++)
            {
                if (VReg[i] == Region.EyeL) { dSum += Dist(VX[i], VY[i], VZ[i], EyeLX, EyeLY, EyeLZ); dN++; }
                else if (VReg[i] == Region.EyeR) { dSum += Dist(VX[i], VY[i], VZ[i], EyeRX, EyeRY, EyeRZ); dN++; }
            }
            EyeRadius = dN > 0 ? 1.5f * (dSum / dN) : 0.08f * H;
        }

        private void ComputeVertexDeltas()
        {
            VDelta = new float[VertCount * Channels * 3];
            for (int i = 0; i < VertCount; i++)
                AddDeltas(VX[i], VY[i], VZ[i], VReg[i], VDelta, i * Channels * 3);
        }

        // Procedural blendshape delta rules from spec 2.3; pos = base + sum(w_i * delta_i).
        private void AddDeltas(float x, float y, float z, Region reg, float[] dst, int off)
        {
            if (reg == Region.Jaw)
            {
                float d = Dist(x, y, z, _chinX, _chinY, _chinZ);
                float fall = 1f - Math.Clamp(d / (0.35f * H), 0f, 1f);
                dst[off + ChJawOpen * 3 + 1] = 0.12f * H * fall;
            }
            else if (reg == Region.Mouth)
            {
                if (y > _lipUpperY)   // lower-lip half (y down)
                {
                    dst[off + ChJawOpen * 3 + 1] = 0.10f * H;
                    dst[off + ChMouthClose * 3 + 1] = -0.06f * H;   // -0.6 x JawOpen lip delta
                }
                dst[off + ChFunnel * 3 + 0] = (_lipMidX - x) * 0.5f;
                dst[off + ChFunnel * 3 + 2] = 0.05f * H;
                dst[off + ChPucker * 3 + 0] = (_lipMidX - x) * 0.6f;
                dst[off + ChPucker * 3 + 1] = (_lipMidY - y) * 0.6f;
                dst[off + ChPucker * 3 + 2] = 0.03f * H;
            }

            float dl = Dist(x, y, z, _cornLX, _cornLY, _cornLZ);
            if (dl < 0.10f * H)
            {
                float f = 1f - dl / (0.10f * H);
                dst[off + ChSmile * 3 + 0] = -0.05f * H * f;   // outward on the left corner
                dst[off + ChSmile * 3 + 1] = -0.04f * H * f;   // up
            }
            else
            {
                float dr = Dist(x, y, z, _cornRX, _cornRY, _cornRZ);
                if (dr < 0.10f * H)
                {
                    float f = 1f - dr / (0.10f * H);
                    dst[off + ChSmile * 3 + 0] = 0.05f * H * f;
                    dst[off + ChSmile * 3 + 1] = -0.04f * H * f;
                }
            }

            if (reg == Region.EyeL) dst[off + ChBlinkL * 3 + 1] = EyeLY - y;      // y toward eye center
            else if (reg == Region.EyeR) dst[off + ChBlinkR * 3 + 1] = EyeRY - y;
            else if (reg == Region.BrowL || reg == Region.BrowR)
            {
                float outerX = reg == Region.BrowL ? _eyeLcX : _eyeRcX;
                if (MathF.Abs(x) < 0.6f * MathF.Abs(outerX))                       // inner half
                    dst[off + ChBrowUp * 3 + 1] = -0.05f * H;
            }
        }

        // 4096 area-weighted random barycentric samples over all triangles (seeded RNG,
        // fixed seed: the mesh is shared state, sim determinism comes from OrbSim's seed).
        private void BuildSamples()
        {
            var cum = new double[TriCount];
            var tnx = new float[TriCount]; var tny = new float[TriCount]; var tnz = new float[TriCount];
            double total = 0;
            for (int t = 0; t < TriCount; t++)
            {
                int a = TA[t], b = TB[t], c = TC[t];
                float ux = VX[b] - VX[a], uy = VY[b] - VY[a], uz = VZ[b] - VZ[a];
                float wx = VX[c] - VX[a], wy = VY[c] - VY[a], wz = VZ[c] - VZ[a];
                float nx = uy * wz - uz * wy, ny = uz * wx - ux * wz, nz = ux * wy - uy * wx;
                float len = MathF.Sqrt(nx * nx + ny * ny + nz * nz);
                if (len > 1e-12f)
                {
                    float inv = 1f / len;
                    if (nz < 0f) inv = -inv;   // orient toward the viewer-ish hemisphere
                    tnx[t] = nx * inv; tny[t] = ny * inv; tnz[t] = nz * inv;
                }
                total += 0.5 * len;
                cum[t] = total;
            }

            SX = new float[SampleCount]; SY = new float[SampleCount]; SZ = new float[SampleCount];
            SNX = new float[SampleCount]; SNY = new float[SampleCount]; SNZ = new float[SampleCount];
            SReg = new Region[SampleCount];
            SDelta = new float[SampleCount * Channels * 3];
            var lists = new List<int>[RegionCount];
            for (int r = 0; r < RegionCount; r++) lists[r] = new List<int>();

            var rnd = new Random(0x0FACE5);
            for (int s = 0; s < SampleCount; s++)
            {
                double pick = rnd.NextDouble() * total;
                int t = Array.BinarySearch(cum, pick);
                if (t < 0) t = ~t;
                if (t >= TriCount) t = TriCount - 1;
                float r1 = MathF.Sqrt((float)rnd.NextDouble());
                float r2 = (float)rnd.NextDouble();
                float w0 = 1f - r1, w1 = r1 * (1f - r2), w2 = r1 * r2;
                int a = TA[t], b = TB[t], c = TC[t];
                float x = w0 * VX[a] + w1 * VX[b] + w2 * VX[c];
                float y = w0 * VY[a] + w1 * VY[b] + w2 * VY[c];
                float z = w0 * VZ[a] + w1 * VZ[b] + w2 * VZ[c];
                SX[s] = x; SY[s] = y; SZ[s] = z;
                SNX[s] = tnx[t]; SNY[s] = tny[t]; SNZ[s] = tnz[t];
                Region reg = Classify(x, y, z);
                SReg[s] = reg;
                AddDeltas(x, y, z, reg, SDelta, s * Channels * 3);
                lists[(int)reg].Add(s);
            }
            RegionSamples = new int[RegionCount][];
            for (int r = 0; r < RegionCount; r++) RegionSamples[r] = lists[r].ToArray();
        }

        private static float Dist(float x, float y, float z, float ax, float ay, float az)
        {
            float dx = x - ax, dy = y - ay, dz = z - az;
            return MathF.Sqrt(dx * dx + dy * dy + dz * dz);
        }
    }
}
