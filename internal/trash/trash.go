package trash

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mitchellh/go-homedir"
)

// TrashInfo holds metadata about a trashed file
type TrashInfo struct {
	OriginalPath string    `json:"original_path"`
	TrashedAt    time.Time `json:"trashed_at"`
	TrashName    string    `json:"trash_name"`
}

// trashDir returns the platform-specific trash directory
func trashDir() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}

	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Local", "Microsoft", "Windows", "Recycle.Bin"), nil
	case "darwin":
		return filepath.Join(home, ".Trash"), nil
	default: // linux and others
		return filepath.Join(home, ".local", "share", "Trash", "files"), nil
	}
}

// trashInfoDir returns the trash info directory (Linux/freedesktop)
func trashInfoDir() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "Trash", "info"), nil
}

// lyraTrashMetaDir returns the lyra-specific metadata directory
func lyraTrashMetaDir() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lyra", "trash-meta"), nil
}

// MoveToTrash moves a file or directory to the system trash
func MoveToTrash(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("could not resolve path: %w", err)
	}

	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("path does not exist: %w", err)
	}

	switch runtime.GOOS {
	case "windows":
		return trashWindows(absPath)
	case "darwin":
		return trashDarwin(absPath)
	default:
		return trashLinux(absPath)
	}
}

// trashWindows uses PowerShell to send to Recycle Bin
func trashWindows(path string) error {
	// Use different methods for files vs directories
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("could not stat path: %w", err)
	}

	var script string
	if info.IsDir() {
		script = fmt.Sprintf(
			`Add-Type -AssemblyName Microsoft.VisualBasic; [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteDirectory('%s', 'OnlyErrorDialogs', 'SendToRecycleBin')`,
			path,
		)
	} else {
		script = fmt.Sprintf(
			`Add-Type -AssemblyName Microsoft.VisualBasic; [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile('%s', 'OnlyErrorDialogs', 'SendToRecycleBin')`,
			path,
		)
	}

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to move to recycle bin: %w\n%s", err, string(out))
	}
	return nil
}

// trashDarwin moves to ~/.Trash on macOS
func trashDarwin(path string) error {
	trashPath, err := trashDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(trashPath, 0755); err != nil {
		return err
	}
	destName := uniqueTrashName(trashPath, filepath.Base(path))
	destPath := filepath.Join(trashPath, destName)
	if err := os.Rename(path, destPath); err != nil {
		return fmt.Errorf("failed to move to trash: %w", err)
	}
	return saveTrashMeta(&TrashInfo{
		OriginalPath: path,
		TrashedAt:    time.Now(),
		TrashName:    destName,
	})
}

// trashLinux follows the freedesktop.org Trash spec
func trashLinux(path string) error {
	filesDir, err := trashDir()
	if err != nil {
		return err
	}
	infoDir, err := trashInfoDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filesDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(infoDir, 0755); err != nil {
		return err
	}

	baseName := filepath.Base(path)
	destName := uniqueTrashName(filesDir, baseName)
	destPath := filepath.Join(filesDir, destName)

	if err := os.Rename(path, destPath); err != nil {
		return fmt.Errorf("failed to move to trash: %w", err)
	}

	// Write .trashinfo file (freedesktop spec)
	infoContent := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		path, time.Now().Format("2006-01-02T15:04:05"))
	infoPath := filepath.Join(infoDir, destName+".trashinfo")
	if err := os.WriteFile(infoPath, []byte(infoContent), 0644); err != nil {
		return fmt.Errorf("failed to write trash info: %w", err)
	}

	return saveTrashMeta(&TrashInfo{
		OriginalPath: path,
		TrashedAt:    time.Now(),
		TrashName:    destName,
	})
}

// saveTrashMeta saves lyra-specific trash metadata for restore support
func saveTrashMeta(info *TrashInfo) error {
	metaDir, err := lyraTrashMetaDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		return err
	}
	metaPath := filepath.Join(metaDir, info.TrashName+".json")
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, data, 0644)
}

// uniqueTrashName generates a unique name in the trash directory
func uniqueTrashName(dir, name string) string {
	candidate := name
	ext := filepath.Ext(name)
	base := name[:len(name)-len(ext)]
	i := 1
	for {
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
		candidate = fmt.Sprintf("%s_%d%s", base, i, ext)
		i++
	}
}

// ListTrash returns all trashed files tracked by lyra
func ListTrash() ([]*TrashInfo, error) {
	metaDir, err := lyraTrashMetaDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(metaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var infos []*TrashInfo
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(metaDir, e.Name()))
		if err != nil {
			continue
		}
		var info TrashInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		infos = append(infos, &info)
	}
	return infos, nil
}

// RestoreFromTrash restores a file from trash by its original path
func RestoreFromTrash(originalPath string) error {
	metaDir, err := lyraTrashMetaDir()
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(metaDir)
	if err != nil {
		return fmt.Errorf("could not read trash metadata: %w", err)
	}

	var found *TrashInfo
	var foundMetaPath string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		metaPath := filepath.Join(metaDir, e.Name())
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var info TrashInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		if info.OriginalPath == originalPath || filepath.Base(info.OriginalPath) == originalPath {
			found = &info
			foundMetaPath = metaPath
			break
		}
	}

	if found == nil {
		return fmt.Errorf("no trashed file found for: %s", originalPath)
	}

	// Find the file in trash
	var trashFilePath string
	switch runtime.GOOS {
	case "windows":
		return fmt.Errorf("restore from recycle bin not supported directly; use Windows Explorer")
	case "darwin":
		home, _ := homedir.Dir()
		trashFilePath = filepath.Join(home, ".Trash", found.TrashName)
	default:
		home, _ := homedir.Dir()
		trashFilePath = filepath.Join(home, ".local", "share", "Trash", "files", found.TrashName)
	}

	if _, err := os.Stat(trashFilePath); err != nil {
		return fmt.Errorf("trashed file not found at %s: %w", trashFilePath, err)
	}

	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(found.OriginalPath), 0755); err != nil {
		return fmt.Errorf("could not create destination directory: %w", err)
	}

	if err := os.Rename(trashFilePath, found.OriginalPath); err != nil {
		return fmt.Errorf("could not restore file: %w", err)
	}

	// Clean up metadata
	os.Remove(foundMetaPath)

	// Clean up .trashinfo on Linux
	if runtime.GOOS == "linux" {
		home, _ := homedir.Dir()
		infoPath := filepath.Join(home, ".local", "share", "Trash", "info", found.TrashName+".trashinfo")
		os.Remove(infoPath)
	}

	return nil
}
