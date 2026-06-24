// catalog.go — becky-ask's view of what becky-tools can do, now backed by the SHARED
// internal/catalog package so every front-door (cmd/ask, cmd/harness, cmd/becky-voice)
// agrees on one source of truth (SPEC-AGENT-HARNESS.md §4, SPEC-BECKY-VOICE.md §3.2).
// The data + logic live in internal/catalog; this file keeps the local names cmd/ask's
// code already uses (capability, matchCapabilities, allOpsList) as thin aliases so the
// extraction is a zero-behavior-change move.
package main

import "becky-go/internal/catalog"

// capability is the shared catalog.Capability — kept as a local alias so the rest of
// cmd/ask (and its struct-literal tests) compile unchanged.
type capability = catalog.Capability

// matchCapabilities returns catalog entries whose keywords appear in the question.
func matchCapabilities(question string) []capability { return catalog.MatchCapabilities(question) }

// allOpsList returns the orchestrator ops, sorted, for the overview / help.
func allOpsList() []capability { return catalog.AllOpsList() }
