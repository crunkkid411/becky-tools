using System;
using System.IO;
using NAudio.Wave;
using NAudio.Wave.SampleProviders;

namespace Whoretana.Audio
{
    // Mic in -> MicLevel (drives the listening orb, "it hears me").
    // TTS wav out -> SpeechLevel via real amplitude metering (drives the lip-sync mouth).
    // Pure managed NAudio, no server. Degrades silently if there is no mic / audio device.
    public sealed class AudioEngine : IDisposable
    {
        private WaveInEvent? _in;
        private WaveFileWriter? _writer;
        private WaveOutEvent? _out;
        private readonly object _lock = new object();

        public float MicLevel { get; private set; }      // smoothed 0..1
        public float SpeechLevel { get; private set; }    // smoothed 0..1
        public bool MicAvailable { get; private set; }
        public event Action? SpeakDone;

        public void StartMic()
        {
            try
            {
                _in = new WaveInEvent { WaveFormat = new WaveFormat(16000, 16, 1), BufferMilliseconds = 40 };
                _in.DataAvailable += OnData;
                _in.StartRecording();
                MicAvailable = true;
            }
            catch { MicAvailable = false; }
        }

        public void StopMic()
        {
            try { _in?.StopRecording(); _in?.Dispose(); } catch { }
            _in = null;
        }

        private void OnData(object? sender, WaveInEventArgs e)
        {
            double sum = 0;
            int n = e.BytesRecorded / 2;
            for (int i = 0; i + 1 < e.BytesRecorded; i += 2)
            {
                short v = (short)(e.Buffer[i] | (e.Buffer[i + 1] << 8));
                double f = v / 32768.0;
                sum += f * f;
            }
            double rms = n > 0 ? Math.Sqrt(sum / n) : 0;
            float lvl = (float)Math.Min(1.0, rms * 4.5);                 // gain so normal speech ~ near full
            float k = lvl > MicLevel ? 0.6f : 0.12f;                     // fast attack, slow decay
            MicLevel += (lvl - MicLevel) * k;
            lock (_lock) { try { _writer?.Write(e.Buffer, 0, e.BytesRecorded); } catch { } }
        }

        // Push-to-talk capture to a temp wav. Returns the path to transcribe afterward.
        public string BeginUtterance()
        {
            string path = Path.Combine(Path.GetTempPath(), "whoretana_utt.wav");
            lock (_lock)
            {
                try { _writer?.Dispose(); } catch { }
                _writer = new WaveFileWriter(path, new WaveFormat(16000, 16, 1));
            }
            return path;
        }

        public void EndUtterance()
        {
            lock (_lock) { try { _writer?.Dispose(); } catch { } _writer = null; }
        }

        // Play a TTS wav; SpeechLevel tracks its real amplitude for the lip-sync mouth.
        public void SpeakWav(string path)
        {
            StopSpeak();
            try
            {
                var reader = new AudioFileReader(path);
                int block = Math.Max(256, reader.WaveFormat.SampleRate / 20); // ~50 ms notifications
                var meter = new MeteringSampleProvider(reader, block);
                meter.StreamVolume += (_, ev) =>
                {
                    float m = ev.MaxSampleValues.Length > 0 ? ev.MaxSampleValues[0] : 0f;
                    float t = Math.Min(1f, m * 1.5f);
                    SpeechLevel += (t - SpeechLevel) * 0.5f;
                };
                _out = new WaveOutEvent();
                _out.Init(meter);
                _out.PlaybackStopped += (_, __) =>
                {
                    SpeechLevel = 0;
                    try { reader.Dispose(); } catch { }
                    try { _out?.Dispose(); } catch { }
                    _out = null;
                    SpeakDone?.Invoke();
                };
                _out.Play();
            }
            catch
            {
                SpeechLevel = 0;
                SpeakDone?.Invoke();
            }
        }

        public bool IsSpeaking => _out != null;

        public void StopSpeak()
        {
            try { _out?.Stop(); } catch { }
        }

        public void Dispose() { StopMic(); StopSpeak(); }
    }
}
