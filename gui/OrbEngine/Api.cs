using System;

namespace OrbEngine
{
    public enum OrbMode { Idle, Listening, Thinking, Speaking }

    public struct OrbInput
    {
        public OrbMode Mode;
        public float MicLevel;      // 0..1
        public float SpeechLevel;   // 0..1
        public float Jaw, Funnel, Pucker, Smile;   // viseme weights 0..1
        public bool  EyeRevealPulse;               // one-frame trigger
        public float EyeRevealDuration;            // seconds, when pulsed
    }

    public struct ParticleView { public float X, Y, Z, PrevX, PrevY, Brightness, Size; }
    public struct MeshHintVertex { public float X, Y, Alpha; }   // projected, alpha premultiplied w/ gate*fresnel*visibility
    public struct EyeGlow { public float X, Y, Radius, Intensity; }
}
