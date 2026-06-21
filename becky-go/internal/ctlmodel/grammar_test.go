package ctlmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrammar_ContainsEveryOp(t *testing.T) {
	g := Grammar()
	for _, op := range grammarOps {
		// Each op appears as a quoted JSON string literal: \"op\".
		want := `\"` + op + `\"`
		if !strings.Contains(g, want) {
			t.Errorf("grammar missing op literal %s", want)
		}
	}
}

func TestGrammar_ContainsEveryKey(t *testing.T) {
	g := Grammar()
	for _, k := range grammarKeys {
		want := `\"` + k + `\"`
		if !strings.Contains(g, want) {
			t.Errorf("grammar missing key literal %s", want)
		}
	}
}

func TestGrammar_StructuralRules(t *testing.T) {
	g := Grammar()
	for _, rule := range []string{"root", "edits", "edit", "op", "member", "key", "value", "string", "number", "boolean", "ws"} {
		if !strings.Contains(g, rule+" ") && !strings.Contains(g, rule+"\t") {
			t.Errorf("grammar missing rule %q", rule)
		}
	}
}

func TestWriteGrammarFile(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteGrammarFile(dir)
	if err != nil {
		t.Fatalf("WriteGrammarFile: %v", err)
	}
	if filepath.Base(path) != "becky-edit.gbnf" {
		t.Errorf("path = %q, want .../becky-edit.gbnf", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != Grammar() {
		t.Errorf("file content != Grammar()")
	}
}
