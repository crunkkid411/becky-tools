// becky-whois - the "who is this?" window, on demand, and what Jordan answered.
//
//	becky-whois            open the window now (even inside an "Ask me later" pause)
//	becky-whois --list     JSON for agents: open questions + every answer Jordan gave
//
// Tools never need this: becky-identify queues a face it could not settle (after every
// check ran) and opens the window itself (internal/whois). An answer is enrolled into
// that knowledge base's face-prints, so the next run names the person by itself.
package main

import (
	"flag"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/whois"
)

func main() {
	list := flag.Bool("list", false, "print open questions and Jordan's answers as JSON")
	flag.Parse()
	qs, err := whois.Questions()
	if err != nil {
		beckyio.Fatalf("read the questions: %v", err)
	}
	as, err := whois.Answers()
	if err != nil {
		beckyio.Fatalf("read the answers: %v", err)
	}
	if *list {
		open := whois.Pending(qs, as)
		if open == nil {
			open = []whois.Question{}
		}
		if as == nil {
			as = []whois.Answer{}
		}
		beckyio.PrintJSON(struct {
			Folder  string           `json:"folder"`
			Open    []whois.Question `json:"open"`
			Answers []whois.Answer   `json:"answers"`
		}{whois.Dir(), open, as})
		return
	}
	if len(whois.Pending(qs, as)) == 0 {
		beckyio.Logf(true, "becky-whois: no open questions")
		return
	}
	if err := whois.ShowNow(config.Load()); err != nil {
		beckyio.Fatalf("%v", err)
	}
}
