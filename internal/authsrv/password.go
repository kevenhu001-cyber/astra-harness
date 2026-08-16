package authsrv

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Validation rules mirror Socrates (server/src/services/auth.ts): a strict
// 8-64 character bound. bcrypt only reads the first 72 bytes, so the upper
// bound exists to avoid two different long passwords sharing a prefix
// (P_password-bcrypt-truncation).
const (
	MinPasswordLength = 8
	MaxPasswordLength = 64
)

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

var (
	ErrInvalidEmail       = errors.New("invalid email format")
	ErrPasswordTooShort   = errors.New("password must be at least 8 characters")
	ErrPasswordTooLong    = errors.New("password is too long (maximum 64 characters)")
	ErrInvalidCredentials = errors.New("invalid email or password")
)

// ValidateCredentials applies the Socrates registration gates.
func ValidateCredentials(email, password string) error {
	if email == "" || password == "" {
		return errors.New("email and password are required")
	}
	if !emailRe.MatchString(NormalizeEmail(email)) {
		return ErrInvalidEmail
	}
	if len(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if len(password) > MaxPasswordLength {
		return ErrPasswordTooLong
	}
	return nil
}

func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// randomHex returns n random bytes hex-encoded.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b)
}

// randomUserCode generates a human-friendly device code like "K7Q2-XM9D".
func randomUserCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no I/O/1/0 ambiguity
	b := make([]byte, 8)
	randBytes := make([]byte, 8)
	if _, err := rand.Read(randBytes); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(randBytes[i])%len(alphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:])
}

// uppercaseCode strips separators and uppercases for lenient matching.
func uppercaseCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
}
