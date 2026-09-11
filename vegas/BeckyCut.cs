// BeckyCut.cs - cut the dead air out of the clips you have SELECTED, in place.
//
// WHAT IT DOES
//
// Select some events on the VEGAS timeline, run this, and the silent parts of
// those events are split out and deleted. Nothing else on the timeline moves,
// nothing is re-encoded, and the source files on disk are only ever READ.
//
// WHERE THE DECISIONS COME FROM
//
// becky-cut, run with --dry-run: auto-editor does the audio detection on the
// file's own level (see becky-go/cmd/cut/level.go - the threshold is measured
// from the gap between THIS recording's room tone and THIS recording's speech,
// so a quiet Rode lav and a loud phone both work with no dial to turn), then a
// Silero VAD post-pass flips coughs, chair squeaks and door thuds to cuts.
// --dry-run means it computes the edit and renders nothing, so this is fast
// and completely non-destructive - the only thing that changes is your
// timeline, and one Ctrl+Z puts it back.
//
// WHY IT SPLITS RATHER THAN RENDERS
//
// becky-cut on its own writes a new _edited.mp4. That is the wrong shape for an
// editor: it throws away your timeline and hands you a flat file. This keeps
// the edit ON the timeline where you can still move, trim and undo it.
//
// REQUIREMENTS: becky-cut.exe built by build-all-tools.bat. Point BECKY_CUT at
// it, or put becky-go\bin on PATH.
//
// VEGAS 14-22 use ScriptPortal.Vegas (below). On VEGAS 13 or older change the
// using line to Sony.Vegas.

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Text;
using System.Text.RegularExpressions;
using System.Windows.Forms;
using ScriptPortal.Vegas;

public class EntryPoint
{
    // A gap shorter than this is left alone. becky-cut's own margin already
    // pads every keep by 0.04s in front and 0.25s behind, so what survives as a
    // 0.1s "cut" is the space between two words in one breath - removing it
    // makes the speech sound clipped. This is the one knob an editor actually
    // turns, so it is on the dialog.
    private const double DefaultMinGapSeconds = 0.25;

    public void FromVegas(Vegas vegas)
    {
        // BECKY_CUT_SELFTEST=<media file> is the headless proof: VEGAS builds a
        // throwaway one-clip project from that file, selects it, runs this
        // script's real code path, writes what changed to
        // <media>.becky-cut-selftest.txt and exits WITHOUT saving anything.
        //   set BECKY_CUT_SELFTEST=C:\clip.mp4
        //   vegas180.exe -SCRIPT:"...\vegas\BeckyCut.cs"
        string selftest = Environment.GetEnvironmentVariable("BECKY_CUT_SELFTEST");
        if (!string.IsNullOrEmpty(selftest))
        {
            SelfTest(vegas, selftest);
            return;
        }

        try
        {
            List<Clip> clips = CollectSelected(vegas);
            if (clips.Count == 0)
            {
                throw new ApplicationException(
                    "Nothing is selected.\n\n" +
                    "Click the events you want cut (Ctrl+click for more than one), then run this again."
                );
            }

            Options opts = AskOptions(clips.Count);
            if (opts == null)
            {
                return; // cancelled
            }

            string beckyExe = ResolveBeckyCut();
            EnsureBeckyExecutable(beckyExe);

            // One becky-cut run per distinct SOURCE FILE, not per event: two
            // events off the same clip share one analysis.
            Dictionary<string, CutReport> bySource = new Dictionary<string, CutReport>(StringComparer.OrdinalIgnoreCase);
            foreach (Clip c in clips)
            {
                if (!bySource.ContainsKey(c.Source))
                {
                    bySource[c.Source] = null;
                }
            }

            string workDir = Path.Combine(Path.GetTempPath(), "BeckyVegasCut");
            Directory.CreateDirectory(workDir);

            AnalyseWithProgress(beckyExe, bySource, workDir);

            int removed = ApplyToTimeline(vegas, clips, bySource, opts);
            if (removed == 0)
            {
                MessageBox.Show(
                    "becky found nothing to cut in the selection.\n\n" +
                    "Either there is no dead air in it, or every gap is shorter than the " +
                    opts.MinGapSeconds.ToString("0.00", CultureInfo.InvariantCulture) + "s you allowed.",
                    "Becky Cut",
                    MessageBoxButtons.OK,
                    MessageBoxIcon.Information
                );
            }

            // No "done" box when it worked. The shorter clips ARE the
            // confirmation - they are on the timeline in front of you.
        }
        catch (Exception ex)
        {
            MessageBox.Show(
                ex.Message,
                "Becky Cut",
                MessageBoxButtons.OK,
                MessageBoxIcon.Error
            );
        }
    }

    // ------------------------------------------------------------ the shapes

    // Clip is one selected event, frozen at its pre-edit geometry. Start/End
    // are captured BEFORE anything is split, because they are the footprint we
    // are allowed to touch.
    private class Clip
    {
        public TrackEvent Event;
        public Track Track;
        public string Source;
        public double SrcIn;    // seconds into the source file
        public double SrcOut;   // seconds into the source file
        public double Rate;     // playback rate; >1 = the event consumes more source than it occupies
        public Timecode Start;  // ruler position, before the edit
        public Timecode End;    // ruler position, before the edit
    }

    private class Options
    {
        public double MinGapSeconds;
        public bool CloseGaps;
    }

    // The becky-cut --dry-run report. Field names match its JSON exactly
    // (becky-go/cmd/cut/main.go). Only the fields used here are declared;
    // JavaScriptSerializer ignores the rest.
    private class CutReport
    {
        public double fps;
        public double duration;
        public string threshold;
        public List<Decision> decisions;
    }

    private class Decision
    {
        public string status; // "keep" or "cut"
        public double start;  // seconds into the source file
        public double end;
    }

    // A span of the RULER to delete, already mapped through one event's trim.
    private class Span
    {
        public Timecode Start;
        public Timecode End;
    }

    // ------------------------------------------------------- reading the edit

    private static List<Clip> CollectSelected(Vegas vegas)
    {
        List<Clip> clips = new List<Clip>();
        foreach (Track track in vegas.Project.Tracks)
        {
            foreach (TrackEvent ev in track.Events)
            {
                if (!ev.Selected)
                {
                    continue;
                }
                if (ev.ActiveTake == null || ev.ActiveTake.Media == null)
                {
                    continue;
                }

                string path = ev.ActiveTake.Media.FilePath;
                if (string.IsNullOrEmpty(path) || !File.Exists(path))
                {
                    continue;
                }

                double lengthSec = ev.Length.ToMilliseconds() / 1000.0;
                if (lengthSec <= 0)
                {
                    continue;
                }

                double rate = PlaybackRateOf(ev);
                double srcIn = ev.ActiveTake.Offset.ToMilliseconds() / 1000.0;

                clips.Add(new Clip
                {
                    Event = ev,
                    Track = track,
                    Source = path,
                    SrcIn = srcIn,
                    SrcOut = srcIn + lengthSec * rate,
                    Rate = rate,
                    Start = ev.Start,
                    End = ev.End
                });
            }
        }
        return clips;
    }

    // PlaybackRateOf returns the event's speed multiplier, or 1.0 when the
    // event type doesn't expose one. A stretched event consumes more (or less)
    // source than its timeline length.
    private static double PlaybackRateOf(TrackEvent ev)
    {
        VideoEvent vev = ev as VideoEvent;
        if (vev != null && vev.PlaybackRate > 0)
        {
            return vev.PlaybackRate;
        }

        AudioEvent aev = ev as AudioEvent;
        if (aev != null && aev.PlaybackRate > 0)
        {
            return aev.PlaybackRate;
        }

        return 1.0;
    }

    // ------------------------------------------------------------ the dialog

    private static Options AskOptions(int clipCount)
    {
        // An agent (or a batch run) sets these and never sees the dialog. This
        // is the same env-var pattern the other becky VEGAS scripts use, because
        // Vegas.ScriptArgs does not exist in this API surface.
        Options fromEnv = OptionsFromEnvironment();
        if (fromEnv != null)
        {
            return fromEnv;
        }

        using (Form form = new Form())
        using (Label heading = new Label())
        using (Label gapLabel = new Label())
        using (NumericUpDown gap = new NumericUpDown())
        using (CheckBox closeGaps = new CheckBox())
        using (Button ok = new Button())
        using (Button cancel = new Button())
        {
            form.Text = "Becky Cut";
            form.ClientSize = new System.Drawing.Size(430, 178);
            form.StartPosition = FormStartPosition.CenterScreen;
            form.FormBorderStyle = FormBorderStyle.FixedDialog;
            form.MaximizeBox = false;
            form.MinimizeBox = false;

            heading.SetBounds(14, 14, 400, 34);
            heading.Text = clipCount + (clipCount == 1 ? " event selected." : " events selected.") +
                           "\nbecky will cut the dead air out of them and leave everything else alone.";

            gapLabel.SetBounds(14, 60, 250, 20);
            gapLabel.Text = "Leave gaps shorter than this alone (seconds):";

            gap.SetBounds(276, 57, 70, 22);
            gap.DecimalPlaces = 2;
            gap.Increment = 0.05M;
            gap.Minimum = 0.00M;
            gap.Maximum = 10.00M;
            gap.Value = (decimal)DefaultMinGapSeconds;

            closeGaps.SetBounds(14, 90, 400, 22);
            closeGaps.Text = "Close the gaps inside each clip (the clip gets shorter)";
            closeGaps.Checked = false;

            ok.SetBounds(240, 130, 84, 28);
            ok.Text = "Cut";
            ok.DialogResult = DialogResult.OK;

            cancel.SetBounds(332, 130, 84, 28);
            cancel.Text = "Cancel";
            cancel.DialogResult = DialogResult.Cancel;

            form.Controls.Add(heading);
            form.Controls.Add(gapLabel);
            form.Controls.Add(gap);
            form.Controls.Add(closeGaps);
            form.Controls.Add(ok);
            form.Controls.Add(cancel);
            form.AcceptButton = ok;
            form.CancelButton = cancel;

            if (form.ShowDialog() != DialogResult.OK)
            {
                return null;
            }

            return new Options
            {
                MinGapSeconds = (double)gap.Value,
                CloseGaps = closeGaps.Checked
            };
        }
    }

    // OptionsFromEnvironment returns null unless at least one of
    // BECKY_CUT_MIN_GAP / BECKY_CUT_CLOSE_GAPS is set, in which case the dialog
    // is skipped entirely and the unset one keeps its default.
    private static Options OptionsFromEnvironment()
    {
        string minGap = Environment.GetEnvironmentVariable("BECKY_CUT_MIN_GAP");
        string close = Environment.GetEnvironmentVariable("BECKY_CUT_CLOSE_GAPS");
        if (string.IsNullOrEmpty(minGap) && string.IsNullOrEmpty(close))
        {
            return null;
        }

        Options opts = new Options
        {
            MinGapSeconds = DefaultMinGapSeconds,
            CloseGaps = false
        };

        double parsed;
        if (!string.IsNullOrEmpty(minGap) &&
            double.TryParse(minGap.Trim(), NumberStyles.Float, CultureInfo.InvariantCulture, out parsed) &&
            parsed >= 0)
        {
            opts.MinGapSeconds = parsed;
        }
        if (!string.IsNullOrEmpty(close))
        {
            string c = close.Trim().ToLowerInvariant();
            opts.CloseGaps = (c == "1" || c == "true" || c == "yes");
        }
        return opts;
    }

    // ------------------------------------------------------- talking to becky

    // AnalyseWithProgress fills in one CutReport per distinct source file while
    // a marquee bar counts up. The analysis is the slow part (auto-editor reads
    // the whole file, then the VAD pass); the timeline edit afterwards is
    // instant.
    private static void AnalyseWithProgress(string beckyExe, Dictionary<string, CutReport> bySource, string workDir)
    {
        List<string> sources = new List<string>(bySource.Keys);
        Exception workerError = null;
        int done = 0;

        using (Form progressForm = new Form())
        using (Label statusLabel = new Label())
        using (ProgressBar progressBar = new ProgressBar())
        using (System.Windows.Forms.Timer timer = new System.Windows.Forms.Timer())
        using (BackgroundWorker worker = new BackgroundWorker())
        {
            progressForm.Text = "Becky Cut";
            progressForm.ClientSize = new System.Drawing.Size(540, 130);
            progressForm.StartPosition = FormStartPosition.CenterScreen;
            progressForm.FormBorderStyle = FormBorderStyle.FixedDialog;
            progressForm.MaximizeBox = false;
            progressForm.MinimizeBox = false;
            progressForm.ControlBox = false;

            statusLabel.SetBounds(12, 12, 516, 46);
            statusLabel.Text = "Listening to " + sources.Count + (sources.Count == 1 ? " file." : " files.");

            progressBar.SetBounds(12, 66, 516, 22);
            progressBar.Style = ProgressBarStyle.Marquee;
            progressBar.MarqueeAnimationSpeed = 30;

            progressForm.Controls.Add(statusLabel);
            progressForm.Controls.Add(progressBar);

            DateTime startedAt = DateTime.UtcNow;
            timer.Interval = 500;
            timer.Tick += delegate
            {
                TimeSpan elapsed = DateTime.UtcNow - startedAt;
                int n = done;
                string current = n < sources.Count ? Path.GetFileName(sources[n]) : "";
                statusLabel.Text = "Listening to file " + Math.Min(n + 1, sources.Count) + " of " + sources.Count +
                                   "   (" + elapsed.ToString(@"hh\:mm\:ss") + ")\n" + current;
            };
            timer.Start();

            worker.DoWork += delegate
            {
                for (int i = 0; i < sources.Count; i++)
                {
                    bySource[sources[i]] = RunBeckyCut(beckyExe, sources[i], workDir);
                    done = i + 1;
                }
            };

            worker.RunWorkerCompleted += delegate (object sender, RunWorkerCompletedEventArgs e)
            {
                timer.Stop();
                if (e.Error != null)
                {
                    workerError = e.Error;
                }
                progressForm.Close();
            };

            worker.RunWorkerAsync();
            progressForm.ShowDialog();
        }

        if (workerError != null)
        {
            throw workerError;
        }
    }

    private static CutReport RunBeckyCut(string beckyExe, string source, string workDir)
    {
        ProcessStartInfo psi = new ProcessStartInfo
        {
            FileName = beckyExe,
            Arguments = Quote(source) + " --dry-run",
            UseShellExecute = false,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            CreateNoWindow = true,
            WorkingDirectory = workDir,
            StandardOutputEncoding = Encoding.UTF8,
            StandardErrorEncoding = Encoding.UTF8
        };

        string stdout;
        string stderr;
        int exitCode;

        Process process;
        try
        {
            process = Process.Start(psi);
        }
        catch (Win32Exception ex)
        {
            throw new ApplicationException(
                "Could not start becky-cut.\nExecutable: " + beckyExe + "\nDetails: " + ex.Message
            );
        }

        using (process)
        {
            if (process == null)
            {
                throw new ApplicationException("Failed to start becky-cut.");
            }
            stdout = process.StandardOutput.ReadToEnd();
            stderr = process.StandardError.ReadToEnd();
            process.WaitForExit();
            exitCode = process.ExitCode;
        }

        // Keep the run's own log - the first thing to read when a cut lands
        // somewhere surprising.
        try
        {
            File.WriteAllText(
                Path.Combine(workDir, Path.GetFileNameWithoutExtension(source) + ".becky-cut.log"),
                "exit " + exitCode + "\n\n--- stdout ---\n" + stdout + "\n--- stderr ---\n" + stderr,
                new UTF8Encoding(false));
        }
        catch
        {
            // A log we cannot write must not fail the run.
        }

        CutReport report = null;
        try
        {
            report = ParseCutReport(stdout);
        }
        catch (Exception ex)
        {
            throw new ApplicationException(
                "becky-cut's answer could not be read for:\n" + source + "\n\n" + ex.Message +
                "\n\n" + Tail(stdout, 600)
            );
        }

        if (report == null || report.decisions == null || report.decisions.Count == 0)
        {
            string message = "becky-cut made no edit decisions for:\n" + source;
            if (exitCode != 0)
            {
                message += "\nExit code: " + exitCode;
            }
            if (!string.IsNullOrEmpty(stderr))
            {
                message += "\n\n" + Tail(stderr, 900);
            }
            throw new ApplicationException(message);
        }

        return report;
    }

    // ------------------------------------------------- reading becky's answer

    // ParseCutReport reads becky-cut's --dry-run JSON WITHOUT
    // System.Web.Extensions.dll.
    //
    // This is the bug that made the first BeckyCut.cs unusable, so it earns the
    // paragraph. VEGAS compiles these scripts in-process against a small fixed
    // set of assemblies. JavaScriptSerializer is not in it - it lives in
    // System.Web.Extensions.dll, which only loads when a <script>.cs.config
    // sidecar sits beside the .cs, and the installer copied only the .cs. So the
    // script compiled clean under csc (whose csc.rsp quietly references that
    // assembly - see check-vegas-script.ps1's /noconfig) and then died the
    // moment Jordan ran it from the Tools menu:
    //   "The type or namespace name 'Script' does not exist in the namespace
    //    'System.Web'"
    // BeckyCaptions.cs - the one that has always worked - reads becky's JSON
    // with a regex for exactly this reason. Doing the same here keeps BeckyCut
    // a SINGLE self-contained file with no sidecar to lose.
    //
    // This only has to survive Go's MarshalIndent (whitespace and key order),
    // not arbitrary JSON: every decision object is flat, three scalar keys.
    private static CutReport ParseCutReport(string json)
    {
        if (string.IsNullOrEmpty(json))
        {
            throw new ApplicationException("becky-cut printed nothing at all.");
        }

        CutReport report = new CutReport();
        report.decisions = new List<Decision>();

        double value;
        if (TryNumberField(json, "fps", out value))
        {
            report.fps = value;
        }
        if (TryNumberField(json, "duration", out value))
        {
            report.duration = value;
        }
        report.threshold = StringField(json, "threshold");

        string array = ExtractArray(json, "decisions");
        if (array == null)
        {
            throw new ApplicationException("becky-cut's answer has no \"decisions\" list.");
        }

        foreach (Match m in Regex.Matches(array, @"\{[^{}]*\}"))
        {
            string blob = m.Value;
            string status = StringField(blob, "status");
            double start;
            double end;

            // All three or nothing - a half-read decision would silently cut
            // the wrong part of the clip, which is worse than not cutting.
            if (status == null)
            {
                continue;
            }
            if (!TryNumberField(blob, "start", out start))
            {
                continue;
            }
            if (!TryNumberField(blob, "end", out end))
            {
                continue;
            }

            report.decisions.Add(new Decision { status = status, start = start, end = end });
        }

        return report;
    }

    // TryNumberField pulls "<key>": <number>. The closing quote inside the
    // pattern is what stops "threshold" also matching "threshold_db".
    private static bool TryNumberField(string blob, string key, out double value)
    {
        value = 0.0;
        Match m = Regex.Match(blob, "\"" + key + "\"" + @"\s*:\s*(-?[0-9]+(?:\.[0-9]+)?(?:[eE][-+]?[0-9]+)?)");
        if (!m.Success)
        {
            return false;
        }
        return double.TryParse(m.Groups[1].Value, NumberStyles.Float,
                               CultureInfo.InvariantCulture, out value);
    }

    // StringField pulls "<key>": "<value>", honouring backslash escapes so a
    // Windows path in the JSON cannot end the match early.
    private static string StringField(string blob, string key)
    {
        Match m = Regex.Match(blob, "\"" + key + "\"" + @"\s*:\s*""((?:[^""\\]|\\.)*)""");
        return m.Success ? m.Groups[1].Value : null;
    }

    // ExtractArray returns the text inside the [ ] following "<key>":, counting
    // brackets so a nested array could never truncate it.
    private static string ExtractArray(string json, string key)
    {
        int at = json.IndexOf("\"" + key + "\"", StringComparison.Ordinal);
        if (at < 0)
        {
            return null;
        }
        int open = json.IndexOf('[', at);
        if (open < 0)
        {
            return null;
        }

        int depth = 0;
        for (int i = open; i < json.Length; i++)
        {
            if (json[i] == '[')
            {
                depth++;
            }
            else if (json[i] == ']')
            {
                depth--;
                if (depth == 0)
                {
                    return json.Substring(open + 1, i - open - 1);
                }
            }
        }
        return null;
    }

    // ----------------------------------------------------------- the edit

    // ApplyToTimeline splits every selected event at becky's cut points and
    // deletes the silent pieces. Returns how many pieces were removed.
    //
    // The whole thing is one UndoBlock, so Ctrl+Z puts the timeline back
    // exactly as it was - that is what makes this safe to try.
    private static int ApplyToTimeline(Vegas vegas, List<Clip> clips,
                                       Dictionary<string, CutReport> bySource, Options opts)
    {
        int removed = 0;
        using (UndoBlock undo = new UndoBlock("Becky Cut"))
        {
            foreach (Clip clip in clips)
            {
                CutReport report = bySource[clip.Source];
                List<Span> spans = SpansToRemove(clip, report, opts.MinGapSeconds);
                if (spans.Count == 0)
                {
                    continue;
                }

                foreach (Span span in spans)
                {
                    removed += RemoveSpan(clip.Track, span, clip.Start, clip.End);
                }

                if (opts.CloseGaps)
                {
                    CloseGapsInFootprint(clip.Track, clip.Start, clip.End);
                }
            }
        }
        return removed;
    }

    // SpansToRemove maps becky's "cut" decisions (seconds into the SOURCE FILE)
    // onto this event's slice of the ruler, dropping anything outside the
    // event's trim and anything shorter than the allowed gap.
    private static List<Span> SpansToRemove(Clip clip, CutReport report, double minGapSeconds)
    {
        List<Span> spans = new List<Span>();
        double fps = report.fps > 0 ? report.fps : 30.0;
        // One video frame is the smallest thing worth removing no matter what
        // the dialog says - below that there is nothing to delete.
        double minSeconds = Math.Max(minGapSeconds, 1.0 / fps);

        foreach (Decision d in report.decisions)
        {
            if (d == null || d.status != "cut")
            {
                continue;
            }

            double cs = Math.Max(d.start, clip.SrcIn);
            double ce = Math.Min(d.end, clip.SrcOut);
            if (ce - cs < minSeconds)
            {
                continue;
            }

            Timecode start = RulerAt(clip, cs);
            Timecode end = RulerAt(clip, ce);
            if (end <= start)
            {
                continue;
            }
            spans.Add(new Span { Start = start, End = end });
        }
        return spans;
    }

    // RulerAt converts a position in the source file to a position on the
    // ruler, through this event's in-point and playback rate.
    private static Timecode RulerAt(Clip clip, double sourceSeconds)
    {
        double intoEvent = (sourceSeconds - clip.SrcIn) / clip.Rate;
        Timecode at = clip.Start + Timecode.FromSeconds(intoEvent);
        if (at < clip.Start)
        {
            return clip.Start;
        }
        if (at > clip.End)
        {
            return clip.End;
        }
        return at;
    }

    // RemoveSpan splits at both edges of [start,end) and deletes whatever now
    // sits inside it, restricted to the selected event's own footprint.
    //
    // Split points are found by RULER POSITION on a fresh snapshot each time,
    // never by holding a reference across a split. VEGAS splits grouped events
    // together, so a held reference can silently become the wrong half.
    private static int RemoveSpan(Track track, Span span, Timecode footStart, Timecode footEnd)
    {
        SplitAt(track, span.Start);
        SplitAt(track, span.End);

        int removed = 0;
        List<TrackEvent> doomed = new List<TrackEvent>();
        foreach (TrackEvent ev in track.Events)
        {
            if (ev.Start >= span.Start && ev.End <= span.End &&
                ev.Start >= footStart && ev.End <= footEnd)
            {
                doomed.Add(ev);
            }
        }
        foreach (TrackEvent ev in doomed)
        {
            track.Events.Remove(ev);
            removed++;
        }
        return removed;
    }

    // SplitAt cuts whichever event on the track straddles this ruler position.
    // Iterating a snapshot matters: Split adds to track.Events while we look.
    private static void SplitAt(Track track, Timecode at)
    {
        List<TrackEvent> snapshot = new List<TrackEvent>();
        foreach (TrackEvent ev in track.Events)
        {
            snapshot.Add(ev);
        }
        foreach (TrackEvent ev in snapshot)
        {
            if (ev.Start < at && ev.End > at)
            {
                ev.Split(at - ev.Start);
            }
        }
    }

    // CloseGapsInFootprint butts the surviving pieces of one clip back together
    // from where the clip started. Only pieces inside that clip's own footprint
    // are touched, and every piece is given an ABSOLUTE position, so nothing
    // else on the timeline moves and doing it twice changes nothing.
    private static void CloseGapsInFootprint(Track track, Timecode footStart, Timecode footEnd)
    {
        List<TrackEvent> pieces = new List<TrackEvent>();
        foreach (TrackEvent ev in track.Events)
        {
            if (ev.Start >= footStart && ev.End <= footEnd)
            {
                pieces.Add(ev);
            }
        }
        pieces.Sort(delegate (TrackEvent a, TrackEvent b) { return a.Start.CompareTo(b.Start); });

        Timecode cursor = footStart;
        foreach (TrackEvent ev in pieces)
        {
            if (ev.Start != cursor)
            {
                ev.Start = cursor;
            }
            cursor = cursor + ev.Length;
        }
    }

    // ------------------------------------------------------- the headless proof

    // SelfTest builds a throwaway project from one media file, selects the
    // whole clip, runs the SAME code path a human click runs, and writes the
    // before/after geometry to a text file. It never saves the project and
    // never touches an existing one.
    private static void SelfTest(Vegas vegas, string mediaPath)
    {
        string reportPath = mediaPath + ".becky-cut-selftest.txt";
        List<string> log = new List<string>();
        try
        {
            log.Add("media: " + mediaPath);
            if (!File.Exists(mediaPath))
            {
                throw new FileNotFoundException("media not found", mediaPath);
            }

            Media media = new Media(mediaPath);
            VideoTrack vtrack = vegas.Project.AddVideoTrack();
            vtrack.Name = "Becky Cut selftest (video)";
            AudioTrack atrack = vegas.Project.AddAudioTrack();
            atrack.Name = "Becky Cut selftest (audio)";

            Timecode start = Timecode.FromSeconds(0.0);
            Timecode length = media.Length;
            log.Add("clip_length_seconds: " + (length.Nanos * 1e-7).ToString("0.###", CultureInfo.InvariantCulture));

            try
            {
                VideoStream vs = media.GetVideoStreamByIndex(0);
                if (vs != null)
                {
                    VideoEvent ve = vtrack.AddVideoEvent(start, length);
                    ve.AddTake(vs);
                    ve.Selected = true;
                }
            }
            catch (Exception ex)
            {
                log.Add("no video stream: " + ex.Message);
            }

            try
            {
                AudioStream au = media.GetAudioStreamByIndex(0);
                if (au != null)
                {
                    AudioEvent ae = atrack.AddAudioEvent(start, length);
                    ae.AddTake(au);
                    ae.Selected = true;
                }
            }
            catch (Exception ex)
            {
                log.Add("no audio stream: " + ex.Message);
            }

            List<Clip> clips = CollectSelected(vegas);
            log.Add("events_selected_before: " + clips.Count);
            if (clips.Count == 0)
            {
                throw new ApplicationException("nothing selected - the media placed no events");
            }

            string beckyExe = ResolveBeckyCut();
            EnsureBeckyExecutable(beckyExe);
            log.Add("becky_cut_exe: " + beckyExe);

            Dictionary<string, CutReport> bySource = new Dictionary<string, CutReport>(StringComparer.OrdinalIgnoreCase);
            foreach (Clip c in clips)
            {
                if (!bySource.ContainsKey(c.Source))
                {
                    bySource[c.Source] = null;
                }
            }
            string workDir = Path.Combine(Path.GetTempPath(), "BeckyVegasCut");
            Directory.CreateDirectory(workDir);
            foreach (string src in new List<string>(bySource.Keys))
            {
                bySource[src] = RunBeckyCut(beckyExe, src, workDir);
            }

            CutReport first = bySource[clips[0].Source];
            log.Add("becky_threshold: " + (first.threshold ?? "(none)"));
            log.Add("becky_decisions: " + first.decisions.Count);

            Options opts = OptionsFromEnvironment();
            if (opts == null)
            {
                opts = new Options { MinGapSeconds = DefaultMinGapSeconds, CloseGaps = false };
            }
            log.Add("min_gap_seconds: " + opts.MinGapSeconds.ToString("0.###", CultureInfo.InvariantCulture));
            log.Add("close_gaps: " + opts.CloseGaps);

            int removed = ApplyToTimeline(vegas, clips, bySource, opts);
            log.Add("pieces_removed: " + removed);

            // What the timeline looks like now - the actual proof.
            foreach (Track track in vegas.Project.Tracks)
            {
                int n = 0;
                double occupied = 0.0;
                double firstStart = -1.0;
                double lastEnd = 0.0;
                foreach (TrackEvent ev in track.Events)
                {
                    n++;
                    double s = ev.Start.Nanos * 1e-7;
                    double e = ev.End.Nanos * 1e-7;
                    if (firstStart < 0)
                    {
                        firstStart = s;
                    }
                    lastEnd = Math.Max(lastEnd, e);
                    occupied += e - s;
                }
                log.Add("track " + track.Index + " (" + track.Name + "): events=" + n +
                        " kept_seconds=" + occupied.ToString("0.###", CultureInfo.InvariantCulture) +
                        " first_start=" + Math.Max(firstStart, 0).ToString("0.###", CultureInfo.InvariantCulture) +
                        " last_end=" + lastEnd.ToString("0.###", CultureInfo.InvariantCulture));
            }
            log.Add("RESULT: OK");
        }
        catch (Exception ex)
        {
            log.Add("FATAL: " + ex);
            log.Add("RESULT: FAIL");
        }

        try
        {
            File.WriteAllLines(reportPath, log.ToArray());
        }
        catch
        {
            // Nothing useful left to do if even the report cannot be written.
        }
        vegas.Exit();
    }

    // --------------------------------------------------------- finding becky

    private static string ResolveBeckyCut()
    {
        string explicitPath = Environment.GetEnvironmentVariable("BECKY_CUT");
        if (!string.IsNullOrEmpty(explicitPath) && File.Exists(explicitPath.Trim()))
        {
            return Path.GetFullPath(explicitPath.Trim());
        }

        // This script normally lives in <repo>\vegas\, with the binaries built
        // into <repo>\becky-go\bin\.
        try
        {
            string scriptDir = Path.GetDirectoryName(new Uri(
                System.Reflection.Assembly.GetExecutingAssembly().CodeBase).LocalPath);
            if (!string.IsNullOrEmpty(scriptDir))
            {
                string candidate = Path.GetFullPath(Path.Combine(scriptDir, "..\\becky-go\\bin\\becky-cut.exe"));
                if (File.Exists(candidate))
                {
                    return candidate;
                }
            }
        }
        catch
        {
            // VEGAS compiles scripts in memory on some versions; fall through.
        }

        return ResolveExecutablePath("becky-cut.exe");
    }

    private static string ResolveExecutablePath(string commandOrPath)
    {
        if (string.IsNullOrEmpty(commandOrPath))
        {
            return commandOrPath;
        }

        string trimmed = commandOrPath.Trim();
        if (File.Exists(trimmed))
        {
            return Path.GetFullPath(trimmed);
        }

        string[] pathExts = (Environment.GetEnvironmentVariable("PATHEXT") ?? ".EXE;.CMD;.BAT")
            .Split(new[] { ';' }, StringSplitOptions.RemoveEmptyEntries);
        string[] pathDirs = (Environment.GetEnvironmentVariable("PATH") ?? string.Empty)
            .Split(new[] { ';' }, StringSplitOptions.RemoveEmptyEntries);

        bool hasExt = Path.HasExtension(trimmed);
        for (int i = 0; i < pathDirs.Length; i++)
        {
            string dir = pathDirs[i].Trim();
            if (dir.Length == 0)
            {
                continue;
            }

            if (hasExt)
            {
                string candidate = Path.Combine(dir, trimmed);
                if (File.Exists(candidate))
                {
                    return candidate;
                }
            }
            else
            {
                for (int e = 0; e < pathExts.Length; e++)
                {
                    string candidate = Path.Combine(dir, trimmed + pathExts[e]);
                    if (File.Exists(candidate))
                    {
                        return candidate;
                    }
                }
            }
        }

        return trimmed;
    }

    private static void EnsureBeckyExecutable(string beckyExe)
    {
        if (string.IsNullOrEmpty(beckyExe) || !File.Exists(beckyExe))
        {
            throw new FileNotFoundException(
                "becky-cut.exe was not found.\n" +
                "Checked: " + (beckyExe ?? "(nothing)") + "\n\n" +
                "Build the tools with build-all-tools.bat, then either add becky-go\\bin to PATH " +
                "or set BECKY_CUT to the full path of becky-cut.exe.",
                beckyExe ?? string.Empty
            );
        }
    }

    private static string Quote(string value)
    {
        if (value == null)
        {
            return "\"\"";
        }
        return "\"" + value.Replace("\"", "\\\"") + "\"";
    }

    private static string Tail(string s, int max)
    {
        if (string.IsNullOrEmpty(s) || s.Length <= max)
        {
            return s ?? string.Empty;
        }
        return s.Substring(s.Length - max);
    }
}
