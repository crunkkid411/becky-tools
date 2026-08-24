/*
 * BeckyRoughCut.cs
 * ----------------------------------------------------------------------------
 * Assemble the becky-roughcut timeline in VEGAS Pro 18, fully unattended.
 *
 * becky-roughcut (becky-go/cmd/roughcut) does ALL the thinking - silence cuts
 * on zero-crossings, snapped boundaries, re-take chains, quote markers - and
 * writes vegas_cut.json. This script is the dumb assembler: it reads that JSON,
 * builds the timeline through the scripting API (VEGAS 18 cannot import FCPXML
 * or OTIO; its FCP7 importer crashes on hand-rolled XML), saves the .veg and
 * quits. Jordan walks away; when he comes back the rough cut is populated.
 *
 * HOW TO RUN
 *   Agent / walk-away:  set BECKY_ROUGHCUT_JSON=<path to vegas_cut.json>, then
 *                       vegas180.exe -SCRIPT:<this file>
 *                       (becky-roughcut --launch-vegas does exactly this)
 *   By hand:            Tools > Scripting > Run Script... ; a picker appears.
 *
 * WHAT IT BUILDS
 *   - project video settings from the JSON (1920x1080x30 for the hj footage);
 *   - one video track + one audio track, "Rough Cut (...)";
 *   - every keep span as a paired video+audio event, butted end to end,
 *     Take.Offset = the span's in-point (sources are only ever READ);
 *   - a named Region per source clip (jump clip-to-clip);
 *   - a Marker per quote lead-in / RETAKE? note;
 *   - saves <save_path> (.veg) and writes <save_path>.buildlog.txt so a
 *     headless run can be audited afterwards.
 *
 * VERIFIED against the official MAGIX scripting docs (VegasProData/):
 *   EntryPoint.FromVegas; AddVideoTrack/AddAudioTrack; AddVideoEvent/
 *   AddAudioEvent(Timecode,Timecode); Take.Offset; Marker(Timecode,label);
 *   Region(Timecode,Timecode,label); Vegas.SaveProject(path); Vegas.Exit().
 *   NOTE for VEGAS Pro 13 and OLDER ONLY: change the using below to Sony.Vegas.
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
        public List<RCMarker> markers;
        public List<RCRegion> regions;
    }
    class RCEvent  { public string source; public double @in; public double @out; }
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
        log.Add("events: " + rc.events.Count + " markers: " +
                (rc.markers == null ? 0 : rc.markers.Count) + " regions: " +
                (rc.regions == null ? 0 : rc.regions.Count));

        // Project settings from the footage, so the ruler reads true.
        if (rc.width > 0)  vegas.Project.Video.Width  = rc.width;
        if (rc.height > 0) vegas.Project.Video.Height = rc.height;
        if (rc.fps > 0)    vegas.Project.Video.FrameRate = rc.fps;

        VideoTrack vtrack = vegas.Project.AddVideoTrack();
        vtrack.Name = "Rough Cut (video)";
        AudioTrack atrack = vegas.Project.AddAudioTrack();
        atrack.Name = "Rough Cut (audio)";

        // One Media per source file, reused for every event of that file.
        Dictionary<string, Media> media = new Dictionary<string, Media>(StringComparer.OrdinalIgnoreCase);
        Timecode cursor = Timecode.FromSeconds(0.0);
        int placed = 0;

        foreach (RCEvent e in rc.events)
        {
            if (e.@out <= e.@in) { log.Add("skip (no length): " + e.source); continue; }
            Media m;
            if (!media.TryGetValue(e.source, out m))
            {
                try { m = new Media(e.source); }
                catch (Exception ex) { log.Add("skip (unreadable): " + e.source + " (" + ex.Message + ")"); continue; }
                media[e.source] = m;
            }
            Timecode start  = cursor;
            Timecode length = Timecode.FromSeconds(e.@out - e.@in);
            Timecode offset = Timecode.FromSeconds(e.@in);
            bool ok = false;
            try
            {
                VideoStream vs = m.GetVideoStreamByIndex(0);
                if (vs != null)
                {
                    VideoEvent ve = vtrack.AddVideoEvent(start, length);
                    ve.AddTake(vs).Offset = offset;
                    ok = true;
                }
            }
            catch { }
            try
            {
                AudioStream au = m.GetAudioStreamByIndex(0);
                if (au != null)
                {
                    AudioEvent ae = atrack.AddAudioEvent(start, length);
                    ae.AddTake(au).Offset = offset;
                    ok = true;
                }
            }
            catch { }
            if (!ok) { log.Add("skip (no streams): " + e.source); continue; }
            cursor = cursor + length;
            placed++;
        }
        log.Add("placed: " + placed + " of " + rc.events.Count);

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
}
