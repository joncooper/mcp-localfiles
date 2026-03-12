package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxEventBodyCapture = 4096

const (
	rpcVersion               = "2.0"
	mcpProtocolVersionLatest = "2025-11-25"
)

var supportedMCPProtocolVersions = map[string]struct{}{
	"2024-11-05": {},
	"2025-03-26": {},
	"2025-06-18": {},
	"2025-11-25": {},
}

type MCPConfig struct {
	Root            string
	AuthToken       string
	ExcludeDotfiles bool
	ExcludeRegex    string
	MaxFileBytes    int64
	AllowedOrigins  []string
	OnEvent         func(MCPEvent)
}

type MCPEvent struct {
	Timestamp     time.Time
	Client        string
	Method        string
	Tool          string
	Details       string
	Status        int
	Error         string
	Latency       time.Duration
	Authorized    bool
	RequestID     string
	RequestParams string
	ResponseBody  string
	RequestSize   int
	ResponseSize  int
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

type InitializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities,omitempty"`
	ClientInfo      map[string]interface{} `json:"clientInfo,omitempty"`
}

type MCPServer struct {
	fileManager    *FileManager
	authToken      string
	maxFileSize    int64
	allowedOrigins map[string]struct{}
	onEvent        func(MCPEvent)
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
	allowedOrigins, err := normalizeAllowedOrigins(cfg.AllowedOrigins)
	if err != nil {
		return nil, fmt.Errorf("normalize allowed origins: %w", err)
	}
	return &MCPServer{
		fileManager:    manager,
		authToken:      cfg.AuthToken,
		maxFileSize:    maxFileSize,
		allowedOrigins: allowedOrigins,
		onEvent:        cfg.OnEvent,
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
	requestID := ""
	requestParams := ""
	requestSize := 0

	defer func() {
		if s.onEvent == nil {
			return
		}
		s.onEvent(MCPEvent{
			Timestamp:     time.Now(),
			Client:        client,
			Method:        method,
			Tool:          tool,
			Details:       details,
			Status:        rw.status,
			Error:         errMsg,
			Latency:       time.Since(start),
			Authorized:    authorized,
			RequestID:     requestID,
			RequestParams: requestParams,
			RequestSize:   requestSize,
			ResponseSize:  rw.size,
		})
	}()

	s.addSecurityHeaders(rw)

	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = rw.Write([]byte("method not allowed"))
		method = "http"
		details = "method must be POST or GET"
		errMsg = "method not allowed"
		return
	}

	if !s.originAllowed(r.Header.Get("Origin")) {
		rw.WriteHeader(http.StatusForbidden)
		_, _ = rw.Write([]byte("forbidden"))
		method = "origin"
		details = "origin validation failed"
		errMsg = "forbidden"
		return
	}
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
	bodyBytes, readErr := io.ReadAll(reqBody)
	if readErr != nil {
		method = "parse"
		details = "request body read error"
		errMsg = "parse error: " + readErr.Error()
		writeRPCError(rw, nil, -32700, errMsg)
		return
	}
	requestSize = len(bodyBytes)

	var req JSONRPCRequest
	if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&req); err != nil {
		method = "parse"
		details = "invalid JSON payload"
		errMsg = "parse error: invalid JSON"
		writeRPCError(rw, req.ID, -32700, errMsg)
		return
	}

	if len(req.ID) > 0 {
		requestID = string(req.ID)
	}
	if len(req.Params) > 0 {
		p := string(req.Params)
		if len(p) > maxEventBodyCapture {
			p = p[:maxEventBodyCapture] + "..."
		}
		requestParams = p
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

	if method != "initialize" {
		protocolVersion := r.Header.Get("MCP-Protocol-Version")
		if protocolVersion == "" {
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte("missing MCP-Protocol-Version header"))
			details = "missing protocol version header"
			errMsg = "bad request"
			return
		}
		if _, ok := supportedMCPProtocolVersions[protocolVersion]; !ok {
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte("unsupported MCP-Protocol-Version header"))
			details = "unsupported protocol version"
			errMsg = "bad request"
			return
		}
	}

	switch method {
	case "initialize":
		protocolVersion, err := negotiateProtocolVersion(req.Params)
		if err != nil {
			details = "initialize handshake rejected"
			errMsg = err.Error()
			writeRPCError(rw, req.ID, -32602, errMsg)
			return
		}
		details = "initialize handshake"
		writeRPCResult(rw, req.ID, map[string]interface{}{
			"protocolVersion": protocolVersion,
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
		rw.WriteHeader(http.StatusAccepted)
	case "tools/list":
		details = "tools/list"
		tools := []MCPTool{
			{
				Name:        "list_files",
				Description: "List directory entries from the configured root with safe filtering. Supports glob patterns (e.g. \"**/*.go\") for finding files by name across the tree.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Relative path under the root. Example: \".\" or \"subdir\"",
						},
						"recursive": map[string]interface{}{
							"type":        "boolean",
							"description": "If true, recursively include nested files. Automatically enabled when glob is set.",
						},
						"glob": map[string]interface{}{
							"type":        "string",
							"description": "Glob pattern to filter results. Use ** for recursive matching (e.g. \"**/*.go\", \"src/**/*.ts\") and * for single-directory matching (e.g. \"*.json\"). When set, the walk is always recursive.",
						},
					},
				},
			},
			{
				Name:        "read_file",
				Description: "Read file contents from the configured root. Supports line-based partial reads with offset and limit for efficiently reading portions of large files.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Relative file path to read",
						},
						"max_bytes": map[string]interface{}{
							"type":        "integer",
							"description": "Override maximum returned bytes (only used when offset/limit are not set)",
							"minimum":     1,
						},
						"offset": map[string]interface{}{
							"type":        "integer",
							"description": "Line number to start reading from (1-based). When set, activates line-based mode and the response includes totalLines, startLine, and endLine metadata.",
							"minimum":     1,
						},
						"limit": map[string]interface{}{
							"type":        "integer",
							"description": "Maximum number of lines to return. Use with offset to read a specific range, e.g. offset=100, limit=50 reads lines 100-149.",
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
							"description": "Optional glob filter for matched files. Use ** for recursive matching (e.g. \"**/*.go\") and * for single-directory matching.",
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
		result, toolName, toolDetails, err := s.callTool(req.Params)
		tool = "tools.call:" + toolName
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

func negotiateProtocolVersion(rawParams json.RawMessage) (string, error) {
	if len(rawParams) == 0 {
		return "", errors.New("missing required initialize argument: protocolVersion")
	}

	var params InitializeParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", errors.New("invalid initialize params")
	}

	requested := strings.TrimSpace(params.ProtocolVersion)
	if requested == "" {
		return "", errors.New("missing required initialize argument: protocolVersion")
	}
	if _, ok := supportedMCPProtocolVersions[requested]; ok {
		return requested, nil
	}
	return mcpProtocolVersionLatest, nil
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
		path, err := getOptionalStringArg(args, "path", ".")
		if err != nil {
			return ToolResult{}, params.Name, "invalid path argument", errors.New("invalid path argument")
		}
		recursive, err := getOptionalBoolArg(args, "recursive", false)
		if err != nil {
			return ToolResult{}, params.Name, "invalid recursive argument", errors.New("invalid recursive argument")
		}
		glob, err := getOptionalStringArg(args, "glob", "")
		if err != nil {
			return ToolResult{}, params.Name, "invalid glob argument", errors.New("invalid glob argument")
		}
		entries, err := s.fileManager.List(path, recursive, glob)
		if err != nil {
			return ToolResult{}, params.Name, fmt.Sprintf("list_files path=%q recursive=%t glob=%q", path, recursive, glob), err
		}
		body, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return ToolResult{}, params.Name, fmt.Sprintf("list_files path=%q recursive=%t glob=%q", path, recursive, glob), err
		}
		return ToolResult{
			Content: []ToolContent{{Type: "text", Text: string(body)}},
		}, params.Name, fmt.Sprintf("list_files path=%q recursive=%t glob=%q", path, recursive, glob), nil
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
			n, err := getPositiveInt64Arg(raw, s.maxFileSize)
			if err != nil {
				return ToolResult{}, params.Name, "invalid max_bytes argument", errors.New("invalid max_bytes argument")
			}
			maxBytes = n
		}
		offset, err := getIntArg(args, "offset", 0, 1, 1<<30)
		if err != nil {
			return ToolResult{}, params.Name, "invalid offset argument", fmt.Errorf("invalid offset argument: %w", err)
		}
		lineLimit, err := getIntArg(args, "limit", 0, 1, 1<<30)
		if err != nil {
			return ToolResult{}, params.Name, "invalid limit argument", fmt.Errorf("invalid limit argument: %w", err)
		}
		file, err := s.fileManager.ReadFile(pathStr, maxBytes, offset, lineLimit)
		if err != nil {
			return ToolResult{}, params.Name, fmt.Sprintf("read_file path=%q", pathStr), err
		}
		content := file.Content
		encoding := "utf8"
		if !file.IsText {
			content = base64.StdEncoding.EncodeToString([]byte(file.Content))
			encoding = "base64"
		}
		metadata := map[string]interface{}{
			"path":       file.Path,
			"size":       file.Size,
			"modTimeUtc": file.ModTimeUTC,
			"encoding":   encoding,
			"truncated":  file.Truncated,
		}
		if file.TotalLines > 0 {
			metadata["totalLines"] = file.TotalLines
			metadata["startLine"] = file.StartLine
			metadata["endLine"] = file.EndLine
		}
		payload := map[string]interface{}{
			"metadata": metadata,
			"content":  content,
		}
		body, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return ToolResult{}, params.Name, fmt.Sprintf("read_file path=%q", pathStr), err
		}
		return ToolResult{Content: []ToolContent{{Type: "text", Text: string(body)}}},
			params.Name, fmt.Sprintf("read_file path=%q offset=%d limit=%d", pathStr, offset, lineLimit), nil
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
			Query:            query,
			CaseSensitive:    caseSensitive,
			CaseSensitiveSet: true,
			Regex:            useRegex,
			FileGlob:         fileGlob,
			MaxMatches:       maxMatches,
			MaxBytesPerFile:  int64(maxBytesPerFile),
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

func getPositiveInt64Arg(raw interface{}, max int64) (int64, error) {
	number, ok := raw.(float64)
	if !ok {
		return 0, errors.New("must be a number")
	}
	if number != float64(int64(number)) {
		return 0, errors.New("must be an integer")
	}
	value := int64(number)
	if value <= 0 {
		return 0, errors.New("must be positive")
	}
	if value > max {
		return 0, errors.New("out of allowed range")
	}
	return value, nil
}

func getOptionalStringArg(args map[string]interface{}, key, fallback string) (string, error) {
	raw, ok := args[key]
	if !ok {
		return fallback, nil
	}
	value, ok := raw.(string)
	if !ok {
		return fallback, errors.New("must be a string")
	}
	return value, nil
}

func getOptionalBoolArg(args map[string]interface{}, key string, fallback bool) (bool, error) {
	raw, ok := args[key]
	if !ok {
		return fallback, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return fallback, errors.New("must be a boolean")
	}
	return value, nil
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
	w.WriteHeader(http.StatusOK)
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

func (s *MCPServer) originAllowed(origin string) bool {
	if strings.TrimSpace(origin) == "" {
		return true
	}
	normalized, err := normalizeOrigin(origin)
	if err != nil {
		return false
	}
	if _, ok := s.allowedOrigins[normalized]; ok {
		return true
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *MCPServer) addSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func normalizeAllowedOrigins(origins []string) (map[string]struct{}, error) {
	normalized := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		value := strings.TrimSpace(origin)
		if value == "" {
			continue
		}
		key, err := normalizeOrigin(value)
		if err != nil {
			return nil, err
		}
		normalized[key] = struct{}{}
	}
	return normalized, nil
}

func normalizeOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return "", errors.New("origin must include scheme and host")
	}
	if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("origin must not include userinfo, path, query, or fragment")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
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
	size   int
}

func (w *mcpResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *mcpResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}
