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

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id, Accept, Last-Event-Id")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.URL.Path == "/" && r.Method == http.MethodGet && r.Header.Get("Accept") != "text/event-stream" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","message":"MCP System Info Server работает","version":"1.0.0"}`))
		return
	}

	if r.URL.Path == "/" || r.URL.Path == "/sse" {
		switch r.Method {
		case http.MethodPost:
			h.handleMCPPost(w, r)
		case http.MethodGet:
			h.handleMCPGet(w, r)
		case http.MethodDelete:
			h.handleMCPDelete(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	http.Error(w, "Not Found", http.StatusNotFound)
}

func (h *MCPHandler) handleMCPPost(w http.ResponseWriter, r *http.Request) {
	acceptHeader := r.Header.Get("Accept")
	log.Printf("[MCP POST] Accept header: %s", acceptHeader)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[MCP POST] Ошибка чтения тела запроса: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	log.Printf("[MCP POST] Получен запрос: %s", string(body))

	var messages []map[string]interface{}

	if err := json.Unmarshal(body, &messages); err != nil {
		var singleMessage map[string]interface{}
		if err := json.Unmarshal(body, &singleMessage); err != nil {
			log.Printf("[MCP POST] Ошибка парсинга JSON: %v", err)
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

func (h *MCPHandler) handleMCPGet(w http.ResponseWriter, r *http.Request) {
	acceptHeader := r.Header.Get("Accept")
	log.Printf("[MCP GET] Accept header: %s", acceptHeader)

	if acceptHeader == "text/event-stream" {
		h.handleSSE(w, r)
	} else {
		if r.URL.Path == "/sse" {
			h.handleSSEWithBackwardCompatibility(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	}
}

func (h *MCPHandler) handleMCPDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID != "" {
		log.Printf("[MCP DELETE] Удаляем сессию: %s", sessionID)
		h.sessionManager.RemoveSession(sessionID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","message":"Session terminated"}`))
}

func (h *MCPHandler) handleJSONRPCMessage(request map[string]interface{}, sessionID string) map[string]interface{} {
	log.Printf("[JSON-RPC] Получен запрос: %v", request)

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
	log.Printf("[INITIALIZE] Создана новая сессия: %s", sessionID)

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
					"description": "Получить информацию о системе (CPU и память)",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
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
					"message": fmt.Sprintf("Ошибка получения информации о системе: %v", err),
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
						"text": fmt.Sprintf("Системная информация:\n\nCPU:\n- Количество ядер: %d\n- Модель: %s\n- Загрузка: %.2f%%\n\nПамять:\n- Общая: %.2f GB\n- Доступная: %.2f GB\n- Используемая: %.2f GB (%.2f%%)",
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

func (h *MCPHandler) handleSSE(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("sessionId")
	}

	autoCreatedSession := false
	if sessionID == "" {
		sessionID = h.sessionManager.CreateSession()
		autoCreatedSession = true
		log.Printf("[SSE] Создана новая сессия: %s", sessionID)
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	done := make(chan struct{})

	go func() {
		select {
		case <-r.Context().Done():
			log.Printf("[SSE] Клиент отключился, сессия: %s", sessionID)
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

			fmt.Fprintf(w, "data: %s\n\n", jsonData)
			flusher.Flush()
		case <-pingTicker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (h *MCPHandler) handleSSEWithBackwardCompatibility(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("sessionId")
	}

	autoCreatedSession := false
	if sessionID == "" {
		sessionID = h.sessionManager.CreateSession()
		autoCreatedSession = true
		log.Printf("[SSE] Создана новая сессия для старого клиента: %s", sessionID)
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	endpointMessage := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/message",
		"params": map[string]interface{}{
			"level": "info",
			"text":  "Connected to MCP System Info Server",
		},
	}
	jsonData, _ := json.Marshal(endpointMessage)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", jsonData)
	flusher.Flush()

	done := make(chan struct{})

	go func() {
		select {
		case <-r.Context().Done():
			log.Printf("[SSE] Клиент отключился (старый протокол), сессия: %s", sessionID)
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

func (h *MCPHandler) sendSSEMessage(sessionID string, message interface{}) error {
	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		return fmt.Errorf("session not found")
	}

	select {
	case session.SSEChan <- message:
		return nil
	default:
		return fmt.Errorf("channel full")
	}
}

func main() {
	ctx := context.Background()

	systemInfoTool := mcp.Tool{
		Name:        "get_system_info",
		Description: "Получить информацию о системе (CPU и память)",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}

	mcpServer := server.NewMCPServer("mcp-system-info", "1.0.0")
	mcpServer.AddTool(systemInfoTool, getSystemInfoHandler)

	if port := os.Getenv("PORT"); port != "" {
		portInt, err := strconv.Atoi(port)
		if err != nil || portInt <= 0 {
			log.Fatal("Неверное значение PORT")
		}

		sessionManager := NewSessionManager()

		handler := &MCPHandler{
			server:         mcpServer,
			sessionManager: sessionManager,
		}

		addr := fmt.Sprintf(":%d", portInt)
		log.Printf("Запуск HTTP сервера на порту %s", port)
		log.Printf("SSE доступен по адресу http://localhost%s/sse", addr)

		if err := http.ListenAndServe(addr, handler); err != nil {
			log.Fatalf("Ошибка запуска HTTP сервера: %v", err)
		}
	} else {
		if err := mcpServer.Serve(ctx); err != nil {
			log.Fatalf("Ошибка запуска MCP сервера в stdio режиме: %v", err)
		}
	}
}

func getSystemInfoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sysInfo, err := sysinfo.Get()
	if err != nil {
		return &mcp.CallToolResult{
			Content: []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": fmt.Sprintf("Ошибка получения информации о системе: %v", err),
				},
			},
			IsError: true,
		}, nil
	}

	content := fmt.Sprintf("Системная информация:\n\nCPU:\n- Количество ядер: %d\n- Модель: %s\n- Загрузка: %.2f%%\n\nПамять:\n- Общая: %.2f GB\n- Доступная: %.2f GB\n- Используемая: %.2f GB (%.2f%%)",
		sysInfo.CPU.Count,
		sysInfo.CPU.ModelName,
		sysInfo.CPU.UsagePercent,
		float64(sysInfo.Memory.Total)/(1024*1024*1024),
		float64(sysInfo.Memory.Available)/(1024*1024*1024),
		float64(sysInfo.Memory.Used)/(1024*1024*1024),
		sysInfo.Memory.UsedPercent)

	return &mcp.CallToolResult{
		Content: []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": content,
			},
		},
		IsError: false,
	}, nil
}
