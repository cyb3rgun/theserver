-- A device token identifies its device: the link looks the device up by the
-- hash of the bearer token, so no two devices may share one.
CREATE UNIQUE INDEX devices_token_hash ON devices (token_hash);
