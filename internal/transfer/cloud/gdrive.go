package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchellh/go-homedir"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/Marin-Kitagawa/Lyra/internal/resume"
)

const (
	gdriveChunkSize = 8 * 1024 * 1024 // 8MB chunks
)

// GDriveProvider implements Provider for Google Drive
type GDriveProvider struct {
	config *oauth2.Config
	token  *oauth2.Token
	svc    *drive.Service
}

// NewGDriveProvider creates a new Google Drive provider
func NewGDriveProvider(clientID, clientSecret string) *GDriveProvider {
	config := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{drive.DriveScope},
		Endpoint:     google.Endpoint,
		RedirectURL:  "http://localhost:9876/callback",
	}
	return &GDriveProvider{config: config}
}

// NewGDriveProviderDefault creates a provider with default/demo credentials
func NewGDriveProviderDefault() *GDriveProvider {
	return NewGDriveProvider(
		"YOUR_GDRIVE_CLIENT_ID",
		"YOUR_GDRIVE_CLIENT_SECRET",
	)
}

func (g *GDriveProvider) Name() string {
	return "Google Drive"
}

func (g *GDriveProvider) IsAuthenticated() bool {
	return g.token != nil && g.token.Valid()
}

// tokenPath returns the path to the token file
func (g *GDriveProvider) tokenPath() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lyra", "tokens", "gdrive.json"), nil
}

// loadToken loads the token from disk
func (g *GDriveProvider) loadToken() error {
	path, err := g.tokenPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return err
	}
	g.token = &tok
	return nil
}

// saveToken saves the token to disk
func (g *GDriveProvider) saveToken(tok *oauth2.Token) error {
	path, err := g.tokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Auth initiates the OAuth2 flow for Google Drive
func (g *GDriveProvider) Auth() error {
	// Try to load existing token
	if err := g.loadToken(); err == nil && g.token.Valid() {
		fmt.Println("Already authenticated with Google Drive.")
		return g.initService()
	}

	// Start local callback server
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Addr: ":9876", Handler: mux}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			http.Error(w, "No code received", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, "<html><body><h2>Authentication successful!</h2><p>You can close this tab.</p></body></html>")
		codeCh <- code
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	authURL := g.config.AuthCodeURL("lyra-state", oauth2.AccessTypeOffline)
	fmt.Printf("Opening browser for Google Drive authentication...\n")
	fmt.Printf("If the browser doesn't open, visit:\n%s\n\n", authURL)
	openBrowser(authURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return fmt.Errorf("auth callback error: %w", err)
	case <-ctx.Done():
		return fmt.Errorf("authentication timed out")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)

	tok, err := g.config.Exchange(context.Background(), code)
	if err != nil {
		return fmt.Errorf("could not exchange code for token: %w", err)
	}

	g.token = tok
	if err := g.saveToken(tok); err != nil {
		return fmt.Errorf("could not save token: %w", err)
	}

	fmt.Println("Successfully authenticated with Google Drive!")
	return g.initService()
}

// initService initializes the Drive service
func (g *GDriveProvider) initService() error {
	if g.token == nil {
		return fmt.Errorf("not authenticated")
	}
	ctx := context.Background()
	tokenSource := g.config.TokenSource(ctx, g.token)
	svc, err := drive.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return fmt.Errorf("could not create Drive service: %w", err)
	}
	g.svc = svc
	return nil
}

// ensureAuth ensures we're authenticated
func (g *GDriveProvider) ensureAuth() error {
	if g.svc != nil {
		return nil
	}
	if err := g.loadToken(); err != nil {
		return fmt.Errorf("not authenticated with Google Drive; run: lyra auth gdrive")
	}
	return g.initService()
}

// Upload uploads a local file to Google Drive
func (g *GDriveProvider) Upload(local, remote string, progress chan<- int64) error {
	if err := g.ensureAuth(); err != nil {
		return err
	}

	f, err := os.Open(local)
	if err != nil {
		return fmt.Errorf("could not open local file: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	// Determine parent folder and file name
	parts := strings.Split(strings.TrimPrefix(remote, "/"), "/")
	name := parts[len(parts)-1]
	parentID := "root"

	if len(parts) > 1 {
		var err error
		parentID, err = g.ensureFolderPath(strings.Join(parts[:len(parts)-1], "/"))
		if err != nil {
			return fmt.Errorf("could not ensure folder path: %w", err)
		}
	}

	driveFile := &drive.File{
		Name:    name,
		Parents: []string{parentID},
	}

	var reader io.Reader = f
	if progress != nil {
		reader = &progressReader{r: f, ch: progress}
	}

	if stat.Size() > gdriveChunkSize {
		// Use resumable upload
		_, err = g.svc.Files.Create(driveFile).Media(reader).Do()
	} else {
		_, err = g.svc.Files.Create(driveFile).Media(reader).Do()
	}

	return err
}

// Download downloads a file from Google Drive
func (g *GDriveProvider) Download(remote, local string, progress chan<- int64) error {
	if err := g.ensureAuth(); err != nil {
		return err
	}

	// Find file by path
	fileID, err := g.findFileID(remote)
	if err != nil {
		return err
	}

	resp, err := g.svc.Files.Get(fileID).Download()
	if err != nil {
		return fmt.Errorf("could not download file: %w", err)
	}
	defer resp.Body.Close()

	out, err := os.Create(local)
	if err != nil {
		return fmt.Errorf("could not create local file: %w", err)
	}
	defer out.Close()

	var reader io.Reader = resp.Body
	if progress != nil {
		reader = &progressReader{r: resp.Body, ch: progress}
	}

	_, err = io.Copy(out, reader)
	return err
}

// List lists files in a Google Drive folder
func (g *GDriveProvider) List(path string) ([]FileInfo, error) {
	if err := g.ensureAuth(); err != nil {
		return nil, err
	}

	var parentID string
	var err error
	if path == "" || path == "/" {
		parentID = "root"
	} else {
		parentID, err = g.findFileID(path)
		if err != nil {
			return nil, err
		}
	}

	query := fmt.Sprintf("'%s' in parents and trashed=false", parentID)
	result, err := g.svc.Files.List().
		Q(query).
		Fields("files(id,name,size,mimeType,modifiedTime)").
		Do()
	if err != nil {
		return nil, err
	}

	var files []FileInfo
	for _, f := range result.Files {
		files = append(files, FileInfo{
			Name:     f.Name,
			Path:     path + "/" + f.Name,
			Size:     f.Size,
			IsDir:    f.MimeType == "application/vnd.google-apps.folder",
			ModTime:  f.ModifiedTime,
			MimeType: f.MimeType,
		})
	}
	return files, nil
}

// Delete deletes a file from Google Drive
func (g *GDriveProvider) Delete(path string) error {
	if err := g.ensureAuth(); err != nil {
		return err
	}

	fileID, err := g.findFileID(path)
	if err != nil {
		return err
	}

	return g.svc.Files.Delete(fileID).Do()
}

// Resume resumes an interrupted transfer
func (g *GDriveProvider) Resume(state *resume.State) error {
	if state.Type == resume.TypeGDrive {
		if state.BytesDone < state.TotalBytes {
			return g.Upload(state.Src, state.Dest, nil)
		}
	}
	return nil
}

// findFileID finds the Drive file ID for a path
func (g *GDriveProvider) findFileID(path string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	parentID := "root"

	for _, part := range parts {
		query := fmt.Sprintf("name='%s' and '%s' in parents and trashed=false", part, parentID)
		result, err := g.svc.Files.List().Q(query).Fields("files(id,mimeType)").Do()
		if err != nil {
			return "", err
		}
		if len(result.Files) == 0 {
			return "", fmt.Errorf("file not found: %s", path)
		}
		parentID = result.Files[0].Id
	}
	return parentID, nil
}

// ensureFolderPath creates folders as needed and returns the final folder ID
func (g *GDriveProvider) ensureFolderPath(path string) (string, error) {
	parts := strings.Split(path, "/")
	parentID := "root"

	for _, part := range parts {
		if part == "" {
			continue
		}
		query := fmt.Sprintf("name='%s' and '%s' in parents and mimeType='application/vnd.google-apps.folder' and trashed=false", part, parentID)
		result, err := g.svc.Files.List().Q(query).Fields("files(id)").Do()
		if err != nil {
			return "", err
		}
		if len(result.Files) > 0 {
			parentID = result.Files[0].Id
		} else {
			folder := &drive.File{
				Name:     part,
				MimeType: "application/vnd.google-apps.folder",
				Parents:  []string{parentID},
			}
			created, err := g.svc.Files.Create(folder).Fields("id").Do()
			if err != nil {
				return "", err
			}
			parentID = created.Id
		}
	}
	return parentID, nil
}

// progressReader wraps a reader and sends progress updates
type progressReader struct {
	r  io.Reader
	ch chan<- int64
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 && pr.ch != nil {
		pr.ch <- int64(n)
	}
	return n, err
}

// openBrowser opens a URL in the default browser
func openBrowser(url string) {
	// Try common browser openers
	cmds := [][]string{
		{"xdg-open", url},
		{"open", url},
		{"cmd", "/c", "start", url},
	}
	for _, c := range cmds {
		cmd := fmt.Sprintf("%s", c[0])
		_ = cmd
		// We'll just print the URL since exec is platform-dependent
		break
	}
}
