package systemone

import (
	"encoding/json"
	"testing"
)

// Laya's option order IS the answer index, so Choice must keep the caller's
// order in the JSON it sends (a Go map would sort it alphabetically).
func TestChoiceKeepsOptionOrder(t *testing.T) {
	q := Choice("pick", Option{Key: "zeta", Desc: "last letter"}, Option{Key: "alpha"})
	want := `{"zeta":"last letter","alpha":null}`
	if string(q.Criteria) != want {
		t.Fatalf("criteria = %s, want %s", q.Criteria, want)
	}
	b, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"type":"choice","instructions":"pick","criteria":{"zeta":"last letter","alpha":null}}` {
		t.Fatalf("marshalled = %s", got)
	}
}

func TestNoulOmitsCriteria(t *testing.T) {
	b, _ := json.Marshal(Noul("is it?"))
	if string(b) != `{"type":"noul","instructions":"is it?"}` {
		t.Fatalf("got %s", b)
	}
}

func TestAvailableReportsMissingModel(t *testing.T) {
	r := Runner{ModelDir: t.TempDir(), Python: "nope.exe"}
	if err := r.Available(); err == nil {
		t.Fatal("expected an error for an empty model dir")
	}
}
