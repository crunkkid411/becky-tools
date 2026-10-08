/*
 * BeckyDumpProject.cs
 * ----------------------------------------------------------------------------
 * READ-ONLY export of every edit decision in a VEGAS project, for learning how
 * Jordan edits (Jordan, 2026-10-07: "the Vegas project files can be READ for
 * information, but they also should not be altered").
 *
 *   set BECKY_DUMP_VEG=C:\scratch\copy-of-project.veg
 *   set BECKY_DUMP_OUT=C:\scratch\copy-of-project.edits.json   (optional)
 *   vegas180.exe -SCRIPT:<this file>
 *
 * Never point it at the original: becky-editlearn copies the .veg to a scratch
 * folder first. This script never calls Save; it opens, reads, writes JSON
 * next to nothing but BECKY_DUMP_OUT, and exits.
 *
 * Output (one JSON object): project video settings, markers, regions, and per
 * track: name, type, mute/solo, effects, envelopes, and per event: timeline
 * start/length, source file + offset into it (the cut), playback rate,
 * fades, mute, effects with their parameter values (blur, pixelate, masks),
 * Pan/Crop keyframes (zooms), envelopes, and Titles & Text content.
 * ----------------------------------------------------------------------------
 */

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Text;
using ScriptPortal.Vegas;

public class EntryPoint
{
    static readonly CultureInfo Inv = CultureInfo.InvariantCulture;

    public void FromVegas(Vegas vegas)
    {
        string path = Environment.GetEnvironmentVariable("BECKY_DUMP_VEG");
        string outPath = Environment.GetEnvironmentVariable("BECKY_DUMP_OUT");
        if (string.IsNullOrEmpty(outPath) && !string.IsNullOrEmpty(path)) outPath = path + ".edits.json";
        StringBuilder sb = new StringBuilder();
        try
        {
            if (string.IsNullOrEmpty(path) || !File.Exists(path)) throw new Exception("BECKY_DUMP_VEG is missing or not a file");
            vegas.OpenFile(path);
            Project p = vegas.Project;
            sb.Append("{\"project\":").Append(Str(path));
            sb.Append(",\"width\":").Append(p.Video.Width).Append(",\"height\":").Append(p.Video.Height);
            sb.Append(",\"fps\":").Append(Num(p.Video.FrameRate));
            sb.Append(",\"markers\":[");
            for (int i = 0; i < p.Markers.Count; i++)
            {
                if (i > 0) sb.Append(',');
                sb.Append("{\"t\":").Append(Sec(p.Markers[i].Position)).Append(",\"label\":").Append(Str(p.Markers[i].Label)).Append('}');
            }
            sb.Append("],\"regions\":[");
            for (int i = 0; i < p.Regions.Count; i++)
            {
                if (i > 0) sb.Append(',');
                Region r = p.Regions[i];
                sb.Append("{\"t\":").Append(Sec(r.Position)).Append(",\"len\":").Append(Sec(r.Length)).Append(",\"label\":").Append(Str(r.Label)).Append('}');
            }
            sb.Append("],\"tracks\":[");
            bool firstTrack = true;
            foreach (Track t in p.Tracks)
            {
                if (!firstTrack) sb.Append(',');
                firstTrack = false;
                sb.Append("{\"index\":").Append(t.Index).Append(",\"name\":").Append(Str(t.Name));
                sb.Append(",\"type\":").Append(Str(t is VideoTrack ? "video" : "audio"));
                sb.Append(",\"mute\":").Append(t.Mute ? "true" : "false").Append(",\"solo\":").Append(t.Solo ? "true" : "false");
                sb.Append(",\"effects\":"); Effects(sb, t.Effects);
                sb.Append(",\"envelopes\":"); Envs(sb, t.Envelopes);
                sb.Append(",\"events\":[");
                bool firstEv = true;
                foreach (TrackEvent ev in t.Events)
                {
                    if (!firstEv) sb.Append(',');
                    firstEv = false;
                    Event(sb, ev);
                }
                sb.Append("]}");
            }
            sb.Append("]}");
        }
        catch (Exception ex)
        {
            sb.Length = 0;
            sb.Append("{\"error\":").Append(Str(ex.ToString())).Append('}');
        }
        try { if (!string.IsNullOrEmpty(outPath)) File.WriteAllText(outPath, sb.ToString(), new UTF8Encoding(false)); } catch { }
        vegas.Exit();
    }

    static void Event(StringBuilder sb, TrackEvent ev)
    {
        sb.Append("{\"start\":").Append(Sec(ev.Start)).Append(",\"len\":").Append(Sec(ev.Length));
        sb.Append(",\"rate\":").Append(Num(ev.PlaybackRate)).Append(",\"mute\":").Append(ev.Mute ? "true" : "false");
        sb.Append(",\"name\":").Append(Str(ev.Name));
        sb.Append(",\"fade_in\":").Append(Sec(ev.FadeIn.Length)).Append(",\"fade_out\":").Append(Sec(ev.FadeOut.Length));
        sb.Append(",\"group\":").Append(ev.IsGrouped ? "true" : "false");
        Take tk = ev.ActiveTake;
        if (tk != null)
        {
            sb.Append(",\"offset\":").Append(Sec(tk.Offset)).Append(",\"stream\":").Append(tk.StreamIndex);
            Media m = tk.Media;
            if (m != null)
            {
                sb.Append(",\"source\":").Append(Str(m.FilePath));
                if (m.Generator != null)
                {
                    sb.Append(",\"generator\":"); OneEffect(sb, m.Generator);
                }
            }
        }
        VideoEvent ve = ev as VideoEvent;
        if (ve != null)
        {
            sb.Append(",\"effects\":"); Effects(sb, ve.Effects);
            sb.Append(",\"envelopes\":"); Envs(sb, ve.Envelopes);
            sb.Append(",\"pan_crop\":[");
            for (int i = 0; i < ve.VideoMotion.Keyframes.Count; i++)
            {
                if (i > 0) sb.Append(',');
                VideoMotionKeyframe k = ve.VideoMotion.Keyframes[i];
                sb.Append("{\"t\":").Append(Sec(k.Position)).Append(",\"type\":").Append(Str(k.Type.ToString()));
                sb.Append(",\"center\":[").Append(Num(k.Center.X)).Append(',').Append(Num(k.Center.Y)).Append(']');
                sb.Append(",\"rotation\":").Append(Num(k.Rotation));
                sb.Append(",\"tl\":[").Append(Num(k.TopLeft.X)).Append(',').Append(Num(k.TopLeft.Y)).Append(']');
                sb.Append(",\"br\":[").Append(Num(k.BottomRight.X)).Append(',').Append(Num(k.BottomRight.Y)).Append("]}");
            }
            sb.Append(']');
        }
        AudioEvent ae = ev as AudioEvent;
        if (ae != null)
        {
            sb.Append(",\"effects\":"); Effects(sb, ae.Effects);
            sb.Append(",\"normalize\":").Append(ae.Normalize ? "true" : "false");
            sb.Append(",\"pitch_semis\":").Append(Num(ae.PitchSemis));
        }
        sb.Append('}');
    }

    static void Effects(StringBuilder sb, Effects fx)
    {
        sb.Append('[');
        for (int i = 0; i < fx.Count; i++)
        {
            if (i > 0) sb.Append(',');
            OneEffect(sb, fx[i]);
        }
        sb.Append(']');
    }

    static void OneEffect(StringBuilder sb, Effect e)
    {
        string name = "";
        try { name = e.PlugIn != null ? e.PlugIn.Name : e.Description; } catch { }
        sb.Append("{\"name\":").Append(Str(name)).Append(",\"bypass\":").Append(e.Bypass ? "true" : "false");
        sb.Append(",\"keyframes\":").Append(e.Keyframes.Count);
        sb.Append(",\"params\":{");
        if (e.IsOFX && e.OFXEffect != null)
        {
            bool first = true;
            foreach (OFXParameter prm in e.OFXEffect.Parameters)
            {
                string v = ParamValue(prm);
                if (v == null) continue;
                if (!first) sb.Append(',');
                first = false;
                sb.Append(Str(prm.Name)).Append(':').Append(Str(v + (prm.IsAnimated ? " [animated]" : "")));
            }
        }
        sb.Append("}}");
    }

    // OFX parameter subclasses each carry their own Value type; read it by name.
    static string ParamValue(OFXParameter prm)
    {
        try
        {
            System.Reflection.PropertyInfo pi = prm.GetType().GetProperty("Value");
            if (pi == null) return null;
            object v = pi.GetValue(prm, null);
            if (v == null) return null;
            string s = Convert.ToString(v, Inv);
            return s.Length > 4000 ? s.Substring(0, 4000) : s;
        }
        catch { return null; }
    }

    static void Envs(StringBuilder sb, Envelopes envs)
    {
        sb.Append('[');
        bool first = true;
        foreach (Envelope env in envs)
        {
            if (!first) sb.Append(',');
            first = false;
            sb.Append("{\"type\":").Append(Str(env.Type.ToString())).Append(",\"points\":[");
            for (int i = 0; i < env.Points.Count; i++)
            {
                if (i > 0) sb.Append(',');
                sb.Append('[').Append(Sec(env.Points[i].X)).Append(',').Append(Num(env.Points[i].Y)).Append(']');
            }
            sb.Append("]}");
        }
        sb.Append(']');
    }

    static string Sec(Timecode tc) { return (tc.Nanos * 1e-7).ToString("0.#####", Inv); }
    static string Num(double d) { return double.IsNaN(d) || double.IsInfinity(d) ? "null" : d.ToString("0.######", Inv); }

    static string Str(string s)
    {
        if (s == null) return "null";
        StringBuilder b = new StringBuilder("\"");
        foreach (char c in s)
        {
            if (c == '"') b.Append("\\\"");
            else if (c == '\\') b.Append("\\\\");
            else if (c < 0x20) b.Append("\\u").Append(((int)c).ToString("x4"));
            else b.Append(c);
        }
        return b.Append('"').ToString();
    }
}
