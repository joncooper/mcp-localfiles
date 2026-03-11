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
	resp := httptest.NewRecorder()
	srv.ServeHTTP(resp, req)
	return resp
}
