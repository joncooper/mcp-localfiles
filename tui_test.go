package main

import (
	"strings"
	"testing"
)

func TestMCPDashboardEventCounters(t *testing.T) {
	d := NewMCPDashboard(MCPDashboardConfig{
		LocalURL:     "http://127.0.0.1:8080/mcp",
		RemoteURL:    "https://host.ts.net/mcp",
		RootDir:      ".",
		ExposePath:   "/mcp",
		ExposeActive: true,
		AuthToken:    "token",
	})

	d.recordEvent(MCPEvent{Method: "tools/list", Status: 200, Tool: "tools/list"})
	d.recordEvent(MCPEvent{Method: "tools/call", Status: 401, Tool: "tools.call:read_file", Error: "unauthorized"})
	d.recordEvent(MCPEvent{Method: "tools/list", Status: 500, Tool: "tools/list"})

	if d.reqCount != 3 {
		t.Fatalf("expected 3 requests, got %d", d.reqCount)
	}
	if d.okCount != 1 {
		t.Fatalf("expected 1 ok event, got %d", d.okCount)
	}
	if d.errCount != 2 {
		t.Fatalf("expected 2 error statuses, got %d", d.errCount)
	}
	if d.unauth != 1 {
		t.Fatalf("expected 1 unauthorized event, got %d", d.unauth)
	}
}

func TestMCPDashboardHandleInputSelectionAndExpandedState(t *testing.T) {
	d := NewMCPDashboard(MCPDashboardConfig{
		LocalURL:     "http://127.0.0.1:8080/mcp",
		RemoteURL:    "https://host.ts.net/mcp",
		RootDir:      ".",
		ExposePath:   "/mcp",
		ExposeActive: true,
		AuthToken:    "token",
	})

	for i := 0; i < 3; i++ {
		d.recordEvent(MCPEvent{Method: "tools/list", Status: 200, Tool: "tools/list", Details: "request details"})
	}

	d.selected = 0
	d.expanded = true
	d.handleInput("down")
	if d.selected != 1 || d.expanded {
		t.Fatalf("expected selection=1 and expanded=false after down, got selected=%d expanded=%v", d.selected, d.expanded)
	}

	d.handleInput("toggle")
	if !d.expanded {
		t.Fatalf("expected expanded=true after toggle")
	}

	d.handleInput("up")
	if d.selected != 0 || d.expanded {
		t.Fatalf("expected selection=0 and expanded=false after up, got selected=%d expanded=%v", d.selected, d.expanded)
	}
}

func TestMCPDashboardHandleInputClear(t *testing.T) {
	d := NewMCPDashboard(MCPDashboardConfig{
		LocalURL:     "http://127.0.0.1:8080/mcp",
		RemoteURL:    "https://host.ts.net/mcp",
		RootDir:      ".",
		ExposePath:   "/mcp",
		ExposeActive: true,
		AuthToken:    "token",
	})

	for i := 0; i < 2; i++ {
		d.recordEvent(MCPEvent{Method: "tools/list", Status: 200, Tool: "tools/list", Details: "request details"})
	}
	d.handleInput("clear")
	if d.reqCount != 0 || len(d.events) != 0 || d.okCount != 0 || d.errCount != 0 || d.unauth != 0 {
		t.Fatalf("expected all counters reset after clear")
	}
	if d.selected != -1 {
		t.Fatalf("expected selection reset to -1")
	}
}

func TestMCPDashboardHandleInputNoSelectionNoPanic(t *testing.T) {
	d := NewMCPDashboard(MCPDashboardConfig{
		LocalURL:     "http://127.0.0.1:8080/mcp",
		RemoteURL:    "https://host.ts.net/mcp",
		RootDir:      ".",
		ExposePath:   "/mcp",
		ExposeActive: true,
		AuthToken:    "token",
	})

	d.handleInput("toggle")
	if d.expanded {
		t.Fatalf("expected expanded remain false with no selection")
	}
	d.handleInput("up")
	if d.selected != 0 || d.maxSelectable() != 0 {
		t.Fatalf("expected selection clamp to 0 with empty log")
	}
}

func TestMCPDashboardHandleInputUnknownCommand(t *testing.T) {
	d := NewMCPDashboard(MCPDashboardConfig{
		LocalURL:     "http://127.0.0.1:8080/mcp",
		RemoteURL:    "https://host.ts.net/mcp",
		RootDir:      ".",
		ExposePath:   "/mcp",
		ExposeActive: true,
		AuthToken:    "token",
	})
	d.recordEvent(MCPEvent{Method: "tools/list", Status: 200, Tool: "tools/list"})
	old := d.selected
	d.handleInput("noop")
	if d.selected != old {
		t.Fatalf("expected selected unchanged on unknown command")
	}
}

func TestMCPDashboardSetupScreenShowsConnectionInfo(t *testing.T) {
	d := NewMCPDashboard(MCPDashboardConfig{
		LocalURL:     "http://127.0.0.1:12345/mcp",
		RemoteURL:    "https://host.ts.net/mcp",
		RootDir:      ".",
		ExposePath:   "/mcp",
		ExposeActive: true,
		AuthToken:    "token123",
	})

	view := mcpDashboardModel{
		dashboard: d,
		width:     120,
		height:    40,
	}.View()

	got := view.Content
	if !strings.Contains(got, "token123") {
		t.Fatalf("expected token in setup screen, got %q", got)
	}
	if !strings.Contains(got, "host.ts.net") {
		t.Fatalf("expected endpoint in setup screen, got %q", got)
	}
}

func TestClampViewport(t *testing.T) {
	cases := []struct {
		name     string
		selected int
		current  int
		window   int
		total    int
		want     int
	}{
		{name: "total within window", selected: 9, current: 4, window: 12, total: 10, want: 0},
		{name: "scroll down", selected: 10, current: 0, window: 5, total: 20, want: 6},
		{name: "scroll keeps window", selected: 2, current: 4, window: 5, total: 20, want: 2},
		{name: "scroll up", selected: 2, current: 8, window: 5, total: 20, want: 2},
	}

	for _, tc := range cases {
		got := clampViewport(tc.selected, tc.current, tc.window, tc.total)
		if got != tc.want {
			t.Fatalf("%s: expected %d, got %d", tc.name, tc.want, got)
		}
	}
}

func TestMCPDashboardEventRingBufferAllocations(t *testing.T) {
	d := NewMCPDashboard(MCPDashboardConfig{
		LocalURL:     "http://127.0.0.1:8080/mcp",
		RemoteURL:    "https://host.ts.net/mcp",
		RootDir:      ".",
		ExposePath:   "/mcp",
		ExposeActive: true,
		AuthToken:    "token",
	})

	for i := 0; i < tuiLogCapacity; i++ {
		d.recordEvent(MCPEvent{Method: "tools/list", Status: 200, Tool: "tools/list"})
	}

	initialPtr := &d.events[0]
	allocs := 0

	for i := 0; i < tuiLogCapacity*3; i++ {
		d.recordEvent(MCPEvent{Method: "tools/list", Status: 200, Tool: "tools/list"})
		currentPtr := &d.events[0]
		if currentPtr != initialPtr {
			allocs++
			initialPtr = currentPtr
		}
	}

	if allocs > 0 {
		t.Fatalf("expected 0 array reallocations when full, got %v", allocs)
	}
}

func TestMCPDashboardPauseMode(t *testing.T) {
	d := NewMCPDashboard(MCPDashboardConfig{
		LocalURL:  "http://127.0.0.1:8080/mcp",
		AuthToken: "token",
	})

	d.recordEvent(MCPEvent{Method: "tools/list", Status: 200, Tool: "tools/list"})
	d.handleInput("pause")
	if !d.paused {
		t.Fatalf("expected paused after pause command")
	}

	selBefore := d.selected
	d.recordEvent(MCPEvent{Method: "tools/call", Status: 200, Tool: "tools.call:read_file"})
	if d.reqCount != 2 {
		t.Fatalf("expected events still counted while paused, got %d", d.reqCount)
	}
	if d.selected != selBefore {
		t.Fatalf("expected selection unchanged while paused")
	}

	d.handleInput("pause")
	if d.paused {
		t.Fatalf("expected unpaused after second pause")
	}
}

func TestMCPDashboardFilterMode(t *testing.T) {
	d := NewMCPDashboard(MCPDashboardConfig{
		LocalURL:  "http://127.0.0.1:8080/mcp",
		AuthToken: "token",
	})

	d.handleInput("filter")
	if d.mode != modeFilter {
		t.Fatalf("expected filter mode after / command")
	}

	d.handleInput("escape")
	if d.mode != modeNormal {
		t.Fatalf("expected normal mode after escape")
	}
}

func TestFilterEventsFieldMatch(t *testing.T) {
	events := []MCPEvent{
		{Method: "tools/call", Tool: "tools.call:list_files", Status: 200},
		{Method: "tools/call", Tool: "tools.call:read_file", Status: 200},
		{Method: "tools/call", Tool: "tools.call:read_file", Status: 401},
	}

	// Filter by tool
	got := filterEvents(events, "tool:list")
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("expected 1 match for tool:list, got %v", got)
	}

	// Filter by status
	got = filterEvents(events, "status:401")
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("expected 1 match for status:401, got %v", got)
	}

	// Negation
	got = filterEvents(events, "-status:401")
	if len(got) != 2 {
		t.Fatalf("expected 2 matches for -status:401, got %v", got)
	}

	// Empty filter returns nil (meaning "all events")
	got = filterEvents(events, "")
	if got != nil {
		t.Fatalf("expected nil for empty filter, got %v", got)
	}
}

func TestToolShortName(t *testing.T) {
	if got := toolShortName("tools.call:read_file"); got != "read_file" {
		t.Fatalf("expected read_file, got %q", got)
	}
	if got := toolShortName("tools/list"); got != "tools/list" {
		t.Fatalf("expected tools/list, got %q", got)
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		bytes int
		want  string
	}{
		{0, "-"},
		{512, "512B"},
		{1536, "1.5K"},
		{1048576, "1.0M"},
	}
	for _, tc := range cases {
		got := formatSize(tc.bytes)
		if got != tc.want {
			t.Fatalf("formatSize(%d): expected %q, got %q", tc.bytes, tc.want, got)
		}
	}
}

func TestMCPDashboardHomeEnd(t *testing.T) {
	d := NewMCPDashboard(MCPDashboardConfig{
		LocalURL:  "http://127.0.0.1:8080/mcp",
		AuthToken: "token",
	})

	for i := 0; i < 10; i++ {
		d.recordEvent(MCPEvent{Method: "tools/list", Status: 200, Tool: "tools/list"})
	}

	d.handleInput("end")
	if d.selected != 9 {
		t.Fatalf("expected selected=9 after end, got %d", d.selected)
	}

	d.handleInput("home")
	if d.selected != 0 {
		t.Fatalf("expected selected=0 after home, got %d", d.selected)
	}
}

func TestWrapText(t *testing.T) {
	got := wrapText("one two three four", 10)
	if len(got) == 0 {
		t.Fatalf("expected at least one line")
	}
	// First line should contain "one two"
	if !strings.HasPrefix(strings.TrimSpace(got[0]), "one two") {
		t.Fatalf("unexpected first line: %q", got[0])
	}
}

func TestPadLine(t *testing.T) {
	if got := fitToWidth("abc", 5); got != "abc  " {
		t.Fatalf("unexpected pad: %q", got)
	}
	if got := fitToWidth("abcdef", 4); got != "abc…" {
		t.Fatalf("unexpected truncate: %q", got)
	}
}
