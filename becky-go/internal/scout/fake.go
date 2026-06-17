// Deterministic fakes for the two boundaries (PlaylistSource, Assessor) so the
// whole pipeline runs in CI with no network and no model — proving the
// deterministic Go layer end to end. The real backends (yt-dlp for the playlist;
// a local llama.cpp model for the assessor) are wired by the local agent against
// the contracts documented in scout.go.
package scout

// FakePlaylist returns a canned playlist. If Err is set, Playlist returns it
// (exercises the degrade-never-crash path).
type FakePlaylist struct {
	PL  Playlist
	Err error
}

// Playlist implements PlaylistSource.
func (f FakePlaylist) Playlist(ref string) (Playlist, error) {
	if f.Err != nil {
		return Playlist{}, f.Err
	}
	pl := f.PL
	if pl.URL == "" {
		pl.URL = ref
	}
	return pl, nil
}

// FakeAssessor is a canned model stand-in. Verdicts are keyed by video ID so a
// test can drive the "model agrees / disagrees" corroboration branches precisely
// (mirrors research.FakeHelper). An ID absent from the map is treated as
// not-relevant — the model stayed silent.
type FakeAssessor struct {
	ByID map[string]Assessment
}

// Assess implements Assessor.
func (f FakeAssessor) Assess(v Video, _ []Capability) (Assessment, error) {
	if a, ok := f.ByID[v.ID]; ok {
		return a, nil
	}
	return Assessment{Relevant: false}, nil
}
