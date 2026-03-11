package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testToken = "test-token"

func TestMCPAuthRequired(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	srv.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.Code)
	}
}

func TestMCPToolsList(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct{}       `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("unexpected rpc error")
	}

	var tools struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(rpcResp.Result, &tools); err != nil {
		t.Fatalf("decode tools: %v", err)
	}
	if len(tools.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools.Tools))
	}
}

func TestMCPInitializeNegotiatesProtocolVersion(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("unexpected rpc error: %s", rpcResp.Error.Message)
	}

	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities map[string]interface{} `json:"capabilities"`
	}
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != "2025-06-18" {
		t.Fatalf("expected negotiated protocol version 2025-06-18, got %q", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "localfiles-mcp" {
		t.Fatalf("expected server name, got %q", result.ServerInfo.Name)
	}
	if len(result.Capabilities) == 0 {
		t.Fatalf("expected capabilities in initialize result")
	}
}

func TestMCPInitializeReturnsLatestSupportedVersionForUnsupportedClientVersion(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "1999-01-01",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("unexpected rpc error: %s", rpcResp.Error.Message)
	}

	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != mcpProtocolVersionLatest {
		t.Fatalf("expected fallback protocol version %q, got %q", mcpProtocolVersionLatest, result.ProtocolVersion)
	}
}

func TestMCPInitializeRejectsMissingProtocolVersion(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"capabilities": map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	var rpcResp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpcResp.Error == nil {
		t.Fatalf("expected rpc error")
	}
	if rpcResp.Error.Message != "missing required initialize argument: protocolVersion" {
		t.Fatalf("expected missing protocolVersion error, got %q", rpcResp.Error.Message)
	}
}

func TestMCPInitializedNotificationReturnsAccepted(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", resp.Code, resp.Body.String())
	}
	if resp.Body.Len() != 0 {
		t.Fatalf("expected empty body for accepted notification, got %q", resp.Body.String())
	}
}

func TestMCPLocalOriginIsAllowed(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	resp := postMCPWithHeaders(t, srv, body, map[string]string{
		"Origin": "http://localhost:3000",
	})

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestMCPRejectsUnexpectedOrigin(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	resp := postMCPWithHeaders(t, srv, body, map[string]string{
		"Origin": "https://evil.example",
	})

	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestMCPAllowsConfiguredRemoteOrigin(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha-beta"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	srv, err := NewMCPServer(MCPConfig{
		Root:            root,
		AuthToken:       testToken,
		ExcludeDotfiles: true,
		MaxFileBytes:    1024,
		AllowedOrigins:  []string{"https://device.example.ts.net"},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	resp := postMCPWithHeaders(t, srv, body, map[string]string{
		"Origin": "https://device.example.ts.net",
	})

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestMCPToolReadFile(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "read_file",
			"arguments": map[string]interface{}{
				"path": "note.txt",
			},
		},
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("tool call failed: %s", rpcResp.Error.Message)
	}

	var tool ToolResult
	if err := json.Unmarshal(rpcResp.Result, &tool); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if len(tool.Content) == 0 {
		t.Fatalf("expected content in tool response")
	}
	var payload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(tool.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode tool payload: %v", err)
	}
	if !strings.Contains(payload.Content, "alpha") {
		t.Fatalf("expected file text in payload, got %q", payload.Content)
	}
}

func TestMCPToolReadFileRejectsMaxBytesAboveServerCap(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("abcdefghij"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	srv, err := NewMCPServer(MCPConfig{
		Root:            root,
		AuthToken:       testToken,
		ExcludeDotfiles: true,
		MaxFileBytes:    4,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "read_file",
			"arguments": map[string]interface{}{
				"path":      "note.txt",
				"max_bytes": 8,
			},
		},
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	var rpcResp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpcResp.Error == nil {
		t.Fatalf("expected rpc error")
	}
	if rpcResp.Error.Message != "invalid max_bytes argument" {
		t.Fatalf("expected invalid max_bytes error, got %q", rpcResp.Error.Message)
	}
}

func TestMCPToolReadFileRejectsFractionalMaxBytes(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "read_file",
			"arguments": map[string]interface{}{
				"path":      "note.txt",
				"max_bytes": 1.5,
			},
		},
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	var rpcResp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpcResp.Error == nil {
		t.Fatalf("expected rpc error")
	}
	if rpcResp.Error.Message != "invalid max_bytes argument" {
		t.Fatalf("expected invalid max_bytes error, got %q", rpcResp.Error.Message)
	}
}

func TestMCPToolSearchFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("first line\ncontains alpha\nsecond alpha line\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	srv := newTestServerRoot(t, root)
	defer os.RemoveAll(root)

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "search_files",
			"arguments": map[string]interface{}{
				"path":        ".",
				"query":       "alpha",
				"max_matches": 10,
				"file_glob":   "*.txt",
			},
		},
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("tool call failed: %s", rpcResp.Error.Message)
	}

	var tool ToolResult
	if err := json.Unmarshal(rpcResp.Result, &tool); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if len(tool.Content) == 0 {
		t.Fatalf("expected content in tool response")
	}

	var payload struct {
		Metadata struct {
			ResultCount int `json:"result_count"`
		} `json:"metadata"`
		Matches []interface{} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(tool.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode tool payload: %v", err)
	}
	if payload.Metadata.ResultCount < 2 {
		t.Fatalf("expected at least 2 matches, got %d", payload.Metadata.ResultCount)
	}
	if len(payload.Matches) == 0 {
		t.Fatalf("expected matches list to be non-empty")
	}
}

func TestMCPToolSearchFilesRejectsInvalidArgs(t *testing.T) {
	root := t.TempDir()
	srv := newTestServerRoot(t, root)
	defer os.RemoveAll(root)

	cases := []struct {
		name      string
		arguments map[string]interface{}
		wantMsg   string
	}{
		{
			name: "path must be string",
			arguments: map[string]interface{}{
				"path":           1,
				"query":          "alpha",
				"max_matches":    10,
				"file_glob":      "*.txt",
				"case_sensitive": true,
			},
			wantMsg: "invalid path argument",
		},
		{
			name: "query must be string",
			arguments: map[string]interface{}{
				"query":       123,
				"max_matches": 10,
			},
			wantMsg: "invalid query argument",
		},
		{
			name: "file_glob must be string",
			arguments: map[string]interface{}{
				"query":       "alpha",
				"file_glob":   123,
				"max_matches": 10,
			},
			wantMsg: "invalid file_glob argument",
		},
		{
			name: "case_sensitive must be bool",
			arguments: map[string]interface{}{
				"query":          "alpha",
				"case_sensitive": "true",
			},
			wantMsg: "invalid case_sensitive argument",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      99,
				"method":  "tools/call",
				"params": map[string]interface{}{
					"name":      "search_files",
					"arguments": tc.arguments,
				},
			}
			var reqBody bytes.Buffer
			if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
				t.Fatalf("encode request: %v", err)
			}

			resp := postMCP(t, srv, reqBody.Bytes())
			if resp.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
			}

			var rpcResp struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
				t.Fatalf("decode rpc response: %v", err)
			}
			if rpcResp.Error == nil {
				t.Fatalf("expected rpc error")
			}
			if rpcResp.Error.Message != tc.wantMsg {
				t.Fatalf("expected error %q, got %q", tc.wantMsg, rpcResp.Error.Message)
			}
		})
	}
}

func TestMCPToolListFilesRejectsInvalidArgs(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	cases := []struct {
		name      string
		arguments map[string]interface{}
		wantMsg   string
	}{
		{
			name: "path must be string",
			arguments: map[string]interface{}{
				"path":      123,
				"recursive": false,
			},
			wantMsg: "invalid path argument",
		},
		{
			name: "recursive must be bool",
			arguments: map[string]interface{}{
				"path":      ".",
				"recursive": "true",
			},
			wantMsg: "invalid recursive argument",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      98,
				"method":  "tools/call",
				"params": map[string]interface{}{
					"name":      "list_files",
					"arguments": tc.arguments,
				},
			}
			var reqBody bytes.Buffer
			if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
				t.Fatalf("encode request: %v", err)
			}

			resp := postMCP(t, srv, reqBody.Bytes())
			if resp.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
			}

			var rpcResp struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
				t.Fatalf("decode rpc response: %v", err)
			}
			if rpcResp.Error == nil {
				t.Fatalf("expected rpc error")
			}
			if rpcResp.Error.Message != tc.wantMsg {
				t.Fatalf("expected error %q, got %q", tc.wantMsg, rpcResp.Error.Message)
			}
		})
	}
}

func TestMCPToolSearchFilesRejectsUnsafeRegex(t *testing.T) {
	root := t.TempDir()
	srv := newTestServerRoot(t, root)
	defer os.RemoveAll(root)

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      77,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "search_files",
			"arguments": map[string]interface{}{
				"query": "(a+)+",
				"regex": true,
			},
		},
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	var rpcResp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpcResp.Error == nil {
		t.Fatalf("expected rpc error")
	}
	if !strings.Contains(rpcResp.Error.Message, "invalid regex") {
		t.Fatalf("expected invalid regex error, got %q", rpcResp.Error.Message)
	}
}

func TestMCPEmitsRequestEvent(t *testing.T) {
	events := make(chan MCPEvent, 4)
	srv := newTestServerWithEvents(t, func(evt MCPEvent) {
		events <- evt
	})

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/list",
	}
	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(body); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	resp := postMCP(t, srv, reqBody.Bytes())
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	select {
	case evt := <-events:
		if evt.Method != "tools/list" {
			t.Fatalf("expected method tools/list, got %q", evt.Method)
		}
		if evt.Status != http.StatusOK {
			t.Fatalf("expected 200 status, got %d", evt.Status)
		}
	default:
		t.Fatalf("expected request event")
	}
}

func newTestServer(t *testing.T) (*MCPServer, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha-beta"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".secret.txt"), []byte("hide"), 0o644); err != nil {
		t.Fatalf("write hidden file: %v", err)
	}
	return newTestServerRoot(t, root), root
}

func newTestServerRoot(t *testing.T, root string) *MCPServer {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha-beta"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".secret.txt"), []byte("hide"), 0o644); err != nil {
		t.Fatalf("write hidden file: %v", err)
	}
	srv, err := NewMCPServer(MCPConfig{
		Root:            root,
		AuthToken:       testToken,
		ExcludeDotfiles: true,
		MaxFileBytes:    1024,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv
}

func newTestServerWithEvents(t *testing.T, onEvent func(MCPEvent)) *MCPServer {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha-beta"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	srv, err := NewMCPServer(MCPConfig{
		Root:            root,
		AuthToken:       testToken,
		ExcludeDotfiles: true,
		MaxFileBytes:    1024,
		OnEvent:         onEvent,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv
}

func postMCP(t *testing.T, srv *MCPServer, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	resp := httptest.NewRecorder()
	srv.ServeHTTP(resp, req)
	return resp
}

func postMCPWithHeaders(t *testing.T, srv *MCPServer, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp := httptest.NewRecorder()
	srv.ServeHTTP(resp, req)
	return resp
}
