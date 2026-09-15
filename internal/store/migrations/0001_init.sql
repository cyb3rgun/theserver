CREATE TABLE schema_migrations (
  version    INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);

CREATE TABLE devices (
  id               TEXT PRIMARY KEY,
  kind             TEXT NOT NULL CHECK (kind IN ('target','controller','bridge')),
  class            TEXT NOT NULL DEFAULT 'esp' CHECK (class IN ('esp','pi','pc')),
  name             TEXT NOT NULL DEFAULT '',
  room             TEXT NOT NULL DEFAULT '',
  zone             TEXT NOT NULL DEFAULT '',
  status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','blocked')),
  token_hash       BLOB,
  firmware_version TEXT NOT NULL DEFAULT '',
  config_json      TEXT NOT NULL DEFAULT '{}',
  first_seen       INTEGER,
  last_seen        INTEGER,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  scenario   TEXT NOT NULL DEFAULT '',
  room       TEXT NOT NULL DEFAULT '',
  state      TEXT NOT NULL DEFAULT 'created' CHECK (state IN ('created','running','stopped')),
  started_at INTEGER,
  ended_at   INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE session_devices (
  session_id TEXT NOT NULL REFERENCES sessions(id),
  device_id  TEXT NOT NULL REFERENCES devices(id),
  PRIMARY KEY (session_id, device_id)
);

CREATE TABLE events (
  event_id      BLOB PRIMARY KEY,
  device_id     TEXT NOT NULL REFERENCES devices(id),
  seq           INTEGER NOT NULL,
  kind          TEXT NOT NULL,
  controller_id TEXT,
  session_id    TEXT REFERENCES sessions(id),
  ts_device     INTEGER NOT NULL,
  ts_server     INTEGER NOT NULL,
  payload       BLOB NOT NULL,
  UNIQUE (device_id, seq)
);

CREATE INDEX events_session_controller ON events (session_id, controller_id);
CREATE INDEX events_device_ts ON events (device_id, ts_server);
