# Seasons

## Track S (Server)

| Season | Goal | Status | Start | Passes |
|--------|------|--------|-------|--------|
| S01 | theserver runs as one binary on the M150, accepts events from a simulated target over the device link, journals them in SQLite, replays correctly after a dropped connection, and shows a ranking through the API and a first admin page. | running | 15 September 2026 | B01 (D-001 to D-008), B02 (D-009 to D-015), B03 (D-016 to D-022), B04 (D-023 to D-028), B05 (D-029) |

## S01 in progress

- **S01-B01.** Repository foundation: Go module, layered TOML configuration, entry point with a health endpoint and graceful shutdown, dash check, docs trio. The toolchain follows the newest stable Go release; handovers follow the THESITE format and get their own commit (D-001 to D-008).
- **S01-B02.** Storage: SQLite through modernc.org/sqlite with WAL, embedded migrations, the device registry and the idempotent event journal with a contiguous ack (D-009 to D-015).
- **S01-B03.** The device link: CBOR messages, WebSocket over TLS with a self signed certificate on first start, handshake with replay, commands and keepalive, device tokens from the command line, the sessions API, subcommands, and the simulated target simtarget (D-016 to D-022).

- **S01-B04.** Rankings computed from the journal, API v1 with admin tokens and an OpenAPI description, the first admin pages with embedded HTMX, and the device administration the link needs: approve, block, reset into a new sequence epoch, new token (D-023 to D-028).

- **S01-B05.** The devices page shows a device offline at once after a reset or a new token; migration 0004 indexes events by session and kind, and rankings read the journal unsorted (D-029); the season log, `docs/season-01-log.md`.

Missing for the season goal: the founder's run of the full chain on the M150.
