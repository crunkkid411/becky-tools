package forensic

import (
	"testing"

	"becky-go/internal/orchestrate"
)

const identifyJSON = `{
  "identifications": [
    {"type":"corroborated","name":"Shelby","confidence":0.88,"corroborated_by":["voice","face"]},
    {"type":"voice","name":"John","confidence":0.71}
  ],
  "unidentified": [
    {"type":"voice","candidate":"Marcus","candidate_confidence":0.62}
  ]
}`

// TestIdentifyToClaims_Corroboration: a corroborated (voice+face) identification yields a claim
// with two independent sources (concludes); single-modality and demoted candidates yield one.
func TestIdentifyToClaims_Corroboration(t *testing.T) {
	claims := IdentifyToClaims([]byte(identifyJSON))
	got := map[string]orchestrate.Status{}
	for _, c := range claims {
		got[c.Key] = orchestrate.Corroborate(c, orchestrate.DefaultRules()).Status
	}
	if got["person=Shelby"] != orchestrate.Concluded {
		t.Errorf("Shelby (voice+face) => %s, want concluded", got["person=Shelby"])
	}
	if got["person=John"] != orchestrate.Candidate {
		t.Errorf("John (single voice) => %s, want candidate", got["person=John"])
	}
	if got["person=Marcus"] != orchestrate.Candidate {
		t.Errorf("Marcus (demoted) => %s, want candidate", got["person=Marcus"])
	}
}

// Two face models (or ArcFace + Gemma's compare) agreeing inside becky-identify are two
// independent checks; one model, or a face waiting on the voice, is still one.
func TestIdentifyToClaims_TwoModelsCount(t *testing.T) {
	raw := `{"identifications":[
	  {"type":"face","name":"A","confidence":0.8,"match":"cosine+lvface"},
	  {"type":"face","name":"B","confidence":0.8,"match":"cosine+gemma"},
	  {"type":"face","name":"C","confidence":0.8,"match":"cosine"},
	  {"type":"face","name":"D","confidence":0.6,"match":"cosine (needs voice)"},
	  {"type":"voice","name":"E","confidence":0.8,"match":"redimnet-disputed (CAM++ heard X at 0.80)"}]}`
	want := map[string]orchestrate.Status{"person=A": orchestrate.Concluded, "person=B": orchestrate.Concluded,
		"person=C": orchestrate.Candidate, "person=D": orchestrate.Candidate, "person=E": orchestrate.Candidate}
	for _, c := range IdentifyToClaims([]byte(raw)) {
		if got := orchestrate.Corroborate(c, orchestrate.DefaultRules()).Status; got != want[c.Key] {
			t.Errorf("%s => %s, want %s", c.Key, got, want[c.Key])
		}
	}
}

const trJSON = `{"segments":[{"start":10,"end":11,"text":"there goes the cat"},{"start":40,"end":41,"text":"the dog barks"}]}`
const moJSON = `{"motion_bursts":[{"window_start":10.2,"window_end":12}]}`
const vaJSON = `{"observations":[
  {"segment_start":10,"segment_end":13,"visual":"a cat on the floor","finding":"cat present","confidence":0.86},
  {"segment_start":40,"segment_end":42,"visual":"a dog at the door","finding":"dog present","confidence":0.8}
]}`

// TestPresenceSignals_SubjectMatch: the cat gets a WATCH signal (the model saw a cat) at ~10s but
// NOT at ~40s (the model saw a dog there); a mention + a watch are produced, motion is included.
func TestPresenceSignals_SubjectMatch(t *testing.T) {
	sigs := PresenceSignals("cat", []byte(trJSON), []byte(moJSON), []byte(vaJSON))
	var watched, mention int
	for _, s := range sigs {
		switch s.Kind {
		case orchestrate.KindWatched:
			watched++
			if s.Start >= 40 {
				t.Errorf("a WATCH of the cat at 40s is wrong — the model saw a dog there")
			}
		case orchestrate.KindMention:
			mention++
		}
	}
	if watched != 1 {
		t.Errorf("want exactly one cat WATCH signal (~10s), got %d", watched)
	}
	if mention < 1 {
		t.Errorf("want a cat mention signal, got %d", mention)
	}
}
