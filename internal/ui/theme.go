package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Color palette - pink/purple/lavender theme
var (
	ColorPrimary   = lipgloss.Color("#FF69B4") // hot pink
	ColorSecondary = lipgloss.Color("#B57BEE") // lavender
	ColorAccent    = lipgloss.Color("#00CED1") // cyan
	ColorSuccess   = lipgloss.Color("#98FF98") // mint
	ColorWarning   = lipgloss.Color("#FFD700") // yellow
	ColorError     = lipgloss.Color("#FF4444") // red
	ColorMuted     = lipgloss.Color("#888888") // gray
	ColorWhite     = lipgloss.Color("#FFFFFF")
	ColorDim       = lipgloss.Color("#AAAAAA")
)

// Base styles
var (
	StylePrimary = lipgloss.NewStyle().Foreground(ColorPrimary)
	StyleSecondary = lipgloss.NewStyle().Foreground(ColorSecondary)
	StyleAccent  = lipgloss.NewStyle().Foreground(ColorAccent)
	StyleSuccess = lipgloss.NewStyle().Foreground(ColorSuccess)
	StyleWarning = lipgloss.NewStyle().Foreground(ColorWarning)
	StyleError   = lipgloss.NewStyle().Foreground(ColorError)
	StyleMuted   = lipgloss.NewStyle().Foreground(ColorMuted)
	StyleBold    = lipgloss.NewStyle().Bold(true)
	StyleDim     = lipgloss.NewStyle().Foreground(ColorDim)
)

// Box styles
var (
	StyleInfoBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorSecondary).
		Padding(0, 1)

	StyleErrorBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorError).
		Padding(0, 1)

	StyleSuccessBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorSuccess).
		Padding(0, 1)

	StyleHeaderBox = lipgloss.NewStyle().
		Background(ColorPrimary).
		Foreground(ColorWhite).
		Bold(true).
		Padding(0, 2)
)

// Icons
const (
	IconError   = "✗"
	IconSuccess = "✓"
	IconInfo    = "ℹ"
	IconWarning = "⚠"
	IconArrow   = "→"
	IconFile    = "📄"
	IconDir     = "📁"
	IconLink    = "🔗"
)

// RenderHeader renders a styled header
func RenderHeader(title string) string {
	return StyleHeaderBox.Render(title)
}

// RenderError renders a styled error message
func RenderError(msg string) string {
	icon := StyleError.Render(IconError)
	text := StyleError.Render(msg)
	return fmt.Sprintf("%s %s", icon, text)
}

// RenderSuccess renders a styled success message
func RenderSuccess(msg string) string {
	icon := StyleSuccess.Render(IconSuccess)
	text := StyleSuccess.Render(msg)
	return fmt.Sprintf("%s %s", icon, text)
}

// RenderInfo renders a styled info message
func RenderInfo(msg string) string {
	icon := StyleAccent.Render(IconInfo)
	text := msg
	return fmt.Sprintf("%s %s", icon, text)
}

// RenderWarning renders a styled warning message
func RenderWarning(msg string) string {
	icon := StyleWarning.Render(IconWarning)
	text := StyleWarning.Render(msg)
	return fmt.Sprintf("%s %s", icon, text)
}

// RenderInfoBox renders content in a styled info box
func RenderInfoBox(content string) string {
	return StyleInfoBox.Render(content)
}

// RenderKeyValue renders a key-value pair
func RenderKeyValue(key, value string) string {
	k := StyleSecondary.Render(key + ":")
	v := value
	return fmt.Sprintf("  %-20s %s", k, v)
}

// RenderLabel renders a label
func RenderLabel(label string) string {
	return StylePrimary.Bold(true).Render(label)
}
