package ytdlp

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Jordan's rule: one yt-dlp request per 90 seconds.
func TestYtdlpWaitsNinetySeconds(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "ytdlp-last-call.txt")
	if w := Wait(stamp); w != 0 {
		t.Fatalf("no previous call should mean no wait, got %v", w)
	}
	_ = os.WriteFile(stamp, nil, 0o644)
	if w := Wait(stamp); w < 89*time.Second || w > 90*time.Second {
		t.Fatalf("call just made: wait %v, want about 90s", w)
	}
	old := time.Now().Add(-91 * time.Second)
	_ = os.Chtimes(stamp, old, old)
	if w := Wait(stamp); w != 0 {
		t.Fatalf("call 91s ago: wait %v, want 0", w)
	}
}

// First come, first served: the oldest live ticket goes next; a dead waiter's
// stale ticket is ignored and removed.
func TestOldestTicketGoesFirst(t *testing.T) {
	GateDir = t.TempDir()
	write := func(name string, at int64) string {
		p := filepath.Join(GateDir, name)
		_ = os.WriteFile(p, []byte(strconv.FormatInt(at, 10)), 0o644)
		return p
	}
	early, late := write("ytdlp.wait.1", 100), write("ytdlp.wait.2", 200)
	if !first(early, 100) || first(late, 200) {
		t.Fatal("the earlier ticket must go first")
	}
	old := time.Now().Add(-time.Minute)
	_ = os.Chtimes(early, old, old) // its process died while waiting
	if !first(late, 200) {
		t.Fatal("a stale ticket must not block the queue")
	}
	if _, err := os.Stat(early); !os.IsNotExist(err) {
		t.Fatal("the stale ticket should be removed")
	}
}
