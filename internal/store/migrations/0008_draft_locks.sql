-- Draft locking (D-050). Two people on one draft used to overwrite each
-- other; a draft now carries who holds it and since when.
--
-- locked_by is the id of the admin token that holds the lock, which is the
-- identity the server compares. locked_name is the name of that token as it
-- was when the lock was taken, so a notice can say who holds the draft even
-- after the token is gone. locked_at is the last refresh in unix
-- milliseconds; the editor refreshes every minute and a lock that stopped
-- being refreshed falls away by itself.
ALTER TABLE scenario_drafts ADD COLUMN locked_by   TEXT    NOT NULL DEFAULT '';
ALTER TABLE scenario_drafts ADD COLUMN locked_name TEXT    NOT NULL DEFAULT '';
ALTER TABLE scenario_drafts ADD COLUMN locked_at   INTEGER NOT NULL DEFAULT 0;
