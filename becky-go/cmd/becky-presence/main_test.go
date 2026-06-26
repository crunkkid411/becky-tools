package main

import (
	"testing"

	"becky-go/internal/forensic"
	"becky-go/internal/orchestrate"
)

const transcribeFixture = `{"segments":[
  {"start":10.0,"end":11.0,"text":"there goes the cat again"},
  {"start":40.0,"end":41.0,"text":"the dog is barking"}
]}`
const motionFixture = `{"motion_bursts":[
  {"window_start":10.2,"window_end":12.0},
  {"window_start":40.0,"window_end":41.5}
]}`
const validateFixture = `{"observations":[
  {"segment_start":10.0,"segment_end":13.0,"visual":"a cat walks across the floor","finding":"cat present","confidence":0.86},
  {"segment_start":40.0,"segment_end":42.0,"visual":"a dog by the door","finding":"dog present","confidence":0.8}
]}`

// TestPresence_ConcludesOnlyWhereWatched: the real becky-presence flow (forensic mapping +
// orchestrate). The cat is stated on screen at [10-13] (mention + motion + a cat WATCH agree),
// but NOT at 40s — there the model watched a DOG, so the cat window stays a candidate to review.
func TestPresence_ConcludesOnlyWhereWatched(t *testing.T) {
	sigs := forensic.PresenceSignals("cat", []byte(transcribeFixture), []byte(motionFixture), []byte(validateFixture))
	claims := orchestrate.CorrelatePresence("cat", sigs, 2.0)
	res := orchestrate.Resolve(claims, orchestrate.DefaultRules(), nil, 0)

	if len(res.Concluded) != 1 || res.Concluded[0].Claim != "onscreen=cat@[10.0-13.0]" {
		t.Fatalf("want exactly onscreen=cat@[10.0-13.0] concluded, got %+v", res.Concluded)
	}
	for _, v := range res.Concluded {
		if v.Claim != "onscreen=cat@[10.0-13.0]" {
			t.Errorf("only the watched cat window may conclude, got %q", v.Claim)
		}
	}
}

// TestPresence_DogConcludesCatDoesNot: searching the dog at 40s concludes (the model watched a
// dog); searching the cat at 40s must not.
func TestPresence_DogConcludesCatDoesNot(t *testing.T) {
	dogSigs := forensic.PresenceSignals("dog", []byte(transcribeFixture), []byte(motionFixture), []byte(validateFixture))
	dog := orchestrate.Resolve(orchestrate.CorrelatePresence("dog", dogSigs, 2.0), orchestrate.DefaultRules(), nil, 0)
	foundDog := false
	for _, v := range dog.Concluded {
		if v.Claim == "onscreen=dog@[40.0-42.0]" {
			foundDog = true
		}
	}
	if !foundDog {
		t.Errorf("dog should be concluded on screen at 40s, got %+v", dog.Concluded)
	}

	catSigs := forensic.PresenceSignals("cat", []byte(transcribeFixture), []byte(motionFixture), []byte(validateFixture))
	cat := orchestrate.Resolve(orchestrate.CorrelatePresence("cat", catSigs, 2.0), orchestrate.DefaultRules(), nil, 0)
	for _, v := range cat.Concluded {
		if v.Claim == "onscreen=cat@[40.0-41.5]" || v.Claim == "onscreen=cat@[40.0-42.0]" {
			t.Errorf("cat must NOT conclude at 40s — the model watched a dog there")
		}
	}
}
