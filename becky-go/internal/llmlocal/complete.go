package llmlocal

import (
	"context"
	"encoding/json"
	"fmt"
)

// Complete sends one RAW prompt to llama-server's /completion endpoint (no chat template, no
// system turn) and returns the generated text. Fine-tuned models trained on an exact prompt
// shape (DiarizationLM: "<|turn>user\n<speaker:1> ... --> <turn|>\n<|turn>model\n") need the
// prompt byte-for-byte, which the chat endpoint would rewrap. Greedy (temperature 0, seed 42),
// so the same prompt always gives the same answer. Warm and spawn-per-call clients both work.
func (c *Client) Complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	if err := c.Available(); err != nil {
		return "", err
	}
	baseURL := ""
	if c.warm {
		u, err := c.ensureWarm(ctx)
		if err != nil {
			return "", err
		}
		baseURL = u
	} else {
		u, cleanup, err := c.spawnServer(ctx)
		if err != nil {
			return "", err
		}
		defer cleanup()
		baseURL = u
	}
	if maxTokens <= 0 {
		maxTokens = 256
	}
	payload, _ := json.Marshal(map[string]any{
		"prompt":      prompt,
		"n_predict":   maxTokens,
		"temperature": 0.0,
		"seed":        42,
		"top_k":       1,
	})
	resp, err := postJSON(ctx, baseURL+"/completion", payload)
	if err != nil {
		return "", fmt.Errorf("llama-server request: %w", err)
	}
	var cr struct {
		Content string `json:"content"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &cr); err != nil {
		return "", fmt.Errorf("parse llama-server reply: %w", err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("llama-server error: %s", cr.Error.Message)
	}
	return cr.Content, nil
}
