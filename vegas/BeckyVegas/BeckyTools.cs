using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Text;
using System.Web.Script.Serialization;

namespace BeckyVegas
{
    // Hit is one search result row: a spoken line, where it is in its file, and -
    // for a timeline search - where that exact moment sits on the VEGAS ruler.
    internal sealed class Hit
    {
        public string Source;
        public string Name;
        public string Text;
        public double SourceStart;
        public double SourceEnd;
        public bool FromTimeline;   // came from a timeline search
        public bool OnTimeline;     // ...and is visible in the edit (false = cut out)
        public double Timeline;
        public double TimelineEnd;
        public int Track;
    }

    internal sealed class MediaFile
    {
        public string Path;
        public string Name;
        public bool HasTranscript;
    }

    internal sealed class SearchResult
    {
        public readonly List<Hit> Hits = new List<Hit>();
        public readonly List<MediaFile> Media = new List<MediaFile>();

        public List<MediaFile> Untranscribed()
        {
            return Media.FindAll(m => !m.HasTranscript);
        }
    }

    // BeckyTools runs becky's own command-line tools. The extension decides nothing a
    // becky tool already decides: search is becky-review-index (the engine behind
    // Becky Review), transcription is becky-transcribe. Nothing here touches VEGAS,
    // so it is safe on any thread.
    internal static class BeckyTools
    {
        // ponytail: this PC's becky bin folder; BECKY_BIN overrides it, PATH is the last resort.
        const string DefaultBin = @"X:\AI-2\becky-tools\becky-go\bin";

        // Same suffix becky-clip writes (footage.LocalTranscriptMarker), so a
        // transcript made from VEGAS is found by Becky Review too, and an official
        // "<clip>.srt" is never overwritten.
        const string LocalTranscriptSuffix = "_parakeet_transcription.srt";

        internal static string Find(string exe)
        {
            string env = Environment.GetEnvironmentVariable("BECKY_BIN");
            if (!string.IsNullOrEmpty(env) && File.Exists(Path.Combine(env, exe)))
            {
                return Path.Combine(env, exe);
            }
            if (File.Exists(Path.Combine(DefaultBin, exe)))
            {
                return Path.Combine(DefaultBin, exe);
            }
            string pathVar = Environment.GetEnvironmentVariable("PATH") ?? "";
            foreach (string dir in pathVar.Split(';'))
            {
                try
                {
                    string candidate = Path.Combine(dir.Trim(), exe);
                    if (dir.Trim().Length > 0 && File.Exists(candidate))
                    {
                        return candidate;
                    }
                }
                catch (ArgumentException)
                {
                    // a malformed PATH entry is not our problem
                }
            }
            throw new FileNotFoundException(exe + " was not found. Build becky (build-all-tools.bat) or set BECKY_BIN to its bin folder.");
        }

        internal static JavaScriptSerializer Json()
        {
            return new JavaScriptSerializer { MaxJsonLength = int.MaxValue, RecursionLimit = 64 };
        }

        internal sealed class ProcResult
        {
            public int ExitCode;
            public string Stdout = "";
            public string Stderr = "";
            public bool Stopped;
        }

        // Run starts a console tool with no window, drains both pipes on callbacks (two
        // blocking reads in a row can deadlock) and polls so a Cancel click or the
        // timeout can kill it. timeoutMs <= 0 means no timeout.
        internal static ProcResult Run(string exe, string args, int timeoutMs, Func<bool> shouldStop)
        {
            ProcessStartInfo psi = new ProcessStartInfo(exe, args);
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            psi.RedirectStandardOutput = true;
            psi.RedirectStandardError = true;
            psi.StandardOutputEncoding = Encoding.UTF8;
            psi.StandardErrorEncoding = Encoding.UTF8;

            StringBuilder stdout = new StringBuilder();
            StringBuilder stderr = new StringBuilder();
            using (Process p = new Process())
            {
                p.StartInfo = psi;
                p.OutputDataReceived += (s, e) => { if (e.Data != null) { lock (stdout) { stdout.AppendLine(e.Data); } } };
                p.ErrorDataReceived += (s, e) => { if (e.Data != null) { lock (stderr) { stderr.AppendLine(e.Data); } } };
                p.Start();
                p.BeginOutputReadLine();
                p.BeginErrorReadLine();
                Stopwatch clock = Stopwatch.StartNew();
                ProcResult r = new ProcResult();
                while (!p.WaitForExit(200))
                {
                    bool stop = shouldStop != null && shouldStop();
                    if (stop || (timeoutMs > 0 && clock.ElapsedMilliseconds > timeoutMs))
                    {
                        try { p.Kill(); } catch (InvalidOperationException) { }
                        p.WaitForExit(5000);
                        r.Stopped = true;
                        break;
                    }
                }
                if (!r.Stopped)
                {
                    p.WaitForExit(); // lets the async readers flush the last lines
                    r.ExitCode = p.ExitCode;
                }
                else
                {
                    r.ExitCode = -1;
                }
                lock (stdout) { r.Stdout = stdout.ToString(); }
                lock (stderr) { r.Stderr = stderr.ToString(); }
                return r;
            }
        }

        // Quote applies the Windows command-line quoting rules (backslashes before a
        // quote double up), so a folder like "E:\" survives as one argument.
        internal static string Quote(string arg)
        {
            StringBuilder sb = new StringBuilder("\"");
            int slashes = 0;
            foreach (char c in arg ?? "")
            {
                if (c == '\\')
                {
                    slashes++;
                    continue;
                }
                if (c == '"')
                {
                    sb.Append('\\', slashes * 2 + 1);
                }
                else
                {
                    sb.Append('\\', slashes);
                }
                slashes = 0;
                sb.Append(c);
            }
            sb.Append('\\', slashes * 2);
            sb.Append('"');
            return sb.ToString();
        }

        internal static SearchResult SearchTimeline(Dictionary<string, object> timelineDoc, string query, Func<bool> shouldStop)
        {
            string tmp = Path.Combine(Path.GetTempPath(), "BeckyVegas");
            Directory.CreateDirectory(tmp);
            string file = Path.Combine(tmp, "timeline-" + Process.GetCurrentProcess().Id + "-" + Guid.NewGuid().ToString("N") + ".json");
            File.WriteAllText(file, Json().Serialize(timelineDoc), new UTF8Encoding(false));
            try
            {
                string args = "--timeline " + Quote(file) + " --search " + Quote(query ?? "");
                return ParseSearch(RunIndex(args, shouldStop), true);
            }
            finally
            {
                try { File.Delete(file); } catch (IOException) { } catch (UnauthorizedAccessException) { }
            }
        }

        internal static SearchResult SearchFolder(string folder, string query, Func<bool> shouldStop)
        {
            if (string.IsNullOrEmpty(folder) || !Directory.Exists(folder))
            {
                throw new DirectoryNotFoundException("That folder does not exist: " + folder);
            }
            string args = "--folder " + Quote(folder) + " --search " + Quote(query ?? "");
            return ParseSearch(RunIndex(args, shouldStop), false);
        }

        static string RunIndex(string args, Func<bool> shouldStop)
        {
            ProcResult r = Run(Find("becky-review-index.exe"), args, 10 * 60 * 1000, shouldStop);
            if (r.Stopped)
            {
                throw new OperationCanceledException("search stopped");
            }
            if (r.ExitCode != 0)
            {
                throw new InvalidOperationException("becky-review-index failed: " + LastLines(r.Stderr, 3));
            }
            return r.Stdout;
        }

        static SearchResult ParseSearch(string json, bool timeline)
        {
            SearchResult result = new SearchResult();
            Dictionary<string, object> root = Json().DeserializeObject(json) as Dictionary<string, object>;
            if (root == null)
            {
                throw new InvalidOperationException("becky-review-index returned something that is not JSON");
            }
            foreach (Dictionary<string, object> v in Items(root, "videos"))
            {
                result.Media.Add(new MediaFile
                {
                    Path = Str(v, "path"),
                    Name = Str(v, "name"),
                    HasTranscript = Bool(v, "has_transcript")
                });
            }
            foreach (Dictionary<string, object> c in Items(root, timeline ? "timeline_candidates" : "candidates"))
            {
                if (Str(c, "source").Length == 0)
                {
                    continue; // a transcript with no video beside it: nothing VEGAS can jump to or insert
                }
                Hit baseHit = new Hit
                {
                    Source = Str(c, "source"),
                    Name = Str(c, "name"),
                    Text = Str(c, "text"),
                    SourceStart = Num(c, "timestamp"),
                    SourceEnd = Num(c, "end"),
                    FromTimeline = timeline
                };
                List<Dictionary<string, object>> places = Items(c, "on_timeline");
                if (!timeline || places.Count == 0)
                {
                    result.Hits.Add(baseHit);
                    continue;
                }
                // One row per place: a line the edit uses twice shows up twice.
                foreach (Dictionary<string, object> place in places)
                {
                    result.Hits.Add(new Hit
                    {
                        Source = baseHit.Source,
                        Name = baseHit.Name,
                        Text = baseHit.Text,
                        SourceStart = baseHit.SourceStart,
                        SourceEnd = baseHit.SourceEnd,
                        FromTimeline = true,
                        OnTimeline = true,
                        Timeline = Num(place, "timeline"),
                        TimelineEnd = Num(place, "timeline_end"),
                        Track = (int)Num(place, "track")
                    });
                }
            }
            return result;
        }

        internal static string LocalTranscriptPath(string media)
        {
            return Path.Combine(Path.GetDirectoryName(media) ?? "", Path.GetFileNameWithoutExtension(media) + LocalTranscriptSuffix);
        }

        // Transcribe runs becky-transcribe on one file and writes the becky-owned
        // "<clip>_parakeet_transcription.srt" beside it. Returns that path.
        internal static string Transcribe(string media, Func<bool> shouldStop)
        {
            string output = LocalTranscriptPath(media);
            string args = Quote(media) + " --format srt --output " + Quote(output);
            ProcResult r = Run(Find("becky-transcribe.exe"), args, 0, shouldStop);
            if (r.Stopped)
            {
                throw new OperationCanceledException("transcription stopped");
            }
            if (r.ExitCode != 0 || !File.Exists(output))
            {
                throw new InvalidOperationException("becky-transcribe could not transcribe " + Path.GetFileName(media) + ": " + LastLines(r.Stderr, 3));
            }
            return output;
        }

        internal static string LastLines(string text, int count)
        {
            string[] lines = (text ?? "").Replace("\r", "").Split(new[] { '\n' }, StringSplitOptions.RemoveEmptyEntries);
            if (lines.Length == 0)
            {
                return "(no details)";
            }
            int start = Math.Max(0, lines.Length - count);
            return string.Join(" | ", lines, start, lines.Length - start);
        }

        internal static List<Dictionary<string, object>> Items(Dictionary<string, object> obj, string key)
        {
            List<Dictionary<string, object>> list = new List<Dictionary<string, object>>();
            object value;
            if (obj != null && obj.TryGetValue(key, out value) && value is object[])
            {
                foreach (object item in (object[])value)
                {
                    Dictionary<string, object> d = item as Dictionary<string, object>;
                    if (d != null)
                    {
                        list.Add(d);
                    }
                }
            }
            return list;
        }

        internal static string Str(Dictionary<string, object> obj, string key)
        {
            object value;
            return obj != null && obj.TryGetValue(key, out value) && value != null ? Convert.ToString(value, CultureInfo.InvariantCulture) : "";
        }

        internal static double Num(Dictionary<string, object> obj, string key)
        {
            object value;
            if (obj == null || !obj.TryGetValue(key, out value) || value == null)
            {
                return 0.0;
            }
            if (value is string)
            {
                double parsed;
                return double.TryParse((string)value, NumberStyles.Float, CultureInfo.InvariantCulture, out parsed) ? parsed : 0.0;
            }
            return Convert.ToDouble(value, CultureInfo.InvariantCulture);
        }

        internal static bool Has(Dictionary<string, object> obj, string key)
        {
            return obj != null && obj.ContainsKey(key) && obj[key] != null;
        }

        internal static bool Bool(Dictionary<string, object> obj, string key)
        {
            object value;
            if (obj == null || !obj.TryGetValue(key, out value) || value == null)
            {
                return false;
            }
            if (value is bool)
            {
                return (bool)value;
            }
            string s = Convert.ToString(value, CultureInfo.InvariantCulture).Trim().ToLowerInvariant();
            return s == "true" || s == "1" || s == "yes";
        }
    }
}
