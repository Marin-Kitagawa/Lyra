package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox"
	"github.com/dropbox/dropbox-sdk-go-unofficial/v6/dropbox/files"
	"github.com/mitchellh/go-homedir"
	"golang.org/x/oauth2"

	"github.com/Marin-Kitagawa/Lyra/internal/resume"
)

const (
	dropboxChunkSize = 8 * 1024 * 1024 // 8MB chunks
)

// dropboxToken holds the Dropbox OAuth token
type dropboxToken struct {
	AccessToken string `json:"access_token"`
}

// DropboxProvider implements Provider for Dropbox
type DropboxProvider struct {
	oauthConfig *oauth2.Config
	accessToken string
	client      files.Client
}

// NewDropboxProvider creates a new Dropbox provider
func NewDropboxProvider(appKey, appSecret string) *DropboxProvider {
	config := &oauth2.Config{
		ClientID:     appKey,
		ClientSecret: appSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://www.dropbox.com/oauth2/authorize",
			TokenURL: "https://api.dropboxapi.com/oauth2/token",
		},
		RedirectURL: "http://localhost:9876/callback",
		Scopes:      []string{"files.content.write", "files.content.read", "files.metadata.read"},
	}
	return &DropboxProvider{oauthConfig: config}
}

// NewDropboxProviderDefault creates a provider with default/demo credentials
func NewDropboxProviderDefault() *DropboxProvider {
	return NewDropboxProvider("YOUR_DROPBOX_APP_KEY", "YOUR_DROPBOX_APP_SECRET")
}

func (d *DropboxProvider) Name() string {
	return "Dropbox"
}

func (d *DropboxProvider) IsAuthenticated() bool {
	return d.accessToken != ""
}

func (d *DropboxProvider) tokenPath() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lyra", "tokens", "dropbox.json"), nil
}

func (d *DropboxProvider) loadToken() error {
	path, err := d.tokenPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var tok dropboxToken
	if err := json.Unmarshal(data, &tok); err != nil {
		return err
	}
	d.accessToken = tok.AccessToken
	return nil
}

func (d *DropboxProvider) saveToken(accessToken string) error {
	path, err := d.tokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tok := dropboxToken{AccessToken: accessToken}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Auth initiates the OAuth2 flow for Dropbox
func (d *DropboxProvider) Auth() error {
	if err := d.loadToken(); err == nil && d.accessToken != "" {
		fmt.Println("Already authenticated with Dropbox.")
		return d.initClient()
	}

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
		fmt.Fprintf(w, "<html><body><h2>Dropbox authentication successful!</h2><p>You can close this tab.</p></body></html>")
		codeCh <- code
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	authURL := d.oauthConfig.AuthCodeURL("lyra-state", oauth2.AccessTypeOffline)
	fmt.Printf("Opening browser for Dropbox authentication...\n")
	fmt.Printf("If the browser doesn't open, visit:\n%s\n\n", authURL)

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

	tok, err := d.oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		return fmt.Errorf("could not exchange code for token: %w", err)
	}

	d.accessToken = tok.AccessToken
	if err := d.saveToken(tok.AccessToken); err != nil {
		return fmt.Errorf("could not save token: %w", err)
	}

	fmt.Println("Successfully authenticated with Dropbox!")
	return d.initClient()
}

func (d *DropboxProvider) initClient() error {
	if d.accessToken == "" {
		return fmt.Errorf("not authenticated")
	}
	cfg := dropbox.Config{Token: d.accessToken}
	d.client = files.New(cfg)
	return nil
}

func (d *DropboxProvider) ensureAuth() error {
	if d.client != nil {
		return nil
	}
	if err := d.loadToken(); err != nil {
		return fmt.Errorf("not authenticated with Dropbox; run: lyra auth dropbox")
	}
	return d.initClient()
}

// normalizePath ensures path starts with /
func normalizePath(path string) string {
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

// Upload uploads a local file to Dropbox
func (d *DropboxProvider) Upload(local, remote string, progress chan<- int64) error {
	if err := d.ensureAuth(); err != nil {
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

	remotePath := normalizePath(remote)

	if stat.Size() <= dropboxChunkSize {
		// Simple upload
		uploadArg := files.NewUploadArg(remotePath)
		uploadArg.Mode = &files.WriteMode{Tagged: dropbox.Tagged{Tag: "overwrite"}}
		_, err = d.client.Upload(uploadArg, f)
		if err == nil && progress != nil {
			progress <- stat.Size()
		}
		return err
	}

	// Chunked upload for large files
	sessionResult, err := d.client.UploadSessionStart(files.NewUploadSessionStartArg(), bytes.NewReader([]byte{}))
	if err != nil {
		return fmt.Errorf("could not start upload session: %w", err)
	}
	sessionID := sessionResult.SessionId

	var offset uint64
	buf := make([]byte, dropboxChunkSize)

	for {
		n, readErr := readFull(f, buf)
		if n == 0 {
			break
		}

		cursor := files.NewUploadSessionCursor(sessionID, offset)
		chunk := buf[:n]

		if readErr != nil || int64(offset)+int64(n) >= stat.Size() {
			// Last chunk
			commitInfo := files.NewCommitInfo(remotePath)
			commitInfo.Mode = &files.WriteMode{Tagged: dropbox.Tagged{Tag: "overwrite"}}
			arg := files.NewUploadSessionFinishArg(cursor, commitInfo)
			_, err = d.client.UploadSessionFinish(arg, bytes.NewReader(chunk))
			if err != nil {
				return fmt.Errorf("could not finish upload session: %w", err)
			}
			if progress != nil {
				progress <- int64(n)
			}
			break
		}

		appendArg := files.NewUploadSessionAppendArg(cursor)
		err = d.client.UploadSessionAppendV2(appendArg, bytes.NewReader(chunk))
		if err != nil {
			return fmt.Errorf("could not append to upload session: %w", err)
		}

		if progress != nil {
			progress <- int64(n)
		}
		offset += uint64(n)
	}

	return nil
}

// Download downloads a file from Dropbox
func (d *DropboxProvider) Download(remote, local string, progress chan<- int64) error {
	if err := d.ensureAuth(); err != nil {
		return err
	}

	remotePath := normalizePath(remote)
	arg := files.NewDownloadArg(remotePath)
	_, content, err := d.client.Download(arg)
	if err != nil {
		return fmt.Errorf("could not download file: %w", err)
	}
	defer content.Close()

	out, err := os.Create(local)
	if err != nil {
		return fmt.Errorf("could not create local file: %w", err)
	}
	defer out.Close()

	buf := make([]byte, 32*1024)
	for {
		n, err := content.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			if progress != nil {
				progress <- int64(n)
			}
		}
		if err != nil {
			break
		}
	}
	return nil
}

// List lists files in a Dropbox folder
func (d *DropboxProvider) List(path string) ([]FileInfo, error) {
	if err := d.ensureAuth(); err != nil {
		return nil, err
	}

	var dbPath string
	if path == "" || path == "/" {
		dbPath = ""
	} else {
		dbPath = normalizePath(path)
	}

	arg := files.NewListFolderArg(dbPath)
	result, err := d.client.ListFolder(arg)
	if err != nil {
		return nil, fmt.Errorf("could not list folder: %w", err)
	}

	var infos []FileInfo
	for _, entry := range result.Entries {
		switch e := entry.(type) {
		case *files.FileMetadata:
			infos = append(infos, FileInfo{
				Name:    e.Name,
				Path:    e.PathDisplay,
				Size:    int64(e.Size),
				IsDir:   false,
				ModTime: e.ClientModified.String(),
			})
		case *files.FolderMetadata:
			infos = append(infos, FileInfo{
				Name:  e.Name,
				Path:  e.PathDisplay,
				IsDir: true,
			})
		}
	}
	return infos, nil
}

// Delete deletes a file from Dropbox
func (d *DropboxProvider) Delete(path string) error {
	if err := d.ensureAuth(); err != nil {
		return err
	}
	arg := files.NewDeleteArg(normalizePath(path))
	_, err := d.client.DeleteV2(arg)
	return err
}

// Resume resumes an interrupted transfer
func (d *DropboxProvider) Resume(state *resume.State) error {
	return d.Upload(state.Src, state.Dest, nil)
}

// readFull reads up to len(buf) bytes
func readFull(f *os.File, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := f.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
