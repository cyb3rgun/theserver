# theserver

theserver is the local venue server of the CYB3RGUN system.
It brings together everything that is not real time: players, sessions, scores, rankings, scenario content, devices, configuration and administration.
It is never in the hit path; every target decides, reacts and scores on its own and keeps playing when theserver is gone.
The same binary runs on a Raspberry Pi inside a target, on a Mini PC at home and on a rack machine in a venue hall.
One binary, no external runtime, no database service, no message broker: copy, start, done.

## Status

Season S01. theserver serves HTTPS, keeps its SQLite journal in the data directory, and speaks the device link of [docs/protocol.md](docs/protocol.md): targets connect over WebSocket, authenticate with a token, replay what the server has not acknowledged, and receive commands. `simtarget` is a simulated target that exercises the whole path without firmware. The ranking API and the first admin page follow in this season. See [docs/seasons.md](docs/seasons.md) and [docs/capabilities.md](docs/capabilities.md).

## Requirements

- Go at the version named by the `go` directive in [go.mod](go.mod)
- Git
- Python 3, for `tools/check_dashes.py`

## Build

Windows, from PowerShell or cmd:

```
go build -trimpath -ldflags "-s -w -X github.com/cyb3rgun/theserver/internal/version.Version=0.3.0-dev" -o dist/theserver.exe ./cmd/theserver
go build -trimpath -ldflags "-s -w -X github.com/cyb3rgun/theserver/internal/version.Version=0.3.0-dev" -o dist/simtarget.exe ./cmd/simtarget
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

`theserver` takes a subcommand; without one it serves.

| Command | What it does |
| --- | --- |
| `theserver serve` | runs the server; the default when no subcommand is given |
| `theserver serve --version` | prints version, commit, build date and Go version |
| `theserver serve --write-default-config <path>` | writes a configuration file with every setting and its default |
| `theserver device add --id <id> --kind target\|controller\|bridge [--class esp\|pi\|pc]` | registers an approved device and prints its token once |
| `theserver device list` | lists id, kind, class, status and last seen |
| `theserver device revoke --id <id>` | blocks a device; a running server drops its connection within a second |
| `theserver db info` | prints the database path, schema version, journal mode, foreign keys, busy timeout and row counts |

Every subcommand takes `--config <path>` and `--data-dir <dir>`. `--db-info` still works in S01 as a deprecated alias of `db info`.

A first run:

```
dist\theserver.exe serve --write-default-config data\theserver.toml
dist\theserver.exe device add --id tgt-01 --kind target --class esp
dist\theserver.exe serve --config data\theserver.toml
```

`device add` prints the token only once; the database keeps just its hash. Check that the server is up:

```
curl -k https://127.0.0.1:8443/healthz
```

The answer is `{"status":"ok","version":"...","db":"ok"}`, and 503 with `"db":"error"` when the database does not answer. Ctrl+C stops the server; open requests and device connections get 5 seconds, and every device connection stores what it received before it closes.

## TLS

theserver serves HTTPS only. On the first start without a configured certificate it creates a self signed one in `<data_dir>/tls/server.crt` and `server.key` (ECDSA P-256, ten years, for localhost, 127.0.0.1, ::1 and the host name) and logs its SHA-256 fingerprint on every start, in the line `tls certificate`. To use a real certificate, replace the two files, or name others with `tls.cert_file` and `tls.key_file`. Devices in S01 pin the fingerprint or skip verification explicitly; `curl -k` and `simtarget --insecure` do the latter.

## Simulated target

`simtarget` connects like a target, journals its events in `data/simtarget/<id>` before sending them, replays what the server has not acknowledged, and answers commands.

```
dist\simtarget.exe --server wss://127.0.0.1:8443 --id tgt-01 --token <token> --insecure --rate 5 --drop-every 10 --duration 60
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--server` | `wss://127.0.0.1:8443` | theserver address |
| `--id`, `--token` | none | device id and the token from `device add` |
| `--insecure` | off | skip TLS verification, for test servers only |
| `--rate` | `5` | events per second; every trigger pull is a shot followed by a hit (70 percent) or a miss |
| `--controllers` | `3` | controller ids to rotate |
| `--journal` | `data/simtarget/<id>` | where the journal lives |
| `--drop-every` | `0` | close the connection every so many seconds and reconnect after 2 seconds |
| `--duration` | `0` | stop generating after so many seconds, wait for the last acks and exit; 0 runs until Ctrl+C |

At the end it prints a summary: events generated, frames sent, replays, connections, drops, commands, the last ack and what is still unacknowledged. It exits 1 while events are unacknowledged and 3 when the server refuses the token.

## Configuration

Precedence, highest first: command line flags, environment variables, the TOML file named by `--config`, built in defaults. `--write-default-config <path>` writes a file with every setting, its default and a one line comment, and never overwrites an existing file.

| Setting | Default | Environment | Flag |
| --- | --- | --- | --- |
| `server.listen_addr` | `:8443` | `THESERVER_SERVER_LISTENADDR` | `--listen` |
| `server.data_dir` | `./data` | `THESERVER_SERVER_DATADIR` | `--data-dir` |
| `tls.cert_file` | empty, the certificate in `<data_dir>/tls` | `THESERVER_TLS_CERTFILE` | none |
| `tls.key_file` | empty, set together with `cert_file` | `THESERVER_TLS_KEYFILE` | none |
| `store.busy_timeout_ms` | `5000` | `THESERVER_STORE_BUSYTIMEOUTMS` | none |
| `link.ack_interval_ms` | `100` | `THESERVER_LINK_ACKINTERVALMS` | none |
| `link.ack_batch` | `32` | `THESERVER_LINK_ACKBATCH` | none |
| `link.ping_interval_s` | `15` | `THESERVER_LINK_PINGINTERVALS` | none |
| `link.pong_timeout_s` | `10` | `THESERVER_LINK_PONGTIMEOUTS` | none |
| `link.hello_timeout_s` | `5` | `THESERVER_LINK_HELLOTIMEOUTS` | none |
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
- [docs/capabilities.md](docs/capabilities.md): what theserver is meant to do, by area and phase
- [docs/protocol.md](docs/protocol.md): the device link protocol, version 1, with the server behaviour in section 8
- [docs/decisions.md](docs/decisions.md): numbered decisions with reasons and pinned versions
- [docs/seasons.md](docs/seasons.md): the S track
- `docs/briefings/` and `docs/handovers/`: one briefing and one handover per pass

## Legal Notice

theserver is part of CYB3RGUN and is not for persons under 18.
