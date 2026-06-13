// becky-cluster — group RECURRING UNKNOWN people across the corpus from the
// embeddings becky already produces, so an un-enrolled stranger becomes
// "Person A appears in N clips" BEFORE anyone names them.
//
//	becky-cluster <clip|dir|glob ...> [options]    # embed raw clips, then cluster
//	becky-cluster --db forensic.db                 # cluster stored appearance embeddings
//	becky-cluster --identify-glob "**/identify.json"  # harvest provenance from identify runs
//
// Per SPEC-PERSON-CLUSTERING: Chinese Whispers (graph) for 512-d ArcFace faces,
// agglomerative (cosine stop-threshold) for 192-d CAM++ voices. Thresholds are
// anchored to becky's measured margins (face ~0.50, voice ~0.65) and are
// precision-leaning (a wrong MERGE of two strangers is the dangerous error).
//
// VOICE clusters ship first (no dependency on the face-rotation fix). FACE
// clustering needs the F1 rotation fix + enrolled-quality frames and degrades
// gracefully here (a clip with no detectable face contributes nothing).
//
// This supports the forensic philosophy directly: a recurring face/voice across N
// clips is a CORROBORATED "Person A" — stated with member count, distinct files,
// and cohesion — not a pile of singletons. A cluster is a [CANDIDATE] same-person
// grouping for human confirmation, never an identity conclusion.
//
// JSON to stdout (or --output); diagnostics to stderr; exit 0 on success.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

// Output is the becky-cluster JSON contract (SPEC §5).
type Output struct {
	Tool       string            `json:"tool"`
	Modality   string            `json:"modality"`
	FaceEdge   float64           `json:"face_edge"`
	VoiceEdge  float64           `json:"voice_edge"`
	MinCluster int               `json:"min_cluster"`
	Clusters   []Cluster         `json:"clusters"`
	Singletons int               `json:"singletons"` // people seen once (not yet "recurring")
	Inputs     InputSummary      `json:"inputs"`
	Notes      map[string]string `json:"notes,omitempty"`
}

// InputSummary records how the run was fed (provenance/auditability).
type InputSummary struct {
	Mode            string `json:"mode"`             // embed | db | identify-glob
	ClipsConsidered int    `json:"clips_considered"` // input clips / files seen
	AppearancesUsed int    `json:"appearances_used"` // embeddings actually clustered
	VoiceCount      int    `json:"voice_count"`
	FaceCount       int    `json:"face_count"`
}

// Cluster is one recurring person (a [CANDIDATE] same-person grouping).
type Cluster struct {
	ClusterID           string        `json:"cluster_id"`
	Modality            string        `json:"modality"`
	SuggestedName       *string       `json:"suggested_name"` // null until a human names it once
	MemberCount         int           `json:"member_count"`   // "Person A appears in N clips"
	DistinctSourceFiles int           `json:"distinct_source_files"`
	Cohesion            float64       `json:"cohesion"` // mean intra-cluster cosine (quality)
	EdgeThreshold       float64       `json:"edge_threshold"`
	Representative      string        `json:"representative"` // best member's source clip (for the human to look at)
	Members             []Member      `json:"members"`
	KBCrossCheck        *KBCrossCheck `json:"kb_crosscheck,omitempty"`
}

// Member is one appearance inside a cluster, with full provenance.
type Member struct {
	AppearanceID string  `json:"appearance_id"`
	SourceFile   string  `json:"source_file"`
	SourceSHA256 string  `json:"source_sha256"`
	Timestamp    float64 `json:"timestamp"`
	FrameIndex   int     `json:"frame_index"`
	SpeakerID    string  `json:"speaker_id,omitempty"`
	DetScore     float64 `json:"det_score"`
}

// runConfig bundles the parsed flags that drive the run.
type runConfig struct {
	modality   string
	faceEdge   float64
	voiceEdge  float64
	minCluster int
	kbDir      string
	dbPath     string
	store      bool
	matchVoice float64
	matchFace  float64
	device     string
	verbose    bool
}

func main() {
	out := flag.String("output", "", "write clusters JSON here instead of stdout")
	modality := flag.String("modality", "both", "face | voice | both")
	faceEdge := flag.Float64("face-edge", 0.50, "cosine threshold to link two faces as same-person (clustering; stricter than identify's recall-first match)")
	voiceEdge := flag.Float64("voice-edge", 0.65, "cosine threshold to link two voices as same-speaker (becky's measured same ~0.84 vs different ~0.03)")
	minCluster := flag.Int("min-cluster", 2, "smallest cluster to report (a stranger seen in >= N clips)")
	kbDir := flag.String("kb", "", "knowledge base dir to cross-check clusters against (label known people)")
	dbPath := flag.String("db", "", "read stored appearance embeddings from this forensic.db (instead of embedding clips)")
	store := flag.Bool("store", false, "persist freshly embedded appearances + cluster results to --db (durable path)")
	identifyGlob := flag.String("identify-glob", "", "harvest provenance from becky-identify JSON files (no vectors stored there yet)")
	matchVoice := flag.Float64("kb-voice-threshold", 0.45, "voice cosine threshold for the KB cross-check 'is_known' flag")
	matchFace := flag.Float64("kb-face-threshold", 0.55, "face cosine threshold for the KB cross-check 'is_known' flag")
	device := flag.String("device", "", "device: cuda | cpu (default from config)")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	positional := parsePositionals()

	mod := strings.ToLower(strings.TrimSpace(*modality))
	if mod != "face" && mod != "voice" && mod != "both" {
		beckyio.Fatalf("--modality must be face, voice, or both (got %q)", mod)
	}
	if *minCluster < 1 {
		beckyio.Fatalf("--min-cluster must be >= 1")
	}

	cfg := config.Load()
	rc := runConfig{
		modality: mod, faceEdge: *faceEdge, voiceEdge: *voiceEdge,
		minCluster: *minCluster, kbDir: *kbDir, dbPath: *dbPath, store: *store,
		matchVoice: *matchVoice, matchFace: *matchFace, device: *device, verbose: *verbose,
	}

	report, err := run(cfg, rc, positional, *identifyGlob)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if err := emit(report, *out); err != nil {
		beckyio.Fatalf("%v", err)
	}
}

// run gathers appearances (embed clips | read DB | harvest identify), clusters per
// modality, cross-checks against the KB, and assembles the report.
func run(cfg config.Config, rc runConfig, positional []string, identifyGlob string) (Output, error) {
	report := Output{
		Tool:       "becky-cluster v1.0.0",
		Modality:   rc.modality,
		FaceEdge:   rc.faceEdge,
		VoiceEdge:  rc.voiceEdge,
		MinCluster: rc.minCluster,
		Clusters:   []Cluster{},
		Notes:      map[string]string{},
	}
	report.Notes["honesty"] = "clusters are [CANDIDATE] same-person groupings for human confirmation; not identity conclusions"
	report.Notes["thresholds"] = "precision-leaning: better to split one person into two clusters a human merges than merge two strangers a human cannot unmerge"

	apps, summary, err := gatherAppearances(cfg, rc, positional, identifyGlob, &report)
	if err != nil {
		return report, err
	}
	report.Inputs = summary

	if len(apps) == 0 {
		report.Notes["result"] = "no appearance embeddings available to cluster; see input mode notes"
		return report, nil
	}

	// Persist freshly embedded appearances if requested (durable store path).
	if rc.store && rc.dbPath != "" && summary.Mode == "embed" {
		if serr := storeAppearances(cfg, rc.dbPath, apps); serr != nil {
			report.Notes["store"] = "failed to store appearances: " + serr.Error()
			beckyio.Logf(true, "warning: %v", serr)
		} else {
			beckyio.Logf(rc.verbose, "stored %d appearance(s) to %s", len(apps), rc.dbPath)
		}
	}

	// Cluster each requested modality separately (vectors are not cross-comparable).
	var allClusters []Cluster
	totalSingletons := 0
	for _, m := range modalitiesToRun(rc.modality) {
		modApps := filterModality(apps, m)
		if len(modApps) == 0 {
			continue
		}
		clusters, singletons := clusterModality(cfg, rc, m, modApps)
		allClusters = append(allClusters, clusters...)
		totalSingletons += singletons
	}

	sort.Slice(allClusters, func(i, j int) bool {
		if allClusters[i].MemberCount != allClusters[j].MemberCount {
			return allClusters[i].MemberCount > allClusters[j].MemberCount // biggest first
		}
		return allClusters[i].ClusterID < allClusters[j].ClusterID
	})
	report.Clusters = allClusters
	report.Singletons = totalSingletons

	if rc.store && rc.dbPath != "" && len(allClusters) > 0 {
		if serr := storeClusters(cfg, rc.dbPath, allClusters); serr != nil {
			report.Notes["store_clusters"] = "failed to store clusters: " + serr.Error()
		}
	}

	if rc.modality == "face" || rc.modality == "both" {
		report.Notes["face_prereq"] = "face clustering requires the F1 rotation fix + enrolled-quality frames (SPEC §2); voice is the more reliable modality"
	}
	return report, nil
}

// gatherAppearances resolves the input mode and returns the appearance embeddings.
// Priority: --db (stored vectors) > positional clips (embed) > --identify-glob
// (provenance-only, no vectors). The chosen mode + counts go into the summary.
func gatherAppearances(cfg config.Config, rc runConfig, positional []string, identifyGlob string, report *Output) ([]appearance, InputSummary, error) {
	switch {
	case rc.dbPath != "" && len(positional) == 0:
		apps, err := loadAppearancesFromDB(cfg, rc.dbPath, rc.modality)
		if err != nil {
			return nil, InputSummary{}, err
		}
		return apps, summarize("db", len(apps), apps), nil

	case len(positional) > 0:
		clips, err := expandClips(positional)
		if err != nil {
			return nil, InputSummary{}, err
		}
		beckyio.Logf(rc.verbose, "embedding %d clip(s) for modality %q", len(clips), rc.modality)
		var apps []appearance
		for _, m := range modalitiesToRun(rc.modality) {
			modApps, eerr := embedClips(cfg, clips, m, rc.device, rc.verbose)
			if eerr != nil {
				report.Notes[m+"_embed"] = "skipped: " + eerr.Error()
				beckyio.Logf(true, "warning: %s embedding failed: %v", m, eerr)
				continue
			}
			apps = append(apps, modApps...)
		}
		return apps, summarize("embed", len(clips), apps), nil

	case identifyGlob != "":
		files, err := harvestIdentifyGlob(identifyGlob, rc.modality)
		if err != nil {
			return nil, InputSummary{}, err
		}
		report.Notes["identify_glob"] = fmt.Sprintf(
			"matched %d identify.json file(s) with an unidentified %s; becky-identify does not yet persist embeddings, so re-run with the clips (embed mode) or --db to cluster vectors",
			len(files), rc.modality)
		return nil, summarize("identify-glob", len(files), nil), nil

	default:
		return nil, InputSummary{}, fmt.Errorf("no input: pass clip(s)/dir/glob to embed, or --db to read stored embeddings, or --identify-glob")
	}
}

// clusterModality clusters one modality's appearances and builds the Cluster
// records (>= min-cluster members), counting the rest as singletons. It runs the
// algorithm the SPEC pairs with the modality: Chinese Whispers for face,
// agglomerative for voice.
func clusterModality(cfg config.Config, rc runConfig, modality string, apps []appearance) ([]Cluster, int) {
	edge := rc.voiceEdge
	if modality == "face" {
		edge = rc.faceEdge
	}
	var groups [][]int
	if modality == "face" {
		groups = chineseWhispers(apps, edge, 20)
	} else {
		groups = agglomerative(apps, edge)
	}

	sim := similarityMatrix(apps)

	// Optional KB cross-check prints for this modality (loaded once).
	var prints []enrolledPrint
	matchThreshold := rc.matchVoice
	if modality == "face" {
		matchThreshold = rc.matchFace
	}
	if rc.kbDir != "" {
		p, err := loadEnrolledPrints(cfg, rc.kbDir, modality, rc.device, rc.verbose)
		if err != nil {
			beckyio.Logf(true, "warning: KB cross-check (%s) failed: %v", modality, err)
		} else {
			prints = p
		}
	}

	var clusters []Cluster
	singletons := 0
	letter := 0
	for _, members := range groups {
		if len(members) < rc.minCluster {
			singletons += len(members)
			continue
		}
		c := buildCluster(modality, letterLabel(letter), members, apps, sim, edge)
		letter++
		if len(prints) > 0 {
			cc := crossCheck(centroid(members, apps), prints, matchThreshold)
			c.KBCrossCheck = &cc
		}
		clusters = append(clusters, c)
	}
	beckyio.Logf(rc.verbose, "%s: %d cluster(s), %d singleton(s) at edge %.2f", modality, len(clusters), singletons, edge)
	return clusters, singletons
}

// buildCluster assembles one Cluster from a member-index group, ordering members
// by descending detector score (the strongest appearance first) and picking the
// strongest member's clip as the representative for a human to look at.
func buildCluster(modality, label string, members []int, apps []appearance, sim [][]float64, edge float64) Cluster {
	sort.Slice(members, func(a, b int) bool {
		return apps[members[a]].DetScore > apps[members[b]].DetScore
	})
	mem := make([]Member, 0, len(members))
	files := map[string]bool{}
	for _, idx := range members {
		a := apps[idx]
		files[a.SourceFile] = true
		mem = append(mem, Member{
			AppearanceID: a.ID,
			SourceFile:   a.SourceFile,
			SourceSHA256: a.SourceSHA256,
			Timestamp:    round4(a.Timestamp),
			FrameIndex:   a.FrameIndex,
			SpeakerID:    a.SpeakerID,
			DetScore:     round4(a.DetScore),
		})
	}
	rep := ""
	if len(mem) > 0 {
		rep = mem[0].SourceFile
	}
	return Cluster{
		ClusterID:           modality + "-" + label,
		Modality:            modality,
		SuggestedName:       nil,
		MemberCount:         len(members),
		DistinctSourceFiles: len(files),
		Cohesion:            round4(cohesion(members, sim)),
		EdgeThreshold:       edge,
		Representative:      rep,
		Members:             mem,
	}
}

// summarize builds the InputSummary, counting voice vs face appearances.
func summarize(mode string, clips int, apps []appearance) InputSummary {
	s := InputSummary{Mode: mode, ClipsConsidered: clips, AppearancesUsed: len(apps)}
	for _, a := range apps {
		switch a.Modality {
		case "voice":
			s.VoiceCount++
		case "face":
			s.FaceCount++
		}
	}
	return s
}

// modalitiesToRun expands "both" into the two single modalities (voice first, the
// more reliable + ships-first one), or returns the single requested modality.
func modalitiesToRun(modality string) []string {
	if modality == "both" {
		return []string{"voice", "face"}
	}
	return []string{modality}
}

// filterModality returns the appearances of one modality.
func filterModality(apps []appearance, modality string) []appearance {
	var out []appearance
	for _, a := range apps {
		if a.Modality == modality {
			out = append(out, a)
		}
	}
	return out
}

// letterLabel maps 0->A, 1->B, ... 25->Z, 26->AA, etc. for human-friendly cluster
// ids ("Person A").
func letterLabel(n int) string {
	if n < 0 {
		n = 0
	}
	var b []byte
	for {
		b = append([]byte{byte('A' + n%26)}, b...)
		n = n/26 - 1
		if n < 0 {
			break
		}
	}
	return string(b)
}

// parsePositionals parses leading flags, collects all positional args, then
// re-parses any flags that followed them (Go's flag stops at the first non-flag).
func parsePositionals() []string {
	flag.Parse()
	rest := flag.Args()
	var positional []string
	for len(rest) > 0 {
		positional = append(positional, rest[0])
		if err := flag.CommandLine.Parse(rest[1:]); err != nil {
			break
		}
		rest = flag.Args()
	}
	return positional
}

// emit writes the report to stdout (indented JSON) or to outPath.
func emit(o Output, outPath string) error {
	if outPath == "" {
		beckyio.PrintJSON(o)
		return nil
	}
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, append(b, '\n'), 0o644)
}
