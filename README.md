# theserver

theserver is the local venue server of the CYB3RGUN system.
It brings together everything that is not real time: players, sessions, scores, rankings, scenario content, devices, configuration and administration.
It is never in the hit path; every target decides, reacts and scores on its own and keeps playing when theserver is gone.
The same binary runs on a Raspberry Pi inside a target, on a Mini PC at home and on a rack machine in a venue hall.
One binary, no external runtime, no database service, no message broker: copy, start, done.

## Status

Season S01, foundation. The binary loads its layered configuration, logs with `log/slog`, opens its SQLite database in the data directory and serves a health endpoint over plain HTTP. The device link, the API and the admin page follow in this season. See [docs/seasons.md](docs/seasons.md).

## Requirements

- Go at the version named by the `go` directive in [go.mod](go.mod)
- Git
- Python 3, for `tools/check_dashes.py`

## Build

Windows, from PowerShell or cmd:

```
go build -trimpath -ldflags "-s -w -X github.com/cyb3rgun/theserver/internal/version.Version=0.1.0-dev" -o dist/theserver.exe ./cmd/theserver
```

Cross compile for Linux ARM64 (Raspberry Pi), from cmd:

```
set GOOS=linux& set GOARCH=arm64& go build -trimpath -ldflags "-s -w" -o dist/theserver-linux-arm64 ./cmd/theserver
```

The same from PowerShell:

```
$env:GOOS = "linux"; $env:GOARCH = "arm64"; go build -trimpath -ldflags "-s -w" -o dist/theserver-linux-arm64 ./cmd/theserver; Remove-Item Env:GOOS, Env:GOARCH
```

`Version`, `Commit` and `BuildDate` in `internal/version` are set with `-ldflags "-X ..."` and read `dev` otherwise. Build output goes to `dist/`, which is not tracked.

## Run

```
dist\theserver.exe --version
dist\theserver.exe --write-default-config data\theserver.toml
dist\theserver.exe --config data\theserver.toml --db-info
dist\theserver.exe --config data\theserver.toml
```

`--db-info` prints the database path, the schema version, the journal mode, the foreign key state, the busy timeout and the row count of every table, then exits.

Check that it is up:

```
curl http://127.0.0.1:8443/healthz
```

The answer is `{"status":"ok","version":"...","db":"ok"}`, and 503 with `"db":"error"` when the database does not answer. Ctrl+C shuts the server down and gives open requests 5 seconds to finish.

## Configuration

Precedence, highest first: command line flags, environment variables, the TOML file named by `--config`, built in defaults. `--write-default-config <path>` writes a file with every setting, its default and a one line comment, and never overwrites an existing file.

| Setting | Default | Environment | Flag |
| --- | --- | --- | --- |
| `server.listen_addr` | `:8443` | `THESERVER_SERVER_LISTENADDR` | `--listen` |
| `server.data_dir` | `./data` | `THESERVER_SERVER_DATADIR` | `--data-dir` |
| `store.busy_timeout_ms` | `5000` | `THESERVER_STORE_BUSYTIMEOUTMS` | none |
| `log.level` | `info` | `THESERVER_LOG_LEVEL` | `--log-level` |
| `log.format` | `text` | `THESERVER_LOG_FORMAT` | none |

## Development

```
go vet ./...
go test ./...
python tools/check_dashes.py
```

Commits follow Conventional Commits, `type(scope): description`, in English. The dash check runs before every commit.

## Documentation

- [docs/concept.md](docs/concept.md): what theserver is and the principles it keeps
- [docs/protocol.md](docs/protocol.md): the device link protocol, version 1
- [docs/decisions.md](docs/decisions.md): numbered decisions with reasons and pinned versions
- [docs/seasons.md](docs/seasons.md): the S track
- `docs/briefings/` and `docs/handovers/`: one briefing and one handover per pass

## Legal Notice

theserver is part of CYB3RGUN and is not for persons under 18.
