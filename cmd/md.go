package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lyra-cli/lyra/internal/tui"
	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
)

var mdCd bool

var mdCmd = &cobra.Command{
	Use:   "md <path>",
	Short: "Create directories",
	Long: `Create directories (including all parents).

With --cd, the directory is created and the path is written to ~/.lyra/.cdto
so a shell function can cd into it automatically.

Shell integration — add this to your ~/.bashrc or ~/.zshrc:
  lmd() { lyra md "$@" && [ -f ~/.lyra/.cdto ] && cd "$(cat ~/.lyra/.cdto)" && rm ~/.lyra/.cdto; }

Then use 'lmd' instead of 'lyra md' to automatically cd into created directories.

Examples:
  lyra md /tmp/my/new/dir
  lmd my-project/src    # Creates and cds into it`,
	Args: cobra.ExactArgs(1),
	RunE: runMd,
}

func init() {
	mdCmd.Flags().BoolVar(&mdCd, "cd", false, "Write path to ~/.lyra/.cdto for shell auto-cd")
}

func runMd(cmd *cobra.Command, args []string) error {
	path := args[0]

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("could not resolve path: %w", err)
	}

	if err := os.MkdirAll(absPath, 0755); err != nil {
		return fmt.Errorf("could not create directory: %w", err)
	}

	tui.Print(ui.RenderSuccess(fmt.Sprintf("Created %s", ui.StylePrimary.Render(absPath))))

	if mdCd {
		if err := writeCdTo(absPath); err != nil {
			return fmt.Errorf("could not write cd path: %w", err)
		}
	}

	return nil
}

// writeCdTo writes the path to ~/.lyra/.cdto for shell integration
func writeCdTo(path string) error {
	home, err := homedir.Dir()
	if err != nil {
		return err
	}

	lyraDir := filepath.Join(home, ".lyra")
	if err := os.MkdirAll(lyraDir, 0755); err != nil {
		return err
	}

	cdtoPath := filepath.Join(lyraDir, ".cdto")
	return os.WriteFile(cdtoPath, []byte(path), 0644)
}
