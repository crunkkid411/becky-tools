package ctlmodel

// prompt.go — the model-side glue: a compact arrangement Snapshot (the context the
// model needs without dumping the whole session), the BuildPrompt that frames the task,
// and DecodeBatch which extracts the JSON object from the model's stdout and parses it
// through ctledit.ParseBatch. All deterministic and cloud-testable; the exec that sits
// between BuildPrompt and DecodeBatch is the model boundary (execRunner.run).

import (
	"fmt"
	"strings"

	"becky-go/internal/ctledit"
	"becky-go/internal/dawmodel"
)

// Snapshot renders a compact, model-friendly summary of the arrangement: transport,
// then one line per track (id, kind, clip/note counts, mixer state, bus). It gives the
// model the track IDs and current mix it must reference, without the note data.
func Snapshot(arr *dawmodel.Arrangement) string {
	if arr == nil || len(arr.Tracks) == 0 {
		return "session: (empty — no tracks loaded)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "session: bpm=%d ppq=%d time=%d/%d", arr.BPM, ppqOf(arr), tsNum(arr), tsDen(arr))
	if arr.Genre != "" {
		fmt.Fprintf(&b, " genre=%s", arr.Genre)
	}
	if arr.Root != "" || arr.Scale != "" {
		fmt.Fprintf(&b, " key=%s%s", arr.Root, scaleSuffix(arr.Scale))
	}
	b.WriteString("\ntracks:")
	for _, t := range arr.Tracks {
		notes := 0
		for _, c := range t.Clips {
			notes += len(c.Notes)
		}
		fmt.Fprintf(&b, "\n  - id=%q kind=%s clips=%d notes=%d gain=%.2f pan=%.2f mute=%v solo=%v bus=%s",
			t.ID, t.Kind, len(t.Clips), notes, t.Strip.Gain, t.Strip.Pan, t.Strip.Mute, t.Strip.Solo, t.Strip.Bus)
	}
	if len(arr.Buses) > 0 {
		b.WriteString("\nbuses:")
		for _, bus := range arr.Buses {
			fmt.Fprintf(&b, "\n  - id=%q out=%s", bus.ID, bus.Out)
		}
	}
	return b.String()
}

// BuildPrompt frames the NL→BeckyEditBatch task for a small instruct model. The
// matching GBNF grammar (Grammar()) enforces the output shape; this prompt supplies the
// op vocabulary, the session snapshot, and the user's request.
func BuildPrompt(instruction, snapshot string) string {
	var b strings.Builder
	b.WriteString("You are becky, editing a music session. Convert the user's request into a ")
	b.WriteString("BeckyEditBatch JSON object: {\"summary\": <one short sentence>, \"edits\": [ ... ]}.\n")
	b.WriteString("Each edit is {\"op\": <one of the ops below>, ...only the fields that op needs...}.\n")
	b.WriteString("Reference tracks by their exact id from the session. Output ONLY the JSON.\n\n")
	b.WriteString("ops:\n")
	for _, line := range opVocabulary() {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\n")
	b.WriteString(snapshot)
	b.WriteString("\n\nrequest: ")
	b.WriteString(strings.TrimSpace(instruction))
	b.WriteString("\nJSON:")
	return b.String()
}

// opVocabulary is the one-line-per-op cheat sheet embedded in the prompt.
func opVocabulary() []string {
	return []string{
		ctledit.OpSetTempo + "      {bpm:int}",
		ctledit.OpTranspose + "     {track, semitones:int}",
		ctledit.OpAddNotes + "     {track, notes:[[pitch,start_beats,dur_beats,velocity],...]}",
		ctledit.OpDeleteNotes + "  {track, note_ids:[int,...]}",
		ctledit.OpMoveNotes + "    {track, note_ids:[...], d_ticks:int, d_pitch:int}",
		ctledit.OpResizeNotes + "  {track, note_ids:[...], d_dur:int}",
		ctledit.OpSetVelocity + "  {track, note_ids:[...], velocity:1-127}",
		ctledit.OpSetStep + "      {track, lane_idx:int, step:int, on:bool, step_vel:int}",
		ctledit.OpSetGain + "      {target, gain:0-2}",
		ctledit.OpSetPan + "       {target, pan:-1..1}",
		ctledit.OpMute + "          {target, muted:bool}",
		ctledit.OpSolo + "          {target, soloed:bool}",
		ctledit.OpRouteTo + "      {target, bus_id}",
		ctledit.OpAddSidechain + " {bus_id, sidechain_source}",
	}
}

// DecodeBatch extracts the first JSON object from a model's stdout (chatter before or
// after is tolerated) and parses it into a BeckyEditBatch via ctledit.ParseBatch.
func DecodeBatch(stdout string) (ctledit.BeckyEditBatch, error) {
	obj := extractFirstJSONObject(stdout)
	if obj == "" {
		return ctledit.BeckyEditBatch{}, fmt.Errorf("ctlmodel: no JSON object in model output")
	}
	return ctledit.ParseBatch([]byte(obj))
}

// extractFirstJSONObject returns the first balanced {...} run in s, respecting strings
// and escapes so a "}" inside a JSON string doesn't end the object early. Empty when
// no balanced object is found.
func extractFirstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// ─── small arrangement accessors (defensive defaults) ─────────────────────────

func ppqOf(arr *dawmodel.Arrangement) int {
	if arr.PPQ > 0 {
		return arr.PPQ
	}
	return 96
}

func tsNum(arr *dawmodel.Arrangement) int {
	if arr.Num > 0 {
		return arr.Num
	}
	return 4
}

func tsDen(arr *dawmodel.Arrangement) int {
	if arr.Den > 0 {
		return arr.Den
	}
	return 4
}

func scaleSuffix(scale string) string {
	if scale == "" {
		return ""
	}
	return " " + scale
}
