package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/dustin/go-humanize"
	"github.com/lyra-cli/lyra/internal/ui"
)

// SummaryRecord holds the result of a single file operation.
type SummaryRecord struct {
	Name     string
	Op       string
	Err      error
	Size     int64 // -1 if N/A
	Duration time.Duration
}

// ShowSummary renders a summary table to stdout using bubbles/table.
// Does nothing if records has fewer than 2 entries.
// Falls back to plain-text lines when stdout is not a TTY.
func ShowSummary(records []SummaryRecord) {
	if len(records) < 2 {
		return
	}
	if !isTTY() {
		for _, r := range records {
			status := "OK"
			if r.Err != nil {
				status = "FAIL: " + r.Err.Error()
			}
			fmt.Printf("  %-30s  %-8s  %s\n", r.Name, r.Op, status)
		}
		return
	}

	// Build rows
	rows := make([]table.Row, len(records))
	ok, fail := 0, 0
	for i, r := range records {
		status := ui.StyleSuccess.Render("✓ OK")
		if r.Err != nil {
			status = ui.StyleError.Render("✗ " + truncateStr(r.Err.Error(), 20))
			fail++
		} else {
			ok++
		}

		sizeStr := "—"
		if r.Size >= 0 {
			sizeStr = humanize.Bytes(uint64(r.Size))
		}

		durStr := "—"
		if r.Duration > 0 {
			durStr = fmtDuration(r.Duration)
		}

		rows[i] = table.Row{
			truncateStr(r.Name, 28),
			r.Op,
			status,
			sizeStr,
			durStr,
		}
	}

	// Table style
	baseStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(ui.ColorSecondary)

	headerStyle := lipgloss.NewStyle().
		Foreground(ui.ColorSecondary).
		Bold(true)

	cellStyle := lipgloss.NewStyle().
		Foreground(ui.ColorWhite)

	selectedStyle := lipgloss.NewStyle()

	s := table.Styles{
		Header:   headerStyle,
		Cell:     cellStyle,
		Selected: selectedStyle,
	}

	cols := []table.Column{
		{Title: "File", Width: 28},
		{Title: "Op", Width: 8},
		{Title: "Status", Width: 22},
		{Title: "Size", Width: 10},
		{Title: "Duration", Width: 10},
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithHeight(len(rows)+1),
		table.WithFocused(false),
	)
	t.SetStyles(s)

	// Header line
	total := len(records)
	headline := fmt.Sprintf("  %s  %s · %s · %s",
		ui.StylePrimary.Bold(true).Render("Summary"),
		ui.StyleMuted.Render(fmt.Sprintf("%d operations", total)),
		ui.StyleSuccess.Render(fmt.Sprintf("%d succeeded", ok)),
		ui.StyleError.Render(fmt.Sprintf("%d failed", fail)),
	)

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(headline + "\n\n")
	sb.WriteString(baseStyle.Render(t.View()))
	sb.WriteString("\n")
	fmt.Println(sb.String())
}

func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

func truncateStr(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
