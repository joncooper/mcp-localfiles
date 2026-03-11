package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestBuildAppleSandboxProfileDoesNotGrantRootWriteAccess(t *testing.T) {
	root := "/tmp/root"
	profile := buildAppleSandboxProfile(root, "/tmp/localfiles-mcp", "/usr/local/bin/tailscale")

	rootWriteRule := "(allow file-write* (subpath " + strconv.Quote(root) + "))"
	if strings.Contains(profile, rootWriteRule) {
		t.Fatalf("expected sandbox profile not to allow writes to served root, got %q", profile)
	}
}
