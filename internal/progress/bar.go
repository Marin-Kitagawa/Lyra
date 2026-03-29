package progress

import (
	"io"
	"time"

	"github.com/dustin/go-humanize"
)

// ProgressReader wraps an io.Reader and reports bytes to a channel.
type ProgressReader struct {
	reader io.Reader
	ch     chan<- int64
}

// NewProgressReader creates a reader that reports each read to ch.
func NewProgressReader(r io.Reader, ch chan<- int64) *ProgressReader {
	return &ProgressReader{reader: r, ch: ch}
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		select {
		case pr.ch <- int64(n):
		default:
		}
	}
	return n, err
}

// ProgressWriter wraps an io.Writer and reports bytes to a channel.
type ProgressWriter struct {
	writer       io.Writer
	ch           chan<- int64
	bytesWritten int64
}

// NewProgressWriter creates a writer that reports each write to ch.
func NewProgressWriter(w io.Writer, ch chan<- int64) *ProgressWriter {
	return &ProgressWriter{writer: w, ch: ch}
}

func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	if n > 0 {
		pw.bytesWritten += int64(n)
		select {
		case pw.ch <- int64(n):
		default:
		}
	}
	return n, err
}

// BytesWritten returns total bytes written so far.
func (pw *ProgressWriter) BytesWritten() int64 {
	return pw.bytesWritten
}

// FormatBytes formats bytes to human-readable string.
func FormatBytes(b int64) string {
	return humanize.Bytes(uint64(b))
}

// FormatSpeed formats bytes/sec to human-readable.
func FormatSpeed(bytesPerSec float64) string {
	return humanize.Bytes(uint64(bytesPerSec)) + "/s"
}

// SpeedTracker tracks transfer speed over time using EMA.
type SpeedTracker struct {
	speed     float64
	lastTime  time.Time
	lastBytes int64
}

func NewSpeedTracker() *SpeedTracker {
	return &SpeedTracker{lastTime: time.Now()}
}

func (s *SpeedTracker) Record(bytes int64) float64 {
	now := time.Now()
	dt := now.Sub(s.lastTime).Seconds()
	if dt > 0 {
		instant := float64(bytes) / dt
		if s.speed == 0 {
			s.speed = instant
		} else {
			s.speed = 0.8*s.speed + 0.2*instant
		}
	}
	s.lastTime = now
	return s.speed
}

func (s *SpeedTracker) Speed() float64 {
	return s.speed
}
