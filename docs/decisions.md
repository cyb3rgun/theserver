# Decisions

Numbered newest first. Every entry names its date, the decision, the reason and the versions it pins, copied from `go.mod`. A version is never typed from memory: a dependency is added with `go get <module>@latest` and the version Go resolves is the one recorded here.

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

## D-001 Go 1.26 toolchain

- Date: 15 September 2026 (S01-B01)
- Decision: theserver is built with Go 1.26, latest patch. The move to Go 1.27 is a recorded decision in a later pass, not a side effect.
- Reason: Proven line. Go 1.27.0 shipped in August 2026 and is not yet the pinned toolchain.
- Versions: `go 1.26.1` in `go.mod`, written by `go mod init` with the installed toolchain `go version go1.26.1 windows/amd64`. The go directive is never newer than the installed toolchain.
- Note: On 15 September 2026 the release list on go.dev named `go1.26.8` as the latest 1.26 patch and `go1.27.1` as the latest release. The installed toolchain is therefore not the latest 1.26 patch. Installing a newer toolchain and raising the go directive is open (S01-B01 handover).
