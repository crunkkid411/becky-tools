// manifest.go — the becky-eval manifest schema (input) and the eval result
// schema (output).
//
// The manifest pairs each raw video with its wiki "answer key" (the facts a human
// documented for that clip) and a config/prompt search space (named settings, each
// a set of extra CLI args for the tool under test). becky-eval runs the tool once
// per (case x config), scores the output against the answer key by RECALL, and
// emits a ranked comparison + the best config, holding out flagged cases to check
// generalization.
package main

// Manifest is the becky-eval input document (a JSON file path on argv).
type Manifest struct {
	// Tool is the default becky tool under test (e.g. "validate", "transcribe").
	// A case may override it.
	Tool string `json:"tool"`
	// BinDir is where the becky-*.exe binaries live (default: the eval binary's dir).
	BinDir string `json:"bin_dir,omitempty"`
	// ServerURL optionally points the model-backed tools (validate) at an
	// already-running llama-server so the eval does not reload the model per run.
	ServerURL string `json:"server_url,omitempty"`
	// Configs is the search space shared by all cases unless a case overrides it.
	Configs []Config `json:"configs"`
	// Cases are the video<->answer-key pairs to evaluate.
	Cases []Case `json:"cases"`
}

// Config is one named setting in the search space: a set of extra CLI args passed
// to the tool (e.g. ["--window","30","--fps","1"] or ["--backend","fusion"]).
type Config struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

// Case is one video paired with its answer key.
type Case struct {
	ID        string   `json:"id"`
	Tool      string   `json:"tool,omitempty"`    // overrides Manifest.Tool
	Input     string   `json:"input"`             // path to the raw video
	AnswerKey []Fact   `json:"answer_key"`        // the human-documented facts to recall
	Configs   []Config `json:"configs,omitempty"` // overrides Manifest.Configs
	Holdout   bool     `json:"holdout"`           // excluded from ranking; checks generalization
}

// Fact is one answer-key item. It is "recalled" when ANY of its aliases appears
// (case-insensitively, as a substring) in the tool's flattened output text. A
// fact may name many aliases so the eval is robust to phrasing (e.g. a contact
// region documented as "gluteal/buttock" matched by either word).
type Fact struct {
	ID       string   `json:"id"`
	Aliases  []string `json:"aliases"`            // any-of substrings that count as a hit
	Weight   float64  `json:"weight,omitempty"`   // default 1.0
	Category string   `json:"category,omitempty"` // e.g. contact_region, body_language, statement
}

// effectiveWeight returns the fact's weight, defaulting to 1.0.
func (f Fact) effectiveWeight() float64 {
	if f.Weight <= 0 {
		return 1.0
	}
	return f.Weight
}

// ---- Output schema ----

// Report is the becky-eval output document.
type Report struct {
	EvaluatedAt string        `json:"evaluated_at"` // RFC3339 UTC
	Tool        string        `json:"tool"`
	BinDir      string        `json:"bin_dir"`
	CaseResults []CaseResult  `json:"case_results"`
	Ranking     []ConfigScore `json:"ranking"` // configs ranked by mean train recall (desc)
	Best        *ConfigScore  `json:"best,omitempty"`
	Holdout     []ConfigScore `json:"holdout,omitempty"` // best config's recall on held-out cases
	Notes       []string      `json:"notes,omitempty"`
}

// CaseResult is the per-(case x config) outcome.
type CaseResult struct {
	CaseID     string    `json:"case_id"`
	Config     string    `json:"config"`
	Tool       string    `json:"tool"`
	Holdout    bool      `json:"holdout"`
	Status     string    `json:"status"` // ok | failed | skipped
	Error      string    `json:"error,omitempty"`
	Recall     float64   `json:"recall"`       // weighted recall 0..1
	HitCount   int       `json:"hit_count"`    // facts recalled
	FactCount  int       `json:"fact_count"`   // total facts
	OutputLen  int       `json:"output_chars"` // size of flattened output (false-positive proxy)
	DurationMS int64     `json:"duration_ms"`
	FactHits   []FactHit `json:"fact_hits"` // per-fact hit/miss for human inspection
}

// FactHit records whether one answer-key fact was recalled, and by which alias.
type FactHit struct {
	ID       string  `json:"id"`
	Category string  `json:"category,omitempty"`
	Hit      bool    `json:"hit"`
	Matched  string  `json:"matched,omitempty"` // the alias that matched (first)
	Weight   float64 `json:"weight"`
}

// ConfigScore is a config's aggregate recall across the (train or holdout) cases.
type ConfigScore struct {
	Config     string  `json:"config"`
	MeanRecall float64 `json:"mean_recall"`
	Cases      int     `json:"cases"`
	OK         int     `json:"ok"`
	Failed     int     `json:"failed"`
}
