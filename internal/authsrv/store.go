package authsrv

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// User is a verified account. DisplayName is optional and editable from
// the /account page (server endpoint POST /api/auth/account).
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"password_hash"`
	DisplayName  string    `json:"display_name,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	VerifiedAt   time.Time `json:"verified_at,omitempty"`
}

// PendingRegistration mirrors Socrates' pending_registrations: the email +
// password hash are stored first, the user is only created when the
// verification link is clicked.
type PendingRegistration struct {
	Email        string    `json:"email"`
	PasswordHash string    `json:"password_hash"`
	Token        string    `json:"token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Session is a website browser session (sid cookie).
type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// APIToken is an opaque bearer credential issued to CLI/TUI clients.
type APIToken struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// DeviceGrant is one device-authorization flow (RFC 8628 style).
type DeviceGrant struct {
	DeviceCode string    `json:"device_code"`
	UserCode   string    `json:"user_code"`
	Status     string    `json:"status"` // pending | approved | expired
	UserID     string    `json:"user_id,omitempty"`
	Token      string    `json:"token,omitempty"` // issued API token once approved
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	ApprovedAt time.Time `json:"approved_at,omitempty"`
}

// Store persists all auth state as a single JSON document, written
// atomically (tmp + rename) on every mutation. File storage matches the
// project's zero-dependency posture (no DB), and the schema is small enough
// that a full rewrite per change is cheap.
type Store struct {
	mu       sync.Mutex
	path     string
	Users    []User                `json:"users"`
	Pending  []PendingRegistration `json:"pending"`
	Sessions []Session             `json:"sessions"`
	Tokens   []APIToken            `json:"tokens"`
	Devices  []DeviceGrant         `json:"devices"`
}

// OpenStore loads (or creates) the store at path.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	return s, nil
}

// Close is a no-op kept for symmetry with the engine's lifecycle.
func (s *Store) Close() error { return nil }

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

// NormalizeEmail lowercases and trims, per Socrates.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// --- users ---

func (s *Store) FindUserByEmail(email string) *User {
	email = NormalizeEmail(email)
	for i := range s.Users {
		if s.Users[i].Email == email {
			return &s.Users[i]
		}
	}
	return nil
}

func (s *Store) FindUserByID(id string) *User {
	for i := range s.Users {
		if s.Users[i].ID == id {
			return &s.Users[i]
		}
	}
	return nil
}

func (s *Store) CreateUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Users = append(s.Users, *u)
	return s.saveLocked()
}

// UpdateUser applies a mutator to the user with the given ID under the
// store lock. The mutator may modify any persisted field. Returns
// ErrUserNotFound if no such user exists.
func (s *Store) UpdateUser(id string, mutate func(*User)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Users {
		if s.Users[i].ID == id {
			mutate(&s.Users[i])
			return s.saveLocked()
		}
	}
	return ErrUserNotFound
}

// ErrUserNotFound is returned by UpdateUser when no user matches the id.
var ErrUserNotFound = errors.New("user not found")

// --- pending registrations ---

func (s *Store) FindPending(email string) *PendingRegistration {
	email = NormalizeEmail(email)
	for i := range s.Pending {
		if s.Pending[i].Email == email {
			return &s.Pending[i]
		}
	}
	return nil
}

func (s *Store) FindPendingByToken(token string) *PendingRegistration {
	for i := range s.Pending {
		if s.Pending[i].Token == token {
			return &s.Pending[i]
		}
	}
	return nil
}

func (s *Store) UpsertPending(p *PendingRegistration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	email := NormalizeEmail(p.Email)
	for i := range s.Pending {
		if s.Pending[i].Email == email {
			s.Pending[i] = *p
			return s.saveLocked()
		}
	}
	s.Pending = append(s.Pending, *p)
	return s.saveLocked()
}

func (s *Store) DeletePending(email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	email = NormalizeEmail(email)
	out := s.Pending[:0]
	for _, p := range s.Pending {
		if p.Email != email {
			out = append(out, p)
		}
	}
	s.Pending = out
	return s.saveLocked()
}

// --- sessions ---

func (s *Store) CreateSession(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Sessions = append(s.Sessions, *sess)
	return s.saveLocked()
}

// FindSession returns the session if present and unexpired.
func (s *Store) FindSession(token string) *Session {
	for i := range s.Sessions {
		if s.Sessions[i].Token == token {
			if time.Now().Before(s.Sessions[i].ExpiresAt) {
				return &s.Sessions[i]
			}
			return nil
		}
	}
	return nil
}

func (s *Store) DeleteSession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Sessions[:0]
	for _, sess := range s.Sessions {
		if sess.Token != token {
			out = append(out, sess)
		}
	}
	s.Sessions = out
	return s.saveLocked()
}

// --- api tokens ---

func (s *Store) FindToken(token string) *APIToken {
	for i := range s.Tokens {
		if s.Tokens[i].Token == token {
			if time.Now().Before(s.Tokens[i].ExpiresAt) {
				return &s.Tokens[i]
			}
			return nil
		}
	}
	return nil
}

func (s *Store) CreateToken(t *APIToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Tokens = append(s.Tokens, *t)
	return s.saveLocked()
}

func (s *Store) TokensForUser(userID string) []APIToken {
	var out []APIToken
	for _, t := range s.Tokens {
		if t.UserID == userID && time.Now().Before(t.ExpiresAt) {
			out = append(out, t)
		}
	}
	return out
}

func (s *Store) RevokeToken(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Tokens[:0]
	for _, t := range s.Tokens {
		if t.Token != token {
			out = append(out, t)
		}
	}
	s.Tokens = out
	return s.saveLocked()
}

// --- device grants ---

func (s *Store) CreateDevice(g *DeviceGrant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Devices = append(s.Devices, *g)
	return s.saveLocked()
}

// FindDeviceByCode looks up by device code (TUI polling).
func (s *Store) FindDeviceByCode(deviceCode string) *DeviceGrant {
	for i := range s.Devices {
		if s.Devices[i].DeviceCode == deviceCode {
			return &s.Devices[i]
		}
	}
	return nil
}

// FindDeviceByUserCode looks up by the human-readable code (website approve).
func (s *Store) FindDeviceByUserCode(userCode string) *DeviceGrant {
	userCode = strings.ToUpper(strings.TrimSpace(userCode))
	for i := range s.Devices {
		if s.Devices[i].UserCode == userCode {
			return &s.Devices[i]
		}
	}
	return nil
}

func (s *Store) UpdateDevice(deviceCode string, mutate func(*DeviceGrant)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Devices {
		if s.Devices[i].DeviceCode == deviceCode {
			mutate(&s.Devices[i])
			return s.saveLocked()
		}
	}
	return nil
}
