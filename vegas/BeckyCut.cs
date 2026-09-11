// BeckyCut.cs - cut the dead air out of the events you have SELECTED, in place.
//
// ONE CLICK. NO QUESTIONS. THE SAME ANSWER EVERY TIME.
//
// Select events, run this, done. There is deliberately NO dialog and NO knob.
// The numbers were dialled in over nine months and they live inside becky-cut,
// which is the thing that actually works. A knob here would let a number drift
// away from the tested one, and would make the result depend on what got typed.
// If a number ever needs changing it changes in becky-cut, once, for every
// caller. Do not add a dialog back.
//
// WHERE THE DECISIONS COME FROM - becky-cut, and nothing else
//
// "becky-cut <file> --dry-run" measures THIS recording's own room tone against
// THIS recording's own speech to pick its threshold (becky-go/cmd/cut/level.go),
// cuts the silence with auto-editor, then makes a second pass with a Silero VAD
// so noise with nobody actually talking in it is cut too. It returns keep/cut
// spans and renders nothing. That is the entire edit. This script invents no
// threshold, no padding and no minimum gap - it applies what becky-cut returned,
// verbatim. It is an applicator, not a second opinion.
//
// THE RULE THAT KEEPS PICTURE AND SOUND TOGETHER
//
// The cut points are decided ONCE, from the AUDIO, and then applied to every
// selected event at the same ruler positions.
//
// That rule is the whole fix for the 2026-09-10 failure. Jordan's timeline pairs
// camera video (IURJ0280) with separately recorded sound (a different file
// entirely). The old version analysed each event's OWN source, so the picture
// was cut where the camera's mic was quiet and the sound was cut where the
// recorder was quiet - two different edits on two grouped tracks. What came out
// was video with no sound and sound with no picture. Per-event decisions can
// never be safe on a dual-system timeline; one decision applied to everything
// always is.
//
// AUTO-RIPPLE, ALWAYS
//
// Removing a span closes it. Everything to the right on the affected tracks
// moves left by exactly the span's length, so no gaps are left behind - that is
// what VEGAS's own auto-ripple does and it is what an editor expects. Spans are
// applied last-first so the ones still to come keep the ruler positions they
// were measured at, and every affected track is moved by the SAME delta, which
// is what keeps grouped picture and sound locked (the scripting object model
// moves one event at a time - group-follow is a UI behaviour, so both halves
// have to be moved, and are).
//
// "Affected tracks" means the tracks the selection lives on. A music bed or a
// title track you did not select is never touched.
//
// All of it is one UndoBlock. One Ctrl+Z puts the timeline back.
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
    public void FromVegas(Vegas vegas)
    {
        // BECKY_CUT_SELFTEST=<media file> is the headless proof: VEGAS builds a
        // throwaway one-clip project from that file, selects it, runs this
        // script's real code path, writes what changed to
        // <media>.becky-cut-selftest.txt and exits WITHOUT saving anything.
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

            string beckyExe = ResolveBeckyCut();
            EnsureBeckyExecutable(beckyExe);

            // Only the DECIDING events are listened to - see DecidingClips.
            // One becky-cut run per distinct source file, not per event.
            List<Clip> deciders = DecidingClips(clips);
            Dictionary<string, CutReport> bySource = new Dictionary<string, CutReport>(StringComparer.OrdinalIgnoreCase);
            foreach (Clip c in deciders)
            {
                if (!bySource.ContainsKey(c.Source))
                {
                    bySource[c.Source] = null;
                }
            }

            string workDir = Path.Combine(Path.GetTempPath(), "BeckyVegasCut");
            Directory.CreateDirectory(workDir);

            AnalyseWithProgress(beckyExe, bySource, workDir);

            List<Span> spans = SpansToRemove(deciders, bySource);
            int removed = ApplyToTimeline(vegas.Project, clips, spans);

            WarnIfVADSkipped(bySource);

            if (removed == 0)
            {
                MessageBox.Show(
                    "becky found nothing to cut in the selection.",
                    "Becky Cut",
                    MessageBoxButtons.OK,
                    MessageBoxIcon.Information
                );
            }

            // No "done" box when it worked. The shorter, gapless clips ARE the
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
    // are captured BEFORE anything is split, because that is the stretch of
    // ruler this event is allowed to contribute cut spans for.
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

    // The becky-cut --dry-run report. Field names match its JSON exactly.
    private class CutReport
    {
        public double fps;
        public double duration;
        public string threshold;
        public bool vadApplied;
        public List<Decision> decisions;
    }

    private class Decision
    {
        public string status; // "keep" or "cut"
        public double start;  // seconds into the source file
        public double end;
    }

    // Span is a stretch of RULER to remove. Ruler, not source - that is the
    // whole point: one span list gets applied to every affected track.
    private class Span
    {
        public Timecode Start;
        public Timecode End;
    }

    // Piece is one surviving fragment of the selection, tracked through the
    // splits so the regrouping at the end knows exactly which events came from
    // what Jordan selected - and touches nothing else on the timeline.
    //
    // WasGrouped is read BEFORE the edit starts. A piece whose original event
    // was not grouped is left ungrouped afterwards; inventing groups nobody
    // asked for would be its own nasty surprise.
    private class Piece
    {
        public TrackEvent Event;
        public bool WasGrouped;
    }

    // ------------------------------------------------------- reading the edit

    private static List<Clip> CollectSelected(Vegas vegas)
    {
        List<TrackEvent> chosen = new List<TrackEvent>();
        foreach (Track track in vegas.Project.Tracks)
        {
            foreach (TrackEvent ev in track.Events)
            {
                if (ev.Selected && !chosen.Contains(ev))
                {
                    chosen.Add(ev);
                }
            }
        }

        // Grouped partners come along whether they were clicked or not.
        // Jordan's picture and sound are grouped and "a cut needs to affect
        // both of them the same", so selecting either half puts both into the
        // edit. Without this, selecting only the audio would shorten the sound
        // and leave the picture at full length - the same desync by another
        // route. TrackEventGroup is a BaseList<TrackEvent>, so it enumerates.
        List<TrackEvent> withPartners = new List<TrackEvent>(chosen);
        foreach (TrackEvent selected in chosen)
        {
            if (!selected.IsGrouped)
            {
                continue;
            }
            TrackEventGroup group = selected.Group;
            if (group == null)
            {
                continue;
            }
            foreach (TrackEvent partner in group)
            {
                if (partner != null && !withPartners.Contains(partner))
                {
                    withPartners.Add(partner);
                }
            }
        }

        List<Clip> clips = new List<Clip>();
        foreach (TrackEvent ev in withPartners)
        {
            Track track = ev.Track;
            if (track == null)
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
        return clips;
    }

    // DecidingClips picks which of the selected events becky is allowed to
    // LISTEN to. Two rules, each there because breaking it desyncs the edit.
    //
    // 1. AUDIO WINS. On a dual-system timeline the picture file and the sound
    //    file are different files, and only the sound file is the one the edit
    //    should follow. Letting a camera clip vote as well is exactly how
    //    picture and sound ended up cut differently on 2026-09-10.
    //
    // 2. ONE DECIDER PER MOMENT. Two audio events covering the same stretch of
    //    ruler - say a camera scratch mic and the real recorder, both grouped to
    //    the same picture - have different noise floors, so becky returns two
    //    different cut lists for them. Merging those would mean "cut wherever
    //    EITHER was quiet", which lets the wrong list delete speech. So when
    //    deciders overlap, only the best one survives.
    //
    // The ranking is deterministic and never depends on track order: an audio
    // file that is NOT also a picture source in this selection wins first (that
    // is the dual-system signal - the separate recorder has no video on the
    // timeline), then the longer event, then the earlier one, then the path.
    //
    // With no audio event selected at all, fall back to what there is; a camera
    // clip still carries its own audio.
    private static List<Clip> DecidingClips(List<Clip> clips)
    {
        List<Clip> audio = new List<Clip>();
        List<string> pictureSources = new List<string>();
        foreach (Clip c in clips)
        {
            if (c.Event is AudioEvent)
            {
                audio.Add(c);
            }
            if (c.Event is VideoEvent && !pictureSources.Contains(c.Source))
            {
                pictureSources.Add(c.Source);
            }
        }

        List<Clip> ranked = new List<Clip>(audio.Count > 0 ? audio : clips);
        ranked.Sort(delegate (Clip a, Clip b)
        {
            bool aIsCamera = pictureSources.Contains(a.Source);
            bool bIsCamera = pictureSources.Contains(b.Source);
            if (aIsCamera != bIsCamera)
            {
                return aIsCamera ? 1 : -1;
            }
            int byLength = (b.End - b.Start).CompareTo(a.End - a.Start);
            if (byLength != 0)
            {
                return byLength;
            }
            int byStart = a.Start.CompareTo(b.Start);
            if (byStart != 0)
            {
                return byStart;
            }
            return string.Compare(a.Source, b.Source, StringComparison.OrdinalIgnoreCase);
        });

        List<Clip> deciders = new List<Clip>();
        foreach (Clip candidate in ranked)
        {
            bool overlapsOne = false;
            foreach (Clip kept in deciders)
            {
                if (candidate.Start < kept.End && kept.Start < candidate.End)
                {
                    overlapsOne = true;
                    break;
                }
            }
            if (!overlapsOne)
            {
                deciders.Add(candidate);
            }
        }
        return deciders;
    }

    // PlaybackRateOf returns the event's speed multiplier. A stretched event
    // consumes more (or less) source than its timeline length.
    //
    // PlaybackRate is declared on TrackEvent itself, so neither the VideoEvent
    // nor the AudioEvent cast this used to do was ever needed.
    private static double PlaybackRateOf(TrackEvent ev)
    {
        double rate = ev.PlaybackRate;
        return rate > 0 ? rate : 1.0;
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
            // stderr is drained on the child's own callback rather than with a
            // second blocking ReadToEnd. Sequential reads deadlock if the child
            // fills the stderr pipe while we are still draining stdout - and the
            // symptom here would be VEGAS hung behind a progress window with no
            // cancel button, which README section 0 says must never be
            // force-killed. becky-cut only writes stderr with --verbose today,
            // so this is cheap insurance, not a fix for a seen bug.
            StringBuilder errBuf = new StringBuilder();
            process.ErrorDataReceived += delegate (object sender, DataReceivedEventArgs e)
            {
                if (e.Data != null)
                {
                    errBuf.AppendLine(e.Data);
                }
            };
            process.BeginErrorReadLine();

            stdout = process.StandardOutput.ReadToEnd();
            process.WaitForExit();
            stderr = errBuf.ToString();
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

        // becky-cut's second pass is the VAD filter that cuts noise with nobody
        // talking in it. It SKIPS ITSELF with only a stderr warning when
        // silero_vad.onnx is missing, which would quietly turn this into a
        // silence-only edit - so the flag is read and reported rather than
        // assumed.
        report.vadApplied = Regex.IsMatch(json, "\"vad_applied\"\\s*:\\s*true");

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
    // WarnIfVADSkipped says so when becky-cut's second pass did not run.
    //
    // This is not a question and not a setting - it is the one thing that can
    // silently make the cut disobey becky-cut's own rules, and Jordan asked
    // specifically whether "becky-cut's second pass VAD rules are being
    // followed". A run where they were not has to say so out loud.
    private static void WarnIfVADSkipped(Dictionary<string, CutReport> bySource)
    {
        List<string> skipped = new List<string>();
        foreach (KeyValuePair<string, CutReport> entry in bySource)
        {
            if (entry.Value != null && !entry.Value.vadApplied)
            {
                skipped.Add(PathTail(entry.Key));
            }
        }
        if (skipped.Count == 0)
        {
            return;
        }

        MessageBox.Show(
            "becky-cut's second pass did not run, so this cut removed silence only -\n" +
            "noise with nobody talking in it was NOT cut.\n\n" +
            "Usually that means silero_vad.onnx is missing.\n\n" +
            string.Join("\n", skipped.ToArray()) + "\n\n" +
            "Press Ctrl+Z if you would rather undo it.",
            "Becky Cut",
            MessageBoxButtons.OK,
            MessageBoxIcon.Warning
        );
    }

    private static string PathTail(string path)
    {
        try
        {
            return Path.GetFileName(path);
        }
        catch
        {
            return path;
        }
    }

    // ----------------------------------------------------------- the edit

    // SpansToRemove turns becky's cut decisions into ONE list of ruler spans.
    //
    // Each decision is mapped through the event it came from (that event's take
    // offset and playback rate), clipped to that event's own footprint, snapped
    // to the project frame grid, and then merged with all the others. The result
    // is a single list that every affected track is cut by - which is what keeps
    // grouped picture and sound identical. See the header.
    private static List<Span> SpansToRemove(List<Clip> deciders,
                                            Dictionary<string, CutReport> bySource)
    {
        long perFrame = NanosPerFrame();
        List<Span> raw = new List<Span>();

        foreach (Clip clip in deciders)
        {
            CutReport report;
            if (!bySource.TryGetValue(clip.Source, out report))
            {
                continue;
            }
            if (report == null || report.decisions == null)
            {
                continue;
            }

            foreach (Decision d in report.decisions)
            {
                if (d == null || d.status != "cut")
                {
                    continue;
                }

                // becky's spans are seconds into the SOURCE file. Only the part
                // of one that this event actually shows can be removed.
                double cs = Math.Max(d.start, clip.SrcIn);
                double ce = Math.Min(d.end, clip.SrcOut);
                if (ce <= cs)
                {
                    continue;
                }

                Timecode start = SnapToFrame(RulerAt(clip, cs), perFrame);
                Timecode end = SnapToFrame(RulerAt(clip, ce), perFrame);
                if (end <= start)
                {
                    continue; // shorter than one frame once snapped - nothing to remove
                }

                raw.Add(new Span { Start = start, End = end });
            }
        }

        return MergeSpans(raw);
    }

    // MergeSpans sorts and unions overlapping or touching spans, so the same
    // stretch of ruler is never removed twice - which is what would happen with
    // two selected events off one source, or with picture and sound agreeing.
    private static List<Span> MergeSpans(List<Span> spans)
    {
        List<Span> merged = new List<Span>();
        if (spans.Count == 0)
        {
            return merged;
        }

        spans.Sort(delegate (Span a, Span b) { return a.Start.CompareTo(b.Start); });

        Span current = new Span { Start = spans[0].Start, End = spans[0].End };
        for (int i = 1; i < spans.Count; i++)
        {
            if (spans[i].Start <= current.End)
            {
                if (spans[i].End > current.End)
                {
                    current.End = spans[i].End;
                }
            }
            else
            {
                merged.Add(current);
                current = new Span { Start = spans[i].Start, End = spans[i].End };
            }
        }
        merged.Add(current);
        return merged;
    }

    // RulerAt converts a position in the source file to a position on the
    // ruler, through this event's own in-point and playback rate. That is what
    // makes this correct on a clip you have already trimmed or speed-changed.
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

    // SnapToFrame puts a cut on a real frame boundary.
    //
    // The grid comes from VEGAS itself: Timecode.FromFrames(1).Nanos IS one
    // frame, whatever the project's ruler format says a frame is. That beats
    // arithmetic on a frame rate we looked up, which lands NEAR a frame edge
    // rather than on it - and off-grid cut points are what leave one-frame
    // slivers behind. Picture and sound must be cut at the SAME instant or they
    // drift, so both go through this.
    private static Timecode SnapToFrame(Timecode t, long nanosPerFrame)
    {
        if (nanosPerFrame <= 0)
        {
            return t; // no grid to snap to: leave the position alone rather than invent one
        }
        // Timecode.FrameCount is VEGAS's own "which frame is this", so there is
        // no dividing of nanoseconds by an integer frame length and no rounding
        // error that grows with position on the ruler. Adding half a frame first
        // turns FrameCount's floor into round-to-nearest.
        Timecode half = Timecode.FromNanos(nanosPerFrame / 2);
        long frame = (t + half).FrameCount;
        if (frame < 0)
        {
            frame = 0;
        }
        return Timecode.FromFrames(frame);
    }

    // NanosPerFrame asks VEGAS how long one frame is. 0 means "do not snap".
    private static long NanosPerFrame()
    {
        try
        {
            long n = Timecode.FromFrames(1).Nanos;
            return n > 0 ? n : 0;
        }
        catch
        {
            return 0;
        }
    }

    // ApplyToTimeline removes every span and closes the hole behind it.
    //
    // Order matters three times over:
    //   1. Spans are applied LAST FIRST, so the ones still to come keep the
    //      ruler positions they were measured at.
    //   2. Within a span, every affected track is split at BOTH edges before
    //      anything is deleted, so the tracks stay in step with each other.
    //   3. The ripple moves every affected track by the SAME delta. Grouped
    //      picture and sound are separate objects to the scripting API
    //      (group-follow is a UI behaviour), so both halves must be moved
    //      explicitly - moving one and hoping the other follows is what pulled
    //      them apart before.
    //
    // One UndoBlock wraps the lot, so one Ctrl+Z puts the timeline back.
    private static int ApplyToTimeline(Project project, List<Clip> clips, List<Span> spans)
    {
        List<Track> tracks = AffectedTracks(clips);
        if (tracks.Count == 0 || spans.Count == 0)
        {
            return 0;
        }

        EnsureNothingLocked(tracks, spans);

        // Lineage. Every fragment the edit produces from the selection is
        // tracked here, so Regroup at the end knows precisely which events are
        // Jordan's and never reaches the rest of the timeline. IsGrouped is
        // sampled now, before anything is touched.
        List<Piece> scope = new List<Piece>();
        foreach (Clip c in clips)
        {
            scope.Add(new Piece { Event = c.Event, WasGrouped = c.Event.IsGrouped });
        }

        Timecode zero = Timecode.FromFrames(0);
        int removed = 0;

        using (UndoBlock undo = new UndoBlock("Becky Cut"))
        {
            for (int i = spans.Count - 1; i >= 0; i--)
            {
                Span span = spans[i];
                Timecode length = span.End - span.Start;
                if (length <= zero)
                {
                    continue;
                }

                foreach (Track track in tracks)
                {
                    SplitAt(track, span.Start, scope);
                    SplitAt(track, span.End, scope);
                }
                foreach (Track track in tracks)
                {
                    removed += DeleteInside(track, span, scope);
                }
                foreach (Track track in tracks)
                {
                    RippleLeft(track, span.End, length);
                }
            }

            // Inside the same UndoBlock on purpose: one Ctrl+Z has to put the
            // grouping back exactly as well as the cuts.
            Regroup(project, scope);
        }
        return removed;
    }

    // EnsureNothingLocked refuses the whole edit BEFORE anything is changed if a
    // locked event sits in the way of a cut.
    //
    // Split throws on a locked event, and UndoBlock has no Commit - Dispose
    // commits whatever happened, including a half-finished edit. Failing part
    // way through would leave the tracks out of step until Jordan noticed and
    // pressed Ctrl+Z. Checking first turns that into a clean refusal that
    // changed nothing at all.
    private static void EnsureNothingLocked(List<Track> tracks, List<Span> spans)
    {
        foreach (Track track in tracks)
        {
            foreach (TrackEvent ev in track.Events)
            {
                if (!ev.Locked)
                {
                    continue;
                }
                foreach (Span span in spans)
                {
                    if (ev.Start < span.End && span.Start < ev.End)
                    {
                        throw new ApplicationException(
                            "A locked event is in the way, so nothing was changed.\n\n" +
                            "It is on track " + (track.Index + 1) + ".\n\n" +
                            "Unlock it, or leave it out of the selection, and run this again."
                        );
                    }
                }
            }
        }
    }

    // AffectedTracks is every track the selection lives on - and only those. A
    // music bed or a title track you did not select is never cut and never
    // rippled.
    private static List<Track> AffectedTracks(List<Clip> clips)
    {
        List<Track> tracks = new List<Track>();
        foreach (Clip c in clips)
        {
            if (c.Track != null && !tracks.Contains(c.Track))
            {
                tracks.Add(c.Track);
            }
        }
        return tracks;
    }

    // SplitAt cuts whichever events on the track straddle this ruler position.
    // Iterating a SNAPSHOT matters: Split adds to track.Events while we look.
    private static void SplitAt(Track track, Timecode at, List<Piece> scope)
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
                Piece parent = FindPiece(scope, ev);
                // Deliberately NOT wrapped in try/catch. If a split fails on
                // one track and succeeds on another the tracks are already out
                // of step, and every later ripple makes it worse - a silent
                // half-done edit is the worst outcome available here. Let it
                // surface as an error instead; the UndoBlock means one Ctrl+Z
                // puts the timeline back.
                TrackEvent half = ev.Split(at - ev.Start);

                // The new half inherits the parent's lineage, so the regrouping
                // at the end sees every fragment of Jordan's selection.
                if (parent != null && half != null)
                {
                    scope.Add(new Piece { Event = half, WasGrouped = parent.WasGrouped });
                }
            }
        }
    }

    // DeleteInside removes whatever is now wholly inside the span.
    //
    // Snapshot first, like every other loop in this file. Whether Remove
    // cascades to a group partner on the same track is NOT documented; if it
    // ever does, walking live indices would skip an event or run off the end
    // half way through the edit. Remove returns false for something already
    // gone, so a cascade just means it is not counted twice.
    private static int DeleteInside(Track track, Span span, List<Piece> scope)
    {
        List<TrackEvent> snapshot = new List<TrackEvent>();
        foreach (TrackEvent ev in track.Events)
        {
            snapshot.Add(ev);
        }

        int removed = 0;
        foreach (TrackEvent ev in snapshot)
        {
            if (ev.Start >= span.Start && ev.End <= span.End)
            {
                if (track.Events.Remove(ev))
                {
                    removed++;
                    RemovePiece(scope, ev);
                }
            }
        }
        return removed;
    }

    // RippleLeft closes the hole: everything at or after "from" moves back by
    // "by". This is the auto-ripple. Without it the cut leaves a gap, which is
    // not an edit anyone can use.
    //
    // It assigns ABSOLUTE positions - every original Start is read first, then
    // each event is put at (its own original - by). It does NOT do the obvious
    // "ev.Start = ev.Start - by".
    //
    // That matters because whether setting TrackEvent.Start drags a grouped
    // partner along with it is NOT documented anywhere in the VEGAS API
    // reference. If it does, a relative subtraction moves some events twice -
    // which is exactly the picture/sound desync this script exists to prevent.
    // Reading the originals first makes the question irrelevant: an event that
    // something else already nudged still lands on its own target, and running
    // the whole thing twice changes nothing.
    private static void RippleLeft(Track track, Timecode from, Timecode by)
    {
        List<TrackEvent> events = new List<TrackEvent>();
        List<Timecode> origins = new List<Timecode>();
        foreach (TrackEvent ev in track.Events)
        {
            events.Add(ev);
            origins.Add(ev.Start);
        }

        for (int i = 0; i < events.Count; i++)
        {
            if (origins[i] >= from)
            {
                events[i].Start = origins[i] - by;
            }
        }
    }

    // ------------------------------------------------------------- regrouping

    // Regroup rebuilds one group per clip after the cut.
    //
    // WHY THIS EXISTS. Jordan, 2026-09-10, after the cut finally worked:
    //
    //   "After running becky-cut, all events that were affected are now grouped
    //    together in a DIFFERENT way ... if I try to delete a clip, it deletes
    //    ALL the clips because, even though they are separate events on the
    //    timeline, they are grouped as a single group, which is meaningfully
    //    different than grouping each clips audio to the corresponding video."
    //
    // That is VEGAS's own behaviour, not a bug in the cut: splitting a grouped
    // event leaves BOTH halves in the ORIGINAL group, so fourteen cuts turn one
    // video+audio pair into a single group of thirty events. Deleting any one of
    // them takes the whole edit with it.
    //
    // His manual workaround is exactly the two steps this does, and he spelled
    // them out: "if I highlight all the clips which were affected by becky-cut
    // and use the 'remove from group' feature, it ungroups them as a single
    // event, then allows me to use my 'Make Groups' script." So: strip the old
    // membership first, then build a fresh group per column. Done natively here,
    // so no third-party extension has to be installed or called.
    //
    // Scope is the lineage list, which is only ever the fragments of what he
    // selected - "just make sure it only applies to the clips I had selected on
    // the timeline - not the entire timeline".
    private static void Regroup(Project project, List<Piece> pieces)
    {
        List<Piece> live = new List<Piece>();
        foreach (Piece p in pieces)
        {
            if (p != null && p.Event != null && p.Event.Track != null)
            {
                live.Add(p);
            }
        }
        if (live.Count < 2)
        {
            return;
        }

        // Which fragments belong together: those that overlap in time on
        // DIFFERENT tracks. Butt-joined neighbours (one ends exactly where the
        // next starts) do not overlap, and that is precisely what keeps every
        // cut its own group instead of one long chain.
        int[] owner = new int[live.Count];
        for (int i = 0; i < live.Count; i++)
        {
            owner[i] = i;
        }
        for (int i = 0; i < live.Count; i++)
        {
            for (int j = i + 1; j < live.Count; j++)
            {
                TrackEvent a = live[i].Event;
                TrackEvent b = live[j].Event;
                if (a.Track == b.Track)
                {
                    continue;
                }
                if (a.Start < b.End && b.Start < a.End)
                {
                    Union(owner, i, j);
                }
            }
        }

        Dictionary<int, List<Piece>> columns = new Dictionary<int, List<Piece>>();
        for (int i = 0; i < live.Count; i++)
        {
            int root = Find(owner, i);
            if (!columns.ContainsKey(root))
            {
                columns[root] = new List<Piece>();
            }
            columns[root].Add(live[i]);
        }

        foreach (KeyValuePair<int, List<Piece>> column in columns)
        {
            List<Piece> members = column.Value;
            if (members.Count < 2)
            {
                continue;
            }

            // Only rebuild what was grouped to begin with.
            bool wasGrouped = false;
            foreach (Piece p in members)
            {
                if (p.WasGrouped)
                {
                    wasGrouped = true;
                    break;
                }
            }
            if (!wasGrouped)
            {
                continue;
            }

            // Step 1 - "remove from group".
            foreach (Piece p in members)
            {
                TrackEventGroup old = p.Event.Group;
                if (old != null)
                {
                    old.Remove(p.Event);
                }
            }

            // Step 2 - "Make Groups".
            TrackEventGroup fresh = new TrackEventGroup(project);
            project.TrackEventGroups.Add(fresh);
            foreach (Piece p in members)
            {
                fresh.Add(p.Event);
            }
        }

        // The old whole-selection group is empty now. Drop any group the rebuild
        // emptied out, backwards so the indices stay valid.
        for (int i = project.TrackEventGroups.Count - 1; i >= 0; i--)
        {
            if (project.TrackEventGroups[i].Count == 0)
            {
                project.TrackEventGroups.RemoveAt(i);
            }
        }
    }

    private static int Find(int[] owner, int i)
    {
        while (owner[i] != i)
        {
            owner[i] = owner[owner[i]];
            i = owner[i];
        }
        return i;
    }

    private static void Union(int[] owner, int a, int b)
    {
        int rootA = Find(owner, a);
        int rootB = Find(owner, b);
        if (rootA != rootB)
        {
            owner[rootB] = rootA;
        }
    }

    // FindPiece / RemovePiece match on TrackEvent.Equals, NOT reference
    // identity. The VEGAS wrappers override Equals and op_Equality, so two
    // different wrapper objects can point at the same timeline event - matching
    // by reference would miss those and silently drop a fragment's lineage.
    private static Piece FindPiece(List<Piece> scope, TrackEvent ev)
    {
        foreach (Piece p in scope)
        {
            if (p.Event != null && p.Event.Equals(ev))
            {
                return p;
            }
        }
        return null;
    }

    private static void RemovePiece(List<Piece> scope, TrackEvent ev)
    {
        for (int i = scope.Count - 1; i >= 0; i--)
        {
            if (scope[i].Event != null && scope[i].Event.Equals(ev))
            {
                scope.RemoveAt(i);
            }
        }
    }

    // ------------------------------------------------------- the headless proof

    // SelfTest builds a throwaway project from one media file, selects the
    // whole clip on both a video and an audio track, runs the SAME code path a
    // human click runs, and writes the before/after geometry to a text file. It
    // never saves the project and never touches an existing one.
    //
    // The line worth reading in the output is the pair of track lines: the
    // video and audio tracks must end up with the SAME event count and the SAME
    // kept seconds. If they differ, picture and sound have come apart, which is
    // the exact failure this script exists to avoid.
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

            List<Clip> deciders = DecidingClips(clips);
            log.Add("deciding_events: " + deciders.Count + " (audio wins when there is any)");

            Dictionary<string, CutReport> bySource = new Dictionary<string, CutReport>(StringComparer.OrdinalIgnoreCase);
            foreach (Clip c in deciders)
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

            CutReport first = bySource[deciders[0].Source];
            log.Add("becky_threshold: " + (first.threshold ?? "(none)"));
            log.Add("becky_decisions: " + first.decisions.Count);

            log.Add("nanos_per_frame: " + NanosPerFrame());

            List<Span> spans = SpansToRemove(deciders, bySource);
            log.Add("spans_to_remove: " + spans.Count);
            log.Add("vad_applied: " + first.vadApplied);

            double spanSeconds = 0.0;
            foreach (Span s in spans)
            {
                spanSeconds += (s.End - s.Start).Nanos * 1e-7;
            }
            log.Add("spans_total_seconds: " + spanSeconds.ToString("0.###", CultureInfo.InvariantCulture));

            int removed = ApplyToTimeline(vegas.Project, clips, spans);
            log.Add("pieces_removed: " + removed);

            // What the timeline looks like now - the actual proof. Video and
            // audio must match each other, and nothing should start after the
            // last end (no gaps left behind by the ripple).
            foreach (Track track in vegas.Project.Tracks)
            {
                int n = 0;
                double occupied = 0.0;
                double firstStart = -1.0;
                double lastEnd = 0.0;
                double gaps = 0.0;
                double cursor = -1.0;
                foreach (TrackEvent ev in track.Events)
                {
                    n++;
                    double s = ev.Start.Nanos * 1e-7;
                    double e = ev.End.Nanos * 1e-7;
                    if (firstStart < 0)
                    {
                        firstStart = s;
                        cursor = s;
                    }
                    if (s > cursor)
                    {
                        gaps += s - cursor;
                    }
                    cursor = Math.Max(cursor, e);
                    lastEnd = Math.Max(lastEnd, e);
                    occupied += e - s;
                }
                log.Add("track " + track.Index + " (" + track.Name + "): events=" + n +
                        " kept_seconds=" + occupied.ToString("0.###", CultureInfo.InvariantCulture) +
                        " first_start=" + Math.Max(firstStart, 0).ToString("0.###", CultureInfo.InvariantCulture) +
                        " last_end=" + lastEnd.ToString("0.###", CultureInfo.InvariantCulture) +
                        " gap_seconds=" + gaps.ToString("0.###", CultureInfo.InvariantCulture));
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
