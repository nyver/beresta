-- Tracks a backup found corrupt by manifest verification, so it can be
-- excluded from restore eligibility and from the seven-valid-day rotation
-- count (see specs/backup-and-recovery.md, "Backup integrity
-- classification") without being silently deleted: a corrupt archive is
-- kept, marked, and left for the user to see or replace, not removed.
ALTER TABLE backups ADD COLUMN corrupt INTEGER NOT NULL DEFAULT 0;
