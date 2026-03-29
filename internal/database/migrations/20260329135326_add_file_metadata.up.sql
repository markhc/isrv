-- Migration: Add metadata column to files table
-- This column will store mixed metadata about the file, such as Content-Type.

ALTER TABLE files ADD COLUMN metadata TEXT;