package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestMCPGetRequiresAuthInsteadOfReturningMethodNotAllowed(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	resp := httptest.NewRecorder()
	srv.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected GET /mcp without auth to return 401 once GET is supported, got %d", resp.Code)
	}
}

func TestMCPRejectsSubsequentRequestWithoutProtocolHeader(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	mustInitializeMCPForProtocolTests(t, srv)

	body := mustEncodeJSON(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	resp := postMCP(t, srv, body)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected tools/list without MCP-Protocol-Version header to return 400, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestMCPRejectsUnsupportedSubsequentProtocolHeader(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	mustInitializeMCPForProtocolTests(t, srv)

	body := mustEncodeJSON(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	resp := postMCPWithHeaders(t, srv, body, map[string]string{
		"MCP-Protocol-Version": "1999-01-01",
	})

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected unsupported MCP-Protocol-Version header to return 400, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func mustInitializeMCPForProtocolTests(t *testing.T, srv *MCPServer) {
	t.Helper()

	body := mustEncodeJSON(t, map[string]interface{}{
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
	})
	resp := postMCP(t, srv, body)
	if resp.Code != http.StatusOK {
		t.Fatalf("initialize returned %d body=%s", resp.Code, resp.Body.String())
	}

	var rpcResp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("initialize returned rpc error: %s", rpcResp.Error.Message)
	}
}

func mustEncodeJSON(t *testing.T, value interface{}) []byte {
	t.Helper()

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
	return body.Bytes()
}

func TestMCPSecurityHeadersOnNonPost(t *testing.T) {
	srv, root := newTestServer(t)
	defer os.RemoveAll(root)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	resp := httptest.NewRecorder()
	srv.ServeHTTP(resp, req)

	headers := []string{
		"X-Content-Type-Options",
		"X-Frame-Options",
		"Cache-Control",
	}

	for _, h := range headers {
		if resp.Header().Get(h) == "" {
			t.Errorf("expected security header %q on non-POST response, but it was missing", h)
		}
	}
}

func TestMCPSuccessfulRPCResponseRecordsStatusCode(t *testing.T) {
	var capturedStatus int
	srv := newTestServerWithEvents(t, func(evt MCPEvent) {
		capturedStatus = evt.Status
	})
	
	mustInitializeMCPForProtocolTests(t, srv)
	
	capturedStatus = 0

	body := mustEncodeJSON(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	resp := postMCPWithHeaders(t, srv, body, map[string]string{
		"MCP-Protocol-Version": "2025-06-18",
	})

	if resp.Code != http.StatusOK {
		t.Fatalf("expected tools/list to return 200, got %d", resp.Code)
	}
	
	if capturedStatus != http.StatusOK {
		t.Fatalf("expected recorded event status to be 200, got %d", capturedStatus)
	}
}
