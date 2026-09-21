-- Tracks, per workspace, that this device began a workspace-key rotation
-- (see core/account.BeginWorkspaceKeyRotation) that has not yet been
-- confirmed published and applied locally. Its presence holds this
-- workspace's outbound sync push - pull/apply continue normally - until the
-- rotation completes, so a crash or network failure between revoking a
-- member and finishing the rotation cannot leave a new local edit sealed
-- under the key the removed member still holds. See
-- openspec/changes/harden-product-ux-reliability/design.md decision 10.
CREATE TABLE pending_key_rotations (
    workspace_id      BLOB PRIMARY KEY REFERENCES workspaces (id),
    requested_unix_ms INTEGER NOT NULL
);
