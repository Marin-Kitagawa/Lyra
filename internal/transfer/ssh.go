package transfer

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchellh/go-homedir"
	"github.com/pkg/sftp"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/lyra-cli/lyra/internal/resume"
)

// SSHTarget represents a parsed SSH/SFTP target
type SSHTarget struct {
	User string
	Host string
	Port string
	Path string
}

// ParseSSHTarget parses user@host:/path or sftp://user@host:port/path
func ParseSSHTarget(s string) (*SSHTarget, error) {
	t := &SSHTarget{Port: "22"}

	if strings.HasPrefix(s, "sftp://") {
		// sftp://user@host:port/path
		rest := strings.TrimPrefix(s, "sftp://")
		if idx := strings.Index(rest, "@"); idx >= 0 {
			t.User = rest[:idx]
			rest = rest[idx+1:]
		}
		if idx := strings.Index(rest, "/"); idx >= 0 {
			hostPort := rest[:idx]
			t.Path = rest[idx:]
			host, port, err := net.SplitHostPort(hostPort)
			if err != nil {
				t.Host = hostPort
			} else {
				t.Host = host
				t.Port = port
			}
		} else {
			t.Host = rest
		}
	} else {
		// user@host:/path
		if idx := strings.Index(s, "@"); idx >= 0 {
			t.User = s[:idx]
			s = s[idx+1:]
		}
		if idx := strings.Index(s, ":"); idx >= 0 {
			t.Host = s[:idx]
			t.Path = s[idx+1:]
		} else {
			return nil, fmt.Errorf("invalid SSH target: %s", s)
		}
	}

	if t.User == "" {
		t.User = os.Getenv("USER")
		if t.User == "" {
			t.User = os.Getenv("USERNAME")
		}
	}

	if t.Host == "" {
		return nil, fmt.Errorf("no host specified in SSH target")
	}

	return t, nil
}

// IsSSHTarget returns true if the string looks like an SSH/SFTP target
func IsSSHTarget(s string) bool {
	if strings.HasPrefix(s, "sftp://") {
		return true
	}
	// user@host:/path - must have @ and : but no ://
	if strings.Contains(s, "@") && strings.Contains(s, ":") && !strings.Contains(s, "://") {
		return true
	}
	return false
}

// SSHOptions configures SSH transfer behavior
type SSHOptions struct {
	Password    string
	KeyFile     string
	Recursive   bool
	Resume      bool
	Preserve    bool
	KeepPartial bool
}

// SSHTransfer handles SSH/SFTP file transfers
type SSHTransfer struct {
	opts   SSHOptions
	mgr    *mpb.Progress
	ctx    context.Context
	cancel context.CancelFunc
}

// NewSSHTransfer creates a new SSH transfer handler
func NewSSHTransfer(opts SSHOptions) *SSHTransfer {
	ctx, cancel := context.WithCancel(context.Background())
	p := mpb.New(
		mpb.WithWidth(60),
		mpb.WithRefreshRate(100*time.Millisecond),
	)
	return &SSHTransfer{
		opts:   opts,
		mgr:    p,
		ctx:    ctx,
		cancel: cancel,
	}
}

// buildSSHConfig creates an SSH client config
func (st *SSHTransfer) buildSSHConfig(user string) (*ssh.ClientConfig, error) {
	var authMethods []ssh.AuthMethod

	// Try SSH agent first
	if agentConn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
		agentClient := agent.NewClient(agentConn)
		authMethods = append(authMethods, ssh.PublicKeysCallback(agentClient.Signers))
	}

	// Try key file
	keyFile := st.opts.KeyFile
	if keyFile == "" {
		home, _ := homedir.Dir()
		// Try common key files
		for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
			candidate := filepath.Join(home, ".ssh", name)
			if _, err := os.Stat(candidate); err == nil {
				keyFile = candidate
				break
			}
		}
	}

	if keyFile != "" {
		keyData, err := os.ReadFile(keyFile)
		if err == nil {
			signer, err := ssh.ParsePrivateKey(keyData)
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
	}

	// Password auth
	if st.opts.Password != "" {
		authMethods = append(authMethods, ssh.Password(st.opts.Password))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available")
	}

	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: implement known_hosts checking
		Timeout:         30 * time.Second,
	}, nil
}

// addBar adds a progress bar
func (st *SSHTransfer) addBar(name string, total int64) *mpb.Bar {
	shortName := name
	if len(shortName) > 20 {
		shortName = "..." + shortName[len(shortName)-17:]
	}
	return st.mgr.New(total,
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

// Upload uploads a local file to a remote SSH/SFTP host
func (st *SSHTransfer) Upload(localPath string, target *SSHTarget) error {
	config, err := st.buildSSHConfig(target.User)
	if err != nil {
		return err
	}

	sshClient, err := ssh.Dial("tcp", net.JoinHostPort(target.Host, target.Port), config)
	if err != nil {
		return fmt.Errorf("could not connect to %s: %w", target.Host, err)
	}
	defer sshClient.Close()

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("could not create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	return st.uploadFile(sftpClient, localPath, target.Path)
}

// Download downloads a file from a remote SSH/SFTP host
func (st *SSHTransfer) Download(target *SSHTarget, localPath string) error {
	config, err := st.buildSSHConfig(target.User)
	if err != nil {
		return err
	}

	sshClient, err := ssh.Dial("tcp", net.JoinHostPort(target.Host, target.Port), config)
	if err != nil {
		return fmt.Errorf("could not connect to %s: %w", target.Host, err)
	}
	defer sshClient.Close()

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("could not create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	return st.downloadFile(sftpClient, target.Path, localPath)
}

// uploadFile uploads a single file via SFTP
func (st *SSHTransfer) uploadFile(client *sftp.Client, localPath, remotePath string) error {
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("could not open local file: %w", err)
	}
	defer localFile.Close()

	localStat, err := localFile.Stat()
	if err != nil {
		return err
	}

	// Check for resume
	var startOffset int64
	if st.opts.Resume {
		state, err := resume.Load(localPath, remotePath)
		if err == nil && state != nil {
			startOffset = state.BytesDone
		}
	}

	// Seek local file if resuming
	if startOffset > 0 {
		if _, err := localFile.Seek(startOffset, io.SeekStart); err != nil {
			return err
		}
	}

	// Create remote file
	if err := client.MkdirAll(filepath.Dir(remotePath)); err != nil {
		return fmt.Errorf("could not create remote directory: %w", err)
	}

	var remoteFile *sftp.File
	if startOffset > 0 {
		remoteFile, err = client.OpenFile(remotePath, os.O_WRONLY|os.O_APPEND)
	} else {
		remoteFile, err = client.Create(remotePath)
	}
	if err != nil {
		return fmt.Errorf("could not create remote file: %w", err)
	}
	defer remoteFile.Close()

	if startOffset > 0 {
		if _, err := remoteFile.Seek(startOffset, io.SeekStart); err != nil {
			return err
		}
	}

	remaining := localStat.Size() - startOffset
	bar := st.addBar(filepath.Base(localPath), remaining)

	buf := make([]byte, 32*1024)
	var written int64
	start := time.Now()

	for {
		select {
		case <-st.ctx.Done():
			state := &resume.State{
				Src:        localPath,
				Dest:       remotePath,
				BytesDone:  startOffset + written,
				TotalBytes: localStat.Size(),
				Type:       resume.TypeSSH,
			}
			resume.Save(state)
			return fmt.Errorf("transfer paused")
		default:
		}

		n, err := localFile.Read(buf)
		if n > 0 {
			nw, werr := remoteFile.Write(buf[:n])
			written += int64(nw)
			elapsed := time.Since(start)
			if elapsed > 0 {
				bar.EwmaIncrInt64(int64(nw), elapsed)
				start = time.Now()
			} else {
				bar.IncrInt64(int64(nw))
			}
			if werr != nil {
				return fmt.Errorf("remote write error: %w", werr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	resume.Delete(localPath, remotePath)
	return nil
}

// downloadFile downloads a single file via SFTP
func (st *SSHTransfer) downloadFile(client *sftp.Client, remotePath, localPath string) error {
	remoteFile, err := client.Open(remotePath)
	if err != nil {
		return fmt.Errorf("could not open remote file: %w", err)
	}
	defer remoteFile.Close()

	remoteStat, err := remoteFile.Stat()
	if err != nil {
		return err
	}

	var startOffset int64
	if st.opts.Resume {
		state, err := resume.Load(remotePath, localPath)
		if err == nil && state != nil {
			startOffset = state.BytesDone
		}
	}

	if startOffset > 0 {
		if _, err := remoteFile.Seek(startOffset, io.SeekStart); err != nil {
			return err
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

	remaining := remoteStat.Size() - startOffset
	bar := st.addBar(filepath.Base(remotePath), remaining)

	buf := make([]byte, 32*1024)
	var written int64
	start := time.Now()

	for {
		select {
		case <-st.ctx.Done():
			state := &resume.State{
				Src:        remotePath,
				Dest:       localPath,
				BytesDone:  startOffset + written,
				TotalBytes: remoteStat.Size(),
				Type:       resume.TypeSSH,
			}
			resume.Save(state)
			return fmt.Errorf("transfer paused")
		default:
		}

		n, err := remoteFile.Read(buf)
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
				return fmt.Errorf("local write error: %w", werr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	resume.Delete(remotePath, localPath)
	return nil
}

// Cancel cancels the transfer
func (st *SSHTransfer) Cancel() {
	st.cancel()
}

// Wait waits for all progress bars to finish
func (st *SSHTransfer) Wait() {
	st.mgr.Wait()
}
