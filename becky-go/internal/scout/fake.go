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

// FakeProposer is a canned Proposer keyed by video ID, for testing the
// autonomous propose→judge gate with no model.
type FakeProposer struct {
	ByID map[string]Proposal
	Err  error
}

// Propose implements Proposer.
func (f FakeProposer) Propose(it Item) (Proposal, error) {
	if f.Err != nil {
		return Proposal{}, f.Err
	}
	return f.ByID[it.ID], nil // zero Proposal (WorthBuilding=false) when absent
}

// FakeJudge is a canned Judge: it agrees unless the proposal slug is in Reject.
type FakeJudge struct {
	JudgeName string
	Reject    map[string]bool // slugs this judge votes against
	Err       error
}

// Name implements Judge.
func (f FakeJudge) Name() string { return f.JudgeName }

// Vote implements Judge.
func (f FakeJudge) Vote(p Proposal, _ Item) (JudgeVote, error) {
	if f.Err != nil {
		return JudgeVote{}, f.Err
	}
	agree := !f.Reject[p.Slug]
	return JudgeVote{Judge: f.JudgeName, Agree: agree, Why: "fake judge"}, nil
}
