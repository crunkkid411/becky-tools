/*
 * BeckyRenderRanges.cs - render short stretches of a project so a person or a model can LOOK at them.
 * ----------------------------------------------------------------------------
 * becky-tools rule: "an LLM must watch the output before it ships". A headless
 * vegas.SaveSnapshot() returns blank frames, so the proof that an effect landed is
 * VEGAS rendering the stretch itself. Read-only on the project: it never saves.
 *
 *   BECKY_RR_VEG=<project.veg> BECKY_RR_LIST=<list.txt> vegas180.exe -SCRIPT:<this file>
 * list.txt, TAB-separated, one per line:  <first frame> <TAB> <length in frames> <TAB> <out.mp4>
 * Writes <list.txt>.result.txt: "ok rendered=N template=<name>" or "error: ...".
 * ----------------------------------------------------------------------------
 */

using System;
using System.IO;
using ScriptPortal.Vegas;

public class EntryPoint
{
    public void FromVegas(Vegas vegas)
    {
        string veg = Environment.GetEnvironmentVariable("BECKY_RR_VEG");
        string list = Environment.GetEnvironmentVariable("BECKY_RR_LIST");
        string result = (string.IsNullOrEmpty(list) ? Path.Combine(Path.GetTempPath(), "BeckyRenderRanges") : list) + ".result.txt";
        try
        {
            if (string.IsNullOrEmpty(veg) || !File.Exists(list)) throw new ApplicationException("needs BECKY_RR_VEG and BECKY_RR_LIST");
            if (!vegas.OpenProject(veg)) throw new ApplicationException("could not open " + veg);
            RenderTemplate tpl = Mp4Template(vegas);
            if (tpl == null) throw new ApplicationException("no MP4 render template found");
            int n = 0;
            foreach (string line in File.ReadAllLines(list))
            {
                string[] f = line.Split('\t');
                if (f.Length < 3) continue;
                RenderStatus st = vegas.Render(f[2].Trim(), tpl, Timecode.FromFrames(long.Parse(f[0].Trim())), Timecode.FromFrames(long.Parse(f[1].Trim())));
                if (st != RenderStatus.Complete) throw new ApplicationException("render of " + f[2].Trim() + " ended " + st);
                n++;
            }
            File.WriteAllText(result, "ok rendered=" + n + " template=" + tpl.Name);
        }
        catch (Exception ex)
        {
            try { File.WriteAllText(result, "error: " + ex.Message); } catch { }
        }
        vegas.Exit();
    }

    // Mp4Template: an AVC (H.264) template first - the first .mp4 template on
    // this PC was MPEG-2 video + PCM sound in an MP4 box, which Windows players
    // will not play (fxtest, 2026-10-08). Then any valid .mp4 template.
    static RenderTemplate Mp4Template(Vegas vegas)
    {
        RenderTemplate any = null;
        foreach (Renderer r in vegas.Renderers)
        {
            if (r.FileExtension == null || !r.FileExtension.ToLower().Contains("mp4")) continue;
            bool avc = (r.FileTypeName ?? "").ToUpperInvariant().Contains("AVC");
            foreach (RenderTemplate t in r.Templates)
            {
                if (!t.IsValid() || t.VideoStreamCount == 0) continue;
                if (avc && t.Name.Contains("1080p")) return t;
                if (any == null) any = t;
            }
        }
        return any;
    }
}
