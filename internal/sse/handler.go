package sse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"mcp-system-info/internal/types"

	"github.com/gofiber/fiber/v2"
)

// Handler обрабатывает Legacy SSE запросы (2024-11-05)
type Handler struct {
	sessionManager       *types.SessionManager
	lastCreatedSessionID *sync.Map
	handleJSONRPC        func(map[string]interface{}, string) map[string]interface{}
}

// NewHandler создает новый Legacy SSE handler
func NewHandler(sessionManager *types.SessionManager, lastCreatedSessionID *sync.Map, handleJSONRPC func(map[string]interface{}, string) map[string]interface{}) *Handler {
	return &Handler{
		sessionManager:       sessionManager,
		lastCreatedSessionID: lastCreatedSessionID,
		handleJSONRPC:        handleJSONRPC,
	}
}

// HandlePost обрабатывает POST запросы для Legacy SSE
func (h *Handler) HandlePost(c *fiber.Ctx) error {
	body := c.Body()
	log.Printf("[Legacy POST] Received request: %s", string(body))

	var request map[string]interface{}
	if err := json.Unmarshal(body, &request); err != nil {
		log.Printf("[Legacy POST] JSON parsing error: %v", err)
		return c.Status(fiber.StatusBadRequest).SendString("Invalid JSON")
	}

	// Получаем sessionID из заголовка или создаем новый для initialize
	sessionID := c.Get("Mcp-Session-Id")
	method, _ := request["method"].(string)

	// Для initialize запроса sessionID не нужен, для остальных - обязателен
	if method != "initialize" && method != "notifications/initialized" {
		if sessionID == "" {
			log.Printf("[Legacy POST] Missing session ID for method: %s", method)
			return c.Status(fiber.StatusBadRequest).JSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"error": map[string]interface{}{
					"code":    -32001,
					"message": "Missing Mcp-Session-Id header",
				},
			})
		}
	}

	response := h.handleJSONRPC(request, sessionID)

	if response != nil {
		// Для initialize запроса добавляем Session ID в ответ и заголовок
		if method == "initialize" {
			if response["result"] != nil {
				if value, ok := h.lastCreatedSessionID.Load("sessionID"); ok {
					if newSessionID, ok := value.(string); ok {
						if result, ok := response["result"].(map[string]interface{}); ok {
							result["sessionId"] = newSessionID
						}
						// Устанавливаем заголовок для последующих запросов
						c.Set("Mcp-Session-Id", newSessionID)
						h.lastCreatedSessionID.Delete("sessionID")
					}
				}
			}
		}

		return c.JSON(response)
	}

	return c.SendStatus(fiber.StatusAccepted)
}

// HandleSSE обрабатывает SSE подключения для Legacy
func (h *Handler) HandleSSE(c *fiber.Ctx) error {
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
