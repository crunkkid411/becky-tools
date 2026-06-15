package research

import (
	"testing"
)

func TestCache_putGetWriteOnce(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cap := Capture{URL: "https://e.com/a", Text: "hello world", HTTPStatus: 200, LinkOK: true}
	stored, err := c.Put(cap)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ContentSHA256 == "" {
		t.Error("Put should fill ContentSHA256 from text")
	}
	if !c.Has("https://e.com/a") {
		t.Error("Has should report a stored URL")
	}

	// Write-once: a second Put with different text must NOT overwrite.
	again, err := c.Put(Capture{URL: "https://e.com/a", Text: "DIFFERENT"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Text != "hello world" {
		t.Errorf("cache is not write-once; got %q", again.Text)
	}

	got, ok := c.Get("https://e.com/a")
	if !ok || got.Text != "hello world" {
		t.Errorf("Get round-trip failed: ok=%v cap=%+v", ok, got)
	}
}

func TestCache_keyIsContentAddressedAndCanonical(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewCache(dir)
	// Two links to the same canonical page → one cache entry.
	if _, err := c.Put(Capture{URL: "https://e.com/p?utm_source=x", Text: "t"}); err != nil {
		t.Fatal(err)
	}
	if !c.Has("https://e.com/p") {
		t.Error("canonical URL should map to the same cache key")
	}
	// HashContent is stable: identical bytes → identical key.
	if HashContent([]byte("abc")) != HashContent([]byte("abc")) {
		t.Error("HashContent is not stable")
	}
	if HashContent([]byte("abc")) == HashContent([]byte("abd")) {
		t.Error("different content must hash differently")
	}
}

func TestCache_snapshotReproducible(t *testing.T) {
	dirA := t.TempDir()
	cA, _ := NewCache(dirA)
	// Insert in DIFFERENT order to prove the tree hash is order-independent.
	_, _ = cA.Put(Capture{URL: "https://e.com/2", Text: "two"})
	_, _ = cA.Put(Capture{URL: "https://e.com/1", Text: "one"})
	snapA, err := cA.SnapshotSHA256()
	if err != nil {
		t.Fatal(err)
	}

	cB, _ := NewCache(t.TempDir())
	_, _ = cB.Put(Capture{URL: "https://e.com/1", Text: "one"})
	_, _ = cB.Put(Capture{URL: "https://e.com/2", Text: "two"})
	snapB, _ := cB.SnapshotSHA256()

	if snapA != snapB {
		t.Errorf("same corpus must yield same snapshot hash regardless of insert order:\n A=%s\n B=%s", snapA, snapB)
	}

	// A different corpus must yield a different snapshot (the freshness signal).
	cC, _ := NewCache(t.TempDir())
	_, _ = cC.Put(Capture{URL: "https://e.com/1", Text: "CHANGED"})
	snapC, _ := cC.SnapshotSHA256()
	if snapC == snapB {
		t.Error("changed content must change the snapshot hash (else differences are silent)")
	}
}

func TestCache_emptyDirSnapshot(t *testing.T) {
	c, _ := NewCache(t.TempDir())
	snap, err := c.SnapshotSHA256()
	if err != nil {
		t.Fatal(err)
	}
	if snap == "" {
		t.Error("empty cache should still produce a (stable) snapshot hash")
	}
}

func TestNewCache_rejectsEmptyDir(t *testing.T) {
	if _, err := NewCache(""); err == nil {
		t.Error("empty dir should error, not silently succeed")
	}
}
