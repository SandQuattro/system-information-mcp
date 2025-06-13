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

	"mcp-system-info/internal/sysinfo"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// MCPHandler - HTTP обработчик для MCP сервера
type MCPHandler struct {
	server *server.MCPServer
}

func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Добавляем CORS заголовки
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

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

	// sse streaming
	if r.URL.Path == "/sse" {
		sseHandler(w, r)
		return
	}

	// Обработка MCP запросов
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	// Простая JSON-RPC обработка для MCP через HTTP
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Ошибка чтения запроса", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	log.Printf("Получен MCP запрос: %s", string(body))

	// Парсим JSON-RPC запрос
	var request map[string]interface{}
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, "Неверный JSON", http.StatusBadRequest)
		return
	}

	// Обрабатываем разные типы MCP запросов
	method, ok := request["method"].(string)
	if !ok {
		http.Error(w, "Отсутствует метод", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      request["id"],
	}

	switch method {
	case "tools/list":
		response["result"] = map[string]interface{}{
			"tools": []map[string]interface{}{
				{
					"name":        "get_system_info",
					"description": "Получает информацию о системе: CPU и память",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
			},
		}
	case "tools/call":
		// Обрабатываем вызов инструмента
		params, ok := request["params"].(map[string]interface{})
		if !ok {
			response["error"] = map[string]interface{}{
				"code":    -32602,
				"message": "Неверные параметры",
			}
		} else {
			toolName, ok := params["name"].(string)
			if !ok || toolName != "get_system_info" {
				response["error"] = map[string]interface{}{
					"code":    -32601,
					"message": "Неизвестный инструмент",
				}
			} else {
				// Получаем системную информацию напрямую
				sysInfo, err := sysinfo.Get()
				if err != nil {
					response["error"] = map[string]interface{}{
						"code":    -32603,
						"message": err.Error(),
					}
				} else {
					jsonData, _ := json.MarshalIndent(sysInfo, "", "  ")
					response["result"] = map[string]interface{}{
						"content": []map[string]interface{}{
							{
								"type": "text",
								"text": string(jsonData),
							},
						},
					}
				}
			}
		}
	case "initialize":
		response["result"] = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "System Info Server",
				"version": "1.0.0",
			},
		}
	default:
		response["error"] = map[string]interface{}{
			"code":    -32601,
			"message": "Метод не найден",
		}
	}

	json.NewEncoder(w).Encode(response)
}

// sseHandler - обработчик SSE для отправки системной информации в реальном времени
func sseHandler(w http.ResponseWriter, r *http.Request) {
	// Настройка заголовков для SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	sysInfo, err := sysinfo.Get()
	if err != nil {
		log.Printf("Ошибка получения системной информации: %v", err)
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
	} else {
		jsonData, err := json.Marshal(sysInfo)
		if err != nil {
			log.Printf("Ошибка сериализации: %v", err)
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		} else {
			// Отправляем данные
			fmt.Fprintf(w, "event: system-info\ndata: %s\n\n", string(jsonData))
		}
	}

	// Сбрасываем буфер
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	} else {
		log.Println("Flusher не поддерживается")
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

		// Создаем HTTP обработчик для MCP сервера
		handler := &MCPHandler{server: s}

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
