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

	"github.com/Marin-Kitagawa/Lyra/internal/resume"
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

	// Collapse consecutive slashes in path (e.g. //file.txt → /file.txt)
	cleanPath := u.Path
	for strings.Contains(cleanPath, "//") {
		cleanPath = strings.ReplaceAll(cleanPath, "//", "/")
	}

	t := &FTPTarget{
		Host: u.Hostname(),
		Port: u.Port(),
		Path: cleanPath,
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
	opts     FTPOptions
	reportFn func(int64) // called with bytes written; can be nil
	doneFn   func(error) // called when done; can be nil
}

// NewFTPTransfer creates a new FTP transfer handler.
// reportFn is called with each chunk of bytes written (can be nil).
// doneFn is called when the transfer completes (can be nil).
func NewFTPTransfer(opts FTPOptions, reportFn func(int64), doneFn func(error)) *FTPTransfer {
	return &FTPTransfer{
		opts:     opts,
		reportFn: reportFn,
		doneFn:   doneFn,
	}
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
		if _, err := localFile.Seek(startOffset, io.SeekStart); err != nil {
			return err
		}
		remaining = localStat.Size() - startOffset
	}

	trackedReader := &ftpProgressReader{
		r:        reader,
		reportFn: ft.reportFn,
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
		if ft.doneFn != nil {
			ft.doneFn(uploadErr)
		}
		return fmt.Errorf("FTP upload failed: %w", uploadErr)
	}

	_ = remaining
	resume.Delete(localPath, target.Path)
	if ft.doneFn != nil {
		ft.doneFn(nil)
	}
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
	_ = remaining

	buf := make([]byte, 32*1024)
	var written int64

	for {
		n, err := ftpReader.Read(buf)
		if n > 0 {
			nw, werr := localFile.Write(buf[:n])
			written += int64(nw)
			if ft.reportFn != nil {
				ft.reportFn(int64(nw))
			}
			if werr != nil {
				if ft.doneFn != nil {
					ft.doneFn(werr)
				}
				return fmt.Errorf("write error: %w", werr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			if ft.doneFn != nil {
				ft.doneFn(err)
			}
			return fmt.Errorf("FTP read error: %w", err)
		}
	}

	resume.Delete(target.Path, localPath)
	if ft.doneFn != nil {
		ft.doneFn(nil)
	}
	return nil
}

// ftpProgressReader is a reader that tracks progress for FTP uploads
type ftpProgressReader struct {
	r         io.Reader
	reportFn  func(int64)
	bytesRead int64
}

func (r *ftpProgressReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.bytesRead += int64(n)
		if r.reportFn != nil {
			r.reportFn(int64(n))
		}
	}
	return n, err
}
