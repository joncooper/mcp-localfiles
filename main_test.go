package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestResolveRootDirRelative(t *testing.T) {
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("get cwd abs: %v", err)
	}

	resolved, err := resolveRootDir(".")
	if err != nil {
		t.Fatalf("resolve root dir: %v", err)
	}
	if resolved != cwd {
		t.Fatalf("expected resolved path %q, got %q", cwd, resolved)
	}
	if !filepath.IsAbs(resolved) {
		t.Fatalf("expected absolute root path, got %q", resolved)
	}
}

func TestGenerateTokenFormat(t *testing.T) {
	for range 3 {
		token, err := generateToken()
		if err != nil {
			t.Fatalf("generate token: %v", err)
		}
		if len(token) != 48 {
			t.Fatalf("expected 48-char token, got %d", len(token))
		}
		if !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(token) {
			t.Fatalf("token should be hex encoded, got %q", token)
		}
	}
}

func TestMergeExcludePattern(t *testing.T) {
	if got := mergeExcludePattern("", "skip\\.txt$"); got != "skip\\.txt$" {
		t.Fatalf("expected only exclude-regex to remain, got %q", got)
	}

	if got := mergeExcludePattern("secret.*", ""); got != "secret.*" {
		t.Fatalf("expected only --exclude to remain, got %q", got)
	}

	if got := mergeExcludePattern("secret.*", "skip\\.txt$"); got != "(?:secret.*)|(?:skip\\.txt$)" {
		t.Fatalf("expected merged regex, got %q", got)
	}

	whitespaceOnly := "   "
	if got := mergeExcludePattern(whitespaceOnly, whitespaceOnly); got != "" {
		t.Fatalf("expected empty merged regex for whitespace only inputs, got %q", got)
	}
}

func TestParseCLIArgsSupportsPositionalThenFlags(t *testing.T) {
	args := []string{"/tmp/root", "127.0.0.1:8080", "--no-tailscale", "--exclude=.*"}
	cfg, err := parseCLIArgs(args)
	if err != nil {
		t.Fatalf("parse cli args: %v", err)
	}
	if cfg.NoTailscale != true {
		t.Fatalf("expected no-tailscale parsed as true")
	}
	if cfg.Root != "/tmp/root" {
		t.Fatalf("expected root from positional, got %q", cfg.Root)
	}
	if cfg.Listen != "127.0.0.1:8080" {
		t.Fatalf("expected listen from positional, got %q", cfg.Listen)
	}
	if cfg.Exclude != ".*" {
		t.Fatalf("expected exclude parsed as .* got %q", cfg.Exclude)
	}
}

func TestParseCLIArgsDefaultsToEphemeralPortAndNoTailscale(t *testing.T) {
	cfg, err := parseCLIArgs(nil)
	if err != nil {
		t.Fatalf("parse cli args: %v", err)
	}
	if cfg.Listen != defaultListenAddr {
		t.Fatalf("expected default listen %q, got %q", defaultListenAddr, cfg.Listen)
	}
	if cfg.TailscaleExpose {
		t.Fatalf("expected default tailscale exposure to be false")
	}
	if cfg.Sandbox != (runtime.GOOS == "darwin") {
		t.Fatalf("expected default sandbox value to match runtime platform default")
	}
}

func TestParseCLIArgsDefaultsToCurrentDir(t *testing.T) {
	cfg, err := parseCLIArgs(nil)
	if err != nil {
		t.Fatalf("parse cli args: %v", err)
	}
	if cfg.Root != "" {
		t.Fatalf("expected empty root before resolution for default path, got %q", cfg.Root)
	}
	resolvedRoot, err := resolveRootDir(cfg.Root)
	if err != nil {
		t.Fatalf("resolve root dir: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	expected, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		t.Fatalf("resolve expected root: %v", err)
	}
	if resolvedRoot != expected {
		t.Fatalf("expected resolved root to current working directory, got %q (cwd %q)", resolvedRoot, cwd)
	}
}

func TestDefaultCLIConfigInvariants(t *testing.T) {
	cfg, err := parseCLIArgs(nil)
	if err != nil {
		t.Fatalf("parse cli args: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	expectedRoot, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		t.Fatalf("resolve expected cwd: %v", err)
	}

	resolvedRoot, err := resolveRootDir(cfg.Root)
	if err != nil {
		t.Fatalf("resolve root dir: %v", err)
	}

	if cfg.Listen != defaultListenAddr {
		t.Fatalf("expected default listen %q, got %q", defaultListenAddr, cfg.Listen)
	}
	if resolvedRoot != expectedRoot {
		t.Fatalf("expected resolved default root %q, got %q", expectedRoot, resolvedRoot)
	}
	if cfg.NoTailscale != false {
		t.Fatalf("expected no-tailscale default to false (tailscale disabled by default)")
	}
	if cfg.Sandbox != (runtime.GOOS == "darwin") {
		t.Fatalf("expected sandbox default to be true on macOS")
	}
}

func TestParseCLIArgsDisableSandbox(t *testing.T) {
	cfg, err := parseCLIArgs([]string{"--sandbox=false", "/tmp/root"})
	if err != nil {
		t.Fatalf("parse cli args: %v", err)
	}
	if cfg.Sandbox {
		t.Fatalf("expected sandbox to be disabled")
	}
}

func TestParseCLIArgsSandboxProfile(t *testing.T) {
	cfg, err := parseCLIArgs([]string{"--sandbox", "--sandbox-profile", "/tmp/custom.sb", "/tmp/root"})
	if err != nil {
		t.Fatalf("parse cli args: %v", err)
	}
	if cfg.SandboxProfile != "/tmp/custom.sb" {
		t.Fatalf("expected sandbox profile path, got %q", cfg.SandboxProfile)
	}
	if !cfg.Sandbox {
		t.Fatalf("expected sandbox to be enabled")
	}
}

func TestParseCLIArgsUsesEnvRootWhenRootNotSet(t *testing.T) {
	t.Setenv("LOCALFILES_MCP_ROOT", "/env/root")
	cfg, err := parseCLIArgs([]string{"--listen", "127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("parse cli args: %v", err)
	}
	if cfg.Root != "/env/root" {
		t.Fatalf("expected root from env, got %q", cfg.Root)
	}
	if cfg.Listen != "127.0.0.1:8080" {
		t.Fatalf("expected listen from positional, got %q", cfg.Listen)
	}
}

func TestParseCLIArgsRootFlagOverridesPositional(t *testing.T) {
	args := []string{"--root", "/flag/root", "/pos/root", "127.0.0.1:8080"}
	cfg, err := parseCLIArgs(args)
	if err != nil {
		t.Fatalf("parse cli args: %v", err)
	}
	if cfg.Root != "/flag/root" {
		t.Fatalf("expected root from flag, got %q", cfg.Root)
	}
}

func TestBuildAppleSandboxProfile(t *testing.T) {
	profile := buildAppleSandboxProfile("/tmp/root", "/tmp/localfiles-mcp", "/usr/local/bin/tailscale")
	if !strings.Contains(profile, "(version 1)") {
		t.Fatalf("expected sandbox profile version")
	}
	if !strings.Contains(profile, "(import \"bsd.sb\")") {
		t.Fatalf("expected sandbox profile to import bsd baseline rules")
	}
	if !strings.Contains(profile, "(subpath "+strconv.Quote("/tmp/root")+")") {
		t.Fatalf("expected sandbox profile to restrict reads to root")
	}
	if !strings.Contains(profile, "(literal "+strconv.Quote("/tmp/localfiles-mcp")+")") {
		t.Fatalf("expected sandbox profile to allow executable execution")
	}
	if !strings.Contains(profile, "(literal "+strconv.Quote("/usr/local/bin/tailscale")+")") {
		t.Fatalf("expected sandbox profile to allow tailscale execution when available")
	}
	if !strings.Contains(profile, "(allow network-inbound (local ip))") {
		t.Fatalf("expected sandbox profile to allow local inbound network")
	}
}

func TestBuildSandboxArgvForRelaunchPreservesValues(t *testing.T) {
	cfg := cliConfig{
		Root:              "/tmp/root",
		Listen:            "127.0.0.1:54321",
		Exclude:           "secret.*",
		ExcludeRegex:      "node_modules",
		ExcludeDotfiles:   false,
		MaxFileBytes:      2048,
		NoTui:             true,
		TailscaleExpose:   true,
		NoTailscale:       false,
		TailscalePath:     "/mcp",
		TailscalePort:     444,
		TailscaleHostname: "host.ts.net",
		TLSCert:           "/tmp/cert.pem",
		TLSKey:            "/tmp/key.pem",
		Token:             "token-123",
		SandboxProfile:    "/tmp/custom.sb",
	}
	args := buildSandboxArgvForRelaunch(cfg)
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--sandbox" {
			t.Fatalf("sandbox relaunch args should avoid recursive sandbox flag")
		}
	}

	parsed, err := parseCLIArgs(args)
	if err != nil {
		t.Fatalf("parse relaunch args: %v", err)
	}
	if parsed.Root != cfg.Root {
		t.Fatalf("expected root %q, got %q", cfg.Root, parsed.Root)
	}
	if parsed.Listen != cfg.Listen {
		t.Fatalf("expected listen %q, got %q", cfg.Listen, parsed.Listen)
	}
	if parsed.Exclude != cfg.Exclude {
		t.Fatalf("expected exclude %q, got %q", cfg.Exclude, parsed.Exclude)
	}
	if parsed.ExcludeRegex != cfg.ExcludeRegex {
		t.Fatalf("expected exclude-regex %q, got %q", cfg.ExcludeRegex, parsed.ExcludeRegex)
	}
	if parsed.ExcludeDotfiles != cfg.ExcludeDotfiles {
		t.Fatalf("expected exclude-dotfiles %v, got %v", cfg.ExcludeDotfiles, parsed.ExcludeDotfiles)
	}
	if parsed.MaxFileBytes != cfg.MaxFileBytes {
		t.Fatalf("expected max-file-bytes %d, got %d", cfg.MaxFileBytes, parsed.MaxFileBytes)
	}
	if parsed.NoTui != cfg.NoTui {
		t.Fatalf("expected no-tui %v, got %v", cfg.NoTui, parsed.NoTui)
	}
	if parsed.TailscaleExpose != cfg.TailscaleExpose {
		t.Fatalf("expected tailscale-expose %v, got %v", cfg.TailscaleExpose, parsed.TailscaleExpose)
	}
	if parsed.NoTailscale != cfg.NoTailscale {
		t.Fatalf("expected no-tailscale %v, got %v", cfg.NoTailscale, parsed.NoTailscale)
	}
	if parsed.TailscalePath != cfg.TailscalePath {
		t.Fatalf("expected tailscale-path %q, got %q", cfg.TailscalePath, parsed.TailscalePath)
	}
	if parsed.TailscalePort != cfg.TailscalePort {
		t.Fatalf("expected tailscale-port %d, got %d", cfg.TailscalePort, parsed.TailscalePort)
	}
	if parsed.TailscaleHostname != cfg.TailscaleHostname {
		t.Fatalf("expected tailscale-hostname %q, got %q", cfg.TailscaleHostname, parsed.TailscaleHostname)
	}
	if parsed.TLSCert != cfg.TLSCert {
		t.Fatalf("expected tls-cert %q, got %q", cfg.TLSCert, parsed.TLSCert)
	}
	if parsed.TLSKey != cfg.TLSKey {
		t.Fatalf("expected tls-key %q, got %q", cfg.TLSKey, parsed.TLSKey)
	}
	if parsed.Token != cfg.Token {
		t.Fatalf("expected token %q, got %q", cfg.Token, parsed.Token)
	}
	if parsed.SandboxProfile != cfg.SandboxProfile {
		t.Fatalf("expected sandbox-profile %q, got %q", cfg.SandboxProfile, parsed.SandboxProfile)
	}
}
