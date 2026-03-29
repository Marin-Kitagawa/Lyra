package cmd

import (
	"fmt"
	"strings"

	"github.com/lyra-cli/lyra/internal/transfer/cloud"
	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth <provider>",
	Short: "Authenticate with cloud providers",
	Long: `Authenticate with cloud storage providers.

Supported providers:
  gdrive    Google Drive
  dropbox   Dropbox
  onedrive  Microsoft OneDrive

Examples:
  lyra auth gdrive
  lyra auth dropbox
  lyra auth onedrive
  lyra auth status    # Show authentication status`,
	Args: cobra.ExactArgs(1),
	RunE: runAuth,
}

func runAuth(cmd *cobra.Command, args []string) error {
	provider := strings.ToLower(args[0])

	switch provider {
	case "gdrive", "google", "googledrive":
		return authGDrive()
	case "dropbox", "db":
		return authDropbox()
	case "onedrive", "od", "microsoft":
		return authOneDrive()
	case "status":
		return showAuthStatus()
	default:
		return fmt.Errorf("unknown provider: %s\n\nSupported: gdrive, dropbox, onedrive", provider)
	}
}

func authGDrive() error {
	fmt.Println(ui.RenderHeader("Google Drive Authentication"))
	fmt.Println()
	fmt.Println(ui.RenderInfo("Starting OAuth2 flow for Google Drive..."))
	fmt.Println()

	p := cloud.NewGDriveProviderDefault()

	fmt.Println(ui.StyleWarning.Render("Note: Using demo credentials."))
	fmt.Println(ui.StyleMuted.Render("For production use, set your own credentials in ~/.lyra/config.json:"))
	fmt.Println(ui.StyleMuted.Render(`  {
    "gdrive_client_id": "your-client-id",
    "gdrive_client_secret": "your-client-secret"
  }`))
	fmt.Println()

	if err := p.Auth(); err != nil {
		return fmt.Errorf("Google Drive authentication failed: %w", err)
	}

	return nil
}

func authDropbox() error {
	fmt.Println(ui.RenderHeader("Dropbox Authentication"))
	fmt.Println()
	fmt.Println(ui.RenderInfo("Starting OAuth2 flow for Dropbox..."))
	fmt.Println()

	p := cloud.NewDropboxProviderDefault()

	fmt.Println(ui.StyleWarning.Render("Note: Using demo credentials."))
	fmt.Println(ui.StyleMuted.Render("For production use, set your credentials in ~/.lyra/config.json:"))
	fmt.Println(ui.StyleMuted.Render(`  {
    "dropbox_app_key": "your-app-key",
    "dropbox_app_secret": "your-app-secret"
  }`))
	fmt.Println()

	if err := p.Auth(); err != nil {
		return fmt.Errorf("Dropbox authentication failed: %w", err)
	}

	return nil
}

func authOneDrive() error {
	fmt.Println(ui.RenderHeader("OneDrive Authentication"))
	fmt.Println()
	fmt.Println(ui.RenderInfo("Starting OAuth2 flow for Microsoft OneDrive..."))
	fmt.Println()

	p := cloud.NewOneDriveProviderDefault()

	fmt.Println(ui.StyleWarning.Render("Note: Using demo credentials."))
	fmt.Println(ui.StyleMuted.Render("For production use, set your credentials in ~/.lyra/config.json:"))
	fmt.Println(ui.StyleMuted.Render(`  {
    "onedrive_client_id": "your-client-id",
    "onedrive_tenant_id": "common"
  }`))
	fmt.Println()

	if err := p.Auth(); err != nil {
		return fmt.Errorf("OneDrive authentication failed: %w", err)
	}

	return nil
}

func showAuthStatus() error {
	fmt.Println(ui.RenderHeader("Cloud Provider Status"))
	fmt.Println()

	providers := []struct {
		name     string
		provider cloud.Provider
	}{
		{"Google Drive", cloud.NewGDriveProviderDefault()},
		{"Dropbox", cloud.NewDropboxProviderDefault()},
		{"OneDrive", cloud.NewOneDriveProviderDefault()},
	}

	for _, p := range providers {
		status := ui.StyleError.Render("✗ Not authenticated")
		if p.provider.IsAuthenticated() {
			status = ui.StyleSuccess.Render("✓ Authenticated")
		}
		fmt.Printf("  %-12s %s\n", ui.StylePrimary.Render(p.name), status)
	}
	fmt.Println()
	return nil
}
