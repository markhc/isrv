ALTER TABLE files DROP COLUMN token;
DROP INDEX IF EXISTS idx_files_token;