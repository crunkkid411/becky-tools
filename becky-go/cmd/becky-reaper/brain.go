package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"becky-go/internal/reaperbrain"
)

// cmdBrain boots (or inspects) the local llama-server that REAPER's "REAPER Chat"
// extension connects to on port 11435 — the fix for the live blocker in
// reaper1.jpg. With no flags it prints the resolved plan + whether something is
// already serving. --start actually launches the server (foreground, until Ctrl-C
// or REAPER is closed). --check only probes the port.
//
//	becky-reaper brain            # show the plan + connection status
//	becky-reaper brain --start    # launch the brain so REAPER Chat works
//	becky-reaper brain --check    # is REAPER Chat able to connect right now?
func cmdBrain(args []string) error {
	start, check := false, false
	for _, a := range args {
		switch a {
		case "--start":
			start = true
		case "--check":
			check = true
		case "-h", "--help":
			fmt.Println("becky-reaper brain [--start|--check] - serve REAPER Chat's llama backend on :11435")
			return nil
		}
	}

	cfg, resErr := reaperbrain.NewResolver().Resolve()
	ctx := context.Background()

	if check {
		return reportHealth(ctx, cfg)
	}

	fmt.Println("REAPER Chat brain (llama.cpp llama-server; Ollama is banned):")
	fmt.Printf("  endpoint : %s\n", cfg.ChatCompletionsURL())
	fmt.Printf("  server   : %s\n", cfg.Server)
	fmt.Printf("  model    : %s\n", cfg.Model)
	fmt.Printf("  command  : %s\n", cfg.CommandLine())

	// Is something already serving? (idempotent — don't double-launch.)
	if err := reaperbrain.CheckHealth(ctx, cfg.BaseURL(), 1*time.Second); err == nil {
		fmt.Printf("  status   : ALREADY RUNNING - REAPER Chat can connect now.\n")
		return nil
	}

	if resErr != nil {
		fmt.Printf("  status   : NOT READY - %v\n", resErr)
		if start {
			return fmt.Errorf("cannot start the brain: %w", resErr)
		}
		fmt.Println("\nFix: install llama.cpp's llama-server and a chat GGUF, or set")
		fmt.Printf("  %s / %s, then re-run with --start.\n", reaperbrain.EnvServer, reaperbrain.EnvModel)
		return nil
	}

	if !start {
		fmt.Println("  status   : ready to start - re-run with --start (or use the one-click launcher).")
		return nil
	}
	return startBrain(ctx, cfg)
}

// startBrain launches llama-server in the foreground and waits, streaming its
// logs. It blocks until the server exits or the user interrupts (Ctrl-C), which
// is the natural lifetime for "keep the brain alive while I use REAPER".
func startBrain(ctx context.Context, cfg reaperbrain.Config) error {
	fmt.Printf("\nstarting REAPER brain on :%d (Ctrl-C to stop) ...\n", cfg.Port)
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := exec.CommandContext(ctx, cfg.Server, cfg.Args()...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch llama-server (%s): %w", cfg.Server, err)
	}

	// Announce readiness once /health is up, so Jordan knows REAPER Chat will work.
	go func() {
		for i := 0; i < 180; i++ {
			if ctx.Err() != nil {
				return
			}
			if err := reaperbrain.CheckHealth(ctx, cfg.BaseURL(), 2*time.Second); err == nil {
				fmt.Printf("\n>>> REAPER brain is LIVE at %s\n", cfg.ChatCompletionsURL())
				fmt.Println(">>> In REAPER Chat, ask it to control your DAW now. Leave this window open.")
				return
			}
			time.Sleep(1 * time.Second)
		}
	}()

	err := cmd.Wait()
	if ctx.Err() != nil {
		// Interrupted on purpose — a clean stop, not a failure.
		fmt.Println("\nREAPER brain stopped.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("llama-server exited: %w", err)
	}
	return nil
}

func reportHealth(ctx context.Context, cfg reaperbrain.Config) error {
	if err := reaperbrain.CheckHealth(ctx, cfg.BaseURL(), 2*time.Second); err != nil {
		fmt.Printf("REAPER Chat CANNOT connect: %s is not serving (%v)\n", cfg.ChatCompletionsURL(), err)
		fmt.Println("Run: becky-reaper brain --start   (or the 'Start Becky REAPER Brain' one-click)")
		return nil
	}
	fmt.Printf("OK - REAPER Chat can connect: %s is live.\n", cfg.ChatCompletionsURL())
	return nil
}
