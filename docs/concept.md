# CYB3RGUN theserver: concept

Status: living reference. This document only grows; nothing is removed, superseded statements are marked as such with a date.
Version: 1, 15 September 2026.

## 1. What theserver is

theserver is the local venue server of the CYB3RGUN system. It is the single place where everything that is not real time comes together: players and members, sessions and matches, scores and rankings, scenario content, device inventory, configuration and administration.

It is never in the hit path. A target decides on its own that it was hit, reacts on its own, keeps score on its own and journals every event on its own. theserver collects, counts, coordinates and directs. If theserver disappears, every target keeps playing.

theserver is a role, not a machine. The same binary runs on a Raspberry Pi built into a single 25 x 25 cm target at home, on a Mini PC in a living room, and on a rack machine in a hall with 500 targets.

## 2. Non-negotiable principles

1. The hit reacts locally, the server directs, the cloud is optional.
2. The target and the game are the same program. It runs without a server (single player, local high score) and with a server (multiplayer, members, rankings, directed scenarios). Standalone mode is the boot state of a target, not a separate build.
3. No event is ever lost and no event is ever counted twice. Every event carries a device id, a monotonically increasing sequence number and a globally unique event id.
4. One binary, no external runtime, no external database service, no external message broker. Copy, start, done.
5. Configuration is exhaustive. Everything that could reasonably be a setting is a setting, with a documented default, and every setting is reachable from the admin interface.
6. The admin interface is a first class product surface, not an afterthought.
7. Only proven, current versions of the toolchain and every dependency. Versions are pinned, recorded in `docs/decisions.md`, and updated deliberately.
8. Security is designed in: device identities, signed content, signed updates, an SBOM and a vulnerability process, because the EU Cyber Resilience Act reporting duties apply to connected hardware products.

## 3. Language and stack

Go. Reasons: memory safe, a strict compatibility guarantee, a single static binary per platform, cross compilation to Linux x86-64, Linux ARM64 (Raspberry Pi) and Windows from one command, a mature standard library covering HTTP, TLS, WebSocket adjacent needs, and the founder's own Go experience.

Reverse engineering: Go binaries are stripped (`-s -w`, `-trimpath`) and may be obfuscated with `garble` for protected builds. This raises the effort, it does not make the code unreadable. Real protection lies in the model: the open source part is open, the paid part (content, hosting, Pro modules) is not shipped inside the binary but delivered as signed, encrypted content packages and hosted services.

Stack decisions for the foundation:

| Concern | Decision | Reason |
| --- | --- | --- |
| Toolchain | Go 1.26, latest patch release at the time of `go mod init`; upgrade to 1.27 as a deliberate decision once its first patch releases have landed | proven line; 1.27.0 shipped in August 2026 |
| HTTP and routing | `net/http` from the standard library (pattern routing since Go 1.22) | no framework dependency for the core |
| WebSocket | `github.com/coder/websocket` | pure Go, maintained, context aware |
| Storage | SQLite via `modernc.org/sqlite` | pure Go, no cgo, no external service; WAL mode; the appliance never pins an old SQLite because of the March 2026 WAL reset fix |
| Configuration | TOML file plus environment overrides plus command line flags, in that precedence order | comments in the file, human editable, machine writable by the admin UI |
| Logging | `log/slog` structured logging | standard library |
| Admin UI | server rendered `html/template` plus HTMX, no build step, no Node toolchain | one binary stays one binary |
| Message encoding on the device link | CBOR | compact, binary, cheap to encode on an ESP32 |

Every dependency version is recorded in `docs/decisions.md` when it is introduced. The PostgreSQL option for large venues is a later decision and is not part of the foundation.

## 4. Network model

### 4.1 Radio

Controllers (pistols) speak ESP-NOW only. A shot is broadcast; there are no fixed ESP-NOW peers and no ESP-NOW peer limit applies. Authenticity of a shot is carried inside the packet (device id, sequence, signature), not by ESP-NOW link encryption.

Targets speak ESP-NOW (to hear controllers) and hold a permanent WiFi station connection (to reach theserver) on the same radio chip. Both must be on the same fixed 2.4 GHz channel. The whole venue runs on one channel; automatic channel selection is disabled everywhere.

### 4.2 Infrastructure

Home: one router on a fixed 2.4 GHz channel, theserver on the same network (Raspberry Pi, Mini PC or built into the target).

Venue: UniFi access points, one dedicated SSID for the system (for example `CYB3RGUN-OPS`), all APs on the same fixed 20 MHz 2.4 GHz channel, 5 GHz disabled for that SSID, band steering off, minimum RSSI kick-off off, every AP wired to a PoE switch, theserver wired to the same switch, fixed addresses for targets and server. Room count and AP count scale independently of the software.

### 4.3 Capacity, on paper, to be measured

- Per target: every controller in the room. Planning figure: up to 10 simultaneous shooters per target.
- Per radio zone: about 40 simultaneously firing controllers at 5 shots per second each before channel collisions rise; raising the ESP-NOW data rate multiplies this.
- Per venue: sum of its radio zones; walls separate zones.

These figures supersede the earlier "25 players maximum" in `cyb3rgun-reference.md` section 1 and are replaced by measurements as soon as the first physical bench exists.

## 5. Device link

Each target opens one outbound connection to theserver at boot and keeps it open: WebSocket over TLS. The target presents a client certificate (device identity); theserver holds an allowlist that the operator approves from the admin UI on first contact.

Over that one connection, both directions, CBOR messages:

Up: events (`shot_received`, `hit`, `miss`, `session_event`, `state`, `health`), each with `device_id`, `seq`, `event_id`, `ts_device`.

Down: acknowledgements (`ack_through seq`), commands (`load_scenario`, `session_start`, `session_stop`, `assign_team`, `set_config`, `time_mark`), update notices.

Replay: on reconnect the target reports its last sequence, theserver answers with the last sequence it has stored, the target resends everything after it. theserver deduplicates on `event_id`. Nothing lost, nothing doubled.

Large content (scenario packages, media, firmware) never travels over the device link. Targets fetch it over HTTPS from theserver as resumable, checksummed, signed downloads.

Why not MQTT: it needs a broker as a separate moving part and has no native request and response. Why not HTTPS only: no server push without polling. Why not WebSocket only: a large download would queue in front of hit events.

## 6. API

One HTTPS API, versioned under `/api/v1`, JSON, token authenticated, used by the admin UI, the website, the UE5 game, operator tooling and later CIND3R3LLA. Every capability of theserver is reachable through the API; the admin UI uses nothing that the API does not offer. The API is documented as OpenAPI in the repo and served by the binary.

## 7. Data model, foundation

Added 23 September 2026 with S01-B13 (D-066): a venue has a shape, and everything that is counted, billed or directed later hangs from it.

- **Site**: the venue. Name, address, timezone, contact, notes, and the licence values the manufacturer sets: licence id, franchise rate, monthly threshold, currency, valid from (D-070). Those last five are stored and shown and nothing computes with them yet; they are there so the table does not have to be rebuilt when the money side arrives.
- **Room**: belongs to a site. Name, the age of its players, the WiFi channel of its access point, the beacon multiplex plan as a period in milliseconds and a number of slots, capacity, notes. A scenario rated above the age of the room cannot be played in it (D-067).
- **Target**: a device of kind target that stands in a room, with its target type (D-061), its slot in the beacon plan of that room and a free note about where it hangs. Two targets of one room never hold the same slot.
- **Controller**: a device of kind controller. It belongs to a site, and it may belong to one room of that site or be free for the whole site.
- A **session** runs in a room: the approved targets of the room become its devices when it is created, a single one can be left out before it starts, and the age of the room is the age of the session.

- Device: id, kind (target, controller, bridge), certificate fingerprint, name, site, room, slot, position, status, firmware version, config overrides.
- Event: event_id, device_id, seq, kind, payload, ts_device, ts_server.
- Session: id, scenario, room, targets, players, state, timestamps.
- Site and room as above; a device of an earlier version was moved into a site Default and a room named after the text it carried, so nothing lost its history (D-069).
- Score entries derived from events, never edited by hand.
- Player and member management, teams, rankings: later seasons, designed now so that the schema does not have to be rewritten.

All tables carry created and updated timestamps. Migrations are versioned and embedded in the binary.

## 8. Deployment

One binary per platform under `dist/`: `theserver-linux-amd64`, `theserver-linux-arm64`, `theserver-windows-amd64.exe`. First start creates the data directory, a default configuration file with every setting and its default written out as comments, the SQLite database, and a self signed certificate for the admin UI until the operator installs a real one. A systemd unit and a Windows service wrapper follow in a later season.

## 9. Seasons

Season S01, goal in one sentence: theserver runs as one binary on the M150, accepts events from a simulated target over the device link, journals them in SQLite, replays correctly after a dropped connection, and shows a ranking through the API and a first admin page.

Out of scope for S01: members, login, multiplayer coordination, scenario distribution, CIND3R3LLA.

Each season ends with a log and a handover in `docs/`. The founder decides when a season ends.

## 10. Open decisions

- Licence for theserver (the game is BSL 1.1 with a four year change date to GPL-3.0; theserver has no decision yet).
- Whether the PostgreSQL option is ever needed.
- The exact CBOR message schema, to be specified in `docs/protocol.md` before the simulated target is written.
- Findings from the parallel research chat, to be merged into this document.
