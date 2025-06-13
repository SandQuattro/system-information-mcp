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
	// Логируем все запросы
	log.Printf("[HTTP] %s %s Headers: %v", r.Method, r.URL.Path, r.Header)

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
	if r.URL.Path == "/sse" && (r.Method == http.MethodGet || r.Method == http.MethodPost) {
		// n8n может отправлять POST запрос с конфигурацией
		if r.Method == http.MethodPost {
			// Читаем тело запроса (n8n может отправлять конфигурацию)
			body, _ := io.ReadAll(r.Body)
			defer r.Body.Close()
			log.Printf("[SSE] POST запрос с телом: %s", string(body))
		}
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
		log.Printf("[JSON-RPC] Ошибка чтения тела запроса: %v", err)
		http.Error(w, "Ошибка чтения запроса", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	log.Printf("[JSON-RPC] Получен запрос: %s", string(body))

	// Парсим JSON-RPC запрос
	var request map[string]interface{}
	if err := json.Unmarshal(body, &request); err != nil {
		log.Printf("[JSON-RPC] Ошибка парсинга JSON: %v", err)
		http.Error(w, "Неверный JSON", http.StatusBadRequest)
		return
	}

	method, ok := request["method"].(string)
	if !ok {
		log.Printf("[JSON-RPC] Отсутствует поле method")
		http.Error(w, "Отсутствует метод", http.StatusBadRequest)
		return
	}

	// Получаем session ID из заголовка
	sessionID := r.Header.Get("Mcp-Session-Id")
	log.Printf("[JSON-RPC] Метод: %s, Session ID: %s", method, sessionID)

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
		// Для n8n и других клиентов можем разрешить некоторые методы без сессии
		if method == "tools/list" || method == "tools/call" {
			log.Printf("[JSON-RPC] Автоматическое создание сессии для метода %s", method)
			sessionID = h.sessionManager.CreateSession()
			// Отправляем session ID в заголовке ответа
			w.Header().Set("Mcp-Session-Id", sessionID)
		} else {
			log.Printf("[JSON-RPC] Ошибка: отсутствует Mcp-Session-Id для метода %s", method)
			writeJSONRPCError(w, request["id"], -32600, "Отсутствует Mcp-Session-Id")
			return
		}
	}

	// Проверяем существование сессии
	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		log.Printf("[JSON-RPC] Ошибка: неверный Mcp-Session-Id: %s", sessionID)
		writeJSONRPCError(w, request["id"], -32600, "Неверный Mcp-Session-Id")
		return
	}

	// Обрабатываем различные методы
	switch method {
	case "initialized":
		// Нотификация о завершении инициализации
		log.Printf("[JSON-RPC] Получена initialized нотификация")
		w.WriteHeader(http.StatusAccepted)
		return

	case "tools/list":
		h.handleToolsList(w, request, session)

	case "tools/call":
		h.handleToolCall(w, request, session)

	default:
		// Для нотификаций возвращаем 202 Accepted
		if request["id"] == nil {
			log.Printf("[JSON-RPC] Получена нотификация: %s", method)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		log.Printf("[JSON-RPC] Неизвестный метод: %s", method)
		writeJSONRPCError(w, request["id"], -32601, "Метод не найден")
	}
}

// handleInitialize обрабатывает запрос инициализации
func (h *MCPHandler) handleInitialize(w http.ResponseWriter, request map[string]interface{}) {
	log.Printf("[Initialize] Обработка запроса инициализации")

	// Создаем новую сессию
	sessionID := h.sessionManager.CreateSession()
	log.Printf("[Initialize] Создана новая сессия: %s", sessionID)

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

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("[Initialize] Ошибка отправки ответа: %v", err)
	} else {
		log.Printf("[Initialize] Ответ отправлен успешно")
	}
}

// handleToolsList обрабатывает запрос списка инструментов
func (h *MCPHandler) handleToolsList(w http.ResponseWriter, request map[string]interface{}, session *Session) {
	log.Printf("[Tools/List] Обработка запроса списка инструментов")

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

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("[Tools/List] Ошибка отправки ответа: %v", err)
	} else {
		log.Printf("[Tools/List] Список инструментов отправлен успешно")
	}
}

// handleToolCall обрабатывает вызов инструмента
func (h *MCPHandler) handleToolCall(w http.ResponseWriter, request map[string]interface{}, session *Session) {
	log.Printf("[Tools/Call] Обработка вызова инструмента")

	params, ok := request["params"].(map[string]interface{})
	if !ok {
		log.Printf("[Tools/Call] Ошибка: неверные параметры")
		writeJSONRPCError(w, request["id"], -32602, "Неверные параметры")
		return
	}

	toolName, ok := params["name"].(string)
	if !ok || toolName != "get_system_info" {
		log.Printf("[Tools/Call] Ошибка: неизвестный инструмент: %v", toolName)
		writeJSONRPCError(w, request["id"], -32601, "Неизвестный инструмент")
		return
	}

	log.Printf("[Tools/Call] Вызов инструмента: %s", toolName)

	// Получаем системную информацию
	sysInfo, err := sysinfo.Get()
	if err != nil {
		log.Printf("[Tools/Call] Ошибка получения системной информации: %v", err)
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

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("[Tools/Call] Ошибка отправки ответа: %v", err)
	} else {
		log.Printf("[Tools/Call] Результат вызова инструмента отправлен успешно")
	}
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
		// Для n8n и других клиентов, которые не проходят инициализацию
		// Создаем новую сессию автоматически
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
			http.Error(w, "Неверный Mcp-Session-Id", http.StatusBadRequest)
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
		http.Error(w, "SSE не поддерживается", http.StatusInternalServerError)
		return
	}

	// Отправляем endpoint event согласно спецификации
	// Используем более совместимый формат
	endpointData := map[string]string{
		"url":    "/",
		"method": "POST",
	}
	endpointJSON, _ := json.Marshal(endpointData)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", string(endpointJSON))
	flusher.Flush()

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
