package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"mcp-system-info/internal/sysinfo"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

type Session struct {
	ID        string
	CreatedAt time.Time
	SSEChan   chan interface{}
	mu        sync.Mutex
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

func (sm *SessionManager) CreateSession() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sessionID := uuid.New().String()
	sm.sessions[sessionID] = &Session{
		ID:        sessionID,
		CreatedAt: time.Now(),
		SSEChan:   make(chan interface{}, 100),
	}
	return sessionID
}

func (sm *SessionManager) GetSession(id string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[id]
	return session, ok
}

func (sm *SessionManager) RemoveSession(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session, ok := sm.sessions[id]; ok {
		close(session.SSEChan)
		delete(sm.sessions, id)
	}
}

type MCPHandler struct {
	server               *server.MCPServer
	sessionManager       *SessionManager
	lastCreatedSessionID sync.Map
}

func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("[HTTP] %s %s Headers: %v", r.Method, r.URL.Path, r.Header)

	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id, Accept, Last-Event-Id")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Streamable HTTP на корневом маршруте
	if r.URL.Path == "/" {
		switch r.Method {
		case http.MethodPost:
			h.handleStreamableHTTPPost(w, r)
		case http.MethodGet:
			h.handleStreamableHTTPGet(w, r)
		case http.MethodDelete:
			h.handleStreamableHTTPDelete(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Backward compatibility для SSE маршрута
	if r.URL.Path == "/sse" {
		switch r.Method {
		case http.MethodPost:
			h.handleLegacyPost(w, r)
		case http.MethodGet:
			h.handleLegacySSE(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	http.Error(w, "Not Found", http.StatusNotFound)
}

// Streamable HTTP POST согласно спецификации 2025-03-26
func (h *MCPHandler) handleStreamableHTTPPost(w http.ResponseWriter, r *http.Request) {
	acceptHeader := r.Header.Get("Accept")
	log.Printf("[Streamable HTTP POST] Accept header: %s", acceptHeader)

	// Проверяем поддерживаемые Accept headers
	supportsJSON := strings.Contains(acceptHeader, "application/json") || strings.Contains(acceptHeader, "*/*")
	supportsSSE := strings.Contains(acceptHeader, "text/event-stream")

	if !supportsJSON && !supportsSSE {
		http.Error(w, "Not Acceptable", http.StatusNotAcceptable)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[Streamable HTTP POST] Error reading request body: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	log.Printf("[Streamable HTTP POST] Received request: %s", string(body))

	var messages []map[string]interface{}

	// Парсим JSON - может быть одиночное сообщение или массив
	if err := json.Unmarshal(body, &messages); err != nil {
		var singleMessage map[string]interface{}
		if err := json.Unmarshal(body, &singleMessage); err != nil {
			log.Printf("[Streamable HTTP POST] JSON parsing error: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		messages = []map[string]interface{}{singleMessage}
	}

	// Анализируем типы сообщений
	hasRequests := false
	onlyResponsesOrNotifications := true

	for _, msg := range messages {
		if _, hasID := msg["id"]; hasID {
			if _, hasMethod := msg["method"]; hasMethod {
				// Это request
				hasRequests = true
				onlyResponsesOrNotifications = false
			} else {
				// Это response
				continue
			}
		} else {
			// Это notification
			continue
		}
	}

	sessionID := r.Header.Get("Mcp-Session-Id")

	// Если только responses или notifications
	if onlyResponsesOrNotifications {
		for _, msg := range messages {
			h.handleJSONRPCMessage(msg, sessionID)
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Если есть requests, обрабатываем их
	if hasRequests {
		responses := []map[string]interface{}{}

		for _, msg := range messages {
			if _, hasMethod := msg["method"]; hasMethod {
				response := h.handleJSONRPCMessage(msg, sessionID)
				if response != nil {
					// Для initialize запроса добавляем Session ID в заголовок
					if method, ok := msg["method"].(string); ok && method == "initialize" {
						if response["result"] != nil {
							if value, ok := h.lastCreatedSessionID.Load("sessionID"); ok {
								if newSessionID, ok := value.(string); ok {
									w.Header().Set("Mcp-Session-Id", newSessionID)
									h.lastCreatedSessionID.Delete("sessionID")
								}
							}
						}
					}
					responses = append(responses, response)
				}
			}
		}

		// Возвращаем в зависимости от Accept header
		if supportsSSE && len(responses) > 0 {
			// Возвращаем SSE поток
			h.handleStreamableHTTPSSE(w, r, responses)
		} else {
			// Возвращаем JSON
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			if len(responses) == 1 {
				json.NewEncoder(w).Encode(responses[0])
			} else {
				json.NewEncoder(w).Encode(responses)
			}
		}
	}
}

// Streamable HTTP GET согласно спецификации 2025-03-26
func (h *MCPHandler) handleStreamableHTTPGet(w http.ResponseWriter, r *http.Request) {
	acceptHeader := r.Header.Get("Accept")
	log.Printf("[Streamable HTTP GET] Accept header: %s", acceptHeader)

	if strings.Contains(acceptHeader, "text/event-stream") {
		h.handleStreamableHTTPSSE(w, r, nil)
		return
	}

	// Для обычных GET запросов возвращаем информацию о сервере
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","message":"MCP System Info Server is running","version":"1.0.0","protocol":"2025-03-26","endpoints":{"mcp":"/","legacy_sse":"/sse"}}`))
}

// Streamable HTTP DELETE согласно спецификации 2025-03-26
func (h *MCPHandler) handleStreamableHTTPDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "Bad Request: Missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}

	log.Printf("[Streamable HTTP DELETE] Terminating session: %s", sessionID)
	h.sessionManager.RemoveSession(sessionID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","message":"Session terminated"}`))
}

// SSE обработка для Streamable HTTP
func (h *MCPHandler) handleStreamableHTTPSSE(w http.ResponseWriter, r *http.Request, initialResponses []map[string]interface{}) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	lastEventID := r.Header.Get("Last-Event-Id")

	log.Printf("[Streamable HTTP SSE] Session ID: %s, Last-Event-ID: %s", sessionID, lastEventID)

	autoCreatedSession := false
	if sessionID == "" {
		sessionID = h.sessionManager.CreateSession()
		autoCreatedSession = true
		log.Printf("[Streamable HTTP SSE] Created new session: %s", sessionID)
	}

	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	if autoCreatedSession {
		w.Header().Set("Mcp-Session-Id", sessionID)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Отправляем initial responses если есть
	eventCounter := 0
	if initialResponses != nil {
		for _, response := range initialResponses {
			jsonData, _ := json.Marshal(response)
			eventCounter++
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", eventCounter, jsonData)
			flusher.Flush()
		}
	}

	done := make(chan struct{})

	go func() {
		select {
		case <-r.Context().Done():
			log.Printf("[Streamable HTTP SSE] Client disconnected, session: %s", sessionID)
			if autoCreatedSession {
				h.sessionManager.RemoveSession(sessionID)
			}
			close(done)
		case <-done:
		}
	}()

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-done:
			return
		case message, ok := <-session.SSEChan:
			if !ok {
				return
			}

			jsonData, err := json.Marshal(message)
			if err != nil {
				continue
			}

			eventCounter++
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", eventCounter, jsonData)
			flusher.Flush()
		case <-pingTicker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// Legacy поддержка для backward compatibility
func (h *MCPHandler) handleLegacyPost(w http.ResponseWriter, r *http.Request) {
	acceptHeader := r.Header.Get("Accept")
	log.Printf("[Legacy POST] Accept header: %s", acceptHeader)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[Legacy POST] Error reading request body: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	log.Printf("[Legacy POST] Received request: %s", string(body))

	var messages []map[string]interface{}

	if err := json.Unmarshal(body, &messages); err != nil {
		var singleMessage map[string]interface{}
		if err := json.Unmarshal(body, &singleMessage); err != nil {
			log.Printf("[Legacy POST] JSON parsing error: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		messages = []map[string]interface{}{singleMessage}
	}

	hasRequests := false
	responses := []map[string]interface{}{}

	for _, msg := range messages {
		if _, hasID := msg["id"]; hasID {
			if _, hasMethod := msg["method"]; hasMethod {
				hasRequests = true
			}
		}
	}

	if hasRequests {
		sessionID := r.Header.Get("Mcp-Session-Id")

		for _, msg := range messages {
			if method, ok := msg["method"].(string); ok {
				response := h.handleJSONRPCMessage(msg, sessionID)
				if response != nil {
					if method == "initialize" && response["result"] != nil {
						if value, ok := h.lastCreatedSessionID.Load("sessionID"); ok {
							if newSessionID, ok := value.(string); ok {
								w.Header().Set("Mcp-Session-Id", newSessionID)
								h.lastCreatedSessionID.Delete("sessionID")
							}
						}
					}
					responses = append(responses, response)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if len(responses) == 1 {
			json.NewEncoder(w).Encode(responses[0])
		} else {
			json.NewEncoder(w).Encode(responses)
		}
	} else {
		w.WriteHeader(http.StatusAccepted)
	}
}

func (h *MCPHandler) handleLegacySSE(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("sessionId")
	}

	autoCreatedSession := false
	if sessionID == "" {
		sessionID = h.sessionManager.CreateSession()
		autoCreatedSession = true
		log.Printf("[Legacy SSE] Created new session: %s", sessionID)
	}

	w.Header().Set("Mcp-Session-Id", sessionID)

	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Legacy endpoint event для обратной совместимости
	endpointMessage := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/message",
		"params": map[string]interface{}{
			"level": "info",
			"text":  "Connected to MCP System Info Server (Legacy SSE)",
		},
	}
	jsonData, _ := json.Marshal(endpointMessage)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", jsonData)
	flusher.Flush()

	done := make(chan struct{})

	go func() {
		select {
		case <-r.Context().Done():
			log.Printf("[Legacy SSE] Client disconnected, session: %s", sessionID)
			if autoCreatedSession {
				h.sessionManager.RemoveSession(sessionID)
			}
			close(done)
		case <-done:
		}
	}()

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-done:
			return
		case message, ok := <-session.SSEChan:
			if !ok {
				return
			}

			jsonData, err := json.Marshal(message)
			if err != nil {
				continue
			}

			fmt.Fprintf(w, "event: message\ndata: %s\n\n", jsonData)
			flusher.Flush()
		case <-pingTicker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (h *MCPHandler) handleJSONRPCMessage(request map[string]interface{}, sessionID string) map[string]interface{} {
	log.Printf("[JSON-RPC] Received request: %v", request)

	method, hasMethod := request["method"].(string)
	id, hasID := request["id"]

	if !hasMethod {
		return nil
	}

	if method == "initialize" {
		return h.handleInitializeRequest(request)
	}

	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		if hasID {
			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]interface{}{
					"code":    -32001,
					"message": "Session not found",
				},
			}
		}
		return nil
	}

	switch method {
	case "tools/list":
		if !hasID {
			return nil
		}
		return h.handleToolsListRequest(request, session)

	case "tools/call":
		if !hasID {
			return nil
		}
		return h.handleToolCallRequest(request, session)

	default:
		if hasID {
			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]interface{}{
					"code":    -32601,
					"message": "Method not found",
				},
			}
		}
		return nil
	}
}

func (h *MCPHandler) handleInitializeRequest(request map[string]interface{}) map[string]interface{} {
	id := request["id"]

	sessionID := h.sessionManager.CreateSession()
	log.Printf("[INITIALIZE] Created new session: %s", sessionID)

	h.lastCreatedSessionID.Store("sessionID", sessionID)

	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "mcp-system-info",
				"version": "1.0.0",
			},
		},
	}
}

func (h *MCPHandler) handleToolsListRequest(request map[string]interface{}, session *Session) map[string]interface{} {
	id := request["id"]

	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]interface{}{
			"tools": []map[string]interface{}{
				{
					"name":        "get_system_info",
					"description": "Gets system information: CPU and memory",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"random_string": map[string]interface{}{
								"type":        "string",
								"description": "Dummy parameter for no-parameter tools",
							},
						},
						"required": []string{"random_string"},
					},
				},
			},
		},
	}
}

func (h *MCPHandler) handleToolCallRequest(request map[string]interface{}, session *Session) map[string]interface{} {
	id := request["id"]
	params, ok := request["params"].(map[string]interface{})
	if !ok {
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]interface{}{
				"code":    -32602,
				"message": "Invalid params",
			},
		}
	}

	toolName, ok := params["name"].(string)
	if !ok {
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]interface{}{
				"code":    -32602,
				"message": "Missing tool name",
			},
		}
	}

	if toolName == "get_system_info" {
		sysInfo, err := sysinfo.Get()
		if err != nil {
			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]interface{}{
					"code":    -32603,
					"message": fmt.Sprintf("Error getting system information: %v", err),
				},
			}
		}

		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result": map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": fmt.Sprintf("System Information:\n\nCPU:\n- Core count: %d\n- Model: %s\n- Usage: %.2f%%\n\nMemory:\n- Total: %.2f GB\n- Available: %.2f GB\n- Used: %.2f GB (%.2f%%)",
							sysInfo.CPU.Count,
							sysInfo.CPU.ModelName,
							sysInfo.CPU.UsagePercent,
							float64(sysInfo.Memory.Total)/(1024*1024*1024),
							float64(sysInfo.Memory.Available)/(1024*1024*1024),
							float64(sysInfo.Memory.Used)/(1024*1024*1024),
							sysInfo.Memory.UsedPercent),
					},
				},
				"isError": false,
			},
		}
	}

	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    -32601,
			"message": "Tool not found",
		},
	}
}

func main() {
	systemInfoTool := mcp.NewTool("get_system_info",
		mcp.WithDescription("Gets system information: CPU and memory"),
		mcp.WithString("random_string",
			mcp.Required(),
			mcp.Description("Dummy parameter for no-parameter tools"),
		),
	)

	mcpServer := server.NewMCPServer("mcp-system-info", "1.0.0")
	mcpServer.AddTool(systemInfoTool, getSystemInfoHandler)

	if port := os.Getenv("PORT"); port != "" {
		portInt, err := strconv.Atoi(port)
		if err != nil || portInt <= 0 {
			log.Fatal("Invalid PORT value")
		}

		sessionManager := NewSessionManager()

		handler := &MCPHandler{
			server:         mcpServer,
			sessionManager: sessionManager,
		}

		addr := fmt.Sprintf(":%d", portInt)
		log.Printf("Starting HTTP server on port %s", port)
		log.Printf("SSE available at http://%s/sse", addr)

		if err = http.ListenAndServe(addr, handler); err != nil {
			log.Fatalf("Error starting HTTP server: %v", err)
		}
	} else {
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Error starting MCP server in stdio mode: %v", err)
		}
	}
}

func getSystemInfoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sysInfo, err := sysinfo.Get()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error getting system information: %v", err)), nil
	}

	content := fmt.Sprintf("System Information:\n\nCPU:\n- Core count: %d\n- Model: %s\n- Usage: %.2f%%\n\nMemory:\n- Total: %.2f GB\n- Available: %.2f GB\n- Used: %.2f GB (%.2f%%)",
		sysInfo.CPU.Count,
		sysInfo.CPU.ModelName,
		sysInfo.CPU.UsagePercent,
		float64(sysInfo.Memory.Total)/(1024*1024*1024),
		float64(sysInfo.Memory.Available)/(1024*1024*1024),
		float64(sysInfo.Memory.Used)/(1024*1024*1024),
		sysInfo.Memory.UsedPercent)

	return mcp.NewToolResultText(content), nil
}
