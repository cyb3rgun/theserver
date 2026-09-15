# Decisions

Numbered newest first. Every entry names its date, the decision, the reason and the versions it pins, copied from `go.mod`. A version is never typed from memory: a dependency is added with `go get <module>@latest` and the version Go resolves is the one recorded here.

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
