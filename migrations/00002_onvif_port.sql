-- SPDX-License-Identifier: Apache-2.0
-- Copyright The Jaga Authors
--
-- ONVIF service port per camera (Tapo: 2020). NULL: no ONVIF, so no PTZ.
-- The host and credentials come from main_url.

-- +goose Up
ALTER TABLE cameras ADD COLUMN onvif_port INTEGER CHECK (onvif_port BETWEEN 1 AND 65535);

-- +goose Down
ALTER TABLE cameras DROP COLUMN onvif_port;
