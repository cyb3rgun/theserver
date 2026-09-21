-- Target types (D-061): a kind of target described once, so that devices,
-- scenarios and clients stop carrying the same numbers by hand. The display
-- size is in millimetres, the resolution in pixels, and the beacon layout is
-- a list of clusters with x and y in millimetres from the top left corner of
-- the picture. A cluster may sit outside the picture, so negative values are
-- allowed.
--
-- name and notes are JSON objects with one text per language, as scenario
-- titles are stored. builtin marks the seeds below: they can be edited but
-- not deleted (D-064).
CREATE TABLE target_types (
  id           TEXT PRIMARY KEY,
  name         TEXT    NOT NULL DEFAULT '{}',
  class        TEXT    NOT NULL DEFAULT 'pi' CHECK (class IN ('esp','pi','pc')),
  display_w_mm REAL    NOT NULL,
  display_h_mm REAL    NOT NULL,
  res_w        INTEGER NOT NULL,
  res_h        INTEGER NOT NULL,
  orientation  TEXT    NOT NULL DEFAULT 'landscape' CHECK (orientation IN ('landscape','portrait')),
  beacons      TEXT    NOT NULL DEFAULT '[]',
  sound        TEXT    NOT NULL DEFAULT 'none' CHECK (sound IN ('none','builtin','hdmi','usb')),
  notes        TEXT    NOT NULL DEFAULT '{}',
  builtin      INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);

-- The type a device is: NULL until an operator assigns one.
ALTER TABLE devices ADD COLUMN target_type TEXT REFERENCES target_types(id);

CREATE INDEX devices_target_type ON devices (target_type);

-- The three types that exist today (D-064). The display sizes come from the
-- diagonal and the aspect ratio of the panel and are rounded to whole
-- millimetres; the operator corrects them from the datasheet in the admin.
INSERT INTO target_types (
  id, name, class, display_w_mm, display_h_mm, res_w, res_h, orientation, beacons, sound, notes, builtin,
  created_at, updated_at
) VALUES
  (
    'board-10',
    '{"en":"Board 10.1 inch","de":"Board 10,1 Zoll"}',
    'pi', 217, 136, 1280, 800, 'landscape',
    '[{"x":108.5,"y":0},{"x":217,"y":68},{"x":108.5,"y":136},{"x":0,"y":68}]',
    'builtin',
    '{"en":"The board target: a 10.1 inch panel with the beacon clusters at the midpoints of its edges.","de":"Das Board-Ziel: ein 10,1-Zoll-Panel mit den Bakenclustern in den Kantenmitten."}',
    1,
    CAST(strftime('%s','now') AS INTEGER) * 1000,
    CAST(strftime('%s','now') AS INTEGER) * 1000
  ),
  (
    'bar-12',
    '{"en":"Bar display 11.9 inch","de":"Leistendisplay 11,9 Zoll"}',
    'pi', 295, 64, 1480, 320, 'landscape',
    '[{"x":-27.5,"y":-68},{"x":322.5,"y":-68},{"x":322.5,"y":132},{"x":-27.5,"y":132}]',
    'hdmi',
    '{"en":"The bar display over HDMI, with the clusters on the corners of a 350 by 200 mm frame around the picture.","de":"Das Leistendisplay über HDMI, mit den Clustern an den Ecken eines Rahmens von 350 mal 200 mm um das Bild."}',
    1,
    CAST(strftime('%s','now') AS INTEGER) * 1000,
    CAST(strftime('%s','now') AS INTEGER) * 1000
  ),
  (
    'tv-50',
    '{"en":"Television 50 inch","de":"Fernseher 50 Zoll"}',
    'pc', 1107, 623, 1920, 1080, 'landscape',
    '[{"x":553.5,"y":0},{"x":1107,"y":311.5},{"x":553.5,"y":623},{"x":0,"y":311.5}]',
    'hdmi',
    '{"en":"A 50 inch television driven by a mini PC, with the beacon clusters at the midpoints of its edges.","de":"Ein 50-Zoll-Fernseher an einem Mini-PC, mit den Bakenclustern in den Kantenmitten."}',
    1,
    CAST(strftime('%s','now') AS INTEGER) * 1000,
    CAST(strftime('%s','now') AS INTEGER) * 1000
  );
