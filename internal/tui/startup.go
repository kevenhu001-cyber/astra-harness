package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
)

type startupProgressMsg struct {
	stage string
	done  int
	total int
}

type startupDoneMsg struct {
	eng *engine.Engine
	err error
}

type startupModel struct {
	anim   *asciiAnim
	stage  string
	done   int
	total  int
	width  int
	result *startupDoneMsg
}

func newStartupModel() startupModel {
	return startupModel{anim: newAsciiAnim(framesDots), stage: "Scanning workspace…"}
}

func (m startupModel) Init() tea.Cmd {
	if !motionEnabled() {
		return nil
	}
	return animTickCmd()
}

func (m startupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case animTickMsg:
		m.anim.tick()
		return m, animTickCmd()
	case startupProgressMsg:
		m.stage = msg.stage
		m.done = msg.done
		m.total = msg.total
	case startupDoneMsg:
		m.result = &msg
		return m, tea.Quit
	}
	return m, nil
}

func (m startupModel) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	lines := []string{
		m.anim.view() + " Astra — " + m.stage,
	}
	if m.total > 0 {
		barWidth := 36
		filled := int(float64(m.done) / float64(m.total) * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)
		lines = append(lines, fmt.Sprintf("%s %d/%d", bar, m.done, m.total))
	} else {
		lines = append(lines, "This can take a while on large directories…")
	}
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(strings.Join(lines, "\n"))
}

// RunStartup shows a live progress screen while the engine (and its first
// knowledge index) loads, then hands back the ready engine.
func RunStartup(root string, cfg *engine.Config) (*engine.Engine, error) {
	p := tea.NewProgram(newStartupModel(), tea.WithAltScreen())
	go loadEngineWithProgress(root, cfg, p)
	m, err := p.Run()
	if err != nil {
		return nil, err
	}
	sm, ok := m.(startupModel)
	if !ok || sm.result == nil {
		return nil, fmt.Errorf("startup cancelled")
	}
	return sm.result.eng, sm.result.err
}

func loadEngineWithProgress(root string, cfg *engine.Config, p *tea.Program) {
	p.Send(startupProgressMsg{stage: "Scanning workspace…"})
	lastPct := -1
	progress := func(done, total int) {
		if total <= 0 {
			return
		}
		pct := done * 100 / total
		if pct == lastPct && done != total {
			return
		}
		lastPct = pct
		p.Send(startupProgressMsg{stage: "Building knowledge index…", done: done, total: total})
	}
	eng, err := engine.NewEngineWithProgress(root, cfg, progress)
	p.Send(startupDoneMsg{eng: eng, err: err})
}
