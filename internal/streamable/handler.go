package streamable

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"mcp-system-info/internal/types"

	"github.com/gofiber/fiber/v2"
)

// Handler обрабатывает Streamable HTTP запросы (2025-03-26)
type Handler struct {
	sessionManager       *types.SessionManager
	lastCreatedSessionID *sync.Map
	handleJSONRPC        func(map[string]interface{}, string) map[string]interface{}
}

// NewHandler создает новый Streamable HTTP handler
func NewHandler(sessionManager *types.SessionManager, lastCreatedSessionID *sync.Map, handleJSONRPC func(map[string]interface{}, string) map[string]interface{}) *Handler {
	return &Handler{
		sessionManager:       sessionManager,
		lastCreatedSessionID: lastCreatedSessionID,
		handleJSONRPC:        handleJSONRPC,
	}
}

// HandlePost обрабатывает POST запросы для Streamable HTTP
func (h *Handler) HandlePost(c *fiber.Ctx) error {
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
			h.handleJSONRPC(msg, sessionID)
		}
		return c.SendStatus(fiber.StatusAccepted)
	}

	// Если есть requests, обрабатываем их
	if hasRequests {
		responses := []map[string]interface{}{}

		for _, msg := range messages {
			if _, hasMethod := msg["method"]; hasMethod {
				response := h.handleJSONRPC(msg, sessionID)
				if response != nil {
					// Для initialize запроса добавляем Session ID в заголовок И в JSON response
					if method, ok := msg["method"].(string); ok && method == "initialize" {
						if response["result"] != nil {
							if value, ok := h.lastCreatedSessionID.Load("sessionID"); ok {
								if newSessionID, ok := value.(string); ok {
									// Устанавливаем заголовок для HTTP
									c.Set("Mcp-Session-Id", newSessionID)
									// Добавляем sessionId в JSON response для клиента
									if result, ok := response["result"].(map[string]interface{}); ok {
										result["sessionId"] = newSessionID
									}
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
			return h.HandleSSE(c, responses)
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

// HandleGet обрабатывает GET запросы для Streamable HTTP
func (h *Handler) HandleGet(c *fiber.Ctx) error {
	acceptHeader := c.Get("Accept")
	log.Printf("[Streamable HTTP GET] Accept header: %s", acceptHeader)

	if strings.Contains(acceptHeader, "text/event-stream") {
		return h.HandleSSE(c, nil)
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

// HandleDelete обрабатывает DELETE запросы для Streamable HTTP
func (h *Handler) HandleDelete(c *fiber.Ctx) error {
	sessionID := c.Get("Mcp-Session-Id")
	log.Printf("[Streamable HTTP DELETE] Deleting session: %s", sessionID)

	if sessionID != "" {
		h.sessionManager.RemoveSession(sessionID)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// HandleSSE обрабатывает SSE поток для Streamable HTTP
func (h *Handler) HandleSSE(c *fiber.Ctx, initialResponses []map[string]interface{}) error {
	sessionID := c.Get("Mcp-Session-Id")
	lastEventID := c.Get("Last-Event-Id")

	// Также проверяем стандартный заголовок Last-Event-ID
	if lastEventID == "" {
		lastEventID = c.Get("Last-Event-ID")
	}

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
		// Обработка Resume from Last Event согласно спецификации
		var startEventID int64 = 0
		if lastEventID != "" {
			if parsedID, err := strconv.ParseInt(lastEventID, 10, 64); err == nil {
				startEventID = parsedID
				log.Printf("[Streamable HTTP SSE] Resuming from event ID: %d", startEventID)
			}
		}

		// Message Replay: воспроизводим пропущенные события
		if startEventID > 0 {
			missedEvents := session.GetEventsAfter(startEventID)
			log.Printf("[Streamable HTTP SSE] Replaying %d missed events", len(missedEvents))
			for _, event := range missedEvents {
				jsonData, _ := json.Marshal(event.Data)
				fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.ID, jsonData)
				w.Flush()
			}
		}

		// Отправляем начальные responses если есть
		for _, response := range initialResponses {
			eventID := session.StoreEvent(response)
			jsonData, _ := json.Marshal(response)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", eventID, jsonData)
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

				// Сохраняем событие и получаем уникальный ID
				eventID := session.StoreEvent(message)
				jsonData, err := json.Marshal(message)
				if err != nil {
					continue
				}

				fmt.Fprintf(w, "id: %d\ndata: %s\n\n", eventID, jsonData)
				w.Flush()
			case <-pingTicker.C:
				fmt.Fprintf(w, ": ping\n\n")
				w.Flush()
			}
		}
	})

	return nil
}
