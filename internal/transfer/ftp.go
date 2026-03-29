package transfer

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	"github.com/lyra-cli/lyra/internal/resume"
)

// FTPTarget represents a parsed FTP target
type FTPTarget struct {
	User string
	Pass string
	Host string
	Port string
	Path string
}

// ParseFTPTarget parses ftp://user:pass@host:port/path
func ParseFTPTarget(s string) (*FTPTarget, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("invalid FTP URL: %w", err)
	}

	t := &FTPTarget{
		Host: u.Hostname(),
		Port: u.Port(),
		Path: u.Path,
	}

	if t.Port == "" {
		t.Port = "21"
	}

	if u.User != nil {
		t.User = u.User.Username()
		t.Pass, _ = u.User.Password()
	}

	if t.User == "" {
		t.User = "anonymous"
	}
	if t.Pass == "" {
		t.Pass = "anonymous@"
	}

	return t, nil
}

// IsFTPTarget returns true if the string looks like an FTP URL
func IsFTPTarget(s string) bool {
	return strings.HasPrefix(s, "ftp://")
}

// FTPOptions configures FTP transfer behavior
type FTPOptions struct {
	Resume      bool
	KeepPartial bool
}

// FTPTransfer handles FTP file transfers
type FTPTransfer struct {
	opts   FTPOptions
	mgr    *mpb.Progress
}

// NewFTPTransfer creates a new FTP transfer handler
func NewFTPTransfer(opts FTPOptions) *FTPTransfer {
	p := mpb.New(
		mpb.WithWidth(60),
		mpb.WithRefreshRate(100*time.Millisecond),
	)
	return &FTPTransfer{
		opts: opts,
		mgr:  p,
	}
}

// addBar adds a progress bar
func (ft *FTPTransfer) addBar(name string, total int64) *mpb.Bar {
	shortName := name
	if len(shortName) > 20 {
		shortName = "..." + shortName[len(shortName)-17:]
	}
	return ft.mgr.New(total,
		mpb.BarStyle().Lbound("").Filler("█").Tip("▓").Padding("░").Rbound(""),
		mpb.PrependDecorators(
			decor.Name(shortName, decor.WC{W: 22, C: decor.DindentRight | decor.DextraSpace}),
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
}

// connect establishes an FTP connection
func (ft *FTPTransfer) connect(target *FTPTarget) (*ftp.ServerConn, error) {
	addr := target.Host + ":" + target.Port
	conn, err := ftp.Dial(addr, ftp.DialWithTimeout(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("could not connect to FTP server %s: %w", addr, err)
	}

	if err := conn.Login(target.User, target.Pass); err != nil {
		conn.Quit()
		return nil, fmt.Errorf("FTP login failed: %w", err)
	}

	return conn, nil
}

// Upload uploads a local file to an FTP server
func (ft *FTPTransfer) Upload(localPath string, target *FTPTarget) error {
	conn, err := ft.connect(target)
	if err != nil {
		return err
	}
	defer conn.Quit()

	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("could not open local file: %w", err)
	}
	defer localFile.Close()

	localStat, err := localFile.Stat()
	if err != nil {
		return err
	}

	// Check resume state
	var startOffset int64
	if ft.opts.Resume {
		state, err := resume.Load(localPath, target.Path)
		if err == nil && state != nil {
			startOffset = state.BytesDone
		}
	}

	// Ensure remote directory exists
	remoteDir := filepath.Dir(target.Path)
	if remoteDir != "." && remoteDir != "/" {
		conn.MakeDir(remoteDir)
	}

	var reader io.Reader = localFile
	remaining := localStat.Size()

	if startOffset > 0 {
		// Use REST command for resumable FTP
		if _, err := localFile.Seek(startOffset, io.SeekStart); err != nil {
			return err
		}
		remaining = localStat.Size() - startOffset
	}

	bar := ft.addBar(filepath.Base(localPath), remaining)
	trackedReader := &ftpProgressReader{
		r:         reader,
		bar:       bar,
		startTime: time.Now(),
	}

	var uploadErr error
	if startOffset > 0 {
		uploadErr = conn.StorFrom(target.Path, trackedReader, uint64(startOffset))
	} else {
		uploadErr = conn.Stor(target.Path, trackedReader)
	}

	if uploadErr != nil {
		state := &resume.State{
			Src:        localPath,
			Dest:       target.Path,
			BytesDone:  startOffset + trackedReader.bytesRead,
			TotalBytes: localStat.Size(),
			Type:       resume.TypeFTP,
		}
		resume.Save(state)
		return fmt.Errorf("FTP upload failed: %w", uploadErr)
	}

	resume.Delete(localPath, target.Path)
	return nil
}

// Download downloads a file from an FTP server
func (ft *FTPTransfer) Download(target *FTPTarget, localPath string) error {
	conn, err := ft.connect(target)
	if err != nil {
		return err
	}
	defer conn.Quit()

	// Get file size
	size, err := conn.FileSize(target.Path)
	if err != nil {
		size = -1 // Unknown size
	}

	// Check resume state
	var startOffset int64
	if ft.opts.Resume {
		state, loadErr := resume.Load(target.Path, localPath)
		if loadErr == nil && state != nil {
			startOffset = state.BytesDone
		}
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}

	var localFile *os.File
	if startOffset > 0 {
		localFile, err = os.OpenFile(localPath, os.O_WRONLY|os.O_APPEND, 0644)
	} else {
		localFile, err = os.Create(localPath)
	}
	if err != nil {
		return err
	}
	defer localFile.Close()

	var ftpReader io.ReadCloser
	if startOffset > 0 {
		ftpReader, err = conn.RetrFrom(target.Path, uint64(startOffset))
	} else {
		ftpReader, err = conn.Retr(target.Path)
	}
	if err != nil {
		return fmt.Errorf("FTP download failed: %w", err)
	}
	defer ftpReader.Close()

	remaining := size - startOffset
	if remaining < 0 {
		remaining = 0
	}

	bar := ft.addBar(filepath.Base(target.Path), remaining)
	buf := make([]byte, 32*1024)
	var written int64
	start := time.Now()

	for {
		n, err := ftpReader.Read(buf)
		if n > 0 {
			nw, werr := localFile.Write(buf[:n])
			written += int64(nw)
			elapsed := time.Since(start)
			if elapsed > 0 {
				bar.EwmaIncrInt64(int64(nw), elapsed)
				start = time.Now()
			} else {
				bar.IncrInt64(int64(nw))
			}
			if werr != nil {
				return fmt.Errorf("write error: %w", werr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("FTP read error: %w", err)
		}
	}

	resume.Delete(target.Path, localPath)
	return nil
}

// Wait waits for all progress bars to finish
func (ft *FTPTransfer) Wait() {
	ft.mgr.Wait()
}

// ftpProgressReader is a reader that tracks progress for FTP uploads
type ftpProgressReader struct {
	r         io.Reader
	bar       *mpb.Bar
	bytesRead int64
	startTime time.Time
}

func (r *ftpProgressReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.bytesRead += int64(n)
		elapsed := time.Since(r.startTime)
		if elapsed > 0 {
			r.bar.EwmaIncrInt64(int64(n), elapsed)
			r.startTime = time.Now()
		} else {
			r.bar.IncrInt64(int64(n))
		}
	}
	return n, err
}
