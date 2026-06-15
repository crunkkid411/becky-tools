package palantir

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildOpenPlanterEnv_stripsExaKeyWhenOffline(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "EXA_API_KEY=secret", "HOME=/home/x"}

	offline := BuildOpenPlanterEnv(parent, EnrichOptions{WebSearch: false})
	for _, kv := range offline {
		if strings.HasPrefix(kv, "EXA_API_KEY=") {
			t.Fatal("EXA_API_KEY must NOT be passed to the subprocess when web search is off (offline default)")
		}
	}

	online := BuildOpenPlanterEnv(parent, EnrichOptions{WebSearch: true})
	hasKey := false
	for _, kv := range online {
		if kv == "EXA_API_KEY=secret" {
			hasKey = true
		}
	}
	if !hasKey {
		t.Error("with --enrich the EXA key should be passed through")
	}
}

func TestBuildOpenPlanterArgs_shape(t *testing.T) {
	args := BuildOpenPlanterArgs(EnrichOptions{Workspace: "/ws", Provider: "ollama", Model: "llama3.2", MaxDepth: 4, MaxSteps: 100})
	joined := strings.Join(args, " ")
	for _, want := range []string{"--workspace /ws", "--headless", "--provider ollama", "--model llama3.2", "--max-depth 4", "--max-steps 100", "--task @TASK.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q in %q", want, joined)
		}
	}
}

func TestOpenPlanterStub_returnsUnavailable(t *testing.T) {
	_, err := OpenPlanterStub{}.Enrich(EnrichOptions{Workspace: t.TempDir()})
	if !errors.Is(err, ErrEnricherUnavailable) {
		t.Errorf("stub should request a clean degrade via ErrEnricherUnavailable, got %v", err)
	}
}

func TestReadRawGraph_parsesSyntheticFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "graph.out.json")
	const synthetic = `{
	  "nodes": [{"node_id":"person:a","kind":"person","label":"A"}],
	  "edges": [{"kind":"co_occurrence","source":"person:a","target":"person:b","confidence":0.9,"evidence_row_ids":["r1"]}]
	}`
	if err := os.WriteFile(path, []byte(synthetic), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := ReadRawGraph(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(g.Nodes) != 1 || len(g.Edges) != 1 {
		t.Fatalf("parsed %d nodes / %d edges", len(g.Nodes), len(g.Edges))
	}
	if g.Edges[0].EvidenceRowIDs[0] != "r1" {
		t.Errorf("evidence_row_ids not parsed: %+v", g.Edges[0])
	}
}
