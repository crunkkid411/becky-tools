using System;
using Xunit;

namespace OrbEngine.Tests
{
    // The six spec 2.7 cases. Deterministic, no rendering: fixed dt, seeded RNGs.
    public class OrbSimTests
    {
        private const float Dt = 1f / 60f;

        private static OrbInput In(OrbMode m, float speech = 0f, float mic = 0f)
            => new OrbInput { Mode = m, SpeechLevel = speech, MicLevel = mic };

        private static void Run(OrbSim sim, OrbInput inp, float seconds)
        {
            int frames = (int)MathF.Round(seconds / Dt);
            for (int i = 0; i < frames; i++) sim.Update(in inp, Dt);
        }

        [Fact]
        public void MouthGateOpensFirst()
        {
            var sim = new OrbSim();
            Run(sim, In(OrbMode.Speaking, 0.6f), 0.3f);
            Assert.True(sim.GateMouth > 0.5f, $"mouth={sim.GateMouth}");
            Assert.True(sim.GateJaw < 0.15f && sim.GateCheeks < 0.15f && sim.GateNose < 0.15f
                     && sim.GateBrows < 0.15f && sim.GateForehead < 0.15f && sim.GateEyes < 0.15f,
                $"jaw={sim.GateJaw} cheeks={sim.GateCheeks} nose={sim.GateNose} " +
                $"brows={sim.GateBrows} forehead={sim.GateForehead} eyes={sim.GateEyes}");
        }

        [Fact]
        public void EyesNeverBuildWithoutReveal()
        {
            var sim = new OrbSim();
            Run(sim, In(OrbMode.Speaking, 0.7f), 10f);
            Assert.Equal(0f, sim.GateEyes);
            Assert.Equal(0, sim.Eyes.Length);

            var pulse = In(OrbMode.Speaking, 0.7f);
            pulse.EyeRevealPulse = true;
            pulse.EyeRevealDuration = 1f;
            sim.Update(in pulse, Dt);
            Run(sim, In(OrbMode.Speaking, 0.7f), 0.3f);
            Assert.True(sim.GateEyes > 0.9f, $"reveal ramp {sim.GateEyes}");
            Assert.Equal(2, sim.Eyes.Length);
            Run(sim, In(OrbMode.Speaking, 0.7f), 2f);
            Assert.True(sim.GateEyes < 0.01f, $"reveal decay {sim.GateEyes}");
        }

        [Fact]
        public void FaceBudgetTracksF()
        {
            var sim = new OrbSim();
            Run(sim, In(OrbMode.Speaking, 0.6f), 3f);
            float frac = sim.SeekingCount / (float)sim.Count;
            Assert.True(MathF.Abs(frac - sim.BudgetF) <= 0.06f, $"frac={frac} F={sim.BudgetF}");
        }

        [Fact]
        public void DissolvesAfterSpeechEnds()
        {
            var sim = new OrbSim();
            Run(sim, In(OrbMode.Speaking, 0.6f), 2f);
            Run(sim, In(OrbMode.Idle), 2f);
            Assert.True(sim.GateMouth < 0.02f && sim.GateJaw < 0.02f && sim.GateCheeks < 0.02f
                     && sim.GateNose < 0.02f && sim.GateBrows < 0.02f && sim.GateForehead < 0.02f,
                $"gates {sim.GateMouth} {sim.GateJaw} {sim.GateCheeks} " +
                $"{sim.GateNose} {sim.GateBrows} {sim.GateForehead}");
            Assert.True(sim.AllFreeOrReleasing(), "particles still migrating/attached");
        }

        [Fact]
        public void FuzzStaysFiniteAndBounded()
        {
            var sim = new OrbSim();
            var rng = new Random(1234);
            var modes = new[] { OrbMode.Idle, OrbMode.Listening, OrbMode.Thinking, OrbMode.Speaking };
            var inp = new OrbInput();
            for (int f = 0; f < 3600; f++)   // 60 s at dt = 1/60
            {
                if (f % 30 == 0) inp.Mode = modes[rng.Next(4)];
                inp.MicLevel = (float)rng.NextDouble();
                inp.SpeechLevel = (float)rng.NextDouble();
                inp.Jaw = (float)rng.NextDouble();
                inp.Funnel = (float)rng.NextDouble();
                inp.Pucker = (float)rng.NextDouble();
                inp.Smile = (float)rng.NextDouble();
                inp.EyeRevealPulse = rng.NextDouble() < 0.005;
                inp.EyeRevealDuration = 0.5f + (float)rng.NextDouble();
                sim.Update(in inp, Dt);
                ReadOnlySpan<ParticleView> ps = sim.Particles;
                for (int i = 0; i < ps.Length; i++)
                {
                    ParticleView p = ps[i];
                    bool ok = float.IsFinite(p.X) && float.IsFinite(p.Y) && float.IsFinite(p.Z)
                           && MathF.Abs(p.X) <= 1.6f && MathF.Abs(p.Y) <= 1.6f && MathF.Abs(p.Z) <= 1.6f;
                    if (!ok) Assert.Fail($"frame {f} particle {i}: {p.X},{p.Y},{p.Z}");
                }
            }
        }

        [Fact]
        public void DeterministicGivenSeedAndScript()
        {
            var a = new OrbSim(2600, 7);
            var b = new OrbSim(2600, 7);
            for (int f = 0; f < 600; f++)
            {
                OrbInput inp = Script(f);
                a.Update(in inp, Dt);
                OrbInput inp2 = Script(f);
                b.Update(in inp2, Dt);
            }
            ReadOnlySpan<ParticleView> pa = a.Particles;
            ReadOnlySpan<ParticleView> pb = b.Particles;
            Assert.Equal(pa.Length, pb.Length);
            for (int i = 0; i < pa.Length; i++)
            {
                bool same = pa[i].X == pb[i].X && pa[i].Y == pb[i].Y && pa[i].Z == pb[i].Z
                         && pa[i].PrevX == pb[i].PrevX && pa[i].PrevY == pb[i].PrevY
                         && pa[i].Brightness == pb[i].Brightness && pa[i].Size == pb[i].Size;
                if (!same) Assert.Fail($"particle {i} diverged at frame 600");
            }
        }

        private static OrbInput Script(int f)
        {
            OrbMode m = (f / 150) switch
            {
                0 => OrbMode.Speaking,
                1 => OrbMode.Listening,
                2 => OrbMode.Thinking,
                _ => OrbMode.Speaking,
            };
            return new OrbInput
            {
                Mode = m,
                MicLevel = 0.5f + 0.5f * MathF.Cos(f * 0.05f),
                SpeechLevel = 0.5f + 0.5f * MathF.Sin(f * 0.03f),
                Jaw = MathF.Abs(MathF.Sin(f * 0.1f)),
                Funnel = 0.3f,
                Pucker = 0.2f,
                Smile = 0.15f,
                EyeRevealPulse = f == 200,
                EyeRevealDuration = 0.8f,
            };
        }
    }
}
