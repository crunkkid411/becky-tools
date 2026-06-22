// tui.go — the high-contrast bubbletea review card. One cluster per screen: the
// representative face (opened beside the TUI via the image shower) + the big readable
// facts + one large green name input. Enter = enroll the whole cluster; s = skip
// (leave it unnamed, never guess); q = quit. Built on the SAME bubbletea + lipgloss +
// bubbles/textinput stack as becky-ask, with the becky-ask palette (styles.go).
//
// Cloud cannot RENDER this (no display); the orchestration it calls lives in
// internal/facenaming and is fully unit-tested headless. This file must COMPILE and the
// no-TTY guard (main.go dispatch) must keep it from launching off a terminal.
package main

import (
	"fmt"
	"os"
	"strings"

	"becky-go/internal/facenaming"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// enrollResultMsg is delivered when one card's enroll finishes (async, off the UI).
type enrollResultMsg struct {
	outcome facenaming.EnrollOutcome
	err     error
}

// cardModel is the review-card state: the ordered clusters, the cursor, the name input,
// the shower + enroller seams, and a small transcript of confirmations.
type cardModel struct {
	clusters []facenaming.Cluster
	idx      int
	input    textinput.Model
	shower   facenaming.ImageShower
	enroller facenaming.Enroller
	kb       string
	cap      int
	busy     bool
	log      []string // one-line confirmations, newest last
	skipped  []string // cluster ids left unnamed
	done     bool
	width    int
}

// runTUI launches the colored card window over the reviewable clusters. Returns a
// process exit code. Called only when stdin is a terminal (main.go dispatch).
func runTUI(rc runConfig, cl facenaming.Clusters) int {
	order := facenaming.WalkOrder(cl, rc.modality, rc.minClips)
	if len(order) == 0 {
		fmt.Println("becky-name: no clusters to review (check --modality / --min-clips).")
		return 0
	}
	m := newCardModel(order, rc.kb, rc.cap, osImageShower{}, newExecEnroller(rc))
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "becky-name error: %v\n", err)
		return 1
	}
	return 0
}

// newCardModel builds the initial card state. Exported-shape inputs (the seams) make it
// constructible with fakes in a (local) test, though rendering is not asserted here.
func newCardModel(clusters []facenaming.Cluster, kb string, cap int, shower facenaming.ImageShower, en facenaming.Enroller) cardModel {
	ti := textinput.New()
	ti.Placeholder = "type the name, Enter to enroll"
	ti.Prompt = "Name ▸ "
	ti.PromptStyle = inputStyle
	ti.TextStyle = inputStyle
	ti.Focus()
	ti.CharLimit = 80
	ti.Width = 40
	return cardModel{
		clusters: clusters, input: ti, shower: shower, enroller: en,
		kb: kb, cap: cap, idx: 0,
	}
}

// Init shows the first cluster's representative image (best-effort).
func (m cardModel) Init() tea.Cmd {
	return m.showCurrent()
}

// showCurrent returns a command that opens the current cluster's representative image
// beside the TUI. Best-effort: a failure becomes a log line, never a crash.
func (m cardModel) showCurrent() tea.Cmd {
	if m.idx >= len(m.clusters) {
		return nil
	}
	rep := m.clusters[m.idx].Representative
	shower := m.shower
	return func() tea.Msg {
		if shower != nil && rep != "" {
			_ = shower.Show(rep)
		}
		return nil
	}
}

// Update handles keys: Enter (enroll), s (skip), q/esc/ctrl-c (quit).
func (m cardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case enrollResultMsg:
		m.busy = false
		if msg.err != nil {
			m.log = append(m.log, "error: "+msg.err.Error())
		} else {
			m.log = append(m.log, msg.outcome.Summary())
		}
		return m.advance()
	case tea.KeyMsg:
		if m.busy {
			return m, nil // ignore input mid-enroll
		}
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.done = true
			return m, tea.Quit
		case tea.KeyEnter:
			name := strings.TrimSpace(m.input.Value())
			if name == "" {
				return m.skip() // blank + Enter = skip, never guess
			}
			return m.enrollCurrent(name)
		case tea.KeyRunes:
			if string(msg.Runes) == "q" && m.input.Value() == "" {
				m.done = true
				return m, tea.Quit
			}
			if string(msg.Runes) == "s" && m.input.Value() == "" {
				return m.skip()
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// enrollCurrent enrolls the current cluster under name (async via the enroller seam).
func (m cardModel) enrollCurrent(name string) (tea.Model, tea.Cmd) {
	if m.idx >= len(m.clusters) {
		return m, tea.Quit
	}
	cl := m.clusters[m.idx]
	en := m.enroller
	kb := m.kb
	cap := m.cap
	m.busy = true
	return m, func() tea.Msg {
		out := facenaming.EnrollCluster(cl, name, kb, en, cap)
		return enrollResultMsg{outcome: out}
	}
}

// skip records the current cluster as unnamed and advances (never invents a name).
func (m cardModel) skip() (tea.Model, tea.Cmd) {
	if m.idx < len(m.clusters) {
		m.skipped = append(m.skipped, m.clusters[m.idx].ClusterID)
	}
	return m.advance()
}

// advance moves to the next cluster, resets the input, and shows the next image; quits
// when the list is exhausted.
func (m cardModel) advance() (tea.Model, tea.Cmd) {
	m.idx++
	m.input.SetValue("")
	if m.idx >= len(m.clusters) {
		m.done = true
		return m, tea.Quit
	}
	return m, m.showCurrent()
}

// View renders the current card with the high-contrast palette.
func (m cardModel) View() string {
	if m.done || m.idx >= len(m.clusters) {
		return m.summaryView()
	}
	cl := m.clusters[m.idx]
	var b strings.Builder
	b.WriteString(titleBarStyle.Render(fmt.Sprintf("Who is this?   (%d of %d)", m.idx+1, len(m.clusters))))
	b.WriteString("\n\n")
	if cl.Representative != "" {
		b.WriteString(systemStyle.Render("face opened in viewer: "+cl.Representative) + "\n\n")
	}
	b.WriteString(targetBarStyle.Render(factsLine(cl)) + "\n")
	b.WriteString(systemStyle.Render(qualityLine(cl)) + "\n")
	if cl.Cohesion > 0 && cl.Cohesion < looseCohesion {
		b.WriteString(warnStyle.Render("loose grouping — double-check this is one person") + "\n")
	}
	b.WriteString("\n")
	if m.busy {
		b.WriteString(busyStyle.Render("enrolling "+strings.TrimSpace(m.input.Value())+"…") + "\n")
	} else {
		b.WriteString(m.input.View() + "\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("Enter = enroll   ·   s = skip   ·   q = quit"))
	if len(m.log) > 0 {
		b.WriteString("\n\n" + systemStyle.Render(m.log[len(m.log)-1]))
	}
	return b.String()
}

// summaryView is shown after the last card / on quit.
func (m cardModel) summaryView() string {
	var b strings.Builder
	b.WriteString(titleBarStyle.Render("Naming complete") + "\n\n")
	for _, line := range m.log {
		b.WriteString(targetBarStyle.Render("✓ ") + line + "\n")
	}
	if len(m.skipped) > 0 {
		b.WriteString(systemStyle.Render("skipped (left unnamed): "+strings.Join(m.skipped, ", ")) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("press q to close") + "\n")
	return b.String()
}

// factsLine is the BIG cyan facts line ("Person A — seen in 41 clips (38 files)").
func factsLine(cl facenaming.Cluster) string {
	person := personLabel(cl.ClusterID)
	return fmt.Sprintf("%s — seen in %d clip(s) (%d file(s))", person, cl.MemberCount, cl.DistinctSourceFiles)
}

// qualityLine is the dim quality/modality line ("cohesion 0.71 · voice").
func qualityLine(cl facenaming.Cluster) string {
	return fmt.Sprintf("cohesion %.2f · %s", cl.Cohesion, cl.Modality)
}

// personLabel turns a cluster id like "face-A" into "Person A" for the human-facing
// facts line; falls back to the raw id when it does not match the pattern.
func personLabel(clusterID string) string {
	if i := strings.LastIndex(clusterID, "-"); i >= 0 && i+1 < len(clusterID) {
		return "Person " + clusterID[i+1:]
	}
	return clusterID
}
