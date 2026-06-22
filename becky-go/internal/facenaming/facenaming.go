// Package facenaming is the orchestration LOGIC behind becky-name: the "who is
// this?" loop that turns an anonymous becky-cluster grouping ("Person A, seen in 41
// clips") into a named KB enrollee by walking clusters biggest-first, taking a typed
// name, and enrolling the whole cluster under it.
//
// This package is deliberately HEADLESS and deterministic so the whole decision layer
// (cluster walk, name capture, per-cluster clip selection, enroll-argv construction,
// the --names apply, dry-run) is unit-testable with NO display, NO models, and NO
// ffmpeg. The two hardware/display concerns are behind small interfaces:
//
//	imageShower — shows the representative face (local: OS viewer / inline graphics)
//	enroller    — runs the actual embedding+enroll (local: exec becky-enroll)
//
// becky-name is a thin orchestrator in the becky house style: it never re-implements
// any enroll/embedding logic — it only chooses which clips + name to hand to the
// EXISTING becky-enroll teach path. One tool clusters, one enrolls, one names.
package facenaming

import (
	"encoding/json"
	"fmt"
	"sort"
)

// DefaultEnrollCap is the default maximum number of distinct member clips to enroll
// from per cluster. More clips = a richer print but slower enrollment; the rest are
// provenance, not needed for a good print (SPEC §3d, Open Decision 3).
const DefaultEnrollCap = 5

// EnrollBinary is the name of the teach binary becky-name shells out to.
const EnrollBinary = "becky-enroll"

// Member mirrors becky-cluster's Member (cmd/cluster/main.go). Only the fields the
// naming loop needs are kept; unknown JSON fields are ignored on load.
type Member struct {
	AppearanceID string  `json:"appearance_id"`
	SourceFile   string  `json:"source_file"`
	Timestamp    float64 `json:"timestamp"`
	DetScore     float64 `json:"det_score"`
}

// Cluster mirrors becky-cluster's Cluster (cmd/cluster/main.go): one recurring
// person, a [CANDIDATE] same-person grouping, with no name until a human supplies one.
type Cluster struct {
	ClusterID           string   `json:"cluster_id"`
	Modality            string   `json:"modality"`
	SuggestedName       *string  `json:"suggested_name"`
	MemberCount         int      `json:"member_count"`
	DistinctSourceFiles int      `json:"distinct_source_files"`
	Cohesion            float64  `json:"cohesion"`
	Representative      string   `json:"representative"`
	Members             []Member `json:"members"`
}

// Clusters is the becky-cluster Output envelope. Only the fields the loop reads are
// declared; the rest of the rich becky-cluster JSON is ignored harmlessly.
type Clusters struct {
	Tool       string    `json:"tool"`
	Modality   string    `json:"modality"`
	MinCluster int       `json:"min_cluster"`
	Clusters   []Cluster `json:"clusters"`
}

// LoadClusters parses a becky-cluster Output JSON. It degrades-never-crashes: malformed
// JSON returns a typed error (no panic), and a well-formed file with no clusters is a
// valid empty result, not an error.
func LoadClusters(data []byte) (Clusters, error) {
	var c Clusters
	if err := json.Unmarshal(data, &c); err != nil {
		return Clusters{}, fmt.Errorf("malformed clusters JSON: %w", err)
	}
	return c, nil
}

// WalkOrder returns the clusters in the order they should be reviewed: biggest first
// (most members), ties broken by cluster_id, matching the order becky-cluster already
// emits (cmd/cluster/main.go sorts MemberCount desc). becky-name presents in this
// fixed, deterministic order. An optional modality filter ("face"|"voice") and a
// min-members floor narrow the set; "" / 0 mean no filter.
func WalkOrder(c Clusters, modality string, minClips int) []Cluster {
	out := make([]Cluster, 0, len(c.Clusters))
	for _, cl := range c.Clusters {
		if modality != "" && modality != "both" && cl.Modality != modality {
			continue
		}
		if minClips > 0 && cl.MemberCount < minClips {
			continue
		}
		out = append(out, cl)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MemberCount != out[j].MemberCount {
			return out[i].MemberCount > out[j].MemberCount // biggest first
		}
		return out[i].ClusterID < out[j].ClusterID
	})
	return out
}

// clipsToEnroll returns the distinct member clips to teach from for a cluster, in
// strongest-first order (members arrive sorted by DetScore desc from becky-cluster),
// deduped by SourceFile, capped at cap. A cap <= 0 means DefaultEnrollCap.
func clipsToEnroll(cl Cluster, cap int) []string {
	if cap <= 0 {
		cap = DefaultEnrollCap
	}
	seen := map[string]bool{}
	var clips []string
	// Preserve the strongest-first order becky-cluster produced (do NOT re-sort, so a
	// test's ordering is the engine's ordering); dedupe by distinct source file.
	for _, m := range cl.Members {
		if m.SourceFile == "" || seen[m.SourceFile] {
			continue
		}
		seen[m.SourceFile] = true
		clips = append(clips, m.SourceFile)
		if len(clips) >= cap {
			break
		}
	}
	// If the cluster has no members (provenance-only), fall back to the representative.
	if len(clips) == 0 && cl.Representative != "" {
		clips = []string{cl.Representative}
	}
	return clips
}

// EnrollArgs builds the per-clip `becky-enroll` argv slices for naming a cluster: one
// argv per distinct member clip (deduped, strongest-first, capped). Each argv is the
// EXISTING single-clip teach path: becky-enroll --clip <clip> --name <name> --kb <kb>
// [--device <device>]. argv[0] is the binary name; bin (if non-empty) is prepended as
// a directory so the caller can resolve a sibling binary. The result is a pure
// function of its inputs — golden-argv testable.
func EnrollArgs(cl Cluster, name, kb, device string, cap int) [][]string {
	clips := clipsToEnroll(cl, cap)
	args := make([][]string, 0, len(clips))
	for _, clip := range clips {
		a := []string{EnrollBinary, "--clip", clip, "--name", name, "--kb", kb}
		if device != "" {
			a = append(a, "--device", device)
		}
		args = append(args, a)
	}
	return args
}
