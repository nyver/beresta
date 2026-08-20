CREATE TABLE users (
    user_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    identity_public BLOB NOT NULL CHECK (length(identity_public) = 32),
    authority_public BLOB NOT NULL CHECK (length(authority_public) = 32),
    quota_bytes INTEGER NOT NULL CHECK (quota_bytes > 0),
    used_bytes INTEGER NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
    reserved_bytes INTEGER NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0),
    created_at INTEGER NOT NULL
);

CREATE TABLE devices (
    device_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    display_name TEXT NOT NULL,
    signing_public BLOB NOT NULL CHECK (length(signing_public) = 32),
    created_at INTEGER NOT NULL,
    revoked_at INTEGER,
    UNIQUE (user_id, device_id)
);
CREATE INDEX devices_user_active_idx ON devices(user_id, revoked_at);

CREATE TABLE invites (
    invite_id TEXT PRIMARY KEY,
    code_hash BLOB NOT NULL UNIQUE CHECK (length(code_hash) = 32),
    display_name TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    consumed_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX invites_expiry_idx ON invites(expires_at, consumed_at);

CREATE TABLE challenges (
    challenge_id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES devices(device_id) ON DELETE CASCADE,
    nonce_hash BLOB NOT NULL UNIQUE CHECK (length(nonce_hash) = 32),
    scope TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    consumed_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX challenges_device_expiry_idx ON challenges(device_id, expires_at, consumed_at);

CREATE TABLE sessions (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    user_id TEXT NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES devices(device_id) ON DELETE CASCADE,
    scope TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX sessions_device_expiry_idx ON sessions(device_id, expires_at);

CREATE TABLE keybags (
    user_id TEXT PRIMARY KEY REFERENCES users(user_id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version > 0),
    ciphertext BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE workspaces (
    workspace_id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(user_id),
    current_key_id TEXT NOT NULL,
    latest_seq INTEGER NOT NULL DEFAULT 0 CHECK (latest_seq >= 0),
    cursor_epoch INTEGER NOT NULL DEFAULT 1 CHECK (cursor_epoch > 0),
    created_at INTEGER NOT NULL
);

CREATE TABLE memberships (
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    created_at INTEGER NOT NULL,
    revoked_at INTEGER,
    PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX memberships_user_active_idx ON memberships(user_id, revoked_at, workspace_id);

CREATE TABLE key_envelopes (
    workspace_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    key_id TEXT NOT NULL,
    envelope BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (workspace_id, user_id, key_id),
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships(workspace_id, user_id) ON DELETE CASCADE
);

CREATE TABLE operations (
    op_id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES devices(device_id),
    seq INTEGER NOT NULL CHECK (seq > 0),
    hlc_physical_ms INTEGER NOT NULL CHECK (hlc_physical_ms >= 0),
    hlc_logical INTEGER NOT NULL CHECK (hlc_logical >= 0),
    key_id TEXT NOT NULL,
    nonce BLOB NOT NULL CHECK (length(nonce) = 24),
    ciphertext BLOB NOT NULL,
    signature BLOB NOT NULL CHECK (length(signature) = 64),
    envelope_hash BLOB NOT NULL CHECK (length(envelope_hash) = 32),
    created_at INTEGER NOT NULL,
    UNIQUE (workspace_id, seq)
);
CREATE INDEX operations_workspace_seq_idx ON operations(workspace_id, seq);
CREATE INDEX operations_device_idx ON operations(device_id, created_at);

CREATE TABLE blobs (
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id) ON DELETE CASCADE,
    blob_id TEXT NOT NULL,
    owner_user_id TEXT NOT NULL REFERENCES users(user_id),
    key_id TEXT NOT NULL,
    encrypted_manifest BLOB NOT NULL,
    total_bytes INTEGER NOT NULL CHECK (total_bytes > 0),
    chunk_count INTEGER NOT NULL CHECK (chunk_count > 0),
    state TEXT NOT NULL CHECK (state IN ('staging', 'complete')),
    reserved_bytes INTEGER NOT NULL CHECK (reserved_bytes >= 0),
    reference_count INTEGER NOT NULL DEFAULT 0 CHECK (reference_count >= 0),
    created_at INTEGER NOT NULL,
    completed_at INTEGER,
    unreferenced_at INTEGER,
    PRIMARY KEY (workspace_id, blob_id)
);
CREATE INDEX blobs_owner_state_idx ON blobs(owner_user_id, state);
CREATE INDEX blobs_gc_idx ON blobs(state, reference_count, unreferenced_at);

CREATE TABLE blob_references (
    workspace_id TEXT NOT NULL,
    blob_id TEXT NOT NULL,
    reference_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (workspace_id, reference_id),
    FOREIGN KEY (workspace_id, blob_id) REFERENCES blobs(workspace_id, blob_id) ON DELETE CASCADE
);
CREATE INDEX blob_references_blob_idx ON blob_references(workspace_id, blob_id);

CREATE TABLE blob_chunks (
    workspace_id TEXT NOT NULL,
    blob_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL CHECK (chunk_index >= 0),
    expected_bytes INTEGER NOT NULL CHECK (expected_bytes > 0),
    expected_hash BLOB NOT NULL CHECK (length(expected_hash) = 32),
    uploaded_at INTEGER,
    PRIMARY KEY (workspace_id, blob_id, chunk_index),
    FOREIGN KEY (workspace_id, blob_id) REFERENCES blobs(workspace_id, blob_id) ON DELETE CASCADE
);

CREATE TABLE snapshots (
    snapshot_id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id) ON DELETE CASCADE,
    base_seq INTEGER NOT NULL CHECK (base_seq >= 0),
    cursor_epoch INTEGER NOT NULL CHECK (cursor_epoch > 0),
    key_id TEXT NOT NULL,
    creator_device_id TEXT NOT NULL REFERENCES devices(device_id),
    hlc_physical_ms INTEGER NOT NULL CHECK (hlc_physical_ms >= 0),
    hlc_logical INTEGER NOT NULL CHECK (hlc_logical >= 0),
    nonce BLOB NOT NULL CHECK (length(nonce) = 24),
    ciphertext_hash BLOB NOT NULL CHECK (length(ciphertext_hash) = 32),
    ciphertext BLOB NOT NULL,
    signature BLOB NOT NULL CHECK (length(signature) = 64),
    eligible_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX snapshots_workspace_base_idx ON snapshots(workspace_id, base_seq DESC, created_at DESC);

CREATE TABLE snapshot_acknowledgements (
    snapshot_id TEXT NOT NULL REFERENCES snapshots(snapshot_id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES devices(device_id) ON DELETE CASCADE,
    ciphertext_hash BLOB NOT NULL CHECK (length(ciphertext_hash) = 32),
    signature BLOB NOT NULL CHECK (length(signature) = 64),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (snapshot_id, device_id)
);
