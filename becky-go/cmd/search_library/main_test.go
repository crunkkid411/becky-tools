package main

import "testing"

// --json must be a recognized no-op flag, not swallowed into the query text
// (becky-AI-Agent-review-1.md acceptance criterion 8: "every tool supports
// --json"). search_library's default output is already the JSON envelope;
// this test guards against --json silently becoming part of the search
// string.
func TestParseArgs_JSONFlagNotSwallowedIntoQuery(t *testing.T) {
	query, _, pretty, err := parseArgs([]string{"--json", "hostinger", "setup"})
	if err != nil {
		t.Fatalf("parseArgs error: %v", err)
	}
	if pretty {
		t.Error("--json must not set pretty")
	}
	if query != "hostinger setup" {
		t.Errorf("query = %q, want %q (--json must not leak into the query)", query, "hostinger setup")
	}
}

func TestParseArgs_PrettyAndLimitStillWork(t *testing.T) {
	query, limit, pretty, err := parseArgs([]string{"my query", "--pretty", "--limit", "5"})
	if err != nil {
		t.Fatalf("parseArgs error: %v", err)
	}
	if !pretty || limit != 5 || query != "my query" {
		t.Errorf("got query=%q limit=%d pretty=%v, want query=%q limit=5 pretty=true", query, limit, pretty, "my query")
	}
}
