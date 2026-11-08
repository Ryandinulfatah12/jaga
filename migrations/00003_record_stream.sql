-- SPDX-License-Identifier: Apache-2.0
-- Copyright The Jaga Authors
--
-- Which stream is recorded: main (full resolution) or sub (smaller files).
-- 'sub' needs sub_url; the store checks that.

-- +goose Up
ALTER TABLE cameras ADD COLUMN record_stream TEXT NOT NULL DEFAULT 'main' CHECK (record_stream IN ('main', 'sub'));

-- +goose Down
ALTER TABLE cameras DROP COLUMN record_stream;
