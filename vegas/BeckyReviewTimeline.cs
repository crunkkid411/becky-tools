/*
 * BeckyReviewTimeline.cs
 * ----------------------------------------------------------------------------
 * Assemble a forensic REVIEW timeline in VEGAS Pro 18 (Windows) from a plain
 * text "review list" that becky (or you, by hand) produces.
 *
 * WHAT IT DOES
 *   - Pops a file picker; you choose a review list (.txt / .csv).
 *   - Reads one clip per line:   <full path> | <in> | <out> | <optional label>
 *   - Drops every clip END-TO-END on a single video track + audio track, in the
 *     order listed, trimmed to exactly [in, out] of each source.
 *   - Adds a NAMED REGION over each clip so you can jump candidate-to-candidate
 *     (Region bar, or the Regions window) instead of scrubbing blindly.
 *   - Never touches the source files. Skips missing/bad files and tells you which.
 *
 * WHY THIS EXISTS
 *   becky's forensic tools find the moments (e.g. "every segment where the cat is
 *   close to the camera"); this script lets you REVIEW those moments immediately
 *   in the snappy editor you already know, while we decide the long-term host.
 *
 * REVIEW LIST FORMAT (UTF-8 or ANSI text; '#' starts a comment line)
 *   # path                        | in      | out     | label
 *   C:\Videos\cam1.mp4            | 65.0    | 73.5    | cat closeup - chipped tooth?
 *   C:\Videos\cam2.mp4            | 00:02:00 | 00:02:08 | cat near camera
 *   E:\evidence\clip.mov          | 1320.25 | 1331.0  |
 *
 *   - in/out accept PLAIN SECONDS ("73.5") OR colon time ("MM:SS", "HH:MM:SS",
 *     with optional decimals "HH:MM:SS.250"). Mix freely between lines.
 *   - label is optional; if blank, the file name is used.
 *
 * HOW TO RUN (no compiling needed)
 *   1. VEGAS Pro 18:  Tools > Scripting > Run Script...  > pick this .cs file.
 *   2. (Optional, to pin it in the menu) copy this file into:
 *        C:\Users\<you>\Documents\Vegas Script Menu\
 *      then restart VEGAS; it appears under Tools > Scripting.
 *
 * VERIFIED AGAINST the official MAGIX VEGAS Pro scripting docs (v18-era):
 *   namespace ScriptPortal.Vegas; EntryPoint.FromVegas(Vegas); AddVideoTrack /
 *   AddAudioTrack; AddVideoEvent(Timecode,Timecode) / AddAudioEvent; AddTake;
 *   Take.Offset (= source in-point); Timecode.FromSeconds; Region(start,len,label).
 *   (VEGAS 18 = MAGIX branding, hence ScriptPortal.Vegas, NOT Sony.Vegas.)
 *   NOTE for VEGAS Pro 13 and OLDER ONLY: change the using below to Sony.Vegas.
 * ----------------------------------------------------------------------------
 */

using System;
using System.IO;
using System.Globalization;
using System.Collections.Generic;
using System.Windows.Forms;
using ScriptPortal.Vegas;   // VEGAS Pro 13 or older: use Sony.Vegas;

public class EntryPoint
{
    public void FromVegas(Vegas vegas)
    {
        // ---- 1. Choose the review list -------------------------------------
        string listPath = null;
        using (OpenFileDialog dlg = new OpenFileDialog())
        {
            dlg.Title = "Becky: choose the review clip list";
            dlg.Filter = "Becky review list (*.txt;*.csv)|*.txt;*.csv|All files (*.*)|*.*";
            if (dlg.ShowDialog() != DialogResult.OK)
                return; // cancelled - do nothing
            listPath = dlg.FileName;
        }

        // ---- 2. Parse the list --------------------------------------------
        List<ClipLine> clips = new List<ClipLine>();
        List<string> warnings = new List<string>();
        int lineNo = 0;
        foreach (string raw in File.ReadAllLines(listPath))
        {
            lineNo++;
            string line = raw.Trim();
            if (line.Length == 0 || line.StartsWith("#"))
                continue;

            string[] parts = line.Split('|');
            if (parts.Length < 3)
            {
                warnings.Add("line " + lineNo + ": need at least  path | in | out");
                continue;
            }

            string path = parts[0].Trim();
            double inSec, outSec;
            if (!TryParseTime(parts[1].Trim(), out inSec) ||
                !TryParseTime(parts[2].Trim(), out outSec))
            {
                warnings.Add("line " + lineNo + ": could not read in/out time");
                continue;
            }
            if (outSec <= inSec)
            {
                warnings.Add("line " + lineNo + ": out (" + outSec + ") <= in (" + inSec + ")");
                continue;
            }

            string label = (parts.Length >= 4 && parts[3].Trim().Length > 0)
                ? parts[3].Trim()
                : Path.GetFileName(path);

            clips.Add(new ClipLine(path, inSec, outSec, label));
        }

        if (clips.Count == 0)
        {
            MessageBox.Show(
                "No usable clips found in:\n" + listPath +
                (warnings.Count > 0 ? "\n\n" + string.Join("\n", warnings.ToArray()) : ""),
                "Becky Review Timeline");
            return;
        }

        // ---- 3. Build the timeline ----------------------------------------
        VideoTrack vtrack = vegas.Project.AddVideoTrack();
        vtrack.Name = "Becky Review (video)";
        AudioTrack atrack = vegas.Project.AddAudioTrack();
        atrack.Name = "Becky Review (audio)";

        Timecode cursor = Timecode.FromSeconds(0.0);
        int added = 0;
        List<string> skipped = new List<string>();

        foreach (ClipLine c in clips)
        {
            Media media;
            try
            {
                media = new Media(c.Path); // throws if the file is missing/unreadable
            }
            catch (Exception ex)
            {
                skipped.Add(c.Path + "   (" + ex.Message + ")");
                continue;
            }

            Timecode start  = cursor;
            Timecode length = Timecode.FromSeconds(c.Out - c.In);
            Timecode offset = Timecode.FromSeconds(c.In); // in-point INTO the source
            bool placed = false;

            // video stream (guarded - some evidence is audio-only)
            try
            {
                VideoStream vs = media.GetVideoStreamByIndex(0);
                if (vs != null)
                {
                    VideoEvent ve = vtrack.AddVideoEvent(start, length);
                    Take t = ve.AddTake(vs);
                    t.Offset = offset;
                    placed = true;
                }
            }
            catch { /* no video stream - fall through to audio */ }

            // audio stream (guarded - some evidence is silent video)
            try
            {
                AudioStream au = media.GetAudioStreamByIndex(0);
                if (au != null)
                {
                    AudioEvent ae = atrack.AddAudioEvent(start, length);
                    Take t = ae.AddTake(au);
                    t.Offset = offset;
                    placed = true;
                }
            }
            catch { /* no audio stream - fine */ }

            if (!placed)
            {
                skipped.Add(c.Path + "   (no decodable video or audio stream)");
                continue;
            }

            // a named region per clip = one-keypress navigation between candidates
            try
            {
                Region r = new Region(start, length, c.Label);
                vegas.Project.Regions.Add(r);
            }
            catch { /* regions are a nicety; never fail the build over one */ }

            cursor = cursor + length;
            added++;
        }

        // ---- 4. Park the playhead at the top and report -------------------
        try { vegas.Transport.CursorPosition = Timecode.FromSeconds(0.0); }
        catch { }

        string msg = "Becky placed " + added + " of " + clips.Count + " clip(s), end to end.\n" +
                     "Each clip is a named Region - use the Regions window (or the region\n" +
                     "markers on the ruler) to jump straight to each candidate.";
        if (skipped.Count > 0)
            msg += "\n\nSkipped " + skipped.Count + ":\n" + string.Join("\n", skipped.ToArray());
        if (warnings.Count > 0)
            msg += "\n\nList warnings:\n" + string.Join("\n", warnings.ToArray());
        MessageBox.Show(msg, "Becky Review Timeline");
    }

    // Accepts plain seconds ("73.5") OR colon time ("MM:SS" / "HH:MM:SS",
    // optional decimals). InvariantCulture so a comma locale can't break parsing.
    static bool TryParseTime(string s, out double seconds)
    {
        seconds = 0;
        if (string.IsNullOrEmpty(s))
            return false;

        if (s.IndexOf(':') < 0)
            return double.TryParse(s, NumberStyles.Float, CultureInfo.InvariantCulture, out seconds);

        double total = 0;
        foreach (string bit in s.Split(':'))
        {
            double v;
            if (!double.TryParse(bit, NumberStyles.Float, CultureInfo.InvariantCulture, out v))
                return false;
            total = total * 60.0 + v;
        }
        seconds = total;
        return true;
    }

    class ClipLine
    {
        public string Path;
        public double In;
        public double Out;
        public string Label;
        public ClipLine(string p, double i, double o, string l)
        {
            Path = p; In = i; Out = o; Label = l;
        }
    }
}
