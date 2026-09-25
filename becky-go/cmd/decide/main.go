// becky-decide — local System One decisions with Laya (Jev-compatible, ONNX, CPU).
//
//	becky-decide < request.json          # Jev system_one request in, response out
//	becky-decide --selftest              # three known-answer checks; exit 1 on a miss
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
	selftest := flag.Bool("selftest", false, "run known-answer checks against the local model")
	flag.Parse()

	r := systemone.New()
	if err := r.Available(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	ctx := context.Background()

	if *selftest {
		os.Exit(runSelftest(ctx, r))
	}

	var req systemone.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil || len(req.Questions) == 0 {
		fmt.Fprintln(os.Stderr, "usage: becky-decide < request.json   (needs a state and at least one question)")
		os.Exit(2)
	}
	resp, err := r.Decide(ctx, req)
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

func runSelftest(ctx context.Context, r systemone.Runner) int {
	cases := selfCases()
	reqs := make([]systemone.Request, len(cases))
	for i, c := range cases {
		reqs[i] = systemone.Request{State: c.state, Questions: map[string]systemone.Question{"q": c.q}}
	}
	resps, err := r.DecideBatch(ctx, reqs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	failed := 0
	for i, c := range cases {
		a := resps[i].Answers["q"]
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
