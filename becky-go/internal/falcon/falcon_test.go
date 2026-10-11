package falcon

import "testing"

func TestParseKeepsGoodLinesOnly(t *testing.T) {
	got := parse(`noise
{"ok": true, "frame": "a.jpg", "count": 1, "boxes": [{"x":0.1,"y":0.2,"w":0.3,"h":0.6,"confidence":0.97}]}
{"ok": false, "frame": "b.jpg", "error": "unreadable"}
`)
	if len(got) != 1 || len(got["a.jpg"]) != 1 {
		t.Fatalf("got %+v", got)
	}
	if b := got["a.jpg"][0]; b.X != 0.1 || b.H != 0.6 || b.Confidence != 0.97 {
		t.Fatalf("box %+v", b)
	}
}
