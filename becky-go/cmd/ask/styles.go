// styles.go — the look of the becky-ask chat window. Visual styling is carried
// forward from the original becky-ask (cmd/ask in the old becky-go): a bold pink
// "Ask Becky:" prompt, green-on-black input, amber responses, and a dim gray help
// footer ("Type your question and press Enter | q: quit"). Kept in one place so the
// palette is consistent and easy to retune.
package main

import "github.com/charmbracelet/lipgloss"

// The becky-ask palette. These hex values are the ones the original tool used; the
// only intent here is to reproduce that familiar green-on-black look with a pink
// header, now rendered through a scrollback viewport instead of a single static line.
var (
	// promptStyle renders the "Ask Becky:" header (bold pink, like the original).
	promptStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF75B8"))

	// userStyle renders what the human typed, echoed into the scrollback (green).
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575")).
			Bold(true)

	// beckyStyle renders becky's replies in the scrollback (amber/orange).
	beckyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500"))

	// systemStyle renders meta/system notes (dim, distinct from a real answer).
	systemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Italic(true)

	// inputStyle colors the live input line (green, matching the original prompt).
	inputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575"))

	// helpStyle renders the footer hint (dim gray), same copy as the original.
	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	// titleBarStyle frames the header row across the top of the window.
	titleBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF75B8")).
			Bold(true).
			Padding(0, 1)

	// targetBarStyle renders the "Target: <file>" context bar shown when a file or
	// folder has been dropped/typed (cyan, so it reads as live state, not chat).
	targetBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00D7FF")).
			Bold(true)

	// actionRowStyle renders an unselected quick-action row (dim green key + label).
	actionRowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575"))

	// busyStyle renders the "running…" status while a tool executes (amber, bold).
	busyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500")).
			Bold(true)
)

// Speaker labels prefixed onto each scrollback line so a human can tell who said
// what at a glance. Plain words, per the forensic-output philosophy (clarity first).
const (
	labelUser   = "You"
	labelBecky  = "Becky"
	labelSystem = "·"
)
