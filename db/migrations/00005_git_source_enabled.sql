-- +goose Up
-- A git source syncs only while it is enabled. A new source starts disabled; the sources
-- of an upgrade keep their sync.
ALTER TABLE git_sources ADD COLUMN enabled boolean NOT NULL DEFAULT false;
UPDATE git_sources SET enabled = true;
