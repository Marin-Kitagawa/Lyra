package cmd

import (
	"fmt"
	"os"

	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "lyra",
	Short: "lyra — a beautiful, feature-rich replacement for cp, mkdir, rm and more",
	Long: ui.StylePrimary.Bold(true).Render("✨ lyra") + " — the file management tool you always deserved\n\n" +
		"Replace cp, mkdir, rm and more with a beautiful, fast, cloud-aware alternative.\n\n" +
		ui.StyleSecondary.Render("Commands:") + "\n" +
		"  cp      Copy files and directories (local, SSH, FTP, cloud)\n" +
		"  md      Create directories with optional auto-cd\n" +
		"  rm      Remove files (trash by default)\n" +
		"  mv      Move and rename files with progress\n" +
		"  ls      Beautiful directory listing\n" +
		"  find    Find files with rich filtering\n" +
		"  sync    Sync directories like rsync\n" +
		"  touch   Create files or update timestamps\n" +
		"  info    Show detailed file information\n" +
		"  rename  Batch rename files with patterns\n" +
		"  auth    Authenticate with cloud providers\n",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, ui.RenderError(err.Error()))
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(cpCmd)
	rootCmd.AddCommand(mdCmd)
	rootCmd.AddCommand(rmCmd)
	rootCmd.AddCommand(mvCmd)
	rootCmd.AddCommand(lsCmd)
	rootCmd.AddCommand(findCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(touchCmd)
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(renameCmd)
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(resumeListCmd)
}
