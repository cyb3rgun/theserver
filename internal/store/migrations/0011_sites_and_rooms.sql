-- The shape of a venue (D-066): a site is the place, a room belongs to a
-- site, a target is a device of kind target in a room, and a controller is a
-- device of kind controller that belongs to a site and may belong to a room.
--
-- The licence columns of a site are set by the manufacturer and are stored
-- and shown, not yet used for anything (D-070). franchise_rate is a
-- percentage, monthly_threshold is in the smallest unit of currency, and
-- valid_from is unix milliseconds, 0 while it is not set.
CREATE TABLE sites (
  id                TEXT PRIMARY KEY,
  name              TEXT    NOT NULL DEFAULT '',
  address           TEXT    NOT NULL DEFAULT '',
  timezone          TEXT    NOT NULL DEFAULT '',
  contact           TEXT    NOT NULL DEFAULT '',
  notes             TEXT    NOT NULL DEFAULT '{}',
  licence_id        TEXT    NOT NULL DEFAULT '',
  franchise_rate    REAL    NOT NULL DEFAULT 0,
  monthly_threshold INTEGER NOT NULL DEFAULT 0,
  currency          TEXT    NOT NULL DEFAULT 'EUR',
  valid_from        INTEGER NOT NULL DEFAULT 0,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);

-- A room carries the age its players are, the WiFi channel of its access
-- point and the beacon multiplex plan: the period of one round in
-- milliseconds and how many slots that round has. A target of the room takes
-- one slot.
CREATE TABLE rooms (
  id               TEXT PRIMARY KEY,
  site_id          TEXT    NOT NULL REFERENCES sites(id),
  name             TEXT    NOT NULL DEFAULT '',
  age_rating       TEXT    NOT NULL DEFAULT '18',
  wifi_channel     INTEGER NOT NULL DEFAULT 0,
  beacon_period_ms INTEGER NOT NULL DEFAULT 100,
  beacon_slots     INTEGER NOT NULL DEFAULT 8,
  capacity         INTEGER NOT NULL DEFAULT 0,
  notes            TEXT    NOT NULL DEFAULT '{}',
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);

CREATE INDEX rooms_site ON rooms (site_id, name);

-- Where a device stands. beacon_slot is the slot of a target in the plan of
-- its room, 0 while none is given; position is a free note such as "left of
-- the door".
ALTER TABLE devices ADD COLUMN site_id TEXT REFERENCES sites(id);
ALTER TABLE devices ADD COLUMN room_id TEXT REFERENCES rooms(id);
ALTER TABLE devices ADD COLUMN beacon_slot INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN position TEXT NOT NULL DEFAULT '';

CREATE INDEX devices_room ON devices (room_id, id);
CREATE INDEX devices_site ON devices (site_id, id);

-- A session runs in a room (D-067). The old free text room column stays for
-- what was written into it; the pages show the room instead.
ALTER TABLE sessions ADD COLUMN room_id TEXT REFERENCES rooms(id);

-- What exists keeps its history (D-069). Every device lands in the site
-- Default, in a room named after the text it carried, or in a room Default
-- when it carried none.
INSERT INTO sites (id, name, timezone, notes, created_at, updated_at)
SELECT 'site-default', 'Default', 'Europe/Berlin',
       '{"en":"The site the devices of earlier versions were moved into.","de":"Der Standort, in den die Geräte älterer Versionen übernommen wurden."}',
       CAST(strftime('%s','now') AS INTEGER) * 1000,
       CAST(strftime('%s','now') AS INTEGER) * 1000
 WHERE EXISTS (SELECT 1 FROM devices);

INSERT INTO rooms (id, site_id, name, notes, created_at, updated_at)
SELECT 'room-' || lower(hex(randomblob(3))), 'site-default', name,
       '{"en":"Made by the move to sites and rooms.","de":"Beim Umzug auf Standorte und Räume angelegt."}',
       CAST(strftime('%s','now') AS INTEGER) * 1000,
       CAST(strftime('%s','now') AS INTEGER) * 1000
  FROM (SELECT DISTINCT CASE WHEN room = '' THEN 'Default' ELSE room END AS name FROM devices)
 ORDER BY name;

UPDATE devices
   SET site_id = 'site-default',
       room_id = (SELECT r.id FROM rooms r
                   WHERE r.site_id = 'site-default'
                     AND r.name = CASE WHEN devices.room = '' THEN 'Default' ELSE devices.room END);

-- A session that names a room by text runs in that room from now on.
UPDATE sessions
   SET room_id = (SELECT r.id FROM rooms r WHERE r.site_id = 'site-default' AND r.name = sessions.room)
 WHERE room <> '';

-- Every beacon cluster names the output channel of the target module that
-- drives it (D-068). The seeds of 0010 are written again with the channels 0
-- to 3, in the order they were seeded; a cluster of any other type that has
-- no channel reads as channel 0.
UPDATE target_types
   SET beacons = '[{"x":108.5,"y":0,"ch":0},{"x":217,"y":68,"ch":1},{"x":108.5,"y":136,"ch":2},{"x":0,"y":68,"ch":3}]',
       updated_at = CAST(strftime('%s','now') AS INTEGER) * 1000
 WHERE id = 'board-10' AND builtin = 1;

UPDATE target_types
   SET beacons = '[{"x":-27.5,"y":-68,"ch":0},{"x":322.5,"y":-68,"ch":1},{"x":322.5,"y":132,"ch":2},{"x":-27.5,"y":132,"ch":3}]',
       updated_at = CAST(strftime('%s','now') AS INTEGER) * 1000
 WHERE id = 'bar-12' AND builtin = 1;

UPDATE target_types
   SET beacons = '[{"x":553.5,"y":0,"ch":0},{"x":1107,"y":311.5,"ch":1},{"x":553.5,"y":623,"ch":2},{"x":0,"y":311.5,"ch":3}]',
       updated_at = CAST(strftime('%s','now') AS INTEGER) * 1000
 WHERE id = 'tv-50' AND builtin = 1;
