using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using ScriptPortal.Vegas;

namespace BeckyVegas
{
    // ProjectOps is every read and edit of the open VEGAS project. EVERYTHING in here
    // must run on VEGAS's main thread (UiThread.Run / a UI event handler). Every edit
    // is one UndoBlock, so one Ctrl+Z puts it back.
    internal static class ProjectOps
    {
        internal const string PullsTrackName = "Becky Pulls";

        internal static double Sec(Timecode t)
        {
            return t == null ? 0.0 : t.ToMilliseconds() / 1000.0;
        }

        internal static Timecode Tc(double seconds)
        {
            return Timecode.FromSeconds(Math.Max(0.0, seconds));
        }

        // Events lists every media event on the timeline in the VegasTimeline shape
        // becky reads (becky-go/internal/edl/vegastimeline.go): source file, source
        // in/out, ruler position, track and playback rate - the same numbers
        // BeckyCaptions.cs sends, so both tools agree on where a word is. Generated
        // media (titles, colour) has no file and is skipped.
        internal static List<Dictionary<string, object>> Events(Vegas vegas, bool selectedOnly)
        {
            List<Dictionary<string, object>> list = new List<Dictionary<string, object>>();
            Project project = vegas.Project;
            if (project == null)
            {
                return list;
            }
            foreach (Track track in project.Tracks)
            {
                foreach (TrackEvent ev in track.Events)
                {
                    if (selectedOnly && !ev.Selected)
                    {
                        continue;
                    }
                    Take take = ev.ActiveTake;
                    if (take == null || take.Media == null || take.Media.IsGenerated())
                    {
                        continue;
                    }
                    string path = take.Media.FilePath;
                    double length = Sec(ev.Length);
                    if (string.IsNullOrEmpty(path) || length <= 0)
                    {
                        continue;
                    }
                    double rate = ev.PlaybackRate > 0 ? ev.PlaybackRate : 1.0;
                    double inSec = Sec(take.Offset);
                    Dictionary<string, object> e = new Dictionary<string, object>();
                    e["source"] = path;
                    e["in"] = inSec;
                    e["out"] = inSec + length * rate;
                    e["timeline"] = Sec(ev.Start);
                    e["timeline_end"] = Sec(ev.Start) + length;
                    e["track"] = track.Index;
                    e["track_name"] = track.Name ?? "";
                    e["kind"] = ev.IsVideo() ? "video" : "audio";
                    e["rate"] = rate;
                    e["label"] = Path.GetFileNameWithoutExtension(path);
                    e["selected"] = ev.Selected;
                    e["grouped"] = ev.IsGrouped;
                    list.Add(e);
                }
            }
            return list;
        }

        // TimelineDoc is the JSON document becky-review-index --timeline reads.
        internal static Dictionary<string, object> TimelineDoc(Vegas vegas, bool selectedOnly)
        {
            Dictionary<string, object> doc = new Dictionary<string, object>();
            doc["version"] = "1";
            doc["project"] = vegas.Project == null ? "" : (vegas.Project.FilePath ?? "");
            doc["fps"] = vegas.Project == null ? 0.0 : vegas.Project.Video.FrameRate;
            doc["events"] = Events(vegas, selectedOnly);
            return doc;
        }

        internal static Dictionary<string, object> Status(Vegas vegas, bool rendering)
        {
            Dictionary<string, object> s = new Dictionary<string, object>();
            Project p = vegas.Project;
            s["pid"] = Process.GetCurrentProcess().Id;
            s["vegas_version"] = vegas.Version;
            s["extension_version"] = BeckyVegasModule.ExtensionVersion;
            s["rendering"] = rendering;
            s["playing"] = vegas.Transport.IsPlaying;
            s["cursor"] = Sec(vegas.Transport.CursorPosition);
            s["cursor_text"] = vegas.Transport.CursorPosition.ToPositionString();
            s["selection_start"] = Sec(vegas.Transport.SelectionStart);
            s["selection_length"] = Sec(vegas.Transport.SelectionLength);
            if (p != null)
            {
                s["project_path"] = p.FilePath ?? "";
                s["project_untitled"] = p.IsUntitled;
                s["project_modified"] = p.IsModified;
                s["project_length"] = Sec(p.Length);
                s["fps"] = p.Video.FrameRate;
                s["width"] = p.Video.Width;
                s["height"] = p.Video.Height;
                int tracks = 0, events = 0, selected = 0;
                foreach (Track t in p.Tracks)
                {
                    tracks++;
                    foreach (TrackEvent ev in t.Events)
                    {
                        events++;
                        if (ev.Selected) { selected++; }
                    }
                }
                s["tracks"] = tracks;
                s["events"] = events;
                s["selected_events"] = selected;
                s["markers"] = p.Markers.Count;
                s["regions"] = p.Regions.Count;
            }
            return s;
        }

        internal static List<Dictionary<string, object>> Markers(Vegas vegas)
        {
            List<Dictionary<string, object>> list = new List<Dictionary<string, object>>();
            if (vegas.Project == null)
            {
                return list;
            }
            foreach (Marker m in vegas.Project.Markers)
            {
                Dictionary<string, object> d = new Dictionary<string, object>();
                d["kind"] = "marker";
                d["position"] = Sec(m.Position);
                d["label"] = m.Label ?? "";
                list.Add(d);
            }
            foreach (Region r in vegas.Project.Regions)
            {
                Dictionary<string, object> d = new Dictionary<string, object>();
                d["kind"] = "region";
                d["position"] = Sec(r.Position);
                d["length"] = Sec(r.Length);
                d["label"] = r.Label ?? "";
                list.Add(d);
            }
            return list;
        }

        // JumpTo puts the cursor on a line and selects exactly the time it is spoken,
        // so Play plays just that line, and scrolls the timeline so it is in view.
        // The cursor goes FIRST: setting CursorPosition after the selection clears
        // the selection (measured in VEGAS 18 on 2026-09-13 - the selection length
        // read back 0). Returns the selection length VEGAS actually holds, so the
        // caller only claims what really happened.
        internal static double JumpTo(Vegas vegas, double start, double end)
        {
            TransportControl t = vegas.Transport;
            t.CursorPosition = Tc(start);
            double length = Math.Max(0.0, end - start);
            if (length > 0)
            {
                t.SelectionStart = Tc(start);
                t.SelectionLength = Tc(length);
            }
            t.ViewCursor(true);
            return Sec(t.SelectionLength);
        }

        internal static void AddMarker(Vegas vegas, double position, string label)
        {
            RequireProject(vegas);
            using (new UndoBlock("Becky: add marker"))
            {
                vegas.Project.Markers.Add(new Marker(Tc(position), label ?? ""));
            }
        }

        internal static void AddRegion(Vegas vegas, double start, double end, string label)
        {
            RequireProject(vegas);
            if (end <= start)
            {
                throw new ArgumentException("a region needs an end after its start");
            }
            using (new UndoBlock("Becky: add region"))
            {
                vegas.Project.Regions.Add(new Region(Tc(start), Tc(end - start), label ?? ""));
            }
        }

        // InsertClip puts [inSec,outSec) of a media file on the "Becky Pulls" tracks at
        // atSec - never on one of Jordan's own tracks, so pulling a line can never
        // overwrite or shift his edit. Picture and sound are grouped the way VEGAS's
        // own "Group Video and Audio Events.cs" does it (the idiom BeckyCut proved).
        // outSec <= inSec means "to the end of the file". Returns the new ruler end so
        // the caller can park the cursor there and pull the next line after it.
        internal static double InsertClip(Vegas vegas, string path, double inSec, double outSec, double atSec)
        {
            RequireProject(vegas);
            if (string.IsNullOrEmpty(path) || !File.Exists(path))
            {
                throw new FileNotFoundException("That file is not there any more: " + path);
            }
            Project project = vegas.Project;
            Media media = project.MediaPool.Find(path) ?? new Media(path);
            VideoStream videoStream = media.HasVideo() ? media.GetVideoStreamByIndex(0) : null;
            AudioStream audioStream = media.HasAudio() ? media.GetAudioStreamByIndex(0) : null;
            if (videoStream == null && audioStream == null)
            {
                throw new InvalidOperationException("VEGAS could not read any picture or sound in " + Path.GetFileName(path));
            }
            double mediaLength = Sec(media.Length);
            inSec = Math.Max(0.0, inSec);
            if (mediaLength > 0 && inSec >= mediaLength)
            {
                throw new ArgumentException("That line is past the end of " + Path.GetFileName(path) +
                    " - its transcript is longer than the video, so it probably belongs to a different cut of it.");
            }
            if (outSec <= inSec || (mediaLength > 0 && outSec > mediaLength))
            {
                outSec = mediaLength > 0 ? mediaLength : inSec + 5.0;
            }
            double length = outSec - inSec;
            if (length <= 0)
            {
                throw new ArgumentException("that span is empty");
            }

            using (new UndoBlock("Becky: insert " + Path.GetFileName(path)))
            {
                VideoEvent videoEvent = null;
                AudioEvent audioEvent = null;
                if (videoStream != null)
                {
                    VideoTrack vt = FindOrAddVideoTrack(project);
                    videoEvent = vt.AddVideoEvent(Tc(atSec), Tc(length));
                    videoEvent.AddTake(videoStream).Offset = Tc(inSec);
                }
                if (audioStream != null)
                {
                    AudioTrack at = FindOrAddAudioTrack(project);
                    audioEvent = at.AddAudioEvent(Tc(atSec), Tc(length));
                    audioEvent.AddTake(audioStream).Offset = Tc(inSec);
                }
                if (videoEvent != null && audioEvent != null)
                {
                    TrackEventGroup group = new TrackEventGroup();
                    project.TrackEventGroups.Add(group);
                    group.Add(videoEvent);
                    group.Add(audioEvent);
                }
            }
            return atSec + length;
        }

        static VideoTrack FindOrAddVideoTrack(Project project)
        {
            foreach (Track t in project.Tracks)
            {
                if (t.IsVideo() && t.Name == PullsTrackName)
                {
                    return (VideoTrack)t;
                }
            }
            VideoTrack track = project.AddVideoTrack();
            track.Name = PullsTrackName;
            return track;
        }

        static AudioTrack FindOrAddAudioTrack(Project project)
        {
            foreach (Track t in project.Tracks)
            {
                if (t.IsAudio() && t.Name == PullsTrackName)
                {
                    return (AudioTrack)t;
                }
            }
            AudioTrack track = project.AddAudioTrack();
            track.Name = PullsTrackName;
            return track;
        }

        static void RequireProject(Vegas vegas)
        {
            if (vegas.Project == null)
            {
                throw new InvalidOperationException("no project is open in VEGAS");
            }
        }
    }
}
