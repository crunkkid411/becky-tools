// styles.go — the becky-name review-card palette. These are the SAME high-contrast
// hex values as cmd/ask/styles.go (the becky-ask palette): bold pink title, cyan live
// state, green input, amber busy, dim help. Reproduced here (not imported — cmd/ask is
// a separate main package) so the card looks identical to becky-ask. The palette is an
// accessibility AID for Jordan (ACCESSIBILITY.md fact #2): keep colored TUIs, never
// strip color for "accessibility".
package main

import "github.com/charmbracelet/lipgloss"

var (
	// promptStyle / titleBarStyle — the "Who is this?  (1 of 6)" header (bold pink).
	titleBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF75B8")).
			Bold(true).
			Padding(0, 1)

	// targetBarStyle — the BIG facts line ("Person A — seen in 41 clips"); cyan bold so
	// it reads as live state, not chat.
	targetBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00D7FF")).
			Bold(true)

	// systemStyle — the dim quality/skip line ("cohesion 0.71 · voice").
	systemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Italic(true)

	// inputStyle — the one place the human types (green).
	inputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575"))

	// helpStyle — the footer hint (dim gray).
	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	// busyStyle — "enrolling Braxton…" (amber, bold).
	busyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500")).
			Bold(true)

	// warnStyle — the loose-grouping caution ("loose grouping — double-check").
	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500")).
			Italic(true)
)

// looseCohesion is the threshold below which a cluster is flagged as a loose grouping
// to double-check (SPEC §2a + SPEC-PERSON-CLUSTERING §8). A low-cohesion cluster is
// more likely to mix two people, so the human should look harder before naming.
const looseCohesion = 0.50
