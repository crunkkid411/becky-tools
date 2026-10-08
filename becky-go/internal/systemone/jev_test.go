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
	"time"
)

func fakeJev(t *testing.T, fails int) (*httptest.Server, *int) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"data":[{"id":"~typesafe/jev-latest","architecture":{"output_modalities":["decisions"]}},{"id":"anthropic/claude-sonnet-5","architecture":{"output_modalities":["text"]}}]}`))
			return
		}
		calls++
		if calls <= fails {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":{"message":"busy"}}`))
			return
		}
		w.Write([]byte(`{"model":"typesafe/jev-1.13","answers":{"best":{"type":"choice","choice":"43","probabilities":{"41":0,"43":1},"confidence":1}},"usage":{"input_tokens":368,"cost":0.25}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func testHosted(t *testing.T, url string) Hosted {
	return Hosted{Model: JevModel, Dir: t.TempDir(), Key: "k", Endpoint: url, Models: url + "/models", Client: http.DefaultClient, Tool: "test"}
}

var testReq = Request{State: "take 41, take 43", Questions: map[string]Question{"best": Choice("Which take?", Option{Key: "41"}, Option{Key: "43"})}}

func TestJevRecordsCostAndAnswers(t *testing.T) {
	srv, _ := fakeJev(t, 0)
	h := testHosted(t, srv.URL)
	got, err := h.Decide(context.Background(), testReq)
	if err != nil || got.Answers["best"].Choice != "43" {
		t.Fatalf("got %+v, %v", got, err)
	}
	month := time.Now().Format("2006-01")
	if s := h.spent(month); s != 0.25 {
		t.Fatalf("ledger = %v, want 0.25", s)
	}
	log, _ := os.ReadFile(filepath.Join(h.Dir, "log-"+month+".jsonl"))
	var line map[string]any
	if json.Unmarshal(log, &line) != nil || line["tool"] != "test" || line["answers"] == nil {
		t.Fatalf("log line missing or wrong: %s", log)
	}
}

// Jordan's $5/month: past the cap nothing is sent.
func TestJevRefusesPastMonthlyCap(t *testing.T) {
	srv, calls := fakeJev(t, 0)
	h := testHosted(t, srv.URL)
	month := time.Now().Format("2006-01")
	_ = os.WriteFile(filepath.Join(h.Dir, "spend-"+month+".json"), []byte(`{"usd":5.0,"calls":9}`), 0o644)
	if _, err := h.Decide(context.Background(), testReq); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("want a budget refusal, got %v", err)
	}
	if *calls != 0 {
		t.Fatalf("a request was sent past the cap")
	}
}

func TestJevRefusesOtherModels(t *testing.T) {
	srv, calls := fakeJev(t, 0)
	h := testHosted(t, srv.URL)
	h.Model = "anthropic/claude-sonnet-5"
	if _, err := h.Decide(context.Background(), testReq); err == nil || *calls != 0 {
		t.Fatalf("non-Jev model was sent (err %v, calls %d)", err, *calls)
	}
}

func TestJevRetriesOutage(t *testing.T) {
	srv, calls := fakeJev(t, 1)
	h := testHosted(t, srv.URL)
	if _, err := h.Decide(context.Background(), testReq); err != nil || *calls != 2 {
		t.Fatalf("err %v after %d calls, want success on the 2nd", err, *calls)
	}
}

// Real calls, opt-in: BECKY_JEV_LIVE=1 (costs about $0.00003 for both).
func TestDeciderLive(t *testing.T) {
	if os.Getenv("BECKY_JEV_LIVE") != "1" {
		t.Skip("set BECKY_JEV_LIVE=1 to call the real models")
	}
	for _, model := range []string{DefaultModel, JevModel, "openai/gpt-5.5"} {
		t.Run(model, func(t *testing.T) { liveOne(t, model) })
	}
}

func liveOne(t *testing.T, model string) {
	h := NewHosted("live-test").WithModel(model)
	h.Dir = t.TempDir()
	if model == "openai/gpt-5.5" { // a chat model: must be refused before sending
		if _, err := h.Decide(context.Background(), testReq); err == nil || !strings.Contains(err.Error(), "not a System One") {
			t.Fatalf("chat model not refused: %v", err)
		}
		return
	}
	got, err := h.Decide(context.Background(), Request{
		State: "Sentence 41: So the whole point of this... Sentence 42: So the whole point of this, uh. Sentence 43: So the whole point of this video is to show you the new editor.",
		Questions: map[string]Question{
			"best":   Choice("Which sentence is the finished take that should stay in the edit?", Option{Key: "41"}, Option{Key: "42"}, Option{Key: "43"}),
			"retake": Noul("Does the speaker restart the same sentence more than once?"),
		}})
	if err != nil || got.Answers["best"].Choice != "43" {
		t.Fatalf("got %+v, %v", got, err)
	}
	t.Logf("model %s, best=%s (conf %.2f), retake=%.2f, spent $%.6f", got.Model, got.Answers["best"].Choice,
		got.Answers["best"].Confidence, got.Answers["retake"].Noul, h.spent(time.Now().Format("2006-01")))
}
