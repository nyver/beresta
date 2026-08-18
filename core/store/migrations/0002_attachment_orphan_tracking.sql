-- Tracks when an attachment lost its last live note_attachments reference,
-- so a future garbage-collection sweep (see docs/threat-model.md's
-- crash-safety row and the blob store's grace-period design) can enforce a
-- minimum retention window before deleting an unreferenced blob. NULL means
-- the attachment is currently referenced by at least one note; the
-- repository layer maintains this column transactionally alongside
-- note_attachments writes rather than relying on a periodic full scan.
ALTER TABLE attachments ADD COLUMN orphaned_unix_ms INTEGER;
