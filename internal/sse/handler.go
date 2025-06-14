package sse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"mcp-system-info/internal/logger"
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
	logger.SSE.Info().Msg("Creating new Legacy SSE handler")

	return &Handler{
		sessionManager:       sessionManager,
		lastCreatedSessionID: lastCreatedSessionID,
		handleJSONRPC:        handleJSONRPC,
	}
}

// HandlePost обрабатывает POST запросы для Legacy SSE
func (h *Handler) HandlePost(c *fiber.Ctx) error {
	body := c.Body()
	sessionID := c.Get("Mcp-Session-Id")

	sseLogger := logger.SSE.With().
		Str("session_id", sessionID).
		Str("method", "POST").
		Logger()

	sseLogger.Debug().
		Bytes("request_body", body).
		Msg("Received Legacy SSE POST request")

	var request map[string]interface{}
	if err := json.Unmarshal(body, &request); err != nil {
		sseLogger.Error().
			Err(err).
			Str("body", string(body)).
			Msg("JSON parsing error")
		return c.Status(fiber.StatusBadRequest).SendString("Invalid JSON")
	}

	// Получаем sessionID из заголовка или создаем новый для initialize
	method, _ := request["method"].(string)

	sseLogger = sseLogger.With().
		Str("rpc_method", method).
		Logger()

	// Для initialize запроса sessionID не нужен, для остальных - обязателен
	if method != "initialize" && method != "notifications/initialized" {
		if sessionID == "" {
			sseLogger.Warn().
				Str("rpc_method", method).
				Msg("Missing session ID for non-initialize method")

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
						sseLogger.Info().
							Str("new_session_id", newSessionID).
							Msg("Initializing new session for Legacy SSE")

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

		sseLogger.Debug().
			Interface("response", response).
			Msg("Sending Legacy SSE POST response")

		return c.JSON(response)
	}

	sseLogger.Debug().Msg("No response to send, returning 202 Accepted")
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

		logger.SSE.Info().
			Str("session_id", sessionID).
			Bool("auto_created", true).
			Msg("Created new session for Legacy SSE connection")
	}

	sseLogger := logger.SSE.With().
		Str("session_id", sessionID).
		Bool("auto_created", autoCreatedSession).
		Logger()

	c.Set("Mcp-Session-Id", sessionID)

	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		sseLogger.Error().Msg("Session not found for Legacy SSE")
		return c.Status(fiber.StatusNotFound).SendString("Session not found")
	}

	sseLogger.Info().Msg("Starting Legacy SSE connection")

	// Устанавливаем SSE заголовки
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		sseLogger.Debug().Msg("Legacy SSE stream started")

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

		sseLogger.Debug().Msg("Sent endpoint notification")

		done := make(chan struct{})
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		// Используем безопасный способ отслеживания отключения клиента
		go func() {
			defer func() {
				if r := recover(); r != nil {
					sseLogger.Error().
						Interface("panic", r).
						Msg("Recovered from panic in Legacy SSE goroutine")
				}
				// Гарантируем очистку ресурсов
				if autoCreatedSession {
					h.sessionManager.RemoveSession(sessionID)
				}
				close(done)
			}()

			// Используем простой timeout с контекстом для лучшего управления ресурсами
			timeout := time.NewTimer(5 * time.Minute)
			defer timeout.Stop()

			select {
			case <-timeout.C:
				sseLogger.Info().
					Dur("timeout", 5*time.Minute).
					Msg("Legacy SSE session timeout")
			case <-c.Context().Done():
				sseLogger.Info().Msg("Legacy SSE client disconnected")
			case <-done:
				return
			}
		}()

		messageCount := 0
		for {
			select {
			case <-done:
				sseLogger.Info().
					Int("messages_sent", messageCount).
					Msg("Legacy SSE connection closed")
				return
			case message, ok := <-session.SSEChan:
				if !ok {
					sseLogger.Debug().Msg("Legacy SSE channel closed")
					return
				}

				jsonData, err := json.Marshal(message)
				if err != nil {
					sseLogger.Error().
						Err(err).
						Interface("message", message).
						Msg("Failed to marshal Legacy SSE message")
					continue
				}

				fmt.Fprintf(w, "event: message\ndata: %s\n\n", jsonData)
				w.Flush()
				messageCount++

				// Логируем только каждое 5-е сообщение для снижения нагрузки
				if messageCount%5 == 0 {
					sseLogger.Debug().
						Int("message_count", messageCount).
						Msg("Sent Legacy SSE messages (batch log)")
				}

			case <-pingTicker.C:
				fmt.Fprintf(w, ": ping\n\n")
				w.Flush()

				sseLogger.Trace().Msg("Sent Legacy SSE ping")
			}
		}
	})

	return nil
}
