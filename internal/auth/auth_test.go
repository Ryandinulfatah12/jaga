// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/db"
	"github.com/ryandinulfatah12/jaga/migrations"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Open(ctx, filepath.Join(t.TempDir(), "jaga.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	return NewStore(conn)
}

func TestPasswordHash(t *testing.T) {
	h := HashPassword("correct horse")
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("hash = %q", h)
	}
	if !CheckPassword(h, "correct horse") || CheckPassword(h, "correct horsE") {
		t.Error("check mismatch")
	}
	if HashPassword("correct horse") == h {
		t.Error("same salt twice")
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=65536,t=3,p=4$AAAA$AAAA",
		"$argon2id$v=19$m=99999999,t=3,p=4$AAAA$AAAA", "$argon2id$v=19$m=65536,t=3,p=4$!!$AAAA"} {
		if CheckPassword(bad, "x") {
			t.Errorf("accepted hash %q", bad)
		}
	}
}

func TestValidateUser(t *testing.T) {
	for _, c := range []struct{ user, pass, role string }{
		{"", "password1", RoleAdmin},
		{"has space", "password1", RoleAdmin},
		{"tab\t", "password1", RoleAdmin},
		{strings.Repeat("a", 65), "password1", RoleAdmin},
		{"ryan", "short", RoleAdmin},
		{"ryan", strings.Repeat("p", 129), RoleAdmin},
		{"ryan", "password1", "root"},
	} {
		if err := ValidateUser(c.user, c.pass, c.role); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidateUser(%q, %q, %q) = %v", c.user, c.pass, c.role, err)
		}
	}
	if err := ValidateUser("ryan", "password1", RoleViewer); err != nil {
		t.Error(err)
	}
}

func TestLoginSessionLogout(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if ok, _ := s.HasAdmin(ctx); ok {
		t.Fatal("fresh db has an admin")
	}
	if _, err := s.CreateUser(ctx, "Ryan", "password1", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "ryan", "password2", RoleViewer); !errors.Is(err, ErrUserTaken) {
		t.Errorf("duplicate (case-insensitive) = %v", err)
	}
	if ok, _ := s.HasAdmin(ctx); !ok {
		t.Error("HasAdmin false after creating admin")
	}

	for _, c := range [][2]string{{"ryan", "wrong-pass"}, {"nobody", "password1"}} {
		if _, _, err := s.Login(ctx, c[0], c[1]); !errors.Is(err, ErrBadLogin) {
			t.Errorf("Login(%q) = %v, want ErrBadLogin", c[0], err)
		}
	}
	u, tok, err := s.Login(ctx, "RYAN", "password1") // username is case-insensitive
	if err != nil || u.Role != RoleAdmin || len(tok) < 20 {
		t.Fatalf("login = %+v %q %v", u, tok, err)
	}
	if got, err := s.Session(ctx, tok); err != nil || got.ID != u.ID {
		t.Fatalf("session = %+v %v", got, err)
	}
	// The token itself is never stored.
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE token_hash = ?`, tok).Scan(&n)
	if n != 0 {
		t.Error("plain token stored")
	}
	if _, err := s.Session(ctx, tok+"x"); !errors.Is(err, ErrNoSession) {
		t.Errorf("wrong token = %v", err)
	}
	if err := s.Logout(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Errorf("after logout = %v", err)
	}
}

func TestSessionExpiresAndPasswordResetEndsSessions(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	if _, err := s.CreateUser(ctx, "ryan", "password1", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	_, old, _ := s.Login(ctx, "ryan", "password1")
	now = now.Add(SessionTTL)
	if _, err := s.Session(ctx, old); !errors.Is(err, ErrNoSession) {
		t.Errorf("expired session = %v", err)
	}
	_, tok, _ := s.Login(ctx, "ryan", "password1") // also sweeps the expired row
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&n)
	if n != 1 {
		t.Errorf("%d session rows, want 1 (expired swept)", n)
	}
	if err := s.SetPassword(ctx, "ryan", "password2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Errorf("session survived password reset: %v", err)
	}
	if _, _, err := s.Login(ctx, "ryan", "password2"); err != nil {
		t.Errorf("new password: %v", err)
	}
	if err := s.SetPassword(ctx, "nobody", "password2"); err == nil {
		t.Error("reset of unknown user succeeded")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(5, time.Minute)
	l.now = func() time.Time { return now }
	for i := range 5 {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("attempt %d blocked", i+1)
		}
		now = now.Add(time.Second)
	}
	ok, wait := l.Allow("1.2.3.4")
	if ok || wait != 55*time.Second {
		t.Errorf("6th attempt: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Error("other IP blocked")
	}
	now = now.Add(55 * time.Second)
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Error("still blocked after the window")
	}
}
