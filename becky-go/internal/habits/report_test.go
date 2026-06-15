package habits

import (
	"strings"
	"testing"
)

// TestDescribe_empty gives a clear "nothing learned yet" message for a fresh store.
func TestDescribe_empty(t *testing.T) {
	out := NewStore().Describe()
	if !strings.Contains(out, "hasn't learned any habits yet") {
		t.Errorf("empty describe missing guidance:\n%s", out)
	}
}

// TestDescribe_learnedVsCandidate confirms the report states a corroborated
// default as a conclusion and a one-off fix as still-a-candidate.
func TestDescribe_learnedVsCandidate(t *testing.T) {
	s := NewStore()
	s.Observe(rec("kick", "gain_db", "-7"))
	s.Observe(rec("kick", "gain_db", "-7"))  // learned
	s.Observe(rec("snare", "gain_db", "-3")) // candidate

	out := s.Describe()
	if !strings.Contains(out, "LEARNED") || !strings.Contains(out, "defaults to \"-7\"") {
		t.Errorf("learned default not stated as a conclusion:\n%s", out)
	}
	if !strings.Contains(out, "CANDIDATE") || !strings.Contains(out, "snare") {
		t.Errorf("candidate not surfaced as still-weighing:\n%s", out)
	}
	// the candidate value must NOT be presented as a learned default
	if strings.Contains(out, "snare gain_db → defaults to") {
		t.Errorf("candidate wrongly stated as a default:\n%s", out)
	}
}

// TestBuildReport_partitions confirms the JSON report splits learned vs candidate
// and echoes the threshold/schema for consumers.
func TestBuildReport_partitions(t *testing.T) {
	s := NewStore()
	s.Observe(rec("kick", "gain_db", "-7"))
	s.Observe(rec("kick", "gain_db", "-7"))
	s.Observe(rec("snare", "gain_db", "-3"))

	rep := s.BuildReport()
	if rep.Tool != "becky-habits" || rep.MinEvidence != MinEvidence || rep.SchemaVersion != SchemaVersion {
		t.Errorf("report header wrong: %+v", rep)
	}
	if len(rep.Learned) != 1 || rep.Learned[0].Scope != "kick" {
		t.Errorf("learned partition wrong: %+v", rep.Learned)
	}
	if len(rep.Candidates) != 1 || rep.Candidates[0].Scope != "snare" {
		t.Errorf("candidate partition wrong: %+v", rep.Candidates)
	}
}
