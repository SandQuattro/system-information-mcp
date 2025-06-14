package streamable

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"mcp-system-info/internal/logger"
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
	logger.Streamable.Info().Msg("Creating new Streamable HTTP handler")

	return &Handler{
		sessionManager:       sessionManager,
		lastCreatedSessionID: lastCreatedSessionID,
		handleJSONRPC:        handleJSONRPC,
	}
}

// HandlePost обрабатывает POST запросы для Streamable HTTP
func (h *Handler) HandlePost(c *fiber.Ctx) error {
	acceptHeader := c.Get("Accept")
	sessionID := c.Get("Mcp-Session-Id")

	streamLogger := logger.Streamable.With().
		Str("session_id", sessionID).
		Str("method", "POST").
		Str("accept_header", acceptHeader).
		Logger()

	streamLogger.Debug().Msg("Processing Streamable HTTP POST request")

	// Проверяем поддерживаемые Accept headers
	supportsJSON := strings.Contains(acceptHeader, "application/json") || strings.Contains(acceptHeader, "*/*")
	supportsSSE := strings.Contains(acceptHeader, "text/event-stream")

	if !supportsJSON && !supportsSSE {
		streamLogger.Warn().
			Bool("supports_json", supportsJSON).
			Bool("supports_sse", supportsSSE).
			Msg("Unsupported Accept header")
		return c.Status(fiber.StatusNotAcceptable).SendString("Not Acceptable")
	}

	body := c.Body()
	streamLogger.Debug().
		Bytes("request_body", body).
		Msg("Received request body")

	var messages []map[string]interface{}

	// Парсим JSON - может быть одиночное сообщение или массив
	if err := json.Unmarshal(body, &messages); err != nil {
		var singleMessage map[string]interface{}
		if err := json.Unmarshal(body, &singleMessage); err != nil {
			streamLogger.Error().
				Err(err).
				Str("body", string(body)).
				Msg("JSON parsing error")
			return c.Status(fiber.StatusBadRequest).SendString("Invalid JSON")
		}
		messages = []map[string]interface{}{singleMessage}
	}

	streamLogger.Debug().
		Int("message_count", len(messages)).
		Msg("Parsed JSON messages")

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

	streamLogger.Debug().
		Bool("has_requests", hasRequests).
		Bool("only_responses_or_notifications", onlyResponsesOrNotifications).
		Msg("Analyzed message types")

	// Если только responses или notifications
	if onlyResponsesOrNotifications {
		streamLogger.Debug().Msg("Processing responses/notifications only")
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
				method, _ := msg["method"].(string)

				streamLogger.Debug().
					Str("rpc_method", method).
					Msg("Processing request")

				response := h.handleJSONRPC(msg, sessionID)
				if response != nil {
					// Для initialize запроса добавляем Session ID в заголовок И в JSON response
					if method == "initialize" {
						if response["result"] != nil {
							if value, ok := h.lastCreatedSessionID.Load("sessionID"); ok {
								if newSessionID, ok := value.(string); ok {
									streamLogger.Info().
										Str("new_session_id", newSessionID).
										Msg("Initializing new session for Streamable HTTP")

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

		streamLogger.Debug().
			Int("response_count", len(responses)).
			Bool("supports_sse", supportsSSE).
			Msg("Prepared responses")

		// Возвращаем в зависимости от Accept header
		if supportsSSE && len(responses) > 0 {
			streamLogger.Debug().Msg("Returning SSE stream")
			// Возвращаем SSE поток
			return h.HandleSSE(c, responses)
		} else {
			streamLogger.Debug().Msg("Returning JSON responses")
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
	sessionID := c.Get("Mcp-Session-Id")

	streamLogger := logger.Streamable.With().
		Str("session_id", sessionID).
		Str("method", "GET").
		Str("accept_header", acceptHeader).
		Logger()

	streamLogger.Debug().Msg("Processing Streamable HTTP GET request")

	if strings.Contains(acceptHeader, "text/event-stream") {
		streamLogger.Debug().Msg("Returning SSE stream for GET request")
		return h.HandleSSE(c, nil)
	}

	streamLogger.Debug().Msg("Returning server info for GET request")

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

	streamLogger := logger.Streamable.With().
		Str("session_id", sessionID).
		Str("method", "DELETE").
		Logger()

	streamLogger.Info().Msg("Processing session deletion request")

	if sessionID != "" {
		h.sessionManager.RemoveSession(sessionID)
		streamLogger.Info().Msg("Session deleted successfully")
	} else {
		streamLogger.Warn().Msg("No session ID provided for deletion")
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

	streamLogger := logger.Streamable.With().
		Str("session_id", sessionID).
		Str("last_event_id", lastEventID).
		Int("initial_responses", len(initialResponses)).
		Logger()

	streamLogger.Info().Msg("Starting Streamable HTTP SSE connection")

	// Если нет Session ID, создаем новый как fallback
	autoCreatedSession := false
	if sessionID == "" {
		sessionID = h.sessionManager.CreateSession()
		autoCreatedSession = true

		streamLogger.Info().
			Str("new_session_id", sessionID).
			Bool("auto_created", true).
			Msg("Created new session for Streamable HTTP SSE")

		c.Set("Mcp-Session-Id", sessionID)

		// Обновляем логгер с новым session ID
		streamLogger = streamLogger.With().
			Str("session_id", sessionID).
			Bool("auto_created", autoCreatedSession).
			Logger()
	}

	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		streamLogger.Error().Msg("Session not found for Streamable HTTP SSE")
		return c.Status(fiber.StatusNotFound).SendString("Session not found")
	}

	// Устанавливаем SSE заголовки
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		streamLogger.Debug().Msg("Streamable HTTP SSE stream started")

		// Обработка Resume from Last Event согласно спецификации
		var startEventID int64 = 0
		if lastEventID != "" {
			if parsedID, err := strconv.ParseInt(lastEventID, 10, 64); err == nil {
				startEventID = parsedID
				streamLogger.Info().
					Int64("start_event_id", startEventID).
					Msg("Resuming from last event ID")
			} else {
				streamLogger.Warn().
					Err(err).
					Str("last_event_id", lastEventID).
					Msg("Failed to parse last event ID")
			}
		}

		// Message Replay: воспроизводим пропущенные события
		if startEventID > 0 {
			missedEvents := session.GetEventsAfter(startEventID)
			streamLogger.Info().
				Int("missed_events_count", len(missedEvents)).
				Msg("Replaying missed events")

			for _, event := range missedEvents {
				jsonData, _ := json.Marshal(event.Data)
				fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.ID, jsonData)
				w.Flush()
			}
		}

		// Отправляем начальные responses если есть
		if len(initialResponses) > 0 {
			streamLogger.Debug().
				Int("initial_responses", len(initialResponses)).
				Msg("Sending initial responses")

			for _, response := range initialResponses {
				eventID := session.StoreEvent(response)
				jsonData, _ := json.Marshal(response)
				fmt.Fprintf(w, "id: %d\ndata: %s\n\n", eventID, jsonData)
				w.Flush()
			}
		}

		done := make(chan struct{})
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					streamLogger.Error().
						Interface("panic", r).
						Msg("Recovered from panic in Streamable HTTP SSE goroutine")
				}
			}()

			// Используем timeout вместо c.Context().Done() для безопасности
			timeout := time.NewTimer(5 * time.Minute)
			defer timeout.Stop()

			select {
			case <-timeout.C:
				streamLogger.Info().
					Dur("timeout", 5*time.Minute).
					Msg("Streamable HTTP SSE session timeout")

				if autoCreatedSession {
					h.sessionManager.RemoveSession(sessionID)
				}
				close(done)
			case <-done:
			}
		}()

		messageCount := 0
		for {
			select {
			case <-done:
				streamLogger.Info().
					Int("messages_sent", messageCount).
					Msg("Streamable HTTP SSE connection closed")
				return
			case message, ok := <-session.SSEChan:
				if !ok {
					streamLogger.Debug().Msg("Streamable HTTP SSE channel closed")
					return
				}

				// Сохраняем событие и получаем уникальный ID
				eventID := session.StoreEvent(message)
				jsonData, err := json.Marshal(message)
				if err != nil {
					streamLogger.Error().
						Err(err).
						Interface("message", message).
						Msg("Failed to marshal Streamable HTTP SSE message")
					continue
				}

				fmt.Fprintf(w, "id: %d\ndata: %s\n\n", eventID, jsonData)
				w.Flush()
				messageCount++

				streamLogger.Trace().
					Int64("event_id", eventID).
					Int("message_count", messageCount).
					Msg("Sent Streamable HTTP SSE message")

			case <-pingTicker.C:
				fmt.Fprintf(w, ": ping\n\n")
				w.Flush()

				streamLogger.Trace().Msg("Sent Streamable HTTP SSE ping")
			}
		}
	})

	return nil
}
