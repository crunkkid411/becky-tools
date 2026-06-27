package main

import (
	"reflect"
	"testing"
)

func TestSplitTerms(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string{}},
		{"blanks only", "   \t  ", []string{}},
		{"single", "cat", []string{"cat"}},
		{"multi collapses spaces", "  cat   near  camera ", []string{"cat", "near", "camera"}},
		{"tabs and newlines", "threat\tto\nhost", []string{"threat", "to", "host"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitTerms(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("splitTerms(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
