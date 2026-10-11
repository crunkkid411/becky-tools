// invideo.go - tell the people IN ONE VIDEO apart before naming anyone.
//
// The old rule compared each speaker only with the enrolled voices, so with one person
// enrolled every speaker that sounded a bit like him could be named him (hj-clone
// TOOL-ISSUES #13: other people's lines called Jordan's in 69% of cases). A person is
// one voice: in a video with several speakers, only the speaker who is clearly closer to
// the enrolled voice than everyone else in that same video can be them, unless two
// labels are one voice split in two. And when the transcript says who is who
// (context.go), that decides, unless the voice clearly points at someone else.
package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	minTalk = 2.0 // seconds: a label with less speech is too short to compare
	// voice bars on ReDimNet2+'s scale (CAM++ uses its own, see voiceBars)
	clearLead = 0.10 // the closest speaker must beat the next one in the same video by this
)

// labelScore is one speaker label's voice compared with one enrolled person.
type labelScore struct {
	ID    string
	Sim   float64 // cosine with the enrolled person
	Talk  float64 // seconds of speech
	Emb   []float64
	Spans []SpeakerSpan
}

// videoVerdict is who one enrolled person is in this video.
type videoVerdict struct {
	Owners []string // their speaker label(s); empty = not settled
	How    string   // "voice", "voice+context"
	Via    string   // the second sign with "voice+context": context | look | jordan
	Why    string   // plain words when nobody is named
	Tied   []string // labels that sound about equally like them (voicecheck.go escalates these)
}

// samePerson is how alike two labels' voices must be to be one person split in two.
func samePerson(model string) float64 {
	if model == "redimnet" {
		return 0.60
	}
	return 0.75 // CAM++ same-person band (README: 0.76-0.91)
}

// decideInVideo picks the label(s) of one enrolled person among the speakers of one video
// (pure). floor is the voice detection bar; pick is what the transcript said (nil = no words).
func decideInVideo(scores []labelScore, pick *contextPick, floor float64, model string) videoVerdict {
	var talkers []labelScore
	for _, s := range scores {
		if s.Emb != nil && s.Talk >= minTalk {
			talkers = append(talkers, s)
		}
	}
	sort.SliceStable(talkers, func(i, j int) bool { return talkers[i].Sim > talkers[j].Sim })
	if len(talkers) < 2 {
		return videoVerdict{How: "single"} // one voice: the enrolled-voice bars decide alone
	}
	top, next := talkers[0], talkers[1]
	byID := map[string]labelScore{}
	for _, t := range talkers {
		byID[t.ID] = t
	}
	voice := byVoice(talkers, floor, model)
	if pick == nil || !pick.sure() || len(pick.Labels) == 0 {
		return voice
	}
	var picked []string
	for _, id := range pick.Labels {
		if _, ok := byID[id]; ok {
			picked = append(picked, id)
		}
	}
	if len(picked) == 0 {
		return voice
	}
	sort.Strings(picked)
	via := pick.Via
	if via == "" {
		via = "context"
	}
	// A look or Jordan comes AFTER the words and the voice disagreed (voicecheck.go): it decides.
	if via == "look" || via == "jordan" {
		return videoVerdict{Owners: picked, How: "voice+context", Via: via}
	}
	if len(voice.Owners) > 0 { // the voice is clear: the words agree, or the look settles it
		if subset(picked, voice.Owners) { // agree (the words may name only part of a split voice)
			return videoVerdict{Owners: voice.Owners, How: "voice+context", Via: via}
		}
		voice.Tied = disputed(picked, top, floor)
		voice.Why = fmt.Sprintf("the words point at %s, but %s sounds clearly more like them (%.2f): named by voice, checking",
			strings.Join(picked, "+"), top.ID, top.Sim)
		return voice
	}
	// the voice cannot settle it: the words break the tie when every picked voice is a candidate
	low := ""
	for _, id := range picked {
		if byID[id].Sim < floor {
			low = id
		}
	}
	if low == "" {
		return videoVerdict{Owners: picked, How: "voice+context", Via: via}
	}
	_ = next
	return videoVerdict{Tied: disputed(picked, top, floor),
		Why: fmt.Sprintf("the words point at %s, but that voice does not sound like them (%.2f)", low, byID[low].Sim)}
}

// byVoice is the decision from the voices alone (pure; talkers sorted, 2 or more).
func byVoice(talkers []labelScore, floor float64, model string) videoVerdict {
	top, next := talkers[0], talkers[1]
	if top.Sim < floor {
		return videoVerdict{Why: "no speaker sounds like them"}
	}
	same := samePerson(model)
	owners := []string{top.ID}
	for _, t := range talkers[1:] { // one voice split across labels
		if t.Sim >= floor && cosine(t.Emb, top.Emb) >= same {
			owners = append(owners, t.ID)
		}
	}
	if len(owners) == 1 && top.Sim-next.Sim < clearLead {
		var tied []string
		for _, t := range talkers {
			if t.Sim >= floor && top.Sim-t.Sim < clearLead {
				tied = append(tied, t.ID)
			}
		}
		return videoVerdict{Tied: tied, Why: fmt.Sprintf("%s and %s sound about equally like them (%.2f vs %.2f) and are different voices",
			top.ID, next.ID, top.Sim, next.Sim)}
	}
	sort.Strings(owners)
	return videoVerdict{Owners: owners, How: "voice"}
}

func subset(a, b []string) bool {
	for _, x := range a {
		if !contains(b, x) {
			return false
		}
	}
	return true
}

// disputed is every label in a words-versus-voice disagreement: the words' pick, plus the
// closest voice when it is a candidate at all.
func disputed(picked []string, top labelScore, floor float64) []string {
	out := append([]string(nil), picked...)
	if top.Sim >= floor && !contains(out, top.ID) {
		out = append([]string{top.ID}, out...)
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func talkSeconds(spans []SpeakerSpan) float64 {
	t := 0.0
	for _, s := range spans {
		t += s.End - s.Start
	}
	return t
}

// speakerAudit is the basis for every speaker's outcome, shown in the output.
type speakerAudit struct {
	SpeakerID string             `json:"speaker_id"`
	TalkS     float64            `json:"talk_s"`
	Voice     map[string]float64 `json:"voice"`              // cosine with each enrolled person
	Decision  string             `json:"decision,omitempty"` // plain words
}

// applyInVideo re-decides the voice names of a video with several speakers: each enrolled
// person gets at most the label(s) decideInVideo picks. A label the old per-speaker bars
// had named but that loses here becomes a candidate with the reason.
func applyInVideo(speakers []speakerAudio, enrolled []enrolledVoice, ids []Identification, unids []Unidentified,
	picks []contextPick, opts voiceOptions, model string) ([]Identification, []Unidentified, []speakerAudit, []voiceTie) {
	var ties []voiceTie
	audit := make([]speakerAudit, len(speakers))
	for i, sp := range speakers {
		audit[i] = speakerAudit{SpeakerID: sp.id, TalkS: round4(talkSeconds(sp.segments)), Voice: map[string]float64{}}
		for _, e := range enrolled {
			if sp.embedding != nil {
				audit[i].Voice[e.name] = round4(cosine(sp.embedding, e.embedding))
			}
		}
	}
	prev := map[string]Identification{} // speaker -> old voice name
	for _, id := range ids {
		prev[id.SpeakerID] = id
	}
	owned := map[string]Identification{}
	lost := map[string]string{} // speaker -> why it lost its old name
	settled := map[string]bool{}
	for _, e := range filterCast(enrolled, opts.cast) {
		var scores []labelScore
		for _, sp := range speakers {
			s := labelScore{ID: sp.id, Talk: talkSeconds(sp.segments), Emb: sp.embedding, Spans: sp.segments}
			if sp.embedding != nil {
				s.Sim = cosine(sp.embedding, e.embedding)
			}
			scores = append(scores, s)
		}
		var pick *contextPick
		for i := range picks {
			if strings.EqualFold(picks[i].Name, e.name) {
				pick = &picks[i]
			}
		}
		v := decideInVideo(scores, pick, opts.threshold, model)
		if v.How == "single" {
			continue
		}
		if len(v.Tied) > 0 {
			t := voiceTie{Name: e.name, Key: e.key, Why: v.Why, Dispute: strings.HasPrefix(v.Why, "the words"), Named: len(v.Owners) > 0, Spans: map[string][]SpeakerSpan{}}
			for _, sc := range scores {
				if contains(v.Tied, sc.ID) {
					t.Labels = append(t.Labels, sc.ID)
					t.Spans[sc.ID] = sc.Spans
				}
			}
			ties = append(ties, t)
		}
		settled[e.name] = true
		for _, s := range scores {
			if contains(v.Owners, s.ID) {
				// ponytail: a label two enrolled people both claim keeps the closer voice
				if o, ok := owned[s.ID]; ok && o.Confidence >= s.Sim {
					continue
				}
				id := Identification{Type: "voice", SpeakerID: s.ID, Name: e.name, Confidence: round4(s.Sim),
					Segments: s.Spans, Match: model + " (closest voice in this video; needs a second sign)"}
				if p, ok := prev[s.ID]; ok && p.Name == e.name {
					id.Match, id.VoiceMargin, id.RunnerUp, id.RunnerUpConfidence = p.Match, p.VoiceMargin, p.RunnerUp, p.RunnerUpConfidence
				}
				if v.How == "voice+context" {
					id.Match = model + "+" + v.Via
				}
				owned[s.ID] = id
			} else if p, ok := prev[s.ID]; ok && p.Name == e.name {
				why := v.Why
				if why == "" {
					why = "another speaker in this video is clearly closer to " + e.name
				}
				lost[s.ID] = why
			}
		}
		for i := range audit {
			if contains(v.Owners, audit[i].SpeakerID) {
				how := v.How
				if v.Via != "" {
					how = "voice+" + v.Via
				}
				audit[i].Decision = e.name + " (" + how + ")"
			} else if v.Why != "" && audit[i].Decision == "" {
				audit[i].Decision = e.name + " not settled: " + v.Why
			}
		}
	}
	if len(settled) == 0 {
		return ids, unids, audit, ties
	}
	var outIDs []Identification
	for _, id := range ids {
		if !settled[id.Name] {
			outIDs = append(outIDs, id)
		}
	}
	for _, sp := range speakers {
		if id, ok := owned[sp.id]; ok {
			outIDs = append(outIDs, id)
		}
	}
	var outUnids []Unidentified
	for _, u := range unids {
		if _, ok := owned[u.SpeakerID]; !(ok && u.Type == "voice") {
			outUnids = append(outUnids, u)
		}
	}
	for _, sp := range speakers {
		if why, ok := lost[sp.id]; ok {
			if _, nowOwned := owned[sp.id]; nowOwned {
				continue
			}
			p := prev[sp.id]
			outUnids = append(outUnids, Unidentified{Type: "voice", SpeakerID: sp.id, Candidate: p.Name,
				CandidateConfidence: p.Confidence, WhyUnnamed: whyOtherSpeaker,
				Description: fmt.Sprintf("possible %s (voice %.2f), not named: %s", p.Name, p.Confidence, why)})
		}
	}
	return outIDs, outUnids, audit, ties
}

// voiceTie is an enrolled person two or more speakers sound about equally like.
type voiceTie struct {
	Name, Key, Why string
	Dispute        bool // the words and the voice disagree (else: two voices tie)
	Named          bool // the clear voice already named someone; only a look can overturn it
	Labels         []string
	Spans          map[string][]SpeakerSpan
}

const whyOtherSpeaker = "other-speaker" // another speaker in the same video is them, or they can't be told apart
