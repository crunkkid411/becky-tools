// propose.go — the autonomous "let the models decide what to build" loop.
//
// Jordan's brief: he doesn't want to hand-review scout's findings. Instead, when
// the local judge model (Qwen) thinks a video is genuinely useful, it should
// PROPOSE a concrete becky tool; a SECOND, independent model (Gemma-4) then has
// to AGREE; only if they agree does the proposal become a real build request.
//
// This is becky's "corroborate, then CONCLUDE" applied to MODEL judgment: the
// proposer alone is one opinion (a candidate); proposer + an independent judge
// agreeing is two independent signals → a stated conclusion worth acting on. The
// approved proposal is emitted as a becky-new-tool intake record (the exact shape
// `becky-new-tool --intake-file` consumes), so the existing staged build factory
// does the actual building — scout only decides WHETHER to ask it to.
//
// Everything here is deterministic and model-free: Proposer/Judge are interfaces
// with canned fakes, so the whole gate is unit-tested without a GPU. The real
// llama-server-backed implementations live in cmd/scout (model.go) and degrade to
// "no proposal" when the models aren't present — never a crash.
package scout

import "strings"

// Proposal is a model's pitch for a buildable becky tool, derived from a video.
// Its fields line up with the becky-new-tool intake shape (see Intake below).
type Proposal struct {
	WorthBuilding bool   `json:"worth_building"` // the proposer's own gate
	Slug          string `json:"slug"`           // becky-<kebab>
	Capability    string `json:"capability"`     // one-sentence "what it does"
	InputKind     string `json:"input_kind"`     // video|audio|image|url|json|text
	OutputKind    string `json:"output_kind"`    // json|csv|text
	Kind          string `json:"kind"`           // improve | extend
	Why           string `json:"why"`            // why this video justifies building it
}

// Proposer turns a useful video (Item) into a build Proposal. The real impl asks
// the local Qwen model; the fake is canned.
//
// Local-agent contract (Qwen, --temp 0): given the video + becky's catalog, reply
// with strict JSON {worth_building, slug, capability, input_kind, output_kind,
// kind, why}. worth_building=false means "not worth a tool" (most videos).
type Proposer interface {
	Propose(it Item) (Proposal, error)
}

// JudgeVote is one independent model's verdict on a Proposal.
type JudgeVote struct {
	Judge      string  `json:"judge"`
	Agree      bool    `json:"agree"`
	Why        string  `json:"why"`
	Confidence float64 `json:"confidence,omitempty"`
}

// Judge is an independent second opinion on a Proposal. The real impl asks a
// DIFFERENT model than the proposer (Gemma-4) so agreement is real corroboration.
//
// Local-agent contract (Gemma-4, --temp 0): given the proposal + the video,
// reply with strict JSON {agree, why, confidence}.
type Judge interface {
	Name() string
	Vote(p Proposal, it Item) (JudgeVote, error)
}

// Decision is the full record of one autonomous gate: the proposal, every judge's
// vote, and whether it was APPROVED (proposer proposed AND ≥minAgree judges
// agreed — i.e. ≥2 independent models concur).
type Decision struct {
	Video    Video       `json:"video"`
	Proposal Proposal    `json:"proposal"`
	Votes    []JudgeVote `json:"votes"`
	Agrees   int         `json:"agrees"`
	Approved bool        `json:"approved"`
	Reason   string      `json:"reason"`
}

// Propose runs the autonomous gate over the given items (scout's relevant +
// candidate + useful videos). For each: the proposer pitches; if it judges the
// video worth building, every judge votes; the proposal is APPROVED when at least
// minAgree judges agree. Returns one Decision per item the proposer engaged with
// (items the proposer skips produce no Decision — no flood).
//
// A proposer/judge error on one item is swallowed (that item just doesn't get a
// decision); the loop never crashes. minAgree<1 is treated as 1 (a lone proposer
// is never enough — corroboration is mandatory).
func Propose(items []Item, proposer Proposer, judges []Judge, minAgree int) []Decision {
	if proposer == nil || len(judges) == 0 {
		return nil
	}
	if minAgree < 1 {
		minAgree = 1
	}
	var out []Decision
	for _, it := range items {
		p, err := proposer.Propose(it)
		if err != nil || !p.WorthBuilding {
			continue
		}
		if p.Slug == "" || p.Capability == "" {
			continue // a proposal with no name/capability isn't actionable
		}
		d := Decision{Video: it.Video, Proposal: p}
		for _, j := range judges {
			v, err := j.Vote(p, it)
			if err != nil {
				continue
			}
			if v.Judge == "" {
				v.Judge = j.Name()
			}
			d.Votes = append(d.Votes, v)
			if v.Agree {
				d.Agrees++
			}
		}
		d.Approved = d.Agrees >= minAgree
		d.Reason = decisionReason(d, minAgree)
		out = append(out, d)
	}
	return out
}

// decisionReason states the gate outcome in plain language.
func decisionReason(d Decision, minAgree int) string {
	if d.Approved {
		return "APPROVED — the proposer and " + plural(d.Agrees, "judge") +
			" agree this is worth building (≥2 independent models concur)."
	}
	return "held back — proposed, but only " + plural(d.Agrees, "judge") +
		" agreed (need " + itoa(minAgree) + "). Not built; left for review."
}

// Intake is the becky-new-tool intake record. Its JSON shape matches what
// `becky-new-tool --intake-file` consumes (cmd/ask/pitch.go's PitchRecord), so an
// approved Decision feeds straight into the existing build factory.
type Intake struct {
	Slug             string   `json:"slug"`
	Capability       string   `json:"capability"`
	InputKind        string   `json:"input_kind"`
	OutputKind       string   `json:"output_kind"`
	Constraints      []string `json:"constraints"`
	DefinitionOfDone []string `json:"definition_of_done"`
	CapturedAt       string   `json:"captured_at"`
	NormalizedBy     string   `json:"normalized_by"`
	// Provenance so a human (or the factory log) can trace where this came from.
	Source string `json:"source,omitempty"`
}

// ToIntake converts an approved Decision into a becky-new-tool intake record.
// capturedAt is injected (the caller passes a date) so the result is testable.
func (d Decision) ToIntake(capturedAt string) Intake {
	bare := strings.TrimPrefix(d.Proposal.Slug, "becky-")
	in := d.Proposal.InputKind
	if in == "" {
		in = "text"
	}
	out := d.Proposal.OutputKind
	if out == "" {
		out = "json"
	}
	return Intake{
		Slug:        d.Proposal.Slug,
		Capability:  d.Proposal.Capability,
		InputKind:   in,
		OutputKind:  out,
		Constraints: []string{"offline"},
		DefinitionOfDone: []string{
			"go build ./cmd/" + bare + " passes",
			"go vet ./cmd/" + bare + " passes",
			"go test ./cmd/" + bare + "/... passes",
			"runs on a test input and exits 0",
			"output is valid JSON on stdout",
		},
		CapturedAt:   capturedAt,
		NormalizedBy: "becky-scout (qwen proposed, judges concurred)",
		Source:       d.Video.URL,
	}
}

// plural formats "1 judge" / "2 judges".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}

// itoa is a tiny int→string (avoids importing strconv just for this).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
