// apply.go — the headless apply layer: turn a {cluster_id -> name} map into enroll
// calls through the enroller seam, recording outcomes and skips. This is the
// non-interactive heart of becky-name that runs in --names mode and in CI; the TUI
// (cmd/name) drives the same logic one card at a time.
package facenaming

import (
	"fmt"
	"sort"
	"strings"
)

// imageShower shows a representative face/clip beside the review card. The local
// implementation opens the OS image viewer (or inline terminal graphics); tests use a
// record-only fake. Defined here so the cluster walk is testable headless.
type imageShower interface {
	Show(path string) error
}

// ImageShower is the exported alias for callers (cmd/name) that supply a real shower.
type ImageShower = imageShower

// EnrollOutcome is the result of teaching one cluster under one name: which clips were
// enrolled, which were skipped (with a recorded reason — never fabricated), and the KB.
type EnrollOutcome struct {
	ClusterID string   `json:"cluster_id"`
	Name      string   `json:"name"`
	KB        string   `json:"kb"`
	Enrolled  []string `json:"enrolled"`          // clips that enrolled OK
	Skipped   []string `json:"skipped,omitempty"` // clips that did not enroll
	Reasons   []string `json:"reasons,omitempty"` // one reason per skipped clip (aligned)
}

// enroller runs the actual face/voice embedding + KB enroll for one clip under one
// name. The local implementation shells out to becky-enroll; tests use a fake that
// records its calls. This is the model/hardware seam (CLAUDE.md §4).
type enroller interface {
	Enroll(clip, name, kb string) error
}

// Enroller is the exported alias for callers that supply a real enroller.
type Enroller = enroller

// ApplyResult is the full record of an apply pass: the per-cluster outcomes plus the
// skip log (clusters intentionally left unnamed). Deterministic + serializable so a
// --out audit file can record exactly what happened.
type ApplyResult struct {
	KB        string          `json:"kb"`
	Outcomes  []EnrollOutcome `json:"outcomes"`
	SkippedID []string        `json:"skipped_clusters,omitempty"` // clusters left unnamed (blank name)
}

// ApplyNames teaches each named cluster under its supplied name by calling the enroller
// once per distinct member clip (deduped, strongest-first, capped). It is the headless
// core used by --names mode and the TUI's per-card commit.
//
//   - names maps cluster_id -> chosen name. A blank/whitespace name (or a cluster_id
//     absent from the map) is a SKIP: no enroll calls, the id is recorded in
//     SkippedID, never a name invented (SPEC §2a: "A skip is recorded, not a name
//     invented").
//   - device is passed through to the enroller's caller via EnrollArgs in real mode;
//     the enroller interface itself only needs clip/name/kb.
//   - A clip that fails to enroll is recorded as a skip-with-reason and the loop
//     continues to the next clip/cluster (degrade-never-crash; mirrors enroll's
//     per-person skip discipline).
//
// Clusters are processed in WalkOrder (biggest-first) for deterministic output.
func ApplyNames(c Clusters, names map[string]string, en enroller, modality string, minClips, cap int) ApplyResult {
	res := ApplyResult{KB: ""}
	for _, cl := range WalkOrder(c, modality, minClips) {
		name := strings.TrimSpace(names[cl.ClusterID])
		if name == "" {
			res.SkippedID = append(res.SkippedID, cl.ClusterID)
			continue
		}
		out := enrollCluster(cl, name, "", en, cap)
		res.Outcomes = append(res.Outcomes, out)
	}
	return res
}

// EnrollCluster teaches one cluster under one name through the enroller, returning the
// outcome (enrolled + skipped-with-reason). Exported so the TUI can commit a single
// card. kb is recorded on the outcome for the audit trail.
func EnrollCluster(cl Cluster, name, kb string, en enroller, cap int) EnrollOutcome {
	return enrollCluster(cl, name, kb, en, cap)
}

func enrollCluster(cl Cluster, name, kb string, en enroller, cap int) EnrollOutcome {
	out := EnrollOutcome{ClusterID: cl.ClusterID, Name: name, KB: kb}
	for _, clip := range clipsToEnroll(cl, cap) {
		if err := en.Enroll(clip, name, kb); err != nil {
			out.Skipped = append(out.Skipped, clip)
			out.Reasons = append(out.Reasons, err.Error())
			continue
		}
		out.Enrolled = append(out.Enrolled, clip)
	}
	return out
}

// DryRunPlan returns, for each named cluster in WalkOrder, the exact enroll argv the
// real run WOULD execute — the offline, measurable PROOF the cloud ships (SPEC §5,
// CLAUDE.md §2 provable handoff). It executes nothing. Clusters with no name in the
// map are omitted (nothing would be enrolled for them).
func DryRunPlan(c Clusters, names map[string]string, kb, device string, modality string, minClips, cap int) [][]string {
	var plan [][]string
	for _, cl := range WalkOrder(c, modality, minClips) {
		name := strings.TrimSpace(names[cl.ClusterID])
		if name == "" {
			continue
		}
		plan = append(plan, EnrollArgs(cl, name, kb, device, cap)...)
	}
	return plan
}

// Summary renders a one-line, human-readable confirmation for an enroll outcome,
// matching SPEC §2a ("Enrolled Braxton from 41 clips → kb-final"; or the honest
// partial). Plain words, concise, per FORENSIC-OUTPUT-PHILOSOPHY + ACCESSIBILITY.
func (o EnrollOutcome) Summary() string {
	switch {
	case len(o.Enrolled) == 0 && len(o.Skipped) == 0:
		return fmt.Sprintf("%s: nothing to enroll", o.Name)
	case len(o.Skipped) == 0:
		return fmt.Sprintf("Enrolled %s from %d clip(s) → %s", o.Name, len(o.Enrolled), kbLabel(o.KB))
	case len(o.Enrolled) == 0:
		return fmt.Sprintf("%s: enrolled 0 clip(s), %d skipped (%s)", o.Name, len(o.Skipped), firstReason(o.Reasons))
	default:
		return fmt.Sprintf("Enrolled %s: %d clip(s) ✓, %d skipped (%s)", o.Name, len(o.Enrolled), len(o.Skipped), firstReason(o.Reasons))
	}
}

func kbLabel(kb string) string {
	if kb == "" {
		return "KB"
	}
	return kb
}

func firstReason(reasons []string) string {
	if len(reasons) == 0 {
		return "no reason recorded"
	}
	return reasons[0]
}

// ParseNamesMap normalizes a raw {cluster_id: name} JSON object map into the map the
// apply path expects, trimming whitespace from names. Used by --names file mode.
func ParseNamesMap(raw map[string]string) map[string]string {
	m := make(map[string]string, len(raw))
	for id, n := range raw {
		m[strings.TrimSpace(id)] = strings.TrimSpace(n)
	}
	return m
}

// ModalitySummary returns a stable, human-readable count of clusters per modality in
// the loaded set (e.g. "2 face, 1 voice") for the no-TTY parsed-summary output.
func ModalitySummary(c Clusters) string {
	counts := map[string]int{}
	for _, cl := range c.Clusters {
		counts[cl.Modality]++
	}
	mods := make([]string, 0, len(counts))
	for m := range counts {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	parts := make([]string, 0, len(mods))
	for _, m := range mods {
		parts = append(parts, fmt.Sprintf("%d %s", counts[m], m))
	}
	if len(parts) == 0 {
		return "0 clusters"
	}
	return strings.Join(parts, ", ")
}
