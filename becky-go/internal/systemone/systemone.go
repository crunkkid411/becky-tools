// Package systemone is becky's local "System One" decision layer: Laya (Convai
// Innovations, Apache-2.0), the open-source Jev-compatible decision model, run
// through ONNX Runtime by the embedded laya_decide.py helper.
//
// A System One model never writes text. It reads a state plus typed questions
// (choice / score / noul) and returns calibrated probabilities in one forward
// pass, in TypeSafe Jev's exact request/response shape. Code does counting,
// dates and filtering; the model does only the judgement (Jev's own rule, see
// research/system-one-models-jev.md).
//
// Degrade, never crash: a missing model or interpreter is a plain error from
// Available/Decide, and callers keep their deterministic fallback.
package systemone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/pyhelpers"
)

// DefaultModelDir holds laya.onnx, laya.onnx.data, laya_config.json and
// tokenizer/ (receptron/laya-onnx @ 68f27dfe, the English checkpoint).
const DefaultModelDir = `X:\AI-2\becky-tools\models\laya`

// DefaultPython is the venv-dml interpreter: onnxruntime + tokenizers.
const DefaultPython = `X:\AI-2\becky-tools\models\asr\venv-dml\Scripts\python.exe`

// Question is one typed question. Criteria is raw JSON so a choice keeps the
// caller's option order (a Go map would re-sort it). Build it with Choice,
// Score or Noul.
type Question struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

// Option is one choice option: a key and a short description ("" = none).
type Option struct{ Key, Desc string }

// Choice asks the model to pick one option.
func Choice(instructions string, opts ...Option) Question {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, o := range opts {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(o.Key)
		b.Write(k)
		b.WriteByte(':')
		if o.Desc == "" {
			b.WriteString("null")
		} else {
			d, _ := json.Marshal(o.Desc)
			b.Write(d)
		}
	}
	b.WriteByte('}')
	return Question{Type: "choice", Instructions: instructions, Criteria: b.Bytes()}
}

// Score asks for an expected level on an ordered rubric (index 0 = lowest).
func Score(instructions string, levels ...string) Question {
	c, _ := json.Marshal(levels)
	return Question{Type: "score", Instructions: instructions, Criteria: c}
}

// Noul asks for P(true) of a yes/no statement.
func Noul(instructions string) Question {
	return Question{Type: "noul", Instructions: instructions}
}

// Request is one state plus its questions (Jev's system_one request body).
type Request struct {
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// Answer is one typed answer. Choice/Probabilities/Confidence are set for
// choice and score; Noul for noul; Score for score.
type Answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Noul          float64            `json:"noul,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
}

// Response is Jev's system_one response body.
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

// Runner locates the model and interpreter.
type Runner struct {
	ModelDir string
	Python   string
}

// New returns a Runner using BECKY_LAYA_DIR / BECKY_LAYA_PYTHON when set.
func New() Runner {
	r := Runner{ModelDir: DefaultModelDir, Python: DefaultPython}
	if v := strings.TrimSpace(os.Getenv("BECKY_LAYA_DIR")); v != "" {
		r.ModelDir = v
	}
	if v := strings.TrimSpace(os.Getenv("BECKY_LAYA_PYTHON")); v != "" {
		r.Python = v
	}
	return r
}

// Available reports whether the model bundle and interpreter exist.
func (r Runner) Available() error {
	for _, p := range []string{r.ModelDir + `\laya.onnx`, r.ModelDir + `\laya.onnx.data`, r.Python} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("System One model not ready: %s is missing", p)
		}
	}
	return nil
}

// Decide answers one request.
func (r Runner) Decide(ctx context.Context, req Request) (Response, error) {
	out, err := r.DecideBatch(ctx, []Request{req})
	if err != nil {
		return Response{}, err
	}
	return out[0], nil
}

// DecideBatch answers many requests with a single model load (~2 s load, then
// ~0.2 s per question on CPU).
func (r Runner) DecideBatch(ctx context.Context, reqs []Request) ([]Response, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	if err := r.Available(); err != nil {
		return nil, err
	}
	script, err := pyhelpers.Materialize("laya_decide.py", pyhelpers.LayaDecide)
	if err != nil {
		return nil, fmt.Errorf("materialize laya helper: %w", err)
	}
	body, err := json.Marshal(map[string]any{"requests": reqs})
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, r.Python, script, "--model-dir", r.ModelDir, "--cpu")
	cmd.Stdin = bytes.NewReader(body)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("laya helper failed: %v: %s", err, lastLine(stderr.String()))
	}
	var parsed struct {
		Responses []Response `json:"responses"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("laya helper returned unreadable output: %w", err)
	}
	if len(parsed.Responses) != len(reqs) {
		return nil, fmt.Errorf("laya helper answered %d of %d requests", len(parsed.Responses), len(reqs))
	}
	return parsed.Responses, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
