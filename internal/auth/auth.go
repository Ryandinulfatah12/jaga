// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package auth stores users and login sessions. Passwords are hashed with
// argon2id; session tokens are random and only their SHA-256 is stored.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Roles.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// User is an account without its password hash.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

var (
	// ErrBadLogin is returned for an unknown user or a wrong password alike,
	// so a caller cannot tell which usernames exist.
	ErrBadLogin = errors.New("wrong username or password")
	// ErrNoSession means the token is unknown or expired.
	ErrNoSession = errors.New("no valid session")
	// ErrInvalid wraps every rejected username, password or role.
	ErrInvalid = errors.New("invalid user")
	// ErrUserTaken means the username is already in use (case-insensitive).
	ErrUserTaken = errors.New("username already in use")
)

// SessionTTL is how long a session lives after login.
const SessionTTL = 30 * 24 * time.Hour

// argon2id parameters: RFC 9106 second recommendation (64 MiB, 3 passes).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
)

// Store reads and writes users and sessions.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore returns a Store on an opened and migrated database.
func NewStore(db *sql.DB) *Store { return &Store{db: db, now: time.Now} }

// HashPassword returns an argon2id hash in the PHC string format.
func HashPassword(password string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt) // never fails (crypto/rand)
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64(salt), b64(key))
}

// CheckPassword reports whether password matches a HashPassword result.
// Parameters are read from the hash, so old hashes keep working when the
// defaults change.
func CheckPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 || m > 1<<20 || t > 16 {
		return false // the bounds stop a corrupt hash from costing gigabytes
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want))) //nolint:gosec // len(want) <= 64
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked for unknown users so a login takes as long whether or
// not the username exists.
var dummyHash = HashPassword("jaga-dummy-password")

// ValidateUser checks a new username, password and role. Errors wrap ErrInvalid.
func ValidateUser(username, password, role string) error {
	bad := func(msg string) error { return fmt.Errorf("%w: %s", ErrInvalid, msg) }
	switch {
	case username == "" || utf8.RuneCountInString(username) > 64:
		return bad("username must be 1-64 characters")
	case strings.IndexFunc(username, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return bad("username must not contain spaces or control characters")
	case role != RoleAdmin && role != RoleViewer:
		return bad("role must be admin or viewer")
	}
	return ValidatePassword(password)
}

// ValidatePassword enforces 8-128 characters. The upper bound keeps argon2
// input small; no composition rules (NIST SP 800-63B).
func ValidatePassword(password string) error {
	if n := utf8.RuneCountInString(password); n < 8 || n > 128 {
		return fmt.Errorf("%w: password must be 8-128 characters", ErrInvalid)
	}
	return nil
}

// CreateUser adds a user.
func (s *Store) CreateUser(ctx context.Context, username, password, role string) (User, error) {
	if err := ValidateUser(username, password, role); err != nil {
		return User{}, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`,
		username, HashPassword(password), role)
	if err != nil {
		var se *sqlite.Error
		if errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return User{}, ErrUserTaken
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, Role: role}, nil
}

// SetPassword replaces a user's password and ends all their sessions.
func (s *Store) SetPassword(ctx context.Context, username, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `UPDATE users SET password_hash = ? WHERE username = ? RETURNING id`,
		HashPassword(password), username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("user %q not found", username)
	}
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// HasAdmin reports whether at least one admin exists (jaga start refuses to
// run without one).
func (s *Store) HasAdmin(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&n)
	return n > 0, err
}

// Login checks the password and opens a session. The returned token goes
// into the cookie; only its hash is stored.
func (s *Store) Login(ctx context.Context, username, password string) (User, string, error) {
	var u User
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, role, password_hash FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.Role, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		CheckPassword(dummyHash, password)
		return User{}, "", ErrBadLogin
	}
	if err != nil {
		return User{}, "", fmt.Errorf("login: %w", err)
	}
	if !CheckPassword(hash, password) {
		return User{}, "", ErrBadLogin
	}
	token := rand.Text() // 26 base32 chars, 130 bits
	now := s.now()
	// Expired sessions are swept on every login: cheap, and no extra job.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return User{}, "", err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash(token), u.ID, now.Add(SessionTTL).Unix()); err != nil {
		return User{}, "", fmt.Errorf("login: %w", err)
	}
	return u, token, nil
}

// Session returns the user behind a session token.
func (s *Store) Session(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNoSession
	}
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.role FROM sessions s
		JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND s.expires_at > ?`,
		tokenHash(token), s.now().Unix()).Scan(&u.ID, &u.Username, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	return u, err
}

// Logout ends one session. Unknown tokens are not an error.
func (s *Store) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash(token))
	return err
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
