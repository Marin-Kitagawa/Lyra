package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Marin-Kitagawa/Lyra/internal/tui"
	"github.com/Marin-Kitagawa/Lyra/internal/ui"
	"github.com/spf13/cobra"
)

var (
	syncDelete   bool
	syncDryRun   bool
	syncChecksum bool
	syncTwoWay   bool
)

var syncCmd = &cobra.Command{
	Use:   "sync <source> <destination>",
	Short: "Sync two directories",
	Long: `Synchronize two directories. By default, syncs source → destination.

Examples:
  lyra sync src/ dest/
  lyra sync src/ dest/ --delete      # Remove files not in src
  lyra sync src/ dest/ --dry-run     # Preview changes
  lyra sync src/ dest/ --checksum    # Use checksums for change detection
  lyra sync src/ dest/ --two-way     # Bidirectional sync`,
	Args: cobra.ExactArgs(2),
	RunE: runSync,
}

func init() {
	syncCmd.Flags().BoolVar(&syncDelete, "delete", false, "Delete files in dest not present in src")
	syncCmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "Preview changes without making them")
	syncCmd.Flags().BoolVar(&syncChecksum, "checksum", false, "Use checksums for change detection")
	syncCmd.Flags().BoolVar(&syncTwoWay, "two-way", false, "Bidirectional sync")
}

// syncAction represents an action to perform during sync
type syncAction struct {
	op   string // "copy", "delete", "skip"
	src  string
	dest string
	size int64
}

func runSync(cmd *cobra.Command, args []string) error {
	src := args[0]
	dest := args[1]

	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("source does not exist: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("source must be a directory")
	}

	fmt.Println(ui.RenderInfo(fmt.Sprintf("Syncing %s → %s",
		ui.StylePrimary.Render(src),
		ui.StyleSecondary.Render(dest),
	)))

	actions, err := computeSyncActions(src, dest)
	if err != nil {
		return err
	}

	if syncTwoWay {
		reverseActions, err := computeSyncActions(dest, src)
		if err != nil {
			return err
		}
		actions = mergeSyncActions(actions, reverseActions)
	}

	if len(actions) == 0 {
		fmt.Println(ui.RenderSuccess("Already in sync — nothing to do."))
		return nil
	}

	// Print preview
	copies := 0
	deletes := 0
	for _, a := range actions {
		switch a.op {
		case "copy", "mkdir":
			copies++
			prefix := ui.StyleSuccess.Render("  + ")
			if syncDryRun {
				prefix = ui.StyleAccent.Render("  [copy] ")
			}
			fmt.Printf("%s%s\n", prefix, a.dest)
		case "delete":
			deletes++
			prefix := ui.StyleError.Render("  - ")
			if syncDryRun {
				prefix = ui.StyleError.Render("  [del]  ")
			}
			fmt.Printf("%s%s\n", prefix, a.dest)
		}
	}

	fmt.Printf("\n%s\n",
		ui.StyleMuted.Render(fmt.Sprintf("  %d to copy, %d to delete", copies, deletes)))

	if syncDryRun {
		fmt.Println(ui.RenderInfo("Dry run — no changes made."))
		return nil
	}

	return executeSyncActions(actions)
}

// computeSyncActions computes the actions needed to sync src → dest
func computeSyncActions(src, dest string) ([]syncAction, error) {
	var actions []syncAction

	// Walk source
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dest, rel)

		if info.IsDir() {
			destInfo, err := os.Stat(destPath)
			if err != nil || !destInfo.IsDir() {
				actions = append(actions, syncAction{op: "mkdir", src: path, dest: destPath})
			}
			return nil
		}

		// Check if file needs to be copied
		destInfo, err := os.Stat(destPath)
		if err != nil {
			// Destination doesn't exist
			actions = append(actions, syncAction{op: "copy", src: path, dest: destPath, size: info.Size()})
			return nil
		}

		// Check if file has changed
		changed := false
		if syncChecksum {
			srcHash, err1 := hashFile(path)
			destHash, err2 := hashFile(destPath)
			if err1 != nil || err2 != nil || srcHash != destHash {
				changed = true
			}
		} else {
			if info.Size() != destInfo.Size() || info.ModTime().After(destInfo.ModTime()) {
				changed = true
			}
		}

		if changed {
			actions = append(actions, syncAction{op: "copy", src: path, dest: destPath, size: info.Size()})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Find files in dest that are not in src (for --delete)
	if syncDelete {
		if _, err := os.Stat(dest); err == nil {
			err = filepath.Walk(dest, func(path string, info os.FileInfo, err error) error {
				if err != nil || path == dest {
					return err
				}
				rel, _ := filepath.Rel(dest, path)
				srcPath := filepath.Join(src, rel)
				if _, err := os.Stat(srcPath); os.IsNotExist(err) {
					actions = append(actions, syncAction{op: "delete", dest: path})
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}

	return actions, nil
}

// mergeSyncActions merges two-way sync actions, preferring newer files
func mergeSyncActions(forward, reverse []syncAction) []syncAction {
	seen := make(map[string]bool)
	var merged []syncAction

	for _, a := range forward {
		seen[a.dest] = true
		merged = append(merged, a)
	}
	for _, a := range reverse {
		if !seen[a.dest] {
			merged = append(merged, a)
		}
	}
	return merged
}

// executeSyncActions executes all sync actions with a BubbleTea progress display
func executeSyncActions(actions []syncAction) error {
	// Separate actions by type
	var mkdirActions []syncAction
	var copyActions []syncAction
	var deleteActions []syncAction
	for _, a := range actions {
		switch a.op {
		case "mkdir":
			mkdirActions = append(mkdirActions, a)
		case "copy":
			copyActions = append(copyActions, a)
		default:
			deleteActions = append(deleteActions, a)
		}
	}

	// Create directories first
	for _, a := range mkdirActions {
		if err := os.MkdirAll(a.dest, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", a.dest, err)
		}
	}

	var records []tui.SummaryRecord
	var mu sync.Mutex

	// Handle copy actions with progress
	if len(copyActions) > 0 {
		pp := tui.NewProgressProgram("syncing", nil)
		entries := make([]*tui.Entry, len(copyActions))
		for i, a := range copyActions {
			entries[i] = pp.Add(filepath.Base(a.src), a.size)
		}

		semaphore := make(chan struct{}, 4)
		var wg sync.WaitGroup
		errCh := make(chan error, len(copyActions))

		for i, action := range copyActions {
			a := action
			entry := entries[i]
			wg.Add(1)
			go func() {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				start := time.Now()
				err := syncCopyFile(a.src, a.dest, entry.Report)
				entry.Finish(err)
				mu.Lock()
				records = append(records, tui.SummaryRecord{
					Name:     filepath.Base(a.src),
					Op:       "Copy",
					Err:      err,
					Size:     a.size,
					Duration: time.Since(start),
				})
				mu.Unlock()
				if err != nil {
					errCh <- fmt.Errorf("failed to copy %s: %w", a.src, err)
				}
			}()
		}

		// Wait for all goroutines in background, then pp.Run() blocks
		go func() {
			wg.Wait()
			close(errCh)
		}()

		pp.Run()

		for err := range errCh {
			if err != nil {
				return err
			}
		}
	}

	// Handle delete actions
	for _, a := range deleteActions {
		start := time.Now()
		err := os.RemoveAll(a.dest)
		mu.Lock()
		records = append(records, tui.SummaryRecord{
			Name:     filepath.Base(a.dest),
			Op:       "Delete",
			Err:      err,
			Size:     -1,
			Duration: time.Since(start),
		})
		mu.Unlock()
		if err != nil {
			return fmt.Errorf("failed to delete %s: %w", a.dest, err)
		}
	}

	if !noSummary {
		tui.ShowSummary(records)
	}

	fmt.Println(ui.RenderSuccess("Sync complete!"))
	return nil
}

// syncCopyFile copies a file as part of sync
func syncCopyFile(src, dest string, reportFn func(int64)) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcStat, err := srcFile.Stat()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	destFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		destFile.Close()
		os.Chmod(dest, srcStat.Mode())
		os.Chtimes(dest, srcStat.ModTime(), srcStat.ModTime())
	}()

	buf := make([]byte, 1024*1024)
	for {
		n, err := srcFile.Read(buf)
		if n > 0 {
			nw, werr := destFile.Write(buf[:n])
			if reportFn != nil {
				reportFn(int64(nw))
			}
			if werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// hashFile computes the SHA256 hash of a file
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 1024*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ensure strings is used
var _ = strings.TrimPrefix
