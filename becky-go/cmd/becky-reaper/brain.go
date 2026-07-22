package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"becky-go/internal/reaperbrain"
)

// cmdBrain runs the featherweight proxy REAPER's "REAPER Chat" extension
// connects to on port 11435. This replaced the llama-server brain (2026-07-22):
// no GPU, no GGUF, a few MB of RAM — the fix for "the chatbox hogs system
// resources and errors every time I open REAPER". The thinking happens in the
// chosen backend: Claude Code OAuth (default, already paid) or OpenCode Zen
// (free models only, enforced in code).
//
//	becky-reaper brain                       # show the plan + connection status
//	becky-reaper brain --start               # serve the brain so REAPER Chat works
//	becky-reaper brain --start --backend zen # use OpenCode Zen free models instead
//	becky-reaper brain --check               # can REAPER Chat connect right now?
//	becky-reaper brain --selftest            # offline proof of the real code path
func cmdBrain(args []string) error {
	var start, check, selftest bool
	backendName := os.Getenv(reaperbrain.EnvBackend)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--start":
			start = true
		case "--check":
			check = true
		case "--selftest":
			selftest = true
		case "--backend":
			i++
			if i < len(args) {
				backendName = args[i]
			}
		case "-h", "--help":
			fmt.Println("becky-reaper brain [--start|--check|--selftest] [--backend claude|zen] - serve REAPER Chat's backend on :11435")
			return nil
		}
	}

	if selftest {
		return brainSelftest()
	}

	port := reaperbrain.DefaultPort
	if p := strings.TrimSpace(os.Getenv(reaperbrain.EnvPort)); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n <= 65535 {
			port = n
		}
	}

	ctx := context.Background()
	baseURL := fmt.Sprintf("http://%s:%d", reaperbrain.DefaultHost, port)
	if check {
		if err := reaperbrain.CheckHealth(ctx, baseURL, 2*time.Second); err != nil {
			fmt.Printf("REAPER Chat CANNOT connect: %s/v1/chat/completions is not serving (%v)\n", baseURL, err)
			fmt.Println("Run: becky-reaper brain --start   (or the 'Start Becky REAPER Brain' one-click)")
			return nil
		}
		fmt.Printf("OK - REAPER Chat can connect: %s/v1/chat/completions is live.\n", baseURL)
		return nil
	}

	backend, err := reaperbrain.SelectBackend(backendName)
	if err != nil {
		return err
	}
	srv := reaperbrain.NewServer(backend, reaperbrain.DefaultHost, port)
	srv.Logf = func(format string, a ...any) { fmt.Printf(format+"\n", a...) }

	fmt.Println("REAPER Chat brain (lightweight proxy - no local model, no GPU):")
	fmt.Printf("  endpoint : %s\n", srv.ChatCompletionsURL())
	fmt.Printf("  backend  : %s (%s)\n", backend.Name(), backend.Model())
	if backend.Name() == "claude" {
		fmt.Println("             answers via your Claude Code login - already covered by Claude Max")
	} else {
		fmt.Println("             answers via OpenCode Zen - FREE models only (enforced)")
	}

	// Is something already serving? (idempotent — don't double-launch.)
	if err := reaperbrain.CheckHealth(ctx, srv.BaseURL(), 1*time.Second); err == nil {
		fmt.Println("  status   : ALREADY RUNNING - REAPER Chat can connect now.")
		return nil
	}

	if err := backend.Ready(); err != nil {
		fmt.Printf("  status   : NOT READY - %v\n", err)
		if backend.Name() == "zen" {
			fmt.Println("\nFix: put your OpenCode Zen API key in the OPENCODE_API_KEY environment")
			fmt.Println("variable (key from opencode.ai/zen), or switch to --backend claude.")
		} else {
			fmt.Println("\nFix: install Claude Code (the claude CLI) and sign in, or switch to --backend zen.")
		}
		if start {
			return fmt.Errorf("cannot start the brain: %w", err)
		}
		return nil
	}

	if !start {
		fmt.Println("  status   : ready to start - re-run with --start (or use the one-click launcher).")
		return nil
	}

	fmt.Printf("\nstarting REAPER brain on :%d (Ctrl-C to stop) ...\n", port)
	fmt.Printf(">>> Leave this window open while you use REAPER Chat.\n")
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.ListenAndServe(runCtx); err != nil {
		return err
	}
	fmt.Println("\nREAPER brain stopped.")
	return nil
}

// brainSelftest is THE PROVABLE HANDOFF proof: it exercises the REAL wire path
// REAPER Chat uses — a live TCP listener, a real HTTP POST of an OpenAI chat
// request (plain AND streamed), the /health and /v1/models probes — with a
// deterministic echo backend, plus the Zen spend guard. No network, no models,
// no GPU; measurable output; non-zero exit on any failure.
func brainSelftest() error {
	fail := func(step string, err error) error { return fmt.Errorf("SELFTEST FAIL [%s]: %v", step, err) }

	// An OS-assigned port so the selftest never collides with a live brain.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fail("listen", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	echo := echoBackend{}
	srv := reaperbrain.NewServer(echo, "127.0.0.1", port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ListenAndServe(ctx)

	if err := waitHealthy(srv.BaseURL()); err != nil {
		return fail("health", err)
	}
	fmt.Printf("selftest: /health OK on %s\n", srv.BaseURL())

	// 1. The exact POST REAPER Chat sends.
	body := `{"model":"x","messages":[{"role":"system","content":"You control REAPER."},{"role":"user","content":"change tempo to 128"}]}`
	resp, err := http.Post(srv.ChatCompletionsURL(), "application/json", strings.NewReader(body))
	if err != nil {
		return fail("chat POST", err)
	}
	raw := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		return fail("chat POST", fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw))
	}
	var out struct {
		Choices []struct {
			Message reaperbrain.Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return fail("chat decode", err)
	}
	if len(out.Choices) != 1 || !strings.Contains(out.Choices[0].Message.Content, "change tempo to 128") {
		return fail("chat content", fmt.Errorf("unexpected reply %q", raw))
	}
	fmt.Printf("selftest: chat completion OK (%d bytes, echoed the user turn)\n", len(raw))

	// 2. The streamed variant some OpenAI clients request.
	resp, err = http.Post(srv.ChatCompletionsURL(), "application/json",
		strings.NewReader(`{"stream":true,"messages":[{"role":"user","content":"ping"}]}`))
	if err != nil {
		return fail("stream POST", err)
	}
	sraw := readAll(resp)
	if !strings.Contains(sraw, `"chat.completion.chunk"`) || !strings.Contains(sraw, "data: [DONE]") {
		return fail("stream body", fmt.Errorf("not valid SSE: %q", sraw))
	}
	fmt.Printf("selftest: streaming (SSE) OK (%d bytes, chunks + [DONE])\n", len(sraw))

	// 3. /v1/models answers (some clients probe it before chatting).
	mresp, err := http.Get(srv.BaseURL() + "/v1/models")
	if err != nil || mresp.StatusCode != http.StatusOK {
		return fail("models", fmt.Errorf("%v HTTP %v", err, mresp))
	}
	readAll(mresp)
	fmt.Println("selftest: /v1/models OK")

	// 4. The Zen SPEND GUARD refuses paid ids and passes free ones.
	if reaperbrain.IsZenFree("claude-sonnet-5") || reaperbrain.IsZenFree("gpt-5.5") {
		return fail("spend guard", fmt.Errorf("a PAID model id passed IsZenFree"))
	}
	if !reaperbrain.IsZenFree(reaperbrain.DefaultZenModel) || !reaperbrain.IsZenFree("deepseek-v4-flash-free") {
		return fail("spend guard", fmt.Errorf("a free model id was refused"))
	}
	fmt.Println("selftest: Zen spend guard OK (paid ids refused, free ids allowed)")

	fmt.Println("SELFTEST PASS: the exact wire path REAPER Chat uses is serving correctly.")
	return nil
}

// echoBackend is the deterministic selftest backend: replies with the last user
// message so the round trip is measurable without any model.
type echoBackend struct{}

func (echoBackend) Name() string  { return "selftest-echo" }
func (echoBackend) Model() string { return "selftest/echo" }
func (echoBackend) Complete(_ context.Context, msgs []reaperbrain.Message) (string, error) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return "echo: " + msgs[i].Content, nil
		}
	}
	return "echo: (no user message)", nil
}

func waitHealthy(baseURL string) error {
	var last error
	for i := 0; i < 50; i++ {
		if last = reaperbrain.CheckHealth(context.Background(), baseURL, 500*time.Millisecond); last == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last
}

func readAll(resp *http.Response) string {
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String()
		}
	}
}
