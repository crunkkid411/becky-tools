// BeckyCaptions.cs - captions on the VEGAS Pro timeline, timed by becky.
//
// ADAPTED FROM louismathy/vegas-script (WhisperAutoSubtitles.cs). The parts that
// make it pleasant to use are his and are kept as they were: the style dialog
// with its live preview, the elapsed-time progress window, the RTF text-setting
// for Titles & Text, and the timeline placement loop. Thank you.
//
// WHAT CHANGED, AND WHY
//
// 1. TRANSCRIPTION -> becky. The original shells out to `whisper`. This calls
//    becky-subtitle, which asks becky-captions FIRST whether a trustworthy
//    official transcript already exists for the media (and refuses one that is
//    short because the stream was edited), and only then runs local ASR. That
//    is becky's settled acquisition order, and it can save the slow step
//    entirely on downloaded footage.
//
// 2. CHUNKING -> becky. The original splits every N words, which is why lines
//    break mid-thought. becky's chunker (internal/subs) is pace-driven: it
//    breaks where the speaker actually pauses, caps a line at 22 characters,
//    never ends a line on a dangling "a"/"the"/"to", floors every caption so
//    none is a one-frame flash, and closes every gap so nothing blinks off
//    between two captions.
//
// 3. THE EDIT IS READ, NOT IGNORED. This is the real fix. The original
//    transcribes ONE media file and lays captions from 0, so the moment you cut
//    anything the words drift away from the picture. This hands becky every
//    event on the track - source file, the [in,out] of that source, and where
//    the event sits on the ruler - so captions are snapped to YOUR cuts and
//    placed back at the right ruler position, gaps included.
//
// 4. Whisper's model/language/split prompts are gone: becky decides those. The
//    style dialog is untouched.
//
// REQUIREMENTS: becky-subtitle.exe (and becky-transcribe.exe beside it) built by
// build-all-tools.bat. Point BECKY_SUBTITLE at it, or put becky-go\bin on PATH.
//
// VEGAS 14-22 use ScriptPortal.Vegas (below). On VEGAS 13 or older change the
// using line to Sony.Vegas.

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Globalization;
using System.IO;
using System.Text;
using System.Text.RegularExpressions;
using System.Windows.Forms;
using ScriptPortal.Vegas;

public class EntryPoint
{
    // The track captions are written to. Re-running the script reuses and clears
    // this track instead of stacking a second one on top of the first.
    private const string CaptionTrackName = "Becky Captions";

    public void FromVegas(Vegas vegas)
    {
        try
        {
            List<SourceEvent> events = CollectEvents(vegas);
            if (events == null)
            {
                return; // user cancelled the file picker
            }

            if (events.Count == 0)
            {
                throw new ApplicationException(
                    "Nothing to caption.\n\n" +
                    "Select the events you want captioned, or just leave the timeline as it is " +
                    "and this will caption the first video track that has media on it."
                );
            }

            // No style prompt. The look is becky-review-3's, settled long ago, and
            // asking again every run is the opposite of deterministic.
            SubtitleStyleSettings style = BeckyStyle();

            string beckyExe = ResolveBeckySubtitle();
            EnsureBeckyExecutable(beckyExe);

            // The two JSON files and the log are machinery - they stay in temp.
            string workDir = Path.Combine(Path.GetTempPath(), "BeckyVegasCaptions", Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(workDir);

            string timelineJson = Path.Combine(workDir, "timeline.json");
            string cuesJson = Path.Combine(workDir, "cues.json");
            // The .srt is a DELIVERABLE, so it lands beside the clip it captions,
            // not in AppData.
            string srtPath = SrtPathBesideClip(events, workDir);
            WriteTimelineJson(timelineJson, vegas, events);

            RunBeckyWithProgress(beckyExe, timelineJson, cuesJson, srtPath, workDir, events.Count);

            List<Caption> captions = ParseCuesJson(cuesJson);
            if (captions.Count == 0)
            {
                throw new ApplicationException(
                    "becky finished but produced no captions.\n\n" +
                    "That usually means the media has no speech in the range you selected, " +
                    "or the transcript could not be made. The log is in:\n" + workDir
                );
            }

            AddSubtitlesToTimeline(vegas, captions, style);

            // No "done" box. The captions ARE the confirmation - they are on the
            // timeline in front of you. One click in, captions out.
        }
        catch (Exception ex)
        {
            MessageBox.Show(
                ex.Message,
                "Becky Captions",
                MessageBoxButtons.OK,
                MessageBoxIcon.Error
            );
        }
    }

    // ---------------------------------------------------------------- model

    // SourceEvent is one timeline event becky needs to know about: which file,
    // which part of it, and where it sits on the ruler.
    private class SourceEvent
    {
        public string Source;
        public double In;
        public double Out;
        public double Timeline;
        public int Track;
        public string Label;
    }

    private class Caption
    {
        public TimeSpan Start;
        public TimeSpan End;
        public string Text;
    }

    private class SubtitleStyleSettings
    {
        public string FontName;
        public int FontSize;
        public Color TextColor;
        public Color OutlineColor;
        public int OutlineWidth;
        public bool UseLineBreaks;
        public int WordsPerLine;
    }

    // ------------------------------------------------------- reading the edit

    // CollectEvents decides WHAT gets captioned, in this order:
    //
    //   1. the events you have selected (any track), else
    //   2. every event on the first video track that has media, else
    //   3. every event on the first audio track that has media, else
    //   4. a file you pick, treated as one full-length event at ruler 0
    //      (this is the original script's behaviour, kept as the fallback).
    //
    // Returns null only when the user cancels the picker in case 4.
    private static List<SourceEvent> CollectEvents(Vegas vegas)
    {
        List<SourceEvent> selected = new List<SourceEvent>();
        foreach (Track track in vegas.Project.Tracks)
        {
            foreach (TrackEvent ev in track.Events)
            {
                if (ev.Selected)
                {
                    AddEvent(selected, ev, track);
                }
            }
        }
        if (selected.Count > 0)
        {
            return selected;
        }

        // No selection: take one whole track, preferring video.
        List<SourceEvent> fromTrack = FirstTrackWithMedia(vegas, true);
        if (fromTrack.Count == 0)
        {
            fromTrack = FirstTrackWithMedia(vegas, false);
        }
        if (fromTrack.Count > 0)
        {
            return fromTrack;
        }

        // Empty timeline: fall back to the original script's single-file flow.
        using (OpenFileDialog ofd = new OpenFileDialog())
        {
            ofd.Title = "Pick a media file to caption";
            ofd.Filter = "Media files|*.wav;*.mp3;*.m4a;*.flac;*.aac;*.ogg;*.mp4;*.mov;*.mkv;*.avi|All files|*.*";
            ofd.Multiselect = false;
            if (ofd.ShowDialog() != DialogResult.OK)
            {
                return null;
            }

            List<SourceEvent> one = new List<SourceEvent>();
            one.Add(new SourceEvent
            {
                Source = ofd.FileName,
                In = 0.0,
                // 0 means "to the end of the file" - filled in below from the
                // media's own length once Vegas has opened it.
                Out = MediaLengthSeconds(vegas, ofd.FileName),
                Timeline = 0.0,
                Track = 0,
                Label = Path.GetFileNameWithoutExtension(ofd.FileName)
            });
            return one;
        }
    }

    private static List<SourceEvent> FirstTrackWithMedia(Vegas vegas, bool wantVideo)
    {
        foreach (Track track in vegas.Project.Tracks)
        {
            if (track.IsVideo() != wantVideo)
            {
                continue;
            }

            List<SourceEvent> events = new List<SourceEvent>();
            foreach (TrackEvent ev in track.Events)
            {
                AddEvent(events, ev, track);
            }
            if (events.Count > 0)
            {
                return events;
            }
        }
        return new List<SourceEvent>();
    }

    // AddEvent converts one Vegas event into a SourceEvent. Events with no media
    // on disk are skipped - becky can only transcribe a real file.
    private static void AddEvent(List<SourceEvent> into, TrackEvent ev, Track track)
    {
        if (ev == null || ev.ActiveTake == null || ev.ActiveTake.Media == null)
        {
            return;
        }

        string path = ev.ActiveTake.Media.FilePath;
        if (string.IsNullOrWhiteSpace(path) || !File.Exists(path))
        {
            return;
        }

        double lengthSec = ev.Length.ToMilliseconds() / 1000.0;
        if (lengthSec <= 0)
        {
            return;
        }

        // Take.Offset is where this event starts INSIDE the source file, which is
        // exactly the in-point becky needs. A speed-changed event would make
        // in+length wrong, so scale by the playback rate when Vegas reports one.
        double inSec = ev.ActiveTake.Offset.ToMilliseconds() / 1000.0;
        double consumed = lengthSec * PlaybackRateOf(ev);

        into.Add(new SourceEvent
        {
            Source = path,
            In = inSec,
            Out = inSec + consumed,
            Timeline = ev.Start.ToMilliseconds() / 1000.0,
            Track = track.Index,
            Label = Path.GetFileNameWithoutExtension(path)
        });
    }

    // PlaybackRateOf returns the event's speed multiplier, or 1.0 when the event
    // type doesn't expose one. A stretched event consumes more (or less) source
    // than its timeline length.
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

    private static double MediaLengthSeconds(Vegas vegas, string path)
    {
        try
        {
            Media m = new Media(path);
            if (m != null && m.Length.ToMilliseconds() > 0)
            {
                return m.Length.ToMilliseconds() / 1000.0;
            }
        }
        catch
        {
            // Unreadable here is not fatal - becky probes the file itself.
        }
        return 0.0;
    }

    // ------------------------------------------------------ talking to becky

    // WriteTimelineJson emits the contract internal/edl.VegasTimeline reads.
    // Hand-rolled because a Vegas script has no JSON writer it can count on.
    private static void WriteTimelineJson(string path, Vegas vegas, List<SourceEvent> events)
    {
        double fps = 0.0;
        try
        {
            fps = vegas.Project.Video.FrameRate;
        }
        catch
        {
            fps = 0.0;
        }

        StringBuilder sb = new StringBuilder();
        sb.Append("{\n");
        sb.Append("  \"version\": \"1\",\n");
        sb.Append("  \"project\": \"").Append(JsonEscape(ProjectPath(vegas))).Append("\",\n");
        sb.Append("  \"fps\": ").Append(Num(fps)).Append(",\n");
        sb.Append("  \"events\": [\n");
        for (int i = 0; i < events.Count; i++)
        {
            SourceEvent e = events[i];
            sb.Append("    {");
            sb.Append("\"source\": \"").Append(JsonEscape(e.Source)).Append("\", ");
            sb.Append("\"in\": ").Append(Num(e.In)).Append(", ");
            sb.Append("\"out\": ").Append(Num(e.Out)).Append(", ");
            sb.Append("\"timeline\": ").Append(Num(e.Timeline)).Append(", ");
            sb.Append("\"track\": ").Append(e.Track.ToString(CultureInfo.InvariantCulture)).Append(", ");
            sb.Append("\"label\": \"").Append(JsonEscape(e.Label)).Append("\"}");
            if (i < events.Count - 1)
            {
                sb.Append(",");
            }
            sb.Append("\n");
        }
        sb.Append("  ]\n");
        sb.Append("}\n");

        File.WriteAllText(path, sb.ToString(), new UTF8Encoding(false));
    }

    private static string ProjectPath(Vegas vegas)
    {
        try
        {
            return vegas.Project.FilePath ?? string.Empty;
        }
        catch
        {
            return string.Empty;
        }
    }

    private static string Num(double v)
    {
        return v.ToString("0.######", CultureInfo.InvariantCulture);
    }

    private static string JsonEscape(string s)
    {
        if (string.IsNullOrEmpty(s))
        {
            return string.Empty;
        }

        StringBuilder sb = new StringBuilder(s.Length + 16);
        foreach (char c in s)
        {
            switch (c)
            {
                case '\\': sb.Append("\\\\"); break;
                case '"': sb.Append("\\\""); break;
                case '\n': sb.Append("\\n"); break;
                case '\r': sb.Append("\\r"); break;
                case '\t': sb.Append("\\t"); break;
                default:
                    if (c < 0x20)
                    {
                        sb.Append("\\u").Append(((int)c).ToString("x4", CultureInfo.InvariantCulture));
                    }
                    else
                    {
                        sb.Append(c);
                    }
                    break;
            }
        }
        return sb.ToString();
    }

    private static void RunBecky(string beckyExe, string timelineJson, string cuesJson, string srtPath, string workDir)
    {
        StringBuilder args = new StringBuilder();
        args.Append("--timeline ").Append(Quote(timelineJson));
        args.Append(" --cues ").Append(Quote(cuesJson));
        args.Append(" --out ").Append(Quote(srtPath));
        args.Append(" --verbose");

        // The LLM regroup pass is OFF, matching becky-review-3 (Jordan 2026-07-24,
        // "pause the llm step"): the deterministic pace chunker already honours
        // every caption rule on his real footage. becky-subtitle's own --review
        // default is still true, which is why the first VEGAS run sat through two
        // 90-second OpenCode Zen timeouts and then fell back to exactly these
        // deterministic captions anyway. Off means instant and identical.
        // Set BECKY_CAPTIONS_REVIEW=1 to opt back in.
        string review = Environment.GetEnvironmentVariable("BECKY_CAPTIONS_REVIEW");
        bool wantReview = !string.IsNullOrWhiteSpace(review) &&
                          (review.Trim() == "1" || review.Trim().ToLowerInvariant() == "true");
        if (!wantReview)
        {
            args.Append(" --review=false");
        }

        ProcessStartInfo psi = new ProcessStartInfo
        {
            FileName = beckyExe,
            Arguments = args.ToString(),
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
                "Could not start becky-subtitle.\n" +
                "Executable: " + beckyExe + "\n" +
                "Details: " + ex.Message
            );
        }

        using (process)
        {
            if (process == null)
            {
                throw new ApplicationException("Failed to start becky-subtitle.");
            }

            stdout = process.StandardOutput.ReadToEnd();
            stderr = process.StandardError.ReadToEnd();
            process.WaitForExit();
            exitCode = process.ExitCode;
        }

        // Keep the run's own log next to its output - the first thing to read
        // when a caption lands somewhere surprising.
        try
        {
            File.WriteAllText(Path.Combine(workDir, "becky.log"),
                "exit " + exitCode + "\n\n--- stdout ---\n" + stdout + "\n--- stderr ---\n" + stderr,
                new UTF8Encoding(false));
        }
        catch
        {
            // A log we cannot write must not fail the run.
        }

        if (File.Exists(cuesJson))
        {
            return;
        }

        string message = "becky-subtitle did not produce any captions.";
        if (exitCode != 0)
        {
            message += "\nExit code: " + exitCode;
        }

        if (!string.IsNullOrWhiteSpace(stderr))
        {
            string lower = stderr.ToLowerInvariant();
            if (lower.Contains("becky-transcribe not found"))
            {
                message += "\n\nbecky-transcribe is missing. Run build-all-tools.bat so it sits " +
                           "beside becky-subtitle.exe.";
            }
            message += "\n\n" + Tail(stderr, 1200);
        }
        else if (!string.IsNullOrWhiteSpace(stdout))
        {
            message += "\n\n" + Tail(stdout, 1200);
        }

        throw new ApplicationException(message);
    }

    private static string Tail(string s, int max)
    {
        if (string.IsNullOrEmpty(s) || s.Length <= max)
        {
            return s ?? string.Empty;
        }
        return s.Substring(s.Length - max);
    }

    // RunBeckyWithProgress is the original's progress window, unchanged in shape:
    // a marquee bar with an elapsed clock while the slow work happens off the UI
    // thread. Transcription is still the slow part; becky just decides whether it
    // has to happen at all.
    private static void RunBeckyWithProgress(string beckyExe, string timelineJson, string cuesJson,
                                             string srtPath, string workDir, int eventCount)
    {
        Exception workerError = null;

        using (Form progressForm = new Form())
        using (Label statusLabel = new Label())
        using (ProgressBar progressBar = new ProgressBar())
        using (System.Windows.Forms.Timer timer = new System.Windows.Forms.Timer())
        using (BackgroundWorker worker = new BackgroundWorker())
        {
            progressForm.Text = "Becky Captions";
            progressForm.Width = 560;
            progressForm.Height = 180;
            progressForm.StartPosition = FormStartPosition.CenterScreen;
            progressForm.FormBorderStyle = FormBorderStyle.FixedDialog;
            progressForm.MaximizeBox = false;
            progressForm.MinimizeBox = false;
            progressForm.ControlBox = false;

            statusLabel.Left = 12;
            statusLabel.Top = 12;
            statusLabel.Width = 520;
            statusLabel.Height = 46;
            statusLabel.Text = "Captioning " + eventCount + " event(s).\n" +
                               "First run on a file has to transcribe it, which takes a while.";

            progressBar.Left = 12;
            progressBar.Top = 68;
            progressBar.Width = 520;
            progressBar.Style = ProgressBarStyle.Marquee;
            progressBar.MarqueeAnimationSpeed = 30;

            progressForm.Controls.Add(statusLabel);
            progressForm.Controls.Add(progressBar);

            DateTime startedAt = DateTime.UtcNow;
            timer.Interval = 500;
            timer.Tick += delegate
            {
                TimeSpan elapsed = DateTime.UtcNow - startedAt;
                statusLabel.Text = "Captioning " + eventCount + " event(s).  Elapsed: " +
                                   elapsed.ToString(@"hh\:mm\:ss") + "\n" +
                                   "First run on a file has to transcribe it, which takes a while.";
            };
            timer.Start();

            worker.DoWork += delegate
            {
                RunBecky(beckyExe, timelineJson, cuesJson, srtPath, workDir);
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

    // ParseCuesJson reads becky's --cues file. The times are already VEGAS RULER
    // SECONDS - becky mapped them back through the edit, including its gaps - so
    // there is no arithmetic to do here.
    //
    // Regex rather than a JSON parser for the same reason the original used one:
    // a Vegas script cannot rely on one being available. becky writes the fields
    // in a fixed order specifically so this stays a one-liner (cmd/subtitle/cues.go).
    private static List<Caption> ParseCuesJson(string path)
    {
        List<Caption> captions = new List<Caption>();
        if (!File.Exists(path))
        {
            return captions;
        }

        string json = File.ReadAllText(path, Encoding.UTF8);
        MatchCollection matches = Regex.Matches(
            json,
            "\\{\\s*\"start\"\\s*:\\s*(-?[\\d.]+)\\s*,\\s*\"end\"\\s*:\\s*(-?[\\d.]+)\\s*,\\s*\"text\"\\s*:\\s*\"((?:[^\"\\\\]|\\\\.)*)\"\\s*\\}",
            RegexOptions.Singleline);

        foreach (Match m in matches)
        {
            double start;
            double end;
            if (!double.TryParse(m.Groups[1].Value, NumberStyles.Float, CultureInfo.InvariantCulture, out start) ||
                !double.TryParse(m.Groups[2].Value, NumberStyles.Float, CultureInfo.InvariantCulture, out end))
            {
                continue;
            }

            string text = JsonUnescape(m.Groups[3].Value).Trim();
            if (text.Length == 0)
            {
                continue;
            }

            if (end <= start)
            {
                end = start + 0.1;
            }

            captions.Add(new Caption
            {
                Start = TimeSpan.FromSeconds(start),
                End = TimeSpan.FromSeconds(end),
                Text = text
            });
        }

        return captions;
    }

    private static string JsonUnescape(string value)
    {
        if (string.IsNullOrEmpty(value))
        {
            return value;
        }

        StringBuilder sb = new StringBuilder(value.Length);
        for (int i = 0; i < value.Length; i++)
        {
            char c = value[i];
            if (c != '\\' || i + 1 >= value.Length)
            {
                sb.Append(c);
                continue;
            }

            i++;
            char esc = value[i];
            switch (esc)
            {
                case 'n': sb.Append('\n'); break;
                case 'r': sb.Append('\r'); break;
                case 't': sb.Append('\t'); break;
                case 'b': sb.Append('\b'); break;
                case 'f': sb.Append('\f'); break;
                case '/': sb.Append('/'); break;
                case '"': sb.Append('"'); break;
                case '\\': sb.Append('\\'); break;
                case 'u':
                    if (i + 4 < value.Length)
                    {
                        int code;
                        if (int.TryParse(value.Substring(i + 1, 4), NumberStyles.HexNumber,
                                         CultureInfo.InvariantCulture, out code))
                        {
                            sb.Append((char)code);
                            i += 4;
                        }
                    }
                    break;
                default:
                    sb.Append(esc);
                    break;
            }
        }
        return sb.ToString();
    }

    // ------------------------------------------------- finding becky-subtitle

    // ResolveBeckySubtitle looks in becky's usual order: an explicit
    // BECKY_SUBTITLE, then becky-go\bin next to this script's repo, then PATH.
    private static string ResolveBeckySubtitle()
    {
        string explicitPath = Environment.GetEnvironmentVariable("BECKY_SUBTITLE");
        if (!string.IsNullOrWhiteSpace(explicitPath) && File.Exists(explicitPath.Trim()))
        {
            return Path.GetFullPath(explicitPath.Trim());
        }

        // This script normally lives in <repo>\vegas\, with the binaries built
        // into <repo>\becky-go\bin\.
        try
        {
            string scriptDir = Path.GetDirectoryName(new Uri(
                System.Reflection.Assembly.GetExecutingAssembly().CodeBase).LocalPath);
            if (!string.IsNullOrWhiteSpace(scriptDir))
            {
                string candidate = Path.GetFullPath(Path.Combine(scriptDir, "..\\becky-go\\bin\\becky-subtitle.exe"));
                if (File.Exists(candidate))
                {
                    return candidate;
                }
            }
        }
        catch
        {
            // Vegas compiles scripts in memory on some versions; fall through.
        }

        return ResolveExecutablePath("becky-subtitle.exe");
    }

    private static string ResolveExecutablePath(string commandOrPath)
    {
        if (string.IsNullOrWhiteSpace(commandOrPath))
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
        if (string.IsNullOrWhiteSpace(beckyExe) || !File.Exists(beckyExe))
        {
            throw new FileNotFoundException(
                "becky-subtitle.exe was not found.\n" +
                "Checked: " + (beckyExe ?? "(nothing)") + "\n\n" +
                "Build the tools with build-all-tools.bat, then either add becky-go\\bin to PATH " +
                "or set BECKY_SUBTITLE to the full path of becky-subtitle.exe.",
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

    // -------------------------------------------------------------- the style
    // becky-review-3's caption look, as constants. There is no style dialog: the
    // look was settled long ago and re-asking every run is the opposite of
    // deterministic. To change the house style, change it here.
    //
    // The source of truth is subs.DefaultStyle() (becky-go/internal/subs/style.go)
    // as drawn by native/becky-review/captions.cpp: white text, thin black
    // outline, no shadow, centred.

    // THE STYLE LIVES IN A VEGAS PRESET, not in this file.
    //
    // "becky-captions" is a Titles & Text preset Jordan built by hand in VEGAS,
    // where a caption style can actually be judged. When it is present it wins
    // outright and every constant below is ignored: the script applies the
    // preset and swaps only the words. That is also how VEGAS's own subtitle
    // tools (and the UltraPaste extension) do it.
    //
    // On this machine the preset is Proxima Nova bold 9pt, white, outline width
    // 10, shadow on - none of which this script could have guessed.
    private const string BeckyPresetName = "becky-captions";

    // ---- fallback only: used when a machine has no "becky-captions" preset ----

    // The Windows FAMILY name. becky's libass style says "ProximaNova-Semibold";
    // the family VEGAS wants on this machine is "Proxima Nova Lt" (Jordan, after
    // seeing it render - the plain "Proxima Nova" family came out wrong).
    private const string BeckyFontName = "Proxima Nova Lt";

    // Point size for the Titles & Text generator. 24 rendered too big on screen
    // (Jordan, after looking at it), so this is now becky's own caption size:
    // subs.DefaultStyle().FontSize, the 12 that becky-review-3 draws with.
    //
    // Still the ONE number to change if the captions come out too big or small.
    // It cannot be checked from here - vegas.SaveSnapshot() returns fully
    // transparent frames when VEGAS is driven by -SCRIPT, so the only way to
    // judge the size is to look at a real preview.
    private const int BeckyFontPointSize = 12;

    // Thin black outline, no shadow - subs.DefaultStyle() is Outline=1, Shadow=0.
    // Dropped from 2 to 1 alongside the font size: an outline is only "thin"
    // relative to the text it edges, and keeping 2 under a 12pt font would have
    // doubled its apparent weight. Bump back to 2 if it reads too faint.
    private const int BeckyOutlineWidth = 1;

    // The captions .srt is named "<clip>.becky.srt", NOT "<clip>.srt". That is
    // deliberate: becky-captions treats "<stem>.srt" beside a video as an
    // OFFICIAL transcript (internal/captions/captions.go), so writing there would
    // make the next run mistake becky's own output for an official subtitle,
    // skip transcription, and collapse each cue into one caption. Same folder as
    // the clip, distinct name, no self-poisoning.
    private const string BeckySrtSuffix = ".becky.srt";

    private static SubtitleStyleSettings BeckyStyle()
    {
        return new SubtitleStyleSettings
        {
            FontName = BeckyFontName,
            FontSize = BeckyFontPointSize,
            TextColor = Color.White,
            OutlineColor = Color.Black,
            OutlineWidth = BeckyOutlineWidth,
            // becky already decided where every line breaks, using the pause
            // pacing and the 22-character cap. Re-wrapping here would undo it.
            UseLineBreaks = false,
            WordsPerLine = 0
        };
    }

    // SrtPathBesideClip puts the .srt in the same folder as the clip it captions,
    // because the .srt is a deliverable, not machinery. Falls back to the work
    // directory when that folder cannot be written (a read-only or protected
    // evidence drive) rather than failing the run.
    private static string SrtPathBesideClip(List<SourceEvent> events, string fallbackDir)
    {
        foreach (SourceEvent ev in events)
        {
            if (ev == null || string.IsNullOrEmpty(ev.Source))
            {
                continue;
            }

            string dir;
            string stem;
            try
            {
                dir = Path.GetDirectoryName(ev.Source);
                stem = Path.GetFileNameWithoutExtension(ev.Source);
            }
            catch (ArgumentException)
            {
                continue; // an unusable path on this event; try the next one
            }

            if (string.IsNullOrEmpty(dir) || string.IsNullOrEmpty(stem) || !Directory.Exists(dir))
            {
                continue;
            }

            if (!FolderIsWritable(dir))
            {
                continue;
            }

            return Path.Combine(dir, stem + BeckySrtSuffix);
        }

        return Path.Combine(fallbackDir, "captions.srt");
    }

    private static bool FolderIsWritable(string dir)
    {
        string probe = Path.Combine(dir, ".becky-write-probe.tmp");
        try
        {
            File.WriteAllText(probe, "");
            File.Delete(probe);
            return true;
        }
        catch (Exception)
        {
            return false;
        }
    }


    // ---------------------------------------------------------- the placement
    // The original's loop. The only change: it reuses and clears a "Becky
    // Captions" track rather than adding another one every run.

    private static void AddSubtitlesToTimeline(Vegas vegas, List<Caption> captions, SubtitleStyleSettings style)
    {
        using (UndoBlock undo = new UndoBlock(vegas.Project, "Becky Captions"))
        {
            VideoTrack subtitleTrack = FindCaptionTrack(vegas);
            if (subtitleTrack == null)
            {
                // Index 0 = TOPMOST track, so the captions are never hidden behind
                // the picture. The constructor's index is what places the track;
                // Tracks.Insert(0, t) LOOKS like the obvious call and compiles, but
                // it throws NotSupportedException at runtime (VEGAS 18 leaves it
                // unimplemented). Verified on VEGAS Pro 18.0 build 527 before this
                // was written - do not "simplify" it back to Insert.
                subtitleTrack = new VideoTrack(vegas.Project, 0, CaptionTrackName);
                vegas.Project.Tracks.Add(subtitleTrack);
            }
            else
            {
                // Re-running replaces the previous captions instead of stacking a
                // second set on top of them.
                while (subtitleTrack.Events.Count > 0)
                {
                    subtitleTrack.Events.Remove(subtitleTrack.Events[0]);
                }
            }

            PlugInNode textGenerator = FindTextGenerator(vegas);
            if (textGenerator == null)
            {
                throw new ApplicationException(
                    "Could not find a text generator plugin (Titles & Text or Legacy Text)."
                );
            }

            foreach (Caption caption in captions)
            {
                Timecode start = Timecode.FromMilliseconds(caption.Start.TotalMilliseconds);
                Timecode length = Timecode.FromMilliseconds((caption.End - caption.Start).TotalMilliseconds);

                if (length.Nanos <= 0)
                {
                    length = Timecode.FromMilliseconds(100);
                }

                Media media = new Media(textGenerator);
                MediaStream stream = media.Streams.GetItemByMediaType(MediaType.Video, 0);
                if (stream == null)
                {
                    throw new ApplicationException("Generated text media has no video stream.");
                }

                string displayText = caption.Text;
                if (style.UseLineBreaks)
                {
                    displayText = InsertLineBreaks(caption.Text, style.WordsPerLine);
                }

                TrySetGeneratedText(media, displayText, style);

                VideoEvent ev = subtitleTrack.AddVideoEvent(start, length);
                ev.Takes.Add(new Take(stream));
            }
        }
    }

    private static VideoTrack FindCaptionTrack(Vegas vegas)
    {
        foreach (Track track in vegas.Project.Tracks)
        {
            VideoTrack vt = track as VideoTrack;
            if (vt != null && string.Equals(vt.Name, CaptionTrackName, StringComparison.OrdinalIgnoreCase))
            {
                return vt;
            }
        }
        return null;
    }

    private static string InsertLineBreaks(string text, int wordsPerLine)
    {
        if (string.IsNullOrWhiteSpace(text) || wordsPerLine < 1)
        {
            return text;
        }

        string[] words = text.Split(new[] { ' ' }, StringSplitOptions.RemoveEmptyEntries);
        if (words.Length <= wordsPerLine)
        {
            return text;
        }

        StringBuilder result = new StringBuilder();
        for (int i = 0; i < words.Length; i++)
        {
            if (i > 0)
            {
                if (i % wordsPerLine == 0)
                {
                    result.Append("\r\n");
                }
                else
                {
                    result.Append(" ");
                }
            }
            result.Append(words[i]);
        }

        return result.ToString();
    }

    private static PlugInNode FindTextGenerator(Vegas vegas)
    {
        string[] preferredNames =
        {
            "Titles & Text",
            "Legacy Text",
            "VEGAS Titles & Text",
            "Titler Pro"
        };

        foreach (string name in preferredNames)
        {
            PlugInNode found = vegas.Generators.GetChildByName(name);
            if (found != null)
            {
                return found;
            }
        }

        foreach (PlugInNode node in vegas.Generators)
        {
            if (node == null)
            {
                continue;
            }

            string n = node.Name ?? string.Empty;
            if (n.IndexOf("text", StringComparison.OrdinalIgnoreCase) >= 0 ||
                n.IndexOf("title", StringComparison.OrdinalIgnoreCase) >= 0)
            {
                return node;
            }
        }

        return null;
    }

    private static void TrySetGeneratedText(Media media, string text, SubtitleStyleSettings style)
    {
        Effect generator = media.Generator;
        if (generator == null)
        {
            return;
        }

        // PREFERRED PATH: Jordan's own "becky-captions" Titles & Text preset.
        //
        // The preset IS the style - font, size, bold, outline width, shadow, all
        // of it - set up by hand in VEGAS where it can actually be seen. Applying
        // it and changing only the words means this script stops guessing at a
        // look through OFX parameters, which is what made these captions behave
        // unlike every other title in VEGAS. Nothing below touches font, colour,
        // outline or shadow on this path; the preset owns them.
        if (generator.IsOFX && generator.OFXEffect != null && ApplyBeckyPreset(generator))
        {
            if (SetTextKeepingPreset(generator.OFXEffect, text))
            {
                return;
            }
            // preset applied but its text field could not be rewritten - fall
            // through rather than leaving every caption reading "becky"
        }

        if (generator.IsOFX && generator.OFXEffect != null)
        {
            SetOFXText(generator.OFXEffect, text, style);
            return;
        }

        SetLegacyText(generator);
    }

    // ApplyBeckyPreset selects the named preset if this install has it. Returns
    // false (quietly) when it does not, so the script still works on a machine
    // where the preset was never created.
    private static bool ApplyBeckyPreset(Effect generator)
    {
        try
        {
            foreach (EffectPreset preset in generator.Presets)
            {
                if (preset == null || preset.Name == null)
                {
                    continue;
                }
                if (string.Equals(preset.Name, BeckyPresetName, StringComparison.OrdinalIgnoreCase))
                {
                    generator.Preset = preset.Name;
                    return true;
                }
            }
        }
        catch (Exception)
        {
            // an install whose preset list cannot be read is not a failure worth
            // stopping the run for - the constants path below still works
        }
        return false;
    }

    // SetTextKeepingPreset swaps the WORDS inside the preset's own RTF and
    // changes nothing else, so \b, \fs, the font table and the alignment the
    // preset carries all survive.
    private static bool SetTextKeepingPreset(OFXEffect ofx, string text)
    {
        foreach (OFXParameter parameter in ofx.Parameters)
        {
            OFXStringParameter stringParameter = parameter as OFXStringParameter;
            if (stringParameter == null)
            {
                continue;
            }
            if (!string.Equals(stringParameter.Name, "Text", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            string swapped = ReplaceRtfWords(stringParameter.Value, text);
            if (swapped == null)
            {
                return false;
            }

            stringParameter.Value = swapped;
            stringParameter.ParameterChanged();
            return true;
        }
        return false;
    }

    private static void SetOFXText(OFXEffect ofx, string text, SubtitleStyleSettings style)
    {
        if (!string.IsNullOrWhiteSpace(style.FontName))
        {
            ApplyGeneratedFont(ofx, style.FontName);
        }

        ApplyOutlineSettings(ofx, style);
        ApplyTextColor(ofx, style);

        string rtfText = ConvertToRtf(text, style);

        foreach (OFXParameter parameter in ofx.Parameters)
        {
            OFXStringParameter stringParameter = parameter as OFXStringParameter;
            if (stringParameter == null)
            {
                continue;
            }

            string name = (stringParameter.Name ?? string.Empty).ToLowerInvariant();
            string label = (stringParameter.Label ?? string.Empty).ToLowerInvariant();

            if (name.Contains("text") || label.Contains("text") || name.Contains("caption") || label.Contains("caption"))
            {
                string currentValue = stringParameter.Value ?? string.Empty;
                if (currentValue.StartsWith("{\\rtf") || currentValue.Contains("\\rtf"))
                {
                    stringParameter.Value = rtfText;
                }
                else
                {
                    stringParameter.Value = text;
                }
                stringParameter.ParameterChanged();
                return;
            }
        }

        foreach (OFXParameter parameter in ofx.Parameters)
        {
            OFXStringParameter stringParameter = parameter as OFXStringParameter;
            if (stringParameter != null)
            {
                string currentValue = stringParameter.Value ?? string.Empty;
                if (currentValue.StartsWith("{\\rtf") || currentValue.Contains("\\rtf"))
                {
                    stringParameter.Value = rtfText;
                }
                else
                {
                    stringParameter.Value = text;
                }
                stringParameter.ParameterChanged();
                return;
            }
        }
    }

    private static void SetLegacyText(Effect generator)
    {
        try
        {
            if (generator.Presets != null && generator.Presets.Count > 0)
            {
                generator.Preset = generator.Presets[0].Name;
            }
        }
        catch
        {
            // Older generators vary; a failure here just leaves the default look.
        }
    }

    private static void ApplyOutlineSettings(OFXEffect ofx, SubtitleStyleSettings style)
    {
        foreach (OFXParameter parameter in ofx.Parameters)
        {
            string name = (parameter.Name ?? string.Empty).ToLowerInvariant();
            string label = (parameter.Label ?? string.Empty).ToLowerInvariant();

            if (name.Contains("outline") || name.Contains("stroke") || label.Contains("outline") || label.Contains("stroke"))
            {
                OFXDoubleParameter doubleParam = parameter as OFXDoubleParameter;
                if (doubleParam != null && (name.Contains("width") || name.Contains("size") || label.Contains("width") || label.Contains("size")))
                {
                    doubleParam.Value = style.OutlineWidth;
                    doubleParam.ParameterChanged();
                }

                OFXRGBAParameter rgbaParam = parameter as OFXRGBAParameter;
                if (rgbaParam != null && (name.Contains("color") || label.Contains("color")))
                {
                    rgbaParam.Value = new OFXColor(
                        style.OutlineColor.R / 255.0,
                        style.OutlineColor.G / 255.0,
                        style.OutlineColor.B / 255.0,
                        style.OutlineColor.A / 255.0
                    );
                    rgbaParam.ParameterChanged();
                }
            }
        }
    }

    // ApplyTextColor paints the caption through the generator's own TextColor
    // parameter rather than through the RTF. That is what keeps the Titles & Text
    // edit box readable (black on white) while the rendered caption is white -
    // see the comment in ConvertToRtf. "TextColor" is the exact parameter name on
    // VEGAS Pro 18's "VEGAS Titles & Text"; the name/label match keeps it working
    // if another version spells it differently.
    private static void ApplyTextColor(OFXEffect ofx, SubtitleStyleSettings style)
    {
        foreach (OFXParameter parameter in ofx.Parameters)
        {
            OFXRGBAParameter rgba = parameter as OFXRGBAParameter;
            if (rgba == null)
            {
                continue;
            }

            string name = (parameter.Name ?? string.Empty).ToLowerInvariant();
            string label = (parameter.Label ?? string.Empty).ToLowerInvariant();

            // the TEXT colour only - not outline, shadow or background
            bool isTextColor = (name == "textcolor") ||
                               ((name.Contains("text") || label.Contains("text")) &&
                                (name.Contains("color") || label.Contains("color")));
            if (!isTextColor)
            {
                continue;
            }

            rgba.Value = new OFXColor(
                style.TextColor.R / 255.0,
                style.TextColor.G / 255.0,
                style.TextColor.B / 255.0,
                1.0);
            rgba.ParameterChanged();
            return;
        }
    }

    // ReplaceRtfWords replaces the readable words in an RTF string and leaves
    // every formatting instruction untouched.
    //
    // In RTF everything is either a group ({...}), a control word (\b, \fs18,
    // \par) or literal text. The preset's text sits as literal text in the
    // outermost group - the font table is a nested group, so it is never
    // mistaken for words. Walking the string once and rewriting just that run is
    // what lets the preset keep its font, size, bold and alignment.
    //
    // Returns null if no literal text was found, so the caller can fall back.
    private static string ReplaceRtfWords(string rtf, string text)
    {
        if (string.IsNullOrEmpty(rtf))
        {
            return null;
        }

        int first = -1;
        int last = -1;
        int depth = 0;
        int i = 0;

        while (i < rtf.Length)
        {
            char c = rtf[i];

            if (c == '{') { depth++; i++; continue; }
            if (c == '}') { depth--; i++; continue; }

            if (c == '\\')
            {
                int j = i + 1;
                if (j < rtf.Length && IsAsciiLetter(rtf[j]))
                {
                    while (j < rtf.Length && IsAsciiLetter(rtf[j])) { j++; }      // \fs
                    if (j < rtf.Length && rtf[j] == '-') { j++; }                  // \fi-360
                    while (j < rtf.Length && rtf[j] >= '0' && rtf[j] <= '9') { j++; } // \fs18
                    if (j < rtf.Length && rtf[j] == ' ') { j++; }                  // the delimiting space belongs to the word
                    i = j;
                    continue;
                }

                // \\ \{ \} are escaped literal characters, i.e. words
                if (depth == 1)
                {
                    if (first < 0) { first = i; }
                    last = i + 2;
                }
                i += 2;
                continue;
            }

            if (c == '\r' || c == '\n') { i++; continue; }  // line breaks are not content in RTF

            if (depth == 1)
            {
                if (first < 0) { first = i; }
                last = i + 1;
            }
            i++;
        }

        if (first < 0 || last <= first)
        {
            return null;
        }

        return rtf.Substring(0, first) + EscapeRtf(text) + rtf.Substring(last);
    }

    private static bool IsAsciiLetter(char c)
    {
        return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z');
    }

    // EscapeRtf makes caption text safe to drop into an RTF string. Anything
    // above ASCII is written as \'hh, because the generator's RTF is \ansi and a
    // raw high byte would render as mojibake.
    private static string EscapeRtf(string text)
    {
        if (string.IsNullOrEmpty(text))
        {
            return " ";
        }

        StringBuilder sb = new StringBuilder(text.Length + 16);
        foreach (char c in text)
        {
            switch (c)
            {
                case '\\': sb.Append("\\\\"); break;
                case '{': sb.Append("\\{"); break;
                case '}': sb.Append("\\}"); break;
                case '\r': break;
                case '\n': sb.Append("\\par "); break;
                default:
                    if (c > 127)
                    {
                        sb.Append("\\'").Append(((int)c & 0xFF).ToString("x2"));
                    }
                    else
                    {
                        sb.Append(c);
                    }
                    break;
            }
        }
        return sb.ToString();
    }

    private static string ConvertToRtf(string plainText, SubtitleStyleSettings style)
    {
        if (string.IsNullOrEmpty(plainText))
        {
            plainText = " ";
        }

        string escaped = EscapeRtf(plainText);

        StringBuilder rtf = new StringBuilder();
        rtf.Append("{\\rtf1\\ansi\\deff0");

        string font = string.IsNullOrWhiteSpace(style.FontName) ? "Arial" : style.FontName.Trim();
        rtf.Append("{\\fonttbl{\\f0\\fnil\\fcharset0 ");
        rtf.Append(font);
        rtf.Append(";}}");

        // NO colour table, and NO \cf colour selector. This is deliberate.
        //
        // Titles & Text shows the RTF in its edit box on a white background, and
        // it renders the picture using its own TextColor parameter. The original
        // script baked the caption colour into the RTF as "\cf1" -> white, so the
        // edit box drew WHITE TEXT ON WHITE and you could not see what you were
        // editing. Leaving the colour out lets the edit box use its default black
        // (normal VEGAS behaviour) while the rendered caption stays white, because
        // the colour that actually paints the frame is the TextColor OFX parameter
        // set in ApplyTextColor.

        int fontSizeHalfPts = style.FontSize * 2;

        rtf.AppendFormat("\\pard\\qc\\f0\\fs{0} ", fontSizeHalfPts);
        rtf.Append(escaped);
        rtf.Append("}");

        return rtf.ToString();
    }

    private static void ApplyGeneratedFont(OFXEffect ofx, string fontName)
    {
        if (string.IsNullOrWhiteSpace(fontName))
        {
            return;
        }

        string desired = fontName.Trim();
        string desiredLower = desired.ToLowerInvariant();

        foreach (OFXParameter parameter in ofx.Parameters)
        {
            OFXChoiceParameter choiceParameter = parameter as OFXChoiceParameter;
            if (choiceParameter == null)
            {
                continue;
            }

            string name = (choiceParameter.Name ?? string.Empty).ToLowerInvariant();
            string label = (choiceParameter.Label ?? string.Empty).ToLowerInvariant();
            if (!(name.Contains("font") || label.Contains("font") || name.Contains("typeface") || label.Contains("typeface")))
            {
                continue;
            }

            OFXChoice[] choices = choiceParameter.Choices;
            if (choices == null)
            {
                continue;
            }

            for (int i = 0; i < choices.Length; i++)
            {
                string choiceText = choices[i] == null ? string.Empty : choices[i].ToString();
                if (choiceText.ToLowerInvariant().Contains(desiredLower))
                {
                    choiceParameter.Value = choices[i];
                    choiceParameter.ParameterChanged();
                    return;
                }
            }
        }

        foreach (OFXParameter parameter in ofx.Parameters)
        {
            OFXStringParameter stringParameter = parameter as OFXStringParameter;
            if (stringParameter == null)
            {
                continue;
            }

            string name = (stringParameter.Name ?? string.Empty).ToLowerInvariant();
            string label = (stringParameter.Label ?? string.Empty).ToLowerInvariant();
            if (name.Contains("font") || label.Contains("font") || name.Contains("typeface") || label.Contains("typeface"))
            {
                stringParameter.Value = desired;
                stringParameter.ParameterChanged();
                return;
            }
        }
    }
}
