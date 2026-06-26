package main

import (
	"encoding/json"
	"testing"

	"becky-go/internal/orchestrate"
)

// real-shaped tool fixtures (match becky-transcribe / becky-motion / becky-validate contracts).
const transcribeFixture = `{"segments":[
  {"start":10.0,"end":11.0,"text":"there goes the cat again"},
  {"start":40.0,"end":41.0,"text":"the dog is barking"}
]}`

const motionFixture = `{"motion_bursts":[
  {"window_start":10.2,"window_end":12.0},
  {"window_start":40.0,"window_end":41.5}
]}`

// validate WATCHED the cat at ~10s, but at ~40s it saw a dog (not the cat).
const validateFixture = `{"observations":[
  {"segment_start":10.0,"segment_end":13.0,"visual":"a cat walks across the floor","finding":"cat present","confidence":0.86},
  {"segment_start":40.0,"segment_end":42.0,"visual":"a dog by the door","finding":"dog present","confidence":0.8}
]}`

func parse(t *testing.T) (transcribeDoc, motionDoc, validateDoc) {
	t.Helper()
	var tr transcribeDoc
	var mo motionDoc
	var va validateDoc
	if err := json.Unmarshal([]byte(transcribeFixture), &tr); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(motionFixture), &mo); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(validateFixture), &va); err != nil {
		t.Fatal(err)
	}
	return tr, mo, va
}

// TestPresence_ConcludesOnlyWhereWatched: the cat is CONCLUDED on screen at ~10s (mention +
// motion + a model watch all agree) but NOT at ~40s — there, only a mention/motion exist and the
// model watched a DOG, not the cat. So becky states exactly one tight interval, and the 40s window
// stays a candidate-to-review, never a stated presence.
func TestPresence_ConcludesOnlyWhereWatched(t *testing.T) {
	tr, mo, va := parse(t)
	sigs := signalsFor("cat", tr, mo, va)
	claims := orchestrate.CorrelatePresence("cat", sigs, 2.0)
	res := orchestrate.Resolve(claims, orchestrate.DefaultRules(), nil, 0)

	if len(res.Concluded) != 1 {
		t.Fatalf("want exactly one concluded on-screen interval, got %d (%+v)", len(res.Concluded), res.Concluded)
	}
	if got := res.Concluded[0].Claim; got != "onscreen=cat@[10.0-13.0]" {
		t.Errorf("concluded interval = %q, want onscreen=cat@[10.0-13.0]", got)
	}
	// the 40s window must NOT be stated as the cat on screen (the model saw a dog there).
	for _, v := range res.Concluded {
		if v.Claim != "onscreen=cat@[10.0-13.0]" {
			t.Errorf("unexpected concluded presence: %q (only the watched cat window may conclude)", v.Claim)
		}
	}
}

// TestPresence_NoWatchedSubjectStaysCandidate: searching for the dog at 40s — there's a mention
// and a motion burst and a model watch of a dog, so it concludes; but searching for "cat" at 40s
// must NOT conclude (no watch of a cat there).
func TestPresence_DogConcludesCatDoesNot(t *testing.T) {
	tr, mo, va := parse(t)

	dog := orchestrate.Resolve(orchestrate.CorrelatePresence("dog", signalsFor("dog", tr, mo, va), 2.0), orchestrate.DefaultRules(), nil, 0)
	foundDog := false
	for _, v := range dog.Concluded {
		if v.Claim == "onscreen=dog@[40.0-42.0]" {
			foundDog = true
		}
	}
	if !foundDog {
		t.Errorf("dog should be concluded on screen at 40s (watched), got %+v", dog.Concluded)
	}

	cat := orchestrate.Resolve(orchestrate.CorrelatePresence("cat", signalsFor("cat", tr, mo, va), 2.0), orchestrate.DefaultRules(), nil, 0)
	for _, v := range cat.Concluded {
		if v.Claim == "onscreen=cat@[40.0-41.5]" || v.Claim == "onscreen=cat@[40.0-42.0]" {
			t.Errorf("cat must NOT be concluded at 40s — the model watched a dog there, not a cat")
		}
	}
}
