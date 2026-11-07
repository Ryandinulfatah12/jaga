// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package camera

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryandinulfatah12/jaga/internal/db"
	"github.com/ryandinulfatah12/jaga/migrations"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func newStore(t *testing.T, secret string) (*Store, func() []byte) {
	t.Helper()
	ctx := t.Context()
	conn, err := db.Open(ctx, filepath.Join(t.TempDir(), "jaga.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.Migrate(ctx, conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	seal, err := NewSealer(secret)
	if err != nil {
		t.Fatal(err)
	}
	// rawMain returns the stored main_url bytes of camera 1, to check encryption.
	rawMain := func() []byte {
		var b []byte
		if err := conn.QueryRowContext(ctx, `SELECT main_url FROM cameras WHERE id = 1`).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	return NewStore(conn, seal), rawMain
}

func TestSealer(t *testing.T) {
	if _, err := NewSealer("short"); err == nil {
		t.Fatal("short secret accepted")
	}
	s, _ := NewSealer(testSecret)
	a, b := s.Seal("rtsp://u:p@cam/s", "main_url"), s.Seal("rtsp://u:p@cam/s", "main_url")
	if bytes.Equal(a, b) {
		t.Error("same plaintext sealed twice gives the same ciphertext (nonce reuse)")
	}
	if got, err := s.Open(a, "main_url"); err != nil || got != "rtsp://u:p@cam/s" {
		t.Errorf("Open = %q, %v", got, err)
	}
	other, _ := NewSealer(strings.Repeat("x", MinSecretLen))
	tampered := append([]byte(nil), a...)
	tampered[len(tampered)-1] ^= 1
	for name, open := range map[string]func() (string, error){
		"wrong column": func() (string, error) { return s.Open(a, "sub_url") },
		"wrong secret": func() (string, error) { return other.Open(a, "main_url") },
		"tampered":     func() (string, error) { return s.Open(tampered, "main_url") },
		"truncated":    func() (string, error) { return s.Open(a[:10], "main_url") },
		"empty":        func() (string, error) { return s.Open(nil, "main_url") },
	} {
		if _, err := open(); !errors.Is(err, ErrSealed) {
			t.Errorf("%s: err = %v, want ErrSealed", name, err)
		}
	}
}

func TestStoreCRUD(t *testing.T) {
	ctx := t.Context()
	st, rawMain := newStore(t, testSecret)

	c := New("Depan", "rtsp://admin:hunter2@192.168.0.60:554/stream1")
	c.SubURL = "rtsp://admin:hunter2@192.168.0.60:554/stream2"
	days := 14
	c.RetentionDays = &days
	created, err := st.Create(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.CreatedAt.IsZero() || created.MainURL != c.MainURL || *created.RetentionDays != 14 {
		t.Fatalf("created = %+v", created)
	}
	if bytes.Contains(rawMain(), []byte("hunter2")) || bytes.Contains(rawMain(), []byte("rtsp")) {
		t.Fatal("main_url is stored in plain text")
	}

	got, err := st.Get(ctx, created.ID)
	if err != nil || got.SubURL != c.SubURL || got.RecordMode != RecordContinuous {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	got.Name, got.SubURL, got.RetentionDays, got.Enabled = "Garasi", "", nil, false
	got.MotionMode, got.MotionConfig = MotionFrameDiff, json.RawMessage(`{"threshold":0.1}`)
	updated, err := st.Update(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Garasi" || updated.SubURL != "" || updated.RetentionDays != nil || updated.Enabled ||
		updated.MotionMode != MotionFrameDiff || updated.CreatedAt != created.CreatedAt {
		t.Errorf("updated = %+v", updated)
	}

	if _, err := st.Create(ctx, New("garasi", "rtsp://cam/x")); !errors.Is(err, ErrNameTaken) {
		t.Errorf("duplicate name (case-insensitive): err = %v", err)
	}
	if _, err := st.Create(ctx, New("Belakang", "rtsp://cam/b")); err != nil {
		t.Fatal(err)
	}
	list, err := st.List(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "Belakang" {
		t.Fatalf("List = %+v, %v", list, err)
	}

	if err := st.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: %v", err)
	}
	if err := st.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete: %v", err)
	}
	ghost := New("Hantu", "rtsp://cam/h")
	ghost.ID = 999
	if _, err := st.Update(ctx, ghost); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update missing: %v", err)
	}
}

func TestStoreValidates(t *testing.T) {
	ctx := t.Context()
	st, _ := newStore(t, testSecret)
	zero := 0
	for name, mutate := range map[string]func(*Camera){
		"empty name":        func(c *Camera) { c.Name = "" },
		"padded name":       func(c *Camera) { c.Name = " depan" },
		"long name":         func(c *Camera) { c.Name = strings.Repeat("a", 65) },
		"control in name":   func(c *Camera) { c.Name = "dep\nan" },
		"bad main url":      func(c *Camera) { c.MainURL = "file:///etc/passwd" },
		"bad sub url":       func(c *Camera) { c.SubURL = "-i x" },
		"record mode":       func(c *Camera) { c.RecordMode = "motion" },
		"motion mode":       func(c *Camera) { c.MotionMode = "ai" },
		"segment too short": func(c *Camera) { c.SegmentSec = 5 },
		"retention zero":    func(c *Camera) { c.RetentionDays = &zero },
		"config not object": func(c *Camera) { c.MotionConfig = json.RawMessage(`[1]`) },
		"config not json":   func(c *Camera) { c.MotionConfig = json.RawMessage(`{`) },
	} {
		t.Run(name, func(t *testing.T) {
			c := New("Depan", "rtsp://cam/s")
			mutate(&c)
			if _, err := st.Create(ctx, c); !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
	if _, err := st.Create(ctx, New("Depan", "file:///etc/passwd")); !errors.Is(err, ErrInvalidURL) {
		t.Errorf("URL error should also match ErrInvalidURL: %v", err)
	}
}

func TestStoreWrongSecret(t *testing.T) {
	ctx := t.Context()
	st, _ := newStore(t, testSecret)
	c, err := st.Create(ctx, New("Depan", "rtsp://cam/s"))
	if err != nil {
		t.Fatal(err)
	}
	other, _ := NewSealer(strings.Repeat("y", MinSecretLen))
	st.seal = other
	if _, err := st.Get(ctx, c.ID); !errors.Is(err, ErrSealed) {
		t.Errorf("Get with another secret_key: %v, want ErrSealed", err)
	}
}

func TestMaskURL(t *testing.T) {
	for in, want := range map[string]string{
		"rtsp://admin:hunter2@cam:554/s?x=1": "rtsp://***@cam:554/s?x=1",
		"rtsps://admin@cam/s":                "rtsps://***@cam/s",
		"rtsp://cam/s":                       "rtsp://cam/s",
		"rtsp://admin:hunter2@cam/%zz":       "(invalid URL)",
	} {
		if got := MaskURL(in); got != want || strings.Contains(got, "hunter2") {
			t.Errorf("MaskURL(%q) = %q, want %q", in, got, want)
		}
	}
}
