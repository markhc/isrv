-- Migration: Add encryption_version column to files table
-- This column identifies the at-rest encryption scheme used for the stored
-- object. Zero (the default) means the object is stored in plaintext.

ALTER TABLE files ADD COLUMN encryption_version INTEGER NOT NULL DEFAULT 0;
