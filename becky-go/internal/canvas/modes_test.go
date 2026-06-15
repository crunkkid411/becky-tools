package canvas

import "testing"

func TestModes_fixedOrder(t *testing.T) {
	got := Modes()
	want := []Mode{ModeAsk, ModeVideo, ModeDAW, ModeMIDI, ModeDrum}
	if len(got) != len(want) {
		t.Fatalf("got %d modes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mode[%d] = %q, want %q (order must be stable)", i, got[i], want[i])
		}
	}
}

func TestParseMode_table(t *testing.T) {
	cases := []struct {
		in     string
		want   Mode
		wantOK bool
	}{
		{"ask", ModeAsk, true},
		{"video", ModeVideo, true},
		{"daw", ModeDAW, true},
		{"midi", ModeMIDI, true},
		{"drum", ModeDrum, true},
		{"", "", false},
		{"DAW", "", false}, // case-sensitive on purpose (matches the flag value)
		{"piano", "", false},
	}
	for _, c := range cases {
		got, ok := ParseMode(c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("ParseMode(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestModeNames_mirrorsModes(t *testing.T) {
	names := ModeNames()
	modes := Modes()
	if len(names) != len(modes) {
		t.Fatalf("ModeNames len %d != Modes len %d", len(names), len(modes))
	}
	for i := range modes {
		if names[i] != string(modes[i]) {
			t.Errorf("name[%d]=%q, want %q", i, names[i], string(modes[i]))
		}
	}
}
