package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
)

// Pain points are Jordan's known frustrations (pains.json, editable, one line
// each with its source). Relevance = how close a repo or video is to the
// nearest pain, by Qwen3-Embedding-0.6B cosine similarity. Measured on the 35
// repos of GitHub Trending Weekly #50 against hand labels: AUC 0.64 here vs
// 0.45-0.59 for Laya yes/no questions, which said "yes" to nearly everything on
// README text. So Laya routes, embeddings match, code combines.

const (
	defaultPains = `X:\AI-2\becky-tools\research\playlist-intake\pains.json`
	painTask     = "Given a pain point, find software projects that could solve it"
	// Official Qwen/Qwen3-Embedding-0.6B-GGUF, rev 370f27d7 (Apache-2.0).
	embedGGUF = `X:\AI-2\becky-tools\models\embeddings\gguf\Qwen3-Embedding-0.6B-Q8_0.gguf`
)

type pain struct {
	ID     string `json:"id"`
	Pain   string `json:"pain"`
	Source string `json:"source"`
}

type painMatch struct {
	PainID string  `json:"pain_id"`
	Pain   string  `json:"pain"`
	Sim    float64 `json:"similarity"`
}

func loadPains(path string) ([]pain, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ps []pain
	if err := json.Unmarshal(raw, &ps); err != nil {
		return nil, fmt.Errorf("pains file %s is not valid JSON: %w", path, err)
	}
	if len(ps) == 0 {
		return nil, fmt.Errorf("pains file %s is empty", path)
	}
	return ps, nil
}

// matchPains returns, for each text, its closest pain point. Pains and texts go
// in ONE llama.cpp call on CPU (~5s for 50 texts; the old in-process
// sentence-transformers path took 466s for the same 35 repos, same AUC 0.64).
func matchPains(ctx context.Context, pains []pain, texts []string) ([]painMatch, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	all := make([]string, 0, len(pains)+len(texts))
	for _, p := range pains {
		all = append(all, "Instruct: "+painTask+"\nQuery: "+p.Pain) // Qwen3 query prompt
	}
	all = append(all, texts...) // documents: raw text
	c := llmlocal.NewEmbedClient(embedModelPath(), config.Load().LlamaServer, nil)
	v, err := c.Embed(ctx, all)
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	return nearest(pains, v[:len(pains)], v[len(pains):]), nil
}

func embedModelPath() string {
	if p := os.Getenv("BECKY_INTAKE_EMBED_GGUF"); p != "" {
		return p
	}
	return embedGGUF
}

// nearest is the pure part: vectors are L2-normalised, so dot == cosine.
func nearest(pains []pain, pv, tv [][]float64) []painMatch {
	out := make([]painMatch, len(tv))
	for i, t := range tv {
		best := -2.0
		for j, p := range pv {
			s := 0.0
			for k := range t {
				s += t[k] * p[k]
			}
			if s > best {
				best, out[i] = s, painMatch{PainID: pains[j].ID, Pain: pains[j].Pain, Sim: s}
			}
		}
	}
	return out
}
