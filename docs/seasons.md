# Seasons

## Track S (Server)

| Season | Goal | Status | Start |
|--------|------|--------|-------|
| S01 | theserver runs as one binary on the M150, accepts events from a simulated target over the device link, journals them in SQLite, replays correctly after a dropped connection, and shows a ranking through the API and a first admin page. | running | 15 September 2026 |

## S01 in progress

- **S01-B01.** Repository foundation: Go module, layered TOML configuration, entry point with a health endpoint and graceful shutdown, dash check, docs trio (D-001 to D-006). No device link, database or admin page yet.
