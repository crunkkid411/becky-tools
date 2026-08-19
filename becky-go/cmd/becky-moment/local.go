// local.go — the DEFAULT content judge: a local GGUF model over llmlocal.
//
// Why local is the default and OpenCode Zen is the option, not the other way
// round (which is how HANDOFF-SHORTS-PIPELINE.md originally specified it):
//
//  1. CLAUDE.md's standing invariant is "offline + deterministic — no network at
//     runtime". A cloud judge as the primary path breaks becky's own rule for
//     the one decision that most determines whether a short is worth posting.
//  2. It costs nothing, so the never-spend-money rule is satisfied by
//     construction rather than by a guard.
//  3. It actually runs. Measured on Jordan's own footage 2026-08-18: with no
//     content pass, 112,625 candidates from 177 transcripts all score between
//     0.985 and 1.000 on the structural prior — the top few thousand are
//     effectively tied, so "--top 8" returns an arbitrary eight. Structure alone
//     cannot rank interestingness; that is precisely the job of this second
//     signal, and a judge that needs a key Jordan has not set is a judge that
//     never runs.
//
// The contract is identical to zen.go's — same moment.Prompt, same
// moment.ParseJudgements — so Rank corroborates two independent signals exactly
// the same way whichever backend produced the verdicts.
package main

import (
	"context"
	"fmt"

	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
	"becky-go/internal/moment"
)

// localCtxLen sizes the context for one batch. A batch of 8 windows of ~30s
// speech is roughly 1.2k tokens of transcript plus the rubric; 8192 leaves room
// for the reply without paying for a context becky does not use.
const localCtxLen = 8192

// localBatchSize is smaller than Zen's 12. A 4B local model holds a per-line
// JSON format more reliably over fewer items, and an unparseable line costs a
// whole candidate's verdict (ParseJudgements skips it, degrading that window to
// CandidateOnly).
const localBatchSize = 8

// localJudge returns a JudgeFunc backed by the local Gemma-4 GGUF, plus a
// cleanup func that shuts the resident llama-server down. The client is WARM:
// one server load serves every batch, which matters because a folder of real
// transcripts produces hundreds of batches and a cold start is ~11s each.
//
// Degrades, never crashes: a missing GGUF or a server that will not come up is
// returned as an error the caller turns into a structure-only ranking.
func localJudge(cfg config.Config, logf func(string, ...any)) (moment.JudgeFunc, func(), error) {
	model := cfg.GemmaModel
	if model == "" {
		return nil, nil, fmt.Errorf("no local model configured (gemma_model)")
	}
	c := llmlocal.NewClientCtx(model, cfg.LlamaServer, localCtxLen, logf)
	if err := c.Available(); err != nil {
		return nil, nil, fmt.Errorf("local judge unavailable: %w", err)
	}
	// Keep the weights resident for the whole run.
	c = llmlocal.NewWarmClient(model, cfg.LlamaServer, logf)

	judge := func(ctx context.Context, cands []moment.Candidate) ([]moment.Judgement, error) {
		var all []moment.Judgement
		for start := 0; start < len(cands); start += localBatchSize {
			end := min(start+localBatchSize, len(cands))
			batch := cands[start:end]

			// ~120 tokens covers one JSON verdict line comfortably.
			raw, err := c.Chat(ctx, "", moment.Prompt(batch), llmlocal.Options{
				MaxTokens: 120 * len(batch),
			})
			if err != nil {
				// Keep earlier batches: a partial verdict set degrades
				// per-candidate in Rank, which is honest. Throwing away every
				// good verdict because one batch failed would not be.
				if len(all) == 0 {
					return nil, err
				}
				return all, nil
			}
			for _, j := range moment.ParseJudgements(raw) {
				j.Index += start // re-base onto the full candidate slice
				all = append(all, j)
			}
		}
		return all, nil
	}
	return judge, c.Close, nil
}
