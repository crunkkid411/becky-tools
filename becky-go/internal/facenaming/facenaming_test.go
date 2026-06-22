package facenaming

import (
	"errors"
	"reflect"
	"testing"
)

// fakeEnroller records every Enroll call so tests can assert the cluster->enroll
// wiring (clip + name + kb) without running models/ffmpeg. failOn returns an error
// for any clip whose path is in the set, to exercise the skip-with-reason path.
type fakeEnroller struct {
	calls  []enrollCall
	failOn map[string]string // clip -> reason
}

type enrollCall struct{ clip, name, kb string }

func (f *fakeEnroller) Enroll(clip, name, kb string) error {
	f.calls = append(f.calls, enrollCall{clip, name, kb})
	if r, bad := f.failOn[clip]; bad {
		return errors.New(r)
	}
	return nil
}

// fakeShower records the paths it was asked to show.
type fakeShower struct{ shown []string }

func (f *fakeShower) Show(p string) error { f.shown = append(f.shown, p); return nil }

var _ enroller = (*fakeEnroller)(nil)
var _ imageShower = (*fakeShower)(nil)

func clusterFixture() Clusters {
	return Clusters{
		Tool:     "becky-cluster v1.0.0",
		Modality: "both",
		Clusters: []Cluster{
			{
				ClusterID: "face-A", Modality: "face", MemberCount: 3, DistinctSourceFiles: 2,
				Cohesion: 0.71, Representative: "a1.mp4",
				Members: []Member{
					{SourceFile: "a1.mp4", DetScore: 0.92, Timestamp: 2.0},
					{SourceFile: "a2.mp4", DetScore: 0.81, Timestamp: 5.0},
					{SourceFile: "a1.mp4", DetScore: 0.60, Timestamp: 9.0}, // dup file
				},
			},
			{
				ClusterID: "voice-A", Modality: "voice", MemberCount: 5, DistinctSourceFiles: 5,
				Cohesion: 0.84, Representative: "v1.wav",
				Members: []Member{
					{SourceFile: "v1.wav", DetScore: 0.99},
					{SourceFile: "v2.wav", DetScore: 0.95},
					{SourceFile: "v3.wav", DetScore: 0.90},
					{SourceFile: "v4.wav", DetScore: 0.85},
					{SourceFile: "v5.wav", DetScore: 0.80},
				},
			},
		},
	}
}

// TestEnrollArgs_GoldenArgv asserts the EXACT argv for a 2-distinct-clip cluster named
// "Braxton" — paths filled, name passed, kb passed, in strongest-first order.
func TestEnrollArgs_GoldenArgv(t *testing.T) {
	cl := clusterFixture().Clusters[0] // face-A, distinct clips a1.mp4, a2.mp4
	got := EnrollArgs(cl, "Braxton", "kb-final", "", 0)
	want := [][]string{
		{"becky-enroll", "--clip", "a1.mp4", "--name", "Braxton", "--kb", "kb-final"},
		{"becky-enroll", "--clip", "a2.mp4", "--name", "Braxton", "--kb", "kb-final"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnrollArgs mismatch:\n got  %v\n want %v", got, want)
	}
}

// TestEnrollArgs_WithDevice asserts --device is appended only when set.
func TestEnrollArgs_WithDevice(t *testing.T) {
	cl := Cluster{ClusterID: "face-Z", Members: []Member{{SourceFile: "z.mp4", DetScore: 1}}}
	got := EnrollArgs(cl, "Z", "kb", "cuda", 0)
	want := [][]string{{"becky-enroll", "--clip", "z.mp4", "--name", "Z", "--kb", "kb", "--device", "cuda"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("device argv mismatch:\n got  %v\n want %v", got, want)
	}
}

// TestEnrollArgs_DedupesAndCaps asserts repeated SourceFiles yield ONE argv per
// distinct clip, and the per-cluster cap bounds the count.
func TestEnrollArgs_DedupesAndCaps(t *testing.T) {
	c := clusterFixture()
	// face-A has a duplicate a1.mp4 -> 2 distinct argv.
	if n := len(EnrollArgs(c.Clusters[0], "X", "kb", "", 0)); n != 2 {
		t.Fatalf("dedupe: expected 2 distinct argv, got %d", n)
	}
	// voice-A has 5 distinct clips; cap at 3 -> 3 argv, strongest first.
	got := EnrollArgs(c.Clusters[1], "Y", "kb", "", 3)
	if len(got) != 3 {
		t.Fatalf("cap: expected 3 argv, got %d", len(got))
	}
	wantClips := []string{"v1.wav", "v2.wav", "v3.wav"}
	for i, a := range got {
		if a[2] != wantClips[i] {
			t.Errorf("cap order [%d]: got %q want %q", i, a[2], wantClips[i])
		}
	}
}

// TestApplyNames_WiresClusterToEnroll asserts naming face-A="Braxton" calls Enroll
// once per distinct member clip with name "Braxton" (the cluster->enroll wiring,
// asserted via the fake enroller's recorded calls).
func TestApplyNames_WiresClusterToEnroll(t *testing.T) {
	c := clusterFixture()
	fe := &fakeEnroller{}
	names := map[string]string{"face-A": "Braxton"} // voice-A intentionally unnamed
	res := ApplyNames(c, names, fe, "", 0, 0)

	want := []enrollCall{
		{clip: "a1.mp4", name: "Braxton", kb: ""},
		{clip: "a2.mp4", name: "Braxton", kb: ""},
	}
	if !reflect.DeepEqual(fe.calls, want) {
		t.Fatalf("enroll calls mismatch:\n got  %v\n want %v", fe.calls, want)
	}
	if len(res.Outcomes) != 1 || res.Outcomes[0].ClusterID != "face-A" {
		t.Fatalf("expected one outcome for face-A, got %+v", res.Outcomes)
	}
	if len(res.Outcomes[0].Enrolled) != 2 {
		t.Errorf("expected 2 enrolled clips, got %v", res.Outcomes[0].Enrolled)
	}
	if !contains(res.SkippedID, "voice-A") {
		t.Errorf("expected voice-A recorded as skipped, got %v", res.SkippedID)
	}
}

// TestApplyNames_SkipLeavesUnnamed asserts a blank/absent name produces NO enroll
// calls and is recorded as a skip (never a name invented).
func TestApplyNames_SkipLeavesUnnamed(t *testing.T) {
	c := clusterFixture()
	fe := &fakeEnroller{}
	names := map[string]string{"face-A": "   ", "voice-A": ""} // both blank
	res := ApplyNames(c, names, fe, "", 0, 0)
	if len(fe.calls) != 0 {
		t.Fatalf("blank names must enroll nothing, got %d calls", len(fe.calls))
	}
	if len(res.Outcomes) != 0 {
		t.Fatalf("blank names must produce no outcomes, got %d", len(res.Outcomes))
	}
	if len(res.SkippedID) != 2 {
		t.Fatalf("both clusters should be skipped, got %v", res.SkippedID)
	}
}

// TestApplyNames_FailedClipSkippedWithReason asserts a clip that fails to enroll is
// recorded as a skip-with-reason and the loop continues (degrade-never-crash).
func TestApplyNames_FailedClipSkippedWithReason(t *testing.T) {
	c := clusterFixture()
	fe := &fakeEnroller{failOn: map[string]string{"a1.mp4": "no clean frame"}}
	res := ApplyNames(c, map[string]string{"face-A": "Braxton"}, fe, "", 0, 0)
	o := res.Outcomes[0]
	if len(o.Enrolled) != 1 || o.Enrolled[0] != "a2.mp4" {
		t.Errorf("expected a2.mp4 enrolled, got %v", o.Enrolled)
	}
	if len(o.Skipped) != 1 || o.Skipped[0] != "a1.mp4" {
		t.Errorf("expected a1.mp4 skipped, got %v", o.Skipped)
	}
	if len(o.Reasons) != 1 || o.Reasons[0] != "no clean frame" {
		t.Errorf("expected recorded reason, got %v", o.Reasons)
	}
}

// TestWalkOrder_BiggestFirst asserts deterministic biggest-first ordering.
func TestWalkOrder_BiggestFirst(t *testing.T) {
	c := clusterFixture() // voice-A=5, face-A=3
	order := WalkOrder(c, "", 0)
	if len(order) != 2 || order[0].ClusterID != "voice-A" || order[1].ClusterID != "face-A" {
		t.Fatalf("expected [voice-A, face-A] biggest-first, got %v", ids(order))
	}
}

// TestWalkOrder_ModalityAndMinFilter asserts modality + min-clips filtering.
func TestWalkOrder_ModalityAndMinFilter(t *testing.T) {
	c := clusterFixture()
	faceOnly := WalkOrder(c, "face", 0)
	if len(faceOnly) != 1 || faceOnly[0].ClusterID != "face-A" {
		t.Fatalf("face filter: got %v", ids(faceOnly))
	}
	bigOnly := WalkOrder(c, "", 4) // only voice-A (5) clears 4
	if len(bigOnly) != 1 || bigOnly[0].ClusterID != "voice-A" {
		t.Fatalf("min-clips filter: got %v", ids(bigOnly))
	}
}

// TestLoadClusters_BadJSON_Degrades asserts malformed JSON -> typed error, no panic.
func TestLoadClusters_BadJSON_Degrades(t *testing.T) {
	if _, err := LoadClusters([]byte("{not json")); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
	// Well-formed but empty is valid (not an error).
	c, err := LoadClusters([]byte(`{"tool":"becky-cluster","clusters":[]}`))
	if err != nil {
		t.Fatalf("empty clusters should load cleanly, got %v", err)
	}
	if len(c.Clusters) != 0 {
		t.Fatalf("expected 0 clusters, got %d", len(c.Clusters))
	}
}

// TestLoadClusters_RealShape parses a becky-cluster-shaped JSON and reads the fields
// the loop needs.
func TestLoadClusters_RealShape(t *testing.T) {
	data := []byte(`{
		"tool":"becky-cluster v1.0.0","modality":"both","min_cluster":2,
		"clusters":[
			{"cluster_id":"voice-A","modality":"voice","suggested_name":null,
			 "member_count":41,"distinct_source_files":38,"cohesion":0.71,
			 "representative":"clip1.wav",
			 "members":[{"source_file":"clip1.wav","det_score":0.9,"timestamp":1.0}]}
		]}`)
	c, err := LoadClusters(data)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(c.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(c.Clusters))
	}
	cl := c.Clusters[0]
	if cl.MemberCount != 41 || cl.DistinctSourceFiles != 38 || cl.Representative != "clip1.wav" {
		t.Fatalf("fields not parsed: %+v", cl)
	}
	if cl.SuggestedName != nil {
		t.Fatalf("suggested_name should be nil (unnamed)")
	}
}

// TestDryRunPlan_PrintsArgvNoEnroll asserts --dry-run produces the exact argv and runs
// zero enroll calls (the offline proof from SPEC §5).
func TestDryRunPlan_PrintsArgvNoEnroll(t *testing.T) {
	c := clusterFixture()
	fe := &fakeEnroller{}
	plan := DryRunPlan(c, map[string]string{"face-A": "Braxton"}, "kb-final", "", "", 0, 0)
	want := [][]string{
		{"becky-enroll", "--clip", "a1.mp4", "--name", "Braxton", "--kb", "kb-final"},
		{"becky-enroll", "--clip", "a2.mp4", "--name", "Braxton", "--kb", "kb-final"},
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("dry-run plan mismatch:\n got  %v\n want %v", plan, want)
	}
	if len(fe.calls) != 0 {
		t.Fatalf("dry-run must not enroll, got %d calls", len(fe.calls))
	}
}

// TestSummary_PlainEnglish asserts the one-line confirmations match the spec phrasing.
func TestSummary_PlainEnglish(t *testing.T) {
	full := EnrollOutcome{Name: "Braxton", KB: "kb-final", Enrolled: []string{"a", "b"}}
	if got, want := full.Summary(), "Enrolled Braxton from 2 clip(s) → kb-final"; got != want {
		t.Errorf("full summary: got %q want %q", got, want)
	}
	partial := EnrollOutcome{Name: "Braxton", KB: "kb", Enrolled: []string{"a"}, Skipped: []string{"b"}, Reasons: []string{"no clean frame"}}
	if got := partial.Summary(); got != "Enrolled Braxton: 1 clip(s) ✓, 1 skipped (no clean frame)" {
		t.Errorf("partial summary: got %q", got)
	}
}

// TestModalitySummary asserts the no-TTY parsed-summary count.
func TestModalitySummary(t *testing.T) {
	if got, want := ModalitySummary(clusterFixture()), "1 face, 1 voice"; got != want {
		t.Errorf("modality summary: got %q want %q", got, want)
	}
}

func ids(cs []Cluster) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ClusterID
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
