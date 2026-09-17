-- A device reset starts a new sequence epoch (D-026): a device that lost its
-- counter begins at seq 1 again, and the events of earlier epochs stay.
ALTER TABLE devices ADD COLUMN seq_epoch INTEGER NOT NULL DEFAULT 1;

-- The unique constraint of events moves from (device_id, seq) to
-- (device_id, seq_epoch, seq). SQLite cannot change a table constraint, so the
-- table is rebuilt; every event stored so far belongs to epoch 1.
CREATE TABLE events_new (
  event_id      BLOB PRIMARY KEY,
  device_id     TEXT NOT NULL REFERENCES devices(id),
  seq_epoch     INTEGER NOT NULL DEFAULT 1,
  seq           INTEGER NOT NULL,
  kind          TEXT NOT NULL,
  controller_id TEXT,
  session_id    TEXT REFERENCES sessions(id),
  ts_device     INTEGER NOT NULL,
  ts_server     INTEGER NOT NULL,
  payload       BLOB NOT NULL,
  UNIQUE (device_id, seq_epoch, seq)
);

INSERT INTO events_new (
  event_id, device_id, seq_epoch, seq, kind, controller_id, session_id, ts_device, ts_server, payload
)
SELECT event_id, device_id, 1, seq, kind, controller_id, session_id, ts_device, ts_server, payload
  FROM events;

DROP TABLE events;
ALTER TABLE events_new RENAME TO events;

CREATE INDEX events_session_controller ON events (session_id, controller_id);
CREATE INDEX events_device_ts ON events (device_id, ts_server);

-- Admin API tokens (D-025). Only the SHA-256 of a token is stored. A revoked
-- token keeps its row with revoked_at set, so a request with it is answered
-- 403 and not 401.
CREATE TABLE admin_tokens (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  token_hash BLOB NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  last_used  INTEGER,
  revoked_at INTEGER
);
