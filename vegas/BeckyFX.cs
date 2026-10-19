/*
 * BeckyFX.cs - put Jordan's own effects on a stretch of the timeline, the way he does it by hand.
 * ----------------------------------------------------------------------------
 * Jordan, 2026-10-07: "they cover zooms and censoring - do we have the tools to do that in
 * Vegas Pro? If not, we need it." His own scripts (Documents\Vegas_Assets\Scripts\HJ Scripts)
 * all do one thing: put a plug-in with HIS preset on the selected (already split) event -
 * CENSOR = "VEGAS Pixelate" + "CENSOR", CUT IN = "VEGAS Picture In Picture" + "IN", BIG HEAD =
 * "VEGAS Spherize" + "BIG HEAD", DRAMA = "VEGAS Color Corrector" + "DRAMA", and his filter
 * packages ("ZOOM FAST", "ZOOM SLOW", "ZOOM FLASH DRAMA", "SAD ZOOM", ...) are plug-ins too.
 * This script splits at the range and does the same, so the result is exactly what he would
 * have built: an event with his effect on it, which he can open, tweak or delete.
 * Filter packages cannot be added: VEGAS 18's script API shows them as empty shells
 * (measured 2026-10-07), so a moving zoom is a Pan/Crop push-in (zoom line) instead.
 *
 * Job file: plain text, TAB-separated, one item per line, positions in FRAMES:
 *   fx    <TAB> <first frame> <TAB> <length> <TAB> <plug-in> [<TAB> <preset>]
 *   zoom  <TAB> <first frame> <TAB> <length> <TAB> <scale> [<TAB> <cx> <TAB> <cy> [<TAB> <ramp frames>]]
 *   panzoom <TAB> <first frame> <TAB> <length> <TAB> <scale> [<TAB> <end height, 0.5 = none>]  (pipzoom's move, in Event Pan/Crop)
 *   pipzoom <TAB> <first frame> <TAB> <length> <TAB> <scale, his: 1.11 or 1.447> [<TAB> <end height, 0.5 = none>]
 *   censor <TAB> <first frame> <TAB> <length> <TAB> <cx> <TAB> <cy> <TAB> <w> <TAB> <h> [<TAB> Oval|Rectangle]
 *   censor <TAB> <first frame> <TAB> <length> <TAB> <boxes.tsv> [<TAB> Oval|Rectangle]
 *         Jordan's way: copy on a new CENSOR track directly above + CENSOR preset + a mask.
 *         boxes.tsv: <frame in the range> <TAB> cx <TAB> cy <TAB> w <TAB> h per line (fractions,
 *         top-left origin) - one keyframe per line, so the mask follows the thing frame by frame.
 *   duck  <TAB> <first frame> <TAB> <length> <TAB> <dB, e.g. -60 to silence>
 *   audio <TAB> <first frame> <TAB> <file.wav> <TAB> <track name>
 *   bleep <TAB> <first frame> <TAB> <length> <TAB> <sound file> <TAB> <gain dB> <TAB> <audio track number, 0 = top>
 *         Jordan's censor: his speech in the range is cut out and the sound fills the gap ON that track.
 *   trackfx <TAB> 0 <TAB> 0 <TAB> <media file> <TAB> <plug-in> <TAB> <preset> [<TAB> <plug-in> <TAB> <preset> ...]
 *         track effects, in order, on every video track showing the media (his hj-starter package).
 *   overlay <TAB> <first frame> <TAB> <length> <TAB> <file> <TAB> <speech track number>
 *   insert  <TAB> <first frame> <TAB> <length> <TAB> <file> <TAB> <speech track number>
 *         a file of his: over his picture on a B-ROLL track, or inserted (everything after moves later).
 * fx and zoom go on the TOP video track that has footage (not a title) at <first frame>. duck puts
 * points on the volume envelope of every audio track with sound there. audio places a file
 * (e.g. a bleep tone) on the named audio track, made if missing.
 * A job is ONE Undo step and all or nothing: a line that fails cancels it.
 *
 * HOW TO RUN
 *   On the open project: becky-vegas run_script path=<this file> job=<job.txt>  (one Undo step)
 *   Headless on a COPY:  BECKY_FX_JOB=<job> BECKY_FX_VEG=<copy.veg> BECKY_FX_OUT=<new.veg>
 *                        vegas180.exe -SCRIPT:<this file>   (refuses to save over BECKY_FX_VEG)
 * Never a dialog. Writes <job>.result.txt: "ok fx=N duck=N audio=N bleep=N placed=N trackfx=N" or "error: ...".
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
        string job = Environment.GetEnvironmentVariable("BECKY_FX_JOB");
        string veg = Environment.GetEnvironmentVariable("BECKY_FX_VEG");
        string outVeg = Environment.GetEnvironmentVariable("BECKY_FX_OUT");
        bool headless = !string.IsNullOrEmpty(job);
        if (!headless) job = JobPath();
        string result = (job == "" ? Path.Combine(Path.GetTempPath(), "BeckyFX") : job) + ".result.txt";
        try
        {
            if (job == "" || !File.Exists(job))
                throw new ApplicationException("no job file (becky-vegas run_script ... job=<file>, or BECKY_FX_JOB)");
            if (headless)
            {
                if (string.IsNullOrEmpty(veg) || string.IsNullOrEmpty(outVeg))
                    throw new ApplicationException("headless needs BECKY_FX_VEG and BECKY_FX_OUT");
                if (Path.GetFullPath(veg).Equals(Path.GetFullPath(outVeg), StringComparison.OrdinalIgnoreCase))
                    throw new ApplicationException("BECKY_FX_OUT must be a new file, never the project it opened");
                if (!vegas.OpenProject(veg)) throw new ApplicationException("could not open " + veg);
            }
            if (vegas.Project == null) throw new ApplicationException("no project is open");
            string summary = Apply(vegas, File.ReadAllLines(job));
            if (headless && !vegas.SaveProject(outVeg)) throw new ApplicationException("could not save " + outVeg);
            File.WriteAllText(result, "ok " + summary);
        }
        catch (Exception ex)
        {
            try { File.WriteAllText(result, "error: " + ex.Message); } catch { }
        }
        if (headless) vegas.Exit();
    }

    // Apply runs every line as ONE Undo step, all or nothing: a line that fails
    // cancels the whole job, so the project is left exactly as it was.
    static string Apply(Vegas vegas, string[] lines)
    {
        int fx = 0, duck = 0, audio = 0, bleep = 0, placed = 0, trackfx = 0;
        using (UndoBlock u = new UndoBlock("Becky: effects"))
        {
            foreach (string line in lines)
            {
                string[] f = line.Split('\t');
                if (f.Length < 4) continue;
                try
                {
                    switch (f[0])
                    {
                        case "bleep":
                            bleep += Bleep(vegas.Project, Timecode.FromFrames(long.Parse(f[1].Trim())), Timecode.FromFrames(Len(f[2])),
                                           f[3].Trim(), Num(f[4]), int.Parse(f[5].Trim()));
                            break;
                        case "trackfx":
                            trackfx += TrackFx(vegas, f);
                            break;
                        case "overlay":
                        case "insert":
                            placed += Place(vegas.Project, f[0] == "insert", Timecode.FromFrames(long.Parse(f[1].Trim())), Timecode.FromFrames(Len(f[2])),
                                            f[3].Trim(), int.Parse(f[4].Trim()));
                            break;
                        default:
                            ApplyOne(vegas, f, ref fx, ref duck, ref audio);
                            break;
                    }
                }
                catch (Exception ex)
                {
                    u.Cancel = true;
                    string where = ex.StackTrace == null ? "" : ex.StackTrace.Trim().Split('\n')[0].Trim();
                    throw new ApplicationException("\"" + line.Trim().Replace('\t', ' ') + "\": " + ex.Message + " (" + where + ")");
                }
            }
        }
        return "fx=" + fx + " duck=" + duck + " audio=" + audio + " bleep=" + bleep + " placed=" + placed + " trackfx=" + trackfx;
    }

    static void ApplyOne(Vegas vegas, string[] f, ref int fx, ref int duck, ref int audio)
    {
        Timecode at = Timecode.FromFrames(long.Parse(f[1].Trim()));
        switch (f[0])
        {
            case "fx":
                fx += AddFx(vegas, at, Timecode.FromFrames(Len(f[2])), f[3].Trim(), f.Length > 4 ? f[4].Trim() : "");
                break;
            case "zoom":
                fx += Zoom(vegas, at, Timecode.FromFrames(Len(f[2])), Num(f[3]), f.Length > 5 ? Num(f[4]) : 0.5, f.Length > 5 ? Num(f[5]) : 0.5,
                           f.Length > 6 ? long.Parse(f[6].Trim()) : 6);
                break;
            case "panzoom":
                fx += PanZoom(vegas, at, Timecode.FromFrames(Len(f[2])), Num(f[3]), f.Length > 4 ? Num(f[4]) : 0.5);
                break;
            case "pipzoom":
                fx += PipZoom(vegas, at, Timecode.FromFrames(Len(f[2])), Num(f[3]), f.Length > 4 ? Num(f[4]) : 0.5);
                break;
            case "censor":
                fx += Censor(vegas, at, Timecode.FromFrames(Len(f[2])), f);
                break;
            case "duck":
                duck += Duck(vegas.Project, at, Timecode.FromFrames(Len(f[2])), double.Parse(f[3].Trim(), System.Globalization.CultureInfo.InvariantCulture));
                break;
            case "audio":
                audio += PlaceAudio(vegas.Project, at, f[2].Trim(), f[3].Trim());
                break;
        }
    }

    static double Num(string s)
    {
        return double.Parse(s.Trim(), System.Globalization.CultureInfo.InvariantCulture);
    }

    // Zoom is a Pan/Crop push-in on the range: the piece starts at its normal
    // framing and reaches <scale>x (2 = twice as close) around the point
    // (cx, cy) - fractions of the picture, 0.5 0.5 = the middle - after <ramp>
    // frames, holding there to the end of the range. The next piece is untouched,
    // so the zoom ends on a cut. Scriptable, unlike filter packages.
    static int Zoom(Vegas vegas, Timecode at, Timecode len, double scale, double cx, double cy, long ramp)
    {
        if (scale <= 1) throw new ApplicationException("zoom scale must be above 1, got " + scale);
        Track track = FootageTrack(vegas.Project, at);
        if (track == null) throw new ApplicationException("no footage on a video track at frame " + at.FrameCount);
        Timecode end = at + len;
        at = Snap(track, at);
        end = Snap(track, end);
        SplitAt(track, at);
        SplitAt(track, end);
        int n = 0;
        foreach (TrackEvent ev in track.Events)
        {
            if (ev.Start < at || ev.End > end) continue;
            VideoMotion vm = ((VideoEvent)ev).VideoMotion;
            VideoMotionKeyframe k0 = vm.Keyframes[0];
            float x0 = k0.TopLeft.X, y0 = k0.TopLeft.Y, x1 = k0.BottomRight.X, y1 = k0.BottomRight.Y;
            float w = (float)((x1 - x0) / scale), h = (float)((y1 - y0) / scale);
            float mx = Clamp((float)(x0 + cx * (x1 - x0)), x0 + w / 2, x1 - w / 2);
            float my = Clamp((float)(y0 + cy * (y1 - y0)), y0 + h / 2, y1 - h / 2);
            VideoMotionBounds zoomed = new VideoMotionBounds(mx - w / 2, my - h / 2, mx + w / 2, my - h / 2, mx + w / 2, my + h / 2, mx - w / 2, my + h / 2);
            if (n == 0)
            {
                // the first piece pushes in...
                long rampAt = Math.Min(ramp, Math.Max(1, ev.Length.FrameCount - 1));
                VideoMotionKeyframe k = new VideoMotionKeyframe(Timecode.FromFrames(rampAt));
                vm.Keyframes.Add(k);
                k.Bounds = zoomed;
            }
            else
            {
                // ...the pieces after his jump cuts hold the zoom instead of restarting it
                k0.Bounds = zoomed;
            }
            n++;
        }
        if (n == 0) throw new ApplicationException("nothing on the track inside frames " + at.FrameCount + "-" + end.FrameCount);
        return n;
    }

    // PanZoom is the slow creep as an Event Pan/Crop move (Jordan, 2026-10-18: Pan/Crop, not
    // the Picture In Picture plug-in): the framing goes linearly from normal to <scale>x across
    // the range, its centre's height from 0.5 to <y>. One continuous move across his jump cuts:
    // each piece carries its share, like PipZoom.
    static int PanZoom(Vegas vegas, Timecode at, Timecode len, double scale, double y)
    {
        if (scale <= 1) throw new ApplicationException("zoom scale must be above 1, got " + scale);
        Track track = FootageTrack(vegas.Project, at);
        if (track == null) throw new ApplicationException("no footage on a video track at frame " + at.FrameCount);
        Timecode end = at + len;
        at = Snap(track, at);
        end = Snap(track, end);
        SplitAt(track, at);
        SplitAt(track, end);
        double total = (end - at).ToMilliseconds();
        int n = 0;
        foreach (TrackEvent ev in track.Events)
        {
            if (ev.Start < at || ev.End > end) continue;
            double a = (ev.Start - at).ToMilliseconds() / total, b = (ev.End - at).ToMilliseconds() / total;
            VideoMotion vm = ((VideoEvent)ev).VideoMotion;
            VideoMotionKeyframe k0 = vm.Keyframes[0];
            float x0 = k0.TopLeft.X, y0 = k0.TopLeft.Y, x1 = k0.BottomRight.X, y1 = k0.BottomRight.Y;
            k0.Bounds = Framed(x0, y0, x1, y1, 1 + (scale - 1) * a, 0.5 + (y - 0.5) * a);
            k0.Type = VideoKeyframeType.Linear;
            VideoMotionKeyframe k1 = new VideoMotionKeyframe(Timecode.FromFrames(Math.Max(1, ev.Length.FrameCount - 1)));
            vm.Keyframes.Add(k1);
            k1.Bounds = Framed(x0, y0, x1, y1, 1 + (scale - 1) * b, 0.5 + (y - 0.5) * b);
            k1.Type = VideoKeyframeType.Linear;
            n++;
        }
        if (n == 0) throw new ApplicationException("nothing on the track inside frames " + at.FrameCount + "-" + end.FrameCount);
        return n;
    }

    // Framed is the Pan/Crop rectangle <scale>x closer than (x0,y0)-(x1,y1), centred
    // across and at <cy> (a fraction from the top) down, kept inside the picture.
    static VideoMotionBounds Framed(float x0, float y0, float x1, float y1, double scale, double cy)
    {
        float w = (float)((x1 - x0) / scale), h = (float)((y1 - y0) / scale);
        float mx = (x0 + x1) / 2;
        float my = Clamp((float)(y0 + cy * (y1 - y0)), y0 + h / 2, y1 - h / 2);
        return new VideoMotionBounds(mx - w / 2, my - h / 2, mx + w / 2, my - h / 2, mx + w / 2, my + h / 2, mx - w / 2, my + h / 2);
    }

    // PipZoom is Jordan's own slow zoom, measured from his projects (2026-10-08, 17 of them):
    // VEGAS Picture In Picture on each piece, Scale keyframed linearly from 1 at the start of
    // the range to <scale> at its end (his values: 1.11 for a creep, 1.447 for a push), and
    // Location's height easing from 0.5 to <y> so his face stays in frame (he used 0.42-0.45
    // with 1.447). One continuous move across his jump cuts: each piece carries its share.
    static int PipZoom(Vegas vegas, Timecode at, Timecode len, double scale, double y)
    {
        PlugInNode node = Find(vegas.VideoFX, "VEGAS Picture In Picture");
        if (node == null) throw new ApplicationException("no video effect named \"VEGAS Picture In Picture\"");
        Track track = FootageTrack(vegas.Project, at);
        if (track == null) throw new ApplicationException("no footage on a video track at frame " + at.FrameCount);
        Timecode end = at + len;
        at = Snap(track, at);
        end = Snap(track, end);
        SplitAt(track, at);
        SplitAt(track, end);
        double total = (end - at).ToMilliseconds();
        int n = 0;
        foreach (TrackEvent ev in track.Events)
        {
            if (ev.Start < at || ev.End > end) continue;
            double a = (ev.Start - at).ToMilliseconds() / total, b = (ev.End - at).ToMilliseconds() / total;
            Effect e = new Effect(node);
            ((VideoEvent)ev).Effects.Add(e);
            OFXEffect ofx = e.OFXEffect;
            foreach (string name in new string[] { "Scale", "DistortionScaleY" })
            {
                OFXDoubleParameter p = (OFXDoubleParameter)ofx.FindParameterByName(name);
                p.IsAnimated = true;
                p.SetValueAtTime(Timecode.FromFrames(0), 1 + (scale - 1) * a);
                p.SetValueAtTime(ev.Length, 1 + (scale - 1) * b);
            }
            OFXDouble2DParameter loc = (OFXDouble2DParameter)ofx.FindParameterByName("Location");
            loc.IsAnimated = true;
            OFXDouble2D p0 = loc.Value, p1 = loc.Value;
            p0.Y = 0.5 + (y - 0.5) * a;
            p1.Y = 0.5 + (y - 0.5) * b;
            loc.SetValueAtTime(Timecode.FromFrames(0), p0);
            loc.SetValueAtTime(ev.Length, p1);
            n++;
        }
        if (n == 0) throw new ApplicationException("nothing on the track inside frames " + at.FrameCount + "-" + end.FrameCount);
        return n;
    }

    // Censor is Jordan's own way (2026-10-08): "the clip is duplicated directly above the
    // original, the censor preset is added to the topmost one, then a mask is created around
    // the thing being censored... And yes, I often move the censor frame-by-frame." So: the
    // pieces in the range are copied onto a new "CENSOR" track right above, each copy gets
    // VEGAS Pixelate + CENSOR and VEGAS Bezier Masking (Mask 1, oval or rectangle) whose
    // Location/Width/Height take a keyframe for every box row. Box values are fractions of
    // the picture, top-left origin (cx, cy = centre). The original track is only split at
    // the range edges, like every other line here.
    static int Censor(Vegas vegas, Timecode at, Timecode len, string[] f)
    {
        string shape = "Oval";
        List<double[]> boxes = new List<double[]>(); // frame offset in the range, cx, cy, w, h
        if (f.Length >= 7 && !File.Exists(f[3].Trim()))
        {
            boxes.Add(new double[] { 0, Num(f[3]), Num(f[4]), Num(f[5]), Num(f[6]) });
            if (f.Length > 7) shape = f[7].Trim();
        }
        else
        {
            if (!File.Exists(f[3].Trim())) throw new ApplicationException("censor needs cx cy w h, or a box file: " + f[3]);
            foreach (string row in File.ReadAllLines(f[3].Trim()))
            {
                string[] c = row.Split('\t');
                if (c.Length < 5) continue;
                boxes.Add(new double[] { Num(c[0]), Num(c[1]), Num(c[2]), Num(c[3]), Num(c[4]) });
            }
            if (f.Length > 4) shape = f[4].Trim();
            if (boxes.Count == 0) throw new ApplicationException("no boxes in " + f[3]);
        }
        PlugInNode pix = Find(vegas.VideoFX, "VEGAS Pixelate");
        PlugInNode bz = vegas.VideoFX.GetChildByUniqueID("{Svfx:com.vegascreativesoftware:bzmasking}");
        if (pix == null || bz == null) throw new ApplicationException("VEGAS Pixelate or VEGAS Bezier Masking is missing");
        Track track = FootageTrack(vegas.Project, at);
        if (track == null) throw new ApplicationException("no footage on a video track at frame " + at.FrameCount);
        Timecode end = at + len;
        at = Snap(track, at);
        end = Snap(track, end);
        SplitAt(track, at);
        SplitAt(track, end);
        VideoTrack top = new VideoTrack(vegas.Project, track.Index, "CENSOR");
        vegas.Project.Tracks.Add(top);
        List<TrackEvent> inRange = new List<TrackEvent>();
        foreach (TrackEvent ev in track.Events) if (ev.Start >= at && ev.End <= end) inRange.Add(ev);
        int n = 0;
        foreach (TrackEvent ev in inRange)
        {
            VideoEvent copy = (VideoEvent)ev.Copy(top, ev.Start);
            Effect pe = new Effect(pix);
            copy.Effects.Add(pe);
            pe.Preset = "CENSOR";
            Effect me = new Effect(bz);
            copy.Effects.Add(me);
            OFXEffect ofx = me.OFXEffect;
            OFXChoiceParameter type = (OFXChoiceParameter)ofx.FindParameterByName("Type_0");
            foreach (OFXChoice c in type.Choices) if (c.Name.Equals(shape, StringComparison.OrdinalIgnoreCase)) type.Value = c;
            OFXDouble2DParameter loc = (OFXDouble2DParameter)ofx.FindParameterByName("Location_0");
            OFXDoubleParameter w = (OFXDoubleParameter)ofx.FindParameterByName("Width_0");
            OFXDoubleParameter h = (OFXDoubleParameter)ofx.FindParameterByName("Height_0");
            bool moving = boxes.Count > 1;
            loc.IsAnimated = moving; w.IsAnimated = moving; h.IsAnimated = moving;
            long off = (ev.Start - at).FrameCount, evLen = ev.Length.FrameCount;
            foreach (double[] b in boxes)
            {
                long rel = (long)b[0] - off; // frame inside this piece
                if (moving && (rel < 0 || rel >= evLen)) continue;
                OFXDouble2D p = loc.Value;
                p.X = b[1];
                p.Y = 1 - b[2]; // the mask counts up from the bottom
                if (!moving) { loc.Value = p; w.Value = b[3]; h.Value = b[4]; break; }
                Timecode t = Timecode.FromFrames(rel);
                loc.SetValueAtTime(t, p);
                w.SetValueAtTime(t, b[3]);
                h.SetValueAtTime(t, b[4]);
            }
            n++;
        }
        if (n == 0) throw new ApplicationException("nothing on the track inside frames " + at.FrameCount + "-" + end.FrameCount);
        return n;
    }

    static float Clamp(float v, float lo, float hi)
    {
        return lo > hi ? (lo + hi) / 2 : Math.Max(lo, Math.Min(hi, v));
    }

    static long Len(string s)
    {
        long n = long.Parse(s.Trim());
        if (n <= 0) throw new ApplicationException("a range needs a length: " + s);
        return n;
    }

    // AddFx splits the top footage track at both ends of the range and puts the
    // plug-in (or filter package) with the preset on every piece inside it.
    static int AddFx(Vegas vegas, Timecode at, Timecode len, string name, string preset)
    {
        PlugInNode node = Find(vegas.VideoFX, name);
        if (node == null) throw new ApplicationException("no video effect named \"" + name + "\"");
        // VEGAS 18's script API shows a filter package as an empty shell (no id,
        // no contents; adding it fails inside VEGAS), so say so plainly.
        if (node.IsPackage)
            throw new ApplicationException("\"" + name + "\" is a filter package; VEGAS 18 scripts cannot add packages - use a plug-in + preset, or a zoom line");
        Track track = FootageTrack(vegas.Project, at);
        if (track == null) throw new ApplicationException("no footage on a video track at frame " + at.FrameCount);
        Timecode end = at + len;
        at = Snap(track, at);
        end = Snap(track, end);
        SplitAt(track, at);
        SplitAt(track, end);
        int n = 0;
        foreach (TrackEvent ev in track.Events)
        {
            if (ev.Start < at || ev.End > end) continue;
            Effect e = new Effect(node);
            ((VideoEvent)ev).Effects.Add(e);
            if (preset != "")
            {
                bool known = false;
                foreach (EffectPreset p in e.Presets) if (p.Name == preset) known = true;
                if (!known) throw new ApplicationException("\"" + name + "\" has no preset \"" + preset + "\"");
                e.Preset = preset;
            }
            n++;
        }
        if (n == 0) throw new ApplicationException("nothing on the track inside frames " + at.FrameCount + "-" + end.FrameCount);
        return n;
    }

    // Bleep is Jordan's censor (2026-10-14, vegas\censor-example.JPG): his speech
    // in the range is cut out of the speech track and the censor sound goes in
    // the gap, ON that track, trimmed to the gap, its own gain lowered to <dB>
    // (the blue handle in the middle of the event = FadeIn.Gain). The cut-out
    // speech is taken out of its group first - VEGAS deletes grouped events
    // together, and that would take his picture with it - and the rest of the
    // group is grouped again (VEGAS's own idiom: new group, add it, then events).
    static int Bleep(Project p, Timecode at, Timecode len, string file, double db, int trackNo)
    {
        if (!File.Exists(file)) throw new ApplicationException("no file " + file);
        AudioTrack track = null;
        int k = 0;
        foreach (Track t in p.Tracks)
        {
            if (!t.IsAudio()) continue;
            if (k++ == trackNo) { track = (AudioTrack)t; break; }
        }
        if (track == null) throw new ApplicationException("no audio track number " + trackNo);
        Timecode end = at + len;
        SplitAt(track, at);
        SplitAt(track, end);
        List<TrackEvent> speech = new List<TrackEvent>();
        foreach (TrackEvent ev in track.Events) if (ev.Start >= at && ev.End <= end) speech.Add(ev);
        if (speech.Count == 0) throw new ApplicationException("no speech on the track in frames " + at.FrameCount + "-" + end.FrameCount);
        foreach (TrackEvent ev in speech) Ungroup(p, ev);
        foreach (TrackEvent ev in speech) track.Events.Remove(ev);
        Media media = Media.CreateInstance(p, file);
        MediaStream stream = media.GetAudioStreamByIndex(0);
        if (stream == null) throw new ApplicationException("no audio in " + file);
        AudioEvent bleep = track.AddAudioEvent(at, len);
        bleep.AddTake(stream);
        bleep.Name = "censor";
        bleep.FadeIn.Gain = (float)Math.Pow(10.0, db / 20.0);
        return 1;
    }

    // Ungroup takes one event out of its group without touching the others:
    // the group is dissolved (an event cannot leave a group on its own without
    // E_FAIL) and the other members are grouped again.
    static void Ungroup(Project p, TrackEvent ev)
    {
        if (!ev.IsGrouped || ev.Group == null) return;
        TrackEventGroup g = ev.Group;
        List<TrackEvent> rest = new List<TrackEvent>();
        foreach (TrackEvent m in g) if (!m.Equals(ev)) rest.Add(m);
        p.TrackEventGroups.Remove(g);
        if (rest.Count < 2) return;
        TrackEventGroup ng = new TrackEventGroup();
        p.TrackEventGroups.Add(ng);
        foreach (TrackEvent m in rest) ng.Add(m);
    }

    // TrackFx puts plug-ins with presets, in order, on every video track that
    // shows the media (Jordan's "primary" tracks - two for a split screen, never
    // b-roll): his hj-starter package, one plug-in at a time (scripts cannot add
    // a filter package). Every plug-in and preset is checked before anything is
    // added; a plug-in a track already has is not added twice (a rerun is safe).
    //   trackfx <TAB> 0 <TAB> 0 <TAB> <media file> <TAB> <plug-in> <TAB> <preset> [<TAB> <plug-in> <TAB> <preset> ...]
    static int TrackFx(Vegas vegas, string[] f)
    {
        string media = f[3].Trim();
        List<PlugInNode> nodes = new List<PlugInNode>();
        List<string> presets = new List<string>();
        for (int i = 4; i + 1 < f.Length; i += 2)
        {
            PlugInNode node = FindLoose(vegas.VideoFX, f[i].Trim());
            if (node == null) throw new ApplicationException("no video effect named \"" + f[i].Trim() + "\" is installed");
            nodes.Add(node);
            presets.Add(f[i + 1].Trim());
        }
        List<Track> tracks = new List<Track>();
        foreach (Track t in vegas.Project.Tracks)
        {
            if (!t.IsVideo() || t.Name == "B-ROLL") continue;
            foreach (TrackEvent ev in t.Events)
            {
                if (ev.ActiveTake == null || ev.ActiveTake.Media == null) continue;
                if (ev.ActiveTake.Media.FilePath.Equals(media, StringComparison.OrdinalIgnoreCase)) { tracks.Add(t); break; }
            }
        }
        if (tracks.Count == 0) throw new ApplicationException("no video track shows " + media);
        int n = 0;
        foreach (Track t in tracks)
        {
            for (int i = 0; i < nodes.Count; i++)
            {
                bool has = false;
                foreach (Effect e in t.Effects)
                    if (e.PlugIn != null && e.PlugIn.Name == nodes[i].Name && e.CurrentPreset != null && e.CurrentPreset.Name == presets[i]) has = true;
                if (has) continue;
                Effect fx = new Effect(nodes[i]);
                t.Effects.Add(fx);
                bool known = false;
                foreach (EffectPreset ep in fx.Presets) if (ep.Name == presets[i]) known = true;
                if (!known) throw new ApplicationException("\"" + nodes[i].Name + "\" has no preset \"" + presets[i] + "\"");
                fx.Preset = presets[i];
                n++;
            }
        }
        return n;
    }

    // Place puts one of Jordan's files at the frame. overlay: its picture on a
    // B-ROLL track above his (a still for the length; his voice goes on).
    // insert: everything at or after the frame moves later by the length, and
    // the clip's picture and sound go in the gap on his own tracks, grouped.
    static int Place(Project p, bool insert, Timecode at, Timecode len, string file, int speechTrack)
    {
        if (!File.Exists(file)) throw new ApplicationException("no file " + file);
        Media media = Media.CreateInstance(p, file);
        MediaStream vs = media.GetVideoStreamByIndex(0);
        if (vs == null) throw new ApplicationException("no picture in " + file);
        if (!insert)
        {
            VideoTrack top = null;
            foreach (Track t in p.Tracks) if (t.IsVideo() && t.Name == "B-ROLL") top = (VideoTrack)t;
            if (top == null)
            {
                top = new VideoTrack(p, 0, "B-ROLL");
                p.Tracks.Add(top);
            }
            VideoEvent ov = top.AddVideoEvent(at, len);
            ov.AddTake(vs);
            return 1;
        }
        Track footage = null;
        AudioTrack speech = null;
        int k = 0;
        foreach (Track t in p.Tracks)
        {
            if (t.IsVideo() && footage == null && t.Name != "B-ROLL") footage = t;
            if (t.IsAudio() && k++ == speechTrack) speech = (AudioTrack)t;
        }
        if (footage == null || speech == null) throw new ApplicationException("no footage or speech track to insert into");
        foreach (Track t in p.Tracks) SplitAt(t, at);
        // later first, to absolute places: nothing overlaps on the way, and a
        // grouped partner VEGAS may have moved already lands where it belongs
        List<TrackEvent> later = new List<TrackEvent>();
        foreach (Track t in p.Tracks) foreach (TrackEvent ev in t.Events) if (ev.Start >= at) later.Add(ev);
        later.Sort(delegate (TrackEvent a, TrackEvent b) { return b.Start.CompareTo(a.Start); });
        List<Timecode> to = new List<Timecode>();
        foreach (TrackEvent ev in later) to.Add(ev.Start + len);
        for (int i = 0; i < later.Count; i++) later[i].Start = to[i];
        foreach (Marker m in p.Markers) if (m.Position >= at) m.Position = m.Position + len;
        foreach (Region r in p.Regions) if (r.Position >= at) r.Position = r.Position + len;
        VideoEvent ve = ((VideoTrack)footage).AddVideoEvent(at, len);
        ve.AddTake(vs);
        TrackEventGroup g = new TrackEventGroup();
        p.TrackEventGroups.Add(g);
        g.Add(ve);
        MediaStream au = media.GetAudioStreamByIndex(0);
        if (au != null)
        {
            AudioEvent ae = speech.AddAudioEvent(at, len);
            ae.AddTake(au);
            g.Add(ae);
        }
        return 1;
    }

    // FindLoose: the exact name, else VEGAS's own ("Color Curves" is "VEGAS
    // Color Curves"), else the one plug-in whose name contains it ("Beauty Box").
    static PlugInNode FindLoose(PlugInNode root, string name)
    {
        PlugInNode hit = Find(root, name);
        if (hit == null) hit = Find(root, "VEGAS " + name);
        if (hit != null) return hit;
        List<PlugInNode> all = new List<PlugInNode>();
        Collect(root, all);
        PlugInNode only = null;
        foreach (PlugInNode c in all)
        {
            if (c.IsContainer || c.Name.IndexOf(name, StringComparison.OrdinalIgnoreCase) < 0) continue;
            if (only != null && only.Name != c.Name) throw new ApplicationException("\"" + name + "\" matches both \"" + only.Name + "\" and \"" + c.Name + "\"");
            only = c;
        }
        return only;
    }

    static void Collect(PlugInNode root, List<PlugInNode> all)
    {
        foreach (PlugInNode c in root)
        {
            all.Add(c);
            if (c.IsContainer) Collect(c, all);
        }
    }

    static PlugInNode Find(PlugInNode root, string name)
    {
        PlugInNode hit = root.GetChildByName(name);
        if (hit != null) return hit;
        foreach (PlugInNode c in root)
        {
            if (!c.IsContainer) continue;
            hit = Find(c, name);
            if (hit != null) return hit;
        }
        return null;
    }

    // FootageTrack: the topmost video track with a media event (not a title or
    // other generated media) covering the frame.
    static Track FootageTrack(Project project, Timecode at)
    {
        foreach (Track t in project.Tracks)
        {
            if (!t.IsVideo()) continue;
            foreach (TrackEvent ev in t.Events)
            {
                if (ev.Start > at || ev.End <= at || ev.ActiveTake == null) continue;
                Media m = ev.ActiveTake.Media;
                if (m != null && !m.IsGenerated()) return t;
            }
        }
        return null;
    }

    // Snap moves a range edge onto an existing cut within SnapFrames, so the
    // effect never leaves a 1-3 frame sliver next to one of Jordan's cuts
    // ("no 1-frame slivers", 2026-10-06).
    const long SnapFrames = 3;

    static Timecode Snap(Track track, Timecode t)
    {
        Timecode best = t;
        long bestGap = SnapFrames + 1;
        foreach (TrackEvent ev in track.Events)
        {
            foreach (Timecode edge in new Timecode[] { ev.Start, ev.End })
            {
                long gap = Math.Abs(edge.FrameCount - t.FrameCount);
                if (gap < bestGap) { best = edge; bestGap = gap; }
            }
        }
        return best;
    }

    // SplitAt cuts whatever event straddles the position (a snapshot, because
    // Split adds to the list while we look) - same as BeckyCut.cs.
    static void SplitAt(Track track, Timecode at)
    {
        List<TrackEvent> snapshot = new List<TrackEvent>();
        foreach (TrackEvent ev in track.Events) snapshot.Add(ev);
        foreach (TrackEvent ev in snapshot)
            if (ev.Start < at && ev.End > at) ev.Split(at - ev.Start);
    }

    // Duck lowers every audio track that has sound in the range by dB, with a
    // 2-frame ramp each side so the cut is clean. Volume envelope Y is linear gain.
    static int Duck(Project project, Timecode at, Timecode len, double db)
    {
        Timecode ramp = Timecode.FromFrames(2), end = at + len;
        double gain = db <= -96 ? 0 : Math.Pow(10.0, db / 20.0);
        int n = 0;
        foreach (Track t in project.Tracks)
        {
            if (!t.IsAudio() || !HasSound(t, at, end)) continue;
            Envelope env = t.Envelopes.FindByType(EnvelopeType.Volume);
            if (env == null)
            {
                env = new Envelope(EnvelopeType.Volume);
                t.Envelopes.Add(env);
            }
            Timecode before = at > ramp ? at - ramp : Timecode.FromFrames(0);
            double was = env.ValueAt(before), after = env.ValueAt(end + ramp);
            SetPoint(env, before, was);
            SetPoint(env, at, gain);
            SetPoint(env, end, gain);
            SetPoint(env, end + ramp, after);
            n++;
        }
        if (n == 0) throw new ApplicationException("no audio in frames " + at.FrameCount + "-" + end.FrameCount);
        return n;
    }

    static bool HasSound(Track t, Timecode a, Timecode b)
    {
        foreach (TrackEvent ev in t.Events) if (ev.Start < b && ev.End > a && !ev.Mute) return true;
        return false;
    }

    static void SetPoint(Envelope env, Timecode x, double y)
    {
        EnvelopePoint p = env.Points.GetPointAtX(x);
        if (p != null) p.Y = y;
        else env.Points.Add(new EnvelopePoint(x, y));
    }

    // PlaceAudio puts a sound file (e.g. a bleep) on the named audio track at the frame.
    static int PlaceAudio(Project project, Timecode at, string path, string trackName)
    {
        if (!File.Exists(path)) throw new ApplicationException("no file " + path);
        AudioTrack track = null;
        foreach (Track t in project.Tracks)
            if (t.IsAudio() && t.Name == trackName) track = (AudioTrack)t;
        if (track == null)
        {
            track = new AudioTrack(project, project.Tracks.Count, trackName);
            project.Tracks.Add(track);
        }
        Media media = Media.CreateInstance(project, path);
        MediaStream stream = media.GetAudioStreamByIndex(0);
        if (stream == null) throw new ApplicationException("no audio in " + path);
        AudioEvent ev = track.AddAudioEvent(at, stream.Length);
        ev.AddTake(stream);
        return 1;
    }

    // JobPath reads "job" from %LOCALAPPDATA%\BeckyVegas\script-args.json, which
    // becky-vegas run_script rewrites before every run (same as BeckyMarks.cs).
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
