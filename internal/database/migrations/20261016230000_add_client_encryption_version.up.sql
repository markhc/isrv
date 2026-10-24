-- Identifies an inner browser-encrypted container independently from the
-- server-managed encryption-at-rest layer.
ALTER TABLE files ADD COLUMN client_encryption_version INTEGER NOT NULL DEFAULT 0;
