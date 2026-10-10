package systemone

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalDecideSendsJevShapeAndLogs(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/systemone":
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"model":"d1-3B-Q8_0.gguf","answers":{"q":{"type":"noul","noul":0.91}},"usage":{"input_tokens":12}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	l := Local{URL: srv.URL, Dir: dir, Client: srv.Client(), Tool: "test"}

	resp, err := l.Decide(context.Background(), Request{State: "s", Questions: map[string]Question{"q": Noul("yes?")}, Images: []string{"data:image/png;base64,AA=="}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Answers["q"].Noul != 0.91 {
		t.Fatalf("noul = %v, want 0.91", resp.Answers["q"].Noul)
	}
	if got["state"] != "s" || got["images"] == nil || got["questions"] == nil {
		t.Fatalf("server got %v, want state, questions and images", got)
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "log-*.jsonl"))
	if len(logs) != 1 {
		t.Fatalf("want one log file, got %v", logs)
	}
	raw, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(raw), `"model":"local:d1-3B-Q8_0.gguf"`) || !strings.Contains(string(raw), `"cost":0`) {
		t.Fatalf("log line missing model or cost: %s", raw)
	}
}

func TestLocalDecideReportsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad question"}}`))
	}))
	defer srv.Close()
	l := Local{URL: srv.URL, Dir: t.TempDir(), Client: srv.Client()}
	_, err := l.Decide(context.Background(), Request{State: "s", Questions: map[string]Question{"q": Noul("?")}})
	if err == nil || !strings.Contains(err.Error(), "bad question") {
		t.Fatalf("err = %v, want the server's message", err)
	}
}

func TestNewDeciderPicksLocalOrHosted(t *testing.T) {
	if d, name := NewDecider("t", "local:http://127.0.0.1:9999/"); name != "local:http://127.0.0.1:9999" {
		t.Fatalf("name = %q", name)
	} else if _, ok := d.(Local); !ok {
		t.Fatalf("local model gave %T", d)
	}
	if d, name := NewDecider("t", JevModel); name != JevModel {
		t.Fatalf("name = %q", name)
	} else if _, ok := d.(Hosted); !ok {
		t.Fatalf("hosted model gave %T", d)
	}
}
