// remedy.go — the inline "teach me" hint attached to every unidentified entry.
//
// When identify leaves someone unnamed, the output should tell the human how to fix
// it RIGHT THERE — the explicit "Enrollment UX … output should include the remedy
// inline" gap (README). The remedy is a static, deterministic string: it names the
// exact `becky "this is <name>" <clip>` teach command, with <clip> filled in to the
// real input file and <name> left as a literal placeholder (the human supplies the
// name — mirroring how cmd/ask/plan.go fills resolved paths but keeps user-value
// placeholders like <name> intact). One source of truth so it is unit-assertable.
package main

// remedyLine returns the inline teach-me remedy for an unidentified entry detected in
// clip. The returned string is the literal command a human runs to enroll the person,
// with the clip path filled in and <name> left as a placeholder for the human.
func remedyLine(clip string) string {
	return `not enrolled — teach me: becky "this is <name>" ` + clip
}

// attachRemedies fills the inline teach-me Remedy on every unidentified entry in the
// report, using the report's input file as the clip. It is purely additive — it never
// touches the why_unnamed reason, candidate, or description the voice/face paths set;
// it only populates the empty Remedy field. Named identifications are not touched (they
// already have a name, so no remedy applies).
func attachRemedies(report *Output) {
	clip := report.File
	for i := range report.Unidentified {
		if report.Unidentified[i].Remedy == "" {
			report.Unidentified[i].Remedy = remedyLine(clip)
		}
	}
}
