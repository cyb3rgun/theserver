# Seasons

## Track S (Server)

| Season | Goal | Status | Start | Passes |
|--------|------|--------|-------|--------|
| S01 | theserver runs as one binary on the M150, accepts events from a simulated target over the device link, journals them in SQLite, replays correctly after a dropped connection, and shows a ranking through the API and a first admin page. | running | 15 September 2026 | B01 (D-001 to D-008), B02 (D-009 to D-015), B03 (D-016 to D-022) |

## S01 in progress

- **S01-B01.** Repository foundation: Go module, layered TOML configuration, entry point with a health endpoint and graceful shutdown, dash check, docs trio. The toolchain follows the newest stable Go release; handovers follow the THESITE format and get their own commit (D-001 to D-008).
- **S01-B02.** Storage: SQLite through modernc.org/sqlite with WAL, embedded migrations, the device registry and the idempotent event journal with a contiguous ack (D-009 to D-015).
- **S01-B03.** The device link: CBOR messages, WebSocket over TLS with a self signed certificate on first start, handshake with replay, commands and keepalive, device tokens from the command line, the sessions API, subcommands, and the simulated target simtarget (D-016 to D-022).

Missing for the season goal: the ranking API and the first admin page (B04).
