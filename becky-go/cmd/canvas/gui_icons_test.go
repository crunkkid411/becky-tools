//go:build gui

// gui_icons_test.go — completeness gate: every field in iconSet must be non-nil
// after loadIcons(). A nil entry means widget.NewIcon failed on that constant,
// which means the Material pack constant is wrong/missing or the IconVG data is
// corrupt. Catch it at test time, not at runtime in the user's window.
package main

import (
	"reflect"
	"testing"
)

func TestLoadIcons_AllNonNil(t *testing.T) {
	t.Helper()
	set := loadIcons()
	v := reflect.ValueOf(set)
	typ := v.Type()
	for i := range v.NumField() {
		field := v.Field(i)
		name := typ.Field(i).Name
		if field.IsNil() {
			t.Errorf("iconSet.%s is nil after loadIcons() — Material icon constant missing or IconVG decode failed", name)
		}
	}
}
