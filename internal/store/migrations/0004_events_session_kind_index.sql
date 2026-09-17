-- A session ranking reads the hits and the misses of one session (D-023).
-- This index finds them without reading the rest of the session. The ranking
-- over everything reads about half the journal and scans it, which measured
-- faster than an index on kind alone (D-029).
CREATE INDEX events_session_kind ON events (session_id, kind);
