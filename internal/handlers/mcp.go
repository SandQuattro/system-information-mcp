package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"mcp-system-info/internal/logger"
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
	// Простой JSON-RPC endpoint
	app.Post("/", h.HandleJSONRPC)
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
