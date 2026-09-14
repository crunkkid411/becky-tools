// becky-vegas — talk to the VEGAS Pro 18 that is running right now, through the
// BeckyVegas extension's control pipe (vegas/BeckyVegas). One command in, one JSON
// answer out, so Claude Code, Whoretana and becky tools can read and drive the
// open edit without clicking:
//
//	becky-vegas status
//	becky-vegas timeline
//	becky-vegas search query="scissors"
//	becky-vegas search query="scissors" folder="E:\footage"
//	becky-vegas jump start=115.76 end=120.08
//	becky-vegas insert path="X:\clip.mp4" in=10 out=14
//	becky-vegas dialogs
//	becky-vegas help          every command the extension knows
//	becky-vegas instances     which VEGAS windows are reachable
//
// key=value arguments: true/false become booleans, numbers become numbers,
// anything else is a string. --pid N picks a VEGAS when several are open;
// otherwise the one focused most recently wins.
//
// The extension does the safety work: every VEGAS call runs on VEGAS's main
// thread, edits refuse while a dialog is open or a render runs, and each edit is
// one undo step. Exit 0 when VEGAS answered ok, 1 when it refused or failed (the
// reason is in the JSON), 2 when no VEGAS with the extension is reachable.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// instance is one running VEGAS, as the extension describes itself in
// %LOCALAPPDATA%\BeckyVegas\instances\<pid>.json.
type instance struct {
	PID         int    `json:"pid"`
	Pipe        string `json:"pipe"`
	ProjectPath string `json:"project_path"`
	Updated     string `json:"updated"`
	LastFocused string `json:"last_focused,omitempty"`
	Version     string `json:"extension_version"`
}

func main() {
	pid := flag.Int("pid", 0, "talk to this VEGAS process (default: the one focused most recently)")
	timeout := flag.Duration("timeout", 10*time.Minute, "give up waiting for VEGAS after this long")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: becky-vegas <command> [key=value ...]   (becky-vegas help lists the commands)")
		os.Exit(2)
	}
	cmd := flag.Arg(0)

	list := loadInstances(instancesDir(), processAlive)
	if cmd == "instances" {
		printJSON(list)
		return
	}
	args, err := parseArgs(flag.Args()[1:])
	if err != nil {
		fail(2, err.Error())
	}
	req, _ := json.Marshal(map[string]any{"id": 1, "cmd": cmd, "args": args})

	candidates, err := choose(list, *pid)
	if err != nil {
		fail(2, err.Error())
	}
	var lastErr error
	for _, inst := range candidates {
		reply, err := call(inst.Pipe, req, *timeout)
		if err != nil {
			lastErr = fmt.Errorf("VEGAS pid %d: %w", inst.PID, err)
			continue
		}
		os.Stdout.Write(reply)
		var r struct {
			OK bool `json:"ok"`
		}
		if json.Unmarshal(reply, &r) != nil || !r.OK {
			os.Exit(1)
		}
		return
	}
	fail(2, lastErr.Error())
}

func instancesDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "BeckyVegas", "instances")
}

// loadInstances reads every instance file whose process is still alive, most
// recently focused first (then most recently updated). Files of dead processes -
// a VEGAS that crashed or was killed - are ignored, never trusted.
func loadInstances(dir string, alive func(int) bool) []instance {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	list := []instance{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var inst instance
		if json.Unmarshal(b, &inst) != nil || inst.PID <= 0 || inst.Pipe == "" || !alive(inst.PID) {
			continue
		}
		list = append(list, inst)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].LastFocused != list[j].LastFocused {
			return list[i].LastFocused > list[j].LastFocused // RFC 3339 UTC sorts as text
		}
		return list[i].Updated > list[j].Updated
	})
	return list
}

// choose returns the instances to try, in order: just the requested pid, or all
// of them best-first (a stale entry whose pipe is gone falls through to the next).
func choose(list []instance, pid int) ([]instance, error) {
	if pid > 0 {
		for _, inst := range list {
			if inst.PID == pid {
				return []instance{inst}, nil
			}
		}
		return nil, fmt.Errorf("no VEGAS with the BeckyVegas extension is running as pid %d", pid)
	}
	if len(list) == 0 {
		return nil, errors.New("no VEGAS with the BeckyVegas extension is running (open VEGAS Pro 18; the extension loads with it)")
	}
	return list, nil
}

// parseArgs turns key=value words into the JSON args object.
func parseArgs(words []string) (map[string]any, error) {
	args := map[string]any{}
	for _, w := range words {
		k, v, ok := strings.Cut(w, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("argument %q is not key=value", w)
		}
		args[strings.TrimSpace(k)] = typed(v)
	}
	return args, nil
}

func typed(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil && !strings.ContainsAny(v, `\/:`) {
		return f
	}
	return v
}

// call sends one request line to \\.\pipe\<name> and returns the one reply line.
// A busy pipe (every server instance mid-request) is retried for a few seconds.
func call(pipe string, req []byte, timeout time.Duration) ([]byte, error) {
	path := `\\.\pipe\` + pipe
	var f *os.File
	var err error
	for start := time.Now(); ; {
		f, err = os.OpenFile(path, os.O_RDWR, 0)
		if err == nil {
			break
		}
		if errors.Is(err, os.ErrNotExist) || time.Since(start) > 5*time.Second {
			return nil, fmt.Errorf("cannot open %s: %w", path, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer f.Close()
	if _, err := f.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	type result struct {
		line []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := bufio.NewReaderSize(f, 1<<20).ReadBytes('\n')
		done <- result{line, err}
	}()
	select {
	case r := <-done:
		if r.err != nil && len(r.line) == 0 {
			return nil, fmt.Errorf("no answer: %w", r.err)
		}
		return r.line, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("VEGAS did not answer within %s", timeout)
	}
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func fail(code int, msg string) {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": msg})
	fmt.Println(string(b))
	os.Exit(code)
}
