/*
 * BeckyKeepList.cs - put the kept parts of ONE recording on an EMPTY project.
 * ----------------------------------------------------------------------------
 * Report item #6 (keep-list applicator, 2026-10-05). becky-livestream decides
 * what stays and where every cut lands (frame-exact, from the audio); this
 * script is the dumb assembler that VEGAS runs:
 *
 *   - the project takes the clip's own size and frame rate (what VEGAS does for
 *     the first clip dropped on an empty project - JORDANS-WORKFLOWS.md
 *     "New Project"), progressive, square pixels;
 *   - one video track + one audio track; every kept range becomes one grouped
 *     video+audio event pair, butt-joined from the start of the ruler;
 *   - every event is left SELECTED, so BeckyCut.cs can take the dead air out
 *     next, exactly as Jordan uses it by hand;
 *   - one Undo step. The recording is only READ.
 *
 * HOW TO RUN (becky-livestream does this):
 *   becky-vegas run_script path=<this file> job=<job.txt>
 * job.txt is plain text, one item per line, TAB-separated:
 *   media <TAB> X:\folder\clip.mp4
 *   range <TAB> <first frame> <TAB> <end frame, exclusive>     (one per kept range)
 *   audio <TAB> <audio stream index> <TAB> <delay s>   (optional, one per audio track;
 *         default: stream 0, no delay). An OBS recording (Prep OBS Footage.bat) has
 *         its mic on stream 2, and its audio plays <delay> seconds later than the
 *         picture once synced: the take offset is moved by it, and the event
 *         keeps to the frame grid (a sub-frame part goes into the offset).
 *
 * It NEVER shows a dialog (a dialog would stall an unattended run). It writes
 * <job.txt>.result.txt - "ok ..." or "error: ..." - and becky-livestream reads it.
 * Plain string parsing only, so it needs no extra assembly reference.
 * ----------------------------------------------------------------------------
 */

using System;
using System.Collections.Generic;
using System.IO;
using System.Text.RegularExpressions;
using ScriptPortal.Vegas;

public class EntryPoint
{
    public void FromVegas(Vegas vegas)
    {
        string job = JobPath();
        string result = (job == "" ? Path.Combine(Path.GetTempPath(), "BeckyKeepList") : job) + ".result.txt";
        try
        {
            if (job == "" || !File.Exists(job))
                throw new ApplicationException("no job file (run it through becky-vegas run_script ... job=<file>)");
            string media = "";
            List<long> frames = new List<long>();
            List<int> streams = new List<int>();
            List<double> delays = new List<double>();
            foreach (string line in File.ReadAllLines(job))
            {
                string[] f = line.Split('\t');
                if (f.Length >= 2 && f[0] == "media") media = f[1].Trim();
                if (f.Length >= 3 && f[0] == "audio")
                {
                    streams.Add(int.Parse(f[1].Trim()));
                    delays.Add(double.Parse(f[2].Trim(), System.Globalization.CultureInfo.InvariantCulture));
                }
                if (f.Length >= 3 && f[0] == "range")
                {
                    frames.Add(long.Parse(f[1].Trim()));
                    frames.Add(long.Parse(f[2].Trim()));
                }
            }
            if (media == "" || !File.Exists(media)) throw new ApplicationException("media not found: " + media);
            if (frames.Count == 0) throw new ApplicationException("the job has no ranges");

            Project p = vegas.Project;
            foreach (Track t in p.Tracks)
                if (t.Events.Count > 0) throw new ApplicationException("the project is not empty - nothing was changed");

            Media m = p.MediaPool.Find(media);
            if (m == null) m = new Media(media);
            VideoStream vs = m.GetVideoStreamByIndex(0);
            if (streams.Count == 0) { streams.Add(0); delays.Add(0); }
            List<AudioStream> aus = new List<AudioStream>();
            foreach (int k in streams)
            {
                AudioStream a = m.GetAudioStreamByIndex(k);
                if (a == null) throw new ApplicationException("the clip has no audio stream " + k);
                aus.Add(a);
            }
            if (vs == null) throw new ApplicationException("the clip needs a video stream");

            p.Video.Width = vs.Width;
            p.Video.Height = vs.Height;
            p.Video.FrameRate = vs.FrameRate;
            p.Video.FieldOrder = VideoFieldOrder.ProgressiveScan;
            p.Video.PixelAspectRatio = 1.0;

            long pos = 0;
            using (UndoBlock u = new UndoBlock("Becky: keep list"))
            {
                VideoTrack vt = p.AddVideoTrack();
                List<AudioTrack> ats = new List<AudioTrack>();
                foreach (AudioStream a in aus) ats.Add(p.AddAudioTrack());
                double fps = p.Video.FrameRate;
                for (int i = 0; i < frames.Count; i += 2)
                {
                    long len = frames[i + 1] - frames[i];
                    if (len <= 0) continue;
                    VideoEvent ve = vt.AddVideoEvent(Timecode.FromFrames(pos), Timecode.FromFrames(len));
                    ve.AddTake(vs).Offset = Timecode.FromFrames(frames[i]);
                    TrackEventGroup g = new TrackEventGroup();
                    p.TrackEventGroups.Add(g);
                    g.Add(ve);
                    ve.Selected = true;
                    for (int k = 0; k < aus.Count; k++)
                    {
                        AudioEvent ae = AddAudio(ats[k], aus[k], pos, len, frames[i], delays[k], fps);
                        if (ae == null) continue;
                        g.Add(ae);
                        ae.Selected = true;
                    }
                    pos += len;
                }
            }
            vegas.Transport.CursorPosition = Timecode.FromFrames(0);
            File.WriteAllText(result, "ok events=" + (frames.Count / 2) + " frames=" + pos +
                              " size=" + vs.Width + "x" + vs.Height + " fps=" + vs.FrameRate);
        }
        catch (Exception ex)
        {
            try { File.WriteAllText(result, "error: " + ex.Message); } catch { }
        }
    }

    // AddAudio puts the audio under the picture [pos, pos+len) whose source
    // starts at frame src, with the audio playing d seconds after the picture.
    // Edges stay on frames; the sub-frame part goes into the take offset.
    static AudioEvent AddAudio(AudioTrack at, AudioStream au, long pos, long len, long src, double d, double fps)
    {
        double off = src / fps - d; // seconds into the audio media at the event start
        long start = pos;
        if (off < 0)
        {
            long shift = (long)Math.Ceiling(-off * fps - 1e-6);
            start += shift;
            off += shift / fps;
        }
        long n = pos + len - start;
        Timecode offset = Timecode.FromNanos((long)Math.Round(off * 1e7));
        Timecode length = Timecode.FromFrames(n);
        Timecode avail = au.Length - offset;
        if (avail < length) length = avail;
        if (n <= 0 || length.Nanos <= 0) return null;
        AudioEvent ae = at.AddAudioEvent(Timecode.FromFrames(start), length);
        ae.AddTake(au).Offset = offset;
        return ae;
    }

    // JobPath reads "job" from %LOCALAPPDATA%\BeckyVegas\script-args.json, which
    // becky-vegas run_script rewrites before every run.
    static string JobPath()
    {
        try
        {
            string p = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                                    "BeckyVegas", "script-args.json");
            if (!File.Exists(p)) return "";
            Match mm = Regex.Match(File.ReadAllText(p), "\"job\"\\s*:\\s*\"((?:[^\"\\\\]|\\\\.)*)\"");
            if (!mm.Success) return "";
            return mm.Groups[1].Value.Replace("\\\\", "\\").Replace("\\\"", "\"").Replace("\\/", "/");
        }
        catch { return ""; }
    }
}
