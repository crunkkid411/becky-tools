package llmlocal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
)

// NewEmbedClient builds a spawn-per-call client for a GGUF embedding model
// (Qwen3-Embedding: last-token pooling). It runs on CPU (-ngl 0): a 0.6B model
// embeds ~50 short texts in ~5s there and leaves the 8 GB GPU to Whoretana.
func NewEmbedClient(model, server string, logf func(string, ...any)) *Client {
	c := NewClient(model, server, logf)
	c.ngl, c.ctxLen = 0, 8192
	// -ub/-b: one input must fit a single micro-batch or the server rejects it.
	c.extra = []string{"--embedding", "--pooling", "last", "-ub", "8192", "-b", "8192"}
	return c
}

// Embed returns one L2-normalised vector per text (so dot product == cosine).
// Queries must already carry their instruction prefix; documents are raw text.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if err := c.Available(); err != nil {
		return nil, err
	}
	baseURL, cleanup, err := c.spawnServer(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	body, _ := json.Marshal(map[string]any{"input": texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	defer resp.Body.Close()
	var er struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("parse embedding reply: %w", err)
	}
	if er.Error != nil {
		return nil, fmt.Errorf("llama-server error: %s", er.Error.Message)
	}
	if len(er.Data) != len(texts) {
		return nil, fmt.Errorf("server returned %d vectors for %d texts", len(er.Data), len(texts))
	}
	out := make([][]float64, len(texts))
	for _, d := range er.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("embedding index %d out of range", d.Index)
		}
		out[d.Index] = normalize(d.Embedding)
	}
	return out, nil
}

func normalize(v []float64) []float64 {
	n := 0.0
	for _, x := range v {
		n += x * x
	}
	if n == 0 {
		return v
	}
	n = math.Sqrt(n)
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / n
	}
	return out
}
