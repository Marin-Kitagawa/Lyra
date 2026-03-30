package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lyra-cli/lyra/internal/tui"
	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
)

var (
	touchTime       string
	touchNoCreate   bool
	touchAccessOnly bool
	touchModOnly    bool
)

var touchCmd = &cobra.Command{
	Use:   "touch <file>...",
	Short: "Create empty files or update timestamps",
	Long: `Create empty files if they don't exist, or update their timestamps.

Examples:
  lyra touch file.txt
  lyra touch file1.txt file2.txt file3.txt
  lyra touch --no-create file.txt    # Only update time if file exists
  lyra touch --time "2024-01-15 10:30:00" file.txt`,
	Args: cobra.MinimumNArgs(1),
	RunE: runTouch,
}

func init() {
	touchCmd.Flags().StringVarP(&touchTime, "time", "t", "", "Timestamp to use (format: 2006-01-02 15:04:05)")
	touchCmd.Flags().BoolVar(&touchNoCreate, "no-create", false, "Do not create files that don't exist")
	touchCmd.Flags().BoolVar(&touchAccessOnly, "access", false, "Only update access time")
	touchCmd.Flags().BoolVar(&touchModOnly, "modify", false, "Only update modification time")
}

func runTouch(cmd *cobra.Command, args []string) error {
	var t time.Time
	var err error

	if touchTime != "" {
		t, err = time.ParseInLocation("2006-01-02 15:04:05", touchTime, time.Local)
		if err != nil {
			t, err = time.Parse(time.RFC3339, touchTime)
			if err != nil {
				return fmt.Errorf("invalid time format: %s (use '2006-01-02 15:04:05' or RFC3339)", touchTime)
			}
		}
	} else {
		t = time.Now()
	}

	for _, path := range args {
		if err := touchFile(path, t); err != nil {
			return err
		}
	}

	return nil
}

func touchFile(path string, t time.Time) error {
	info, err := os.Stat(path)

	if os.IsNotExist(err) {
		if touchNoCreate {
			return nil // Skip non-existent files
		}

		// Create parent directories if needed
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("could not create parent directory: %w", err)
		}

		// Create the file
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("could not create %s: %w", path, err)
		}
		f.Close()

		tui.Print(ui.RenderSuccess(fmt.Sprintf("Created %s", ui.StylePrimary.Render(path))))

		// Set timestamp
		return os.Chtimes(path, t, t)
	}

	if err != nil {
		return fmt.Errorf("could not access %s: %w", path, err)
	}

	// File exists, update timestamps
	atime := info.ModTime()
	mtime := info.ModTime()

	if !touchAccessOnly {
		mtime = t
	}
	if !touchModOnly {
		atime = t
	}

	if err := os.Chtimes(path, atime, mtime); err != nil {
		return fmt.Errorf("could not update timestamp for %s: %w", path, err)
	}

	tui.Print(ui.RenderSuccess(fmt.Sprintf("Touched %s", ui.StylePrimary.Render(path))))
	return nil
}
