package handoff

import "testing"

// fakeRunner is a fully offline Runner stand-in. It satisfies the interface so
// the package compiles against it, but the decision logic under test is pure, so
// these methods are only here to prove no test reaches the real git/network.
type fakeRunner struct {
	out map[string]string // joined-args -> output
	err map[string]bool   // joined-args -> should error
}

func (f fakeRunner) Run(args ...string) (string, error) {
	key := join(args)
	if f.err[key] {
		return f.out[key], errFake
	}
	return f.out[key], nil
}
func (f fakeRunner) ReadFile(path string) (string, error) { return f.out["file:"+path], nil }

var errFake = errFakeT("fake: command failed")

type errFakeT string

func (e errFakeT) Error() string { return string(e) }

func join(args []string) string {
	s := ""
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += a
	}
	return s
}

// Compile-time check that the fake implements the real seam.
var _ Runner = fakeRunner{}

func TestPickNewestCloudBranch(t *testing.T) {
	cases := []struct {
		name      string
		forEach   string
		notMerged map[string]bool // ref -> unmerged?
		want      string
	}{
		{
			name:      "newest unmerged claude branch wins",
			forEach:   "origin/claude/new\norigin/claude/old\norigin/master",
			notMerged: map[string]bool{"origin/claude/new": true, "origin/claude/old": true},
			want:      "claude/new",
		},
		{
			name:      "skips already-merged newest, takes next unmerged",
			forEach:   "origin/claude/merged\norigin/claude/pending",
			notMerged: map[string]bool{"origin/claude/merged": false, "origin/claude/pending": true},
			want:      "claude/pending",
		},
		{
			name:      "ignores non-claude branches",
			forEach:   "origin/feature/x\norigin/master\norigin/claude/real",
			notMerged: map[string]bool{"origin/claude/real": true},
			want:      "claude/real",
		},
		{
			name:      "none found when no claude branches",
			forEach:   "origin/master\norigin/feature/x",
			notMerged: map[string]bool{},
			want:      "",
		},
		{
			name:      "none found when every claude branch is merged",
			forEach:   "origin/claude/a\norigin/claude/b",
			notMerged: map[string]bool{"origin/claude/a": false, "origin/claude/b": false},
			want:      "",
		},
		{
			name:      "empty input yields none",
			forEach:   "",
			notMerged: map[string]bool{},
			want:      "",
		},
		{
			name:      "blank lines are tolerated",
			forEach:   "\n  \norigin/claude/topic\n",
			notMerged: map[string]bool{"origin/claude/topic": true},
			want:      "claude/topic",
		},
		{
			name:      "refs/remotes prefix is stripped",
			forEach:   "refs/remotes/origin/claude/full",
			notMerged: map[string]bool{"refs/remotes/origin/claude/full": true},
			want:      "claude/full",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PickNewestCloudBranch(c.forEach, func(ref string) bool {
				return c.notMerged[ref]
			})
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestDecide(t *testing.T) {
	const ready = "**Left for local agent:** nothing — merged after build/test passed."
	const review = "*Left for local:* review + ratify the protocol, then merge."
	const noMarker = "Some CLAUDE.md text with no handoff marker at all."

	cases := []struct {
		name   string
		in     Inputs
		want   Action
		reason string // substring expected in Reason
	}{
		{
			name: "no branch => up-to-date",
			in:   Inputs{Branch: ""},
			want: ActionUpToDate,
		},
		{
			name: "whitespace branch => up-to-date",
			in:   Inputs{Branch: "   "},
			want: ActionUpToDate,
		},
		{
			name: "green + ready => merge",
			in:   Inputs{Branch: "claude/x", BuildOK: true, TestOK: true, IsFastFwd: true, Section6Text: ready},
			want: ActionMerge,
		},
		{
			name:   "green + REVIEW pending => handoff",
			in:     Inputs{Branch: "claude/x", BuildOK: true, TestOK: true, IsFastFwd: true, Section6Text: review},
			want:   ActionHandoff,
			reason: "still needs review",
		},
		{
			name:   "green + no marker => handoff",
			in:     Inputs{Branch: "claude/x", BuildOK: true, TestOK: true, IsFastFwd: true, Section6Text: noMarker},
			want:   ActionHandoff,
			reason: "still needs review",
		},
		{
			name:   "build fail => handoff",
			in:     Inputs{Branch: "claude/x", BuildOK: false, TestOK: true, IsFastFwd: true, Section6Text: ready},
			want:   ActionHandoff,
			reason: "did not build",
		},
		{
			name:   "test fail => handoff",
			in:     Inputs{Branch: "claude/x", BuildOK: true, TestOK: false, IsFastFwd: true, Section6Text: ready},
			want:   ActionHandoff,
			reason: "test did not pass",
		},
		{
			name:   "not fast-forward => handoff",
			in:     Inputs{Branch: "claude/x", BuildOK: true, TestOK: true, IsFastFwd: false, Section6Text: ready},
			want:   ActionHandoff,
			reason: "fast-forward",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.in)
			if got.Action != c.want {
				t.Fatalf("action: got %q want %q (reason: %q)", got.Action, c.want, got.Reason)
			}
			if got.Reason == "" {
				t.Error("every decision must carry a plain-language reason")
			}
			if c.reason != "" && !contains(got.Reason, c.reason) {
				t.Errorf("reason %q missing substring %q", got.Reason, c.reason)
			}
			if c.want == ActionHandoff && got.NextStep == "" {
				t.Error("handoff must include a next step for the human")
			}
			if c.want != ActionUpToDate && got.Branch != c.in.Branch {
				t.Errorf("branch echoed wrong: got %q want %q", got.Branch, c.in.Branch)
			}
		})
	}
}

// TestDecide_blockOrder confirms a branch with MULTIPLE problems reports the
// most fundamental one first (build before test before FF before §6).
func TestDecide_blockOrder(t *testing.T) {
	in := Inputs{Branch: "claude/x", BuildOK: false, TestOK: false, IsFastFwd: false, Section6Text: ""}
	d := Decide(in)
	if d.Action != ActionHandoff {
		t.Fatalf("want handoff, got %q", d.Action)
	}
	if !contains(d.Reason, "did not build") {
		t.Errorf("expected build to be reported first, got %q", d.Reason)
	}
}

func TestReadyForLocal(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"nothing => ready", "Left for local agent: nothing", true},
		{"case-insensitive marker + nothing", "left for LOCAL: NOTHING left to do", true},
		{"review pending => not ready", "Left for local: review the protocol", false},
		{"marker missing => not ready", "no marker here", false},
		{"empty => not ready", "", false},
		{"marker present, work listed => not ready", "**Left for local agent:** wire the helper stub", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := readyForLocal(c.text); got != c.want {
				t.Errorf("readyForLocal(%q) = %v want %v", c.text, got, c.want)
			}
		})
	}
}

// TestFakeRunner_offline is a guard: it shows the seam can be exercised entirely
// in-memory (no git, no network), which is the whole reason the Runner exists.
func TestFakeRunner_offline(t *testing.T) {
	f := fakeRunner{
		out: map[string]string{"git status": "clean", "file:CLAUDE.md": "Left for local: nothing"},
		err: map[string]bool{"git fetch origin": true},
	}
	if out, err := f.Run("git", "status"); err != nil || out != "clean" {
		t.Errorf("run status: out=%q err=%v", out, err)
	}
	if _, err := f.Run("git", "fetch", "origin"); err == nil {
		t.Error("expected the configured fetch failure")
	}
	if txt, _ := f.ReadFile("CLAUDE.md"); !contains(txt, "nothing") {
		t.Errorf("readfile: got %q", txt)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
