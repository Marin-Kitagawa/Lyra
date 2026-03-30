package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lyra-cli/lyra/internal/ui"
)

type workDoneMsg struct{ output string }

type spinnerModel struct {
	spinner spinner.Model
	label   string
	work    func() string
	output  string
	done    bool
}

func newSpinnerModel(label string, work func() string) spinnerModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(ui.ColorPrimary)
	return spinnerModel{spinner: sp, label: label, work: work}
}

func (m spinnerModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg { return workDoneMsg{output: m.work()} },
	)
}

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case workDoneMsg:
		m.done = true
		m.output = msg.output
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m spinnerModel) View() string {
	if m.done {
		return m.output
	}
	return "\n  " + m.spinner.View() + " " + ui.StyleMuted.Render(m.label) + "\n"
}

// RunWithSpinner shows an animated spinner while work() executes in the background.
// When work() returns its output string, that string is rendered and the program exits.
//
// Falls back to running work() directly (no spinner) when stdout is not a TTY,
// ensuring the function never blocks in pipes, CI, or test-script capture.
func RunWithSpinner(label string, work func() string) {
	if !isTTY() {
		fmt.Print(work())
		return
	}
	p := tea.NewProgram(newSpinnerModel(label, work))
	p.Run() //nolint:errcheck
}
