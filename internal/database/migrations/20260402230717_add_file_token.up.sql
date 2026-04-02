-- Migration: Add token column to files table
-- This column will store mixed token about the file, such as Content-Type.

ALTER TABLE files ADD COLUMN token TEXT NULL DEFAULT NULL;

CREATE INDEX idx_files_token ON files(token);