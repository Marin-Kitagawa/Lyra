package cloud

import (
	"github.com/Marin-Kitagawa/Lyra/internal/resume"
)

// FileInfo represents a file in a cloud provider
type FileInfo struct {
	Name     string
	Path     string
	Size     int64
	IsDir    bool
	ModTime  string
	MimeType string
}

// Provider defines the interface for cloud storage providers
type Provider interface {
	// Name returns the provider name
	Name() string
	// Auth initiates the OAuth2 authentication flow
	Auth() error
	// Upload uploads a local file to the remote path
	Upload(local, remote string, progress chan<- int64) error
	// Download downloads a remote file to the local path
	Download(remote, local string, progress chan<- int64) error
	// List lists files at the given path
	List(path string) ([]FileInfo, error)
	// Delete deletes a file at the given path
	Delete(path string) error
	// Resume resumes an interrupted transfer
	Resume(state *resume.State) error
	// IsAuthenticated returns true if the provider has valid credentials
	IsAuthenticated() bool
}
