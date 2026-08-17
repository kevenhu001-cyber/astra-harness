package authsrv

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed site
var siteFS embed.FS

// Default constants for the device flow (RFC 8628-style).
const (
	deviceExpiry   = 10 * time.Minute
	deviceInterval = 5 // seconds between TUI polls
	tokenTTL       = 30 * 24 * time.Hour
	sessionTTL     = 30 * 24 * time.Hour
)

// Options configure the server.
type Options struct {
	// BaseURL is the public base URL used to build verification links
	// (e.g. http://localhost:8080). Defaults to the request host when empty.
	BaseURL string
	// CookieSecure sets the Secure flag on session cookies (behind TLS).
	CookieSecure bool
	// SessionTTL overrides the default 30-day session lifetime.
	SessionTTL time.Duration
	// CookiePath scopes the session cookie to a URL path. It defaults to "/"
	// (sent on every path of the host). To keep the auth session cookie from
	// being sent to an unrelated path on the same host — e.g. the installer
	// served at https://astracode.topodrive.top/ — scope it to a path that is
	// NOT a prefix of that location (a cookie Path can only narrow, never
	// exclude, so the auth site and /astracode/ must live under distinct,
	// non-overlapping paths or different subdomains).
	CookiePath string
}

// Server is the auth HTTP server.
type Server struct {
	store   *Store
	mailer  Mailer
	baseURL string
	opts    Options
}

// New builds a Server. If mailer is nil, ConsoleMailer is used.
func New(store *Store, mailer Mailer, opts Options) *Server {
	if mailer == nil {
		mailer = ConsoleMailer{}
	}
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = sessionTTL
	}
	if opts.CookiePath == "" {
		opts.CookiePath = "/"
	}
	return &Server{store: store, mailer: mailer, opts: opts}
}

// Handler returns the full HTTP handler (static site + API).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, err := fs.Sub(siteFS, "site")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/assets/", fileServer)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Only serve known static pages; everything else 404s cleanly.
		switch r.URL.Path {
		case "/", "/index.html":
			if r.URL.Path == "/index.html" {
				http.Redirect(w, r, "/", http.StatusMovedPermanently)
				return
			}
			http.ServeFileFS(w, r, sub, "index.html")
		case "/login":
			http.ServeFileFS(w, r, sub, "login.html")
		case "/authorize":
			http.ServeFileFS(w, r, sub, "authorize.html")
		case "/account":
			http.ServeFileFS(w, r, sub, "account.html")
		case "/favicon.svg":
			http.ServeFileFS(w, r, sub, "favicon.svg")
		case "/login.html":
			http.Redirect(w, r, "/login", http.StatusMovedPermanently)
		case "/authorize.html":
			http.Redirect(w, r, "/authorize", http.StatusMovedPermanently)
		case "/account.html":
			http.Redirect(w, r, "/account", http.StatusMovedPermanently)
		default:
			fileServer.ServeHTTP(w, r)
		}
	})
	mux.HandleFunc("/api/auth/register", s.handleRegister)
	mux.HandleFunc("/api/auth/resend-verification", s.handleResendVerification)
	mux.HandleFunc("/api/auth/verify", s.handleVerify)
	mux.HandleFunc("/api/auth/login", s.handleLogin)
	mux.HandleFunc("/api/auth/logout", s.handleLogout)
	mux.HandleFunc("/api/auth/me", s.handleMe)
	mux.HandleFunc("/api/auth/device", s.handleDeviceCreate)
	mux.HandleFunc("/api/auth/device/approve", s.handleDeviceApprove)
	mux.HandleFunc("/api/auth/device/token", s.handleDeviceToken)
	mux.HandleFunc("/api/auth/tokens", s.handleTokens)
	mux.HandleFunc("/api/auth/account", s.handleAccountUpdate)
	return mux
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

// originAllowed rejects cross-site POSTs (CSRF defense in depth alongside the
// SameSite=Lax session cookie).
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser clients (curl, TUI) carry no Origin
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := r.Host
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i:], "]") {
		host = host[:i] // strip port for host-only comparison
	}
	return u.Hostname() == host || u.Host == r.Host
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return false
	}
	if !originAllowed(r) {
		writeErr(w, http.StatusForbidden, "cross-site request rejected")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// sessionCookie reads the sid cookie and returns the user.
func (s *Server) sessionUser(r *http.Request) *User {
	c, err := r.Cookie("sid")
	if err != nil {
		return nil
	}
	sess := s.store.FindSession(c.Value)
	if sess == nil {
		return nil
	}
	return s.store.FindUserByID(sess.UserID)
}

// bearerUser authenticates via Authorization: Bearer <api token>.
func (s *Server) bearerUser(r *http.Request) *User {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil
	}
	tok := s.store.FindToken(strings.TrimPrefix(h, "Bearer "))
	if tok == nil {
		return nil
	}
	return s.store.FindUserByID(tok.UserID)
}

func (s *Server) currentUser(r *http.Request) *User {
	if u := s.sessionUser(r); u != nil {
		return u
	}
	return s.bearerUser(r)
}

// publicUser is the client-safe user shape (never exposes the password hash).
type publicUser struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	CreatedAt   string `json:"created_at"`
}

func toPublic(u *User) publicUser {
	return publicUser{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, CreatedAt: u.CreatedAt.Format(time.RFC3339)}
}

func (s *Server) verifyURL(token string) string {
	base := strings.TrimSuffix(s.opts.BaseURL, "/")
	if base == "" {
		base = "http://localhost:8080"
	}
	return base + "/api/auth/verify?token=" + url.QueryEscape(token)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, userID string) error {
	token := randomHex(24)
	err := s.store.CreateSession(&Session{Token: token, UserID: userID, ExpiresAt: time.Now().Add(s.opts.SessionTTL)})
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "sid",
		Value:    token,
		Path:     s.opts.CookiePath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.opts.CookieSecure,
		MaxAge:   int(s.opts.SessionTTL.Seconds()),
	})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: "", Path: s.opts.CookiePath, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

// --- registration (Socrates mechanism) ---

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := ValidateCredentials(body.Email, body.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	email := NormalizeEmail(body.Email)

	// Anti-enumeration: always return ok to the caller.
	if s.store.FindUserByEmail(email) != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	hash, err := hashPassword(body.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "password hashing failed")
		return
	}
	token := randomHex(16)
	now := time.Now()
	if err := s.store.UpsertPending(&PendingRegistration{
		Email: email, PasswordHash: hash, Token: token, ExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage failed")
		return
	}
	if err := s.mailer.SendVerification(email, s.verifyURL(token)); err != nil {
		log.Printf("[authsrv] verification email failed for %s: %v", email, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	email := NormalizeEmail(body.Email)
	if p := s.store.FindPending(email); p != nil && time.Now().Before(p.ExpiresAt) {
		_ = s.mailer.SendVerification(email, s.verifyURL(p.Token))
	}
	// Always ok, like Socrates' resend endpoint.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	p := s.store.FindPendingByToken(token)
	if p == nil || time.Now().After(p.ExpiresAt) {
		http.Error(w, "Verification link is invalid or expired. Please register again.", http.StatusBadRequest)
		return
	}
	// Register is idempotent: the user may have verified in another tab.
	user := s.store.FindUserByEmail(p.Email)
	if user == nil {
		user = &User{
			ID: randomHex(8), Email: p.Email, PasswordHash: p.PasswordHash,
			CreatedAt: time.Now(), VerifiedAt: time.Now(),
		}
		if err := s.store.CreateUser(user); err != nil {
			writeErr(w, http.StatusInternalServerError, "storage failed")
			return
		}
	}
	_ = s.store.DeletePending(p.Email)
	if err := s.setSessionCookie(w, user.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "session failed")
		return
	}
	http.Redirect(w, r, "/account", http.StatusFound)
}

// --- login / logout / me ---

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	user := s.store.FindUserByEmail(NormalizeEmail(body.Email))
	if user == nil || !checkPassword(user.PasswordHash, body.Password) {
		writeErr(w, http.StatusUnauthorized, ErrInvalidCredentials.Error())
		return
	}
	if err := s.setSessionCookie(w, user.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "session failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toPublic(user)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("sid"); err == nil {
		_ = s.store.DeleteSession(c.Value)
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		// 200 + user:null instead of 401 so the login/account pages do not
		// produce scary console errors for the "already signed in?" check.
		writeJSON(w, http.StatusOK, map[string]any{"user": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toPublic(user)})
}

// --- device flow (TUI login) ---

func (s *Server) handleDeviceCreate(w http.ResponseWriter, r *http.Request) {
	g := &DeviceGrant{
		DeviceCode: randomHex(16),
		UserCode:   randomUserCode(),
		Status:     "pending",
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(deviceExpiry),
	}
	if err := s.store.CreateDevice(g); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage failed")
		return
	}
	base := strings.TrimSuffix(s.opts.BaseURL, "/")
	if base == "" {
		base = "http://localhost:8080"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":      g.DeviceCode,
		"user_code":        g.UserCode,
		"verification_uri": base + "/authorize?code=" + url.QueryEscape(g.UserCode),
		"expires_in":       int(deviceExpiry.Seconds()),
		"interval":         deviceInterval,
	})
}

func (s *Server) handleDeviceApprove(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "please log in first")
		return
	}
	var body struct {
		UserCode string `json:"user_code"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	g := s.store.FindDeviceByUserCode(body.UserCode)
	if g == nil || g.Status != "pending" || time.Now().After(g.ExpiresAt) {
		writeErr(w, http.StatusBadRequest, "invalid or expired code")
		return
	}
	token := &APIToken{
		Token: randomHex(24), UserID: user.ID, Label: "device",
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(tokenTTL),
	}
	if err := s.store.CreateToken(token); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage failed")
		return
	}
	now := time.Now()
	err := s.store.UpdateDevice(g.DeviceCode, func(d *DeviceGrant) {
		d.Status = "approved"
		d.UserID = user.ID
		d.Token = token.Token
		d.ApprovedAt = now
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	g := s.store.FindDeviceByCode(body.DeviceCode)
	if g == nil || time.Now().After(g.ExpiresAt) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "expired"})
		return
	}
	switch g.Status {
	case "pending":
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
	case "approved":
		user := s.store.FindUserByID(g.UserID)
		if user == nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "expired"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":       "approved",
			"access_token": g.Token,
			"user":         toPublic(user),
		})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"status": "expired"})
	}
}

// --- api tokens (account page) ---

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	switch r.Method {
	case http.MethodPost:
		tok := &APIToken{
			Token: randomHex(24), UserID: user.ID, Label: "manual",
			CreatedAt: time.Now(), ExpiresAt: time.Now().Add(tokenTTL),
		}
		if err := s.store.CreateToken(tok); err != nil {
			writeErr(w, http.StatusInternalServerError, "storage failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": tok.Token})
	case http.MethodGet:
		toks := s.store.TokensForUser(user.ID)
		out := make([]map[string]any, 0, len(toks))
		for _, t := range toks {
			out = append(out, map[string]any{
				"token": t.Token, "label": t.Label,
				"created_at": t.CreatedAt.Format(time.RFC3339),
				"expires_at": t.ExpiresAt.Format(time.RFC3339),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
	case http.MethodDelete:
		var body struct {
			Token string `json:"token"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		_ = s.store.RevokeToken(body.Token)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAccountUpdate lets the signed-in user update mutable profile
// fields. Currently only display_name is editable; password reset lives
// outside this handler.
func (s *Server) handleAccountUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	user := s.currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.DisplayName)
	if len(name) > 60 {
		name = name[:60]
	}
	if err := s.store.UpdateUser(user.ID, func(u *User) {
		u.DisplayName = name
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	user.DisplayName = name
	writeJSON(w, http.StatusOK, map[string]any{"user": toPublic(user)})
}
