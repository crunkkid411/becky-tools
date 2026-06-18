// selector.go — mode dispatch (which Selector), the stdout JSON payload (SPEC
// §2), and the --log rationale sidecar (SPEC §5.5). Kept out of main.go so the
// flag wiring stays readable.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/quotes"
)

// buildSelector picks the selection mode from the flags and returns the Selector
// + optional Expander. Precedence (SPEC §4): --exact > --select-from-json > LLM.
//
//   - exact:  literal phrase match, NO model, expansion OFF.
//   - json:   external anchors, NO model by default; expansion OFF unless a model
//     is independently available (SPEC §5 "off unless a model is available").
//   - local:  LLM semantic selection + LLM expansion (the only model path); if
//     the model is unavailable this returns an error so the CLI degrades.
func buildSelector(f cliFlags, cues []quotes.Cue, criteria string) (mode string, sel quotes.Selector, exp quotes.Expander, err error) {
	switch {
	case strings.TrimSpace(f.exact) != "":
		return "exact", quotes.NewExactSelector(cues, f.exact), nil, nil

	case strings.TrimSpace(f.selectFromJSON) != "":
		js, jerr := quotes.NewJSONSelectorFromFile(f.selectFromJSON)
		if jerr != nil {
			return "", nil, nil, jerr
		}
		// In JSON mode expansion is OFF unless a model happens to be available
		// (SPEC §5). We attach an LLMExpander only when its client reports ready,
		// so an offline GUI run stays purely deterministic.
		if client := newLocalClient(f); client.Available() == nil {
			return "json", js, &quotes.LLMExpander{Client: client}, nil
		}
		return "json", js, nil, nil

	default:
		// LLM semantic selection — the only model-dependent mode.
		client := newLocalClient(f)
		if aerr := client.Available(); aerr != nil {
			return "", nil, nil, fmt.Errorf("LLM selection unavailable: %w", aerr)
		}
		return "local", &quotes.LocalSelector{Client: client}, &quotes.LLMExpander{Client: client}, nil
	}
}

// newLocalClient builds the local llama-server client from --model / env / the
// verified default, with the shared config's llama-server.exe. logf is wired to
// stderr when --verbose.
func newLocalClient(f cliFlags) *quotes.LocalClient {
	model := strings.TrimSpace(f.model)
	if model == "" {
		model = strings.TrimSpace(os.Getenv("BECKY_QUOTES_MODEL"))
	}
	if model == "" {
		model = defaultQuotesModel
	}
	server := config.Load().LlamaServer
	var logf func(string, ...any)
	if f.verbose {
		logf = func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	}
	c := quotes.NewLocalClient(model, server, f.temperature, logf)
	// allow targeting an already-running server (e.g. the GUI's resident one).
	c.BaseURL = strings.TrimSpace(os.Getenv("BECKY_QUOTES_SERVER_URL"))
	return c
}

// payload is the stdout JSON (SPEC §2). modelLabel reflects the actual mode so a
// consumer can see whether selection was exact / external / a named model.
type payload struct {
	Tool      string                 `json:"tool"`
	SRTIn     string                 `json:"srt_in"`
	Out       string                 `json:"out"`
	Model     string                 `json:"model"`
	Criteria  string                 `json:"criteria"`
	Regions   []quotes.RegionSummary `json:"regions"`
	Counts    counts                 `json:"counts"`
	Unmatched []string               `json:"unmatched,omitempty"`
	Integrity integrity              `json:"integrity"`
}

type counts struct {
	Selected   int `json:"selected"`
	AfterMerge int `json:"after_merge"`
}

type integrity struct {
	SRTSHA256   string `json:"srt_sha256,omitempty"`
	VideoSHA256 string `json:"video_sha256,omitempty"`
}

// buildPayload assembles the stdout JSON. regions is empty (not null) when
// nothing was selected, so consumers always get a JSON array.
func buildPayload(f cliFlags, mode, outPath, srtHash, videoHash, criteria string, summary quotes.Summary) payload {
	regions := summary.Regions
	if regions == nil {
		regions = []quotes.RegionSummary{}
	}
	return payload{
		Tool:     "becky-quotes",
		SRTIn:    f.srt,
		Out:      outPath,
		Model:    modelLabel(f, mode),
		Criteria: criteriaLabel(mode, criteria),
		Regions:  regions,
		Counts: counts{
			Selected:   summary.SelectedCount,
			AfterMerge: summary.AfterMerge,
		},
		Unmatched: summary.Unmatched,
		Integrity: integrity{SRTSHA256: srtHash, VideoSHA256: videoHash},
	}
}

// modelLabel describes the selection source for the JSON (SPEC §2 "model").
func modelLabel(f cliFlags, mode string) string {
	switch mode {
	case "exact":
		return "exact"
	case "json":
		return "external:" + f.selectFromJSON
	default:
		model := strings.TrimSpace(f.model)
		if model == "" {
			model = strings.TrimSpace(os.Getenv("BECKY_QUOTES_MODEL"))
		}
		if model == "" {
			model = defaultQuotesModel
		}
		return model
	}
}

func criteriaLabel(mode, criteria string) string {
	if criteria != "" {
		return criteria
	}
	switch mode {
	case "exact":
		return "exact-phrase"
	case "json":
		return "external-anchors"
	default:
		return "generic-salience (default)"
	}
}

// logRecord is the --log sidecar (SPEC §5.5): the run's provenance + the per-
// region yes/no expansion decisions, so a human can audit WHY each block is the
// size it is. Lives OUTSIDE the .srt (SPEC §8).
type logRecord struct {
	Tool      string                   `json:"tool"`
	Mode      string                   `json:"mode"`
	SRTIn     string                   `json:"srt_in"`
	Out       string                   `json:"out"`
	Criteria  string                   `json:"criteria,omitempty"`
	Regions   []quotes.RegionSummary   `json:"regions"`
	Decisions []quotes.RegionDecisions `json:"expansion_decisions,omitempty"`
	Unmatched []string                 `json:"unmatched,omitempty"`
}

// writeLog serializes the rationale sidecar to logPath.
func writeLog(logPath string, f cliFlags, mode, outPath string, summary quotes.Summary) error {
	rec := logRecord{
		Tool:      "becky-quotes",
		Mode:      mode,
		SRTIn:     f.srt,
		Out:       outPath,
		Criteria:  strings.TrimSpace(f.criteria),
		Regions:   summary.Regions,
		Decisions: summary.Decisions,
		Unmatched: summary.Unmatched,
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(logPath, append(data, '\n'), 0o644)
}
