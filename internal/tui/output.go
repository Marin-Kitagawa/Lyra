package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// outputModel renders a static string once through BubbleTea and exits.
type outputModel struct{ content string }

func (m outputModel) Init() tea.Cmd                        { return tea.Quit }
func (m outputModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m outputModel) View() string                         { return m.content }

// Print renders content through BubbleTea when stdout is a TTY.
// Falls back to plain fmt.Print when output is piped or redirected,
// ensuring the function never blocks in non-interactive environments.
func Print(content string) {
	if !isTTY() {
		fmt.Print(content)
		return
	}
	p := tea.NewProgram(outputModel{content: content})
	p.Run() //nolint:errcheck
}
