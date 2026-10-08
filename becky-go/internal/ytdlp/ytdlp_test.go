package ytdlp

import (
	"os"
	"path/filepath"
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
