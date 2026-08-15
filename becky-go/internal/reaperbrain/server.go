package reaperbrain

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Server is the featherweight :11435 proxy REAPER Chat connects to. It speaks
// just enough of the OpenAI wire format (chat completions, plain + streamed,
// /models, llama-server's /health) that the extension can't tell it isn't a
// local model server — while the actual thinking happens in the Backend.
type Server struct {
	Backend Backend
	Host    string
	Port    int
	// Logf receives one line per request; nil = silent (tests).
	Logf func(format string, args ...any)

	// RequestTimeout bounds one chat turn (claude -p cold starts take seconds).
	RequestTimeout time.Duration
}

// NewServer wires a backend to the standard host/port.
func NewServer(b Backend, host string, port int) *Server {
	return &Server{Backend: b, Host: host, Port: port, RequestTimeout: 3 * time.Minute}
}

func (s *Server) BaseURL() string { return fmt.Sprintf("http://%s:%d", s.Host, s.Port) }

// ChatCompletionsURL is the exact endpoint REAPER Chat POSTs to.
func (s *Server) ChatCompletionsURL() string { return s.BaseURL() + "/v1/chat/completions" }

// Handler returns the full route table (exported so tests can drive it with
// httptest and the selftest can mount it on any port).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": s.Backend.Model(), "object": "model", "owned_by": "becky"},
			},
		})
	})
	mux.HandleFunc("/v1/chat/completions", s.handleChat)
	return mux
}

// ListenAndServe blocks until ctx is cancelled or the listener fails. The
// listener is bound before returning any error, so "port already in use" is
// reported immediately instead of racing.
func (s *Server) ListenAndServe(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	srv := &http.Server{Handler: s.Handler()}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return nil
	case err := <-done:
		return err
	}
}

// chatRequest is the subset of the OpenAI request the brain honours. Unknown
// fields (temperature, max_tokens, ...) are accepted and ignored — the backend
// owns those choices.
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: "+err.Error())
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is empty")
		return
	}

	timeout := s.RequestTimeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	start := time.Now()
	text, err := s.Backend.Complete(ctx, req.Messages)
	if err != nil {
		s.logf("chat turn FAILED after %.1fs (%s): %v", time.Since(start).Seconds(), s.Backend.Name(), err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.logf("chat turn ok in %.1fs (%s, %d chars)", time.Since(start).Seconds(), s.Backend.Name(), len(text))

	if req.Stream {
		s.writeStream(w, text)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      fmt.Sprintf("beckychat-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   s.Backend.Model(),
		"choices": []map[string]any{{
			"index":         0,
			"message":       Message{Role: "assistant", Content: text},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	})
}

// writeStream answers a stream:true request as SSE: one role chunk, one content
// chunk, a stop chunk, then [DONE] — the minimal sequence OpenAI-compatible
// clients accept.
func (s *Server) writeStream(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	id := fmt.Sprintf("beckychat-%d", time.Now().UnixNano())
	chunk := func(delta map[string]any, finish any) {
		b, _ := json.Marshal(map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   s.Backend.Model(),
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	chunk(map[string]any{"role": "assistant"}, nil)
	chunk(map[string]any{"content": text}, nil)
	chunk(map[string]any{}, "stop")
	fmt.Fprint(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError uses the OpenAI error envelope so REAPER Chat surfaces the message
// text instead of a bare status code.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": strings.TrimSpace(msg), "type": "becky_brain_error"},
	})
}
