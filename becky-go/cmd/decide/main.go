// becky-decide — System One decisions (Jev-compatible request in, typed answers out).
//
//	becky-decide < request.json                 # local LiquidAI d1 on this PC (default)
//	becky-decide --model laya < request.json    # old local Laya (ONNX, CPU)
//	becky-decide --model perplexity/pplx-decider-v1.1-27b < request.json  # hosted, $5/month cap
//	becky-decide --selftest [--model ...]       # three known-answer checks; exit 1 on a miss
//
// local starts its llama-server on first use (d1-3B, unloads after 10 idle
// minutes) and accepts "images" / "files" (audio) in the request.
//
// The request is {"state": <text or JSON>, "questions": {id: {type, instructions,
// criteria}}} with type choice | score | noul. The answer is typed and carries a
// calibrated probability; the model cannot write text or invent an option.
// It is a signal, never a verdict: callers act only on confident answers and
// route the rest to a bigger model or to Jordan. Exit codes: 0 ok, 1 error,
// 2 usage, 3 model unavailable.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"becky-go/internal/beckyio"
	"becky-go/internal/systemone"
)

func main() {
	selftest := flag.Bool("selftest", false, "run known-answer checks against the model")
	model := flag.String("model", "local", "local (d1 on this PC), laya, or a hosted OpenRouter decision model id")
	flag.Parse()

	var d systemone.Decider
	if *model == "laya" {
		r := systemone.New()
		if err := r.Available(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		d = r
	} else {
		d, _ = systemone.NewDecider("becky-decide", *model)
		if l, ok := d.(systemone.Local); ok {
			if err := l.Ensure(context.Background()); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(3)
			}
		}
	}
	ctx := context.Background()

	if *selftest {
		os.Exit(runSelftest(ctx, d))
	}

	var req systemone.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil || len(req.Questions) == 0 {
		fmt.Fprintln(os.Stderr, "usage: becky-decide < request.json   (needs a state and at least one question)")
		os.Exit(2)
	}
	resp, err := d.Decide(ctx, req)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.PrintJSON(resp)
}

// selfCase is one known-answer check: the answer must land on the right side
// of a wide margin, so a broken sequence layout or tokenizer fails loudly.
type selfCase struct {
	name  string
	state string
	q     systemone.Question
	check func(systemone.Answer) bool
}

func selfCases() []selfCase {
	team := systemone.Choice("Which team should handle this ticket?",
		systemone.Option{Key: "billing", Desc: "payments, refunds, invoices"},
		systemone.Option{Key: "support", Desc: "product help and bugs"},
		systemone.Option{Key: "sales", Desc: "new purchases"})
	return []selfCase{
		{"choice: double charge -> billing", "I was charged twice on my credit card this month, please refund the duplicate payment.", team,
			func(a systemone.Answer) bool { return a.Choice == "billing" && a.Probabilities["billing"] > 0.6 }},
		{"noul: mentions water -> yes", "The sky is blue and water is wet.", systemone.Noul("Does the text mention water?"),
			func(a systemone.Answer) bool { return a.Noul > 0.6 }},
		{"noul: mentions a dog -> no", "The sky is blue and water is wet.", systemone.Noul("Does the text mention a dog?"),
			func(a systemone.Answer) bool { return a.Noul < 0.2 }},
	}
}

func runSelftest(ctx context.Context, d systemone.Decider) int {
	failed := 0
	for _, c := range selfCases() {
		resp, err := d.Decide(ctx, systemone.Request{State: c.state, Questions: map[string]systemone.Question{"q": c.q}})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		a := resp.Answers["q"]
		ok := c.check(a)
		mark := "PASS"
		if !ok {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("%s  %-32s choice=%q noul=%.4f probs=%v\n", mark, c.name, a.Choice, a.Noul, a.Probabilities)
	}
	if failed > 0 {
		return 1
	}
	return 0
}
