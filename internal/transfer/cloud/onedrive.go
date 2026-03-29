package cloud

import (
	"bytes"
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

	"github.com/lyra-cli/lyra/internal/resume"
)

const (
	onedriveChunkSize    = 10 * 1024 * 1024 // 10MB chunks
	onedriveGraphBaseURL = "https://graph.microsoft.com/v1.0"
)

// OneDriveProvider implements Provider for Microsoft OneDrive
type OneDriveProvider struct {
	oauthConfig *oauth2.Config
	token       *oauth2.Token
	httpClient  *http.Client
}

// NewOneDriveProvider creates a new OneDrive provider
func NewOneDriveProvider(clientID, tenantID string) *OneDriveProvider {
	config := &oauth2.Config{
		ClientID: clientID,
		Endpoint: oauth2.Endpoint{
			AuthURL:  fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", tenantID),
			TokenURL: fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
		},
		RedirectURL: "http://localhost:9876/callback",
		Scopes:      []string{"Files.ReadWrite", "offline_access"},
	}
	return &OneDriveProvider{oauthConfig: config}
}

// NewOneDriveProviderDefault creates a provider with default/demo credentials
func NewOneDriveProviderDefault() *OneDriveProvider {
	return NewOneDriveProvider("YOUR_ONEDRIVE_CLIENT_ID", "common")
}

func (o *OneDriveProvider) Name() string {
	return "OneDrive"
}

func (o *OneDriveProvider) IsAuthenticated() bool {
	return o.token != nil && o.token.Valid()
}

func (o *OneDriveProvider) tokenPath() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lyra", "tokens", "onedrive.json"), nil
}

func (o *OneDriveProvider) loadToken() error {
	path, err := o.tokenPath()
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
	o.token = &tok
	return nil
}

func (o *OneDriveProvider) saveToken(tok *oauth2.Token) error {
	path, err := o.tokenPath()
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

// Auth initiates the OAuth2 flow for OneDrive
func (o *OneDriveProvider) Auth() error {
	if err := o.loadToken(); err == nil && o.token.Valid() {
		fmt.Println("Already authenticated with OneDrive.")
		o.initClient()
		return nil
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
		fmt.Fprintf(w, "<html><body><h2>OneDrive authentication successful!</h2><p>You can close this tab.</p></body></html>")
		codeCh <- code
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	authURL := o.oauthConfig.AuthCodeURL("lyra-state", oauth2.AccessTypeOffline)
	fmt.Printf("Opening browser for OneDrive authentication...\n")
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

	tok, err := o.oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		return fmt.Errorf("could not exchange code for token: %w", err)
	}

	o.token = tok
	if err := o.saveToken(tok); err != nil {
		return fmt.Errorf("could not save token: %w", err)
	}

	fmt.Println("Successfully authenticated with OneDrive!")
	o.initClient()
	return nil
}

func (o *OneDriveProvider) initClient() {
	tokenSource := o.oauthConfig.TokenSource(context.Background(), o.token)
	o.httpClient = oauth2.NewClient(context.Background(), tokenSource)
}

func (o *OneDriveProvider) ensureAuth() error {
	if o.httpClient != nil {
		return nil
	}
	if err := o.loadToken(); err != nil {
		return fmt.Errorf("not authenticated with OneDrive; run: lyra auth onedrive")
	}
	o.initClient()
	return nil
}

// graphRequest makes an authenticated request to Microsoft Graph API
func (o *OneDriveProvider) graphRequest(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	url := onedriveGraphBaseURL + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return o.httpClient.Do(req)
}

// Upload uploads a local file to OneDrive
func (o *OneDriveProvider) Upload(local, remote string, progress chan<- int64) error {
	if err := o.ensureAuth(); err != nil {
		return err
	}

	stat, err := os.Stat(local)
	if err != nil {
		return err
	}

	remotePath := strings.TrimPrefix(remote, "/")
	apiPath := fmt.Sprintf("/me/drive/root:/%s:/createUploadSession", remotePath)

	// Create upload session
	sessionReq := map[string]interface{}{
		"item": map[string]string{
			"@microsoft.graph.conflictBehavior": "replace",
		},
	}
	sessionBody, _ := json.Marshal(sessionReq)

	resp, err := o.graphRequest("POST", apiPath, bytes.NewReader(sessionBody), "application/json")
	if err != nil {
		return fmt.Errorf("could not create upload session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload session creation failed (%d): %s", resp.StatusCode, string(body))
	}

	var sessionData struct {
		UploadURL string `json:"uploadUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessionData); err != nil {
		return fmt.Errorf("could not parse upload session response: %w", err)
	}

	// Upload in chunks
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	var offset int64
	buf := make([]byte, onedriveChunkSize)
	totalSize := stat.Size()

	for offset < totalSize {
		n, readErr := io.ReadFull(f, buf)
		if n == 0 {
			break
		}
		chunk := buf[:n]
		end := offset + int64(n) - 1

		req, err := http.NewRequest("PUT", sessionData.UploadURL, bytes.NewReader(chunk))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, end, totalSize))
		req.Header.Set("Content-Length", fmt.Sprintf("%d", n))

		chunkResp, err := o.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("chunk upload failed: %w", err)
		}
		chunkResp.Body.Close()

		if chunkResp.StatusCode != http.StatusAccepted && chunkResp.StatusCode != http.StatusCreated && chunkResp.StatusCode != http.StatusOK {
			return fmt.Errorf("chunk upload failed with status %d", chunkResp.StatusCode)
		}

		if progress != nil {
			progress <- int64(n)
		}
		offset += int64(n)

		if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
			break
		}
	}

	return nil
}

// Download downloads a file from OneDrive
func (o *OneDriveProvider) Download(remote, local string, progress chan<- int64) error {
	if err := o.ensureAuth(); err != nil {
		return err
	}

	remotePath := strings.TrimPrefix(remote, "/")
	apiPath := fmt.Sprintf("/me/drive/root:/%s:/content", remotePath)

	resp, err := o.graphRequest("GET", apiPath, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed (%d): %s", resp.StatusCode, string(body))
	}

	out, err := os.Create(local)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
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

// List lists files in a OneDrive folder
func (o *OneDriveProvider) List(path string) ([]FileInfo, error) {
	if err := o.ensureAuth(); err != nil {
		return nil, err
	}

	var apiPath string
	if path == "" || path == "/" {
		apiPath = "/me/drive/root/children"
	} else {
		remotePath := strings.TrimPrefix(path, "/")
		apiPath = fmt.Sprintf("/me/drive/root:/%s:/children", remotePath)
	}

	resp, err := o.graphRequest("GET", apiPath, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Value []struct {
			Name             string `json:"name"`
			Size             int64  `json:"size"`
			LastModifiedDateTime string `json:"lastModifiedDateTime"`
			Folder           *struct{} `json:"folder"`
			File             *struct {
				MimeType string `json:"mimeType"`
			} `json:"file"`
		} `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var infos []FileInfo
	for _, item := range result.Value {
		fi := FileInfo{
			Name:    item.Name,
			Path:    filepath.Join(path, item.Name),
			Size:    item.Size,
			IsDir:   item.Folder != nil,
			ModTime: item.LastModifiedDateTime,
		}
		if item.File != nil {
			fi.MimeType = item.File.MimeType
		}
		infos = append(infos, fi)
	}
	return infos, nil
}

// Delete deletes a file from OneDrive
func (o *OneDriveProvider) Delete(path string) error {
	if err := o.ensureAuth(); err != nil {
		return err
	}

	remotePath := strings.TrimPrefix(path, "/")
	apiPath := fmt.Sprintf("/me/drive/root:/%s", remotePath)

	resp, err := o.graphRequest("DELETE", apiPath, nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete failed with status %d", resp.StatusCode)
	}
	return nil
}

// Resume resumes an interrupted transfer
func (o *OneDriveProvider) Resume(state *resume.State) error {
	return o.Upload(state.Src, state.Dest, nil)
}
