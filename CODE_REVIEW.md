# Open Findings

## 7. Low: Security headers not applied to non-POST responses

The method check at `server.go:136` returns `405 Method Not Allowed` before `addSecurityHeaders` is called at line 144. Non-POST responses are missing `X-Content-Type-Options`, `X-Frame-Options`, `Cache-Control`, and other security headers.

## 8. Low: `okCount` and `errCount` are not mutually exclusive

In `tui.go:582–590`, `errCount` increments when `evt.Status >= 400` and `okCount` increments when `evt.Error == ""`. A 500 response with an empty `Error` field increments both counters. The dashboard presents these as complementary metrics but they can double-count the same event.

## 9. Info: Dead code and redundant helpers

- `truncate()` at `tui.go:671` is defined but never called; `padLine` covers the same logic.
- `minInt()` at `filemanager.go:818` and `max()` at `tui.go:751` duplicate Go 1.21+ builtins available under the project's Go 1.24 toolchain.

## 10. High: `SearchFiles` and recursive `List` abort entirely on the first file permission error

If `os.Open` or `d.Info()` fails due to permissions on a single file, the `filepath.WalkDir` callback returns the error, terminating the entire search or listing instead of skipping the unreadable file. This makes the tool brittle in directories with mixed permissions.

## 14. High: `GET /mcp` is still rejected even though the transport requires it

`server.go:160` rejects every non-`POST` request with `405 Method Not Allowed`. That leaves the MCP endpoint incompatible with clients that expect the Streamable HTTP transport's required `GET` support.

## 15. Medium: Subsequent MCP requests do not validate `MCP-Protocol-Version`

The server negotiates `initialize.protocolVersion`, but `server.go` still does not validate the `MCP-Protocol-Version` HTTP header on later requests or reject unsupported values with `400 Bad Request`. This leaves protocol mismatches undetected instead of failing fast.

## 16. Medium: Case-insensitive literal search miscomputes offsets for non-ASCII text

In `filemanager.go:444-478`, the case-insensitive literal path lowercases the entire line before searching, then reuses those byte offsets against the original UTF-8 string in `filemanager.go:509-522`. Unicode case folds can change byte width, so `column`, `match_text`, and `line_text` can be wrong or even invalid UTF-8.

## 17. Medium: `search_files` fails on long single-line files that are still within the configured size limit

`filemanager.go:500-531` uses `bufio.Scanner` with a hard 1 MiB token limit, while `SearchFiles` accepts larger `max_bytes_per_file` values and the server allows up to 10 MiB. A valid search over a file containing a single long line therefore fails the whole request with `bufio.Scanner: token too long`.

## 18. Medium: Default Apple sandbox profile grants write access to the served root

`main.go:541-546` adds `(allow file-write* (subpath <root>))` to the generated sandbox profile even though the server only exposes read-oriented operations. That weakens the sandbox's defense-in-depth value by allowing modification or deletion of files under the served tree if the process is compromised.

## 11. Low: Inefficient slice shifting in `MCPDashboard.recordEvent` causes frequent allocations

`d.events = d.events[1:]` followed by `append` moves the slice window forward, forcing a reallocation every `tuiLogCapacity` events. A circular buffer or using `copy()` to shift elements in place would avoid these continuous allocations.

## 12. Low: HTTP response status code is not explicitly captured for successful RPC responses

In `server.go`, `mcpResponseWriter` wraps `http.ResponseWriter` to capture the status code. However, `writeRPCResult` calls `json.NewEncoder(w).Encode(resp)` without explicitly calling `WriteHeader`. This leaves `rw.status` as 0. The deferred logging function works around this by defaulting 0 to 200 OK, but relying on this default obscures cases where a 0 status might indicate a genuinely unwritten response.

## 13. Low: `io.LimitReader` with file size ceiling truncates pseudo-files

In `filemanager.go`, `limit := maxBytes` is clamped to `info.Size()`. For pseudo-files (like those in `/proc` or `/sys`) which often report a size of 0 despite having content, this will result in reading 0 bytes and returning an empty string. While this tool primarily targets standard local files, this remains a limitation.

# Closed Findings

## 6. High: `file_glob` filter does not match files in subdirectories

- Resolved by switching to `github.com/bmatcuk/doublestar/v4`, enabling `**/*.go` globstar matches for recursive traversal.
- Updated tool description to inform clients to use `**` for recursive searches.
- Regression tests added to verify globstar functionality.

## 1. High: Root confinement is bypassable via symlinks

- Resolved by canonicalizing the configured root, validating resolved paths with `filepath.EvalSymlinks`, rejecting direct symlink access, and skipping symlinked entries during list/search traversal.
- Regression tests cover symlinked files and symlinked directories that point outside the root.

## 2. High: MCP initialize handshake is not protocol-compliant

- Resolved by separating JSON-RPC from MCP protocol versioning, negotiating `initialize.protocolVersion` against supported MCP revisions, and returning `202 Accepted` for `notifications/initialized`.
- Regression tests cover initialize negotiation, fallback behavior for unsupported client versions, missing `protocolVersion`, and notification transport semantics.

## 3. Medium: `read_file.max_bytes` bypasses the configured server-side cap

- Resolved by treating the configured `--max-file-bytes` value as a hard ceiling and rejecting non-integer, non-positive, or above-cap `max_bytes` overrides.
- Regression tests cover oversized and fractional `max_bytes` values.

## 4. Medium: HTTP transport does not validate `Origin`

- Resolved by validating the `Origin` header before processing MCP traffic, allowing loopback browser origins plus explicitly configured remote origins, and rejecting unexpected origins with `403 Forbidden`.
- Regression tests cover allowed local origins, allowed configured remote origins, and rejected unexpected origins.

## 5. Medium: `list_files` silently coerces invalid arguments to defaults

- Resolved by validating `list_files.path` and `list_files.recursive` strictly and returning argument errors instead of silently defaulting.
- Regression tests cover invalid `path` and `recursive` argument types.

# New Observations

- In-root symlinks are now rejected consistently instead of being followed. That keeps confinement simple and predictable, but symlink-based aliases inside the served tree are no longer supported.
