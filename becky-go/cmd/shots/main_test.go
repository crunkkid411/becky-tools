package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestShotSpans(t *testing.T) {
	cases := []struct {
		name    string
		cuts    []float64
		dur     float64
		maxShot float64
		want    [][2]float64
	}{
		{"no cuts, short", nil, 8, 20, [][2]float64{{0, 8}}},
		{"two cuts", []float64{3, 7}, 10, 20, [][2]float64{{0, 3}, {3, 7}, {7, 10}}},
		// 3.5 is < 1 s after 3 and 9.6 is < 1 s before the end: both dropped.
		{"tiny pieces join", []float64{3, 3.5, 9.6}, 10, 20, [][2]float64{{0, 3}, {3, 10}}},
		{"long take split evenly", nil, 50, 20, [][2]float64{{0, 50.0 / 3}, {50.0 / 3, 100.0 / 3}, {100.0 / 3, 50}}},
	}
	for _, c := range cases {
		if got := shotSpans(c.cuts, c.dur, c.maxShot); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSceneCutParse(t *testing.T) {
	log := "[Parsed_showinfo_2 @ 0x1] n:0 pts:1001 pts_time:4.171 duration:1\n[Parsed_showinfo_2 @ 0x1] n:1 pts:3003 pts_time:12.5 duration:1\n"
	var got []string
	for _, m := range ptsRe.FindAllStringSubmatch(log, -1) {
		got = append(got, m[1])
	}
	if !reflect.DeepEqual(got, []string{"4.171", "12.5"}) {
		t.Fatalf("got %v", got)
	}
}

func TestShotPromptNames(t *testing.T) {
	if p := shotPrompt(nil, ""); p != describePrompt {
		t.Fatalf("no names/notes must leave the prompt alone, got %q", p)
	}
	p := shotPrompt([]string{"Hair Jordan"}, "the man with green hair is Hair Jordan")
	for _, want := range []string{"identified these people in this shot (by voice or face): Hair Jordan.", "the man with green hair is Hair Jordan"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
}
