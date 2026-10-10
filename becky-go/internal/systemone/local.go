package systemone

// Local System One decision models served by llama.cpp's /v1/systemone
// endpoint (LiquidAI d1-3B / d1-omni-600M, build b11361+). Same Request and
// Response as Hosted, free, offline, and evidence never leaves this PC.
//
// Decide starts the server itself when nothing answers on LocalURL (Ensure),
// and the server unloads the model after 10 idle minutes, so nothing sits in
// graphics memory between uses. Every answer is appended to the same monthly
// log as Hosted calls (cost 0), so local answers are training data too.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Defaults for the auto-started server; BECKY_S1_URL, BECKY_S1_SERVER,
// BECKY_S1_MODEL and BECKY_S1_MMPROJ override. llama.cpp b11539+ is needed
// (b11487 cannot load d1); it lives beside the older build, which Whoretana uses.
const (
	LocalURL    = "http://127.0.0.1:8091"
	LocalServer = `C:\llama.cpp\build\bin-b11539\llama-server.exe`
	LocalModel  = `X:\HuggingFace\models\LiquidAI\d1-3B-GGUF\d1-3B-Q8_0.gguf`
	LocalMMProj = `X:\HuggingFace\models\LiquidAI\d1-3B-GGUF\mmproj-d1-3B-Q8_0.gguf`
)

// Local calls a llama-server that serves /v1/systemone.
type Local struct {
	URL    string
	Dir    string // log folder (shared with Hosted)
	Client *http.Client
	Tool   string
}

// NewLocal reads BECKY_S1_URL and BECKY_JEV_DIR.
func NewLocal(tool string) Local {
	return Local{URL: strings.TrimRight(env("BECKY_S1_URL", LocalURL), "/"), Dir: env("BECKY_JEV_DIR", DefaultJevDir),
		Client: &http.Client{Timeout: 120 * time.Second}, Tool: tool}
}

// NewDecider picks the decision model for a tool: "local" (or "local:<url>")
// is the d1 server on this PC, anything else is a hosted OpenRouter model id
// ("" = Hosted's default).
func NewDecider(tool, model string) (Decider, string) {
	if m := strings.TrimSpace(model); m == "local" || strings.HasPrefix(m, "local:") {
		l := NewLocal(tool)
		if u := strings.TrimPrefix(strings.TrimPrefix(m, "local"), ":"); u != "" {
			l.URL = strings.TrimRight(u, "/")
		}
		return l, "local:" + l.URL
	}
	h := NewHosted(tool)
	if model != "" {
		h = h.WithModel(model)
	}
	return h, h.Model
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func (l Local) healthy(ctx context.Context) bool {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, l.URL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(r)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Ensure starts llama-server on l.URL's port if nothing answers there, and
// waits up to a minute for the model to load. The server outlives this
// process and sleeps (frees the GPU) after 10 idle minutes.
func (l Local) Ensure(ctx context.Context) error {
	if l.healthy(ctx) {
		return nil
	}
	port := l.URL[strings.LastIndex(l.URL, ":")+1:]
	args := []string{"-m", env("BECKY_S1_MODEL", LocalModel), "-ngl", "99", "-b", "4096", "-ub", "4096",
		"--host", "127.0.0.1", "--port", port, "--sleep-idle-seconds", "600"}
	if mm := env("BECKY_S1_MMPROJ", LocalMMProj); mm != "-" {
		args = append(args, "--mmproj", mm)
	}
	logf, err := os.Create(filepath.Join(os.TempDir(), "becky-s1-server.log"))
	if err != nil {
		return err
	}
	cmd := exec.Command(env("BECKY_S1_SERVER", LocalServer), args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		logf.Close()
		return fmt.Errorf("start local System One server: %w", err)
	}
	logf.Close()
	_ = cmd.Process.Release()
	for i := 0; i < 120; i++ {
		if l.healthy(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("local System One server did not come up on %s (log: %s)", l.URL, logf.Name())
}

// Decide sends one request to the local server, starting it if needed.
func (l Local) Decide(ctx context.Context, req Request) (Response, error) {
	if err := l.Ensure(ctx); err != nil {
		return Response{}, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, l.URL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := l.Client.Do(r)
	if err != nil {
		return Response{}, fmt.Errorf("local System One server at %s: %w", l.URL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out jevReply
	if json.Unmarshal(raw, &out) != nil {
		return Response{}, fmt.Errorf("local System One: HTTP %d, unreadable reply", resp.StatusCode)
	}
	if out.Error != nil || resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if out.Error != nil {
			msg += ": " + out.Error.Message
		}
		return Response{}, fmt.Errorf("local System One: %s", msg)
	}
	l.log(req, out)
	return out.Response, nil
}

func (l Local) log(req Request, reply jevReply) {
	line, err := json.Marshal(map[string]any{"time": time.Now().Format(time.RFC3339), "tool": l.Tool,
		"model": "local:" + reply.Model, "cost": 0, "request": req, "answers": reply.Answers})
	if err != nil || os.MkdirAll(l.Dir, 0o755) != nil {
		return
	}
	path := filepath.Join(l.Dir, "log-"+time.Now().Format("2006-01")+".jsonl")
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		f.Write(append(line, '\n'))
		f.Close()
	}
}
