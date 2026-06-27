package main

import (
	"strings"
	"testing"
)

func TestCleanURL(t *testing.T) {
	cases := map[string]string{
		"https://bedroomproducersblog.com/x/?shem=abc,":      "https://bedroomproducersblog.com/x/?shem=abc",
		"  https://example.com/p  ":                          "https://example.com/p",
		"https://example.com/p":                              "https://example.com/p",
		"https://en.wikipedia.org/wiki/Cat_(disambiguation)": "https://en.wikipedia.org/wiki/Cat_(disambiguation)", // legit trailing ) kept
		"https://example.com/p\"":                            "https://example.com/p",
	}
	for in, want := range cases {
		if got := cleanURL(in); got != want {
			t.Errorf("cleanURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripCodeFence(t *testing.T) {
	cases := map[string]string{
		"# Title\n\ntext":                 "# Title\n\ntext",
		"```markdown\n# Title\ntext\n```": "# Title\ntext",
		"```\n# Title\ntext\n```":         "# Title\ntext",
		"```md\nhello\n```":               "hello",
	}
	for in, want := range cases {
		if got := stripCodeFence(in); got != want {
			t.Errorf("stripCodeFence(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlug(t *testing.T) {
	if got := slug("Hello: World! (2026)"); got != "Hello World 2026" {
		t.Errorf("slug = %q, want %q", got, "Hello World 2026")
	}
	if got := slug("   "); got != "" {
		t.Errorf("slug(spaces) = %q, want empty", got)
	}
}

func TestAssembleDoc_hasFrontmatterAndBody(t *testing.T) {
	doc := assembleDoc("https://example.com/p", "My Title", "Some body text here.")
	if !strings.HasPrefix(doc, "---\n") {
		t.Fatalf("doc should start with frontmatter, got %q", doc[:10])
	}
	for _, want := range []string{`url: "https://example.com/p"`, "extraction_method: gemma4-recover", "# My Title", "Some body text here."} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc missing %q", want)
		}
	}
}
