package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeTailscalePath(t *testing.T) {
	cases := map[string]string{
		"":       "/mcp",
		"mcp":    "/mcp",
		"/mcp/":  "/mcp",
		"/mcp/a": "/mcp/a",
	}
	for input, want := range cases {
		got := normalizeTailscalePath(input)
		if got != want {
			t.Fatalf("normalize %q -> %q, want %q", input, got, want)
		}
	}
}

func TestDiscoverTailscaleHostnameFromJSON(t *testing.T) {
	saved := runShellCommand
	defer func() { runShellCommand = saved }()

	statusJSON := `{"Self":{"DNSName":"host.example.ts.net"},"Version":"mock"}`
	runShellCommand = func(_ context.Context, command string, args ...string) ([]byte, error) {
		if command != "tailscale" {
			t.Fatalf("expected tailscale command, got %s", command)
		}
		if strings.Join(args, " ") != "status --json" {
			t.Fatalf("unexpected command args: %q", args)
		}
		return []byte(statusJSON), nil
	}

	hostname, err := discoverTailscaleHostname()
	if err != nil {
		t.Fatalf("discover hostname: %v", err)
	}
	if hostname != "host.example.ts.net" {
		t.Fatalf("expected host.example.ts.net, got %q", hostname)
	}
}

func TestTailscaleSessionLifecycle(t *testing.T) {
	raw := []string{}
	runner := func(_ context.Context, command string, args ...string) ([]byte, error) {
		raw = append(raw, command+" "+strings.Join(args, " "))
		return []byte("ok"), nil
	}

	session, err := NewTailscaleSession(TailscaleSessionConfig{
		Path:      "/mcp",
		Listen:    "127.0.0.1:8080",
		HttpsPort: 443,
		Runner:    runner,
	})
	if err != nil {
		t.Fatalf("new tailscale session: %v", err)
	}

	if err := session.Expose(); err != nil {
		t.Fatalf("expose: %v", err)
	}
	if err := session.Expose(); err != nil {
		t.Fatalf("re-expose: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 command on repeated expose, got %d", len(raw))
	}

	if err := session.Teardown(); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("expected teardown to add second command, got %d", len(raw))
	}

	if raw[0] != "tailscale serve --https=443 --set-path=/mcp=http://127.0.0.1:8080/mcp" {
		t.Fatalf("unexpected expose command: %q", raw[0])
	}
	if raw[1] != "tailscale serve --https=443 --set-path=/mcp=off" {
		t.Fatalf("unexpected teardown command: %q", raw[1])
	}

	if err := session.Teardown(); err != nil {
		t.Fatalf("second teardown: %v", err)
	}
}

func TestTailscaleSessionTeardownFallbackReset(t *testing.T) {
	raw := []string{}
	runner := func(_ context.Context, command string, args ...string) ([]byte, error) {
		raw = append(raw, command+" "+strings.Join(args, " "))
		rawArgs := strings.Join(args, " ")
		if strings.Contains(rawArgs, "serve --https=444 --set-path=/mcp=off") {
			return []byte("failed"), errors.New("off path failed")
		}
		if strings.Contains(rawArgs, "serve reset") {
			return []byte("ok"), nil
		}
		return []byte("ok"), nil
	}

	session, err := NewTailscaleSession(TailscaleSessionConfig{
		Path:      "mcp",
		Listen:    "127.0.0.1:8080",
		HttpsPort: 444,
		Runner:    runner,
	})
	if err != nil {
		t.Fatalf("new tailscale session: %v", err)
	}

	if err := session.Expose(); err != nil {
		t.Fatalf("expose: %v", err)
	}
	err = session.Teardown()
	if err == nil {
		t.Fatalf("expected fallback teardown error")
	}
	if len(raw) < 3 {
		t.Fatalf("expected reset fallback commands, got %d", len(raw))
	}
	last := raw[len(raw)-1]
	if last != "tailscale serve reset" {
		t.Fatalf("expected reset fallback command, got %q", last)
	}
}
