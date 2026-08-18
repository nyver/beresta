-- Beresta client schema v1 (Phase 2B initial migration).
--
-- All identifiers are validated RFC 9562 UUIDv7 values (16 bytes, see
-- core/model.ID) except blob_id, a 32-byte HMAC-SHA-256 private attachment
-- identity, and key_id, 16 bytes of opaque random data. Hybrid Logical
-- Clock (HLC) values are stored as three columns per register:
-- <field>_physical_ms, <field>_logical, and <field>_device_id. NULL clock
-- columns mean the register has never been written, matching the zero
-- core/model.HLC value.
--
-- This file only declares tables and indexes. Connection pragmas (WAL,
-- foreign keys, busy timeout, integrity checks) and the OS-keystore-wrapped
-- per-device database key are the responsibility of the connection layer.

-- Fixed profile constants (crypto_profile, keybag magic/format version, KDF
-- algorithm, derived key length) are not duplicated here: they are pinned
-- literals from crypto_profile_v1 (see docs/crypto-spec.md) and are
-- reconstructed by the account service when it rebuilds the authenticated
-- KeybagHeader for decryption.
CREATE TABLE accounts (
    id                    BLOB PRIMARY KEY,
    x25519_public_key     BLOB NOT NULL,
    ed25519_public_key    BLOB NOT NULL,
    keybag_version        INTEGER NOT NULL,
    keybag_nonce          BLOB NOT NULL,
    keybag_ciphertext     BLOB NOT NULL,
    kdf_salt              BLOB NOT NULL,
    kdf_memory_kib        INTEGER NOT NULL,
    kdf_time_cost         INTEGER NOT NULL,
    kdf_parallelism       INTEGER NOT NULL,
    created_unix_ms       INTEGER NOT NULL
);

CREATE TABLE devices (
    id                    BLOB PRIMARY KEY,
    account_id            BLOB NOT NULL REFERENCES accounts (id),
    public_key            BLOB NOT NULL UNIQUE,
    -- OS-keystore-wrapped BKW1 envelope for this device's own Ed25519
    -- signing private key. NULL for a remote device record synchronized
    -- from another device's authorization, which never holds this device's
    -- private key material.
    signing_key_envelope  BLOB,
    status                INTEGER NOT NULL, -- 1=active, 2=revoked
    is_local              INTEGER NOT NULL DEFAULT 0,
    created_physical_ms   INTEGER NOT NULL,
    created_logical       INTEGER NOT NULL,
    created_device_id     BLOB NOT NULL,
    -- The highest Hybrid Logical Clock value this (local) device has issued,
    -- persisted so a restarted core/model.Clock never regresses. Meaningless
    -- for a remote device record synchronized from another device.
    clock_physical_ms     INTEGER NOT NULL DEFAULT 0,
    clock_logical         INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX devices_account_id_idx ON devices (account_id);
-- At most one row may describe the local device's own record.
CREATE UNIQUE INDEX devices_local_idx ON devices (is_local) WHERE is_local = 1;

CREATE TABLE workspaces (
    id                    BLOB PRIMARY KEY,
    created_physical_ms   INTEGER NOT NULL,
    created_logical       INTEGER NOT NULL,
    created_device_id     BLOB NOT NULL
);

CREATE TABLE workspace_keys (
    key_id                BLOB PRIMARY KEY,
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    state                 INTEGER NOT NULL, -- 1=current, 2=historical, 3=retired
    activated_physical_ms INTEGER NOT NULL,
    activated_logical     INTEGER NOT NULL,
    activated_device_id   BLOB NOT NULL
);

CREATE INDEX workspace_keys_workspace_id_idx ON workspace_keys (workspace_id);
-- At most one current key per workspace.
CREATE UNIQUE INDEX workspace_keys_current_idx ON workspace_keys (workspace_id) WHERE state = 1;

CREATE TABLE notebooks (
    id                    BLOB PRIMARY KEY,
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    parent_id             BLOB REFERENCES notebooks (id),
    parent_physical_ms    INTEGER,
    parent_logical        INTEGER,
    parent_device_id      BLOB,
    name                  TEXT NOT NULL,
    name_physical_ms      INTEGER NOT NULL,
    name_logical          INTEGER NOT NULL,
    name_device_id        BLOB NOT NULL,
    deleted               INTEGER NOT NULL DEFAULT 0,
    deleted_physical_ms   INTEGER,
    deleted_logical       INTEGER,
    deleted_device_id     BLOB,
    created_physical_ms   INTEGER NOT NULL,
    created_logical       INTEGER NOT NULL,
    created_device_id     BLOB NOT NULL
);

CREATE INDEX notebooks_workspace_id_idx ON notebooks (workspace_id);
CREATE INDEX notebooks_parent_id_idx ON notebooks (parent_id);

CREATE TABLE tags (
    id                    BLOB PRIMARY KEY,
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    name                  TEXT NOT NULL,
    created_physical_ms   INTEGER NOT NULL,
    created_logical       INTEGER NOT NULL,
    created_device_id     BLOB NOT NULL,
    deleted               INTEGER NOT NULL DEFAULT 0,
    deleted_physical_ms   INTEGER,
    deleted_logical       INTEGER,
    deleted_device_id     BLOB
);

CREATE UNIQUE INDEX tags_workspace_name_idx ON tags (workspace_id, name);

CREATE TABLE notes (
    id                    BLOB PRIMARY KEY,
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    notebook_id           BLOB REFERENCES notebooks (id),
    notebook_physical_ms  INTEGER,
    notebook_logical      INTEGER,
    notebook_device_id    BLOB,
    title                 TEXT NOT NULL,
    title_physical_ms     INTEGER NOT NULL,
    title_logical         INTEGER NOT NULL,
    title_device_id       BLOB NOT NULL,
    flags                 INTEGER NOT NULL DEFAULT 0,
    flags_physical_ms     INTEGER,
    flags_logical         INTEGER,
    flags_device_id       BLOB,
    deleted               INTEGER NOT NULL DEFAULT 0,
    deleted_physical_ms   INTEGER,
    deleted_logical       INTEGER,
    deleted_device_id     BLOB,
    created_physical_ms   INTEGER NOT NULL,
    created_logical       INTEGER NOT NULL,
    created_device_id     BLOB NOT NULL
);

CREATE INDEX notes_workspace_id_idx ON notes (workspace_id);
CREATE INDEX notes_notebook_id_idx ON notes (notebook_id);
CREATE INDEX notes_deleted_idx ON notes (deleted);

-- Per-tag membership register so concurrent edits to unrelated tags do not
-- replace the entire tag set (see ASSUMPTIONS.md, Product and Data Model #4).
CREATE TABLE note_tags (
    note_id               BLOB NOT NULL REFERENCES notes (id),
    tag_id                BLOB NOT NULL REFERENCES tags (id),
    present               INTEGER NOT NULL DEFAULT 1,
    physical_ms           INTEGER NOT NULL,
    logical               INTEGER NOT NULL,
    device_id             BLOB NOT NULL,
    PRIMARY KEY (note_id, tag_id)
);

CREATE INDEX note_tags_tag_id_idx ON note_tags (tag_id);

CREATE TABLE crdt_states (
    note_id               BLOB PRIMARY KEY REFERENCES notes (id),
    snapshot              BLOB NOT NULL,
    state_vector          BLOB NOT NULL,
    updated_unix_ms       INTEGER NOT NULL
);

CREATE TABLE crdt_updates (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    note_id               BLOB NOT NULL REFERENCES notes (id),
    update_bytes          BLOB NOT NULL,
    origin_device_id      BLOB NOT NULL,
    created_unix_ms       INTEGER NOT NULL
);

CREATE INDEX crdt_updates_note_id_idx ON crdt_updates (note_id, id);

CREATE TABLE revisions (
    id                    BLOB PRIMARY KEY,
    note_id               BLOB NOT NULL REFERENCES notes (id),
    kind                  INTEGER NOT NULL, -- 1=checkpoint, 2=delta
    data                  BLOB NOT NULL,
    created_unix_ms       INTEGER NOT NULL
);

CREATE INDEX revisions_note_id_idx ON revisions (note_id, created_unix_ms);

CREATE TABLE attachments (
    blob_id               BLOB PRIMARY KEY,
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    key_id                BLOB NOT NULL,
    manifest              BLOB NOT NULL,
    size_bytes            INTEGER NOT NULL,
    chunk_count           INTEGER NOT NULL,
    created_unix_ms       INTEGER NOT NULL
);

CREATE INDEX attachments_workspace_id_idx ON attachments (workspace_id);

-- One row per note-attachment reference. Blob garbage collection counts
-- rows here rather than maintaining a separate reference-count column.
CREATE TABLE note_attachments (
    note_id               BLOB NOT NULL REFERENCES notes (id),
    blob_id               BLOB NOT NULL REFERENCES attachments (blob_id),
    present               INTEGER NOT NULL DEFAULT 1,
    physical_ms           INTEGER NOT NULL,
    logical               INTEGER NOT NULL,
    device_id             BLOB NOT NULL,
    PRIMARY KEY (note_id, blob_id)
);

CREATE INDEX note_attachments_blob_id_idx ON note_attachments (blob_id);

CREATE TABLE outbox (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    op_id                 BLOB NOT NULL UNIQUE,
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    device_id             BLOB NOT NULL,
    physical_ms           INTEGER NOT NULL,
    logical               INTEGER NOT NULL,
    key_id                BLOB NOT NULL,
    nonce                 BLOB NOT NULL,
    ciphertext            BLOB NOT NULL,
    signature             BLOB NOT NULL,
    created_unix_ms       INTEGER NOT NULL,
    pushed_unix_ms        INTEGER,
    server_seq            INTEGER
);

CREATE INDEX outbox_workspace_pending_idx ON outbox (workspace_id, id) WHERE pushed_unix_ms IS NULL;

CREATE TABLE inbox (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    op_id                 BLOB NOT NULL,
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    device_id             BLOB NOT NULL,
    physical_ms           INTEGER NOT NULL,
    logical               INTEGER NOT NULL,
    key_id                BLOB NOT NULL,
    nonce                 BLOB NOT NULL,
    ciphertext            BLOB NOT NULL,
    signature             BLOB NOT NULL,
    server_seq            INTEGER NOT NULL,
    received_unix_ms      INTEGER NOT NULL,
    status                INTEGER NOT NULL DEFAULT 1 -- 1=pending, 2=applied, 3=quarantined
);

CREATE UNIQUE INDEX inbox_workspace_op_idx ON inbox (workspace_id, op_id);
CREATE UNIQUE INDEX inbox_workspace_seq_idx ON inbox (workspace_id, server_seq);

CREATE TABLE sync_cursors (
    workspace_id          BLOB PRIMARY KEY REFERENCES workspaces (id),
    transport             TEXT NOT NULL,
    cursor                BLOB NOT NULL,
    updated_unix_ms       INTEGER NOT NULL
);

CREATE TABLE snapshots (
    id                    BLOB PRIMARY KEY,
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    key_id                BLOB NOT NULL,
    base_seq              INTEGER NOT NULL,
    ciphertext_hash       BLOB NOT NULL,
    signature             BLOB NOT NULL,
    created_unix_ms       INTEGER NOT NULL,
    acknowledged_unix_ms  INTEGER
);

CREATE INDEX snapshots_workspace_id_idx ON snapshots (workspace_id, created_unix_ms);

CREATE TABLE backups (
    id                    BLOB PRIMARY KEY,
    kind                  INTEGER NOT NULL, -- 1=daily, 2=pre-migration, 3=pre-restore, 4=manual
    location              TEXT NOT NULL,
    manifest_hash         BLOB NOT NULL,
    verified_unix_ms      INTEGER,
    note_count            INTEGER,
    size_bytes            INTEGER,
    created_unix_ms       INTEGER NOT NULL
);

CREATE INDEX backups_kind_created_idx ON backups (kind, created_unix_ms);

CREATE TABLE saved_searches (
    id                    BLOB PRIMARY KEY,
    workspace_id          BLOB NOT NULL REFERENCES workspaces (id),
    name                  TEXT NOT NULL,
    query                 TEXT NOT NULL,
    created_unix_ms       INTEGER NOT NULL,
    updated_unix_ms       INTEGER NOT NULL
);

CREATE UNIQUE INDEX saved_searches_workspace_name_idx ON saved_searches (workspace_id, name);

-- Standalone (non external-content) FTS5 index. The repository layer keeps
-- it in sync with notes/crdt_states inside the same commit transaction
-- rather than relying on SQL triggers, so an aborted transaction cannot
-- leave the index and canonical rows inconsistent.
CREATE VIRTUAL TABLE notes_fts USING fts5 (
    note_id UNINDEXED,
    title,
    body,
    tokenize = 'unicode61'
);
