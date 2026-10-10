package llmlocal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NewMediaEmbedClient builds a WARM client for a multimodal embedding model (EmbeddingGemma 2:
// text, pictures, sound and short video clips in one 768-dim space). mmproj is its vision+audio
// projector GGUF. One resident server for the whole run (Close it when done): indexing a video is
// thousands of requests. Mean pooling comes from the GGUF itself; -ub/-b let one clip's tokens fit
// a single micro-batch.
func NewMediaEmbedClient(model, mmproj, server string, logf func(string, ...any)) *Client {
	c := NewWarmClientCtx(model, server, 8192, logf)
	c.extra = []string{"--embeddings", "--mmproj", mmproj, "-ub", "4096", "-b", "4096"}
	c.mmproj = mmproj
	return c
}

// TextInput is a text item for EmbedMedia (queries carry their task prefix).
func TextInput(s string) any { return s }

// ImageInput is a picture file for EmbedMedia.
func ImageInput(path string) (any, error) {
	return fileInput(path, "image_url")
}

// AudioInput is a WAV file for EmbedMedia.
func AudioInput(path string) (any, error) {
	return fileInput(path, "input_audio")
}

// VideoInput is a short video clip for EmbedMedia (the model samples its frames).
func VideoInput(path string) (any, error) {
	return fileInput(path, "input_video")
}

func fileInput(path, kind string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data := base64.StdEncoding.EncodeToString(b)
	var part map[string]any
	switch kind {
	case "image_url":
		mime := "image/jpeg"
		if strings.EqualFold(filepath.Ext(path), ".png") {
			mime = "image/png"
		}
		part = map[string]any{"type": kind, "image_url": map[string]any{"url": "data:" + mime + ";base64," + data}}
	case "input_audio":
		part = map[string]any{"type": kind, "input_audio": map[string]any{"data": data, "format": "wav"}}
	default:
		part = map[string]any{"type": kind, "input_video": map[string]any{"url": "data:video/mp4;base64," + data}}
	}
	return map[string]any{"content": []any{part}}, nil
}

// EmbedMedia returns one L2-normalised vector per input (TextInput / ImageInput / AudioInput /
// VideoInput values, mixed freely) from the resident multimodal embedding server.
func (c *Client) EmbedMedia(ctx context.Context, inputs []any) ([][]float64, error) {
	if err := c.Available(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(c.mmproj); c.mmproj != "" && err != nil {
		return nil, fmt.Errorf("multimodal projector not found at %s", c.mmproj)
	}
	baseURL, err := c.ensureWarm(ctx)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"input": inputs})
	resp, err := postJSON(ctx, baseURL+"/v1/embeddings", body)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	var er struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &er); err != nil {
		return nil, fmt.Errorf("parse embedding reply: %w", err)
	}
	if er.Error != nil {
		return nil, fmt.Errorf("llama-server error: %s", er.Error.Message)
	}
	if len(er.Data) != len(inputs) {
		return nil, fmt.Errorf("server returned %d vectors for %d inputs", len(er.Data), len(inputs))
	}
	out := make([][]float64, len(inputs))
	for _, d := range er.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("embedding index %d out of range", d.Index)
		}
		out[d.Index] = normalize(d.Embedding)
	}
	return out, nil
}
