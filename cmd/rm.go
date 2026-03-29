package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lyra-cli/lyra/internal/trash"
	"github.com/lyra-cli/lyra/internal/tui"
	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
)

var (
	rmPermanent bool
	rmListTrash bool
	rmRestore   string
	rmForce     bool
	rmRecursive bool
)

var rmCmd = &cobra.Command{
	Use:   "rm <path>...",
	Short: "Remove files or directories (to trash by default)",
	Long: `Remove files or directories.

By default, files are moved to the system trash (recycle bin).
Use --permanent to delete without trash.

Examples:
  lyra rm file.txt           # Move to trash
  lyra rm --permanent file.txt  # Delete permanently
  lyra rm -r my-directory/   # Remove directory recursively
  lyra rm --list-trash        # Show files in trash
  lyra rm --restore file.txt  # Restore from trash`,
	RunE: runRm,
}

func init() {
	rmCmd.Flags().BoolVar(&rmPermanent, "permanent", false, "Delete permanently (skip trash)")
	rmCmd.Flags().BoolVar(&rmListTrash, "list-trash", false, "List files in trash")
	rmCmd.Flags().StringVar(&rmRestore, "restore", "", "Restore a file from trash")
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "Ignore nonexistent files, never prompt")
	rmCmd.Flags().BoolVarP(&rmRecursive, "recursive", "r", false, "Remove directories recursively")
}

func runRm(cmd *cobra.Command, args []string) error {
	// Handle --list-trash
	if rmListTrash {
		return listTrash()
	}

	// Handle --restore
	if rmRestore != "" {
		return restoreFromTrash(rmRestore)
	}

	if len(args) == 0 {
		return fmt.Errorf("no files specified")
	}

	var records []tui.SummaryRecord
	for _, path := range args {
		start := time.Now()
		err := removeFile(path)
		records = append(records, tui.SummaryRecord{
			Name:     filepath.Base(path),
			Op:       "Delete",
			Err:      err,
			Size:     -1,
			Duration: time.Since(start),
		})
		if err != nil && !rmForce {
			return err
		}
	}

	if !noSummary {
		tui.ShowSummary(records)
	}

	return nil
}

func removeFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) && rmForce {
			return nil
		}
		return fmt.Errorf("cannot access %s: %w", path, err)
	}

	if info.IsDir() && !rmRecursive {
		return fmt.Errorf("%s is a directory; use -r to remove recursively", path)
	}

	if rmPermanent {
		if info.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("could not remove directory %s: %w", path, err)
			}
		} else {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("could not remove %s: %w", path, err)
			}
		}
		fmt.Println(ui.RenderSuccess(fmt.Sprintf("Permanently deleted %s", ui.StylePrimary.Render(path))))
	} else {
		if err := trash.MoveToTrash(path); err != nil {
			return fmt.Errorf("could not move %s to trash: %w", path, err)
		}
		fmt.Println(ui.RenderSuccess(fmt.Sprintf("Moved %s to trash", ui.StylePrimary.Render(path))))
	}

	return nil
}

func listTrash() error {
	infos, err := trash.ListTrash()
	if err != nil {
		return fmt.Errorf("could not list trash: %w", err)
	}

	if len(infos) == 0 {
		fmt.Println(ui.RenderInfo("Trash is empty."))
		return nil
	}

	fmt.Println(ui.RenderHeader("Trash Contents"))
	fmt.Println()
	for _, info := range infos {
		fmt.Printf("  %s\n", ui.StylePrimary.Render(info.OriginalPath))
		fmt.Printf("    Trashed: %s\n", ui.StyleMuted.Render(info.TrashedAt.Format("2006-01-02 15:04:05")))
		fmt.Printf("    Restore: lyra rm --restore %s\n\n", info.OriginalPath)
	}
	return nil
}

func restoreFromTrash(path string) error {
	if err := trash.RestoreFromTrash(path); err != nil {
		return fmt.Errorf("could not restore %s: %w", path, err)
	}
	fmt.Println(ui.RenderSuccess(fmt.Sprintf("Restored %s", ui.StylePrimary.Render(path))))
	return nil
}
