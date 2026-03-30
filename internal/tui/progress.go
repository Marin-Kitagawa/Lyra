package tui

import (
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dustin/go-humanize"
	"github.com/lyra-cli/lyra/internal/ui"
)

// Entry tracks a single file transfer.
type Entry struct {
	Name    string
	Total   int64
	BytesCh chan int64 // transfer goroutine writes byte counts here
	DoneCh  chan error // transfer goroutine sends nil or error when finished
}

// NewEntry creates a tracked transfer entry.
func NewEntry(name string, total int64) *Entry {
	return &Entry{
		Name:    name,
		Total:   total,
		BytesCh: make(chan int64, 1024),
		DoneCh:  make(chan error, 1),
	}
}

// Report sends n bytes of progress (non-blocking; drops if full).
func (e *Entry) Report(n int64) {
	select {
	case e.BytesCh <- n:
	default:
	}
}

// Finish signals that this entry's transfer is done.
func (e *Entry) Finish(err error) {
	e.DoneCh <- err
}

// --------- internal model ---------

type entryState struct {
	entry    *Entry
	bar      *progress.Model
	done     int64
	speed    float64
	eta      time.Duration
	finished bool
	err      error
	lastTick time.Time
}

type progressModel struct {
	entries []*entryState
	spinner spinner.Model
	width   int
	paused  bool
	allDone bool
	sigCh   chan os.Signal
	pauseFn func()
	op      string
}

type tickMsg time.Time
type sigPauseMsg struct{}

func doTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func listenForSig(ch <-chan os.Signal) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return sigPauseMsg{}
	}
}

func newProgressModel(entries []*Entry, op string, pauseFn func(), sigCh chan os.Signal) progressModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(ui.ColorPrimary)

	states := make([]*entryState, len(entries))
	for i, e := range entries {
		bar := progress.New(
			progress.WithGradient(string(ui.ColorPrimary), string(ui.ColorSecondary)),
			progress.WithoutPercentage(),
			progress.WithWidth(50),
		)
		states[i] = &entryState{
			entry:    e,
			bar:      &bar,
			lastTick: time.Now(),
		}
	}
	return progressModel{
		entries: states,
		spinner: sp,
		width:   80,
		sigCh:   sigCh,
		pauseFn: pauseFn,
		op:      op,
	}
}

func (m progressModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, doTick(), listenForSig(m.sigCh))
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		bw := msg.Width - 35
		if bw < 10 {
			bw = 10
		}
		for _, e := range m.entries {
			e.bar.Width = bw
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		var cmds []tea.Cmd
		for _, e := range m.entries {
			newBar, cmd := e.bar.Update(msg)
			nb := newBar.(progress.Model)
			e.bar = &nb
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case tickMsg:
		now := time.Now()
		var cmds []tea.Cmd
		doneCount := 0

		for _, e := range m.entries {
			if e.finished {
				doneCount++
				continue
			}

			// Check for finish signal (non-blocking)
			select {
			case err := <-e.entry.DoneCh:
				e.finished = true
				e.err = err
				cmd := e.bar.SetPercent(1.0)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				doneCount++
				continue
			default:
			}

			// Drain bytes channel (non-blocking)
			var newBytes int64
		drain:
			for {
				select {
				case n := <-e.entry.BytesCh:
					newBytes += n
				default:
					break drain
				}
			}

			e.done += newBytes

			// EMA speed
			dt := now.Sub(e.lastTick).Seconds()
			if dt > 0 && newBytes > 0 {
				instant := float64(newBytes) / dt
				if e.speed == 0 {
					e.speed = instant
				} else {
					e.speed = 0.8*e.speed + 0.2*instant
				}
			}
			e.lastTick = now

			// ETA
			if e.speed > 0 && e.entry.Total > 0 {
				remaining := float64(e.entry.Total - e.done)
				secs := remaining / e.speed
				e.eta = time.Duration(secs * float64(time.Second))
			}

			// Update bar percent
			if e.entry.Total > 0 {
				pct := math.Min(1.0, float64(e.done)/float64(e.entry.Total))
				cmd := e.bar.SetPercent(pct)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}

		if doneCount == len(m.entries) {
			m.allDone = true
			return m, tea.Quit
		}
		cmds = append(cmds, doTick())
		return m, tea.Batch(cmds...)

	case sigPauseMsg:
		m.paused = true
		if m.pauseFn != nil {
			m.pauseFn()
		}
		return m, tea.Quit

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.paused = true
			if m.pauseFn != nil {
				m.pauseFn()
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m progressModel) View() string {
	if m.allDone {
		return ""
	}

	var sb strings.Builder
	header := ui.StylePrimary.Bold(true).Render("✨ lyra") +
		ui.StyleMuted.Render(fmt.Sprintf(" — %s", m.op))
	sb.WriteString("\n  " + header + "\n\n")

	for _, e := range m.entries {
		nameStyle := lipgloss.NewStyle().Foreground(ui.ColorWhite).Bold(true)
		sb.WriteString("  " + nameStyle.Render(truncateMiddle(e.entry.Name, 40)) + "\n")

		if e.finished {
			if e.err != nil {
				sb.WriteString("  " + ui.StyleError.Render("✗ "+e.err.Error()) + "\n\n")
			} else {
				sb.WriteString("  " + e.bar.View() + "\n")
				sb.WriteString("  " + ui.StyleSuccess.Render("✓ done") + "\n\n")
			}
		} else if e.entry.Total > 0 {
			pct := 0.0
			if e.entry.Total > 0 {
				pct = float64(e.done) / float64(e.entry.Total) * 100
			}
			stats := fmt.Sprintf("%s / %s  %s  %s  %.1f%%",
				ui.StyleSecondary.Render(humanize.Bytes(uint64(e.done))),
				ui.StyleMuted.Render(humanize.Bytes(uint64(e.entry.Total))),
				ui.StyleAccent.Render(humanize.Bytes(uint64(e.speed))+"/s"),
				ui.StyleMuted.Render("ETA "+fmtETA(e.eta)),
				pct,
			)
			sb.WriteString("  " + e.bar.View() + "\n")
			sb.WriteString("  " + stats + "\n\n")
		} else {
			// unknown size
			sb.WriteString("  " + m.spinner.View() + " " +
				ui.StyleSecondary.Render(humanize.Bytes(uint64(e.done))) + "  " +
				ui.StyleAccent.Render(humanize.Bytes(uint64(e.speed))+"/s") + "\n\n")
		}
	}

	if m.paused {
		sb.WriteString("  " + ui.StyleWarning.Render("⚠ Paused — press Ctrl+C again to abort") + "\n")
	}
	return sb.String()
}

func fmtETA(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func truncateMiddle(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return "…" + string(r[len(r)-(max-1):])
}

// --------- public API ---------

// ProgressProgram manages a BubbleTea progress display for one or more file transfers.
type ProgressProgram struct {
	entries []*Entry
	op      string
	sigCh   chan os.Signal
	pauseFn func()
}

// NewProgressProgram creates a new ProgressProgram.
// op is a short description like "copying" or "moving".
// pauseFn is called when the user presses Ctrl+C.
func NewProgressProgram(op string, pauseFn func()) *ProgressProgram {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	return &ProgressProgram{
		op:      op,
		sigCh:   sigCh,
		pauseFn: pauseFn,
	}
}

// Add registers a new transfer entry and returns it so the caller
// can call Report/Finish on it from a goroutine.
func (pp *ProgressProgram) Add(name string, total int64) *Entry {
	e := NewEntry(name, total)
	pp.entries = append(pp.entries, e)
	return e
}

// Run starts the BubbleTea progress loop and blocks until all entries finish or
// the user pauses with Ctrl+C. Returns true if the transfer was paused.
//
// When stdout is not a TTY (pipe, redirect, test capture) the BubbleTea renderer
// is skipped entirely — transfers still complete, but no progress UI is shown.
func (pp *ProgressProgram) Run() (paused bool) {
	if len(pp.entries) == 0 {
		return false
	}
	if !isTTY() {
		// Drain all entries without any UI — wait for each DoneCh.
		for _, e := range pp.entries {
			<-e.DoneCh
		}
		return false
	}
	m := newProgressModel(pp.entries, pp.op, pp.pauseFn, pp.sigCh)
	p := tea.NewProgram(m)
	finalModel, _ := p.Run()
	if fm, ok := finalModel.(progressModel); ok {
		return fm.paused
	}
	return false
}
