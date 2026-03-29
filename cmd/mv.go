package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lyra-cli/lyra/internal/tui"
	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
)

var mvCmd = &cobra.Command{
	Use:   "mv <source> <destination>",
	Short: "Move or rename files with progress bar",
	Long: `Move or rename files and directories.

If source and destination are on the same filesystem, uses rename (instant).
Otherwise, copies then removes the source.

Examples:
  lyra mv file.txt newname.txt
  lyra mv file.txt /other/path/
  lyra mv dir/ /new/location/`,
	Args: cobra.ExactArgs(2),
	RunE: runMv,
}

func runMv(cmd *cobra.Command, args []string) error {
	src := args[0]
	dest := args[1]

	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("source does not exist: %w", err)
	}

	// If dest is a directory, move src inside it
	if destInfo, err := os.Stat(dest); err == nil && destInfo.IsDir() {
		dest = filepath.Join(dest, filepath.Base(src))
	}

	fmt.Println(ui.RenderInfo(fmt.Sprintf("Moving %s → %s",
		ui.StylePrimary.Render(src),
		ui.StyleSecondary.Render(dest),
	)))

	// Try fast rename first (same filesystem)
	if err := os.Rename(src, dest); err == nil {
		fmt.Println(ui.RenderSuccess("Moved successfully!"))
		return nil
	}

	// Cross-device move: copy then remove
	var records []tui.SummaryRecord

	if srcInfo.IsDir() {
		if err := moveDirBubble(src, dest, &records); err != nil {
			return err
		}
	} else {
		start := time.Now()
		err := moveFileBubble(src, dest, srcInfo)
		records = append(records, tui.SummaryRecord{
			Name:     filepath.Base(src),
			Op:       "Move",
			Err:      err,
			Size:     srcInfo.Size(),
			Duration: time.Since(start),
		})
		if err != nil {
			return err
		}
	}

	// Remove source
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("move failed: could not remove source: %w", err)
	}

	if !noSummary {
		tui.ShowSummary(records)
	}

	fmt.Println(ui.RenderSuccess("Move complete!"))
	return nil
}

func moveFileBubble(src, dest string, srcInfo os.FileInfo) error {
	pp := tui.NewProgressProgram("moving", nil)
	entry := pp.Add(filepath.Base(src), srcInfo.Size())

	var copyErr error
	go func() {
		err := moveFileIO(src, dest, srcInfo, entry.Report)
		entry.Finish(err)
		copyErr = err
	}()

	pp.Run()
	return copyErr
}

func moveFileIO(src, dest string, srcInfo os.FileInfo, reportFn func(int64)) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("could not open source: %w", err)
	}
	defer srcFile.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	destFile, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("could not create destination: %w", err)
	}
	defer func() {
		destFile.Close()
		os.Chmod(dest, srcInfo.Mode())
		os.Chtimes(dest, srcInfo.ModTime(), srcInfo.ModTime())
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
				os.Remove(dest)
				return fmt.Errorf("write error: %w", werr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			os.Remove(dest)
			return fmt.Errorf("read error: %w", err)
		}
	}

	return nil
}

func moveDirBubble(src, dest string, records *[]tui.SummaryRecord) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dest, rel)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		start := time.Now()
		moveErr := moveFileBubble(path, destPath, info)
		*records = append(*records, tui.SummaryRecord{
			Name:     filepath.Base(path),
			Op:       "Move",
			Err:      moveErr,
			Size:     info.Size(),
			Duration: time.Since(start),
		})
		return moveErr
	})
}
