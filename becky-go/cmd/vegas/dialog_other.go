//go:build !windows

package main

import "errors"

// clickDialogButton: VEGAS only runs on Windows; this keeps CI's Linux build honest.
func clickDialogButton(pid int, button, title string) (string, error) {
	return "", errors.New("dialog_click only works on Windows")
}
