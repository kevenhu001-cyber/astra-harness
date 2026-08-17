// Package auth implements the CLI side of Astra's account OAuth: an
// RFC 8628-style device flow against the site's auth server. `astra login`
// starts a grant, opens the browser at the website, and polls until the user
// approves; the resulting bearer token is stored under the user config dir.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultServer is used when neither config nor env overrides it. It points at
// the self-hosted Astra official site/auth server.
const DefaultServer = "https://astracode.topodrive.top"

// User is the client-safe account shape returned by the auth server.
type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// Credential is a stored login (mirrors ~/.config/gh/hosts.yml style).
type Credential struct {
	Server    string    `json:"server"`
	Token     string    `json:"token"`
	User      User      `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Client talks to an Astra auth server.
type Client struct {
	Server string
	HTTP   *http.Client
}

func New(server string) *Client {
	if server == "" {
		server = DefaultServer
	}
	return &Client{Server: strings.TrimSuffix(server, "/"), HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// DeviceFlow is a pending device authorization (from POST /api/auth/device).
type DeviceFlow struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// PollResult is one poll of POST /api/auth/device/token.
type PollResult struct {
	Status      string `json:"status"` // pending | approved | expired
	AccessToken string `json:"access_token,omitempty"`
	User        *User  `json:"user,omitempty"`
}

// StartDevice requests a new device grant.
func (c *Client) StartDevice(ctx context.Context) (*DeviceFlow, error) {
	var out DeviceFlow
	if err := c.post(ctx, "/api/auth/device", nil, &out); err != nil {
		return nil, err
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	return &out, nil
}

// PollDevice checks the grant status once.
func (c *Client) PollDevice(ctx context.Context, deviceCode string) (*PollResult, error) {
	var out PollResult
	if err := c.post(ctx, "/api/auth/device/token", map[string]any{"device_code": deviceCode}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Me fetches the account behind a bearer token.
func (c *Client) Me(ctx context.Context, token string) (*User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/api/auth/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		User User `json:"user"`
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth server: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.User.ID == "" {
		return nil, nil // 200 + user:null means not authenticated
	}
	return &out.User, nil
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("auth server: %s", msg)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// OpenBrowser opens a URL in the user's default browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// Login runs the full device flow:
//  1. request a grant, 2. print + open the verification URI,
//  3. poll until the user approves in the browser, 4. return the credential.
func Login(ctx context.Context, server string, out io.Writer) (*Credential, error) {
	c := New(server)
	flow, err := c.StartDevice(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "\n  Open: %s\n  Code: %s\n\n", flow.VerificationURI, flow.UserCode)
	fmt.Fprintf(out, "  Waiting for browser authorization... (expires in %ds)\n", flow.ExpiresIn)
	_ = OpenBrowser(flow.VerificationURI)

	interval := time.Duration(flow.Interval) * time.Second
	if interval < time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(flow.ExpiresIn) * time.Second)
	for {
		if time.Now().After(deadline) {
			return nil, errors.New("authorization expired — run `astra login` again")
		}
		res, err := c.PollDevice(ctx, flow.DeviceCode)
		if err != nil {
			// Transient server errors: keep polling until the deadline.
			time.Sleep(interval)
			continue
		}
		switch res.Status {
		case "approved":
			cred := &Credential{
				Server:    c.Server,
				Token:     res.AccessToken,
				User:      *res.User,
				ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
			}
			return cred, nil
		case "expired":
			return nil, errors.New("authorization expired — run `astra login` again")
		default: // pending
			time.Sleep(interval)
		}
	}
}

// --- credential storage ---

// CredentialPath returns the credential file location. Honors ASTRA_CONFIG_DIR
// (same convention as the engine config) for tests and portable setups.
func CredentialPath() string {
	dir := os.Getenv("ASTRA_CONFIG_DIR")
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			dir = ".astra"
		}
		dir = filepath.Join(dir, "astra")
	}
	return filepath.Join(dir, "auth.json")
}

func LoadCredential() (*Credential, error) {
	data, err := os.ReadFile(CredentialPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var c Credential
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Token == "" {
		return nil, nil
	}
	return &c, nil
}

func SaveCredential(c *Credential) error {
	path := CredentialPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func ClearCredential() error {
	err := os.Remove(CredentialPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
