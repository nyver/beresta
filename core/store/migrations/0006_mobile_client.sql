-- Encrypted Android-only preferences and downloaded attachment cache state.
-- Both tables live in SQLCipher; no note title, path, or selection leaks to
-- Android SharedPreferences.
CREATE TABLE mobile_preferences (
    singleton             INTEGER PRIMARY KEY CHECK (singleton = 1),
    value_json            BLOB NOT NULL,
    updated_unix_ms       INTEGER NOT NULL
);

CREATE TABLE mobile_attachment_cache (
    blob_id               BLOB PRIMARY KEY REFERENCES attachments (blob_id),
    notebook_id           BLOB,
    size_bytes            INTEGER NOT NULL,
    pinned                INTEGER NOT NULL DEFAULT 0,
    synchronized_original INTEGER NOT NULL DEFAULT 0,
    last_access_unix_ms   INTEGER NOT NULL
);

CREATE INDEX mobile_attachment_cache_lru_idx
    ON mobile_attachment_cache (pinned, synchronized_original, last_access_unix_ms);
