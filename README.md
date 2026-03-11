# localfiles-mcp

Minimal MCP server in Go that exposes a local filesystem directory through HTTP JSON-RPC, with strict path confinement and optional file filtering.

## Features

- `list_files` and `read_file` MCP tools
- Path traversal protection (`..` and absolute paths are rejected)
- Optional dotfile and regex-based exclusion
- `--exclude` alias for regex-based exclusion (combined with `--exclude-regex` when both are set)
- Bearer-token authentication
- Optional TLS support
- Optional Tailscale `serve` integration
- Foreground-first with live terminal dashboard (TUI)

## Build

```bash

go build ./...
```

## Run

The default workflow is designed for local use:

```bash
go run .
```

This starts the MCP endpoint in the foreground, serves the current directory (`.`), chooses an available local port automatically, auto-generates a token if needed, keeps a live dashboard until shutdown, and on macOS enables Apple sandboxing by default (restricting file access to the target root while inheriting Apple’s baseline runtime allowances).

You can always pass an explicit listen address if you want to pin it:

```bash
go run . /absolute/path/to/expose 127.0.0.1:8080
```

You can disable sandbox mode (for debugging or unsupported environments) with:

```bash
go run . /absolute/path/to/expose --sandbox=false
```

Or provide your own sandbox profile:

```bash
go run . /absolute/path/to/expose --sandbox-profile /path/to/profile.sb
```

You can also pass explicit flags if preferred:

```bash
go run . \
  --root /absolute/path/to/expose \
  --listen 127.0.0.1:8080 \
  --token "<long-random-secret>"
```

To exclude paths with one or two regex expressions:

```bash
go run . /absolute/path/to/expose --exclude='\\.(log|tmp)$'
go run . /absolute/path/to/expose --exclude='\\.git' --exclude-regex='node_modules'
```

The two patterns are combined with OR when both are provided.

If you want old-school output only:

```bash
go run . /absolute/path/to/expose --no-tui
```

### Tailscale exposure

Tailscale exposure is disabled by default and can be enabled explicitly:

`--tailscale-expose` configures mapping with:

- endpoint path: `/mcp`
- remote URL: `https://<tailscale-hostname>/mcp`

You can override defaults:

```bash
go run . /absolute/path/to/expose \
  --tailscale-path /mcp \
  --tailscale-port 443 \
  --tailscale-hostname my-machine.ts.net
```

To disable tailscale exposure:

```bash
go run . /absolute/path/to/expose --no-tailscale
```

### Environment variables

- `LOCALFILES_MCP_ROOT` (alternative to `--root`)
- `LOCALFILES_MCP_TOKEN` (alternative to `--token`)

## MCP endpoint

`POST /mcp` accepts JSON-RPC requests.

Supported methods:

- `initialize`
- `tools/list`
- `tools/call`
  - Tool `list_files`:
    - `path` (string, default `.`)
    - `recursive` (bool, default `false`)
  - Tool `read_file`:
    - `path` (string, required)
    - `max_bytes` (int, default from `--max-file-bytes`)

## ChatGPT setup instructions

1. Open the ChatGPT app.
2. Add a Custom MCP server with endpoint:

   - Remote URL: `https://<your-tailnet-hostname>/mcp`
   - Header: `Authorization: Bearer <token>`

3. Use tools `list_files` and `read_file`.

The token displayed at startup in the dashboard is generated if you don't provide one.

## Tailscale expose internals

The process calls `tailscale serve` with your configured values and removes that mapping when the process exits or if it crashes.

Current command pattern:

- `tailscale serve --https=<tailscale-port> --set-path <tailscale-path>=http://<listen><tailscale-path>`
- teardown: `tailscale serve --https=<tailscale-port> --set-path <tailscale-path>=off` and falls back to `tailscale serve reset` if needed

## Interactive mode

By default, the binary runs a terminal dashboard that shows:

- startup status and setup instructions (shown until the first request)
- local and remote endpoint information
- request throughput and error counters
- per-request stream view (method, tool, client, status, latency, details)
- unauthorized and parse/method errors with inline context
- arrow-key friendly, row-based request log with expandable detail rows

Disable the dashboard with `--no-tui` if you need plain logs.

## Testing

```bash
go test ./...
```
