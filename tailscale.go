package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

const (
	tailscaleBinary = "tailscale"
)

type cmdRunner func(ctx context.Context, command string, args ...string) ([]byte, error)

type TailscaleSessionConfig struct {
	Path      string
	Listen    string
	HttpsPort int
	Runner    cmdRunner
}

type TailscaleSession struct {
	path      string
	listen    string
	httpsPort int
	runner    cmdRunner
	running   bool
	mu        sync.Mutex
}

func NewTailscaleSession(cfg TailscaleSessionConfig) (*TailscaleSession, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, fmt.Errorf("tailscale path is required")
	}
	if strings.TrimSpace(cfg.Listen) == "" {
		return nil, fmt.Errorf("tailscale listen address is required")
	}
	if cfg.HttpsPort <= 0 {
		cfg.HttpsPort = 443
	}
	runner := cfg.Runner
	if runner == nil {
		runner = runShellCommand
	}

	return &TailscaleSession{
		path:      normalizeTailscalePath(cfg.Path),
		listen:    cfg.Listen,
		httpsPort: cfg.HttpsPort,
		runner:    runner,
	}, nil
}

func (s *TailscaleSession) Expose() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}

	endpoint := fmt.Sprintf("http://%s%s", s.listen, s.path)
	args := []string{"serve", fmt.Sprintf("--https=%d", s.httpsPort), fmt.Sprintf("--set-path=%s=%s", s.path, endpoint)}
	if _, err := s.runner(context.Background(), tailscaleBinary, args...); err != nil {
		return err
	}
	s.running = true
	return nil
}

func (s *TailscaleSession) Teardown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return nil
	}

	var firstErr error
	offArgs := []string{"serve", fmt.Sprintf("--https=%d", s.httpsPort), fmt.Sprintf("--set-path=%s=off", s.path)}
	if _, err := s.runner(context.Background(), tailscaleBinary, offArgs...); err != nil {
		firstErr = err
	}

	if firstErr != nil {
		resetArgs := []string{"serve", "reset"}
		if _, resetErr := s.runner(context.Background(), tailscaleBinary, resetArgs...); resetErr != nil {
			s.running = false
			return fmt.Errorf("off-path failed: %v; reset failed: %v", firstErr, resetErr)
		}
	}

	s.running = false
	return firstErr
}

func normalizeTailscalePath(raw string) string {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "/mcp"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func discoverTailscaleHostname() (string, error) {
	output, err := runShellCommand(context.Background(), tailscaleBinary, "status", "--json")
	if err != nil {
		return "", err
	}

	var status tailscaleStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return "", err
	}
	if status.Self.DNSName == "" {
		return "", fmt.Errorf("hostname not available from tailscale status")
	}
	return status.Self.DNSName, nil
}

type tailscaleStatus struct {
	Self struct {
		DNSName string `json:"DNSName"`
	} `json:"Self"`
}

var runShellCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		out := strings.TrimSpace(string(output))
		if out == "" {
			return output, fmt.Errorf("%s %s: %w", command, strings.Join(args, " "), err)
		}
		return output, fmt.Errorf("%s %s: %w (%s)", command, strings.Join(args, " "), err, out)
	}
	return output, nil
}
