# Decisions

Numbered newest first. Every entry names its date, the decision, the reason and the versions it pins, copied from `go.mod`. A version is never typed from memory: a dependency is added with `go get <module>@latest` and the version Go resolves is the one recorded here.

## D-034 Help is part of the structure

- Date: 17 September 2026 (S01-B06)
- Decision: Every field of the settings page renders its label, a control that fits its kind (text, number with unit and range, switch, select), the current value, the value after a restart while one waits, the default, the range, the source, a short description that shows on hover and on keyboard focus, and the why in an element that expands. Each field has its own reset to the default; a save bar counts the unsaved changes; a banner lists the settings that wait for a restart. How all of it looks is the founder's, later.
- Details: The page is a plain form with post, redirect and get, so saving and resetting work without JavaScript. The count comes from `static/settings.js`, a small script of our own served with a subresource integrity hash like HTMX. Browser validation is switched off, so the reasons of a refused save come from the typed errors of the API, translated, next to each field. Settings that an environment variable or a flag sets are shown locked with the name that sets them.
- Reason: Operators have no programming knowledge. What a setting does and why it matters has to stand next to it, in their language, and the machine that renders it has to be the same for every later editor.
- Versions: none.

## D-033 Every settings change is logged

- Date: 17 September 2026 (S01-B06)
- Decision: Every change through the API or the admin page is logged as one structured line, `msg="setting changed"` with `action` (set or reset), `key`, `old`, `new`, `takes_effect` (now or after restart), `admin_token` and `admin_name`; the time is the time of the line. A value equal to the configured one is no change and is not logged. Values of sensitive settings, of which there are none yet, are written as hidden. The audit table that comes with roles reads the same event.
- Reason: Who changed what and when has to be answerable from the first editable setting on.
- Versions: none.

## D-032 Settings are written back to the configuration file

- Date: 17 September 2026 (S01-B06)
- Decision: A change on the admin page or through `PUT /api/v1/settings` is written to the TOML file the server was started with. `config.Write` regenerates the whole file from the registry: per setting its English label and description, default, range or values, environment variable, flag and restart note, the settings the file sets as values and all others commented out with their default. Tables of the file that are not theserver settings are kept with their values; comments written by hand are not. The file is replaced in one step.
- Details: `config.Runtime` applies changes all or none. The settings `log.level`, the five link timings, `admin.language` and `admin.session_hours` take effect at once: the log level through a level variable, the link timings through `link.Server.SetConfig` from the next batch, ping or connection on, the admin settings at the next page or login. `server.listen_addr`, `server.data_dir`, both TLS files, `store.busy_timeout_ms` and `log.format` take effect after a restart and are listed until then. A setting that an environment variable or a flag sets cannot be changed there, since the file would not win at the next start. Without a configuration file a change is refused with 409. A certificate without its key is refused before anything is written.
- Loading changes with it: unknown keys in a theserver section stay an error; tables that are not theserver sections are accepted and kept. For environment variables and flags only the value that wins is checked, as before, so a flag can stand in for an unusable variable.
- Changed in S01-B07, as briefed: a server started without `--config` no longer refuses a change with 409. The first change creates `theserver.toml` in the data directory that the defaults, the environment and the flags give, and logs `msg="configuration file created"` with its path; every later start without `--config` reads that file when it exists, every subcommand included. The file may name another data directory, which then holds the data, while the file stays where it was found. A file that appears there after the start is not overwritten: the change is refused with 409 `conflict` until a restart has read it.
- Reason: The admin page is the product; a setting changed there must survive a restart, and the file stays readable for someone who opens it.
- Versions: none.

## D-031 Two languages from the first form

- Date: 17 September 2026 (S01-B06)
- Decision: `internal/i18n` embeds `catalog/en.toml` and `catalog/de.toml`, read as flat keys. `T` formats a text in a language, falls back to English, renders a missing key as the key and logs a warning once. Every string of the admin pages, the notices, the login errors, the error messages of the API and the words for status, state, kind and source come from the catalogues; the texts of the settings come from the registry. The admin pages are parsed once per language.
- Details: The language of a request is the language cookie `theserver_lang`, else `admin.language`, else English. The switch in the page header sets the cookie for as long as a login lasts, returns only to admin paths, and logout clears it. German texts address the operator formally, use umlauts, and use ASCII apostrophes and hyphens only.
- Tests fail on a key missing in either catalogue, on different format verbs in a translation, on a key that a template or handler names and no catalogue holds, on loose text in a template, and on any page that asks for a missing text in either language.
- Reason: Operators work in their language from the first day; adding a language later must not mean finding strings in code.
- Versions: none. The catalogues are read with github.com/BurntSushi/toml v1.6.0, already required.

## D-030 One settings registry

- Date: 17 September 2026 (S01-B06)
- Decision: `internal/settings` declares every setting once: key, section, kind (string, int, bool, duration, enum, path, addr), default, unit, range or allowed values, whether it may be empty, whether a change needs a restart, whether it is sensitive, its serve flag, and label, description and why in English and German. The configuration loader, the configuration file, the API, the admin page and `docs/settings.md` all read it. `Parse` and `Validate` check a value per kind and answer with a typed `ValueError` whose code is one of `out_of_range`, `not_allowed`, `bad_address`, `bad_duration`, `bad_number`, `bad_bool`, `empty`, `bad_type` and `unknown_setting`.
- Details: The Config struct stays typed and is mapped onto the registry by its toml tags; a test fails when a field has no registry entry, an entry has no field, or the defaults differ. The environment variable of a setting is derived from its key, as before. A duration is a whole number of its unit, so the file keeps its numbers; text such as `2s` is accepted and converted when it is a whole number of the unit. Every number has a closed range: `store.busy_timeout_ms` 0 to 600000 ms, `link.ack_interval_ms` 1 to 10000 ms, `link.ack_batch` 1 to 1024, `link.ping_interval_s` 1 to 3600 s, `link.pong_timeout_s` and `link.hello_timeout_s` 1 to 600 s, `admin.session_hours` 1 to 720 h. New settings: `admin.language` (en or de, default en) and `admin.session_hours` (default 12). `docs/settings.md` is written by `theserver settings doc`, and a test fails when the committed file is stale.
- Reason: The admin interface is the product. Every later editor is built with this machine, and a setting described twice drifts.
- Versions: none.

## D-029 Rankings find a session by session and kind, and read unsorted

- Date: 17 September 2026 (S01-B05)
- Decision: Migration 0004 adds the index `events (session_id, kind)`. `EachEvent`, which a ranking reads through, no longer sorts; `ListEvents` keeps the order of arrival. The ranking over everything scans the journal.
- Reason: The briefing asked for indexes on `events (kind)` and `events (kind, controller_id)`. Measured on homelab (Intel Core i9-11900K) with modernc.org/sqlite and 270,000 events in 30 sessions, they made rankings slower: the planner took `events_kind` for a session ranking and read the hits of every session (22 ms became 118 ms), and the ranking over everything became slower too (538 ms to 715 ms), because hits and misses are about half the journal and an index lookup per row costs more than a scan. `events (kind, controller_id)` was used by no query. With both changes of this decision, the session ranking took 14 ms and the ranking over everything about 420 ms. A test pins the query plans.
- Agreed with the architect during this pass: `events (session_id, kind)` instead of the two briefed indexes, and no sort in `EachEvent`, which a sum does not need.
- Versions: none. SQLite 3.53.4 as built into modernc.org/sqlite v1.59.0.

## D-028 OpenAPI is written by hand

- Date: 17 September 2026 (S01-B04)
- Decision: API v1 is described in `docs/openapi.yaml`, OpenAPI 3.1, written by hand and served at `/api/v1/openapi.yaml` without a token. The binary embeds a copy in `internal/httpapi`; a test keeps the copy equal to the docs file, and a second test checks that every registered route is documented with its method and every documented operation is registered.
- Reason: The description is a product document, not generated noise, and the tests keep it from drifting away from the router.
- Refinement made during this pass: `GET /api/v1/settings` was added beside the briefed routes. The settings page needs it, and the concept asks for every setting to be reachable through the API.
- Versions: none.

## D-027 First admin page with html/template and HTMX

- Date: 17 September 2026 (S01-B04)
- Decision: The admin pages under `/admin` are server rendered with `html/template` and HTMX, embedded in the binary, with no build step. Login takes an admin token once and sets a signed session cookie for 12 hours (HttpOnly, Secure, SameSite Strict, path `/admin`), keyed by `<data_dir>/admin.key`. The pages carry one plain base stylesheet with system fonts; the visual design is the founder's and comes later.
- Reason: One binary stays one binary, and nothing on the page exists without a working API call behind it.
- Refinement agreed with the architect during this pass: the pages reach the API in process. Every page action becomes a request to the `/api/v1` handler, authorized by the admin token id behind the cookie, and the JSON answer is rendered as HTML. The API itself accepts only bearer tokens, so it has no cookie authentication and no CSRF surface; the admin pages are protected by `net/http` cross origin protection and a content security policy without inline script or style.
- Versions: HTMX 4.0.0, the newest official release on 17 September 2026 (published 28 August 2026), downloaded as the release asset `https://github.com/bigskysoftware/htmx/releases/download/v4.0.0/htmx-4.0.0-dist.zip`, 611,364 bytes, SHA-256 `858d5fb806ed3003704bc9c9a7fc7aad15213dcfc78c5b78b005fff4211ce57c`. Its `dist/htmx.min.js`, 36,716 bytes, is embedded unchanged as `internal/admin/static/htmx.min.js`, SHA-256 `e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f`, served with the subresource integrity hash `sha384-BvJpBiO8Kh31EqtJe5DRIeWrHWnCGkwytKs9NKFi86Hhw96dEqdEMzZDeK9iEGTc`. HTMX is under the Zero-Clause BSD licence, which asks for no attribution. A test checks the embedded file against the recorded SHA-256. It is never loaded from a CDN.

## D-026 A device reset is a new sequence epoch

- Date: 17 September 2026 (S01-B04)
- Decision: `devices.seq_epoch` starts at 1; events carry the epoch they were stored in, and the unique constraint is `(device_id, seq_epoch, seq)`. A reset, from `theserver device reset <id>`, the API or the admin page, moves the device to the next epoch: it starts at seq 1 again, and the events of earlier epochs stay. `LastSeq`, the contiguous ack and the handshake count within the current epoch. Migration 0003 rebuilds the events table for the new constraint and keeps every stored event in epoch 1.
- Reason: A device that lost its counter must be able to start over without deleting the journal, which is immutable (D-012).
- Refinement agreed with the architect during this pass: `welcome` carries the epoch under the key `ep` (protocol section 8.8). A reset closes the live connection with WebSocket status 1012. A device whose journal belongs to another epoch drops its unacknowledged events, starts at seq 1 in the new epoch and connects again at once, so the `last` of its next hello belongs to that epoch; simtarget does this. A connection writes only into the epoch it learned at its handshake, so events that were on their way during a reset never land in the new epoch.
- Agreed with the architect on 17 September 2026, after S01-B05: a reset drops the unacknowledged events of the old epoch, on the device and on the server. They are not renumbered into the new epoch.
- Versions: none.

## D-025 Admin API tokens

- Date: 17 September 2026 (S01-B04)
- Decision: `theserver admin token add --name <name>` prints an admin token once and stores its SHA-256 in `admin_tokens`. Every `/api/v1` request carries it as `Authorization: Bearer <token>`; none or an unknown one is answered 401. `admin token list` and `admin token revoke <id>` manage them. Passkeys and roles are S02; the table leaves room for a passkey credential beside a token.
- Reason: The API and the admin page need an operator identity now, before passkeys exist.
- Refinement agreed with the architect during this pass: `admin_tokens` has a `revoked_at` column. A revoked token keeps its row and is answered 403, which the briefing asks for; `last_used` is written at most once a minute.
- Versions: none. crypto/sha256 and crypto/subtle from the standard library.

## D-024 Only internal/scoring reads pts from payloads

- Date: 17 September 2026 (S01-B04)
- Decision: `internal/scoring` is the only package besides the device link that decodes event payloads, and it reads nothing but `pts`. The store stays payload agnostic, and the API hands payloads out base64 encoded as the device sent them.
- Reason: The payload is the device's record. Keeping its interpretation in one place keeps the rules of scoring in one place, where a later pass can recompute points under the loaded scenario.
- Versions: none.

## D-023 Rankings are computed, never stored

- Date: 17 September 2026 (S01-B04)
- Decision: A ranking is a query over the journal: hits and misses per controller, points as the sum of `pts` of the hits, scoped to one session or to everything. It is ordered by points, then hits, both descending, then controller id. Events without a controller are left out; a hit without `pts` counts as a hit worth nothing. There is no score table and no cache in S01.
- Reason: A stored score can disagree with the journal; a computed one cannot, and a replayed event is stored once, so it is counted once.
- Versions: none.

## D-022 theserver has subcommands

- Date: 16 September 2026 (S01-B03)
- Decision: The binary takes a subcommand. `serve` runs the server and is the default when none is given, so the usage of earlier passes keeps working. `device add`, `device list` and `device revoke` manage devices, and `db info` shows the state of the database. `--db-info` stays for this season as a deprecated alias of `db info`. Every subcommand reads the same layered configuration, so `--config` and `--data-dir` mean the same everywhere.
- Reason: Operator tasks that must work before the admin UI exists, above all issuing device tokens, need a home that is not a growing list of flags on the server.
- Versions: none. flag from the standard library.

## D-021 Sessions get a Go API

- Date: 16 September 2026 (S01-B03)
- Decision: The store offers CreateSession, StartSession, StopSession, AddSessionDevice, GetSession and ListSessions, plus RunningSessionFor for the device link. A session moves only forward, created to running to stopped; any other move is ErrBadTransition. welcome carries the running session of the device.
- Reason: Closes the gap from the S01-B02 handover: events referenced sessions that only raw SQL could create, and welcome needs the active session.
- Refinement agreed with the architect during this pass: an event without a session from the caller belongs to the session of its device whose started_at and ended_at cover its ts_device, start inclusive and end exclusive, the latest started if several do. A replayed event therefore lands in the session it happened in. This relies on time_mark keeping device clocks close to the server.
- Versions: none.

## D-020 Handlers leave main

- Date: 16 September 2026 (S01-B03)
- Decision: internal/httpapi owns the router, GET /healthz and the mount of the device link at /link/v1. cmd/theserver only wires configuration, store, certificate, link and lifecycle.
- Reason: Handlers in main could not be tested; the 503 path of the health endpoint, flagged in the S01-B02 handover, now has tests against a failing database and a closed store.
- Versions: none.

## D-019 Device tokens are issued from the command line in S01

- Date: 16 September 2026 (S01-B03)
- Decision: `theserver device add --id <id> --kind <kind> --class <class>` draws a token of 32 random bytes, stores only its SHA-256 hash, sets the device to approved and prints the token once with a warning that it cannot be shown again. Issuing tokens from the admin UI is B04 or later.
- Reason: The device link needs authenticated devices now, and the admin UI does not exist yet.
- Refinement agreed with the architect during this pass: the server finds the device of a bearer token through the hash of the token, before the upgrade, and the hello must then name that device, else err unauthorized. Migration 0002 adds a unique index on devices.token_hash, so no two devices share a token. The protocol keeps its single Authorization header.
- Versions: none. crypto/rand and crypto/sha256 from the standard library.

## D-018 TLS from the first start

- Date: 16 September 2026 (S01-B03)
- Decision: theserver serves HTTPS only. When `tls.cert_file` and `tls.key_file` are empty and `<data_dir>/tls/server.crt` and `server.key` do not exist, it creates a self signed certificate with the standard library: ECDSA P-256, valid for ten years, subject alternative names localhost, 127.0.0.1, ::1 and the host name. It logs the SHA-256 fingerprint on every start. Operators install a real certificate by replacing the two files or by naming others in the configuration. Devices in S01 pin the fingerprint or skip verification with an explicit flag; simtarget does the latter with `--insecure`.
- Reason: The device link carries bearer tokens, so it must never run in the clear, and a first start has to work without an operator preparing certificates.
- Versions: none. crypto/ecdsa, crypto/x509 and crypto/tls from the standard library.

## D-017 WebSocket through github.com/coder/websocket

- Date: 16 September 2026 (S01-B03)
- Decision: The device link uses github.com/coder/websocket, on the server through a net/http handler and in simtarget as the client.
- Reason: Pure Go, context aware, maintained, and it serves from a plain net/http handler.
- Refinement agreed with the architect during this pass: a device has at most one connection. A newer connection of the same device replaces the older one, which is closed without a close handshake after it has flushed what it received, so a device that lost its link gets back in without waiting for the ping timeout.
- Versions: `github.com/coder/websocket v1.8.15`, resolved with `go get github.com/coder/websocket@latest` on 16 September 2026. `go mod tidy` removed it once while nothing imported it; it was required again at exactly this version.

## D-016 CBOR through github.com/fxamacker/cbor/v2

- Date: 16 September 2026 (S01-B03)
- Decision: The messages of the device link are encoded with github.com/fxamacker/cbor/v2, written in Core Deterministic encoding (RFC 8949 section 4.2.1) and decoded with duplicate map keys refused and unknown keys ignored. An event id is a byte string of exactly 16 bytes; any other length is refused rather than padded or cut.
- Reason: Pure Go, RFC 8949, deterministic encoding, the de facto standard in Go; the same wire format is cheap to produce with tinycbor on an ESP32.
- Refinement agreed with the architect during this pass: the command id of cmd and res is an unsigned integer, counted per connection from 1.
- Versions: `github.com/fxamacker/cbor/v2 v2.9.4`, resolved with `go get github.com/fxamacker/cbor/v2@latest` on 16 September 2026, with the indirect module `github.com/x448/float16 v0.8.4`.

## D-015 Device tokens carry S01 authentication, stored as hashes

- Date: 16 September 2026 (S01-B02)
- Decision: A device authenticates in S01 with a token that the operator issues per device. The database keeps only its SHA-256 hash in devices.token_hash, never the token. Mutual TLS with device certificates replaces this in S02, without a change to the message schema.
- Reason: A stolen database must not hand out working device credentials. The column and the hashing helper exist now; issuing tokens is the admin work of B04.
- Versions: none. crypto/sha256 and crypto/subtle from the standard library.

## D-014 Timestamps are integer unix milliseconds in UTC

- Date: 16 September 2026 (S01-B02)
- Decision: Every timestamp in the database is an INTEGER holding unix milliseconds in UTC. No SQLite datetime strings anywhere.
- Reason: One representation that sorts, compares and subtracts without parsing, that an ESP32 can produce, and that carries no time zone to get wrong. The protocol already speaks unix milliseconds.
- Versions: none.

## D-013 Event ids are version 4 UUIDs, stored as 16 byte BLOBs

- Date: 16 September 2026 (S01-B02)
- Decision: An event id is a 16 byte UUID in a BLOB column, generated on the device. The store never invents one. The Go side builds ids with crypto/rand in the RFC 9562 version 4 layout and takes no uuid dependency.
- Reason: The device is the only place that knows which event this is, so the id has to travel with the event for the journal to be idempotent. Sixteen raw bytes are half the size of the text form and index as one value. The standard library covers the generation, so nothing is added to the dependency list for it.
- Versions: none of our own. github.com/google/uuid arrives as an indirect module of modernc.org/sqlite and is not used by theserver.

## D-012 Events are immutable

- Date: 16 September 2026 (S01-B02)
- Decision: The journal inserts and never updates or deletes in S01. Idempotency rests on the primary key on event_id, and a unique index on (device_id, seq) keeps a sequence number unique per device.
- Reason: An event is what a target reported at a moment; correcting it later would make the journal an opinion. Replay after a dropped connection then costs nothing: the same event id arrives, the row is already there, and nothing is written twice.
- Refinements agreed with the architect during this pass:
  - controller_id is filled by the caller, which hands the store the value it already decoded, rather than by the store decoding CBOR payloads. This pass therefore takes no CBOR dependency; the device link fills the column in B03.
  - A sequence number that another event holds is ErrSeqConflict, and an event id already stored under another device or sequence number is ErrEventConflict. Either rolls the whole batch back, so a device that contradicts itself never leaves half a batch behind.
- Versions: none.

## D-011 WAL, synchronous NORMAL, foreign keys on, busy timeout on every connection

- Date: 16 September 2026 (S01-B02)
- Decision: Every connection runs with journal_mode WAL, synchronous NORMAL, foreign_keys ON and a busy timeout, set through the DSN so that no connection of the pool can miss them. The timeout is the setting store.busy_timeout_ms, default 5000.
- Reason: WAL lets readers work while a writer holds the database, which the admin UI will need beside the event stream. synchronous NORMAL is the WAL companion that keeps commits cheap without risking the database on a crash. Foreign keys are off by default in SQLite, and the schema leans on them. The busy timeout turns a lock collision into a wait instead of an error.
- Versions: none.

## D-010 Own migration runner, no library

- Date: 16 September 2026 (S01-B02)
- Decision: Numbered SQL files under internal/store/migrations are embedded with embed, applied in ascending order, each in one transaction together with its row in schema_migrations. No migration library.
- Reason: Twenty lines of Go cover what this project needs, and they are readable in one sitting. A dependency here would have to be trusted with the schema of the journal, and it would ship in the one binary.
- Versions: none. embed from the standard library.

## D-009 SQLite through modernc.org/sqlite

- Date: 16 September 2026 (S01-B02)
- Decision: The database is SQLite, reached through the pure Go driver modernc.org/sqlite.
- Reason: No cgo, so no C toolchain on Windows or on the Raspberry, and cross compilation stays one command. No external database service, so one binary stays one binary (concept, principle 4).
- Versions: modernc.org/sqlite v1.59.0, resolved with go get modernc.org/sqlite@latest on 16 September 2026. It brings nine indirect modules, among them modernc.org/libc v1.75.7, modernc.org/memory v1.12.1, modernc.org/mathutil v1.7.1, golang.org/x/sys v0.47.0, github.com/dustin/go-humanize v1.0.1, github.com/google/uuid v1.6.0, github.com/mattn/go-isatty v0.0.24, github.com/ncruces/go-strftime v1.0.0 and github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec.

## D-008 Every pass ends with a separate docs(handover) commit

- Date: 15 September 2026 (S01-B01)
- Decision: Every pass ends with a separate `docs(handover): add <pass> handover` commit that adds `docs/handovers/<pass>.md`. The six-commit rule from S01-B01 is superseded.
- Reason: The handover reports what the pass proved, including build and run results that only exist after the last code or docs commit. A commit of its own lets the handover describe the finished state and still land in the history, instead of staying untracked or forcing a commit count that cannot hold it.
- Versions: none.

## D-007 Handover format follows THESITE season handover format

- Date: 15 September 2026 (S01-B01)
- Decision: A handover follows the shape of `THESITE/docs/season-02-handover.md`: a short state paragraph, then "Read this first", "The commands", "Where things are", "Traps that survived", "Not done, open" and "What comes next", plus any section a pass needs on top, such as choices to confirm.
- Reason: No game track handover exists on disk or in the THEGAME history to copy. THESITE's season handover is the proven format in the CYB3RGUN projects and lets the next session continue without reading the log.
- Versions: none.

## D-006 No licence file yet

- Date: 15 September 2026 (S01-B01)
- Decision: The repository carries no LICENSE file, and the README makes no licence statement.
- Reason: The licence for theserver is an open decision (concept, section 10). A file added now would claim a licence that has not been chosen.
- Versions: none.

## D-005 The docs trio, briefings and handovers live in docs/

- Date: 15 September 2026 (S01-B01)
- Decision: `docs/concept.md` grows only, `docs/decisions.md` is numbered newest first, `docs/seasons.md` holds the S track table. Briefings go to `docs/briefings/`, handovers to `docs/handovers/`.
- Reason: One fixed place for the reference, the reasons and the season state, in the same shape as the game track, so a new session finds them without being told.
- Versions: none.

## D-004 Layout by responsibility

- Date: 15 September 2026 (S01-B01)
- Decision: `cmd/theserver` is the only `main` package. Everything else lives under `internal/`, starting with `internal/config` and `internal/version`.
- Reason: Nothing leaks as a public Go API by accident; a package is made importable from outside only by a later, recorded decision.
- Versions: none. Module path `github.com/cyb3rgun/theserver`.

## D-003 TOML configuration with layered overrides

- Date: 15 September 2026 (S01-B01)
- Decision: Configuration is a TOML file read with `github.com/BurntSushi/toml`. Precedence, highest first: command line flags, environment variables prefixed `THESERVER_`, the file, built in defaults. `--write-default-config` writes every setting with its default and a one line comment.
- Reason: Comments in the file, human editable, and machine writable later by the admin UI.
- Versions: `github.com/BurntSushi/toml v1.6.0`, resolved by `go get github.com/BurntSushi/toml@latest` on 15 September 2026.

## D-002 Standard library first

- Date: 15 September 2026 (S01-B01)
- Decision: `net/http` for HTTP, `log/slog` for logging, `flag` for the command line. No web framework.
- Reason: No framework dependency for the core; pattern routing has been part of `net/http` since Go 1.22.
- Versions: standard library of the Go toolchain in D-001, no module.

## D-001 theserver always uses the newest stable Go release

- Date: 15 September 2026 (S01-B01, revised at the close of the pass)
- Decision: theserver always uses the newest stable Go release; toolchain resolved via GOTOOLCHAIN=auto. The go directive in `go.mod` names that release and is raised when a newer stable release ships.
- Reason: Patch releases carry the security and bug fixes, and a connected product has to ship them (concept, principle 8). With GOTOOLCHAIN=auto the go directive is the single statement of the toolchain: every machine builds with exactly that release, whatever Go is installed locally, and the go command fetches it on first use. The first version of this decision, Go 1.26 at its latest patch, had already fallen seven patches behind when the module was created.
- Versions: `go 1.27.1` in `go.mod`, released 1 September 2026 and the newest stable release on go.dev on 15 September 2026. `go version` in the module reports `go version go1.27.1 windows/amd64`, fetched by GOTOOLCHAIN=auto; the installed toolchain is `go1.27.0`.
- Supersedes: the first version of D-001, "Go 1.26, latest patch", with `go 1.26.1` in `go.mod` (commit `f70550b`).
