-- Scenario drafts of the editor (D-041). A draft is the working copy of one
-- scenario: the manifest as JSON, the media below content/drafts/<id>/media,
-- and what the server knows about each media file as JSON. A draft is not a
-- package; the server writes the package on publish (D-042), which is why
-- the manifest here carries no [files] table and no hash.
CREATE TABLE scenario_drafts (
  id          TEXT PRIMARY KEY,
  scenario_id TEXT NOT NULL,
  manifest    TEXT NOT NULL DEFAULT '{}',
  media       TEXT NOT NULL DEFAULT '{}',
  created_at  INTEGER NOT NULL,
  created_by  TEXT NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL,
  updated_by  TEXT NOT NULL DEFAULT '',
  -- published_version is the version this draft last published, 0 while it
  -- has published none.
  published_version INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX scenario_drafts_scenario ON scenario_drafts (scenario_id);
