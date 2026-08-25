/*
 * BeckyRoughCut.cs
 * ----------------------------------------------------------------------------
 * Assemble the becky-roughcut timeline in VEGAS Pro 18, fully unattended.
 *
 * becky-roughcut (becky-go/cmd/roughcut) does ALL the thinking - silence
 * jump-cuts on the transcript, retake chains, quote splices, zero-crossing
 * snaps - and writes vegas_cut.json with FINAL timeline positions. This
 * script is the dumb assembler: four tracks exactly (his video, his audio,
 * quotes video, quotes audio), events placed at their given positions,
 * markers and regions, save, exit. Quotes play SEQUENTIALLY - the main edit
 * stops, the quote plays, the main edit resumes - never on top of his voice.
 *
 * HOW TO RUN
 *   Agent / walk-away:  set BECKY_ROUGHCUT_JSON=<path to vegas_cut.json>, then
 *                       vegas180.exe -SCRIPT:<this file>
 *                       (becky-roughcut --launch-vegas does exactly this)
 *   By hand:            Tools > Scripting > Run Script... ; a picker appears.
 *
 * VP18 API gotchas canonized here: Timecode has no .Seconds (use .Nanos*1e-7
 * in the verify script); AudioTrack.Volume is float; Vegas.ScriptArgs does
 * not exist (env vars); a compile error pops the same dialog as a bad
 * project.
 * ----------------------------------------------------------------------------
 */

using System;
using System.Collections.Generic;
using System.IO;
using System.Web.Script.Serialization;
using System.Windows.Forms;
using ScriptPortal.Vegas;   // VEGAS Pro 13 or older: use Sony.Vegas;

public class EntryPoint
{
    class RoughCut
    {
        public string project;
        public double fps;
        public int width;
        public int height;
        public string save_path;
        public List<RCEvent> events;
        public List<RCEvent> quotes;
        public List<RCMarker> markers;
        public List<RCRegion> regions;
        public double audio_gain_db;
    }
    class RCEvent  { public string source; public double @in; public double @out; public double tl; }
    class RCMarker { public double t; public string title; }
    class RCRegion { public double t; public double len; public string label; }

    public void FromVegas(Vegas vegas)
    {
        string jsonPath = Environment.GetEnvironmentVariable("BECKY_ROUGHCUT_JSON");
        if (string.IsNullOrEmpty(jsonPath) || !File.Exists(jsonPath))
        {
            using (OpenFileDialog dlg = new OpenFileDialog())
            {
                dlg.Title = "Becky: choose vegas_cut.json";
                dlg.Filter = "Becky rough cut (*.json)|*.json|All files (*.*)|*.*";
                if (dlg.ShowDialog() != DialogResult.OK) return;
                jsonPath = dlg.FileName;
            }
        }

        string logPath = jsonPath + ".buildlog.txt";
        List<string> log = new List<string>();
        try
        {
            Run(vegas, jsonPath, log);
        }
        catch (Exception ex)
        {
            log.Add("FATAL: " + ex);
        }
        try { File.WriteAllLines(logPath, log.ToArray()); } catch { }
        vegas.Exit();
    }

    static void Run(Vegas vegas, string jsonPath, List<string> log)
    {
        var ser = new JavaScriptSerializer();
        ser.MaxJsonLength = 100 * 1024 * 1024;
        RoughCut rc = ser.Deserialize<RoughCut>(File.ReadAllText(jsonPath));
        if (rc == null || rc.events == null || rc.events.Count == 0)
            throw new Exception("vegas_cut.json has no events");
        log.Add("json: " + jsonPath);
        log.Add("events: " + rc.events.Count + " quotes: " +
                (rc.quotes == null ? 0 : rc.quotes.Count) + " markers: " +
                (rc.markers == null ? 0 : rc.markers.Count));

        if (rc.width > 0)  vegas.Project.Video.Width  = rc.width;
        if (rc.height > 0) vegas.Project.Video.Height = rc.height;
        if (rc.fps > 0)    vegas.Project.Video.FrameRate = rc.fps;

        VideoTrack vtrack = vegas.Project.AddVideoTrack();
        vtrack.Name = "Rough Cut (video)";
        AudioTrack atrack = vegas.Project.AddAudioTrack();
        atrack.Name = "Rough Cut (audio)";
        atrack.Volume = (float)Math.Pow(10.0, rc.audio_gain_db / 20.0);

        Dictionary<string, Media> media = new Dictionary<string, Media>(StringComparer.OrdinalIgnoreCase);
        int placed = 0;

        foreach (RCEvent e in rc.events)
        {
            if (Place(vegas, media, vtrack, atrack, e.source, e.@in, e.@out, e.tl)) placed++;
            else log.Add("skip: " + e.source + " @" + e.tl);
        }
        log.Add("placed: " + placed + " of " + rc.events.Count);

        int qplaced = 0;
        if (rc.quotes != null && rc.quotes.Count > 0)
        {
            VideoTrack qv = vegas.Project.AddVideoTrack();
            qv.Name = "Quotes (video)";
            AudioTrack qa = vegas.Project.AddAudioTrack();
            qa.Name = "Quotes (audio)";
            foreach (RCEvent q in rc.quotes)
            {
                if (Place(vegas, media, qv, qa, q.source, q.@in, q.@out, q.tl)) qplaced++;
                else log.Add("quote skip: " + q.source);
            }
        }
        log.Add("quotes placed: " + qplaced);

        if (rc.regions != null)
        {
            foreach (RCRegion r in rc.regions)
            {
                try { vegas.Project.Regions.Add(new Region(Timecode.FromSeconds(r.t), Timecode.FromSeconds(r.len), r.label)); }
                catch { }
            }
        }
        if (rc.markers != null)
        {
            foreach (RCMarker mk in rc.markers)
            {
                try { vegas.Project.Markers.Add(new Marker(Timecode.FromSeconds(mk.t), mk.title)); }
                catch { }
            }
        }

        try { vegas.Transport.CursorPosition = Timecode.FromSeconds(0.0); } catch { }

        if (!string.IsNullOrEmpty(rc.save_path))
        {
            vegas.SaveProject(rc.save_path);
            log.Add("saved: " + rc.save_path);
        }
    }

    static bool Place(Vegas vegas, Dictionary<string, Media> media,
                      VideoTrack vt, AudioTrack at, string source,
                      double @in, double @out, double tl)
    {
        if (@out <= @in) return false;
        Media m;
        if (!media.TryGetValue(source, out m))
        {
            try { m = new Media(source); }
            catch { return false; }
            media[source] = m;
        }
        Timecode start  = Timecode.FromSeconds(tl);
        Timecode length = Timecode.FromSeconds(@out - @in);
        Timecode offset = Timecode.FromSeconds(@in);
        bool ok = false;
        try
        {
            VideoStream vs = m.GetVideoStreamByIndex(0);
            if (vs != null)
            {
                vt.AddVideoEvent(start, length).AddTake(vs).Offset = offset;
                ok = true;
            }
        }
        catch { }
        try
        {
            AudioStream au = m.GetAudioStreamByIndex(0);
            if (au != null)
            {
                at.AddAudioEvent(start, length).AddTake(au).Offset = offset;
                ok = true;
            }
        }
        catch { }
        return ok;
    }
}
