package canvas

import (
	"fmt"
	"testing"
)

// TestLaneName covers the in-range and out-of-range cases for the drum lane name lookup.
func TestLaneName(t *testing.T) {
	tests := []struct {
		lane int
		want string
	}{
		{0, "kick"},
		{1, "snare"},
		{2, "hat"},
		{3, "clap"},
		{4, "lane-4"},   // out of range → numbered fallback
		{-1, "lane--1"}, // negative → numbered fallback
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("lane%d", tc.lane), func(t *testing.T) {
			if got := LaneName(tc.lane); got != tc.want {
				t.Errorf("LaneName(%d) = %q, want %q", tc.lane, got, tc.want)
			}
		})
	}
}

// TestMapDrumToggle verifies the deterministic before→after mapping for every
// combination of WasOn/IsOn and representative lane/step values.
func TestMapDrumToggle(t *testing.T) {
	tests := []struct {
		name string
		edit DrumCellEdit
		want DrumToggleArgs
	}{
		{
			name: "kick step-0 off→on",
			edit: DrumCellEdit{Lane: 0, Step: 0, WasOn: false, IsOn: true},
			want: DrumToggleArgs{Scope: "kick", Field: "step/0", Auto: "off", Fixed: "on"},
		},
		{
			name: "kick step-0 on→off",
			edit: DrumCellEdit{Lane: 0, Step: 0, WasOn: true, IsOn: false},
			want: DrumToggleArgs{Scope: "kick", Field: "step/0", Auto: "on", Fixed: "off"},
		},
		{
			name: "snare step-4 off→on",
			edit: DrumCellEdit{Lane: 1, Step: 4, WasOn: false, IsOn: true},
			want: DrumToggleArgs{Scope: "snare", Field: "step/4", Auto: "off", Fixed: "on"},
		},
		{
			name: "hat step-15 on→off",
			edit: DrumCellEdit{Lane: 2, Step: 15, WasOn: true, IsOn: false},
			want: DrumToggleArgs{Scope: "hat", Field: "step/15", Auto: "on", Fixed: "off"},
		},
		{
			name: "clap step-8 off→on",
			edit: DrumCellEdit{Lane: 3, Step: 8, WasOn: false, IsOn: true},
			want: DrumToggleArgs{Scope: "clap", Field: "step/8", Auto: "off", Fixed: "on"},
		},
		{
			name: "out-of-range lane step-0",
			edit: DrumCellEdit{Lane: 7, Step: 0, WasOn: false, IsOn: true},
			want: DrumToggleArgs{Scope: "lane-7", Field: "step/0", Auto: "off", Fixed: "on"},
		},
		{
			name: "same state (no-op toggle still logged)",
			edit: DrumCellEdit{Lane: 0, Step: 3, WasOn: true, IsOn: true},
			want: DrumToggleArgs{Scope: "kick", Field: "step/3", Auto: "on", Fixed: "on"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MapDrumToggle(tc.edit)
			if got != tc.want {
				t.Errorf("MapDrumToggle(%+v)\n  got  %+v\n  want %+v", tc.edit, got, tc.want)
			}
		})
	}
}

// TestAppendDrumEdit verifies the full pipeline: DrumCellEdit → log function call.
func TestAppendDrumEdit(t *testing.T) {
	type callArgs struct {
		path, tool, scope, field, auto, fixed string
	}
	tests := []struct {
		name    string
		logPath string
		edit    DrumCellEdit
		want    callArgs
		logErr  error
		wantErr bool
	}{
		{
			name:    "kick step-0 off→on logged correctly",
			logPath: "/tmp/canvas.corrections.jsonl",
			edit:    DrumCellEdit{Lane: 0, Step: 0, WasOn: false, IsOn: true},
			want: callArgs{
				path:  "/tmp/canvas.corrections.jsonl",
				tool:  "canvas",
				scope: "kick",
				field: "step/0",
				auto:  "off",
				fixed: "on",
			},
		},
		{
			name:    "log error is propagated to caller",
			logPath: "/no/such/path.jsonl",
			edit:    DrumCellEdit{Lane: 1, Step: 4, WasOn: true, IsOn: false},
			logErr:  fmt.Errorf("disk full"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got callArgs
			stub := func(path, tool, scope, field, auto, fixed string) error {
				got = callArgs{path, tool, scope, field, auto, fixed}
				return tc.logErr
			}
			err := AppendDrumEdit(tc.logPath, tc.edit, stub)
			if (err != nil) != tc.wantErr {
				t.Fatalf("AppendDrumEdit err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("log args\n  got  %+v\n  want %+v", got, tc.want)
			}
		})
	}
}
