package assistant

import (
	"context"
	"fmt"
	"strings"

	"becky-go/internal/footage"
	"becky-go/internal/habits"
)

// router.go is the heart: the cost-tiered Router (R-AI §1, §4.2). Handle runs the
// route-cheap-first + escalate-on-low-confidence + degrade-on-unavailable chains
// and returns a Proposal — nothing mutates until the GUI's ✓ (propose-then-apply).
// Tier 2 is triple-gated: classifyTier chose it AND ctx.Online AND budget left.
//
// SearchHits are supplied by the CALLER (the GUI execs becky-search and passes the
// results in); this package stays DB/model-free so go test is green offline with
// fake backends. The router only BUILDS exec commands for read-verbs that shell
// out — it does not run them.

// Router is the one entry point the GUI calls.
type Router struct {
	local     Backend // Tier 1 (llama-server via internal/llmlocal)
	claudeCLI Backend // Tier 2a (claude -p on the Max plan)
	api       Backend // Tier 2b (Anthropic /v1/messages)
	funnel    *Funnel
	log       func(format string, a ...any) // logs every online escalation (audit)

	// corrLogPath is where approved proposals are appended (habits learning). ""
	// disables logging (still functions; just no learning).
	corrLogPath string

	// pending holds the last proposals so Apply can log one on ✓ without the GUI
	// round-tripping the whole structure back. Keyed by a small id.
	pending map[string]Proposal
}

// Options configure a Router. Any nil/empty backend is simply unavailable (the
// router degrades).
type Options struct {
	Local       Backend
	ClaudeCLI   Backend
	API         Backend
	Funnel      *Funnel
	Log         func(string, ...any)
	CorrLogPath string
}

// NewRouter builds a Router from explicit backends (the GUI wires real ones;
// tests wire fakes). A nil Funnel gets the default bounds.
func NewRouter(opts Options) *Router {
	f := opts.Funnel
	if f == nil {
		f = NewFunnel()
	}
	logf := opts.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Router{
		local:       opts.Local,
		claudeCLI:   opts.ClaudeCLI,
		api:         opts.API,
		funnel:      f,
		log:         logf,
		corrLogPath: opts.CorrLogPath,
		pending:     map[string]Proposal{},
	}
}

// NewDefaultRouter builds a Router with the production backends from resolved
// paths: a warm local model (Tier 1), the claude CLI (Tier 2a), and the API
// (Tier 2b). The GUI passes config.Load().LlamaServer + a text GGUF + the model
// aliases. Each backend self-reports Available(), so a missing one degrades.
func NewDefaultRouter(localModel, llamaServer, deepModel, midModel, corrLogPath string, logf func(string, ...any)) *Router {
	if deepModel == "" {
		deepModel = "opus"
	}
	return NewRouter(Options{
		Local:       newLocalBackend(localModel, llamaServer, logf),
		ClaudeCLI:   newClaudeCLIBackend(deepModel),
		API:         newAPIBackend(deepModel),
		Log:         logf,
		CorrLogPath: corrLogPath,
	})
}

// Handle is the ONE entry point the GUI calls. searchHits is the caller's
// becky-search output for this utterance (nil when none / not run); the funnel
// merges it with the deterministic grep. The returned Proposal is shown with ✓/✗;
// nothing mutates until Apply.
func (r *Router) Handle(ctx context.Context, utt string, cx Context, searchHits []footage.Candidate) (Proposal, error) {
	d := classifyTier(utt, cx) // §1.2 — NO model call

	var p Proposal
	switch d.Tier {
	case TierDeterministic:
		p = r.deterministic(d, cx)
	case TierLocal:
		p = r.viaLocal(ctx, utt, d, cx)
	case TierFrontier:
		p = r.viaFrontier(ctx, utt, d, cx, searchHits)
	default:
		return Proposal{}, fmt.Errorf("unreachable tier %d", d.Tier)
	}

	r.finalize(&p)
	return p, nil
}

// finalize stamps Mutates, registers the proposal for a later Apply, and assigns
// a small id the GUI echoes on ✓.
func (r *Router) finalize(p *Proposal) {
	p.Mutates = anyMutating(p.Actions)
	if p.ID == "" {
		p.ID = fmt.Sprintf("p%d", len(r.pending)+1)
	}
	r.pending[p.ID] = *p
}

// Apply is called on the human's ✓. It logs the approved proposal to the
// corrections log (habits learning) and returns the actions to run, in order. The
// GUI executes the handlers + ExecCommands itself (it owns the real timeline +
// process spawning); this package never mutates anything.
func (r *Router) Apply(id string) ([]Action, []ExecCommand, error) {
	p, ok := r.pending[id]
	if !ok {
		return nil, nil, fmt.Errorf("no pending proposal %q", id)
	}
	r.logApproval(p)
	delete(r.pending, id)
	return p.Actions, p.ExecCommands, nil
}

// Reject discards a pending proposal (the human's ✗).
func (r *Router) Reject(id string) {
	delete(r.pending, id)
}

// logApproval appends one correction record per approved action so becky learns
// the detective's habits (R-AI §2.3). Best-effort: a logging failure never blocks
// the apply. scope = the verb; auto/fixed capture the approved action.
func (r *Router) logApproval(p Proposal) {
	if r.corrLogPath == "" {
		return
	}
	for _, a := range p.Actions {
		_ = habits.AppendCorrectionLog(r.corrLogPath, "clip", string(a.Verb), "approved", "", argsSummary(a))
	}
}

// --- Tier 0: deterministic -------------------------------------------------

// deterministic builds a Proposal from the Tier-0 parsed actions (or literal
// retrieval). search/find_quotes get their exec command formed here.
func (r *Router) deterministic(d Decision, cx Context) Proposal {
	valid, invalid := ParseActions(d.Actions)
	p := Proposal{
		Actions:     valid,
		Invalid:     invalid,
		Tier:        TierDeterministic,
		PreviewText: summarize(valid),
	}
	r.attachExec(&p, cx)
	r.attachPreview(&p, cx)
	return p
}

// --- Tier 1: local model ---------------------------------------------------

// viaLocal runs the Tier-1 backend to parse a fuzzy single-action request. If the
// local model is unavailable, it degrades to Tier 0 (literal retrieval over the
// utterance) with an honest note.
func (r *Router) viaLocal(ctx context.Context, utt string, d Decision, cx Context) Proposal {
	if !IsAvailable(r.local) {
		return r.degradeToRetrieval(utt, cx, "answered locally — the small model is offline")
	}
	out, err := r.local.Complete(ctx, Request{
		System: localSystemPrompt,
		User:   localUserPrompt(cx.Timeline, utt),
		Tier:   TierLocal,
	})
	if err != nil {
		return r.degradeToRetrieval(utt, cx, "answered locally — the small model errored")
	}
	valid, invalid := Parse(out)
	if len(valid) == 0 {
		return r.degradeToRetrieval(utt, cx, "could not parse a confident action — showing a literal search")
	}
	p := Proposal{Actions: valid, Invalid: invalid, Tier: TierLocal, PreviewText: summarize(valid)}
	r.attachExec(&p, cx)
	r.attachPreview(&p, cx)
	return p
}

// --- Tier 2: frontier funnel -----------------------------------------------

// viaFrontier runs the §3.2 map-reduce-plan funnel on the frontier tier, but ONLY
// when triple-gated: classify chose it (guaranteed here) AND ctx.Online AND budget
// left AND a frontier backend is available. Otherwise it downgrades to Tier 1.
func (r *Router) viaFrontier(ctx context.Context, utt string, d Decision, cx Context, searchHits []footage.Candidate) Proposal {
	if !cx.Online || cx.Budget.Exhausted() {
		return r.downgradeToLocal(ctx, utt, cx, "answered locally — turn on the frontier model for deeper search")
	}
	be := r.frontier()
	if be == nil {
		return r.downgradeToLocal(ctx, utt, cx, "answered locally — no frontier backend is available")
	}
	r.log("ONLINE escalation: %q via %s", utt, be.Name())

	// Retrieval is deterministic and ALWAYS runs (even with the model on), so the
	// model only ever sees bounded candidate windows — never the folder.
	index := footage.FolderIndex{}
	if cx.Index != nil {
		index = *cx.Index
	}
	terms := retrievalTerms(utt)
	candidates := r.funnel.Retrieve(index, terms, searchHits)
	if len(candidates) == 0 {
		return Proposal{
			Tier:        TierFrontier,
			PreviewText: "No transcript candidates matched — nothing to add.",
			Note:        "retrieval found no candidate cues for this ask",
		}
	}

	// MAP: judge each bounded window. REDUCE: dedup + sort.
	windows := r.funnel.Windows(candidates)
	selected := make([][]footage.Candidate, 0, len(windows))
	for _, w := range windows {
		selected = append(selected, runMap(ctx, be, utt, w))
	}
	reduced := Reduce(selected)
	if len(reduced) == 0 {
		// No window matched: fall back to the deterministic candidates as the
		// honest result rather than nothing (corroborate-then-conclude: show the
		// literal hits as candidates for the human to confirm).
		reduced = candidates
	}

	// PLAN: one final model call over the SMALL reduced set → actions.
	p := r.plan(ctx, be, utt, cx, reduced)
	p.Sources = sourceRefs(reduced)
	p.Cost = CostNote{Model: be.Name()}
	return p
}

// plan runs the deep PLAN call (step [4]) over the reduced cues and parses the
// action list. On any failure it degrades to a deterministic add_clip plan built
// straight from the reduced cues (so the detective still gets a usable proposal).
func (r *Router) plan(ctx context.Context, be Backend, utt string, cx Context, reduced []footage.Candidate) Proposal {
	out, err := be.Complete(ctx, Request{
		System: planSystemPrompt,
		User:   planUserPrompt(cx.Timeline, utt, reduced),
		Tier:   TierFrontier,
	})
	if err == nil {
		if valid, invalid := Parse(out); len(valid) > 0 {
			return Proposal{Actions: valid, Invalid: invalid, Tier: TierFrontier, PreviewText: summarize(valid)}
		}
	}
	// Deterministic fallback plan from the reduced cues.
	acts := addClipsFromCandidates(reduced)
	return Proposal{
		Actions:     acts,
		Tier:        TierFrontier,
		PreviewText: summarize(acts),
		Note:        "frontier plan unavailable — proposed the matching cues as clips",
	}
}

// frontier picks the first available frontier backend (CLI on the Max plan first,
// API as the degrade), or nil if neither is usable.
func (r *Router) frontier() Backend {
	if IsAvailable(r.claudeCLI) {
		return r.claudeCLI
	}
	if IsAvailable(r.api) {
		return r.api
	}
	return nil
}

// downgradeToLocal serves a Tier-2 turn at Tier 1 (offline/over-budget/no
// frontier). If even Tier 1 is down, it degrades again to Tier-0 retrieval.
func (r *Router) downgradeToLocal(ctx context.Context, utt string, cx Context, note string) Proposal {
	if !IsAvailable(r.local) {
		return r.degradeToRetrieval(utt, cx, note)
	}
	out, err := r.local.Complete(ctx, Request{
		System: localSystemPrompt,
		User:   localUserPrompt(cx.Timeline, utt),
		Tier:   TierLocal,
	})
	if err != nil {
		return r.degradeToRetrieval(utt, cx, note)
	}
	valid, invalid := Parse(out)
	if len(valid) == 0 {
		return r.degradeToRetrieval(utt, cx, note)
	}
	p := Proposal{Actions: valid, Invalid: invalid, Tier: TierLocal, PreviewText: summarize(valid), Note: note}
	r.attachExec(&p, cx)
	r.attachPreview(&p, cx)
	return p
}

// degradeToRetrieval is the terminal floor (R-AI §1.3): a literal search over the
// utterance, always available because it is just a command. It returns a search
// action + the becky-search exec command, with an honest note.
func (r *Router) degradeToRetrieval(utt string, cx Context, note string) Proposal {
	q := strings.TrimSpace(utt)
	if lit, ok := parseLiteralSearch(strings.ToLower(utt)); ok {
		q = lit
	}
	p := Proposal{
		Actions:     []Action{{Verb: VerbSearch, Args: map[string]any{"query": q, "mode": "keyword"}}},
		Tier:        TierDeterministic,
		PreviewText: fmt.Sprintf("Search the transcripts for %q.", q),
		Note:        note,
	}
	r.attachExec(&p, cx)
	return p
}

// --- proposal assembly helpers ---------------------------------------------

// attachExec forms the deterministic exec command for any read-verb that shells
// out (search → becky-search; find_quotes → becky-quotes). The router builds the
// argv; the GUI runs it on ✓. (Binaries need not be present for the build.)
func (r *Router) attachExec(p *Proposal, cx Context) {
	for _, a := range p.Actions {
		switch a.Verb {
		case VerbSearch:
			p.ExecCommands = append(p.ExecCommands, searchCommand(a, cx))
		case VerbFindQuotes:
			p.ExecCommands = append(p.ExecCommands, findQuotesCommand(a))
		}
	}
}

// searchCommand builds the becky-search argv from a search action.
func searchCommand(a Action, cx Context) ExecCommand {
	q := argString(a, "query")
	mode := argString(a, "mode")
	if mode == "" {
		mode = "hybrid"
	}
	args := []string{q, "--mode", mode, "--format", "json"}
	if cx.DB != "" {
		args = append(args, "--db", cx.DB)
	}
	if lim := argString(a, "limit"); lim != "" {
		args = append(args, "--limit", lim)
	}
	return ExecCommand{Bin: "becky-search", Args: args, Note: "deterministic transcript retrieval"}
}

// findQuotesCommand builds the becky-quotes argv from a find_quotes action.
func findQuotesCommand(a Action) ExecCommand {
	args := []string{}
	if srt := argString(a, "srt"); srt != "" {
		args = append(args, "--srt", srt)
	}
	if crit := argString(a, "criteria"); crit != "" {
		args = append(args, "--criteria", crit)
	}
	return ExecCommand{Bin: "becky-quotes", Args: args, Note: "find important passages by criteria"}
}

// attachPreview builds a before→after diff line per action for the overlay. It is
// best-effort and descriptive (the GUI renders the authoritative diff against its
// real timeline); here it gives the human something to read before ✓.
func (r *Router) attachPreview(p *Proposal, cx Context) {
	for _, a := range p.Actions {
		p.Preview = append(p.Preview, diffFor(a, cx.Timeline))
	}
}

// diffFor produces one DiffLine describing an action's effect.
func diffFor(a Action, ts TimelineState) DiffLine {
	switch a.Verb {
	case VerbAddClip:
		return DiffLine{Label: "add clip", Before: "", After: addClipAfter(a)}
	case VerbRemoveClip:
		ref := argString(a, "id") + argString(a, "index")
		return DiffLine{Label: "remove clip " + ref, Before: "clip " + ref, After: ""}
	case VerbSetOverlay:
		return DiffLine{Label: "overlay " + argString(a, "field"), Before: "", After: argString(a, "value")}
	case VerbSetLabel:
		return DiffLine{Label: "label", Before: "", After: argString(a, "text")}
	case VerbSetMarker:
		return DiffLine{Label: "marker @ " + argString(a, "at"), Before: "", After: argString(a, "label")}
	case VerbReorder:
		return DiffLine{Label: "reorder", Before: "", After: "to " + argString(a, "to")}
	default:
		return DiffLine{Label: string(a.Verb), Before: "", After: argsSummary(a)}
	}
}

func addClipAfter(a Action) string {
	s := baseName(argString(a, "source"))
	in := argString(a, "in")
	out := argString(a, "out")
	lbl := argString(a, "label")
	if lbl != "" {
		return fmt.Sprintf("%s [%s-%s] %q", s, in, out, lbl)
	}
	return fmt.Sprintf("%s [%s-%s]", s, in, out)
}

// addClipsFromCandidates is the deterministic plan fallback: one add_clip per
// reduced cue, in chronological order, labelled with the cue text.
func addClipsFromCandidates(cands []footage.Candidate) []Action {
	out := make([]Action, 0, len(cands))
	for _, c := range cands {
		out = append(out, Action{Verb: VerbAddClip, Args: map[string]any{
			"source": c.Source,
			"in":     secondsToTimecode(c.Timestamp),
			"out":    secondsToTimecode(c.End),
			"label":  truncate(c.Text, 60),
		}})
	}
	return out
}

// sourceRefs converts candidates into provenance for the Proposal.
func sourceRefs(cands []footage.Candidate) []SourceRef {
	out := make([]SourceRef, 0, len(cands))
	for _, c := range cands {
		out = append(out, SourceRef{Source: c.Source, Timestamp: c.Timestamp, Text: c.Text})
	}
	return out
}

// retrievalTerms extracts literal keyword terms from an utterance for the grep
// half of retrieval — strips common stop/framing words so the grep is targeted.
func retrievalTerms(utt string) []string {
	stop := map[string]bool{
		"find": true, "every": true, "time": true, "he": true, "she": true, "the": true,
		"a": true, "an": true, "to": true, "of": true, "for": true, "and": true, "all": true,
		"me": true, "show": true, "where": true, "when": true, "whenever": true, "that": true,
		"is": true, "was": true, "in": true, "on": true, "it": true, "any": true, "about": true,
		"offers": true, "offer": true, "mentions": true, "mention": true, "talks": true,
	}
	var terms []string
	for _, w := range strings.Fields(strings.ToLower(utt)) {
		w = strings.Trim(w, ".,!?;:'\"")
		if w == "" || stop[w] || len(w) < 3 {
			continue
		}
		terms = append(terms, w)
	}
	return terms
}
