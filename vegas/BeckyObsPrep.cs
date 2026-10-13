/*
 * BeckyObsPrep.cs - lay OBS recordings on an EMPTY project the way Jordan preps them.
 * ----------------------------------------------------------------------------
 * Jordan's OBS prep (X:\Videos\...\obs-workflow.md, 2026-10-12). becky-obsprep
 * decides everything (file order, how far each file's audio moves so the clap
 * spike lands on the frame where his hands meet); this script only does what he
 * does by hand, in his order. Three modes (the job's "mode" line):
 *
 * build  - into an EMPTY project:
 *   1. each file goes on the timeline in order, as VEGAS lays out a dropped OBS
 *      file: video on top, then its three audio streams (mic+desktop mix,
 *      desktop, mic), all four grouped. The FIRST file sets the project's size
 *      and frame rate, as dragging it onto an empty project does;
 *   2. the mix track (second from the top) is deleted; the desktop track too
 *      when the job says so;
 *   3. the group is taken apart, each file's audio is moved by its delay, and
 *      video + the remaining audio are grouped again.
 * probe  - renders the MIC track alone (other audio muted for the render, then
 *          restored) to a WAV around each file's clap, so becky-obsprep can hear
 *          where VEGAS itself puts the spike. VEGAS decodes OBS's AAC ~43 ms
 *          earlier than ffmpeg (measured 2026-10-12), so the spike is measured
 *          in VEGAS's own output, never assumed.
 * adjust - moves each file's audio again to a corrected delay.
 *
 * Moving the audio keeps every event edge on the frame grid: the event starts on
 * a frame and the sub-frame part goes into the take's media offset. An audio
 * event never runs past its own picture, so the next file never overlaps it.
 *
 * HOW TO RUN (becky-obsprep does this):
 *   becky-vegas run_script path=<this file> job=<job.txt>
 * job.txt, TAB-separated, one line per item:
 *   mode <TAB> build|probe|adjust
 *   drop_desktop <TAB> 0|1
 *   media <TAB> X:\folder\clip.mp4 <TAB> <audio delay in seconds, + = later>
 *   probe <TAB> <file index> <TAB> <first frame, from the file's start> <TAB> <frames> <TAB> <out.wav>
 * Writes <job.txt>.result.txt - "ok ..." or "error: ..." - never a dialog.
 * ----------------------------------------------------------------------------
 */

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Text.RegularExpressions;
using ScriptPortal.Vegas;

public class EntryPoint
{
    public void FromVegas(Vegas vegas)
    {
        string job = JobPath();
        string result = (job == "" ? Path.Combine(Path.GetTempPath(), "BeckyObsPrep") : job) + ".result.txt";
        try
        {
            if (job == "" || !File.Exists(job))
                throw new ApplicationException("no job file (run it through becky-vegas run_script ... job=<file>)");
            string mode = "build";
            bool dropDesktop = false;
            List<string> files = new List<string>();
            List<double> delays = new List<double>();
            List<string[]> probes = new List<string[]>();
            foreach (string line in File.ReadAllLines(job))
            {
                string[] f = line.Split('\t');
                if (f.Length >= 2 && f[0] == "mode") mode = f[1].Trim();
                if (f.Length >= 2 && f[0] == "drop_desktop") dropDesktop = f[1].Trim() == "1";
                if (f.Length >= 3 && f[0] == "media")
                {
                    files.Add(f[1].Trim());
                    delays.Add(double.Parse(f[2].Trim(), CultureInfo.InvariantCulture));
                }
                if (f.Length >= 5 && f[0] == "probe") probes.Add(f);
            }
            if (files.Count == 0) throw new ApplicationException("the job has no media");
            Project p = vegas.Project;
            string msg;
            if (mode == "build") msg = Build(p, files, delays, dropDesktop);
            else if (mode == "adjust") msg = Adjust(p, files, delays);
            else if (mode == "probe") msg = Probe(vegas, p, files, probes);
            else throw new ApplicationException("unknown mode " + mode);
            vegas.Transport.CursorPosition = Timecode.FromFrames(0);
            File.WriteAllText(result, "ok " + msg);
        }
        catch (Exception ex)
        {
            try { File.WriteAllText(result, "error: " + ex.Message); } catch { }
        }
    }

    static string Build(Project p, List<string> files, List<double> delays, bool dropDesktop)
    {
        foreach (Track t in p.Tracks)
            if (t.Events.Count > 0) throw new ApplicationException("the project is not empty - nothing was changed");
        using (UndoBlock u = new UndoBlock("Becky: OBS prep"))
        {
            VideoTrack vt = null;
            AudioTrack mix = null, desktop = null, mic = null;
            List<VideoEvent> videos = new List<VideoEvent>();
            List<AudioEvent[]> audios = new List<AudioEvent[]>();
            List<TrackEventGroup> groups = new List<TrackEventGroup>();
            long pos = 0; // frames

            // 1. every file, in order, video + 3 audio streams grouped.
            for (int i = 0; i < files.Count; i++)
            {
                if (!File.Exists(files[i])) throw new ApplicationException("media not found: " + files[i]);
                Media m = p.MediaPool.Find(files[i]);
                if (m == null) m = new Media(files[i]);
                VideoStream vs = m.GetVideoStreamByIndex(0);
                if (vs == null) throw new ApplicationException("no video stream in " + files[i]);
                AudioStream[] au = new AudioStream[3];
                for (int k = 0; k < 3; k++)
                {
                    au[k] = m.GetAudioStreamByIndex(k);
                    if (au[k] == null) throw new ApplicationException("expected 3 audio streams (OBS) in " + files[i]);
                }
                if (i == 0)
                {
                    // What VEGAS does for the first clip dropped on an empty project.
                    p.Video.Width = vs.Width;
                    p.Video.Height = vs.Height;
                    p.Video.FrameRate = vs.FrameRate;
                    p.Video.FieldOrder = vs.FieldOrder;
                    p.Video.PixelAspectRatio = vs.PixelAspectRatio;
                    vt = p.AddVideoTrack();
                    mix = p.AddAudioTrack();
                    desktop = p.AddAudioTrack();
                    mic = p.AddAudioTrack();
                }
                long len = vs.Length.FrameCount;
                VideoEvent ve = vt.AddVideoEvent(Timecode.FromFrames(pos), Timecode.FromFrames(len));
                ve.AddTake(vs);
                TrackEventGroup g = new TrackEventGroup();
                p.TrackEventGroups.Add(g);
                g.Add(ve);
                AudioTrack[] ats = new AudioTrack[] { mix, desktop, mic };
                AudioEvent[] aes = new AudioEvent[3];
                for (int k = 0; k < 3; k++)
                {
                    aes[k] = ats[k].AddAudioEvent(Timecode.FromFrames(pos), au[k].Length);
                    aes[k].AddTake(au[k]);
                    g.Add(aes[k]);
                }
                videos.Add(ve);
                audios.Add(aes);
                groups.Add(g);
                pos += len;
            }

            // 2. delete the mix track (and the desktop track when asked).
            p.Tracks.Remove(mix);
            if (dropDesktop) p.Tracks.Remove(desktop);

            // 3. ungroup, move the audio, regroup.
            foreach (TrackEventGroup g in groups) p.TrackEventGroups.Remove(g);
            for (int i = 0; i < files.Count; i++)
            {
                TrackEventGroup g = new TrackEventGroup();
                p.TrackEventGroups.Add(g);
                g.Add(videos[i]);
                for (int k = dropDesktop ? 2 : 1; k < 3; k++)
                {
                    Place(p, videos[i], audios[i][k], delays[i]);
                    g.Add(audios[i][k]);
                }
            }
        }
        return "built files=" + files.Count + " size=" + p.Video.Width + "x" + p.Video.Height +
               " fps=" + p.Video.FrameRate.ToString(CultureInfo.InvariantCulture) + " tracks=" + p.Tracks.Count;
    }

    // Place puts an audio event so its media plays d seconds after the picture's.
    static void Place(Project p, VideoEvent ve, AudioEvent ae, double d)
    {
        double fps = p.Video.FrameRate;
        Timecode mediaLen = ae.ActiveTake.AvailableLength;
        long startFrames = ve.Start.FrameCount;
        double offset; // seconds into the audio media at the event start
        if (d >= 0)
        {
            long shift = (long)Math.Ceiling(d * fps - 1e-6);
            startFrames += shift;
            offset = shift / fps - d;
        }
        else offset = -d;
        Timecode start = Timecode.FromFrames(startFrames);
        Timecode off = Timecode.FromNanos((long)Math.Round(offset * 1e7));
        Timecode length = ve.End - start;
        Timecode avail = mediaLen - off;
        if (avail < length) length = avail;
        ae.Start = start;
        ae.ActiveTake.Offset = off;
        ae.Length = length;
    }

    // Pairs finds each file's video event and its audio events (by media path).
    static void Pairs(Project p, List<string> files, List<VideoEvent> videos, List<List<AudioEvent>> audios)
    {
        foreach (string f in files)
        {
            VideoEvent v = null;
            List<AudioEvent> a = new List<AudioEvent>();
            foreach (Track t in p.Tracks)
                foreach (TrackEvent e in t.Events)
                {
                    if (e.ActiveTake == null || e.ActiveTake.Media == null) continue;
                    if (!string.Equals(e.ActiveTake.Media.FilePath, f, StringComparison.OrdinalIgnoreCase)) continue;
                    if (e.IsVideo() && v == null) v = (VideoEvent)e;
                    if (e.IsAudio()) a.Add((AudioEvent)e);
                }
            if (v == null || a.Count == 0) throw new ApplicationException("not on the timeline: " + f);
            videos.Add(v);
            audios.Add(a);
        }
    }

    static string Adjust(Project p, List<string> files, List<double> delays)
    {
        List<VideoEvent> videos = new List<VideoEvent>();
        List<List<AudioEvent>> audios = new List<List<AudioEvent>>();
        Pairs(p, files, videos, audios);
        using (UndoBlock u = new UndoBlock("Becky: OBS prep sync"))
        {
            for (int i = 0; i < files.Count; i++)
                foreach (AudioEvent ae in audios[i]) Place(p, videos[i], ae, delays[i]);
        }
        return "adjusted files=" + files.Count;
    }

    static string Probe(Vegas vegas, Project p, List<string> files, List<string[]> probes)
    {
        RenderTemplate wav = null;
        foreach (Renderer r in vegas.Renderers)
            foreach (RenderTemplate t in r.Templates)
                if (wav == null && t.IsValid() && r.FileExtension.ToLower().Contains("wav") &&
                    t.Name.Contains("48,000 Hz") && t.Name.Contains("16 Bit") && t.Name.Contains("PCM"))
                    wav = t;
        if (wav == null) throw new ApplicationException("no 48 kHz 16-bit WAV render template");
        List<VideoEvent> videos = new List<VideoEvent>();
        List<List<AudioEvent>> audios = new List<List<AudioEvent>>();
        Pairs(p, files, videos, audios);
        // the mic is the bottom audio track; mute the rest just for the render
        AudioTrack mic = null;
        foreach (Track t in p.Tracks) if (t.IsAudio()) mic = (AudioTrack)t;
        Dictionary<Track, bool> was = new Dictionary<Track, bool>();
        foreach (Track t in p.Tracks) { was[t] = t.Mute; if (t.IsAudio() && t != mic) t.Mute = true; }
        int n = 0;
        try
        {
            foreach (string[] f in probes)
            {
                int i = int.Parse(f[1].Trim());
                long from = videos[i].Start.FrameCount + long.Parse(f[2].Trim());
                RenderStatus st = vegas.Render(f[4].Trim(), wav, Timecode.FromFrames(from), Timecode.FromFrames(long.Parse(f[3].Trim())));
                if (st != RenderStatus.Complete) throw new ApplicationException("probe render ended " + st);
                n++;
            }
        }
        finally
        {
            foreach (KeyValuePair<Track, bool> kv in was) kv.Key.Mute = kv.Value;
        }
        return "probed=" + n;
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
