# Decisions

Numbered newest first. Every entry names its date, the decision, the reason and the versions it pins, copied from `go.mod`. A version is never typed from memory: a dependency is added with `go get <module>@latest` and the version Go resolves is the one recorded here.

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
