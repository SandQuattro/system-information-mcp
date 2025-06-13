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

// SessionManager управляет MCP сессиями
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// Session представляет активную MCP сессию
type Session struct {
	ID        string
	CreatedAt time.Time
	SSEChan   chan interface{}
	mu        sync.Mutex
}

// NewSessionManager создает новый менеджер сессий
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// CreateSession создает новую сессию
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

// GetSession возвращает сессию по ID
func (sm *SessionManager) GetSession(id string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[id]
	return session, ok
}

// RemoveSession удаляет сессию
func (sm *SessionManager) RemoveSession(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session, ok := sm.sessions[id]; ok {
		close(session.SSEChan)
		delete(sm.sessions, id)
	}
}

// MCPHandler - HTTP обработчик для MCP сервера
type MCPHandler struct {
	server         *server.MCPServer
	sessionManager *SessionManager
}

func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Добавляем CORS заголовки
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

	// Обработка preflight OPTIONS запросов
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Обработка корневого GET-запроса
	if r.URL.Path == "/" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","message":"MCP System Info Server работает","version":"1.0.0"}`))
		return
	}

	// SSE endpoint для асинхронных сообщений
	if r.URL.Path == "/sse" && r.Method == http.MethodGet {
		h.handleSSE(w, r)
		return
	}

	// Обработка POST запросов для JSON-RPC
	if r.URL.Path == "/" && r.Method == http.MethodPost {
		h.handleJSONRPC(w, r)
		return
	}

	http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
}

// handleJSONRPC обрабатывает JSON-RPC запросы
func (h *MCPHandler) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Ошибка чтения запроса", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	log.Printf("Получен MCP запрос: %s", string(body))

	// Парсим JSON-RPC запрос
	var request map[string]interface{}
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, "Неверный JSON", http.StatusBadRequest)
		return
	}

	method, ok := request["method"].(string)
	if !ok {
		http.Error(w, "Отсутствует метод", http.StatusBadRequest)
		return
	}

	// Получаем session ID из заголовка
	sessionID := r.Header.Get("Mcp-Session-Id")

	// Обработка инициализации - единственный запрос без session ID
	if method == "initialize" {
		if sessionID != "" {
			writeJSONRPCError(w, request["id"], -32600, "Initialize не должен содержать Mcp-Session-Id")
			return
		}
		h.handleInitialize(w, request)
		return
	}

	// Для всех остальных запросов требуется session ID
	if sessionID == "" {
		writeJSONRPCError(w, request["id"], -32600, "Отсутствует Mcp-Session-Id")
		return
	}

	// Проверяем существование сессии
	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		writeJSONRPCError(w, request["id"], -32600, "Неверный Mcp-Session-Id")
		return
	}

	// Обрабатываем различные методы
	switch method {
	case "initialized":
		// Нотификация о завершении инициализации
		w.WriteHeader(http.StatusAccepted)
		return

	case "tools/list":
		h.handleToolsList(w, request, session)

	case "tools/call":
		h.handleToolCall(w, request, session)

	default:
		// Для нотификаций возвращаем 202 Accepted
		if request["id"] == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSONRPCError(w, request["id"], -32601, "Метод не найден")
	}
}

// handleInitialize обрабатывает запрос инициализации
func (h *MCPHandler) handleInitialize(w http.ResponseWriter, request map[string]interface{}) {
	// Создаем новую сессию
	sessionID := h.sessionManager.CreateSession()

	// Формируем ответ
	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      request["id"],
		"result": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "System Info Server",
				"version": "1.0.0",
			},
		},
	}

	// Добавляем заголовок с session ID
	w.Header().Set("Mcp-Session-Id", sessionID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleToolsList обрабатывает запрос списка инструментов
func (h *MCPHandler) handleToolsList(w http.ResponseWriter, request map[string]interface{}, session *Session) {
	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      request["id"],
		"result": map[string]interface{}{
			"tools": []map[string]interface{}{
				{
					"name":        "get_system_info",
					"description": "Получает информацию о системе: CPU и память",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
						"required":   []string{},
					},
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleToolCall обрабатывает вызов инструмента
func (h *MCPHandler) handleToolCall(w http.ResponseWriter, request map[string]interface{}, session *Session) {
	params, ok := request["params"].(map[string]interface{})
	if !ok {
		writeJSONRPCError(w, request["id"], -32602, "Неверные параметры")
		return
	}

	toolName, ok := params["name"].(string)
	if !ok || toolName != "get_system_info" {
		writeJSONRPCError(w, request["id"], -32601, "Неизвестный инструмент")
		return
	}

	// Получаем системную информацию
	sysInfo, err := sysinfo.Get()
	if err != nil {
		writeJSONRPCError(w, request["id"], -32603, err.Error())
		return
	}

	jsonData, _ := json.MarshalIndent(sysInfo, "", "  ")
	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      request["id"],
		"result": map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": string(jsonData),
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// writeJSONRPCError отправляет JSON-RPC ошибку
func writeJSONRPCError(w http.ResponseWriter, id interface{}, code int, message string) {
	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleSSE обрабатывает Server-Sent Events соединение
func (h *MCPHandler) handleSSE(w http.ResponseWriter, r *http.Request) {
	// Получаем session ID из заголовка
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "Отсутствует Mcp-Session-Id", http.StatusBadRequest)
		return
	}

	// Проверяем существование сессии
	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		http.Error(w, "Неверный Mcp-Session-Id", http.StatusBadRequest)
		return
	}

	// Настройка заголовков для SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Создаем flusher для отправки данных
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE не поддерживается", http.StatusInternalServerError)
		return
	}

	// Отправляем endpoint event согласно спецификации
	fmt.Fprintf(w, "event: endpoint\ndata: {\"url\":\"/\",\"method\":\"POST\"}\n\n")
	flusher.Flush()

	// Канал для завершения
	done := r.Context().Done()

	// Основной цикл обработки SSE
	for {
		select {
		case <-done:
			// Клиент отключился
			log.Printf("SSE клиент отключился для сессии %s", sessionID)
			return

		case message, ok := <-session.SSEChan:
			if !ok {
				// Канал закрыт
				return
			}

			// Отправляем сообщение клиенту
			jsonData, err := json.Marshal(message)
			if err != nil {
				log.Printf("Ошибка сериализации SSE сообщения: %v", err)
				continue
			}

			fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(jsonData))
			flusher.Flush()

		case <-time.After(30 * time.Second):
			// Отправляем ping для поддержания соединения
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// sendSSEMessage отправляет сообщение через SSE канал сессии
func (h *MCPHandler) sendSSEMessage(sessionID string, message interface{}) error {
	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		return fmt.Errorf("сессия не найдена")
	}

	select {
	case session.SSEChan <- message:
		return nil
	default:
		return fmt.Errorf("SSE канал заполнен")
	}
}

func main() {
	s := server.NewMCPServer(
		"System Info Server",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	// Создаем tool для получения системной информации
	systemInfoTool := mcp.NewTool("get_system_info",
		mcp.WithDescription("Получает информацию о системе: CPU и память"),
	)

	// Добавляем handler для tool'а
	s.AddTool(systemInfoTool, getSystemInfoHandler)

	// Определяем режим запуска (HTTP или stdio)
	port := os.Getenv("PORT")
	if port != "" {
		// Запускаем HTTP сервер, если указан порт
		portNum, err := strconv.Atoi(port)
		if err != nil {
			log.Printf("Ошибка преобразования порта: %v, использую порт 8080\n", err)
			portNum = 8080
		}

		// Создаем менеджер сессий
		sessionManager := NewSessionManager()

		// Создаем HTTP обработчик для MCP сервера
		handler := &MCPHandler{
			server:         s,
			sessionManager: sessionManager,
		}

		addr := fmt.Sprintf("0.0.0.0:%d", portNum)
		log.Printf("Запуск HTTP сервера на порту %d\n", portNum)
		log.Printf("SSE доступен по адресу http://%s/sse\n", addr)
		if err = http.ListenAndServe(addr, handler); err != nil {
			log.Fatalf("Ошибка HTTP сервера: %v\n", err)
		}
	} else {
		log.Println("Запуск сервера через stdio")
		if err := server.ServeStdio(s); err != nil {
			log.Printf("Ошибка сервера: %v\n", err)
		}
	}
}

func getSystemInfoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sysInfo, err := sysinfo.Get()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	jsonData, err := json.MarshalIndent(sysInfo, "", "  ")
	if err != nil {
		return mcp.NewToolResultError("Ошибка сериализации данных: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}
