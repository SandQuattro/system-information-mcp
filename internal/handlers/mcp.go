package handlers

import (
	"fmt"
	"log"
	"sync"

	"mcp-system-info/internal/sse"
	"mcp-system-info/internal/streamable"
	"mcp-system-info/internal/sysinfo"
	"mcp-system-info/internal/types"

	"github.com/gofiber/fiber/v2"
	"github.com/mark3labs/mcp-go/server"
)

type FiberMCPHandler struct {
	server               *server.MCPServer
	sessionManager       *types.SessionManager
	lastCreatedSessionID sync.Map
	streamableHandler    *streamable.Handler
	legacyHandler        *sse.Handler
}

func NewFiberMCPHandler(server *server.MCPServer, sessionManager *types.SessionManager) *FiberMCPHandler {
	handler := &FiberMCPHandler{
		server:         server,
		sessionManager: sessionManager,
	}

	// Создаем handlers для разных протоколов
	handler.streamableHandler = streamable.NewHandler(sessionManager, &handler.lastCreatedSessionID, handler.handleJSONRPCMessage)
	handler.legacyHandler = sse.NewHandler(sessionManager, &handler.lastCreatedSessionID, handler.handleJSONRPCMessage)

	return handler
}

func (h *FiberMCPHandler) RegisterRoutes(app *fiber.App) {
	// Streamable HTTP на корневом маршруте (2025-03-26)
	app.Post("/", h.streamableHandler.HandlePost)
	app.Get("/", h.streamableHandler.HandleGet)
	app.Delete("/", h.streamableHandler.HandleDelete)

	// Legacy SSE для обратной совместимости (2024-11-05)
	app.Post("/sse", h.legacyHandler.HandlePost)
	app.Get("/sse", h.legacyHandler.HandleSSE)
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

func (h *FiberMCPHandler) handleToolsListRequest(request map[string]interface{}, session *types.Session) map[string]interface{} {
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

func (h *FiberMCPHandler) handleToolCallRequest(request map[string]interface{}, session *types.Session) map[string]interface{} {
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
