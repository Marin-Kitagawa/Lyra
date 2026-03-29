package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
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
	if srcInfo.IsDir() {
		if err := moveDir(src, dest); err != nil {
			return err
		}
	} else {
		if err := moveFile(src, dest, srcInfo); err != nil {
			return err
		}
	}

	// Remove source
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("move failed: could not remove source: %w", err)
	}

	fmt.Println(ui.RenderSuccess("Move complete!"))
	return nil
}

func moveFile(src, dest string, srcInfo os.FileInfo) error {
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

	p := mpb.New(
		mpb.WithWidth(60),
		mpb.WithRefreshRate(100*time.Millisecond),
	)
	name := filepath.Base(src)
	if len(name) > 20 {
		name = "..." + name[len(name)-17:]
	}
	bar := p.New(srcInfo.Size(),
		mpb.BarStyle().Lbound("").Filler("█").Tip("▓").Padding("░").Rbound(""),
		mpb.PrependDecorators(
			decor.Name(name, decor.WC{W: 22, C: decor.DindentRight | decor.DextraSpace}),
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

	buf := make([]byte, 1024*1024)
	start := time.Now()

	for {
		n, err := srcFile.Read(buf)
		if n > 0 {
			nw, werr := destFile.Write(buf[:n])
			elapsed := time.Since(start)
			if elapsed > 0 {
				bar.EwmaIncrInt64(int64(nw), elapsed)
				start = time.Now()
			} else {
				bar.IncrInt64(int64(nw))
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

	p.Wait()
	return nil
}

func moveDir(src, dest string) error {
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

		return moveFile(path, destPath, info)
	})
}
