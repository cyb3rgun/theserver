-- The history of a draft (D-051). Every change of a manifest keeps the
-- patch that was applied and the manifest as it stood before it, so a
-- version can be restored on the server; the browser keeps its own states
-- for undo and redo and never asks for these.
--
-- Only the newest DraftHistoryDepth rows of a draft are kept, the older ones
-- are dropped when the next change is written.
CREATE TABLE scenario_draft_history (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  draft_id TEXT    NOT NULL,
  -- patch is the JSON merge patch that was applied, kept so that the list
  -- can say which parts of the scenario a change touched.
  patch    TEXT    NOT NULL DEFAULT '{}',
  -- manifest is the state before the patch, which is what a restore writes.
  manifest TEXT    NOT NULL DEFAULT '{}',
  at       INTEGER NOT NULL,
  by       TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX scenario_draft_history_draft ON scenario_draft_history (draft_id, id DESC);
