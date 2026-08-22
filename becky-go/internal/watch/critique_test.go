package watch

import "testing"

// A rejection that does not say what to aim at instead is not actionable: acting
// on it would re-run the identical framing pass, get the identical answer, and
// burn a whole re-render doing it.
func TestUnactionableRejectionIsAnError(t *testing.T) {
	if _, err := parseVerdict(`{"ok": false, "problem": "the framing is wrong", "subject": ""}`); err == nil {
		t.Error("a rejection with no subject was accepted; it would cost a pointless re-render")
	}
	v, err := parseVerdict(`{"ok": false, "problem": "it is on a poster", "subject": "the mouse trap"}`)
	if err != nil {
		t.Fatalf("an actionable rejection was refused: %v", err)
	}
	if v.OK || v.Subject != "the mouse trap" {
		t.Errorf("parsed %+v", v)
	}
}

// The model fences its JSON and chats around it; the parse must survive that.
func TestVerdictSurvivesFencedJSON(t *testing.T) {
	v, err := parseVerdict("Looking at the frames:\n```json\n" +
		`{"ok": true, "problem": "", "subject": ""}` + "\n```\nHope that helps!")
	if err != nil {
		t.Fatalf("fenced JSON did not parse: %v", err)
	}
	if !v.OK {
		t.Error("an approval parsed as a rejection")
	}
}

// THE CRITIC MUST BE TOLD WHAT THE CLIP IS ABOUT. Without that yardstick it is
// judging a vertical crop against nothing, which is how a critic silently
// degrades into a rubber stamp.
func TestCritiquePromptCarriesTheYardstick(t *testing.T) {
	p := critiquePrompt(CritiqueOptions{
		About:   "The prank is revealed and the person reacts.",
		Framing: `grounded "colorful poster"`,
	})
	for _, want := range []string{
		"The prank is revealed and the person reacts.", // what it is judged against
		"FINISHED vertical short",                      // it is looking at output, not a plan
	} {
		if !contains(p, want) {
			t.Errorf("the critic prompt is missing %q", want)
		}
	}

	// AND IT MUST NOT CARRY BECKY'S OWN ANSWER. Measured 2026-08-21: showing the
	// critic what becky thought it framed on made it adopt that as the subject
	// ("The colorful poster, WHICH THE CLIP IS ABOUT, is not visible") and demand
	// a re-frame onto the very thing it was supposed to catch.
	if contains(p, "colorful poster") {
		t.Error("the critic prompt leaks becky's own framing claim; the model will anchor on it")
	}
}

// With no yardstick the critic must be warned OFF the eye-catching object,
// because that is precisely what the detectors get wrong.
func TestCritiquePromptWithoutAYardstickWarnsAgainstScenery(t *testing.T) {
	p := critiquePrompt(CritiqueOptions{})
	if !contains(p, "scenery") {
		t.Error("with no 'about' the prompt does not warn that a poster or sofa is scenery")
	}
}

// The backstop: a verdict asking for what is already in frame is not a
// correction and must not cost a re-render.
func TestVerdictUsableRejectsAnEcho(t *testing.T) {
	echo := Verdict{OK: false, Subject: "the colorful poster"}
	if ok, _ := echo.Usable("colorful poster"); ok {
		t.Error("a verdict echoing becky's current framing was accepted as a correction")
	}
	real := Verdict{OK: false, Subject: "the man in the pink shirt"}
	if ok, _ := real.Usable("colorful poster"); !ok {
		t.Error("a genuine correction was rejected as an echo")
	}
	if ok, _ := (Verdict{OK: true}).Usable("colorful poster"); !ok {
		t.Error("an approval was treated as an echo")
	}
	// Nothing to compare against must never block a correction.
	if ok, _ := real.Usable(""); !ok {
		t.Error("a correction was blocked when becky had no named framing at all")
	}
}

// A shot of a room is normal in this footage and must not be reported as a
// framing mistake on its own - that would re-render every short forever.
func TestCritiquePromptDoesNotTreatEveryRoomShotAsAFault(t *testing.T) {
	p := critiquePrompt(CritiqueOptions{About: "x"})
	if !contains(p, "not every shot is on a face") {
		t.Error("the prompt does not tell the critic that face-less shots are legitimate")
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && indexOf(hay, needle) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
