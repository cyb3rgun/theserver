-- The age a device is set for (D-039): a scenario rated above it cannot be
-- assigned to a session the device is in. Until rooms carry the rating, the
-- device carries it; 18 lets every rating through.
ALTER TABLE devices ADD COLUMN min_age INTEGER NOT NULL DEFAULT 18;

-- The scenario version a session plays: scenario holds its id, and
-- scenario_version is 0 while no published version is assigned.
ALTER TABLE sessions ADD COLUMN scenario_version INTEGER NOT NULL DEFAULT 0;
