package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"mcp-system-info/internal/sysinfo"

	"github.com/gofiber/fiber/v2"
	"github.com/mark3labs/mcp-go/server"
)

type FiberMCPHandler struct {
	server               *server.MCPServer
	sessionManager       *SessionManager
	lastCreatedSessionID sync.Map
}

func NewFiberMCPHandler(server *server.MCPServer, sessionManager *SessionManager) *FiberMCPHandler {
	return &FiberMCPHandler{
		server:         server,
		sessionManager: sessionManager,
	}
}

func (h *FiberMCPHandler) RegisterRoutes(app *fiber.App) {
	// Streamable HTTP на корневом маршруте
	app.Post("/", h.handleStreamableHTTPPost)
	app.Get("/", h.handleStreamableHTTPGet)
	app.Delete("/", h.handleStreamableHTTPDelete)

	// Legacy SSE для обратной совместимости
	app.Post("/sse", h.handleLegacyPost)
	app.Get("/sse", h.handleLegacySSE)
}

// Streamable HTTP POST согласно спецификации 2025-03-26
func (h *FiberMCPHandler) handleStreamableHTTPPost(c *fiber.Ctx) error {
	acceptHeader := c.Get("Accept")
	log.Printf("[Streamable HTTP POST] Accept header: %s", acceptHeader)

	// Проверяем поддерживаемые Accept headers
	supportsJSON := strings.Contains(acceptHeader, "application/json") || strings.Contains(acceptHeader, "*/*")
	supportsSSE := strings.Contains(acceptHeader, "text/event-stream")

	if !supportsJSON && !supportsSSE {
		return c.Status(fiber.StatusNotAcceptable).SendString("Not Acceptable")
	}

	body := c.Body()
	log.Printf("[Streamable HTTP POST] Received request: %s", string(body))

	var messages []map[string]interface{}

	// Парсим JSON - может быть одиночное сообщение или массив
	if err := json.Unmarshal(body, &messages); err != nil {
		var singleMessage map[string]interface{}
		if err := json.Unmarshal(body, &singleMessage); err != nil {
			log.Printf("[Streamable HTTP POST] JSON parsing error: %v", err)
			return c.Status(fiber.StatusBadRequest).SendString("Invalid JSON")
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
			}
		}
	}

	sessionID := c.Get("Mcp-Session-Id")

	// Если только responses или notifications
	if onlyResponsesOrNotifications {
		for _, msg := range messages {
			h.handleJSONRPCMessage(msg, sessionID)
		}
		return c.SendStatus(fiber.StatusAccepted)
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
									c.Set("Mcp-Session-Id", newSessionID)
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
			return h.handleStreamableHTTPSSE(c, responses)
		} else {
			// Возвращаем JSON
			c.Set("Content-Type", "application/json")

			if len(responses) == 1 {
				return c.JSON(responses[0])
			} else {
				return c.JSON(responses)
			}
		}
	}

	return c.SendStatus(fiber.StatusOK)
}

// Streamable HTTP GET согласно спецификации 2025-03-26
func (h *FiberMCPHandler) handleStreamableHTTPGet(c *fiber.Ctx) error {
	acceptHeader := c.Get("Accept")
	log.Printf("[Streamable HTTP GET] Accept header: %s", acceptHeader)

	if strings.Contains(acceptHeader, "text/event-stream") {
		return h.handleStreamableHTTPSSE(c, nil)
	}

	// Для обычных GET запросов возвращаем информацию о сервере
	return c.JSON(fiber.Map{
		"status":    "ok",
		"message":   "MCP System Info Server is running",
		"version":   "1.0.0",
		"protocol":  "2025-03-26",
		"framework": "Fiber v2.52.8",
		"endpoints": fiber.Map{
			"mcp":        "/",
			"legacy_sse": "/sse",
		},
	})
}

// Streamable HTTP DELETE согласно спецификации 2025-03-26
func (h *FiberMCPHandler) handleStreamableHTTPDelete(c *fiber.Ctx) error {
	sessionID := c.Get("Mcp-Session-Id")
	log.Printf("[Streamable HTTP DELETE] Deleting session: %s", sessionID)

	if sessionID != "" {
		h.sessionManager.RemoveSession(sessionID)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// SSE поток для Streamable HTTP
func (h *FiberMCPHandler) handleStreamableHTTPSSE(c *fiber.Ctx, initialResponses []map[string]interface{}) error {
	sessionID := c.Get("Mcp-Session-Id")
	lastEventID := c.Get("Last-Event-Id")

	log.Printf("[Streamable HTTP SSE] Session: %s, Last-Event-Id: %s", sessionID, lastEventID)

	// Если нет Session ID, создаем новый как fallback
	autoCreatedSession := false
	if sessionID == "" {
		sessionID = h.sessionManager.CreateSession()
		autoCreatedSession = true
		log.Printf("[Streamable HTTP SSE] Created new session: %s", sessionID)
		c.Set("Mcp-Session-Id", sessionID)
	}

	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		return c.Status(fiber.StatusNotFound).SendString("Session not found")
	}

	// Устанавливаем SSE заголовки
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// Отправляем начальные responses если есть
		for _, response := range initialResponses {
			jsonData, _ := json.Marshal(response)
			fmt.Fprintf(w, "data: %s\n\n", jsonData)
			w.Flush()
		}

		done := make(chan struct{})
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Streamable HTTP SSE] Recovered from panic: %v", r)
				}
			}()

			// Используем timeout вместо c.Context().Done() для безопасности
			timeout := time.NewTimer(5 * time.Minute)
			defer timeout.Stop()

			select {
			case <-timeout.C:
				log.Printf("[Streamable HTTP SSE] Session timeout, session: %s", sessionID)
				if autoCreatedSession {
					h.sessionManager.RemoveSession(sessionID)
				}
				close(done)
			case <-done:
			}
		}()

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
				w.Flush()
			case <-pingTicker.C:
				fmt.Fprintf(w, ": ping\n\n")
				w.Flush()
			}
		}
	})

	return nil
}

// Legacy POST для обратной совместимости
func (h *FiberMCPHandler) handleLegacyPost(c *fiber.Ctx) error {
	body := c.Body()
	log.Printf("[Legacy POST] Received request: %s", string(body))

	var request map[string]interface{}
	if err := json.Unmarshal(body, &request); err != nil {
		log.Printf("[Legacy POST] JSON parsing error: %v", err)
		return c.Status(fiber.StatusBadRequest).SendString("Invalid JSON")
	}

	response := h.handleJSONRPCMessage(request, "")

	if response != nil {
		// Для initialize запроса добавляем Session ID в ответ
		if method, ok := request["method"].(string); ok && method == "initialize" {
			if response["result"] != nil {
				if value, ok := h.lastCreatedSessionID.Load("sessionID"); ok {
					if sessionID, ok := value.(string); ok {
						if result, ok := response["result"].(map[string]interface{}); ok {
							result["sessionId"] = sessionID
						}
						h.lastCreatedSessionID.Delete("sessionID")
					}
				}
			}
		}

		return c.JSON(response)
	}

	return c.SendStatus(fiber.StatusAccepted)
}

// Legacy SSE для обратной совместимости
func (h *FiberMCPHandler) handleLegacySSE(c *fiber.Ctx) error {
	sessionID := c.Get("Mcp-Session-Id")
	if sessionID == "" {
		sessionID = c.Query("sessionId")
	}

	autoCreatedSession := false
	if sessionID == "" {
		sessionID = h.sessionManager.CreateSession()
		autoCreatedSession = true
		log.Printf("[Legacy SSE] Created new session: %s", sessionID)
	}

	c.Set("Mcp-Session-Id", sessionID)

	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		return c.Status(fiber.StatusNotFound).SendString("Session not found")
	}

	// Устанавливаем SSE заголовки
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
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
		w.Flush()

		done := make(chan struct{})
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		// Используем безопасный способ отслеживания отключения клиента
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Legacy SSE] Recovered from panic: %v", r)
				}
			}()

			// Используем простой timeout вместо c.Context().Done() для избежания паники
			timeout := time.NewTimer(5 * time.Minute)
			defer timeout.Stop()

			select {
			case <-timeout.C:
				log.Printf("[Legacy SSE] Session timeout, session: %s", sessionID)
				if autoCreatedSession {
					h.sessionManager.RemoveSession(sessionID)
				}
				close(done)
			case <-done:
			}
		}()

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
				w.Flush()
			case <-pingTicker.C:
				fmt.Fprintf(w, ": ping\n\n")
				w.Flush()
			}
		}
	})

	return nil
}

func (h *FiberMCPHandler) handleJSONRPCMessage(request map[string]interface{}, sessionID string) map[string]interface{} {
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

func (h *FiberMCPHandler) handleInitializeRequest(request map[string]interface{}) map[string]interface{} {
	id := request["id"]

	sessionID := h.sessionManager.CreateSession()
	log.Printf("[INITIALIZE] Created new session: %s", sessionID)

	h.lastCreatedSessionID.Store("sessionID", sessionID)

	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]interface{}{
			"protocolVersion": "2024-11-05",
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

func (h *FiberMCPHandler) handleToolsListRequest(request map[string]interface{}, session *Session) map[string]interface{} {
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

func (h *FiberMCPHandler) handleToolCallRequest(request map[string]interface{}, session *Session) map[string]interface{} {
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
