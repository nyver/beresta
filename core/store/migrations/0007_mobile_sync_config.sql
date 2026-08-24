-- Persisted Android sync server connection, mirroring the desktop client's
-- AppSettings sync fields so the connect dialog can be prefilled after it is
-- closed and reopened, and so a previously enabled connection can be
-- reattached automatically after the account unlocks again. Encrypted by
-- SQLCipher along with the rest of the account database; no invite code is
-- stored here since it is a single-use registration token.
CREATE TABLE mobile_sync_config (
    singleton       INTEGER PRIMARY KEY CHECK (singleton = 1),
    enabled         INTEGER NOT NULL,
    server_url      TEXT NOT NULL,
    security_mode   TEXT NOT NULL,
    fingerprint     TEXT NOT NULL,
    updated_unix_ms INTEGER NOT NULL
);
