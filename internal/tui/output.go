package tui

import tea "github.com/charmbracelet/bubbletea"

// outputModel renders a static string once through BubbleTea and exits.
// Using BubbleTea (even for static content) keeps all terminal output
// consistent and respects BubbleTea's renderer state.
type outputModel struct {
	content string
}

func (m outputModel) Init() tea.Cmd                           { return tea.Quit }
func (m outputModel) Update(tea.Msg) (tea.Model, tea.Cmd)    { return m, nil }
func (m outputModel) View() string                            { return m.content }

// Print renders content once through BubbleTea and exits immediately.
func Print(content string) {
	p := tea.NewProgram(outputModel{content: content})
	p.Run() //nolint:errcheck
}
