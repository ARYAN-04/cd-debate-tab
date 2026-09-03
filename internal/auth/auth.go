// Package auth holds password hashing (bcrypt) and session token helpers.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the bcrypt work factor for password hashes (PLAN §2).
const BcryptCost = 12

// SessionTTL is how long a login session stays valid.
const SessionTTL = 24 * time.Hour

// HashPassword hashes password with bcrypt cost 12.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword reports whether password matches the bcrypt hash.
func CheckPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// GenerateToken returns a crypto/rand 32-byte hex token (PLAN §2).
func GenerateToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// NewSessionToken is an alias for GenerateToken.
func NewSessionToken() (string, error) {
	return GenerateToken()
}

// Expiry returns the session expiry instant TTL from now.
func Expiry(now time.Time) time.Time {
	return now.Add(SessionTTL)
}

// Expired reports whether expiresAt is at or before now.
func Expired(expiresAt, now time.Time) bool {
	return !expiresAt.After(now)
}
