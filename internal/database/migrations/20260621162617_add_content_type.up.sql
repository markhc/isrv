-- Migration: Add content_type column to files table
-- This column stores the MIME content type of the file as a first-class field.

ALTER TABLE files ADD COLUMN content_type TEXT;
