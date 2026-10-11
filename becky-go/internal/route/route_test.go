package route

import (
	"context"
	"errors"
	"testing"

	"becky-go/internal/systemone"
)

var menu = []Step{{"compare", "Gemma compares pictures"}, {"more_frames", "look at nearby frames"}, {"hold", "ask Jordan"}}

type fake struct {
	resp systemone.Response
	err  error
	got  systemone.Request
}

func (f *fake) Decide(_ context.Context, r systemone.Request) (systemone.Response, error) {
	f.got = r
	return f.resp, f.err
}

func answer(choice string, conf float64) systemone.Response {
	return systemone.Response{Answers: map[string]systemone.Answer{
		"next":       {Type: "choice", Choice: choice, Confidence: conf},
		"face_clear": {Type: "noul", Noul: 0.2},
	}}
}

func TestSurePickIsFollowed(t *testing.T) {
	d := next(context.Background(), &fake{resp: answer("more_frames", 0.95)}, Case{Menu: menu, Default: "compare"})
	if d.Step != "more_frames" || d.By != "system-one" || d.Confidence != 0.95 {
		t.Fatalf("got %+v", d)
	}
	if d.Answers["face_clear"].Noul != 0.2 {
		t.Fatalf("extra answer lost: %+v", d.Answers)
	}
}

func TestUnsurePickTakesTheSafeLane(t *testing.T) {
	d := next(context.Background(), &fake{resp: answer("hold", 0.6)}, Case{Menu: menu, Default: "compare"})
	if d.Step != "compare" || d.By != "code-default" || d.Picked != "hold" {
		t.Fatalf("got %+v", d)
	}
}

func TestOffMenuOrDownTakesTheSafeLane(t *testing.T) {
	for _, f := range []*fake{{resp: answer("delete_everything", 0.99)}, {err: errors.New("server down")}} {
		if d := next(context.Background(), f, Case{Menu: menu, Default: "compare"}); d.Step != "compare" || d.By != "code-default" {
			t.Fatalf("got %+v", d)
		}
	}
}

func TestOneStepLeftSkipsTheModel(t *testing.T) {
	f := &fake{err: errors.New("must not be called")}
	if d := next(context.Background(), f, Case{Menu: menu[:1], Default: "hold"}); d.Step != "compare" {
		t.Fatalf("got %+v", d)
	}
	if f.got.Questions != nil {
		t.Fatal("model was asked")
	}
}

func TestMenuStrengthsReachTheModel(t *testing.T) {
	f := &fake{resp: answer("compare", 0.99)}
	next(context.Background(), f, Case{Menu: menu, Default: "hold", Extra: map[string]systemone.Question{"face_clear": systemone.Noul("x")}})
	if string(f.got.Questions["next"].Criteria) != `{"compare":"Gemma compares pictures","more_frames":"look at nearby frames","hold":"ask Jordan"}` {
		t.Fatalf("criteria %s", f.got.Questions["next"].Criteria)
	}
	if _, ok := f.got.Questions["face_clear"]; !ok {
		t.Fatal("extra question not sent")
	}
}

func TestWithout(t *testing.T) {
	if got := Without(menu, "compare", "hold"); len(got) != 1 || got[0].Key != "more_frames" {
		t.Fatalf("got %+v", got)
	}
}
