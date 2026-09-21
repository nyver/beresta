-- Records the authority signature over each workspace key rotation
-- alongside the sorted recipient set it was signed over, so any active
-- member's device can verify a rotation was authorized by the workspace
-- owner's authority key rather than merely asserted by the server (see
-- core/account.BeginWorkspaceKeyRotation / AcceptWorkspaceKeyRotation and
-- openspec/changes/harden-product-ux-reliability/design.md decision 10).
-- The server stores this opaque signature and never verifies it itself.
CREATE TABLE key_transitions (
    workspace_id       TEXT NOT NULL REFERENCES workspaces(workspace_id) ON DELETE CASCADE,
    key_id              TEXT NOT NULL,
    signature           BLOB NOT NULL,
    recipient_user_ids  TEXT NOT NULL, -- sorted, comma-separated user IDs the signature covers
    created_at          INTEGER NOT NULL,
    PRIMARY KEY (workspace_id, key_id)
);
