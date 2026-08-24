/*
 * BeckyVerifyProject.cs
 * ----------------------------------------------------------------------------
 * Read-only audit of a saved VEGAS project, for headless verification.
 *
 *   set BECKY_VERIFY_VEG=C:\path\rough_cut.veg
 *   vegas180.exe -SCRIPT:<this file>
 *
 * Writes C:\path\rough_cut.veg.verify.txt with track/event/marker/region
 * counts, the timeline length, and the first/last event sources - enough to
 * prove a rough cut actually landed in the project without opening the UI.
 * ----------------------------------------------------------------------------
 */

using System;
using System.Collections.Generic;
using System.IO;
using System.Windows.Forms;
using ScriptPortal.Vegas;   // VEGAS Pro 13 or older: use Sony.Vegas;

public class EntryPoint
{
    public void FromVegas(Vegas vegas)
    {
        string path = Environment.GetEnvironmentVariable("BECKY_VERIFY_VEG");
        if (string.IsNullOrEmpty(path) || !File.Exists(path))
        {
            using (OpenFileDialog dlg = new OpenFileDialog())
            {
                dlg.Title = "Becky: choose a .veg to audit";
                dlg.Filter = "VEGAS project (*.veg)|*.veg";
                if (dlg.ShowDialog() != DialogResult.OK) { vegas.Exit(); return; }
                path = dlg.FileName;
            }
        }
        List<string> lines = new List<string>();
        try
        {
            vegas.OpenFile(path);
            int vEvents = 0, aEvents = 0;
            string first = "", last = "";
            double len = 0;
            foreach (Track t in vegas.Project.Tracks)
            {
                foreach (TrackEvent ev in t.Events)
                {
                    if (t is VideoTrack) vEvents++;
                    if (t is AudioTrack) aEvents++;
                    foreach (Take tk in ev.Takes)
                    {
                        if (tk.Media != null)
                        {
                            string f = Path.GetFileName(tk.Media.FilePath);
                            if (first == "") first = f;
                            last = f;
                        }
                    }
                    len = Math.Max(len, (ev.Start + ev.Length).Nanos * 1e-7);
                }
            }
            lines.Add("project: " + path);
            lines.Add("tracks: " + vegas.Project.Tracks.Count);
            lines.Add("video_events: " + vEvents);
            lines.Add("audio_events: " + aEvents);
            lines.Add("markers: " + vegas.Project.Markers.Count);
            lines.Add("regions: " + vegas.Project.Regions.Count);
            lines.Add("length_seconds: " + len.ToString("0.0"));
            lines.Add("first_source: " + first);
            lines.Add("last_source: " + last);
        }
        catch (Exception ex)
        {
            lines.Add("FATAL: " + ex.Message);
        }
        try { File.WriteAllLines(path + ".verify.txt", lines.ToArray()); } catch { }
        vegas.Exit();
    }
}
