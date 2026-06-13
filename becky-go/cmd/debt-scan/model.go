// model.go — JSON shapes and the category catalogue for becky-debt-scan.
//
// One finding = one technical-debt hit at a file:line. The Report is the single
// JSON document emitted to stdout: the scanned path, detected languages, every
// finding, and a roll-up summary. Keeping the shapes here (away from the scan
// logic) makes the emitted contract easy to read and stable across runs.
package main

// Severity levels, ordered low -> high for threshold comparisons.
const (
	sevLow      = "low"
	sevMedium   = "medium"
	sevHigh     = "high"
	sevCritical = "critical"
)

// severityRank maps a severity string to an ordinal for --ci threshold checks.
func severityRank(s string) int {
	switch s {
	case sevLow:
		return 1
	case sevMedium:
		return 2
	case sevHigh:
		return 3
	case sevCritical:
		return 4
	default:
		return 0
	}
}

// Category identifiers. These are the values accepted by --categories and the
// "category" field on every finding.
const (
	catTODO       = "todo"
	catImports    = "imports"
	catDeadCode   = "dead-code"
	catTypes      = "types"
	catDeprecated = "deprecated"
	catNaming     = "naming"
	catComplexity = "complexity"
	catDupes      = "dupes"
	catDocstrings = "docstrings"
)

// allCategories is the canonical, ordered list of every category we scan.
var allCategories = []string{
	catTODO, catImports, catDeadCode, catTypes, catDeprecated,
	catNaming, catComplexity, catDupes, catDocstrings,
}

// Finding is a single technical-debt hit. Optional fields are omitted when zero
// so the JSON stays compact and each category only carries what it measured.
type Finding struct {
	Category   string `json:"category"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	Language   string `json:"language,omitempty"`
	Symbol     string `json:"symbol,omitempty"`     // function / import / identifier name
	Complexity int    `json:"complexity,omitempty"` // for complexity findings
	AgeDays    int    `json:"age_days,omitempty"`   // for stale TODO findings
	DupWith    string `json:"dup_with,omitempty"`   // "file:line" the block duplicates
	Source     string `json:"source,omitempty"`     // "pure-go" or external tool name
	Fixable    bool   `json:"fixable,omitempty"`    // a safe autofix exists
	Fix        string `json:"fix,omitempty"`        // one-line description of the fix
}

// Summary is the roll-up the CI gate and humans read first.
type Summary struct {
	ByCategory map[string]int `json:"by_category"`
	BySeverity map[string]int `json:"by_severity"`
	Total      int            `json:"total"`
}

// Report is the single JSON document emitted to stdout.
type Report struct {
	Tool              string            `json:"tool"`
	Path              string            `json:"path"`
	LanguagesDetected []string          `json:"languages_detected"`
	FilesScanned      int               `json:"files_scanned"`
	Categories        []string          `json:"categories_scanned"`
	Findings          []Finding         `json:"findings"`
	Summary           Summary           `json:"summary"`
	Notes             map[string]string `json:"notes,omitempty"` // skipped externals, degradations
	FixesApplied      []string          `json:"fixes_applied,omitempty"`
	FixesPlanned      []string          `json:"fixes_planned,omitempty"`
}

// newSummary tallies findings into the summary roll-up.
func newSummary(findings []Finding) Summary {
	s := Summary{
		ByCategory: map[string]int{},
		BySeverity: map[string]int{},
		Total:      len(findings),
	}
	for _, f := range findings {
		s.ByCategory[f.Category]++
		s.BySeverity[f.Severity]++
	}
	return s
}
