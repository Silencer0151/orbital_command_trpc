# Orbital Command

A sci-fi themed TCP server written in Go. Clients connect with `netcat` (or any plain-text TCP client) and issue text commands over a sandboxed command shell. Protocol is UTF-8, newline-delimited, and prioritizes "sci-fi interface" aesthetics over bandwidth.

Currently implements **protocol spec v1.9** — see [spec.txt](spec.txt) for the full specification.

## Features

- **Fleet ops** — `PING`, `REPORT` (OS/arch/time/uptime telemetry)
- **File ops** — `TOUCH`, `LIST`, `TREE`, `CAT`, `WRITE`, `WRITEML`, `APPEND` with CRC32 integrity checks on reads/multi-line writes
- **Comms** — `NICK`, `WHO`, `WHISPER`, `BROADCAST` between connected clients
- **Auth challenge** — optional puzzle gate on connect (`-c`) and on-demand via `CHALLENGE` (binary op, arithmetic, or ASCII sum)
- **Path sandbox** — all file operations are constrained to the server root directory
- **ANSI color output** with `-no-color` flag and `NO_COLOR` env support
- **MOTD** — optional `.motd` file displayed on connect (hot-reloadable)

## Build & Run

```bash
go build -o orbital-command .
./orbital-command
```

Default listen port is `:9078`.

### Flags

| Flag         | Description                                           |
|--------------|-------------------------------------------------------|
| `-n <NAME>`  | Node name shown in banner (default: random `WORD####`)|
| `-d <PATH>`  | Root directory for file sandbox (default: cwd)        |
| `-c`         | Require auth challenge on every new connection        |
| `-no-color`  | Disable ANSI escape codes                             |
| `-h`         | Print usage summary                                   |

Example:

```bash
./orbital-command -n VIPER -d /srv/orbital -c
```

## Connecting

```bash
nc localhost 9078
```

Then type `HELP` to see all commands.

## Testing

```bash
go test ./...
```

The suite contains 80+ tests covering every command, the auth challenge gate, path sandboxing, CRC32 integrity, and multi-client messaging. Tests run against a real TCP server on an ephemeral port using a `./testing/` fixture directory.

## Project Layout

- [main.go](main.go) — server, connection handler, command dispatch, color helpers
- [challenge.go](challenge.go) — auth challenge generators (binary op, arithmetic, ASCII sum)
- [server_test.go](server_test.go) — integration test suite
- [spec.txt](spec.txt) — protocol specification
- [testdir/](testdir/) — sample fixture files
