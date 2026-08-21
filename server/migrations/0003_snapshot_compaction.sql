ALTER TABLE workspaces ADD COLUMN compaction_snapshot_id TEXT REFERENCES snapshots(snapshot_id);
ALTER TABLE workspaces ADD COLUMN compacted_through_seq INTEGER NOT NULL DEFAULT 0;

CREATE INDEX operations_compaction_idx ON operations(workspace_id, created_at, seq);
