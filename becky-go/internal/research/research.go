// becky-research orchestrator — the deterministic state machine that runs the
// R1..R9 pipeline (SPEC §3) over a captured snapshot and assembles the cited
// findings JSON. All reproducible bookkeeping lives here; the only model/network
// touch points are the injected Backends (helper.go). Output is deterministic over
// a snapshot and the tool degrades, never crashes (SPEC §6, §7).
package research

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"becky-go/internal/freshness"
)

// Mode names the entry path: autonomous topic research, or reading-list synthesis.
const (
	ModeTopic       = "topic"
	ModeReadingList = "reading-list"
)

// Status is the forensic conclusion level of a finding (FORENSIC-OUTPUT-PHILOSOPHY).
const (
	StatusCorroborated = "corroborated" // ≥2 independent verified sources
	StatusCandidate    = "candidate"    // a single verified source / partial
	StatusUnknown      = "unknown"      // not supported by any captured source
)

// Source is one captured, citable source in the report's numbered list (SPEC §4).
type Source struct {
	ID            int    `json:"id"`
	URL           string `json:"url"`
	Title         string `json:"title"`
	FetchedAt     string `json:"fetched_at"`
	ContentSHA256 string `json:"content_sha256"`
	CacheFile     string `json:"cache_file"`
	LinkOK        bool   `json:"link_ok"`
}

// Finding is one assembled claim with its forensic status and citations (SPEC §4).
type Finding struct {
	Claim      string  `json:"claim"`
	Status     string  `json:"status"`
	Confidence float64 `json:"confidence"`
	Verify     string  `json:"verify"` // R7 verdict: supports|partial|unsupported
	Cites      []int   `json:"cites"`
	Basis      string  `json:"basis"`
}

// DroppedClaim records a claim R7 found unsupported, so verification is auditable.
type DroppedClaim struct {
	Claim  string `json:"claim"`
	Verify string `json:"verify"`
	Reason string `json:"reason"`
}

// Upgrade is a becky-dependency self-upgrade flag (SPEC §2a / §4).
type Upgrade struct {
	Component      string `json:"component"`
	CurrentInBecky string `json:"current_in_becky"`
	Found          string `json:"found"`
	Status         string `json:"status"`
	Cites          []int  `json:"cites"`
	Recommendation string `json:"recommendation"`
}

// Notes carries the degrade reason (empty when clean) and the honesty statement.
type Notes struct {
	Degrade string `json:"degrade,omitempty"`
	Honesty string `json:"honesty"`
}

// Report is the full deterministic findings JSON written to stdout (SPEC §4).
type Report struct {
	Tool                  string         `json:"tool"`
	Mode                  string         `json:"mode"`
	Question              string         `json:"question"`
	RunDir                string         `json:"run_dir"`
	DeterministicOverSnap bool           `json:"deterministic_over_snapshot"`
	SnapshotSHA256        string         `json:"snapshot_sha256"`
	Plan                  []SubQuestion  `json:"plan"`
	Findings              []Finding      `json:"findings"`
	BeckyUpgrades         []Upgrade      `json:"becky_upgrades"`
	Sources               []Source       `json:"sources"`
	DroppedClaims         []DroppedClaim `json:"dropped_claims"`
	Notes                 Notes          `json:"notes"`
}

const toolVersion = "becky-research v1.0.0"

// Config is the orchestrator's per-run input, parsed from the CLI by cmd/research.
type Config struct {
	Question        string   // case 2b topic; empty for reading-list
	URLs            []string // case 2a seed URLs (reading-list mode)
	RunDir          string   // run dir; cache lives under <RunDir>/cache
	MaxSubquestions int
	MaxQueriesPer   int
	MaxSources      int
	Offline         bool // forbid live search/fetch; use only the cache
	SelfUpgrade     bool // 2a becky-dependency watch
	Now             func() time.Time
}

// Run executes the deterministic pipeline and returns the assembled report plus a
// nil error on every recoverable path (degrade, never crash). A non-nil error is
// reserved for a truly unsalvageable setup failure (e.g. cache dir uncreatable);
// even then the caller still emits valid JSON carrying the degrade reason.
func Run(cfg Config, be Backends) (Report, error) {
	cfg = withDefaults(cfg)
	rep := Report{
		Tool:                  toolVersion,
		Mode:                  modeOf(cfg),
		Question:              questionOf(cfg),
		RunDir:                cfg.RunDir,
		DeterministicOverSnap: true,
		Notes:                 Notes{Honesty: honesty},
	}

	cache, err := NewCache(cfg.RunDir + "/cache")
	if err != nil {
		rep.Notes.Degrade = "cache unavailable: " + err.Error()
		return rep, fmt.Errorf("open cache: %w", err)
	}

	var degrades []string
	plan := buildPlan(cfg, be, &degrades)
	rep.Plan = plan

	results := gatherResults(cfg, be, plan, &degrades)
	captures := fetchAll(cfg, be, cache, results, &degrades)
	captures = CollapseNearDups(captures, nearDupThreshold)

	rep.Sources = numberSources(cache, captures)
	rep.Findings, rep.DroppedClaims = buildFindings(cfg, be, captures, rep.Sources, &degrades)
	if cfg.SelfUpgrade {
		rep.BeckyUpgrades = matchUpgrades(captures, rep.Sources)
	}

	if snap, err := cache.SnapshotSHA256(); err == nil {
		rep.SnapshotSHA256 = snap
	}
	if len(rep.Sources) == 0 {
		degrades = append(degrades, "no-results")
	}
	rep.Notes.Degrade = strings.Join(uniqueNonEmpty(degrades), "; ")
	return rep, nil
}

const (
	honesty          = "every claim is tied to a captured source; candidates are NOT conclusions"
	nearDupThreshold = 0.8 // word-shingle Jaccard at/above which captures collapse
)

// withDefaults fills unset numeric caps and the clock.
func withDefaults(cfg Config) Config {
	if cfg.MaxSubquestions <= 0 {
		cfg.MaxSubquestions = 5
	}
	if cfg.MaxQueriesPer <= 0 {
		cfg.MaxQueriesPer = 3
	}
	if cfg.MaxSources <= 0 {
		cfg.MaxSources = 25
	}
	if cfg.RunDir == "" {
		cfg.RunDir = ".research/run"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return cfg
}

func modeOf(cfg Config) string {
	if len(cfg.URLs) > 0 && strings.TrimSpace(cfg.Question) == "" {
		return ModeReadingList
	}
	return ModeTopic
}

func questionOf(cfg Config) string {
	if q := strings.TrimSpace(cfg.Question); q != "" {
		return q
	}
	return "synthesize this reading list"
}

// buildPlan runs R1. Reading-list mode skips planning (SPEC §2/§3). With a helper it
// uses the model plan; with none it falls back to the deterministic Go planner.
func buildPlan(cfg Config, be Backends, degrades *[]string) []SubQuestion {
	if modeOf(cfg) == ModeReadingList {
		return nil
	}
	if be.Helper != nil {
		if plan, err := be.Helper.Plan(cfg.Question, cfg.MaxSubquestions, cfg.MaxQueriesPer); err == nil && len(plan) > 0 {
			return plan
		}
		*degrades = append(*degrades, "planner helper failed; used deterministic plan")
	} else {
		*degrades = append(*degrades, "no-model; used deterministic plan")
	}
	return PlanQuery(cfg.Question, cfg.MaxSubquestions, cfg.MaxQueriesPer)
}

// gatherResults runs R2+R4: fan out searches in a fixed sorted order and RRF-fuse
// the per-sub-question result lists into one ranking. Reading-list mode synthesizes
// result rows directly from the seed URLs (no search). Offline/no-search degrades
// to the cache via fetchAll, which reuses any already-captured URL.
func gatherResults(cfg Config, be Backends, plan []SubQuestion, degrades *[]string) []SearchResult {
	if modeOf(cfg) == ModeReadingList {
		return seedResults(cfg.URLs)
	}
	if cfg.Offline || be.Search == nil {
		if cfg.Offline {
			*degrades = append(*degrades, "offline; live search skipped")
		} else {
			*degrades = append(*degrades, "no search backend; live search skipped")
		}
		return nil
	}
	lists := runSearches(be.Search, plan, cfg.MaxQueriesPer, degrades)
	return capResults(FuseRRF(lists, defaultRRFK), cfg.MaxSources)
}

// runSearches issues every query in a fixed, sorted order (determinism) and
// collects the ranked lists. A per-query error degrades that query only.
func runSearches(s Searcher, plan []SubQuestion, k int, degrades *[]string) [][]SearchResult {
	queries := orderedQueries(plan)
	lists := make([][]SearchResult, 0, len(queries))
	for _, q := range queries {
		res, err := s.Search(q, k)
		if err != nil {
			*degrades = append(*degrades, "search failed for a query")
			continue
		}
		lists = append(lists, res)
	}
	return lists
}

// orderedQueries flattens the plan's queries and sorts them so the fan-out order is
// fixed regardless of plan iteration order — same plan → same search order.
func orderedQueries(plan []SubQuestion) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, sq := range plan {
		for _, q := range sq.Queries {
			if q != "" && !seen[q] {
				seen[q] = true
				out = append(out, q)
			}
		}
	}
	sort.Strings(out)
	return out
}

// seedResults turns reading-list URLs into rank-ordered result rows (input order).
func seedResults(urls []string) []SearchResult {
	out := make([]SearchResult, 0, len(urls))
	rank := 1
	seen := map[string]bool{}
	for _, u := range urls {
		c := CanonicalURL(u)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, SearchResult{URL: c, Rank: rank})
		rank++
	}
	return out
}

func capResults(rs []SearchResult, max int) []SearchResult {
	if max > 0 && len(rs) > max {
		return rs[:max]
	}
	return rs
}

func uniqueNonEmpty(ss []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// numberSources assigns stable [n] citation ids: one number per unique URL, ordered
// by the (already deterministic) capture ranking (LangChain's one-number-per-URL).
func numberSources(cache *Cache, captures []Capture) []Source {
	out := make([]Source, 0, len(captures))
	for i, c := range captures {
		out = append(out, Source{
			ID:            i + 1,
			URL:           c.URL,
			Title:         c.Title,
			FetchedAt:     c.FetchedAt,
			ContentSHA256: c.ContentSHA256,
			CacheFile:     "cache/" + cache.CacheFileName(c.URL),
			LinkOK:        c.LinkOK,
		})
	}
	return out
}

// matchUpgrades scans captured text for mentions of becky's pinned dependencies and
// flags any that the source describes as having a newer release. It CONSUMES the
// becky-freshness manifest as the source of truth for "what becky pins / what's
// upstream" (COLLAB-PROTOCOL INBOX-1) rather than re-implementing detection here.
func matchUpgrades(captures []Capture, sources []Source) []Upgrade {
	deps, err := freshness.LoadManifest()
	if err != nil {
		return nil
	}
	urlToID := map[string]int{}
	for _, s := range sources {
		urlToID[s.URL] = s.ID
	}
	out := make([]Upgrade, 0)
	for _, d := range deps {
		var cites []int
		for _, c := range captures {
			if mentionsUpgrade(c.Text, d) {
				if id, ok := urlToID[c.URL]; ok {
					cites = append(cites, id)
				}
			}
		}
		if len(cites) == 0 {
			continue
		}
		sort.Ints(cites)
		out = append(out, Upgrade{
			Component:      d.Name + " (used by " + strings.Join(d.UsedBy, ", ") + ")",
			CurrentInBecky: d.Pinned,
			Found:          "a captured source mentions a newer release",
			Status:         StatusCandidate, // narrow + candidate until corroborated (SPEC §9.3)
			Cites:          cites,
			Recommendation: "evaluate before adopting; run becky-freshness to confirm upstream",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Component < out[j].Component })
	return out
}

// mentionsUpgrade is a conservative match: the source names this dependency AND uses
// an upgrade-signal word. Deliberately narrow to avoid a flood (SPEC §9.3).
func mentionsUpgrade(text string, d freshness.Dependency) bool {
	t := strings.ToLower(text)
	name := brandToken(d.Name)
	if len(name) < 4 || !strings.Contains(t, name) { // <4 chars is too generic to trust
		return false
	}
	for _, sig := range []string{"new release", "released", "version", "v2", "v3", "v4", "v5", "v6", "update", "upgrade"} {
		if strings.Contains(t, sig) {
			return true
		}
	}
	return false
}

// brandToken extracts the leading alphabetic run of a dependency name, lowercased
// — e.g. "Parakeet-TDT-0.6B-v3 (sherpa-onnx)" → "parakeet". This is the human brand
// a source is likely to mention, not the full pinned model string.
func brandToken(name string) string {
	first := strings.Fields(name)
	if len(first) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range first[0] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
			continue
		}
		break
	}
	return strings.ToLower(b.String())
}
