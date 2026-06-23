//go:build gui

package main

import (
	"reflect"
	"testing"
)

func TestParseWxH(t *testing.T) {
	cases := []struct {
		in   string
		w, h int
		ok   bool
	}{
		{"1280x800", 1280, 800, true},
		{"1280X800", 1280, 800, true}, // case-insensitive
		{" 640 x 480 ", 640, 480, true},
		{"abc", 0, 0, false},
		{"100", 0, 0, false},
		{"0x100", 0, 0, false},  // non-positive rejected
		{"100x-1", 0, 0, false}, // negative rejected
		{"100x100x1", 0, 0, false},
	}
	for _, c := range cases {
		w, h, ok := parseWxH(c.in)
		if w != c.w || h != c.h || ok != c.ok {
			t.Errorf("parseWxH(%q) = (%d,%d,%v); want (%d,%d,%v)", c.in, w, h, ok, c.w, c.h, c.ok)
		}
	}
}

func TestRenderFrameRequested(t *testing.T) {
	cases := []struct {
		name string
		args []string
		out  string
		ok   bool
	}{
		{"flag with path", []string{"--render-frame", "out.png"}, "out.png", true},
		{"flag bare -> default", []string{"--render-frame"}, defaultRenderOut, true},
		{"flag then another flag -> default", []string{"--render-frame", "--size"}, defaultRenderOut, true},
		{"inline value", []string{"--render-frame=foo.png"}, "foo.png", true},
		{"inline empty -> default", []string{"--render-frame="}, defaultRenderOut, true},
		{"absent", []string{"project.json"}, "", false},
		{"path then target", []string{"--render-frame", "daw.png", "project.json"}, "daw.png", true},
	}
	for _, c := range cases {
		out, ok := renderFrameRequested(c.args)
		if out != c.out || ok != c.ok {
			t.Errorf("%s: renderFrameRequested(%v) = (%q,%v); want (%q,%v)", c.name, c.args, out, ok, c.out, c.ok)
		}
	}
}

func TestRenderFrameSize(t *testing.T) {
	cases := []struct {
		args []string
		w, h int
	}{
		{[]string{"--size", "1280x800"}, 1280, 800},
		{[]string{"--size=640x480"}, 640, 480},
		{[]string{"--render-frame", "x.png"}, defaultRenderWidth, defaultRenderHeight}, // no --size -> default
		{[]string{"--size", "garbage"}, defaultRenderWidth, defaultRenderHeight},       // bad -> default
	}
	for _, c := range cases {
		w, h := renderFrameSize(c.args)
		if w != c.w || h != c.h {
			t.Errorf("renderFrameSize(%v) = (%d,%d); want (%d,%d)", c.args, w, h, c.w, c.h)
		}
	}
}

func TestStripRenderArgs(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"--render-frame", "out.png", "project.json"}, []string{"project.json"}},
		{[]string{"--size", "1280x800", "project.json"}, []string{"project.json"}},
		{[]string{"--render-frame=out.png", "project.json"}, []string{"project.json"}},
		{[]string{"--render-frame", "out.png"}, []string{}},
		{[]string{"project.json"}, []string{"project.json"}},
	}
	for _, c := range cases {
		got := stripRenderArgs(c.args)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("stripRenderArgs(%v) = %v; want %v", c.args, got, c.want)
		}
	}
}
