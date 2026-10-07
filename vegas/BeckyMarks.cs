/*
 * BeckyMarks.cs - put regions and markers on the open project, ON THE FRAME GRID.
 * ----------------------------------------------------------------------------
 * becky-livestream, 2026-10-06. Jordan ripple-deletes everything inside regions
 * with one script, so a region edge between two frames leaves a one-frame black
 * flash or sliver: "everything needs to honor the timeline's frame rate". The
 * extension's add_region verb takes seconds; this script takes FRAME NUMBERS and
 * builds every position with Timecode.FromFrames - exactly how BeckyKeepList.cs
 * places the events - so a region edge and an event edge are the same timecode.
 *
 * HOW TO RUN (becky-livestream does this):
 *   becky-vegas run_script path=<this file> job=<job.txt>
 * job.txt is plain text, one item per line, TAB-separated:
 *   region <TAB> <first frame> <TAB> <length in frames> <TAB> <label>
 *   marker <TAB> <frame> <TAB> <label>
 *
 * One Undo step. It NEVER shows a dialog (a dialog would stall an unattended
 * run). It writes <job.txt>.result.txt - "ok regions=N markers=M" or
 * "error: ..." - and becky-livestream reads it.
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
        string result = (job == "" ? Path.Combine(Path.GetTempPath(), "BeckyMarks") : job) + ".result.txt";
        try
        {
            if (job == "" || !File.Exists(job))
                throw new ApplicationException("no job file (run it through becky-vegas run_script ... job=<file>)");
            if (vegas.Project == null) throw new ApplicationException("no project is open");
            List<string[]> items = new List<string[]>();
            foreach (string line in File.ReadAllLines(job))
            {
                string[] f = line.Split('\t');
                if (f.Length >= 4 && f[0] == "region") items.Add(f);
                if (f.Length >= 3 && f[0] == "marker") items.Add(f);
            }
            int regions = 0, markers = 0;
            using (UndoBlock u = new UndoBlock("Becky: regions and markers"))
            {
                foreach (string[] f in items)
                {
                    long at = long.Parse(f[1].Trim());
                    if (at < 0) throw new ApplicationException("a negative frame: " + f[1]);
                    if (f[0] == "region")
                    {
                        long len = long.Parse(f[2].Trim());
                        if (len <= 0) throw new ApplicationException("a region needs a length: " + f[2]);
                        vegas.Project.Regions.Add(new Region(Timecode.FromFrames(at), Timecode.FromFrames(len), f[3]));
                        regions++;
                    }
                    else
                    {
                        vegas.Project.Markers.Add(new Marker(Timecode.FromFrames(at), f[2]));
                        markers++;
                    }
                }
            }
            File.WriteAllText(result, "ok regions=" + regions + " markers=" + markers);
        }
        catch (Exception ex)
        {
            try { File.WriteAllText(result, "error: " + ex.Message); } catch { }
        }
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
