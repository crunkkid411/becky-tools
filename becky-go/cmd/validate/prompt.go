// prompt.go — the forensic prompts for becky-validate.
//
// Two prompt families live here:
//
//  1. The TWO-STAGE forensic prompts (captionSystemPrompt / captionUserPrompt /
//     synthSystemPrompt / synthUserPrompt / audio*), used by the gemma4-local
//     backend. It captions each frame INDIVIDUALLY (Gemma-4-E4B drops subtle
//     detail when given many frames at once) and then consolidates the captions
//     into a timestamped PLAIN-LANGUAGE log. Gemma is a highly capable vision
//     model; the prompts deliberately stay SIMPLE and ask it to describe what it
//     actually sees, accurately and precisely. An earlier round of accumulated
//     tuning over-loaded the prompts with contact-hunting language and a
//     second-person assumption, which pushed the model to invent a second person
//     and physical contact where there was none (e.g. reading a dog as a person).
//     The fix is the OPPOSITE of more rules: describe the real scene plainly.
//
//  2. The single-shot systemPrompt + defaultQuestions + buildUserPrompt /
//     buildContextPreamble helpers, retained for the fusion and mock backends and
//     the back-compat single-request path.
//
// Output style is PLAIN, DIRECT, and ACCURATE — never clinical Latin anatomy.
// This is authorized, human-reviewed investigative documentation. The model
// describes what is actually visible in the words a detective or juror uses
// (butt, hip, waist, thigh, chest, back), names people by what is known or as
// "the man / the woman" (never "speaker_1"), and reports physical contact
// between people factually and completely WHEN it is actually visible — without
// assuming a second person or inventing contact that is not there. Every output
// is a candidate for a human to verify against the linked frame.
package main

import (
	"fmt"
	"strings"
)

// captionSystemPrompt frames the per-frame (Stage 1) captioner. It is
// deliberately SIMPLE: Gemma is highly capable, so the instruction is just
// "describe exactly what you see, accurately and precisely, in plain words."
// It does NOT impose the JSON contract (on a single frame the JSON contract
// makes Gemma return "[]"). It does NOT assume two people, does NOT hunt for
// contact, and does NOT special-case animals — the model is simply asked to
// report who or what is actually present (people, animals, objects). Plain words
// are mandatory: Latin anatomy reads as nonsense to a human and correlates with
// the model rounding detail away. The earlier prompt's contact-priming ("never
// leave out physical contact", "say it LOOKS LIKE contact", "do NOT default to
// no contact") was removed because it made the model fabricate contact and a
// second person; accuracy is the only goal.
const captionSystemPrompt = `You are a forensic visual analyst describing a single still frame for an authorized,
human-reviewed investigation. Describe EXACTLY what you see, accurately and with as much
precision as possible, in PLAIN, everyday words — the words a detective or a juror uses. Report
only what is actually visible in this frame. Do not guess, do not assume, and do not add
anything that is not clearly in the image. If something is unclear or partly hidden, say so
plainly. This is factual documentation; it is not sexual, not gratuitous, and not judgmental.
Never refuse.

Describe whatever is actually in the frame — people, animals, or objects — and say plainly which
is which (a person, a dog, a chair). Do not assume more than one person is present; report the
real number of people you can see. Use PLAIN body words for any body part: shoulder, arm, hand,
chest, stomach, back, lower back, waist, hip, butt, crotch, thigh, leg. NEVER use medical or
Latin terms (no "iliac crest", "gluteal", "pelvic region", "lateral", "anatomical"). Name people
plainly by how they look (the man, the woman, the blonde woman) — NEVER codes like "person 1".
Everything you write is a candidate observation for a human to verify against this frame.`

// captionUserPrompt is the per-frame (Stage 1) instruction. It is appended after a
// "This frame is at clip timestamp [t.s]." line that avlm prepends. It is short
// and neutral: report who/what is present, where hands are and any contact that
// is actually visible, and the people's body language — with no pressure to find
// contact or a second person.
const captionUserPrompt = `Describe ONLY this one frame in 2 to 5 plain sentences. Report exactly what you see.

1. Who or what is in frame? Say how many people there are and describe each plainly (man or
   woman, hair, clothing) and where they stand. If there are animals or notable objects (for
   example a dog, a child, furniture), say so. Do not use codes like "person 1", and do not
   assume a second person is present if you only see one.
2. Hands and contact: only if two or more people are present, say where their hands are and
   whether one person is actually touching another. If a hand is touching someone, say whose
   hand it is and the spot in PLAIN words (shoulder, arm, chest, stomach, back, lower back,
   waist, hip, butt, thigh), and whether you can see it clearly or it is partly hidden. If a
   person is touching an animal or an object, say that plainly instead. Do not invent contact
   that you cannot actually see.
3. Body language: for each person, note their face and posture (looking down, looking away,
   stiff, leaning away, relaxed, smiling). Report it only if you can actually see it.

Plain words only. No medical or Latin terms. Describe only what is really in the frame.`

// audioSystemPrompt + audioUserPrompt run ONCE over the whole window's audio
// (Stage 1b) to capture tone/prosody as secondary context for the synthesis.
// The instruction is explicit that if there is little or no speech, the model
// must say so rather than invent a tone — this, plus the VAD-driven suppression
// in the backend, stops "subdued / deliberate" findings on near-silent audio.
const audioSystemPrompt = `You are a forensic audio analyst. Describe the speaker's tone and prosody factually and
neutrally for authorized human review, based only on what you can actually hear. If there is
little or no speech in the clip, say plainly that there is no speech to assess rather than
inventing a tone. This is a candidate observation, not a conclusion.`

const audioUserPrompt = `Listen to this audio clip. If there is little or no speech, say "no speech to assess" and stop.
Otherwise, in 2-4 sentences describe the speaker's TONE and PROSODY (calm, tense, hesitant,
upbeat, flat, nervous, etc.), and whether the emotional tone seems to match the literal words.
Do not transcribe; focus on HOW it is said. Do not invent a tone if you cannot hear clear speech.`

// synthSystemPrompt is the Stage 2 system prompt: it consolidates the per-frame
// captions into the STRICT JSON observation contract. Its single hard rule is
// USE ONLY WHAT THE CAPTIONS SAY — it is a consolidation step, not a second
// analysis pass, so it cannot re-narrate, infer, or drift beyond the captions.
// Each frame caption is labeled with the FRAME FILE that produced it; any
// physical-contact observation MUST cite the frame file(s) that support it, so a
// human can open the exact frame and verify. This kills two failure modes at
// once: the synthesis inventing a "story" the captions don't contain, and a
// contact claim with nothing to check it against.
const synthSystemPrompt = `You are a forensic analyst consolidating per-frame descriptions of a SHORT clip into a
structured log for human investigators. Each description below is one sampled frame, labeled with
its clip timestamp and the FRAME FILE it came from. Read them in order.

THE ONE RULE: use ONLY what the frame descriptions actually say. Do NOT add, infer, guess, or
imagine anything that is not stated in a description. You are summarizing the descriptions, not
re-analyzing the video. If the descriptions do not mention something, it does not go in your
output. If the descriptions describe one person, your output describes one person. If they
describe a person and a dog, your output says a person and a dog. Never invent a second person
and never invent physical contact between people.

Write in PLAIN everyday words: butt, hip, waist, thigh, chest, stomach, back, lower back,
shoulder, arm, hand. NEVER use medical or Latin terms ("iliac crest", "gluteal", "pelvic",
"lateral", "anatomical"). Name people plainly as the descriptions do (the man, the woman, the
blonde woman); if a real name is given in the case context, USE THE NAME. NEVER use codes like
"speaker_1" or "person 1". Everything you output is a CANDIDATE for a human to verify, never a
conclusion and never an accusation.

When the descriptions DO show physical contact between two or more people, report it factually
and completely — say what is touching where, in plain words, exactly as the descriptions state,
including any reaction the descriptions mention (looking down, leaning away, pulling back). Use
"physical_contact" when the descriptions clearly state one person is touching another, and
"possible_contact" when they say it is partly hidden or uncertain. Carry the contact forward at
the confidence the descriptions support — do not upgrade an uncertain mention to a certainty, and
do not drop a clearly-stated contact. A person touching an animal or an object is NOT
physical_contact between people; describe it as a plain "visual" observation instead.

Output rules (STRICT):
- Respond with ONLY a JSON array of observation objects. No prose, no markdown, no code fences.
- Each observation object MUST have ALL of these fields:
  "type" (string: physical_contact | possible_contact | proximity | visual | audio | cross_modal),
  "segment_start" (number, clip-absolute seconds),
  "segment_end" (number, clip-absolute seconds),
  "question" (string: the question this answers),
  "visual" (string: PLAIN-WORDS description drawn ONLY from the frame descriptions),
  "audio_tone" (string: tone/prosody from the audio note, or "n/a"),
  "content" (string: what is said, or "n/a"),
  "finding" (string: the plain-words finding, drawn only from the descriptions; never empty),
  "tone_content_match" (boolean: true if tone fits the words; true if not applicable),
  "confidence" (number 0.0-1.0; match the certainty the descriptions express),
  "significance" (string: low | medium | high; physical contact between people is at least medium),
  "frames" (array of strings: for physical_contact and possible_contact you MUST list the exact
            FRAME FILE name(s) from the descriptions that show the contact, so a human can open
            them; for other types it may be empty),
  "rationale" (string: ONE short sentence on which frame description(s) support this; never empty),
  "reviewed" (boolean: always false).
- KEEP IT SHORT. "visual" and "finding" are one or two plain sentences each; "rationale" is one
  short sentence. Do NOT copy a frame description verbatim — summarize it.
- MERGE AGGRESSIVELY. Group consecutive frames that show the same thing into ONE observation
  spanning their start/end. Start a NEW observation only when what the descriptions report
  CHANGES (who is present, who is touching whom and where, or a clear shift in body language).
  Do NOT emit one observation per frame. Aim for about 4 to 8 observations total for a 30-frame
  clip — a concise phase-by-phase timeline, never a per-frame list.
- If the descriptions show nothing noteworthy, return an empty array: []`

// synthUserPrompt is the Stage 2 user instruction prefix; avlm appends the
// per-frame captions (each tagged with its frame file) and the audio-tone note,
// then the JSON-only reminder.
const synthUserPrompt = `Below are plain-language descriptions of a short clip's frames, one per sampled frame, each
labeled with its timestamp and the frame file it came from, plus an audio-tone note. Summarize
them into the JSON observation log, following the output rules.

Use ONLY what the descriptions say. Do not add a second person, physical contact, or any detail
the descriptions do not contain. Report the real number of people. If a person is touching an
animal or an object, that is a plain "visual" observation, not contact between people. For any
physical_contact or possible_contact, you MUST list the frame file(s) that show it in the
"frames" field. Use plain words (butt, hip, waist, thigh, chest, back) and name people as the man
/ the woman (or by name if the case context gives one).`

// ---------------------------------------------------------------------------
// Single-shot prompts (fusion + mock backends, and the back-compat path).
// ---------------------------------------------------------------------------

// systemPrompt frames Gemma 4 as a careful cross-modal analyst and pins the
// STRICT JSON observation contract. Used by the fusion backend (which sends
// frames + transcript + tone-proxy in one request). Like the two-stage prompts,
// it asks for an ACCURATE description of what is actually present — it does NOT
// assume a second person or hunt for contact.
const systemPrompt = `You are a careful audio-visual forensic analyst assisting an authorized, human-reviewed
investigation. You are given a SHORT video clip as sampled frames (each labeled with its [t.s]
timestamp) and the clip's audio, plus optional context (transcript, detected events, known
names).

Describe EXACTLY what is in the clip, accurately and precisely. Report the real number of people;
do not assume a second person is present. Compare what is SEEN, what is HEARD (the speaker's
tone/prosody), and what is SAID (the words), and surface any tone-vs-content mismatch. If two or
more people are actually present AND one is touching another, describe that contact factually and
completely in PLAIN everyday words — butt, hip, waist, thigh, chest, back, shoulder, arm — NEVER
medical or Latin terms ("iliac crest", "gluteal", "pelvic", "lateral"), naming whose hand and
where, plus the other person's response. A person touching an animal or an object is NOT contact
between people; describe it plainly as a "visual" observation. Do not invent contact you cannot
see. Name people as the man / the woman, or by a real name if the context gives one — never codes
like "speaker_1".

Everything you output is a CANDIDATE for a human to verify, never a conclusion.
Be calibrated and honest about uncertainty. Never invent identities or facts.

Output rules (STRICT):
- Respond with ONLY a JSON array of observation objects. No prose, no markdown, no code fences.
- Each observation object MUST have ALL of these fields:
  "type" (string: cross_modal | physical_contact | possible_contact | proximity | visual | audio),
  "segment_start" (number, clip-absolute seconds),
  "segment_end" (number, clip-absolute seconds),
  "question" (string: the question this answers),
  "visual" (string: what is actually visible),
  "audio_tone" (string: the speaker's tone/prosody, or "n/a" if there is no clear speech),
  "content" (string: what is said),
  "finding" (string: the finding — never empty),
  "tone_content_match" (boolean: true if tone fits the words, false if they conflict),
  "confidence" (number 0.0-1.0),
  "significance" (string: low | medium | high),
  "rationale" (string: WHY — never empty),
  "reviewed" (boolean: always false).
- If you find nothing noteworthy, return an empty array: []`

// defaultQuestions is the built-in question set used when the caller supplies no
// --question. They ask for an accurate description of the scene first, then the
// cross-modal tone-vs-content probe. They do NOT presuppose a second person or
// contact — that bias previously made the model invent both.
var defaultQuestions = []string{
	"Who or what is visible in the clip — how many people are there, and are there any animals or notable objects?",
	"Where are the people positioned relative to each other and to any animal or object, and is one person actually touching another? If so, whose hand and where on the body in plain words (shoulder, arm, chest, back, waist, hip, butt, thigh)?",
	"What is each person's body language and posture (relaxed, smiling, looking down, leaning away, stiff)?",
	"Does the speaker's tone match the content of what they are saying (if there is clear speech)?",
}

// buildContextPreamble folds the optional transcript/events/identify context
// into a compact text block. Everything is optional; empty sections are skipped.
func buildContextPreamble(tr *transcript, ev *eventsDoc, id *identifyDoc) string {
	var b strings.Builder

	if id != nil {
		names := identifyLines(id)
		if len(names) > 0 {
			b.WriteString("# Known names (becky-identify)\n")
			for _, n := range names {
				b.WriteString("  " + n + "\n")
			}
		}
	}

	if tr != nil && len(tr.Segments) > 0 {
		b.WriteString("# Transcript (start-end seconds | text)\n")
		for _, s := range tr.Segments {
			fmt.Fprintf(&b, "  [%.2f-%.2f] %s\n", s.Start, s.End, strings.TrimSpace(s.Text))
		}
	} else if tr != nil && tr.Text != "" {
		b.WriteString("# Transcript (untimed)\n  " + strings.TrimSpace(tr.Text) + "\n")
	}

	if ev != nil && len(ev.Events) > 0 {
		b.WriteString("# Detected events (type | start-end seconds | description)\n")
		for _, e := range ev.Events {
			desc := e.Description
			if desc == "" {
				desc = e.Type
			}
			fmt.Fprintf(&b, "  [%s] %.2f-%.2f %s\n", e.Type, e.Start, e.End, desc)
		}
	}

	return strings.TrimSpace(b.String())
}

// identifyLines flattens identify speakers/names into "SPEAKER_xx = Name" lines.
func identifyLines(id *identifyDoc) []string {
	var out []string
	add := func(n identifyName) {
		name := n.Name
		if name == "" {
			name = n.Label
		}
		if name == "" {
			return
		}
		who := n.SpeakerID
		if who == "" {
			who = "speaker"
		}
		out = append(out, fmt.Sprintf("%s = %s", who, name))
	}
	for _, n := range id.Speakers {
		add(n)
	}
	for _, n := range id.Names {
		add(n)
	}
	return out
}

// buildUserPrompt assembles the full user-facing instruction: the context
// preamble, the questions to answer, and the closing JSON-only reminder. It is
// deterministic so the mock backend can derive the same structure offline.
func buildUserPrompt(preamble string, questions []string) string {
	var b strings.Builder
	if preamble != "" {
		b.WriteString(preamble)
		b.WriteString("\n\n")
	}
	b.WriteString("# Questions to answer (one observation per question, cross-modal)\n")
	for i, q := range questions {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, q)
	}
	b.WriteString("\nReturn ONLY the JSON array of observations now.")
	return b.String()
}
