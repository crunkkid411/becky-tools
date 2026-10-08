package systemone

// Hosted Jev (TypeSafe) through Jordan's OpenRouter key: same Request and
// Response as local Laya, so a caller swaps one Decider for the other.
//
// Money rule (Jordan, 2026-10-07: "$5 a month"): every call is added to a
// monthly ledger, and once the month's spend reaches MonthlyCapUSD this code
// refuses to send - no prompt, no override flag. Only Jev ids are accepted,
// so this client can never spend on another model. Every request and answer
// is appended to a monthly JSONL log: the training data for a local copy.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// MonthlyCapUSD is Jordan's approved Jev budget. Code refuses past it.
	MonthlyCapUSD = 5.0
	// JevModel always points at TypeSafe's latest Jev on OpenRouter.
	JevModel    = "~typesafe/jev-latest"
	jevEndpoint = "https://openrouter.ai/api/alpha/decisions"
	// DefaultJevDir holds spend-YYYY-MM.json and log-YYYY-MM.jsonl.
	DefaultJevDir   = `X:\AI-2\becky-tools\research\jev`
	staleLedgerLock = 2 * time.Minute
)

// Decider is anything that answers System One requests: local Laya (Runner)
// or hosted Jev (Hosted).
type Decider interface {
	Decide(ctx context.Context, req Request) (Response, error)
}

// Hosted calls Jev on OpenRouter.
type Hosted struct {
	Model    string // must be a Jev id
	Dir      string // ledger + log folder
	Key      string
	Endpoint string
	Client   *http.Client
	Tool     string // which becky tool made the call (logged)
}

// NewHosted reads OPENROUTER_API_KEY and BECKY_JEV_DIR.
func NewHosted(tool string) Hosted {
	h := Hosted{Model: JevModel, Dir: DefaultJevDir, Key: strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")),
		Endpoint: jevEndpoint, Client: &http.Client{Timeout: 60 * time.Second}, Tool: tool}
	if v := strings.TrimSpace(os.Getenv("BECKY_JEV_DIR")); v != "" {
		h.Dir = v
	}
	return h
}

func isJevModel(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return strings.HasPrefix(id, "~typesafe/jev") || strings.HasPrefix(id, "typesafe/jev")
}

type jevBody struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

type jevReply struct {
	Response
	Usage struct {
		InputTokens int     `json:"input_tokens"`
		Cost        float64 `json:"cost"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Decide sends one request, retrying rate limits and outages (429/5xx) with
// backoff, after checking the monthly cap.
func (h Hosted) Decide(ctx context.Context, req Request) (Response, error) {
	if !isJevModel(h.Model) {
		return Response{}, fmt.Errorf("refusing %q: the paid decision client only calls Jev", h.Model)
	}
	if h.Key == "" {
		return Response{}, fmt.Errorf("OPENROUTER_API_KEY is not set")
	}
	month := time.Now().Format("2006-01")
	if spent := h.spent(month); spent >= MonthlyCapUSD {
		return Response{}, fmt.Errorf("Jev budget used up for %s ($%.2f of $%.2f); calls resume next month", month, spent, MonthlyCapUSD)
	}
	body, err := json.Marshal(jevBody{Model: h.Model, State: req.State, Questions: req.Questions})
	if err != nil {
		return Response{}, err
	}
	var last error
	for attempt, wait := 1, 2*time.Second; attempt <= 4; attempt, wait = attempt+1, wait*2 {
		reply, status, err := h.post(ctx, body)
		if err == nil {
			h.record(month, req, reply)
			return reply.Response, nil
		}
		last = err
		if status != http.StatusTooManyRequests && status < 500 && status != 0 {
			break // a bad request will not fix itself
		}
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-time.After(wait):
		}
	}
	return Response{}, last
}

func (h Hosted) post(ctx context.Context, body []byte) (jevReply, int, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, h.Endpoint, bytes.NewReader(body))
	if err != nil {
		return jevReply{}, 0, err
	}
	r.Header.Set("Authorization", "Bearer "+h.Key)
	r.Header.Set("Content-Type", "application/json")
	resp, err := h.Client.Do(r)
	if err != nil {
		return jevReply{}, 0, fmt.Errorf("jev: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out jevReply
	if json.Unmarshal(raw, &out) != nil {
		return jevReply{}, resp.StatusCode, fmt.Errorf("jev: HTTP %d, unreadable reply", resp.StatusCode)
	}
	if out.Error != nil || resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if out.Error != nil {
			msg += ": " + out.Error.Message
		}
		return jevReply{}, resp.StatusCode, fmt.Errorf("jev: %s", msg)
	}
	return out, resp.StatusCode, nil
}

// ledger is the month's spend, shared by every becky process.
type ledger struct {
	USD   float64 `json:"usd"`
	Calls int     `json:"calls"`
}

func (h Hosted) spent(month string) float64 {
	var l ledger
	if raw, err := os.ReadFile(filepath.Join(h.Dir, "spend-"+month+".json")); err == nil {
		_ = json.Unmarshal(raw, &l)
	}
	return l.USD
}

// record adds the call's cost to the ledger (under a lock file, so parallel
// runs cannot lose a charge) and appends request + answer to the log.
func (h Hosted) record(month string, req Request, reply jevReply) {
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return
	}
	lock := filepath.Join(h.Dir, "spend.lock")
	for i := 0; i < 300; i++ {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			break
		}
		if fi, e := os.Stat(lock); e == nil && time.Since(fi.ModTime()) > staleLedgerLock {
			_ = os.Remove(lock)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer os.Remove(lock)
	path := filepath.Join(h.Dir, "spend-"+month+".json")
	var l ledger
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &l)
	}
	l.USD += reply.Usage.Cost
	l.Calls++
	if b, err := json.Marshal(l); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
	line, err := json.Marshal(map[string]any{"time": time.Now().Format(time.RFC3339), "tool": h.Tool,
		"model": reply.Model, "cost": reply.Usage.Cost, "request": req, "answers": reply.Answers})
	if err != nil {
		return
	}
	if f, err := os.OpenFile(filepath.Join(h.Dir, "log-"+month+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		f.Write(append(line, '\n'))
		f.Close()
	}
}
