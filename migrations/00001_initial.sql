-- SPDX-License-Identifier: Apache-2.0
-- Copyright The Jaga Authors
--
-- Initial schema, see docs/03-design.md. Timestamps are Unix seconds (UTC);
-- segment and event times use milliseconds because segments are seconds long.

-- +goose Up
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE COLLATE NOCASE CHECK (length(username) BETWEEN 1 AND 64),
    password_hash TEXT    NOT NULL,
    role          TEXT    NOT NULL CHECK (role IN ('admin', 'viewer')),
    created_at    INTEGER NOT NULL DEFAULT (unixepoch())
) STRICT;

CREATE TABLE sessions (
    token_hash BLOB    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE cameras (
    id             INTEGER PRIMARY KEY,
    name           TEXT    NOT NULL UNIQUE COLLATE NOCASE CHECK (length(name) BETWEEN 1 AND 64),
    main_url       BLOB    NOT NULL, -- AES-GCM ciphertext, never the plain URL
    sub_url        BLOB,             -- AES-GCM ciphertext, optional
    enabled        INTEGER NOT NULL DEFAULT 1   CHECK (enabled IN (0, 1)),
    record_mode    TEXT    NOT NULL DEFAULT 'continuous' CHECK (record_mode IN ('continuous', 'off')),
    record_audio   INTEGER NOT NULL DEFAULT 0   CHECK (record_audio IN (0, 1)),
    segment_sec    INTEGER NOT NULL DEFAULT 600 CHECK (segment_sec BETWEEN 10 AND 3600),
    retention_days INTEGER CHECK (retention_days >= 1), -- NULL: storage.default_retention_days
    motion_mode    TEXT    NOT NULL DEFAULT 'off' CHECK (motion_mode IN ('off', 'onvif', 'framediff')),
    motion_config  TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(motion_config)),
    created_at     INTEGER NOT NULL DEFAULT (unixepoch())
) STRICT;

CREATE TABLE segments (
    id         INTEGER PRIMARY KEY,
    camera_id  INTEGER NOT NULL REFERENCES cameras (id) ON DELETE CASCADE,
    path       TEXT    NOT NULL UNIQUE, -- relative to data_dir
    start_ts   INTEGER NOT NULL,        -- Unix ms
    end_ts     INTEGER NOT NULL CHECK (end_ts >= start_ts),
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    has_motion INTEGER NOT NULL DEFAULT 0 CHECK (has_motion IN (0, 1))
) STRICT;

CREATE INDEX segments_timeline ON segments (camera_id, start_ts);
-- Retention deletes the oldest segments across all cameras first.
CREATE INDEX segments_age ON segments (start_ts);

CREATE TABLE events (
    id            INTEGER PRIMARY KEY,
    camera_id     INTEGER REFERENCES cameras (id) ON DELETE CASCADE, -- NULL for system events (disk_full)
    type          TEXT    NOT NULL CHECK (type IN ('motion', 'offline', 'online', 'disk_full')),
    start_ts      INTEGER NOT NULL, -- Unix ms
    end_ts        INTEGER CHECK (end_ts IS NULL OR end_ts >= start_ts), -- NULL while still open
    score         REAL,
    snapshot_path TEXT,             -- relative to data_dir
    meta          TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(meta))
) STRICT;

CREATE INDEX events_camera_time ON events (camera_id, start_ts);
CREATE INDEX events_time ON events (start_ts);

CREATE TABLE notifiers (
    id      INTEGER PRIMARY KEY,
    kind    TEXT    NOT NULL CHECK (kind IN ('telegram', 'webhook')),
    config  TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(config)),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
) STRICT;

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT, WITHOUT ROWID;

-- +goose Down
DROP TABLE settings;
DROP TABLE notifiers;
DROP TABLE events;
DROP TABLE segments;
DROP TABLE cameras;
DROP TABLE sessions;
DROP TABLE users;
