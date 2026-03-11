# Open Findings

None.

# Closed Findings

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
