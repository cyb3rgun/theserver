# Season S01 log

**Goal:** theserver runs as one binary on the M150, accepts events from a simulated target over the
device link, journals them in SQLite, replays correctly after a dropped connection, and shows a
ranking through the API and a first admin page. **Dates:** 15 to 17 September 2026. **Result:**
every part of the goal is built and was proven end to end with simulated targets, on the
development machine `homelab`. The run on the M150 was dropped by Sascha's decision on
17 September 2026.

Five briefings: S01-B01 to S01-B05. Decision records D-001 to D-029. `main` went from `473b55a` to
the S01-B05 handover in 34 commits, pushed to `github.com/cyb3rgun/theserver` at the end of every
pass from B01 on. Every measurement below comes from `homelab`: Intel Core i9-11900K, 16 logical
processors, 128 GB, Windows 11 Pro. Whether that machine is the M150 has not been confirmed.

---

## S01-B01: the foundation (15 September)

A Go module with one dependency, `github.com/BurntSushi/toml` v1.6.0; layered configuration with
defaults, a TOML file, environment variables and flags; an entry point with `GET /healthz` and a
graceful shutdown of 5 seconds; `tools/check_dashes.py`; the docs trio of concept, decisions and
seasons. Eight commits, the handover last.

The pass started on go1.26.1. Sascha installed go1.27.0 through winget, and D-001 was revised at
the close of the pass: theserver always follows the newest stable Go release, then 1.27.1,
fetched through `GOTOOLCHAIN=auto`. The binary was built and run on 1.26.1 only; after the move
just vet and tests ran again.

What went wrong: writing the escape for U+2014 through the editor tool put the literal dash into
the dash check itself, which now builds both characters with `chr()`. Processes started from the
agent shell ignore Ctrl+C until the parent calls `SetConsoleCtrlHandler(NULL, FALSE)`; the first
shutdown test had to be killed. The first push failed until the collaborator invitation was
accepted.

## S01-B02: storage (16 September)

SQLite through `modernc.org/sqlite` v1.59.0 with WAL, synchronous NORMAL, foreign keys and a busy
timeout on every connection of the pool (D-009, D-011); an own migration runner over embedded
files (D-010); the device registry with hashed tokens (D-015); the event journal. An event id that
is already stored is a duplicate and is never written twice; a seq held by another event and an
id stored elsewhere are two distinct conflicts that roll the batch back; the ack is the highest
seq with no gap below it. The schema of the briefing went in byte for byte. Seven commits.

Settled with the architect during the pass: the caller supplies `controller_id`, and the store
decodes no payload, so B02 took no CBOR dependency.

What went wrong: `\n` in Go strings written through a bash heredoc arrived as real newlines twice.
The handover called the append measurement one "on the M150" without checking; the B03 handover
corrected that.

## S01-B03: the device link (16 and 17 September)

`fxamacker/cbor/v2` v2.9.4 and `coder/websocket` v1.8.15 (D-016, D-017). The message types and
codec of `docs/protocol.md`; a self signed certificate on first start, so theserver serves HTTPS
only (D-018); the link at `/link/v1` with bearer tokens, handshake, replay, batched storage with
contiguous acks, commands, keepalive, newest connection wins and revocation within 500 ms; the
sessions API with attribution by device time (D-021); handlers out of main (D-020); subcommands
with `device add`, `list`, `revoke` and `db info` (D-019, D-022); `simtarget`, a simulated target
with its own journal. `docs/capabilities.md` arrived during the pass; section 8 of the protocol
now states what the server does. Nine commits.

Settled with the architect: the device is found through the hash of its token, command ids are
unsigned integers, sessions are attributed by device time, the newest connection wins.

What went wrong: `go mod tidy` dropped `coder/websocket` between the dependency commit and the
first import; it was required again at the version resolved earlier, not at a new `@latest`.
coder/websocket closes a connection when a read context expires, and its `Close` waits up to 5
seconds for the peer, so test clients have to keep reading while the server closes. The
briefing asked for ten commits; the pass has nine.

## S01-B04: rankings, API v1 and the first admin page (17 September)

Rankings computed from the journal and never stored (D-023), with `internal/scoring` as the only
reader of `pts` (D-024); admin tokens with 401 for unknown and 403 for revoked (D-025); a device
reset as a new sequence epoch, announced in `welcome` under `ep` (D-026); API v1 under `/api/v1`
with a hand written OpenAPI description that two tests hold to the router (D-028); admin pages
for devices, sessions, ranking and settings, with `html/template` and HTMX 4.0.0 embedded,
checked against its SHA-256 (D-027). Six commits.

Settled with the architect: `welcome` carries the epoch, `admin_tokens` keeps revoked rows, the
pages call the API in process, HTMX 4.0.0 from the official release.

The proof: three simulators, drops every 15 seconds, a reset and a new token from the page while
the devices were connected. An independent Python check that decodes the CBOR payloads itself
matched the API and the page exactly, and 301 stored events sent a second time were all
recognised as duplicates.

What went wrong: `GET /api/v1/settings` had to be added beside the briefed routes. A reset drops
the unacknowledged events of the old epoch, 11 in the proof; the architect confirmed that choice
after B05 (D-026). Right after a reset the devices page still showed the device online with the ack
of its old epoch. The screenshots were taken with a signed session cookie instead of typing a
token into the login form.

## S01-B05: status fix, index, this log (17 September)

The pass arrived as a correction that dropped the Linux binaries and the M150 proof document.
No original briefing was on disk, so the correction itself is stored as `docs/briefings/S01-B05.md`.
After the pass Sascha dropped the run on the M150; Linux binaries are built when a Linux target is
due, not before.

The status fix: the link now marks a connection as leaving before `Disconnect` returns, so
`Online` and `SendCommand` treat the device as gone at once, and the answer to a new token carries
the devices table as an htmx partial. A link test and an admin test with a live device hold the
close handshake back while they check; both were red against the old code. A headless browser
confirmed the partial swap with HTMX 4.

The index: the briefing named `events (kind)` and `events (kind, controller_id)`. Measured with
270,000 events in 30 sessions, the planner then took `events_kind` for a session ranking and read
the hits of every session, 22 ms became 118 ms, and the ranking over everything went from 538 to
715 ms, because hits and misses are half the journal. Settled with the architect: `events
(session_id, kind)` instead, together with reading the ranking unsorted (D-029). A test pins the
plans. Migration 0004 applied cleanly to a copy of the B04 proof database, and the new server gave
the same ranking on it.

What went wrong: a heredoc turned `\\s` into `\s` in a browser script, and a Windows path in a
Python heredoc turned `\b` into a backspace, caught by an assertion before anything was written.
`rm -rf dist/rankbench` in Git Bash also removed `dist/rankbench.exe`, which it takes for the
same name. The game track has no season log to copy, so this file follows THESITE's season 2 log.

## Decisions

| | Decision | Pass |
| --- | --- | --- |
| D-001 | theserver always uses the newest stable Go release | B01 |
| D-002 | Standard library first | B01 |
| D-003 | TOML configuration with layered overrides | B01 |
| D-004 | Layout by responsibility | B01 |
| D-005 | The docs trio, briefings and handovers live in `docs/` | B01 |
| D-006 | No licence file yet | B01 |
| D-007 | Handover format follows THESITE season handover format | B01 |
| D-008 | Every pass ends with a separate `docs(handover)` commit | B01 |
| D-009 | SQLite through `modernc.org/sqlite` | B02 |
| D-010 | Own migration runner, no library | B02 |
| D-011 | WAL, synchronous NORMAL, foreign keys on, busy timeout on every connection | B02 |
| D-012 | Events are immutable | B02 |
| D-013 | Event ids are version 4 UUIDs, stored as 16 byte BLOBs | B02 |
| D-014 | Timestamps are integer unix milliseconds in UTC | B02 |
| D-015 | Device tokens carry S01 authentication, stored as hashes | B02 |
| D-016 | CBOR through `github.com/fxamacker/cbor/v2` | B03 |
| D-017 | WebSocket through `github.com/coder/websocket` | B03 |
| D-018 | TLS from the first start | B03 |
| D-019 | Device tokens are issued from the command line in S01 | B03 |
| D-020 | Handlers leave main | B03 |
| D-021 | Sessions get a Go API | B03 |
| D-022 | theserver has subcommands | B03 |
| D-023 | Rankings are computed, never stored | B04 |
| D-024 | Only `internal/scoring` reads `pts` from payloads | B04 |
| D-025 | Admin API tokens | B04 |
| D-026 | A device reset is a new sequence epoch | B04 |
| D-027 | First admin page with `html/template` and HTMX | B04 |
| D-028 | OpenAPI is written by hand | B04 |
| D-029 | Rankings find a session by session and kind, and read unsorted | B05 |

## Numbers

All measured on `homelab` (Intel Core i9-11900K, 16 logical processors, 128 GB, Windows 11 Pro),
go1.27.1 unless noted.

| | |
| --- | --- |
| Append of 10,000 events in one transaction (B02) | 169 ms, about 59,000 events per second |
| Single simulator, 60 s, a drop every 10 s (B03) | 302 of 302 events stored, 5 replays, 0 duplicates |
| 50 simulators at 5 events per second for 60 s (B03) | 15,064 of 15,064 stored, 251 events per second |
| Server during that run (B03) | 33.8 percent of one core, working set 35.9 MB at the end |
| Revocation with a live connection (B03) | connection closed 449 ms after `device revoke` |
| Three simulators, 60 s, drops every 15 s, reset and new token (B04) | 801 events, ranking equal to an independent check, 13,500 points |
| Reset and new token from the page (B04) | connection closed within 1 ms |
| 301 stored events sent again (B04) | 301 duplicates, 0 stored, ranking unchanged |
| Ranking of one session, 270,000 events in 30 sessions (B05) | 22 ms in B04, 118 ms with the briefed indexes, 14 ms now |
| Ranking over everything, same journal (B05) | 538 ms in B04, 715 ms with the briefed indexes, about 420 ms now |

| | B01 | B02 | B03 | B04 | B05 |
| --- | --- | --- | --- | --- | --- |
| commits | 8 | 7 | 9 | 6 | 4 |
| test functions | 6 | 32 | 90 | 136 | 140 |
| `theserver.exe` bytes | 6,578,176 (go1.26.1) | 11,389,952 | 12,774,912 | 15,207,424 | 15,208,448 |
| schema version | none | 1 | 2 | 3 | 4 |

The query plans of a ranking now, SQLite 3.53.4 inside `modernc.org/sqlite` v1.59.0:

```
session hits:   SEARCH events USING INDEX events_session_kind (session_id=? AND kind=?)
session misses: SEARCH events USING INDEX events_session_kind (session_id=? AND kind=?)
all hits:       SCAN events
all misses:     SCAN events
```

## Open

- **simtarget treats the 1012 close of a reset as an ordinary connection end**, with a WARN line
  and its usual 2 second pause.
- **The ranking over everything scans the journal**, about 420 ms at 270,000 events. It grows with
  the journal; nothing prunes it, and retention is a later decision.
- **No admin token management on the page**, no passkeys, no roles (S02).
- **No fingerprint pinning** in simtarget, and no firmware has talked to the link.
- **`docs/concept.md` section 3** still names Go 1.26; the concept is its author's.
