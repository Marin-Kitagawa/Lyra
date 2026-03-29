package progress

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// Manager manages progress bars for transfers
type Manager struct {
	container *mpb.Progress
	bars      []*mpb.Bar
	done      chan struct{}
}

// NewManager creates a new progress bar manager
func NewManager() *Manager {
	p := mpb.New(
		mpb.WithWidth(60),
		mpb.WithRefreshRate(100*time.Millisecond),
	)
	return &Manager{
		container: p,
		done:      make(chan struct{}),
	}
}

// AddBar adds a new progress bar for a file transfer
func (m *Manager) AddBar(name string, total int64) *mpb.Bar {
	bar := m.container.New(total,
		mpb.BarStyle().Lbound("").Filler("█").Tip("▓").Padding("░").Rbound(""),
		mpb.PrependDecorators(
			decor.Name(truncateName(name, 20), decor.WC{W: 22, C: decor.DindentRight | decor.DextraSpace}),
			decor.CountersKibiByte("% .2f / % .2f", decor.WCSyncWidth),
		),
		mpb.AppendDecorators(
			decor.EwmaETA(decor.ET_STYLE_GO, 30, decor.WCSyncWidth),
			decor.Name(" "),
			decor.EwmaSpeed(decor.SizeB1024(0), "% .2f", 30, decor.WCSyncWidth),
			decor.Name(" "),
			decor.OnComplete(
				decor.NewPercentage("%.2f", decor.WCSyncWidth),
				"done",
			),
		),
	)
	m.bars = append(m.bars, bar)
	return bar
}

// AddSpinner adds a spinner bar (for unknown size)
func (m *Manager) AddSpinner(name string) *mpb.Bar {
	bar := m.container.New(-1,
		mpb.SpinnerStyle("⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"),
		mpb.PrependDecorators(
			decor.Name(truncateName(name, 20), decor.WC{W: 22, C: decor.DindentRight | decor.DextraSpace}),
			decor.CurrentKibiByte("% .2f"),
		),
		mpb.AppendDecorators(
			decor.EwmaSpeed(decor.SizeB1024(0), "% .2f", 30),
		),
	)
	m.bars = append(m.bars, bar)
	return bar
}

// Wait waits for all bars to finish
func (m *Manager) Wait() {
	m.container.Wait()
}

// SetupSignalHandler sets up SIGINT handler for graceful pause
func (m *Manager) SetupSignalHandler(onPause func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case <-sigCh:
			if onPause != nil {
				onPause()
			}
		case <-m.done:
			return
		}
	}()
}

// Stop stops signal handling
func (m *Manager) Stop() {
	select {
	case <-m.done:
	default:
		close(m.done)
	}
}

// ProgressReader wraps an io.Reader with progress reporting
type ProgressReader struct {
	reader    io.Reader
	bar       *mpb.Bar
	bytesRead int64
	startTime time.Time
}

// NewProgressReader creates a new progress-tracking reader
func NewProgressReader(r io.Reader, bar *mpb.Bar) *ProgressReader {
	return &ProgressReader{
		reader:    r,
		bar:       bar,
		startTime: time.Now(),
	}
}

// Read implements io.Reader
func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.bytesRead += int64(n)
		elapsed := time.Since(pr.startTime)
		if elapsed > 0 {
			pr.bar.EwmaIncrInt64(int64(n), elapsed)
			pr.startTime = time.Now()
		} else {
			pr.bar.IncrInt64(int64(n))
		}
	}
	return n, err
}

// ProgressWriter wraps an io.Writer with progress reporting
type ProgressWriter struct {
	writer       io.Writer
	bar          *mpb.Bar
	bytesWritten int64
	startTime    time.Time
}

// NewProgressWriter creates a new progress-tracking writer
func NewProgressWriter(w io.Writer, bar *mpb.Bar) *ProgressWriter {
	return &ProgressWriter{
		writer:    w,
		bar:       bar,
		startTime: time.Now(),
	}
}

// Write implements io.Writer
func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	if n > 0 {
		pw.bytesWritten += int64(n)
		elapsed := time.Since(pw.startTime)
		if elapsed > 0 {
			pw.bar.EwmaIncrInt64(int64(n), elapsed)
			pw.startTime = time.Now()
		} else {
			pw.bar.IncrInt64(int64(n))
		}
	}
	return n, err
}

// BytesWritten returns total bytes written
func (pw *ProgressWriter) BytesWritten() int64 {
	return pw.bytesWritten
}

// FormatBytes formats bytes to human-readable string
func FormatBytes(b int64) string {
	return humanize.Bytes(uint64(b))
}

// FormatSpeed formats bytes/sec to human-readable
func FormatSpeed(bytesPerSec float64) string {
	return fmt.Sprintf("%s/s", humanize.Bytes(uint64(bytesPerSec)))
}

// truncateName truncates a filename to maxLen with ellipsis
func truncateName(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}
	return "..." + name[len(name)-(maxLen-3):]
}
