// forensic.go — routes a FORENSIC question ("who is in this?", "is Shelby on screen?") on a
// dropped file into becky's self-regulating engine (internal/forensicrun -> orchestrate) and
// returns the FINAL corroborated answer, instead of staging a single becky-identify command the
// agent would then have to chain. This is the becky-ask half of "wire orchestrate into the entry
// tools": one plain-English call gets the protocol-enforced answer (a name only when corroborated,
// an on-screen interval only where a model watched it), maybes held — no flags, no chaining.
//
// Scope: the SCRIPTABLE single-shot path (the forensic AGENT's entry; SPEC-BECKY-VOICE.md Step 4).
// The interactive colored TUI is intentionally left untouched so a long model run never freezes it
// (ACCESSIBILITY.md) — a human there still gets the show-the-command confirm flow.
package main

import (
	"context"
	"fmt"
	"strings"

	"becky-go/internal/forensicrun"
)

// forensicKind is what a forensic question is asking for.
type forensicKind int

const (
	fNone     forensicKind = iota // not a forensic question
	fNaming                       // "who is in this / identify the people / who is speaking"
	fPresence                     // "is <subject> on screen / does <subject> appear"
)

// forensicResolveAsk is the seam the single-shot forensic path calls — the real runtime by
// default, swapped in tests so the routing + formatting is verified without models/binaries.
var forensicResolveAsk = forensicrun.RunAndReport

// forensicSingleShot answers a forensic question about a dropped file with the corroborated
// resolution. It returns ok=false (so normal routing proceeds) when the question is not forensic
// or there is no target file. Degrade-never-crash: an absent model/tool yields an honest answer.
func forensicSingleShot(ctx context.Context, q string, t Target) (singleShotResult, bool) {
	if !t.HasTarget() {
		return singleShotResult{}, false
	}
	kind, subject := forensicIntent(q)
	if kind == fNone {
		return singleShotResult{}, false
	}

	// kb "" => forensicrun resolves it from the BECKY_KB env or the kb-final convention.
	rep := forensicResolveAsk(ctx, t.Primary(), subject, "", 0, nil)
	res := singleShotResult{
		Question: q,
		Kind:     "forensic",
		Source:   "becky-orchestrate",
		Degraded: len(rep.Degraded) > 0,
		Answer:   formatForensicAnswer(rep, kind),
		exitCode: 0,
	}
	if len(rep.Degraded) > 0 {
		res.Error = strings.Join(rep.Degraded, "; ")
	}
	return res, true
}

// forensicIntent classifies a question as a naming or presence request and, for presence, pulls
// the subject. Deterministic + unit-tested; no model. Unrecognized -> fNone (normal routing).
func forensicIntent(q string) (forensicKind, string) {
	lower := strings.ToLower(strings.TrimSpace(q))

	// Presence FIRST (it carries a subject): "is <X> on screen", "does <X> appear", etc.
	if subj := presenceSubject(q, lower); subj != "" {
		return fPresence, subj
	}
	for _, p := range []string{
		"who is in", "who's in", "who is this", "who are the", "who is speaking",
		"who's speaking", "identify who", "identify the speaker", "identify the people",
		"name the people", "name everyone", "who appears", "who's on screen", "who is on screen",
	} {
		if strings.Contains(lower, p) {
			return fNaming, ""
		}
	}
	return fNone, ""
}

// presenceSubject extracts the subject of an explicit on-screen/appearance question, preserving
// the subject's original case (it slices the original string at positions found in the lowercased
// one). Only unambiguous presence phrasings are matched, to avoid hijacking ordinary questions.
func presenceSubject(orig, lower string) string {
	type pat struct{ pre, post string }
	for _, p := range []pat{
		{"is ", " on screen"}, {"is ", " on-screen"}, {"is ", " on the screen"},
		{"is ", " present"}, {"is ", " visible"}, {"is ", " in the video"}, {"is ", " in this video"},
		{"does ", " appear"}, {"when does ", " appear"}, {"when is ", " on screen"},
	} {
		if subj, ok := between(orig, lower, p.pre, p.post); ok {
			if s := cleanSubject(subj); s != "" {
				return s
			}
		}
	}
	return ""
}

// between returns the substring of orig that sits between pre and post (both located in the
// lowercased lower, so the returned slice keeps orig's case). post=="" returns the tail after pre.
func between(orig, lower, pre, post string) (string, bool) {
	i := strings.Index(lower, pre)
	if i < 0 {
		return "", false
	}
	start := i + len(pre)
	if post == "" {
		return strings.TrimSpace(orig[start:]), true
	}
	j := strings.Index(lower[start:], post)
	if j < 0 {
		return "", false
	}
	return strings.TrimSpace(orig[start : start+j]), true
}

// cleanSubject strips a leading article and trailing punctuation from an extracted subject.
func cleanSubject(s string) string {
	s = strings.TrimSpace(strings.TrimRight(s, "?.!,"))
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(strings.ToLower(s), art) {
			s = strings.TrimSpace(s[len(art):])
		}
	}
	return s
}

// formatForensicAnswer renders the corroborated report as tight, plain, honest text (Jordan's
// reading limits): stated facts first, then a one-line note that maybes are held. It NEVER dumps
// the held candidates as if they were conclusions.
func formatForensicAnswer(rep forensicrun.ForensicReport, kind forensicKind) string {
	var b strings.Builder
	switch kind {
	case fPresence:
		if len(rep.OnScreen) == 0 {
			fmt.Fprintf(&b, "I can't confirm %s on screen — no model watch corroborated it.", rep.Subject)
		} else {
			b.WriteString("On screen (corroborated by a model watch):")
			for _, v := range rep.OnScreen {
				b.WriteString("\n  - " + presenceLabel(v.Claim))
			}
		}
	default: // fNaming
		if len(rep.Names) == 0 {
			b.WriteString("No one could be named with corroboration (a name needs 2 independent agreeing signals).")
		} else {
			b.WriteString("Named (corroborated):")
			for _, v := range rep.Names {
				b.WriteString("\n  - " + personLabel(v.Claim))
			}
		}
	}
	if n := len(rep.Held); n > 0 {
		fmt.Fprintf(&b, "\n(%d candidate(s) held — not enough corroboration to state.)", n)
	}
	if len(rep.Degraded) > 0 {
		b.WriteString("\nNote: " + strings.Join(rep.Degraded, "; "))
	}
	return b.String()
}

// personLabel turns "person=Shelby" into "Shelby"; presenceLabel turns "onscreen=cat@[12.0-18.0]"
// into "cat @ 12.0-18.0s". Both fall back to the raw claim if the shape is unexpected.
func personLabel(claim string) string {
	return strings.TrimPrefix(claim, "person=")
}

func presenceLabel(claim string) string {
	s := strings.TrimPrefix(claim, "onscreen=")
	if at := strings.IndexByte(s, '@'); at >= 0 {
		subj := s[:at]
		win := strings.Trim(s[at+1:], "[]")
		return fmt.Sprintf("%s @ %ss", subj, win)
	}
	return s
}
