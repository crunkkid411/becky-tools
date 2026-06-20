package audiotrack

// edit.go holds the IMMUTABLE timeline edits. Every function returns a NEW Project /
// Track / Region and leaves its receiver untouched (CLAUDE.md coding-style: create
// new objects, never mutate in place). This lets the GUI keep an undo stack of cheap
// snapshots and keeps concurrent reads safe. Region indices are positions in a
// track's Regions slice; track indices are positions in Project.Tracks. Out-of-range
// indices are no-ops that return an unchanged copy (degrade-never-crash) rather than
// panicking, so a stale GUI index can never crash the engine.

// cloneRegions returns a shallow copy of a region slice (the Clip pointers are
// shared on purpose — clips are immutable backing buffers; only the placement edits).
func cloneRegions(in []Region) []Region {
	if in == nil {
		return nil
	}
	out := make([]Region, len(in))
	copy(out, in)
	return out
}

// cloneTracks returns a copy of the track slice with each track's Regions slice also
// copied, so an edit to one returned track never aliases the original.
func cloneTracks(in []Track) []Track {
	if in == nil {
		return nil
	}
	out := make([]Track, len(in))
	for i, t := range in {
		t.Regions = cloneRegions(t.Regions)
		out[i] = t
	}
	return out
}

// withTracks returns a copy of the project with its Tracks replaced.
func (p Project) withTracks(tracks []Track) Project {
	out := p
	out.Tracks = tracks
	return out
}

// withRegions returns a copy of the track with its Regions replaced.
func (t Track) withRegions(regions []Region) Track {
	out := t
	out.Regions = regions
	return out
}

// validTrack reports whether ti indexes an existing track.
func (p Project) validTrack(ti int) bool { return ti >= 0 && ti < len(p.Tracks) }

// validRegion reports whether ri indexes an existing region on track ti.
func (p Project) validRegion(ti, ri int) bool {
	return p.validTrack(ti) && ri >= 0 && ri < len(p.Tracks[ti].Regions)
}

// ---------------------------------------------------------------------------
// Track-level edits
// ---------------------------------------------------------------------------

// AddTrack returns a copy of the project with t appended.
func (p Project) AddTrack(t Track) Project {
	tracks := cloneTracks(p.Tracks)
	tracks = append(tracks, t)
	return p.withTracks(tracks)
}

// RemoveTrack returns a copy with track ti removed (unchanged copy if ti is invalid).
func (p Project) RemoveTrack(ti int) Project {
	if !p.validTrack(ti) {
		return p.withTracks(cloneTracks(p.Tracks))
	}
	tracks := cloneTracks(p.Tracks)
	tracks = append(tracks[:ti], tracks[ti+1:]...)
	return p.withTracks(tracks)
}

// SetTrackVolume returns a copy with track ti's Volume set (clamped >= 0).
func (p Project) SetTrackVolume(ti int, volume float64) Project {
	return p.mapTrack(ti, func(t Track) Track {
		if volume < 0 {
			volume = 0
		}
		t.Volume = volume
		return t
	})
}

// SetTrackPan returns a copy with track ti's Pan set (clamped to [-1, 1]).
func (p Project) SetTrackPan(ti int, pan float64) Project {
	return p.mapTrack(ti, func(t Track) Track {
		t.Pan = clampF(pan, -1, 1)
		return t
	})
}

// SetTrackMute returns a copy with track ti's Mute set.
func (p Project) SetTrackMute(ti int, mute bool) Project {
	return p.mapTrack(ti, func(t Track) Track { t.Mute = mute; return t })
}

// SetTrackSolo returns a copy with track ti's Solo set.
func (p Project) SetTrackSolo(ti int, solo bool) Project {
	return p.mapTrack(ti, func(t Track) Track { t.Solo = solo; return t })
}

// mapTrack applies fn to track ti in a fresh copy (no-op copy if ti is invalid).
func (p Project) mapTrack(ti int, fn func(Track) Track) Project {
	tracks := cloneTracks(p.Tracks)
	if ti >= 0 && ti < len(tracks) {
		tracks[ti] = fn(tracks[ti])
	}
	return p.withTracks(tracks)
}

// ---------------------------------------------------------------------------
// Region-level edits (the core of the task: add / move / trim / split / gain / fade)
// ---------------------------------------------------------------------------

// AddRegion returns a copy with r appended to track ti (normalized). Invalid ti -> a
// no-op copy.
func (p Project) AddRegion(ti int, r Region) Project {
	return p.mapTrack(ti, func(t Track) Track {
		regions := cloneRegions(t.Regions)
		regions = append(regions, r.Normalize())
		return t.withRegions(regions)
	})
}

// RemoveRegion returns a copy with region ri removed from track ti.
func (p Project) RemoveRegion(ti, ri int) Project {
	if !p.validRegion(ti, ri) {
		return p.withTracks(cloneTracks(p.Tracks))
	}
	return p.mapTrack(ti, func(t Track) Track {
		regions := cloneRegions(t.Regions)
		regions = append(regions[:ri], regions[ri+1:]...)
		return t.withRegions(regions)
	})
}

// mapRegion applies fn to region ri on track ti in a fresh copy, re-normalizing the
// result. No-op copy if the indices are invalid.
func (p Project) mapRegion(ti, ri int, fn func(Region) Region) Project {
	if !p.validRegion(ti, ri) {
		return p.withTracks(cloneTracks(p.Tracks))
	}
	return p.mapTrack(ti, func(t Track) Track {
		regions := cloneRegions(t.Regions)
		regions[ri] = fn(regions[ri]).Normalize()
		return t.withRegions(regions)
	})
}

// MoveRegion returns a copy with region ri on track ti placed at a new timeline
// position (clamped >= 0 by Normalize). This is a horizontal drag on the timeline.
func (p Project) MoveRegion(ti, ri, timelinePos int) Project {
	return p.mapRegion(ti, ri, func(r Region) Region {
		r.TimelinePos = timelinePos
		return r
	})
}

// MoveRegionToTrack returns a copy with region ri moved from track srcTi to the end
// of track dstTi at timelinePos. A no-op copy if either index is invalid. This is a
// vertical drag (re-assign a region to a different track).
func (p Project) MoveRegionToTrack(srcTi, ri, dstTi, timelinePos int) Project {
	if !p.validRegion(srcTi, ri) || !p.validTrack(dstTi) {
		return p.withTracks(cloneTracks(p.Tracks))
	}
	tracks := cloneTracks(p.Tracks)
	r := tracks[srcTi].Regions[ri]
	r.TimelinePos = timelinePos
	r = r.Normalize()
	// Remove from source.
	src := tracks[srcTi].Regions
	tracks[srcTi].Regions = append(src[:ri], src[ri+1:]...)
	// Append to destination.
	tracks[dstTi].Regions = append(tracks[dstTi].Regions, r)
	return p.withTracks(tracks)
}

// TrimRegionStart returns a copy with region ri's start moved by deltaFrames in
// SOURCE frames, keeping the kept audio anchored on the TIMELINE. A positive delta
// trims later (region gets shorter from the left); a negative delta extends the
// region earlier into the source (revealing material before SourceIn), if available.
// TimelinePos shifts by the same delta so the retained audio does not slide. This is
// the left-edge drag.
func (p Project) TrimRegionStart(ti, ri, deltaFrames int) Project {
	return p.mapRegion(ti, ri, func(r Region) Region {
		newIn := r.SourceIn + deltaFrames
		if newIn < 0 {
			deltaFrames -= newIn // clamp: don't go before frame 0
			newIn = 0
		}
		if newIn > r.SourceOut {
			newIn = r.SourceOut
			deltaFrames = newIn - r.SourceIn
		}
		r.SourceIn = newIn
		r.TimelinePos += deltaFrames // keep kept-audio anchored on the timeline
		return r
	})
}

// TrimRegionEnd returns a copy with region ri's end moved by deltaFrames in SOURCE
// frames, keeping the LEFT edge fixed. A positive delta extends the region later into
// the source (clamped to the clip length by Normalize); a negative delta shortens it
// from the right. This is the right-edge drag. TimelinePos is unchanged.
func (p Project) TrimRegionEnd(ti, ri, deltaFrames int) Project {
	return p.mapRegion(ti, ri, func(r Region) Region {
		r.SourceOut += deltaFrames
		if r.SourceOut < r.SourceIn {
			r.SourceOut = r.SourceIn
		}
		return r
	})
}

// SplitRegion returns a copy with region ri on track ti cut at the given TIMELINE
// frame into two adjacent regions: a left part [TimelinePos, atTimelineFrame) and a
// right part [atTimelineFrame, TimelineEnd). The right part's ID is r.ID+rightIDSuffix.
// If the cut falls outside the region (<= start or >= end) the project is returned
// unchanged (no zero-length pieces). Use this for the razor/scissors tool.
func (p Project) SplitRegion(ti, ri, atTimelineFrame int, rightIDSuffix string) Project {
	if !p.validRegion(ti, ri) {
		return p.withTracks(cloneTracks(p.Tracks))
	}
	r := p.Tracks[ti].Regions[ri]
	if atTimelineFrame <= r.TimelinePos || atTimelineFrame >= r.TimelineEnd() {
		return p.withTracks(cloneTracks(p.Tracks)) // cut outside the region: no-op
	}
	cutLocal := atTimelineFrame - r.TimelinePos // frames into the region

	left := r
	left.SourceOut = r.SourceIn + cutLocal
	// A fade-out longer than the new left length is clamped by Normalize; the head
	// fade-in stays. The left part keeps no tail fade unless it still fits.
	left = left.Normalize()

	right := r
	right.ID = r.ID + rightIDSuffix
	right.SourceIn = r.SourceIn + cutLocal
	right.TimelinePos = atTimelineFrame
	right.FadeInFrames = 0 // the head fade belongs to the left part only
	right = right.Normalize()

	return p.mapTrack(ti, func(t Track) Track {
		regions := cloneRegions(t.Regions)
		// Replace ri with {left, right}.
		out := make([]Region, 0, len(regions)+1)
		out = append(out, regions[:ri]...)
		out = append(out, left, right)
		out = append(out, regions[ri+1:]...)
		return t.withRegions(out)
	})
}

// SetRegionGain returns a copy with region ri's linear Gain set (clamped >= 0 by
// Normalize). 1 = unity, 0.5 is about -6 dB, 2 is about +6 dB.
func (p Project) SetRegionGain(ti, ri int, gain float64) Project {
	return p.mapRegion(ti, ri, func(r Region) Region {
		r.Gain = gain
		return r
	})
}

// SetRegionFades returns a copy with region ri's fade-in/out lengths (in frames) set.
// Negative values clamp to 0; a combined length exceeding the region is shrunk to fit
// (by Normalize). Pass -1 for either argument to leave that fade unchanged.
func (p Project) SetRegionFades(ti, ri, fadeInFrames, fadeOutFrames int) Project {
	return p.mapRegion(ti, ri, func(r Region) Region {
		if fadeInFrames >= 0 {
			r.FadeInFrames = fadeInFrames
		}
		if fadeOutFrames >= 0 {
			r.FadeOutFrames = fadeOutFrames
		}
		return r
	})
}

// clampF clamps v to [lo, hi].
func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
