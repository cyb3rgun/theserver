-- Scenario packages (D-036). The packages live in the content directory,
-- content/<id>/<version>/package.zip, exactly as uploaded; this table is
-- their index. The manifest carries the version. A published version is
-- never changed or deleted in S01; a draft may be replaced or deleted
-- (D-037). title is a JSON object of language and text, problems the JSON
-- array of the validation problems of a draft.
CREATE TABLE scenarios (
  id            TEXT NOT NULL,
  version       INTEGER NOT NULL CHECK (version >= 1),
  tier          TEXT NOT NULL,
  title         TEXT NOT NULL DEFAULT '{}',
  age_rating    TEXT NOT NULL,
  manifest_hash TEXT NOT NULL,
  size          INTEGER NOT NULL,
  uploaded_at   INTEGER NOT NULL,
  uploaded_by   TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published')),
  published_at  INTEGER,
  problems      TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY (id, version)
);

-- The scenario versions a device reported (D-038). A row stays once a
-- device held the version; current is 1 while the latest report of the
-- device includes it, and installed_at is when it last became current. A
-- device may report a scenario this server does not know, so there is no
-- reference to scenarios.
CREATE TABLE device_scenarios (
  device_id    TEXT NOT NULL REFERENCES devices(id),
  scenario_id  TEXT NOT NULL,
  version      INTEGER NOT NULL,
  installed_at INTEGER NOT NULL,
  current      INTEGER NOT NULL DEFAULT 1 CHECK (current IN (0, 1)),
  PRIMARY KEY (device_id, scenario_id, version)
);

CREATE INDEX device_scenarios_scenario ON device_scenarios (scenario_id, version);
