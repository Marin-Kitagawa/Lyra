package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/lyra-cli/lyra/internal/resume"
	"github.com/lyra-cli/lyra/internal/transfer"
	"github.com/lyra-cli/lyra/internal/tui"
	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
)

var (
	cpRecursive   bool
	cpPreserve    bool
	cpResume      bool
	cpSync        bool
	cpChecksum    bool
	cpKeepPartial bool
	cpPassword    string
	cpKeyFile     string
)

var cpCmd = &cobra.Command{
	Use:   "cp <source> <destination>",
	Short: "Copy files and directories",
	Long: `Copy files and directories with progress bars and cloud support.

Supports:
  Local:    lyra cp file.txt /dest/
  SSH/SFTP: lyra cp file.txt user@host:/dest/
            lyra cp file.txt sftp://user@host:22/dest/
  FTP:      lyra cp file.txt ftp://user:pass@host/dest/
  GDrive:   lyra cp file.txt gdrive://MyDrive/dest/
  Dropbox:  lyra cp file.txt dropbox://dest/
  OneDrive: lyra cp file.txt onedrive://dest/

Resume an interrupted transfer:
  lyra cp --resume file.txt /dest/

Skip files that are already identical (like rsync):
  lyra cp --sync file.txt /dest/

Verify checksum after copy:
  lyra cp --checksum file.txt /dest/`,
	Args: cobra.ExactArgs(2),
	RunE: runCp,
}

func init() {
	cpCmd.Flags().BoolVarP(&cpRecursive, "recursive", "r", false, "Copy directories recursively")
	cpCmd.Flags().BoolVar(&cpPreserve, "preserve", true, "Preserve timestamps and permissions")
	cpCmd.Flags().BoolVar(&cpResume, "resume", false, "Resume an interrupted transfer")
	cpCmd.Flags().BoolVar(&cpSync, "sync", false, "Skip files that are identical (like rsync)")
	cpCmd.Flags().BoolVar(&cpChecksum, "checksum", false, "Verify checksum after copy")
	cpCmd.Flags().BoolVar(&cpKeepPartial, "keep-partial", false, "Keep partial file on error")
	cpCmd.Flags().StringVar(&cpPassword, "password", "", "SSH password (if not using key auth)")
	cpCmd.Flags().StringVar(&cpKeyFile, "key", "", "Path to SSH private key file")
}

func runCp(cmd *cobra.Command, args []string) error {
	src := args[0]
	dest := args[1]

	fmt.Println(ui.RenderInfo(fmt.Sprintf("Copying %s → %s",
		ui.StylePrimary.Render(src),
		ui.StyleSecondary.Render(dest),
	)))

	srcType := detectTarget(src)
	destType := detectTarget(dest)

	switch {
	case srcType == "local" && destType == "local":
		return runLocalCopy(src, dest)

	case srcType == "ssh" || destType == "ssh":
		return runSSHCopy(src, dest)

	case srcType == "ftp" || destType == "ftp":
		return runFTPCopy(src, dest)

	case srcType == "gdrive" || destType == "gdrive":
		return fmt.Errorf("GDrive transfers require authentication; run: lyra auth gdrive")

	case srcType == "dropbox" || destType == "dropbox":
		return fmt.Errorf("Dropbox transfers require authentication; run: lyra auth dropbox")

	case srcType == "onedrive" || destType == "onedrive":
		return fmt.Errorf("OneDrive transfers require authentication; run: lyra auth onedrive")

	default:
		return fmt.Errorf("unsupported transfer type: %s → %s", srcType, destType)
	}
}

// detectTarget determines what kind of target a path is
func detectTarget(path string) string {
	switch {
	case strings.HasPrefix(path, "gdrive://"):
		return "gdrive"
	case strings.HasPrefix(path, "dropbox://"):
		return "dropbox"
	case strings.HasPrefix(path, "onedrive://"):
		return "onedrive"
	case transfer.IsFTPTarget(path):
		return "ftp"
	case transfer.IsSSHTarget(path):
		return "ssh"
	default:
		return "local"
	}
}

func runLocalCopy(src, dest string) error {
	opts := transfer.LocalOptions{
		Preserve:    cpPreserve,
		Recursive:   cpRecursive,
		Resume:      cpResume,
		Sync:        cpSync,
		Checksum:    cpChecksum,
		KeepPartial: cpKeepPartial,
	}

	// Get file size for progress entry
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("source does not exist: %w", err)
	}

	pp := tui.NewProgressProgram("copying", nil)

	var size int64
	if !srcInfo.IsDir() {
		size = srcInfo.Size()
	}
	entry := pp.Add(src, size)

	lt := transfer.NewLocalTransfer(opts, entry.Report, nil)

	go func() {
		err := lt.Copy(src, dest)
		entry.Finish(err)
	}()

	paused := pp.Run()

	if paused {
		lt.Cancel()
		fmt.Println(ui.RenderWarning("Transfer paused. Resume with: lyra cp --resume " + src + " " + dest))
		return nil
	}

	fmt.Println(ui.RenderSuccess("Copy complete!"))
	return nil
}

func runSSHCopy(src, dest string) error {
	opts := transfer.SSHOptions{
		Password:    cpPassword,
		KeyFile:     cpKeyFile,
		Recursive:   cpRecursive,
		Resume:      cpResume,
		Preserve:    cpPreserve,
		KeepPartial: cpKeepPartial,
	}

	pp := tui.NewProgressProgram("copying", nil)
	entry := pp.Add(src, -1)

	st := transfer.NewSSHTransfer(opts, entry.Report, nil)

	srcType := detectTarget(src)

	go func() {
		var err error
		if srcType == "ssh" {
			target, parseErr := transfer.ParseSSHTarget(src)
			if parseErr != nil {
				entry.Finish(fmt.Errorf("invalid SSH source: %w", parseErr))
				return
			}
			err = st.Download(target, dest)
		} else {
			target, parseErr := transfer.ParseSSHTarget(dest)
			if parseErr != nil {
				entry.Finish(fmt.Errorf("invalid SSH destination: %w", parseErr))
				return
			}
			err = st.Upload(src, target)
		}
		entry.Finish(err)
	}()

	paused := pp.Run()

	if paused {
		st.Cancel()
		fmt.Println(ui.RenderWarning("Transfer paused. Resume with: lyra cp --resume " + src + " " + dest))
		return nil
	}

	fmt.Println(ui.RenderSuccess("Copy complete!"))
	return nil
}

func runFTPCopy(src, dest string) error {
	opts := transfer.FTPOptions{
		Resume:      cpResume,
		KeepPartial: cpKeepPartial,
	}

	pp := tui.NewProgressProgram("copying", nil)
	entry := pp.Add(src, -1)

	ft := transfer.NewFTPTransfer(opts, entry.Report, nil)

	srcType := detectTarget(src)

	go func() {
		var err error
		if srcType == "ftp" {
			target, parseErr := transfer.ParseFTPTarget(src)
			if parseErr != nil {
				entry.Finish(fmt.Errorf("invalid FTP source: %w", parseErr))
				return
			}
			err = ft.Download(target, dest)
		} else {
			target, parseErr := transfer.ParseFTPTarget(dest)
			if parseErr != nil {
				entry.Finish(fmt.Errorf("invalid FTP destination: %w", parseErr))
				return
			}
			err = ft.Upload(src, target)
		}
		entry.Finish(err)
	}()

	paused := pp.Run()

	if paused {
		fmt.Println(ui.RenderWarning("Transfer interrupted. Resume with: lyra cp --resume " + src + " " + dest))
		return nil
	}

	fmt.Println(ui.RenderSuccess("Copy complete!"))
	return nil
}

// resumeListCmd lists all paused transfers
var resumeListCmd = &cobra.Command{
	Use:   "resume",
	Short: "List all paused transfers",
	Long:  `Show all interrupted transfers that can be resumed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		states, err := resume.ListAll()
		if err != nil {
			return fmt.Errorf("could not list resume states: %w", err)
		}

		if len(states) == 0 {
			fmt.Println(ui.RenderInfo("No paused transfers found."))
			return nil
		}

		fmt.Println(ui.RenderHeader("Paused Transfers"))
		fmt.Println()
		for _, s := range states {
			pct := 0.0
			if s.TotalBytes > 0 {
				pct = float64(s.BytesDone) / float64(s.TotalBytes) * 100
			}
			fmt.Printf("  %s\n", ui.StylePrimary.Render(s.Src))
			fmt.Printf("    → %s\n", ui.StyleSecondary.Render(s.Dest))
			fmt.Printf("    Progress: %.1f%% (%s)\n", pct, s.Timestamp.Format("2006-01-02 15:04:05"))
			fmt.Printf("    Resume:   lyra cp --resume %s %s\n\n", s.Src, s.Dest)
		}
		return nil
	},
}
