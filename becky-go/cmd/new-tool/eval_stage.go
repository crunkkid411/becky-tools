// eval_stage.go — S9 AUTO-RESEARCH FINETUNE, by REUSING becky-eval as a subprocess.
//
// The scoring harness is becky-eval (tool #24); this stage does NOT reimplement it.
// S9 synthesizes a becky-eval MANIFEST (real input(s) paired with answer-key FACTS +
// a config search space) from the spec's declared tunable surface + answer-key facts,
// shells `becky-eval <manifest> --bin-dir <bin> --output <report>`, reads the ranked
// report, records the best config + train/holdout recall, and (when a clear winner
// exists) notes it as the applied default. For tools with no meaningful tunable
// surface, S9 is skipped with a note.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: orchestrator.go's stage dispatch calls runS9Finetune.
//  2. No-dup: REUSES becky-eval.exe (subprocess) — does NOT reimplement scoring.
//     The evalManifest/evalReport structs only MIRROR becky-eval's wire schema
//     (cmd/eval/manifest.go) so the JSON is consumed verbatim.
//  3. Data shape: writes eval-manifest.json {tool, bin_dir, configs:[{name,args}],
//     cases:[{id,input,answer_key:[{id,aliases,weight,category}],holdout}]}; reads
//     eval's Report JSON {ranking,best,holdout}; writes state.finetune.
//  4. Verbatim instruction: "Reuse `becky-eval` (subprocess) for the S9 finetune
//     scoring — do NOT reimplement it."
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// evalManifest mirrors becky-eval's input schema (cmd/eval/manifest.go) so the JSON we
// write is consumed verbatim by the becky-eval binary.
type evalManifest struct {
	Tool    string       `json:"tool"`
	BinDir  string       `json:"bin_dir,omitempty"`
	Configs []evalConfig `json:"configs"`
	Cases   []evalCase   `json:"cases"`
}
type evalConfig struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}
type evalCase struct {
	ID        string     `json:"id"`
	Input     string     `json:"input"`
	AnswerKey []evalFact `json:"answer_key"`
	Holdout   bool       `json:"holdout"`
}
type evalFact struct {
	ID       string   `json:"id"`
	Aliases  []string `json:"aliases"`
	Weight   float64  `json:"weight,omitempty"`
	Category string   `json:"category,omitempty"`
}

// evalReport mirrors the subset of becky-eval's output we read.
type evalReport struct {
	Ranking []evalConfigScore `json:"ranking"`
	Best    *evalConfigScore  `json:"best"`
	Holdout []evalConfigScore `json:"holdout"`
	Notes   []string          `json:"notes"`
}
type evalConfigScore struct {
	Config     string  `json:"config"`
	MeanRecall float64 `json:"mean_recall"`
	Cases      int     `json:"cases"`
}

// runS9Finetune tunes the built tool against a real eval via becky-eval. It is
// skipped (with a note) when the spec declares no real tunable surface or the eval
// binary is unavailable — never a hard failure.
func (o *orchestrator) runS9Finetune(ctx context.Context, s *State) error {
	if s.Finetune != nil {
		o.logf("S9 finetune: already done — skipping")
		return nil
	}
	if s.Build == nil || s.Build.Skipped || s.Test == nil || !s.Test.Passed {
		s.Finetune = &Finetune{Skipped: true, SkipReason: "no passing build to tune"}
		return s.save()
	}

	// Skip when there is no real tunable surface (placeholder-only spec).
	if !hasRealTunableSurface(s.Spec) {
		s.Finetune = &Finetune{Skipped: true, SkipReason: "spec declares no concrete tunable surface; nothing to tune"}
		o.logf("S9 finetune: skipped (no tunable surface)")
		return s.save()
	}

	evalBin := o.binPath("becky-eval")
	if evalBin == "" {
		s.Finetune = &Finetune{Skipped: true, SkipReason: "becky-eval binary not found in --bin dir; cannot tune"}
		o.logf("S9 finetune: skipped (becky-eval not found)")
		return s.save()
	}

	// 1) Synthesize the manifest from the spec + the real asset.
	man := o.synthEvalManifest(s)
	manPath := filepath.Join(s.Meta.RunDir, "eval-manifest.json")
	if b, err := json.MarshalIndent(man, "", "  "); err == nil {
		_ = os.WriteFile(manPath, append(b, '\n'), 0o644)
	}
	reportPath := filepath.Join(s.Meta.RunDir, "eval-report.json")

	// 2) Shell becky-eval (the reused harness). Deterministic, offline, resumable.
	rc, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(rc, evalBin, manPath, "--bin-dir", o.binDir, "--output", reportPath)
	cmd.Dir = o.buildRoot
	out, err := cmd.CombinedOutput()

	ft := &Finetune{ManifestPath: manPath, EvalReportPath: reportPath}
	if err != nil {
		ft.Skipped = true
		ft.SkipReason = "becky-eval run failed: " + tail2(string(out), 400)
		s.Finetune = ft
		o.logf("S9 finetune: becky-eval failed: %s", tail2(string(out), 200))
		return s.save()
	}

	// 3) Read the ranked report and record the best config + recall.
	if data, rerr := os.ReadFile(reportPath); rerr == nil {
		var rep evalReport
		if json.Unmarshal(data, &rep) == nil {
			if rep.Best != nil {
				ft.TrainRecall = rep.Best.MeanRecall
				ft.BestConfig = json.RawMessage(fmt.Sprintf("%q", rep.Best.Config))
			}
			if len(rep.Holdout) > 0 {
				ft.HoldoutRecall = rep.Holdout[0].MeanRecall
			}
			ft.GeneralizationCaveat = fmt.Sprintf("tuned on %d case(s) — illustrative, expand the manifest before trusting rankings", len(man.Cases))
			// becky-new-tool RECORDS the best config; applying it as the tool's default
			// is a deliberate follow-up (a human confirms at GATE C) — not auto-applied.
			ft.Applied = false
		}
	}
	s.Finetune = ft
	o.logf("S9 finetune: best train recall=%.3f holdout=%.3f (report %s)", ft.TrainRecall, ft.HoldoutRecall, reportPath)
	return s.save()
}

// synthEvalManifest builds a becky-eval manifest from the spec's answer-key facts +
// tunable surface, paired with the real test asset.
func (o *orchestrator) synthEvalManifest(s *State) evalManifest {
	var facts []evalFact
	for i, f := range s.Spec.AnswerKeyFacts {
		if isPlaceholder(f) {
			continue
		}
		facts = append(facts, evalFact{
			ID:      fmt.Sprintf("fact_%d", i+1),
			Aliases: []string{f},
			Weight:  1.0,
		})
	}
	// Config search space from the tunable surface (best-effort: each surface item
	// becomes a named no-arg config placeholder a human refines).
	configs := []evalConfig{{Name: "default"}}
	for i, t := range s.Spec.TunableSurface {
		if isPlaceholder(t) {
			continue
		}
		configs = append(configs, evalConfig{Name: fmt.Sprintf("variant_%d", i+1)})
	}
	return evalManifest{
		Tool:    cmdDirName(s.Intake.Slug), // becky-eval prepends "becky-"; pass the bare name
		BinDir:  o.binDir,
		Configs: configs,
		Cases: []evalCase{{
			ID:        "real-asset",
			Input:     o.testAsset,
			AnswerKey: facts,
			Holdout:   false,
		}},
	}
}

// hasRealTunableSurface reports whether the spec declares anything concrete to tune
// (i.e. at least one non-placeholder tunable surface item AND one real answer-key fact).
func hasRealTunableSurface(sp *Spec) bool {
	if sp == nil {
		return false
	}
	realTune := false
	for _, t := range sp.TunableSurface {
		if !isPlaceholder(t) {
			realTune = true
			break
		}
	}
	realFact := false
	for _, f := range sp.AnswerKeyFacts {
		if !isPlaceholder(f) {
			realFact = true
			break
		}
	}
	return realTune && realFact
}

// isPlaceholder reports whether a spec field is one of the "(declare per tool: ...)"
// stubs fillSpecFromIntake seeds.
func isPlaceholder(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "(declare per tool")
}
