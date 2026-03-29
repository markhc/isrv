-- Migration: Add ip_address column to files table
-- This column will store the IP address of the uploader for each file. 

ALTER TABLE files ADD COLUMN ip_address TEXT;