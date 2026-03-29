package resume

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mitchellh/go-homedir"
)

// TransferType represents the type of transfer
type TransferType string

const (
	TypeLocal    TransferType = "local"
	TypeSSH      TransferType = "ssh"
	TypeFTP      TransferType = "ftp"
	TypeGDrive   TransferType = "gdrive"
	TypeDropbox  TransferType = "dropbox"
	TypeOneDrive TransferType = "onedrive"
)

// State holds the resume state for an interrupted transfer
type State struct {
	Src       string       `json:"src"`
	Dest      string       `json:"dest"`
	BytesDone int64        `json:"bytes_done"`
	TotalBytes int64       `json:"total_bytes"`
	Timestamp time.Time    `json:"timestamp"`
	Type      TransferType `json:"type"`
	Extra     map[string]string `json:"extra,omitempty"` // for upload session IDs etc
}

// StateKey generates a unique key for a src+dest pair
func StateKey(src, dest string) string {
	h := sha256.New()
	h.Write([]byte(src + "|" + dest))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// stateDir returns the directory where state files are stored
func stateDir() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", fmt.Errorf("could not find home directory: %w", err)
	}
	return filepath.Join(home, ".lyra", "resume"), nil
}

// statePath returns the path for a state file given src and dest
func statePath(src, dest string) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	key := StateKey(src, dest)
	return filepath.Join(dir, key+".json"), nil
}

// Save saves the transfer state to disk
func Save(s *State) error {
	dir, err := stateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("could not create state directory: %w", err)
	}

	path, err := statePath(s.Src, s.Dest)
	if err != nil {
		return err
	}

	s.Timestamp = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("could not marshal state: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// Load loads the transfer state for a src+dest pair
func Load(src, dest string) (*State, error) {
	path, err := statePath(src, dest)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no state file, not an error
		}
		return nil, fmt.Errorf("could not read state file: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("could not parse state file: %w", err)
	}

	return &s, nil
}

// Delete removes the state file for a completed transfer
func Delete(src, dest string) error {
	path, err := statePath(src, dest)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not delete state file: %w", err)
	}
	return nil
}

// ListAll returns all saved transfer states
func ListAll() ([]*State, error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not read state directory: %w", err)
	}

	var states []*State
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s State
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		states = append(states, &s)
	}

	return states, nil
}
