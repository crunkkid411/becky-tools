package beckyio

import (
	"flag"
	"reflect"
	"testing"
)

func TestPositional(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	out := fs.String("output", "", "")
	identify := fs.String("identify", "", "")
	add := fs.Bool("add", false, "")
	// The becky-pipeline call shape that broke becky-shots and becky-mediasearch (2026-10-10).
	pos := Positional(fs, []string{"--add", "a.mp4", "--output", "o.json", "--identify=i.json", "b.mp4"})
	if !reflect.DeepEqual(pos, []string{"a.mp4", "b.mp4"}) || *out != "o.json" || *identify != "i.json" || !*add {
		t.Fatalf("pos=%v output=%q identify=%q add=%v", pos, *out, *identify, *add)
	}
}
