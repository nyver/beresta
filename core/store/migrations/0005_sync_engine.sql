-- Durable phase-6 synchronization state. Cursor columns are explicit so
-- continuity can be enforced transactionally without parsing opaque blobs.
ALTER TABLE sync_cursors ADD COLUMN last_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sync_cursors ADD COLUMN cursor_epoch INTEGER NOT NULL DEFAULT 1;

ALTER TABLE inbox ADD COLUMN quarantine_reason TEXT;
ALTER TABLE outbox ADD COLUMN rejection_reason TEXT;

ALTER TABLE snapshots ADD COLUMN cursor_epoch INTEGER NOT NULL DEFAULT 1;
ALTER TABLE snapshots ADD COLUMN creator_device_id BLOB;
ALTER TABLE snapshots ADD COLUMN hlc_physical_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE snapshots ADD COLUMN hlc_logical INTEGER NOT NULL DEFAULT 0;
ALTER TABLE snapshots ADD COLUMN nonce BLOB;
ALTER TABLE snapshots ADD COLUMN ciphertext BLOB;

CREATE TABLE applied_operations (
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    op_id                 BLOB NOT NULL,
    server_seq            INTEGER NOT NULL,
    envelope_hash         BLOB NOT NULL,
    applied_unix_ms       INTEGER NOT NULL,
    PRIMARY KEY (workspace_id, op_id),
    UNIQUE (workspace_id, server_seq)
);

CREATE INDEX applied_operations_workspace_seq_idx
    ON applied_operations (workspace_id, server_seq);

CREATE TABLE blob_transfers (
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    blob_id               BLOB NOT NULL,
    direction             INTEGER NOT NULL, -- 1=upload, 2=download
    chunk_index           INTEGER NOT NULL,
    chunk_hash            BLOB NOT NULL,
    verified_unix_ms      INTEGER NOT NULL,
    PRIMARY KEY (workspace_id, blob_id, direction, chunk_index)
);

CREATE TABLE snapshot_acknowledgements (
    snapshot_id           BLOB NOT NULL REFERENCES snapshots (id),
    device_id             BLOB NOT NULL,
    signature             BLOB NOT NULL,
    acknowledged_unix_ms  INTEGER NOT NULL,
    PRIMARY KEY (snapshot_id, device_id)
);
