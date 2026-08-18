-- Records which Yjs binary encoding (see core/sync/yjsadapter.Format) a
-- delta revision's data is in, since ApplyUpdate must be told the format
-- explicitly and cannot detect it from the bytes alone. A checkpoint
-- revision always uses the same format the account package projects its
-- full-document snapshots in (core/account's noteSnapshotFormat, currently
-- FormatV2 = 2), which is also the only format any revision in this schema
-- version has ever been written in, making 2 a safe backfill default for
-- pre-existing rows.
ALTER TABLE revisions ADD COLUMN format INTEGER NOT NULL DEFAULT 2;
