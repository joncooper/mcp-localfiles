# TUI Enhancements

Target: Wireshark-level UX for inspecting MCP traffic in real time.

## Layout

Fixed top bar and bottom bar pinned to terminal edges. Everything between is the main content area.

```
┌─────────────────────────────────────────────────────┐
│ Top bar: title, endpoint, uptime, status indicators │
├─────────────────────────────────────────────────────┤
│                                                     │
│  Main content area (fills remaining height)         │
│                                                     │
│  ┌─ Packet list (scrollable table) ──────────────┐  │
│  │ #  Time      Method  Tool       Status Latency│  │
│  │ 1  14:02:01  POST    list_files  200    12ms  │  │
│  │ 2  14:02:03  POST    read_file   200    45ms  │  │
│  │ ...                                           │  │
│  └───────────────────────────────────────────────┘  │
│                                                     │
│  ┌─ Detail inspector (collapsible tree) ─────────┐  │
│  │ ▸ Request params                              │  │
│  │ ▸ Response body                               │  │
│  │ ▸ Error details                               │  │
│  └───────────────────────────────────────────────┘  │
│                                                     │
├─────────────────────────────────────────────────────┤
│ Bottom bar: filter input, keybindings, counters     │
└─────────────────────────────────────────────────────┘
```

### Top bar

- App name and version
- Endpoint URL (local or tailscale)
- Uptime
- Live/paused indicator
- Compact metric counters: `42 req  40 ok  1 err  1 unauth`

### Bottom bar

- Filter input (when active): `Filter: tool:read_file status:200`
- Otherwise: keybinding hints and mode indicator
- Tab selector if multiple views are implemented

## 1. Richer event data

Capture more from each request/response cycle in MCPEvent:

- Request body / JSON-RPC params (truncated if large)
- Response body / result summary
- Request and response sizes (bytes)
- JSON-RPC request ID
- Content-Type header

This is prerequisite for everything else — the inspector can only show what we capture.

## 2. Lipgloss styling

Replace hand-rolled box drawing with lipgloss from the Charm ecosystem:

- Color-coded table rows: green (2xx), red (4xx/5xx), yellow (401)
- Bold headers, dim timestamps, bright tool names
- Proper border styles that adapt to terminal width
- Distinct visual treatment for selected vs unselected rows

## 3. Scrollable full-height packet table

Replace the fixed 8-row event list with a table that fills available height:

- Columns: `#`, `Time`, `Method`, `Tool`, `Client`, `Status`, `Latency`, `Size`
- Column widths adapt to terminal size
- Vim-style scrolling: `j`/`k` line, `ctrl-d`/`ctrl-u` half-page, `G`/`gg` jump to end/start
- Auto-scroll to newest when at bottom (stop auto-scroll when user scrolls up)
- Sortable by column (press column number or letter)

## 4. Filter bar

`/` activates the filter input. Supports expressions:

- Free text: matches against tool, method, client, details
- Field filters: `tool:read_file`, `status:200`, `method:tools/call`, `client:chatgpt`
- Negation: `-status:401`
- Multiple terms are AND'd: `tool:list_files status:200`

Filtered view updates live as you type. `Esc` clears filter. Show match count in bottom bar.

## 5. Structured detail inspector

Bottom pane (resizable split with the table above). Shows selected event as a collapsible tree:

```
▾ Request
    Method    tools/call
    Tool      read_file
    ID        42
    Params    {"path": "src/main.go", "offset": 1, "limit": 50}

▸ Response
▸ Timing
▸ Client
```

- `Enter` or `l` expands/collapses sections
- `h` collapses current section
- Long values are wrapped, not truncated
- JSON values are syntax-highlighted if possible

## 6. Additional features

### Pause/resume
- `Space` toggles capture. New events are buffered and appear when resumed.
- Top bar shows PAUSED indicator.

### Copy
- `y` yanks selected event as JSON to clipboard
- `Y` yanks as a curl command that would reproduce the request

### Search within events
- `?` opens reverse search through event list
- `n`/`N` navigate matches

### Tab views (future)
- **Live**: the main packet list + inspector
- **Stats**: latency histogram, tool call distribution pie chart, requests/sec sparkline
- **Setup**: connection info, token, setup instructions (current pre-first-request screen)

## Implementation order

1. Richer MCPEvent data (server.go) — unblocks everything else
2. Top bar / bottom bar layout with pinned edges
3. Lipgloss styling
4. Scrollable full-height table
5. Detail inspector with collapsible tree
6. Filter bar
7. Pause/resume, copy, search
8. Tab views and stats
