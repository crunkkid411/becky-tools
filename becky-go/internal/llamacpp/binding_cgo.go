//go:build llamacgo

// Package llamacpp is becky's IN-PROCESS llama.cpp binding: it loads a GGUF into the
// process via the llama.cpp shared library (llama.dll) through cgo and answers text
// completions with NO child process and NO HTTP server. This is the SPEC-BECKY-NLE §3.5
// "embedded llama is the inner-loop workhorse" — the warm Gemma-4 QAT model running
// in-process so the agent's NL->tool loop is sub-second.
//
// It is BUILD-TAGGED (`llamacgo`) so the default build (CI/cloud, every other tool)
// stays pure-Go and cgo-free; only becky-edit, built with `-tags llamacgo` + CGO_ENABLED=1
// (see scripts/build-becky-edit-llama.ps1), links the in-process model. The cgo paths
// below point at the local llama.cpp build that already runs Gemma-4 + CUDA (the same one
// becky-validate's AVLM uses via llama-server). The import libs (libllama.dll.a /
// libggml.dll.a) are generated into this dir by the build script.
package llamacpp

/*
#cgo CFLAGS: -I C:/llama.cpp/include -I C:/llama.cpp/ggml/include
#cgo LDFLAGS: -L${SRCDIR} -lllama -lggml
#include <stdlib.h>
#include "llama_shim.h"
*/
import "C"

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"
)

// DefaultBackendDir is where the ggml backend DLLs (ggml-cpu / ggml-cuda) live on this
// machine; it is the same llama.cpp build the AVLM uses. Overridable via Load.
const DefaultBackendDir = `C:/llama.cpp/build/bin`

var (
	mu     sync.Mutex
	loaded bool
)

// Available reports that this binary was built with the in-process llama binding.
func Available() bool { return true }

// Load loads the GGUF into the process once. nGpuLayers<0 = all on GPU. Safe to call
// repeatedly (a second call with a model already loaded is a no-op).
func Load(modelPath, backendDir string, nGpuLayers, nCtx int) error {
	mu.Lock()
	defer mu.Unlock()
	if loaded {
		return nil
	}
	if backendDir == "" {
		backendDir = DefaultBackendDir
	}
	cModel := C.CString(modelPath)
	defer C.free(unsafe.Pointer(cModel))
	cBackend := C.CString(backendDir)
	defer C.free(unsafe.Pointer(cBackend))
	if rc := C.becky_llama_init(cModel, cBackend, C.int(nGpuLayers), C.int(nCtx)); rc != 0 {
		return fmt.Errorf("llamacpp: init failed (rc=%d) for %s", int(rc), modelPath)
	}
	loaded = true
	return nil
}

// Complete returns the model's completion of an already-chat-formatted prompt. temp<=0
// is greedy (deterministic — what the JSON tool loop wants). Trailing Gemma end markers
// are stripped.
func Complete(prompt string, maxTokens int, temp float32, seed uint32) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if !loaded {
		return "", fmt.Errorf("llamacpp: model not loaded")
	}
	cPrompt := C.CString(prompt)
	defer C.free(unsafe.Pointer(cPrompt))
	capBytes := maxTokens*8 + 1024
	buf := make([]byte, capBytes)
	n := C.becky_llama_complete(cPrompt, C.int(maxTokens), C.float(temp), C.uint(seed),
		(*C.char)(unsafe.Pointer(&buf[0])), C.int(capBytes))
	if n < 0 {
		return "", fmt.Errorf("llamacpp: completion failed (rc=%d)", int(n))
	}
	out := string(buf[:n])
	// The greedy decode can emit the turn/eos markers as literal text; trim them.
	for _, marker := range []string{"<end_of_turn>", "<eos>", "<start_of_turn>"} {
		if i := strings.Index(out, marker); i >= 0 {
			out = out[:i]
		}
	}
	return strings.TrimSpace(out), nil
}

// Close frees the model + context (frees VRAM).
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if loaded {
		C.becky_llama_free()
		loaded = false
	}
}
