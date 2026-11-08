// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package camera

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Record and motion modes, matching the CHECK constraints in the schema.
const (
	RecordContinuous = "continuous"
	RecordOff        = "off"

	MotionOff       = "off"
	MotionONVIF     = "onvif"
	MotionFrameDiff = "framediff"
)

// Camera is one configured camera. MainURL and SubURL hold the plain URLs,
// with credentials: never send them to the frontend, use MaskURL.
type Camera struct {
	ID            int64
	Name          string
	MainURL       string
	SubURL        string // optional, "" when unset
	Enabled       bool
	RecordMode    string
	RecordAudio   bool
	SegmentSec    int
	RetentionDays *int // nil: storage.default_retention_days
	MotionMode    string
	MotionConfig  json.RawMessage // JSON object
	CreatedAt     time.Time
}

// New returns a camera with the schema defaults filled in.
func New(name, mainURL string) Camera {
	return Camera{
		Name:         name,
		MainURL:      mainURL,
		Enabled:      true,
		RecordMode:   RecordContinuous,
		SegmentSec:   600,
		MotionMode:   MotionOff,
		MotionConfig: json.RawMessage(`{}`),
	}
}

var (
	// ErrNotFound means no camera has the given id.
	ErrNotFound = errors.New("camera not found")
	// ErrNameTaken means another camera already uses the name (case-insensitive).
	ErrNameTaken = errors.New("camera name already in use")
	// ErrInvalid wraps every validation failure, including ErrInvalidURL.
	ErrInvalid = errors.New("invalid camera")
)

// Validate checks c before it is stored. Errors wrap ErrInvalid.
func (c Camera) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
	}
	name := strings.TrimSpace(c.Name)
	switch {
	case name == "" || utf8.RuneCountInString(name) > 64:
		return bad("name must be 1-64 characters")
	case name != c.Name:
		return bad("name has leading or trailing spaces")
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return bad("name contains control characters")
	case c.RecordMode != RecordContinuous && c.RecordMode != RecordOff:
		return bad("record_mode %q, want continuous or off", c.RecordMode)
	case c.MotionMode != MotionOff && c.MotionMode != MotionONVIF && c.MotionMode != MotionFrameDiff:
		return bad("motion_mode %q, want off, onvif or framediff", c.MotionMode)
	case c.SegmentSec < 10 || c.SegmentSec > 3600:
		return bad("segment_sec %d, want 10-3600", c.SegmentSec)
	case c.RetentionDays != nil && *c.RetentionDays < 1:
		return bad("retention_days %d, want at least 1", *c.RetentionDays)
	}
	var obj map[string]any
	if err := json.Unmarshal(c.MotionConfig, &obj); err != nil || obj == nil {
		return bad("motion_config must be a JSON object")
	}
	if err := ValidateURL(c.MainURL); err != nil {
		return fmt.Errorf("%w: main_url: %w", ErrInvalid, err)
	}
	if c.SubURL != "" {
		if err := ValidateURL(c.SubURL); err != nil {
			return fmt.Errorf("%w: sub_url: %w", ErrInvalid, err)
		}
	}
	return nil
}

// userinfo matches the credentials of any URL in free text. Greedy up to the
// last "@" before the path, so a password with a raw "@" is masked whole.
var userinfo = regexp.MustCompile(`(://)[^/\s"']*@`)

// MaskText hides URL credentials anywhere in s, e.g. an FFmpeg or go2rtc log line.
func MaskText(s string) string { return userinfo.ReplaceAllString(s, "${1}***@") }

// MaskURL hides the credentials in a camera URL for the API and logs.
func MaskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid URL)" // the raw value may contain a password
	}
	if u.User == nil {
		return u.String()
	}
	u.User = nil
	// Inserted by hand: url.User("***") would be encoded as %2A%2A%2A.
	return strings.Replace(u.String(), "://", "://***@", 1)
}

// Store reads and writes cameras. URLs are encrypted with the Sealer.
type Store struct {
	db   *sql.DB
	seal *Sealer
}

// NewStore returns a Store on an opened and migrated database.
func NewStore(db *sql.DB, seal *Sealer) *Store {
	return &Store{db: db, seal: seal}
}

const columns = `id, name, main_url, sub_url, enabled, record_mode, record_audio,
	segment_sec, retention_days, motion_mode, motion_config, created_at`

// Create stores c and returns it with ID and CreatedAt set.
func (s *Store) Create(ctx context.Context, c Camera) (Camera, error) {
	if err := c.Validate(); err != nil {
		return Camera{}, err
	}
	main, sub := s.sealURLs(c)
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO cameras (name, main_url, sub_url, enabled, record_mode, record_audio,
			segment_sec, retention_days, motion_mode, motion_config)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING `+columns,
		c.Name, main, sub, c.Enabled, c.RecordMode, c.RecordAudio,
		c.SegmentSec, c.RetentionDays, c.MotionMode, string(c.MotionConfig))
	out, err := s.scan(row)
	return out, mapErr(err)
}

// Update replaces every field of the camera with id c.ID.
func (s *Store) Update(ctx context.Context, c Camera) (Camera, error) {
	if err := c.Validate(); err != nil {
		return Camera{}, err
	}
	main, sub := s.sealURLs(c)
	row := s.db.QueryRowContext(ctx, `
		UPDATE cameras SET name = ?, main_url = ?, sub_url = ?, enabled = ?, record_mode = ?,
			record_audio = ?, segment_sec = ?, retention_days = ?, motion_mode = ?, motion_config = ?
		WHERE id = ?
		RETURNING `+columns,
		c.Name, main, sub, c.Enabled, c.RecordMode, c.RecordAudio,
		c.SegmentSec, c.RetentionDays, c.MotionMode, string(c.MotionConfig), c.ID)
	out, err := s.scan(row)
	return out, mapErr(err)
}

// Get returns the camera with the given id.
func (s *Store) Get(ctx context.Context, id int64) (Camera, error) {
	out, err := s.scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM cameras WHERE id = ?`, id))
	return out, mapErr(err)
}

// List returns every camera ordered by name.
func (s *Store) List(ctx context.Context) ([]Camera, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM cameras ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Err below reports iteration errors

	var out []Camera
	for rows.Next() {
		c, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete removes the camera and, through ON DELETE CASCADE, its segment and
// event rows. Recording files on disk are left to the retention job.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM cameras WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) sealURLs(c Camera) (main, sub []byte) {
	main = s.seal.Seal(c.MainURL, "main_url")
	if c.SubURL != "" {
		sub = s.seal.Seal(c.SubURL, "sub_url")
	}
	return main, sub
}

type scanner interface{ Scan(dest ...any) error }

func (s *Store) scan(r scanner) (Camera, error) {
	var (
		c         Camera
		main, sub []byte
		retention sql.NullInt64
		motionCfg string
		created   int64
	)
	if err := r.Scan(&c.ID, &c.Name, &main, &sub, &c.Enabled, &c.RecordMode, &c.RecordAudio,
		&c.SegmentSec, &retention, &c.MotionMode, &motionCfg, &created); err != nil {
		return Camera{}, err
	}
	var err error
	if c.MainURL, err = s.seal.Open(main, "main_url"); err != nil {
		return Camera{}, fmt.Errorf("camera %d main_url: %w", c.ID, err)
	}
	if sub != nil {
		if c.SubURL, err = s.seal.Open(sub, "sub_url"); err != nil {
			return Camera{}, fmt.Errorf("camera %d sub_url: %w", c.ID, err)
		}
	}
	if retention.Valid {
		d := int(retention.Int64)
		c.RetentionDays = &d
	}
	c.MotionConfig = json.RawMessage(motionCfg)
	c.CreatedAt = time.Unix(created, 0).UTC()
	return c, nil
}

func mapErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return ErrNameTaken // name is the only UNIQUE column besides the id
	}
	return err
}
