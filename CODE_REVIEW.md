# Open Findings

## 1. High: Root confinement is bypassable via symlinks

- Affected code: `filemanager.go:106`, `filemanager.go:203`, `filemanager.go:688`
- Problem: path validation is lexical only. A symlink placed inside the exposed root can point to a file or directory outside the root, and `List`, `ReadFile`, and `SearchFiles` will follow it through `os.Stat`, `os.ReadDir`, or `os.Open`.
- Impact: the server can disclose files outside the configured root despite advertising strict confinement.
- Suggested remediation actions:
  - Resolve candidate paths with `filepath.EvalSymlinks` before serving them.
  - Reject any resolved path that escapes the configured root after symlink resolution.
  - Decide explicitly whether in-root symlinks should be supported at all; if not, reject symlinks outright.
  - Add tests covering symlinked files and symlinked directories that point outside the root.

## 2. High: MCP initialize handshake is not protocol-compliant

- Affected code: `server.go:16`, `server.go:186`, `server.go:198`
- Problem: the server returns `"protocolVersion": "2.0"` from `initialize`, which is the JSON-RPC version rather than an MCP protocol revision. It also ignores the client's requested MCP version. `notifications/initialized` currently returns `204` instead of the expected accepted-notification response for the HTTP transport.
- Impact: spec-compliant MCP clients can reject the server during startup before any tools are available.
- Suggested remediation actions:
  - Separate JSON-RPC versioning from MCP protocol versioning in the code.
  - Parse and validate the client's requested MCP protocol version during `initialize`.
  - Return a supported MCP protocol revision and fail negotiation cleanly when the client requests an unsupported version.
  - Return the correct transport-level response code for accepted notifications.
  - Add tests for `initialize` negotiation, response shape, and `notifications/initialized`.

## 3. Medium: `read_file.max_bytes` bypasses the configured server-side cap

- Affected code: `server.go:351`
- Problem: `read_file.max_bytes` can override `--max-file-bytes` with any positive JSON number, including values larger than the operator-configured limit. Fractional values are also silently truncated by conversion to `int64`.
- Impact: a client can read larger payloads than the operator intended, defeating the documented per-read limit.
- Suggested remediation actions:
  - Treat `--max-file-bytes` as a hard upper bound.
  - Reject `max_bytes` values that are non-integer, non-positive, or above the configured cap.
  - Consider clamping to the configured cap only if that behavior is documented and tested.
  - Add tests for oversized and fractional `max_bytes` inputs.

## 4. Medium: HTTP transport does not validate `Origin`

- Affected code: `server.go:138`, `server.go:547`
- Problem: the MCP endpoint relies only on bearer authentication and does not validate the `Origin` header.
- Impact: the server is exposed to DNS rebinding-style attacks that the MCP HTTP transport guidance is intended to mitigate.
- Suggested remediation actions:
  - Validate `Origin` for browser-originated requests before processing MCP traffic.
  - Define an allowlist policy for local origins and any explicitly exposed remote origins.
  - Fail closed when an unexpected `Origin` is present.
  - Add tests covering accepted and rejected origins.

## 5. Medium: `list_files` silently coerces invalid arguments to defaults

- Affected code: `server.go:317`, `server.go:323`, `server.go:324`, `server.go:499`, `server.go:507`
- Problem: invalid `list_files` argument types are silently replaced with defaults. For example, a non-string `path` becomes `"."` and a non-bool `recursive` becomes `false`.
- Impact: malformed tool calls can unexpectedly enumerate the root directory instead of failing validation. This is inconsistent with `read_file` and `search_files`.
- Suggested remediation actions:
  - Validate `list_files` arguments strictly, matching the behavior of the other tool handlers.
  - Return argument errors for wrong types instead of defaulting.
  - Add negative tests for invalid `path` and `recursive` values.
  - Consider centralizing argument validation so all tools follow the same rules.

# Closed Findings
