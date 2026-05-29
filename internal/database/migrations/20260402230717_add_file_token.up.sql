-- Migration: Add token column to files table
-- The token is the password used to manage the file, such as for deletion.

ALTER TABLE files ADD COLUMN token TEXT NULL DEFAULT NULL;

CREATE INDEX idx_files_token ON files(token);