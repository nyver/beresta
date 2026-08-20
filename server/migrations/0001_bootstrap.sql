CREATE TABLE server_metadata (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    initialized_at INTEGER NOT NULL
);

INSERT INTO server_metadata (id, initialized_at)
VALUES (1, unixepoch());
