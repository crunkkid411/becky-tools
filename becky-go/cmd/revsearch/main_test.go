package main

import (
	"reflect"
	"testing"
)

func TestVerdictNeedsBothModels(t *testing.T) {
	cases := []struct {
		arc, lv float64
		hasLV   bool
		want    string
	}{
		{0.70, 0.45, true, "same"},
		{0.70, 0.10, true, "near"},      // ArcFace alone is not enough when LVFace is there
		{0.30, 0.20, true, "near"},      // LVFace sees a resemblance
		{0.20, 0.05, true, "different"}, // a look-alike the engine called "similar"
		{0.60, 0, false, "same"},        // LVFace not installed: ArcFace bar as before
	}
	for _, c := range cases {
		if got := verdict(c.arc, c.lv, c.hasLV); got != c.want {
			t.Errorf("verdict(%v,%v,%v) = %s, want %s", c.arc, c.lv, c.hasLV, got, c.want)
		}
	}
}

func TestMentionsMatchesNamesInLinksAndTitles(t *testing.T) {
	people := parseNames("John Anthony Clancy|TakingBack2007,Melancholy;Shelby|PopTart Barbie,Shel")
	if got := mentions("Booking photo - https://example.com/John-Anthony-Clancy_123", people); !reflect.DeepEqual(got, []string{"John Anthony Clancy"}) {
		t.Errorf("link with dashes: %v", got)
	}
	if got := mentions("@poptartbarbie on Instagram", people); !reflect.DeepEqual(got, []string{"Shelby"}) {
		t.Errorf("alias without spaces: %v", got)
	}
	if got := mentions("Shell station robbery", people); got != nil {
		t.Errorf("a 4-letter-or-shorter alias must not match inside another word: %v", got)
	}
	// a last name alone names nobody; the "(née ...)" note is not part of the name
	family := parseNames("John Anthony Clancy|;Shelby Alyse Clancy (née Kerberg)|Shelby Alyse Kerberg")
	if got := mentions("The Clancy family", family); got != nil {
		t.Errorf("bare last name matched: %v", got)
	}
	if got := mentions("Shelby Alyse Clancy at the fair", family); !reflect.DeepEqual(got, []string{"Shelby Alyse Clancy (née Kerberg)"}) {
		t.Errorf("full name with a (née) note: %v", got)
	}
}
