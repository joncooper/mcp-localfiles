package main

import (
	"reflect"
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

	if d == nil {
		t.Fatalf("expected dashboard")
	}

	// Use merged exclusion semantics to keep this test close to the CLI contract.
	p := mergeExcludePattern(`secret`, `tmp\\.log$`)
	if p != `(?:secret)|(?:tmp\\.log$)` {
		t.Fatalf("expected merged regex, got %q", p)
	}

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
		d.recordEvent(MCPEvent{
			Method:  "tools/list",
			Status:  200,
			Tool:    "tools/list",
			Details: "request details",
		})
	}

	// start at first row
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
		d.recordEvent(MCPEvent{
			Method:  "tools/list",
			Status:  200,
			Tool:    "tools/list",
			Details: "request details",
		})
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
	d.recordEvent(MCPEvent{
		Method: "tools/list",
		Status: 200,
		Tool:   "tools/list",
	})
	old := d.selected
	d.handleInput("noop")
	if d.selected != old {
		t.Fatalf("expected selected unchanged on unknown command")
	}
}

func TestWrapAndPadText(t *testing.T) {
	got := wrapText("one two three four", 5)
	want := []string{
		"one  ",
		"two  ",
		"three",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected wrap output: %#v", got)
	}
}

func TestWrapTextWithLongToken(t *testing.T) {
	got := wrapText("supercalifragilistic", 5)
	if len(got) != 1 {
		t.Fatalf("expected one wrapped line, got %d", len(got))
	}
	if got[0] != "supe…" {
		t.Fatalf("unexpected wrapped token: %q", got[0])
	}
}

func TestMCPDashboardSetupScreenShowsChatGPTInstructionsWhenNoRequests(t *testing.T) {
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
		width:     100,
		height:    30,
	}.View()

	got := view.Content
	if !strings.Contains(got, "Connect ChatGPT") {
		t.Fatalf("expected connect section, got %q", got)
	}
	if !strings.Contains(got, "2. Add server URL: https://host.ts.net/mcp") {
		t.Fatalf("expected remote/local setup URL, got %q", got)
	}
	if !strings.Contains(got, "3. Add header: Authorization: Bearer token123") {
		t.Fatalf("expected token setup line, got %q", got)
	}
}

func TestPadLine(t *testing.T) {
	if got := padLine("abc", 5); got != "abc  " {
		t.Fatalf("unexpected pad: %q", got)
	}
	if got := padLine("abcdef", 4); got != "abc…" {
		t.Fatalf("unexpected truncate: %q", got)
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

	// Fill the buffer to capacity
	for i := 0; i < tuiLogCapacity; i++ {
		d.recordEvent(MCPEvent{Method: "tools/list", Status: 200, Tool: "tools/list"})
	}

	initialPtr := &d.events[0]
	allocs := 0

	// Add many more events to force capacity exhaustion if it's naive slice shifting
	for i := 0; i < tuiLogCapacity*3; i++ {
		d.recordEvent(MCPEvent{Method: "tools/list", Status: 200, Tool: "tools/list"})
		currentPtr := &d.events[0]
		if currentPtr != initialPtr {
			allocs++
			initialPtr = currentPtr
		}
	}

	// With naive shifting (d.events[1:] + append), the pointer will change every time cap is reached.
	// With a proper ring buffer or in-place copy, the backing array pointer for the slice 
	// should not change repeatedly (or at all, if we just shift within the same slice).
	// Wait, if it's in-place copy, d.events[0] stays the same pointer.
	// If it's a ring buffer, d.events might not even be a slice, or we just overwrite.
	if allocs > 0 {
		t.Fatalf("expected 0 array reallocations when full, got %v", allocs)
	}
}
