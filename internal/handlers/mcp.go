package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"mcp-system-info/internal/logger"
	"mcp-system-info/internal/middleware"
	"mcp-system-info/internal/sysinfo"
	"mcp-system-info/internal/tools"
	"mcp-system-info/internal/types"

	"github.com/gofiber/fiber/v2"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type FiberMCPHandler struct {
	server               *server.MCPServer
	sessionManager       *types.SessionManager
	lastCreatedSessionID sync.Map
}

func NewFiberMCPHandler(server *server.MCPServer, sessionManager *types.SessionManager) *FiberMCPHandler {
	handler := &FiberMCPHandler{
		server:         server,
		sessionManager: sessionManager,
	}

	return handler
}

func (h *FiberMCPHandler) RegisterRoutes(app *fiber.App) {
	// Health check endpoint (без авторизации)
	app.Get("/", h.HandleHealthCheck)

	// MCP Streamable HTTP endpoints (с авторизацией)
	mcpGroup := app.Group("/mcp", middleware.AuthMiddleware())
	mcpGroup.Post("/", h.HandleJSONRPC)
	mcpGroup.Get("/", h.HandleSSE)
}

// HandleHealthCheck простой health check endpoint
func (h *FiberMCPHandler) HandleHealthCheck(c *fiber.Ctx) error {
	logger.HTTP.Info().
		Str("method", "GET").
		Str("path", "/").
		Str("user_agent", c.Get("User-Agent")).
		Msg("Health check request")

	return c.JSON(map[string]interface{}{
		"status":  "ok",
		"service": "mcp-system-info",
		"version": "1.0.0",
		"message": "MCP endpoints available at /mcp",
	})
}

// HandleJSONRPC обрабатывает JSON-RPC запросы
func (h *FiberMCPHandler) HandleJSONRPC(c *fiber.Ctx) error {
	// Получаем session ID из заголовков
	sessionID := c.Get("Mcp-Session-Id", "")

	mcpLogger := logger.GetMCPLogger("unknown", sessionID)

	// Парсим JSON-RPC запрос
	var request map[string]interface{}
	if err := json.Unmarshal(c.Body(), &request); err != nil {
		mcpLogger.Error().Err(err).Msg("Failed to parse JSON-RPC request")
		return c.Status(400).JSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"error": map[string]interface{}{
				"code":    -32700,
				"message": "Parse error",
			},
		})
	}

	// Проверяем если это streaming tool call и клиент поддерживает SSE
	if h.isStreamingToolCall(request) && h.clientSupportsSSE(c) {
		return h.handleStreamingToolCall(c, request, sessionID)
	}

	// Обрабатываем запрос
	response := h.handleJSONRPCMessage(request, sessionID)
	if response == nil {
		return c.SendStatus(204) // No Content
	}

	// Устанавливаем session ID в заголовок ответа если был создан новый
	if sessionID == "" {
		if storedSessionID, ok := h.lastCreatedSessionID.Load("sessionID"); ok {
			c.Set("Mcp-Session-Id", storedSessionID.(string))
		}
	}

	return c.JSON(response)
}

// isStreamingToolCall проверяет является ли запрос вызовом streaming tool
func (h *FiberMCPHandler) isStreamingToolCall(request map[string]interface{}) bool {
	method, ok := request["method"].(string)
	if !ok || method != "tools/call" {
		return false
	}

	params, ok := request["params"].(map[string]interface{})
	if !ok {
		return false
	}

	toolName, ok := params["name"].(string)
	if !ok {
		return false
	}

	// Список streaming tools
	streamingTools := []string{"system_monitor_stream"}
	for _, streamTool := range streamingTools {
		if toolName == streamTool {
			return true
		}
	}

	return false
}

// clientSupportsSSE проверяет поддерживает ли клиент SSE потоки
func (h *FiberMCPHandler) clientSupportsSSE(c *fiber.Ctx) bool {
	accept := c.Get("Accept", "")

	// Проверяем заголовок Accept на поддержку text/event-stream
	return accept != "" && (accept == "text/event-stream" ||
		c.Accepts("text/event-stream") == "text/event-stream" ||
		strings.Contains(accept, "text/event-stream"))
}

// handleStreamingToolCall обрабатывает streaming tool calls в SSE режиме
func (h *FiberMCPHandler) handleStreamingToolCall(c *fiber.Ctx, request map[string]interface{}, sessionID string) error {
	logger.Streamable.Info().
		Str("session_id", sessionID).
		Msg("Switching to SSE mode for streaming tool call")

	// Устанавливаем SSE headers
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Access-Control-Allow-Origin", "*")

	// Получаем session
	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		return c.Status(400).SendString("event: error\ndata: {\"error\":\"Session not found\"}\n\n")
	}

	// Парсим tool call параметры
	params, _ := request["params"].(map[string]interface{})
	toolName, _ := params["name"].(string)

	// Получаем request ID для финального ответа
	requestID := request["id"]

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		if toolName == "system_monitor_stream" {
			h.handleSystemMonitorStream(w, params, session, requestID)
		}
	})

	return nil
}

// handleSystemMonitorStream выполняет real-time streaming мониторинга системы
func (h *FiberMCPHandler) handleSystemMonitorStream(w *bufio.Writer, params map[string]interface{}, session *types.Session, requestID interface{}) {
	logger.Streamable.Info().
		Str("session_id", session.ID).
		Msg("Starting real-time system monitor stream")

	// Получаем параметры
	arguments := make(map[string]interface{})
	if args, ok := params["arguments"].(map[string]interface{}); ok {
		arguments = args
	}

	var durationStr, intervalStr string
	if dur, exists := arguments["duration"]; exists {
		if durStr, ok := dur.(string); ok {
			durationStr = durStr
		}
	}
	if inter, exists := arguments["interval"]; exists {
		if interStr, ok := inter.(string); ok {
			intervalStr = interStr
		}
	}

	if durationStr == "" {
		durationStr = "30s"
	}
	if intervalStr == "" {
		intervalStr = "2s"
	}

	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		fmt.Fprintf(w, "event: error\n")
		fmt.Fprintf(w, "data: {\"error\":\"Invalid duration format: %v\"}\n\n", err)
		w.Flush()
		return
	}

	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		fmt.Fprintf(w, "event: error\n")
		fmt.Fprintf(w, "data: {\"error\":\"Invalid interval format: %v\"}\n\n", err)
		w.Flush()
		return
	}

	// Отправляем начальную JSON-RPC notification
	fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"tool_progress\",\"params\":{\"phase\":\"start\",\"duration\":\"%v\",\"interval\":\"%v\"}}\n\n", duration, interval)
	w.Flush()

	endTime := time.Now().Add(duration)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	iteration := 0
	for {
		select {
		case <-ticker.C:
			if time.Now().After(endTime) {
				logger.Streamable.Info().
					Str("session_id", session.ID).
					Msg("Stream duration completed")

				// Отправляем финальный JSON-RPC response
				fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":")
				if requestID != nil {
					jsonBytes, _ := json.Marshal(requestID)
					fmt.Fprintf(w, "%s", string(jsonBytes))
				} else {
					fmt.Fprintf(w, "null")
				}
				fmt.Fprintf(w, ",\"result\":{\"status\":\"completed\",\"total_samples\":%d}}\n\n", iteration)
				w.Flush()
				return
			}

			iteration++

			// Получаем системную информацию
			sysInfo, err := sysinfo.Get()
			if err != nil {
				logger.Streamable.Error().
					Err(err).
					Str("session_id", session.ID).
					Int("iteration", iteration).
					Msg("Failed to get system info during stream")

				// Отправляем JSON-RPC notification об ошибке
				fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"tool_progress\",\"params\":{\"iteration\":%d,\"error\":\"%v\"}}\n\n", iteration, err)
				w.Flush()
				continue
			}

			// 🚀 ОТПРАВЛЯЕМ ДАННЫЕ В РЕАЛЬНОМ ВРЕМЕНИ как JSON-RPC notification!
			timestamp := time.Now().Format("15:04:05")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"tool_progress\",\"params\":{")
			fmt.Fprintf(w, "\"iteration\":%d,", iteration)
			fmt.Fprintf(w, "\"timestamp\":\"%s\",", timestamp)
			fmt.Fprintf(w, "\"cpu\":%.2f,", sysInfo.CPU.UsagePercent)
			fmt.Fprintf(w, "\"memory\":%.2f", sysInfo.Memory.UsedPercent)
			fmt.Fprintf(w, "}}\n\n")
			w.Flush() // 🔥 НЕМЕДЛЕННАЯ ОТПРАВКА!

			logger.Streamable.Debug().
				Str("session_id", session.ID).
				Int("iteration", iteration).
				Float64("cpu_usage", sysInfo.CPU.UsagePercent).
				Float64("memory_usage", sysInfo.Memory.UsedPercent).
				Msg("Sample sent via SSE")

		default:
			// Проверяем не закрыто ли соединение
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// HandleSSE обрабатывает GET запросы для SSE streams
func (h *FiberMCPHandler) HandleSSE(c *fiber.Ctx) error {
	accept := c.Get("Accept", "")

	// Проверяем поддержку text/event-stream
	if accept != "" && (accept == "text/event-stream" ||
		c.Accepts("text/event-stream") == "text/event-stream") {

		sessionID := c.Get("Mcp-Session-Id", "")

		logger.SSE.Info().
			Str("session_id", sessionID).
			Str("accept", accept).
			Msg("Opening SSE stream")

		// Устанавливаем SSE headers
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("Access-Control-Allow-Origin", "*")

		// TODO: Реализовать SSE stream
		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			logger.SSE.Debug().Msg("SSE stream writer started")

			// Отправляем initial event
			fmt.Fprintf(w, "event: message\n")
			fmt.Fprintf(w, "data: {\"type\":\"connected\"}\n\n")
			w.Flush()

			// Держим соединение открытым
			select {
			case <-c.Context().Done():
				logger.SSE.Debug().Msg("SSE stream closed by client")
			case <-time.After(30 * time.Second):
				logger.SSE.Debug().Msg("SSE stream timeout")
			}
		})

		return nil
	}

	// Если не SSE запрос, возвращаем информацию о сервере
	return c.JSON(map[string]interface{}{
		"name":          "mcp-system-info",
		"version":       "1.0.0",
		"protocol":      "MCP Streamable HTTP",
		"specification": "2025-03-26",
		"endpoints": []string{
			"GET / (Health Check)",
			"POST /mcp (JSON-RPC)",
			"GET /mcp (SSE Stream)",
		},
	})
}

func (h *FiberMCPHandler) handleJSONRPCMessage(request map[string]interface{}, sessionID string) map[string]interface{} {
	mcpLogger := logger.GetMCPLogger("unknown", sessionID)

	method, hasMethod := request["method"].(string)
	id, hasID := request["id"]

	if hasMethod {
		mcpLogger = logger.GetMCPLogger(method, sessionID)
	}

	mcpLogger.Debug().
		Interface("request", request).
		Msg("Processing JSON-RPC request")

	if !hasMethod {
		mcpLogger.Warn().Msg("Request missing method field")
		return nil
	}

	if method == "initialize" {
		mcpLogger.Info().Msg("Handling initialize request")
		return h.handleInitializeRequest(request)
	}

	session, exists := h.sessionManager.GetSession(sessionID)
	if !exists {
		mcpLogger.Warn().Msg("Session not found")
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
			mcpLogger.Warn().Msg("tools/list request missing id field")
			return nil
		}
		mcpLogger.Debug().Msg("Handling tools/list request")
		return h.handleToolsListRequest(request, session)

	case "tools/call":
		if !hasID {
			mcpLogger.Warn().Msg("tools/call request missing id field")
			return nil
		}
		mcpLogger.Debug().Msg("Handling tools/call request")
		return h.handleToolCallRequest(request, session)

	default:
		mcpLogger.Warn().Str("method", method).Msg("Unknown method")
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

	logger.Session.Info().
		Str("session_id", sessionID).
		Msg("Created new session")

	h.lastCreatedSessionID.Store("sessionID", sessionID)

	logger.Session.Info().
		Str("session_id", sessionID).
		Msg("Initialize response prepared")

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

func (h *FiberMCPHandler) handleToolsListRequest(request map[string]interface{}, session *types.Session) map[string]interface{} {
	id := request["id"]

	logger.Tools.Debug().
		Str("session_id", session.ID).
		Msg("Listing available tools")

	// Возвращаем список всех зарегистрированных инструментов
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
				{
					"name":        "system_monitor_stream",
					"description": "Streams real-time system information: CPU and memory monitoring",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"duration": map[string]interface{}{
								"type":        "string",
								"description": "Monitoring duration (e.g., '30s', '5m')",
							},
							"interval": map[string]interface{}{
								"type":        "string",
								"description": "Update interval (e.g., '1s', '2s')",
							},
						},
						"required": []string{},
					},
				},
			},
		},
	}
}

func (h *FiberMCPHandler) handleToolCallRequest(request map[string]interface{}, session *types.Session) map[string]interface{} {
	id := request["id"]
	params, ok := request["params"].(map[string]interface{})
	if !ok {
		logger.Tools.Warn().
			Str("session_id", session.ID).
			Msg("Invalid params in tool call request")
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
		logger.Tools.Warn().
			Str("session_id", session.ID).
			Msg("Missing tool name in params")
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]interface{}{
				"code":    -32602,
				"message": "Missing tool name",
			},
		}
	}

	logger.Tools.Info().
		Str("session_id", session.ID).
		Str("tool_name", toolName).
		Msg("Executing tool")

	if toolName == "get_system_info" {
		sysInfo, err := sysinfo.Get()
		if err != nil {
			logger.Tools.Error().
				Err(err).
				Str("session_id", session.ID).
				Str("tool_name", toolName).
				Msg("Error getting system information")

			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]interface{}{
					"code":    -32603,
					"message": fmt.Sprintf("Error getting system information: %v", err),
				},
			}
		}

		logger.Tools.Debug().
			Str("session_id", session.ID).
			Str("tool_name", toolName).
			Interface("cpu_count", sysInfo.CPU.Count).
			Float64("memory_total_gb", float64(sysInfo.Memory.Total)/(1024*1024*1024)).
			Msg("System information retrieved successfully")

		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result": map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": sysInfo.FormatText(),
					},
				},
			},
		}
	}

	if toolName == "system_monitor_stream" {
		// Создаем стандартный MCP запрос для вызова инструмента через основной сервер
		arguments := make(map[string]interface{})
		if args, ok := params["arguments"].(map[string]interface{}); ok {
			arguments = args
		}

		// Создаем CallToolRequest напрямую для вызова зарегистрированного обработчика
		toolRequest := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      toolName,
				Arguments: arguments,
			},
		}

		// Вызываем обработчик напрямую
		result, err := tools.SystemMonitorStreamHandler(context.Background(), toolRequest)
		if err != nil {
			logger.Tools.Error().
				Err(err).
				Str("session_id", session.ID).
				Str("tool_name", toolName).
				Msg("Error executing system monitor stream")

			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]interface{}{
					"code":    -32603,
					"message": fmt.Sprintf("Error executing system monitor stream: %v", err),
				},
			}
		}

		logger.Tools.Debug().
			Str("session_id", session.ID).
			Str("tool_name", toolName).
			Msg("System monitor stream executed successfully")

		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result": map[string]interface{}{
				"content": result.Content,
			},
		}
	}

	logger.Tools.Warn().
		Str("session_id", session.ID).
		Str("tool_name", toolName).
		Msg("Unknown tool requested")

	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    -32601,
			"message": "Tool not found",
		},
	}
}
