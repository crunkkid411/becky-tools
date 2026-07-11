package avlm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// propsHandler serves a minimal llama-server /props with the given model path and
// vision flag, matching the real b9873 shape we probe.
func propsHandler(modelPath string, vision bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/props" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		body := `{"model_path":` + jsonStr(modelPath) + `,"modalities":{"vision":` + boolStr(vision) + `}}`
		_, _ = w.Write([]byte(body))
	})
}

func jsonStr(s string) string {
	out := []byte{'"'}
	for _, c := range []byte(s) {
		if c == '\\' || c == '"' {
			out = append(out, '\\')
		}
		out = append(out, c)
	}
	return string(append(out, '"'))
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestResidentServerURL_MatchReuses(t *testing.T) {
	// Resident server serves the exact model becky wants (different DIR, same
	// GGUF basename — the real WHORETANA-vs-becky case) → reuse.
	srv := httptest.NewServer(propsHandler(`C:\whoretana\models\gemma4\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`, true))
	defer srv.Close()

	want := `X:\AI-2\becky-tools\models\gemma4\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`
	url, ok := ResidentServerURL(context.Background(), srv.URL, want, nil)
	if !ok || url != srv.URL {
		t.Fatalf("expected reuse of %s, got (%q, %v)", srv.URL, url, ok)
	}
}

func TestResidentServerURL_MismatchSpawns(t *testing.T) {
	// Resident is E2B (WHORETANA default) but becky needs E4B → no reuse (spawn).
	srv := httptest.NewServer(propsHandler(`X:\models\gemma-4-E2B-it-qat-UD-Q4_K_XL.gguf`, true))
	defer srv.Close()

	want := `X:\models\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`
	if url, ok := ResidentServerURL(context.Background(), srv.URL, want, nil); ok {
		t.Fatalf("E2B resident should NOT satisfy an E4B request, got %q", url)
	}
}

func TestResidentServerURL_NoVisionSpawns(t *testing.T) {
	// A text-only server on the port must not be reused for an image call.
	srv := httptest.NewServer(propsHandler(`X:\models\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`, false))
	defer srv.Close()

	want := `X:\models\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`
	if _, ok := ResidentServerURL(context.Background(), srv.URL, want, nil); ok {
		t.Fatal("no-vision server should not be reused")
	}
}

func TestResidentServerURL_UnreachableSpawns(t *testing.T) {
	// Nothing listening (server closed) → clean "spawn", never an error/panic.
	srv := httptest.NewServer(propsHandler(`X:\m\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`, true))
	srv.Close() // close immediately so the address refuses connections
	if _, ok := ResidentServerURL(context.Background(), srv.URL, `X:\m\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`, nil); ok {
		t.Fatal("unreachable resident must fall back to spawn")
	}
}

func TestResidentServerURL_EmptyInputs(t *testing.T) {
	if _, ok := ResidentServerURL(context.Background(), "", "x.gguf", nil); ok {
		t.Fatal("empty baseURL must not reuse")
	}
	if _, ok := ResidentServerURL(context.Background(), "http://127.0.0.1:1", "", nil); ok {
		t.Fatal("empty wantModel must not reuse")
	}
}
