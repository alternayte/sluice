-- +goose Up
-- Artifact rows of a restarted execution name the storage key of the execution that produced
-- the artifact. Retention and the storage cleanup find the rows of a key through this index.
CREATE INDEX artifacts_storage_key_idx ON artifacts (storage_key);
