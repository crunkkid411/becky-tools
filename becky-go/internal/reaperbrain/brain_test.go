package reaperbrain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeResolver builds a Resolver over in-memory fixtures (no real filesystem).
func fakeResolver(env map[string]string, exists map[string]bool, path map[string]string, glob map[string][]string) Resolver {
	return Resolver{
		Getenv: func(k string) string { return env[k] },
		Exists: func(p string) bool { return exists[p] },
		LookPath: func(name string) (string, error) {
			if p, ok := path[name]; ok {
				return p, nil
			}
			return "", errNotFound
		},
		Glob: func(pat string) ([]string, error) { return glob[pat], nil },
	}
}

var errNotFound = &notFound{}

type notFound struct{}

func (*notFound) Error() string { return "not found" }

func TestResolve_EnvOverridesWin(t *testing.T) {
	r := fakeResolver(
		map[string]string{EnvServer: `D:\ls.exe`, EnvModel: `D:\my.gguf`},
		map[string]bool{`D:\ls.exe`: true, `D:\my.gguf`: true},
		nil, nil,
	)
	c, err := r.Resolve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Server != `D:\ls.exe` || c.Model != `D:\my.gguf` {
		t.Fatalf("env override not honored: %+v", c)
	}
	if c.Port != DefaultPort || c.Host != DefaultHost {
		t.Fatalf("port/host defaults wrong: %+v", c)
	}
}

func TestResolve_FallsBackToBeckyDefaults(t *testing.T) {
	r := fakeResolver(nil,
		map[string]bool{DefaultServer: true, DefaultModel: true},
		nil, nil)
	c, err := r.Resolve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Server != DefaultServer || c.Model != DefaultModel {
		t.Fatalf("did not fall back to becky defaults: %+v", c)
	}
}

func TestResolve_ServerFromPATH(t *testing.T) {
	r := fakeResolver(nil,
		map[string]bool{DefaultModel: true}, // server NOT on disk at default
		map[string]string{"llama-server": "/usr/bin/llama-server"},
		nil)
	c, err := r.Resolve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Server != "/usr/bin/llama-server" {
		t.Fatalf("expected PATH-resolved server, got %q", c.Server)
	}
}

func TestResolve_ModelScanPicksBestChatGGUF(t *testing.T) {
	glob := map[string][]string{
		ModelDir + `\*.gguf`: {
			ModelDir + `\Qwen3-Embedding-4B-Q5_K_M.gguf`, // disqualified (embedding)
			ModelDir + `\mmproj-BF16.gguf`,               // disqualified (mmproj)
			ModelDir + `\Qwen3-4B-Instruct-2507-Q4_K_M.gguf`,
		},
		ModelDir + `\*\*.gguf`: {
			ModelDir + `\gemma4\gemma-4-E4B-it-Q4_K_M.gguf`,
		},
	}
	r := fakeResolver(nil,
		map[string]bool{DefaultServer: true}, // model default NOT present → scan
		nil, glob)
	c, err := r.Resolve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Qwen instruct scores highest (instruct + qwen); embedding/mmproj excluded.
	if !strings.Contains(c.Model, "Qwen3-4B-Instruct") {
		t.Fatalf("scan picked the wrong model: %q", c.Model)
	}
}

func TestResolve_ErrorWhenNothingFound(t *testing.T) {
	r := fakeResolver(nil, map[string]bool{}, nil, nil)
	c, err := r.Resolve()
	if err == nil {
		t.Fatal("expected an error when neither model nor server exists")
	}
	// Config is still usable for display (defaults filled, default paths shown).
	if c.Port != DefaultPort || c.Server == "" || c.Model == "" {
		t.Fatalf("Config should still be display-ready: %+v", c)
	}
	if !strings.Contains(err.Error(), "llama-server") || !strings.Contains(err.Error(), "GGUF") {
		t.Fatalf("error should name both missing pieces: %v", err)
	}
}

func TestResolve_PortOverride(t *testing.T) {
	r := fakeResolver(
		map[string]string{EnvPort: "11999"},
		map[string]bool{DefaultServer: true, DefaultModel: true},
		nil, nil)
	c, _ := r.Resolve()
	if c.Port != 11999 {
		t.Fatalf("port override ignored: %d", c.Port)
	}
}

func TestResolve_BadPortIgnored(t *testing.T) {
	for _, bad := range []string{"0", "-5", "70000", "nope", ""} {
		r := fakeResolver(
			map[string]string{EnvPort: bad},
			map[string]bool{DefaultServer: true, DefaultModel: true},
			nil, nil)
		c, _ := r.Resolve()
		if c.Port != DefaultPort {
			t.Fatalf("bad port %q should fall back to %d, got %d", bad, DefaultPort, c.Port)
		}
	}
}

func TestArgsAndURLs(t *testing.T) {
	c := Config{Server: `C:\llama-server.exe`, Model: `X:\m.gguf`, Host: DefaultHost, Port: DefaultPort, NGL: 99, CtxLen: 4096}
	args := c.Args()
	joined := strings.Join(args, " ")
	for _, want := range []string{"-m X:\\m.gguf", "--port 11435", "--host 127.0.0.1", "-ngl 99", "-c 4096"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %v", want, args)
		}
	}
	if got := c.ChatCompletionsURL(); got != "http://127.0.0.1:11435/v1/chat/completions" {
		t.Fatalf("chat URL wrong: %s", got)
	}
	if got := c.HealthURL(); got != "http://127.0.0.1:11435/health" {
		t.Fatalf("health URL wrong: %s", got)
	}
}

func TestCommandLineQuotesSpaces(t *testing.T) {
	c := Config{Server: `C:\Program Files\llama\llama-server.exe`, Model: `X:\my models\m.gguf`, Host: DefaultHost, Port: DefaultPort, NGL: 99, CtxLen: 4096}
	cl := c.CommandLine()
	if !strings.Contains(cl, `"C:\Program Files\llama\llama-server.exe"`) {
		t.Fatalf("server path with spaces not quoted: %s", cl)
	}
	if !strings.Contains(cl, `"X:\my models\m.gguf"`) {
		t.Fatalf("model path with spaces not quoted: %s", cl)
	}
}

func TestScoreModel(t *testing.T) {
	cases := []struct {
		path string
		want int // <0 disqualified; otherwise we only assert sign/ordering below
	}{
		{`X:\m\Qwen3-Embedding-4B.gguf`, -1},
		{`X:\m\mmproj-BF16.gguf`, -1},
		{`X:\m\silero_vad.onnx.gguf`, -1},
		{`X:\m\clip-vision.gguf`, -1},
	}
	for _, tc := range cases {
		if got := scoreModel(tc.path); got != tc.want {
			t.Errorf("scoreModel(%q) = %d, want %d", tc.path, got, tc.want)
		}
	}
	// A chat instruct model must outscore a bare/unknown model.
	if scoreModel(`X:\m\Qwen3-4B-Instruct.gguf`) <= scoreModel(`X:\m\random-model.gguf`) {
		t.Fatal("instruct model should score higher than an unknown model")
	}
}

func TestCheckHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if err := CheckHealth(context.Background(), srv.URL, 2*time.Second); err != nil {
		t.Fatalf("expected healthy, got %v", err)
	}
	// A dead address fails fast (no server).
	if err := CheckHealth(context.Background(), "http://127.0.0.1:1", 300*time.Millisecond); err == nil {
		t.Fatal("expected an error against a dead address")
	}
}
