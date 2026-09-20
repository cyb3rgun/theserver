# Seasons

## Track S (Server)

| Season | Goal | Status | Start | Passes |
|--------|------|--------|-------|--------|
| S01 | theserver runs as one binary on the M150, accepts events from a simulated target over the device link, journals them in SQLite, replays correctly after a dropped connection, and shows a ranking through the API and a first admin page. | running | 15 September 2026 | B01 (D-001 to D-008), B02 (D-009 to D-015), B03 (D-016 to D-022), B04 (D-023 to D-028), B05 (D-029), B06 (D-030 to D-034), B07 (D-035 to D-040), B08 (D-041 to D-047), B09 (D-048 to D-051), B10 (D-052 to D-055) |

## S01 in progress

- **S01-B01.** Repository foundation: Go module, layered TOML configuration, entry point with a health endpoint and graceful shutdown, dash check, docs trio. The toolchain follows the newest stable Go release; handovers follow the THESITE format and get their own commit (D-001 to D-008).
- **S01-B02.** Storage: SQLite through modernc.org/sqlite with WAL, embedded migrations, the device registry and the idempotent event journal with a contiguous ack (D-009 to D-015).
- **S01-B03.** The device link: CBOR messages, WebSocket over TLS with a self signed certificate on first start, handshake with replay, commands and keepalive, device tokens from the command line, the sessions API, subcommands, and the simulated target simtarget (D-016 to D-022).

- **S01-B04.** Rankings computed from the journal, API v1 with admin tokens and an OpenAPI description, the first admin pages with embedded HTMX, and the device administration the link needs: approve, block, reset into a new sequence epoch, new token (D-023 to D-028).

- **S01-B05.** The devices page shows a device offline at once after a reset or a new token; migration 0004 indexes events by session and kind, and rankings read the journal unsorted (D-029); the season log, `docs/season-01-log.md`.

- **S01-B06.** The settings foundation: one registry that describes every setting with type, range, restart flag and texts in English and German; the admin interface in both languages; the configuration file written back from the registry with live changes where possible; the settings API and an editable settings page with help; the generated reference `docs/settings.md` (D-030 to D-034).

- **S01-B07.** Scenario packages and the catalogue: `docs/scenario.md` with its implementation `pkg/scenario`, which was `internal/scenario` until B09, the content store with immutable published versions, holdings and announcements on the device link, the scenario API with the package download for targets and the assignment with the age check, the catalogue pages, and the whole chain from upload to installed with the simulated target; a server without `--config` creates its configuration file with the first saved change (D-035 to D-040).

- **S01-B08.** The scenario editor for VIDEO and INTERACTIVE: drafts on the server with their media, the package assembled and published by the server, the canvas with the zones and their keyframes, the timeline, the property panel from a field registry with help at every field in both languages, the media check that reads containers and codecs from the header, and the preview that plays the rules of section 7 in the browser and compares them with the same engine in Go (D-041 to D-047).

- **S01-B09.** The forward fixes of B08 and the public surface: the keyframe control follows the tier, a draft is locked while somebody edits it and can be taken over, undo and redo in the browser with a version history on the server, and `pkg/protocol`, `pkg/scenario` and `pkg/journal` moved out of `internal/` and tagged `v0.1.0` for theclient (D-048 to D-051).

- **S01-B10.** The device journal on SQLite: `pkg/journal` keeps its events in `journal.db` in WAL mode with synchronous FULL, one transaction per append, and imports the CBOR journal of an earlier version once; the durability is tested against a process that never closes and against an image of database and write ahead log, and the simulator keeps its journal across a restart. Tagged `v0.2.0` (D-052 to D-055).

The run of the full chain on the M150 was dropped on 17 September 2026 by Sascha's decision. Linux binaries are built when a Linux target is due, not before.
