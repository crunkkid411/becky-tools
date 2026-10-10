package people

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadInSpeaking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identify.json")
	os.WriteFile(path, []byte(`{"identifications":[
		{"type":"voice","name":"Hair Jordan","segments":[{"start":0,"end":10},{"start":20,"end":30}]},
		{"type":"face","name":"Shelby","frames":[{"timestamp":15}]}
	],"unidentified":[{"type":"voice","candidate":"Nobody"}]}`), 0o644)
	spans, err := Load(path)
	if err != nil || len(spans) != 3 {
		t.Fatalf("Load = %v, %v", spans, err)
	}
	if got := In(spans, 9, 16); !reflect.DeepEqual(got, []string{"Hair Jordan", "Shelby"}) {
		t.Fatalf("In(9,16) = %v", got)
	}
	if got := In(spans, 11, 13.5); got != nil {
		t.Fatalf("In(11,13.5) = %v, want none (face reaches only 1 s)", got)
	}
	if got := Speaking(spans, 2, 8); got != "Hair Jordan" {
		t.Fatalf("Speaking(2,8) = %q", got)
	}
	if got := Speaking(spans, 8, 18); got != "" {
		t.Fatalf("Speaking(8,18) = %q, want \"\" (under half covered)", got)
	}
	if s, err := Load(filepath.Join(t.TempDir(), "missing.json")); s != nil || err != nil {
		t.Fatalf("missing file = %v, %v; want nothing, no error", s, err)
	}
}
