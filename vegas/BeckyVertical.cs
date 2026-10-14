/*
 * BeckyVertical.cs - turn the open (already Saved-As "-VERTICAL") project into vertical shorts,
 * the way Jordan does it by hand (vegas\JORDANS-WORKFLOWS.md, section 2).
 * ----------------------------------------------------------------------------
 * becky-vertical decides everything (where his face is, the layout of each short, the caption
 * lines, the hook, the titles); this script only applies it - "wrapper applies, becky decides".
 *
 * Job file: TAB-separated, one item per line, positions in FRAMES of the timeline:
 *   size   <TAB> <width> <TAB> <height>          File > Properties: 1080 x 1920
 *   match                                        "Match Output Aspect" on every video event
 *                                                (VEGAS's own Script Menu\Aspectratio.js math);
 *                                                text and other generated media are left alone
 *   place  <TAB> <layer> <TAB> <first frame> <TAB> <length> <TAB> <x0> <TAB> <y0> <TAB> <x1> <TAB> <y1>
 *          <TAB> <band top> <TAB> <band bottom>
 *          Shows the source rectangle (x0,y0)-(x1,y1) (fractions of the source picture, top-left
 *          origin) in the output band between <band top> and <band bottom> (fractions of the output
 *          height, from the top), full output width. Pure Pan/Crop: the part of the Pan/Crop box
 *          that falls outside the source is see-through, so no mask is needed. Layer 0 is the
 *          footage track itself; layer 1 is a copy of the same pieces on a "Becky Layout" track
 *          directly above it (SPLIT: two people stacked; SCREENCAST: the screen above, the face
 *          below). The footage is split at both ends of the range first.
 *   text   <TAB> <style> <TAB> <first frame> <TAB> <length> <TAB> <x> <TAB> <y> <TAB> <scale> <TAB> <words>
 *          A VEGAS Titles & Text event. style caption = Jordan's "becky-captions" preset on the
 *          "Becky Captions" track; style hook = the same preset in Montserrat Black, no shadow (his
 *          title on the September goodbye-youtube-VERTICAL, Scale 1.52) on a "Becky Hook" track
 *          above it. x, y = Titles & Text Location (fractions, y measured from the BOTTOM, VEGAS's
 *          way); scale = Titles & Text Scale.
 *   region <TAB> <first frame> <TAB> <length> <TAB> <name>
 *   render <TAB> <first frame> <TAB> <length> <TAB> <render template name> <TAB> <out.mp4>
 *          Renders that stretch of the open project (his step 7: "Render all regions").
 *          Refuses to write over a file that exists.
 *
 * Run: becky-vegas run_script path=<this file> job=<job.txt>   (one Undo step; never saves)
 * Writes <job>.result.txt: "ok placed=N text=N regions=N matched=N rendered=N" or "error: ...".
 * ----------------------------------------------------------------------------
 */

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Text;
using System.Text.RegularExpressions;
using ScriptPortal.Vegas;

public class EntryPoint
{
    const string CaptionTrack = "Becky Captions", HookTrack = "Becky Hook", LayoutTrack = "Becky Layout";
    const string CaptionPreset = "becky-captions", HookFont = "Montserrat Black";

    int placed, texts, regions, matched, rendered;

    public void FromVegas(Vegas vegas)
    {
        string job = JobPath();
        string result = (job == "" ? Path.Combine(Path.GetTempPath(), "BeckyVertical") : job) + ".result.txt";
        try
        {
            if (job == "" || !File.Exists(job)) throw new ApplicationException("no job file (becky-vegas run_script ... job=<file>)");
            if (vegas.Project == null) throw new ApplicationException("no project is open");
            using (UndoBlock u = new UndoBlock("Becky: vertical shorts"))
            {
                foreach (string line in File.ReadAllLines(job))
                {
                    string[] f = line.Split('\t');
                    if (f.Length == 0 || f[0].Trim() == "") continue;
                    try
                    {
                        Apply(vegas, f);
                    }
                    catch (Exception ex)
                    {
                        string where = ex.StackTrace == null ? "" : ex.StackTrace.Trim().Split('\n')[0].Trim();
                        throw new ApplicationException("\"" + line.Trim().Replace('\t', ' ') + "\": " + ex.Message + " (" + where + ")");
                    }
                }
            }
            File.WriteAllText(result, "ok placed=" + placed + " text=" + texts + " regions=" + regions + " matched=" + matched + " rendered=" + rendered);
        }
        catch (Exception ex)
        {
            try { File.WriteAllText(result, "error: " + ex.Message); } catch { }
        }
    }

    void Apply(Vegas vegas, string[] f)
    {
        Project p = vegas.Project;
        switch (f[0].Trim())
        {
            case "size":
                p.Video.Width = int.Parse(f[1].Trim());
                p.Video.Height = int.Parse(f[2].Trim());
                break;
            case "match":
                MatchAll(p);
                break;
            case "place":
                Place(p, int.Parse(f[1].Trim()), Frames(f[2]), Frames(f[3]), Num(f[4]), Num(f[5]), Num(f[6]), Num(f[7]), Num(f[8]), Num(f[9]));
                break;
            case "text":
                AddText(vegas, f[1].Trim(), Frames(f[2]), Frames(f[3]), Num(f[4]), Num(f[5]), Num(f[6]), f[7]);
                break;
            case "region":
                p.Regions.Add(new Region(Timecode.FromFrames(Frames(f[1])), Timecode.FromFrames(Frames(f[2])), f[3].Trim()));
                regions++;
                break;
            case "render":
                Render(vegas, Frames(f[1]), Frames(f[2]), f[3].Trim(), f[4].Trim());
                break;
            default:
                throw new ApplicationException("unknown line");
        }
    }

    // ---------------------------------------------------------------- framing

    // MatchAll is Aspectratio.js on every footage event: each Pan/Crop keyframe keeps its centre
    // and loses width (wide source) or height (tall source) until it has the output's aspect.
    void MatchAll(Project p)
    {
        double outAspect = p.Video.PixelAspectRatio * p.Video.Width / p.Video.Height;
        foreach (Track t in p.Tracks)
        {
            if (!t.IsVideo()) continue;
            foreach (TrackEvent ev in t.Events)
            {
                VideoStream vs = Footage(ev);
                if (vs == null) continue;
                foreach (VideoMotionKeyframe k in ((VideoEvent)ev).VideoMotion.Keyframes)
                {
                    double rot = k.Rotation;
                    k.RotateBy(-rot);
                    double w = Math.Abs(k.TopRight.X - k.TopLeft.X), h = Math.Abs(k.BottomLeft.Y - k.TopLeft.Y);
                    double cur = vs.PixelAspectRatio * w / h, cx = k.Center.X, cy = k.Center.Y;
                    double sx = cur < outAspect ? 1 : outAspect / cur, sy = cur < outAspect ? cur / outAspect : 1;
                    k.Bounds = new VideoMotionBounds(Shrink(k.TopLeft, cx, cy, sx, sy), Shrink(k.TopRight, cx, cy, sx, sy),
                                                     Shrink(k.BottomRight, cx, cy, sx, sy), Shrink(k.BottomLeft, cx, cy, sx, sy));
                    k.RotateBy(rot);
                }
                matched++;
            }
        }
    }

    static VideoMotionVertex Shrink(VideoMotionVertex v, double cx, double cy, double sx, double sy)
    {
        return new VideoMotionVertex((float)((v.X - cx) * sx + cx), (float)((v.Y - cy) * sy + cy));
    }

    // Place puts a source rectangle into a band of the output with one Pan/Crop box. The box has
    // the output's aspect; the source rectangle fills the band's full width, so the box is
    // (x1-x0) source pixels wide and starts (band top / scale) above the rectangle.
    void Place(Project p, int layer, long f0, long len, double x0, double y0, double x1, double y1, double top, double bottom)
    {
        Track track = FootageTrack(p, Timecode.FromFrames(f0));
        if (track == null) throw new ApplicationException("no footage on a video track at frame " + f0);
        Timecode at = Timecode.FromFrames(f0), end = Timecode.FromFrames(f0 + len);
        SplitAt(track, at);
        SplitAt(track, end);
        Track target = track;
        if (layer == 1)
        {
            target = FindTrack(p, LayoutTrack);
            if (target == null)
            {
                target = new VideoTrack(p, track.Index, LayoutTrack);
                p.Tracks.Add(target);
            }
        }
        List<TrackEvent> inside = new List<TrackEvent>();
        foreach (TrackEvent ev in track.Events)
            if (ev.Start >= at && ev.End <= end) inside.Add(ev);
        if (inside.Count == 0) throw new ApplicationException("nothing on the footage track inside frames " + f0 + "-" + (f0 + len));
        double W = p.Video.Width, H = p.Video.Height;
        foreach (TrackEvent src in inside)
        {
            TrackEvent ev = layer == 1 ? src.Copy(target, src.Start) : src;
            VideoStream vs = Footage(ev);
            if (vs == null) continue;
            double sw = vs.Width, sh = vs.Height;
            double rx0 = x0 * sw, rx1 = x1 * sw, ry0 = y0 * sh;
            double k = W / (rx1 - rx0);                // output pixels per source pixel
            double boxW = rx1 - rx0, boxH = H / k;      // the box has the output's aspect
            double bx0 = rx0, by0 = ry0 - top * H / k;  // the rectangle sits at the band's top
            VideoMotion vm = ((VideoEvent)ev).VideoMotion;
            while (vm.Keyframes.Count > 1) vm.Keyframes.Remove(vm.Keyframes[vm.Keyframes.Count - 1]);
            VideoMotionKeyframe key = vm.Keyframes[0];
            float a = (float)bx0, b = (float)by0, c = (float)(bx0 + boxW), d = (float)(by0 + boxH);
            key.Bounds = new VideoMotionBounds(new VideoMotionVertex(a, b), new VideoMotionVertex(c, b), new VideoMotionVertex(c, d), new VideoMotionVertex(a, d));
            placed++;
        }
    }

    // Footage is the video stream of a real media file under the event (not a title).
    static VideoStream Footage(TrackEvent ev)
    {
        if (!(ev is VideoEvent) || ev.ActiveTake == null || ev.ActiveTake.Media == null || ev.ActiveTake.Media.IsGenerated()) return null;
        return ev.ActiveTake.MediaStream as VideoStream;
    }

    static Track FootageTrack(Project project, Timecode at)
    {
        foreach (Track t in project.Tracks)
        {
            if (!t.IsVideo() || t.Name == LayoutTrack) continue;
            foreach (TrackEvent ev in t.Events)
                if (ev.Start <= at && ev.End > at && Footage(ev) != null) return t;
        }
        return null;
    }

    static Track FindTrack(Project p, string name)
    {
        foreach (Track t in p.Tracks)
            if (t.IsVideo() && string.Equals(t.Name, name, StringComparison.OrdinalIgnoreCase)) return t;
        return null;
    }

    static void SplitAt(Track track, Timecode at)
    {
        List<TrackEvent> snapshot = new List<TrackEvent>();
        foreach (TrackEvent ev in track.Events) snapshot.Add(ev);
        foreach (TrackEvent ev in snapshot)
            if (ev.Start < at && ev.End > at) ev.Split(at - ev.Start);
    }

    // ------------------------------------------------------------------- text

    void Render(Vegas vegas, long f0, long len, string template, string outPath)
    {
        if (File.Exists(outPath)) throw new ApplicationException(outPath + " already exists - never written over");
        RenderTemplate tpl = null;
        foreach (Renderer r in vegas.Renderers)
            foreach (RenderTemplate t in r.Templates)
                if (tpl == null && t.IsValid() && t.Name == template) tpl = t;
        if (tpl == null) throw new ApplicationException("no render template named \"" + template + "\"");
        Directory.CreateDirectory(Path.GetDirectoryName(outPath));
        RenderStatus st = vegas.Render(outPath, tpl, Timecode.FromFrames(f0), Timecode.FromFrames(len));
        if (st != RenderStatus.Complete) throw new ApplicationException("render of " + outPath + " ended " + st);
        rendered++;
    }

    void AddText(Vegas vegas, string style, long f0, long len, double x, double y, double scaleValue, string words)
    {
        Project p = vegas.Project;
        bool hook = style == "hook";
        string name = hook ? HookTrack : CaptionTrack;
        Track track = FindTrack(p, name);
        if (track == null)
        {
            // index 0 = the TOP track (Tracks.Insert throws in VEGAS 18); the hook goes above the captions
            track = new VideoTrack(p, 0, name);
            p.Tracks.Add(track);
        }
        PlugInNode gen = vegas.Generators.GetChildByName("VEGAS Titles & Text");
        if (gen == null) gen = vegas.Generators.GetChildByName("Titles & Text");
        if (gen == null) throw new ApplicationException("no \"VEGAS Titles & Text\" generator");
        Media media = new Media(gen);
        Effect g = media.Generator;
        if (g == null || g.OFXEffect == null) throw new ApplicationException("Titles & Text has no parameters");
        bool preset = false;
        foreach (EffectPreset ep in g.Presets)
            if (ep.Name != null && ep.Name.Equals(CaptionPreset, StringComparison.OrdinalIgnoreCase)) { g.Preset = ep.Name; preset = true; break; }
        if (!preset) throw new ApplicationException("the \"" + CaptionPreset + "\" Titles & Text preset is missing");
        OFXEffect ofx = g.OFXEffect;
        OFXStringParameter text = (OFXStringParameter)ofx.FindParameterByName("Text");
        string rtf = ReplaceRtfWords(text.Value, words.Trim().Replace("\\n", "\n")); // the job's "\n" is a line break
        if (rtf == null) throw new ApplicationException("the preset's text could not be swapped");
        if (hook) rtf = Regex.Replace(rtf, @"(\\fcharset0 )[^;]+;", "$1" + HookFont + ";");
        text.Value = rtf;
        text.ParameterChanged();
        OFXDouble2DParameter loc = (OFXDouble2DParameter)ofx.FindParameterByName("Location");
        OFXDouble2D v = loc.Value;
        v.X = x;
        v.Y = y;
        loc.Value = v;
        loc.ParameterChanged();
        OFXDoubleParameter scale = (OFXDoubleParameter)ofx.FindParameterByName("Scale");
        scale.Value = scaleValue;
        scale.ParameterChanged();
        if (hook)
        {
            OFXBooleanParameter shadow = (OFXBooleanParameter)ofx.FindParameterByName("ShadowEnable");
            shadow.Value = false;
            shadow.ParameterChanged();
        }
        MediaStream stream = media.Streams.GetItemByMediaType(MediaType.Video, 0);
        VideoEvent ev = ((VideoTrack)track).AddVideoEvent(Timecode.FromFrames(f0), Timecode.FromFrames(len));
        ev.Takes.Add(new Take(stream));
        texts++;
    }

    // ReplaceRtfWords swaps the literal words in the preset's RTF and keeps every control word
    // (font, size, bold, centring) - the same walk as BeckyCaptions.cs.
    static string ReplaceRtfWords(string rtf, string text)
    {
        if (string.IsNullOrEmpty(rtf)) return null;
        int first = -1, last = -1, depth = 0, i = 0;
        while (i < rtf.Length)
        {
            char c = rtf[i];
            if (c == '{') { depth++; i++; continue; }
            if (c == '}') { depth--; i++; continue; }
            if (c == '\\')
            {
                int j = i + 1;
                if (j < rtf.Length && char.IsLetter(rtf[j]) && rtf[j] < 128)
                {
                    while (j < rtf.Length && char.IsLetter(rtf[j]) && rtf[j] < 128) j++;
                    if (j < rtf.Length && rtf[j] == '-') j++;
                    while (j < rtf.Length && rtf[j] >= '0' && rtf[j] <= '9') j++;
                    if (j < rtf.Length && rtf[j] == ' ') j++;
                    i = j;
                    continue;
                }
                if (depth == 1) { if (first < 0) first = i; last = i + 2; }
                i += 2;
                continue;
            }
            if (c == '\r' || c == '\n') { i++; continue; }
            if (depth == 1) { if (first < 0) first = i; last = i + 1; }
            i++;
        }
        if (first < 0 || last <= first) return null;
        return rtf.Substring(0, first) + EscapeRtf(text) + rtf.Substring(last);
    }

    static string EscapeRtf(string text)
    {
        StringBuilder sb = new StringBuilder();
        foreach (char c in text)
        {
            if (c == '\\') sb.Append("\\\\");
            else if (c == '{') sb.Append("\\{");
            else if (c == '}') sb.Append("\\}");
            else if (c == '\n') sb.Append("\\par ");
            else if (c == '\r') { }
            else if (c > 127) sb.Append("\\'").Append(((int)c & 0xFF).ToString("x2"));
            else sb.Append(c);
        }
        return sb.Length == 0 ? " " : sb.ToString();
    }

    // ---------------------------------------------------------------- helpers

    static long Frames(string s)
    {
        return long.Parse(s.Trim(), CultureInfo.InvariantCulture);
    }

    static double Num(string s)
    {
        return double.Parse(s.Trim(), CultureInfo.InvariantCulture);
    }

    static string JobPath()
    {
        try
        {
            string p = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "BeckyVegas", "script-args.json");
            if (!File.Exists(p)) return "";
            Match mm = Regex.Match(File.ReadAllText(p), "\"job\"\\s*:\\s*\"((?:[^\"\\\\]|\\\\.)*)\"");
            if (!mm.Success) return "";
            return mm.Groups[1].Value.Replace("\\\\", "\\").Replace("\\\"", "\"").Replace("\\/", "/");
        }
        catch { return ""; }
    }
}
