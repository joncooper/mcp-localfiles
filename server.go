package main

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	rpcVersion = "2.0"
)

type MCPConfig struct {
	Root            string
	AuthToken       string
	ExcludeDotfiles bool
	ExcludeRegex    string
	MaxFileBytes    int64
	OnEvent         func(MCPEvent)
}

type MCPEvent struct {
	Timestamp  time.Time
	Client     string
	Method     string
	Tool       string
	Details    string
	Status     int
	Error      string
	Latency    time.Duration
	Authorized bool
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolResult struct {
	Content []ToolContent `json:"content"`
}

type MCPServer struct {
	fileManager *FileManager
	authToken   string
	maxFileSize int64
	onEvent     func(MCPEvent)
}

func NewMCPServer(cfg MCPConfig) (*MCPServer, error) {
	manager, err := NewFileManager(cfg.Root, cfg.ExcludeDotfiles, cfg.ExcludeRegex)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, errors.New("auth token is required for secure operation")
	}
	maxFileSize := cfg.MaxFileBytes
	if maxFileSize <= 0 {
		maxFileSize = defaultMaxFileBytes
	}
	return &MCPServer{
		fileManager: manager,
		authToken:   cfg.AuthToken,
		maxFileSize: maxFileSize,
		onEvent:     cfg.OnEvent,
	}, nil
}

func (s *MCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	client := extractClientAddr(r.RemoteAddr)
	rw := &mcpResponseWriter{ResponseWriter: w}

	method := "unknown"
	tool := "-"
	details := "incoming request"
	errMsg := ""
	authorized := false

	defer func() {
		if s.onEvent == nil {
			return
		}
		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}
		s.onEvent(MCPEvent{
			Timestamp:  time.Now(),
			Client:     client,
			Method:     method,
			Tool:       tool,
			Details:    details,
			Status:     status,
			Error:      errMsg,
			Latency:    time.Since(start),
			Authorized: authorized,
		})
	}()

	if r.Method != http.MethodPost {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = rw.Write([]byte("method not allowed"))
		method = "http"
		details = "method must be POST"
		errMsg = "method not allowed"
		return
	}

	s.addSecurityHeaders(rw)
	authorized = s.authorize(r)
	if !authorized {
		rw.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(rw, "unauthorized", http.StatusUnauthorized)
		method = "auth"
		details = "bearer token validation failed"
		errMsg = "unauthorized"
		return
	}

	reqBody := http.MaxBytesReader(rw, r.Body, maxBodyBytes)
	defer reqBody.Close()

	var req JSONRPCRequest
	if err := json.NewDecoder(reqBody).Decode(&req); err != nil {
		method = "parse"
		details = "invalid JSON payload"
		errMsg = "parse error: invalid JSON"
		writeRPCError(rw, req.ID, -32700, errMsg)
		return
	}

	method = strings.TrimSpace(req.Method)
	if method == "" {
		method = "invalid"
		details = "missing method"
		errMsg = "invalid request: method is required"
		writeRPCError(rw, req.ID, -32600, errMsg)
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != rpcVersion {
		details = "jsonrpc version mismatch"
		errMsg = "invalid request: jsonrpc must be 2.0"
		writeRPCError(rw, req.ID, -32600, errMsg)
		return
	}

	switch method {
	case "initialize":
		details = "initialize handshake"
		writeRPCResult(rw, req.ID, map[string]interface{}{
			"protocolVersion": rpcVersion,
			"capabilities": map[string]interface{}{
				"tools": map[string]bool{"listChanged": false},
			},
			"serverInfo": map[string]string{
				"name":    "localfiles-mcp",
				"version": "1.0.0",
			},
		})
	case "notifications/initialized":
		details = "notifications/initialized"
		rw.WriteHeader(http.StatusNoContent)
	case "tools/list":
		details = "tools/list"
		tools := []MCPTool{
			{
				Name:        "list_files",
				Description: "List directory entries from the configured root with safe filtering",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Relative path under the root. Example: \".\" or \"subdir\"",
						},
						"recursive": map[string]interface{}{
							"type":        "boolean",
							"description": "If true, recursively include nested files",
						},
					},
				},
			},
			{
				Name:        "read_file",
				Description: "Read file contents from the configured root",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Relative file path to read",
						},
						"max_bytes": map[string]interface{}{
							"type":        "integer",
							"description": "Override maximum returned bytes",
							"minimum":     1,
						},
					},
					"required": []string{"path"},
				},
			},
			{
				Name:        "search_files",
				Description: "Search for matching text within files under the configured root",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Relative directory path to search within",
							"default":     ".",
						},
						"query": map[string]interface{}{
							"type":        "string",
							"description": "Search query text or regex",
						},
						"case_sensitive": map[string]interface{}{
							"type":        "boolean",
							"description": "Match case exactly when true",
							"default":     false,
						},
						"regex": map[string]interface{}{
							"type":        "boolean",
							"description": "Interpret query as a regular expression",
							"default":     false,
						},
						"file_glob": map[string]interface{}{
							"type":        "string",
							"description": "Optional glob filter for matched files (e.g. \"*.go\")",
						},
						"max_matches": map[string]interface{}{
							"type":        "integer",
							"description": "Maximum number of matches returned (default 100, max 1000)",
							"minimum":     1,
							"maximum":     1000,
							"default":     100,
						},
						"max_bytes_per_file": map[string]interface{}{
							"type":        "integer",
							"description": "Skip files larger than this many bytes",
							"minimum":     1024,
						},
					},
					"required": []string{"query"},
				},
			},
		}
		writeRPCResult(rw, req.ID, map[string]interface{}{"tools": tools})
	case "tools/call":
		result, tool, toolDetails, err := s.callTool(req.Params)
		tool = "tools.call:" + tool
		details = toolDetails
		if err != nil {
			writeRPCError(rw, req.ID, -32602, err.Error())
			errMsg = err.Error()
			return
		}
		writeRPCResult(rw, req.ID, result)
	default:
		details = "method not found"
		errMsg = "method not found"
		writeRPCError(rw, req.ID, -32601, "method not found")
	}
}

func (s *MCPServer) callTool(rawParams json.RawMessage) (ToolResult, string, string, error) {
	if len(rawParams) == 0 {
		return ToolResult{}, "-", "missing params", errors.New("missing params")
	}
	var params ToolCallParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return ToolResult{}, "-", "invalid tool call object", errors.New("invalid params: expected tool call object")
	}
	if params.Name == "" {
		return ToolResult{}, "-", "tool name missing", errors.New("missing tool name")
	}
	switch params.Name {
	case "list_files":
		args := map[string]interface{}{}
		if len(params.Arguments) > 0 {
			if err := json.Unmarshal(params.Arguments, &args); err != nil {
				return ToolResult{}, params.Name, "invalid args for list_files", errors.New("invalid args for list_files")
			}
		}
		path := getStringArg(args, "path", ".")
		recursive := getBoolArg(args, "recursive", false)
		entries, err := s.fileManager.List(path, recursive)
		if err != nil {
			return ToolResult{}, params.Name, fmt.Sprintf("list_files path=%q recursive=%t", path, recursive), err
		}
		body, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return ToolResult{}, params.Name, fmt.Sprintf("list_files path=%q recursive=%t", path, recursive), err
		}
		return ToolResult{
			Content: []ToolContent{{Type: "text", Text: string(body)}},
		}, params.Name, fmt.Sprintf("list_files path=%q recursive=%t", path, recursive), nil
	case "read_file":
		args := map[string]interface{}{}
		if len(params.Arguments) > 0 {
			if err := json.Unmarshal(params.Arguments, &args); err != nil {
				return ToolResult{}, params.Name, "invalid args for read_file", errors.New("invalid args for read_file")
			}
		}
		path, ok := args["path"]
		if !ok {
			return ToolResult{}, params.Name, "missing required argument: path", errors.New("missing required argument: path")
		}
		pathStr, ok := path.(string)
		if !ok {
			return ToolResult{}, params.Name, "invalid path argument", errors.New("invalid path argument")
		}
		maxBytes := s.maxFileSize
		if raw, ok := args["max_bytes"]; ok {
			if n, ok := raw.(float64); ok && n > 0 {
				maxBytes = int64(n)
			} else {
				return ToolResult{}, params.Name, "invalid max_bytes argument", errors.New("invalid max_bytes argument")
			}
		}
		file, err := s.fileManager.ReadFile(pathStr, maxBytes)
		if err != nil {
			return ToolResult{}, params.Name, fmt.Sprintf("read_file path=%q", pathStr), err
		}
		content := file.Content
		encoding := "utf8"
		if !file.IsText {
			content = base64.StdEncoding.EncodeToString([]byte(file.Content))
			encoding = "base64"
		}
		payload := map[string]interface{}{
			"metadata": map[string]interface{}{
				"path":       file.Path,
				"size":       file.Size,
				"modTimeUtc": file.ModTimeUTC,
				"encoding":   encoding,
				"truncated":  file.Truncated,
			},
			"content": content,
		}
		body, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return ToolResult{}, params.Name, fmt.Sprintf("read_file path=%q", pathStr), err
		}
		return ToolResult{Content: []ToolContent{{Type: "text", Text: string(body)}}},
			params.Name, fmt.Sprintf("read_file path=%q max_bytes=%d", pathStr, maxBytes), nil
	case "search_files":
		args := map[string]interface{}{}
		if len(params.Arguments) > 0 {
			if err := json.Unmarshal(params.Arguments, &args); err != nil {
				return ToolResult{}, params.Name, "invalid args for search_files", errors.New("invalid args for search_files")
			}
		}
		path := "."
		if rawPath, ok := args["path"]; ok {
			pathValue, ok := rawPath.(string)
			if !ok {
				return ToolResult{}, params.Name, "invalid path argument", errors.New("invalid path argument")
			}
			path = pathValue
		}
		rawQuery, ok := args["query"]
		if !ok {
			return ToolResult{}, params.Name, "missing required argument: query", errors.New("missing required argument: query")
		}
		query, ok := rawQuery.(string)
		if !ok {
			return ToolResult{}, params.Name, "invalid query argument", errors.New("invalid query argument")
		}
		caseSensitive := false
		if rawCase, ok := args["case_sensitive"]; ok {
			caseValue, ok := rawCase.(bool)
			if !ok {
				return ToolResult{}, params.Name, "invalid case_sensitive argument", errors.New("invalid case_sensitive argument")
			}
			caseSensitive = caseValue
		}
		useRegex := false
		if rawRegex, ok := args["regex"]; ok {
			regexValue, ok := rawRegex.(bool)
			if !ok {
				return ToolResult{}, params.Name, "invalid regex argument", errors.New("invalid regex argument")
			}
			useRegex = regexValue
		}
		fileGlob := ""
		if rawFileGlob, ok := args["file_glob"]; ok {
			fileGlobValue, ok := rawFileGlob.(string)
			if !ok {
				return ToolResult{}, params.Name, "invalid file_glob argument", errors.New("invalid file_glob argument")
			}
			fileGlob = fileGlobValue
		}
		maxMatches, err := getIntArg(args, "max_matches", 100, 1, 1000)
		if err != nil {
			return ToolResult{}, params.Name, "invalid max_matches argument", err
		}
		maxBytesPerFile, err := getIntArg(args, "max_bytes_per_file", defaultSearchMaxBytesPerFile, 1024, 10*1024*1024)
		if err != nil {
			return ToolResult{}, params.Name, "invalid max_bytes_per_file argument", err
		}
		if strings.TrimSpace(query) == "" {
			return ToolResult{}, params.Name, "missing required argument: query", errors.New("missing required argument: query")
		}

		matches, truncated, err := s.fileManager.SearchFiles(path, SearchOptions{
			Query:           query,
			CaseSensitive:   caseSensitive,
			CaseSensitiveSet: true,
			Regex:           useRegex,
			FileGlob:        fileGlob,
			MaxMatches:      maxMatches,
			MaxBytesPerFile: int64(maxBytesPerFile),
		})
		if err != nil {
			return ToolResult{}, params.Name, fmt.Sprintf("search_files path=%q query=%q", path, query), err
		}
		payload := map[string]interface{}{
			"metadata": map[string]interface{}{
				"path":           path,
				"query":          query,
				"case_sensitive": caseSensitive,
				"regex":          useRegex,
				"file_glob":      fileGlob,
				"max_matches":    maxMatches,
				"truncated":      truncated,
				"result_count":   len(matches),
			},
			"matches": matches,
		}
		body, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return ToolResult{}, params.Name, fmt.Sprintf("search_files path=%q", path), err
		}
		return ToolResult{Content: []ToolContent{{Type: "text", Text: string(body)}}}, params.Name, fmt.Sprintf("search_files path=%q query=%q", path, query), nil
	default:
		return ToolResult{}, params.Name, "unknown tool", errors.New("unknown tool: " + params.Name)
	}
}

func getIntArg(args map[string]interface{}, key string, fallback int, min int, max int) (int, error) {
	raw, ok := args[key]
	if !ok {
		return fallback, nil
	}

	number, ok := raw.(float64)
	if !ok {
		return fallback, errors.New("must be a number")
	}
	if number != float64(int64(number)) {
		return fallback, errors.New("must be an integer")
	}
	value := int(number)
	if value < min || value > max {
		return fallback, errors.New("out of allowed range")
	}
	return value, nil
}

func getStringArg(args map[string]interface{}, key, fallback string) string {
	if raw, ok := args[key]; ok {
		if v, ok := raw.(string); ok {
			return v
		}
	}
	return fallback
}

func getBoolArg(args map[string]interface{}, key string, fallback bool) bool {
	if raw, ok := args[key]; ok {
		if v, ok := raw.(bool); ok {
			return v
		}
	}
	return fallback
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
	resultBody, err := json.Marshal(result)
	if err != nil {
		writeRPCError(w, id, -32603, "internal error")
		return
	}
	resp := JSONRPCResponse{
		JSONRPC: rpcVersion,
		ID:      id,
		Result:  resultBody,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: rpcVersion,
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	b, _ := json.Marshal(resp)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (s *MCPServer) authorize(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	given := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if given == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(s.authToken)) == 1
}

func (s *MCPServer) addSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func extractClientAddr(raw string) string {
	host, _, err := net.SplitHostPort(raw)
	if err == nil {
		return host
	}
	return raw
}

type mcpResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *mcpResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
