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
	server               *server.MCPServer
	sessionManager       *SessionManager
	lastCreatedSessionID sync.Map // для временного хранения ID новой сессии
}

func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Логируем все запросы
	log.Printf("[HTTP] %s %s Headers: %v", r.Method, r.URL.Path, r.Header)

	// Добавляем CORS заголовки
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id, Accept, Last-Event-Id")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

	// Обработка preflight OPTIONS запросов
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Обработка корневого GET-запроса для проверки работоспособности
	if r.URL.Path == "/" && r.Method == http.MethodGet && r.Header.Get("Accept") != "text/event-stream" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","message":"MCP System Info Server работает","version":"1.0.0"}`))
		return
	}

	// MCP endpoint - единый endpoint согласно спецификации Streamable HTTP
	// Поддерживаем как корневой путь "/", так и "/sse" для обратной совместимости
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

// handleMCPPost обрабатывает POST запросы на MCP endpoint
func (h *MCPHandler) handleMCPPost(w http.ResponseWriter, r *http.Request) {
	// Проверяем Accept заголовок
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

	// Парсим JSON-RPC сообщение(я)
	var messages []map[string]interface{}

	// Пробуем распарсить как массив сообщений
	if err := json.Unmarshal(body, &messages); err != nil {
		// Если не массив, пробуем как одиночное сообщение
		var singleMessage map[string]interface{}
		if err := json.Unmarshal(body, &singleMessage); err != nil {
			log.Printf("[MCP POST] Ошибка парсинга JSON: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		messages = []map[string]interface{}{singleMessage}
	}

	// Определяем тип сообщений
	hasRequests := false
	responses := []map[string]interface{}{}

	for _, msg := range messages {
		if _, hasID := msg["id"]; hasID {
			if _, hasMethod := msg["method"]; hasMethod {
				// Это request
				hasRequests = true
			}
		}
	}

	// Если есть requests, обрабатываем и возвращаем ответы
	if hasRequests {
		sessionID := r.Header.Get("Mcp-Session-Id")

		for _, msg := range messages {
			if method, ok := msg["method"].(string); ok {
				response := h.handleJSONRPCMessage(msg, sessionID)
				if response != nil {
					// Для initialize добавляем session ID в заголовок
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

		// Возвращаем ответы как JSON (простой случай без SSE)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// Если один ответ - возвращаем объект, если несколько - массив
		if len(responses) == 1 {
			json.NewEncoder(w).Encode(responses[0])
		} else {
			json.NewEncoder(w).Encode(responses)
		}
	} else {
		// Только notifications или responses - возвращаем 202 Accepted
		w.WriteHeader(http.StatusAccepted)
	}
}

// handleMCPGet обрабатывает GET запросы на MCP endpoint (для SSE)
func (h *MCPHandler) handleMCPGet(w http.ResponseWriter, r *http.Request) {
	// Проверяем Accept заголовок
	acceptHeader := r.Header.Get("Accept")
	if !strings.Contains(acceptHeader, "text/event-stream") {
		log.Printf("[MCP GET] Accept header не содержит text/event-stream: %s", acceptHeader)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Проверка для обратной совместимости
	// Если клиент подключается к /sse, возможно это старый клиент
	if r.URL.Path == "/sse" {
		h.handleSSEWithBackwardCompatibility(w, r)
	} else {
		h.handleSSE(w, r)
	}
}

// handleMCPDelete обрабатывает DELETE запросы для завершения сессии
func (h *MCPHandler) handleMCPDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "Bad Request: Missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}

	if _, exists := h.sessionManager.GetSession(sessionID); !exists {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	h.sessionManager.RemoveSession(sessionID)
	w.WriteHeader(http.StatusNoContent)
}

// handleJSONRPCMessage обрабатывает одно JSON-RPC сообщение и возвращает ответ
func (h *MCPHandler) handleJSONRPCMessage(request map[string]interface{}, sessionID string) map[string]interface{} {
	method, ok := request["method"].(string)
	if !ok {
		return nil
	}

	log.Printf("[JSON-RPC] Обработка метода: %s, Session ID: %s", method, sessionID)

	// Обработка инициализации
	if method == "initialize" {
		if sessionID != "" {
			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"error": map[string]interface{}{
					"code":    -32600,
					"message": "Initialize не должен содержать Mcp-Session-Id",
				},
			}
		}
		return h.handleInitializeRequest(request)
	}

	// Для остальных методов нужна сессия
	if sessionID == "" {
		// Автоматическое создание сессии для некоторых методов
		if method == "tools/list" || method == "tools/call" {
			sessionID = h.sessionManager.CreateSession()
			h.lastCreatedSessionID.Store("sessionID", sessionID)
		} else if request["id"] != nil {
			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"error": map[string]interface{}{
					"code":    -32600,
					"message": "Отсутствует Mcp-Session-Id",
				},
			}
		}
	}

	// Проверяем сессию
	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists && request["id"] != nil {
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"error": map[string]interface{}{
				"code":    -32600,
				"message": "Неверный Mcp-Session-Id",
			},
		}
	}

	// Обрабатываем методы
	switch method {
	case "initialized":
		// Notification - нет ответа
		log.Printf("[JSON-RPC] Получена initialized нотификация")
		return nil

	case "tools/list":
		return h.handleToolsListRequest(request, session)

	case "tools/call":
		return h.handleToolCallRequest(request, session)

	default:
		if request["id"] != nil {
			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"error": map[string]interface{}{
					"code":    -32601,
					"message": "Метод не найден",
				},
			}
		}
		return nil
	}
}

// handleInitializeRequest обрабатывает запрос инициализации и возвращает ответ
func (h *MCPHandler) handleInitializeRequest(request map[string]interface{}) map[string]interface{} {
	log.Printf("[Initialize] Обработка запроса инициализации")

	// Создаем новую сессию
	sessionID := h.sessionManager.CreateSession()
	h.lastCreatedSessionID.Store("sessionID", sessionID)
	log.Printf("[Initialize] Создана новая сессия: %s", sessionID)

	// Формируем ответ
	return map[string]interface{}{
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
}

// handleToolsListRequest обрабатывает запрос списка инструментов и возвращает ответ
func (h *MCPHandler) handleToolsListRequest(request map[string]interface{}, session *Session) map[string]interface{} {
	log.Printf("[Tools/List] Обработка запроса списка инструментов")

	return map[string]interface{}{
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
}

// handleToolCallRequest обрабатывает вызов инструмента и возвращает ответ
func (h *MCPHandler) handleToolCallRequest(request map[string]interface{}, session *Session) map[string]interface{} {
	log.Printf("[Tools/Call] Обработка вызова инструмента")

	params, ok := request["params"].(map[string]interface{})
	if !ok {
		log.Printf("[Tools/Call] Ошибка: неверные параметры")
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"error": map[string]interface{}{
				"code":    -32602,
				"message": "Неверные параметры",
			},
		}
	}

	toolName, ok := params["name"].(string)
	if !ok || toolName != "get_system_info" {
		log.Printf("[Tools/Call] Ошибка: неизвестный инструмент: %v", toolName)
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"error": map[string]interface{}{
				"code":    -32601,
				"message": "Неизвестный инструмент",
			},
		}
	}

	log.Printf("[Tools/Call] Вызов инструмента: %s", toolName)

	// Получаем системную информацию
	sysInfo, err := sysinfo.Get()
	if err != nil {
		log.Printf("[Tools/Call] Ошибка получения системной информации: %v", err)
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"error": map[string]interface{}{
				"code":    -32603,
				"message": err.Error(),
			},
		}
	}

	jsonData, _ := json.MarshalIndent(sysInfo, "", "  ")
	return map[string]interface{}{
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
}

// handleSSE обрабатывает Server-Sent Events соединение
func (h *MCPHandler) handleSSE(w http.ResponseWriter, r *http.Request) {
	// Получаем session ID из заголовка или URL параметра
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		// Пробуем получить из URL параметра (для браузерных клиентов)
		sessionID = r.URL.Query().Get("sessionId")
		if sessionID == "" {
			sessionID = r.URL.Query().Get("session")
		}
	}

	log.Printf("[SSE] Попытка подключения с session ID: %s", sessionID)

	var session *Session
	var exists bool

	if sessionID == "" {
		// Для клиентов без сессии создаем новую
		log.Printf("[SSE] Session ID не предоставлен, создаем новую сессию")
		sessionID = h.sessionManager.CreateSession()
		session, exists = h.sessionManager.GetSession(sessionID)
		log.Printf("[SSE] Создана автоматическая сессия: %s", sessionID)

		// Отправляем session ID в заголовке ответа
		w.Header().Set("Mcp-Session-Id", sessionID)
	} else {
		// Проверяем существование сессии
		session, exists = h.sessionManager.GetSession(sessionID)
		if !exists {
			log.Printf("[SSE] Ошибка: неверный session ID: %s", sessionID)
			http.Error(w, "Bad Request: Invalid Mcp-Session-Id", http.StatusBadRequest)
			return
		}
	}

	// Настройка заголовков для SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no") // Для nginx

	// Создаем flusher для отправки данных
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("[SSE] Ошибка: SSE не поддерживается")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Согласно новой спецификации Streamable HTTP, сервер НЕ отправляет endpoint event
	// Сервер может отправлять только requests и notifications, но НЕ responses

	log.Printf("[SSE] Клиент подключился для сессии %s", sessionID)

	// Канал для завершения
	done := r.Context().Done()

	// Основной цикл обработки SSE
	for {
		select {
		case <-done:
			// Клиент отключился
			log.Printf("[SSE] Клиент отключился для сессии %s", sessionID)
			// Удаляем автоматически созданную сессию
			h.sessionManager.RemoveSession(sessionID)
			return

		case message, ok := <-session.SSEChan:
			if !ok {
				// Канал закрыт
				log.Printf("[SSE] Канал закрыт для сессии %s", sessionID)
				return
			}

			// Отправляем сообщение клиенту
			jsonData, err := json.Marshal(message)
			if err != nil {
				log.Printf("[SSE] Ошибка сериализации сообщения: %v", err)
				continue
			}

			// Отправляем как data-only event (без имени события)
			fmt.Fprintf(w, "data: %s\n\n", string(jsonData))
			flusher.Flush()

		case <-time.After(30 * time.Second):
			// Отправляем ping для поддержания соединения
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// handleSSEWithBackwardCompatibility обрабатывает SSE с поддержкой старого протокола
func (h *MCPHandler) handleSSEWithBackwardCompatibility(w http.ResponseWriter, r *http.Request) {
	// Получаем session ID из заголовка или URL параметра
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		// Пробуем получить из URL параметра (для браузерных клиентов)
		sessionID = r.URL.Query().Get("sessionId")
		if sessionID == "" {
			sessionID = r.URL.Query().Get("session")
		}
	}

	log.Printf("[SSE] Попытка подключения с session ID: %s (режим обратной совместимости)", sessionID)

	var session *Session
	var exists bool

	if sessionID == "" {
		// Для клиентов без сессии создаем новую
		log.Printf("[SSE] Session ID не предоставлен, создаем новую сессию")
		sessionID = h.sessionManager.CreateSession()
		session, exists = h.sessionManager.GetSession(sessionID)
		log.Printf("[SSE] Создана автоматическая сессия: %s", sessionID)

		// Отправляем session ID в заголовке ответа
		w.Header().Set("Mcp-Session-Id", sessionID)
	} else {
		// Проверяем существование сессии
		session, exists = h.sessionManager.GetSession(sessionID)
		if !exists {
			log.Printf("[SSE] Ошибка: неверный session ID: %s", sessionID)
			http.Error(w, "Bad Request: Invalid Mcp-Session-Id", http.StatusBadRequest)
			return
		}
	}

	// Настройка заголовков для SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no") // Для nginx

	// Создаем flusher для отправки данных
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("[SSE] Ошибка: SSE не поддерживается")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Для обратной совместимости отправляем endpoint event
	// (старый протокол 2024-11-05 требует это)
	endpointData := map[string]string{
		"url":    "/",
		"method": "POST",
	}
	endpointJSON, _ := json.Marshal(endpointData)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", string(endpointJSON))
	flusher.Flush()

	log.Printf("[SSE] Клиент подключился для сессии %s (отправлен endpoint event для обратной совместимости)", sessionID)

	// Канал для завершения
	done := r.Context().Done()

	// Основной цикл обработки SSE
	for {
		select {
		case <-done:
			// Клиент отключился
			log.Printf("[SSE] Клиент отключился для сессии %s", sessionID)
			// Удаляем автоматически созданную сессию
			h.sessionManager.RemoveSession(sessionID)
			return

		case message, ok := <-session.SSEChan:
			if !ok {
				// Канал закрыт
				log.Printf("[SSE] Канал закрыт для сессии %s", sessionID)
				return
			}

			// Отправляем сообщение клиенту
			jsonData, err := json.Marshal(message)
			if err != nil {
				log.Printf("[SSE] Ошибка сериализации сообщения: %v", err)
				continue
			}

			// Для старого протокола используем event: message
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
