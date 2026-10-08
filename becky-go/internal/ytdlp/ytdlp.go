// Package ytdlp is the ONLY way becky runs yt-dlp.
//
// Jordan's rules (2026-10-07): "one yt-dlp request per 90 seconds - that needs
// to be mandatory", across every becky process; and never read or change his
// global yt-dlp.conf ("just don't change my .conf file please"), so every call
// passes --ignore-config.
package ytdlp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Jordan's rule (2026-10-07): at most ONE yt-dlp request per 90 seconds, across
// every becky process. Run is the only place yt-dlp runs, and it
// always goes through this gate: a lock file so two runs never call at once,
// and a stamp file whose time is the last call's start and end.
const (
	Gap       = 90 * time.Second
	staleLock = 15 * time.Minute // a run killed mid-call leaves its lock behind
)

var GateDir = `X:\AI-2\becky-tools\research\playlist-intake` // tests point this elsewhere

func waitTurn() (done func(), err error) {
	if err := os.MkdirAll(GateDir, 0o755); err != nil {
		return nil, fmt.Errorf("yt-dlp gate: %w", err)
	}
	lock, stamp := filepath.Join(GateDir, "ytdlp.lock"), filepath.Join(GateDir, "ytdlp-last-call.txt")
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			break
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("yt-dlp gate: %w", err)
		}
		if fi, e := os.Stat(lock); e == nil && time.Since(fi.ModTime()) > staleLock {
			_ = os.Remove(lock)
		}
		time.Sleep(2 * time.Second)
	}
	time.Sleep(Wait(stamp))
	touch := func() { _ = os.WriteFile(stamp, []byte(time.Now().Format(time.RFC3339)), 0o644) }
	touch() // counts even if this process is killed mid-call
	return func() { touch(); _ = os.Remove(lock) }, nil
}

// Wait is how long to wait so 90 s pass since the last call's stamp.
func Wait(stamp string) time.Duration {
	fi, err := os.Stat(stamp)
	if err != nil {
		return 0
	}
	if w := Gap - time.Since(fi.ModTime()); w > 0 {
		return w
	}
	return 0
}

// Run calls yt-dlp --ignore-config <args> once its turn comes; stdout is returned.
func Run(args ...string) ([]byte, error) {
	done, err := waitTurn()
	if err != nil {
		return nil, err
	}
	defer done()
	bin := os.Getenv("BECKY_YTDLP")
	if bin == "" {
		bin = "yt-dlp"
	}
	cmd := exec.Command(bin, append([]string{"--ignore-config"}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			lines := strings.Split(strings.TrimSpace(string(ee.Stderr)), "\n")
			msg = lines[len(lines)-1]
		}
		return nil, fmt.Errorf("yt-dlp: %s", msg)
	}
	return out, nil
}
