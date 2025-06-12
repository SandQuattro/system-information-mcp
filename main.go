package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

type SystemInfo struct {
	CPU    CPUInfo    `json:"cpu"`
	Memory MemoryInfo `json:"memory"`
}

type CPUInfo struct {
	Count        int     `json:"count"`
	ModelName    string  `json:"model_name"`
	UsagePercent float64 `json:"usage_percent"`
}

type MemoryInfo struct {
	Total       uint64  `json:"total_bytes"`
	Available   uint64  `json:"available_bytes"`
	Used        uint64  `json:"used_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

// MCPHandler - HTTP обработчик для MCP сервера
type MCPHandler struct {
	server *server.MCPServer
}

// ServeHTTP - реализация http.Handler интерфейса
func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Обработка SSE запросов
	if r.URL.Path == "/sse" {
		sseHandler(w, r)
		return
	}

	// Обработка MCP запросов
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	// Читаем тело запроса
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Ошибка чтения запроса", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Логируем тело запроса для отладки
	log.Printf("Получен запрос: %s", string(body))

	// Простой ответ для проверки работы сервера
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","message":"MCP System Info Server работает"}`))
}

// sseHandler - обработчик SSE для отправки системной информации в реальном времени
func sseHandler(w http.ResponseWriter, r *http.Request) {
	// Настройка заголовков для SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Бесконечный цикл отправки данных
	for {
		// Получаем системную информацию
		sysInfo, err := getSystemInfo()
		if err != nil {
			log.Printf("Ошибка получения системной информации: %v", err)
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		} else {
			// Сериализуем в JSON
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
			break
		}

		// Задержка между обновлениями
		time.Sleep(1 * time.Second)
	}
}

// getSystemInfo - получение информации о системе
func getSystemInfo() (*SystemInfo, error) {
	// Получаем информацию о CPU
	cpuCount := runtime.NumCPU()

	cpuInfo, err := cpu.Info()
	if err != nil {
		return nil, fmt.Errorf("не удалось получить информацию о CPU: %v", err)
	}

	var modelName string
	if len(cpuInfo) > 0 {
		modelName = cpuInfo[0].ModelName
	}

	// Получаем загрузку CPU
	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить загрузку CPU: %v", err)
	}

	var usagePercent float64
	if len(cpuPercent) > 0 {
		usagePercent = cpuPercent[0]
	}

	// Получаем информацию о памяти
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("не удалось получить информацию о памяти: %v", err)
	}

	// Формируем структуру с системной информацией
	return &SystemInfo{
		CPU: CPUInfo{
			Count:        cpuCount,
			ModelName:    modelName,
			UsagePercent: usagePercent,
		},
		Memory: MemoryInfo{
			Total:       memInfo.Total,
			Available:   memInfo.Available,
			Used:        memInfo.Used,
			UsedPercent: memInfo.UsedPercent,
		},
	}, nil
}

func main() {
	// Создаем новый MCP сервер
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
			fmt.Printf("Ошибка преобразования порта: %v, использую порт 8080\n", err)
			portNum = 8080
		}

		// Создаем HTTP обработчик для MCP сервера
		handler := &MCPHandler{server: s}

		addr := fmt.Sprintf(":%d", portNum)
		fmt.Printf("Запуск HTTP сервера на порту %d\n", portNum)
		fmt.Printf("SSE доступен по адресу http://localhost:%d/sse\n", portNum)
		if err := http.ListenAndServe(addr, handler); err != nil {
			log.Fatalf("Ошибка HTTP сервера: %v\n", err)
		}
	} else {
		// Запускаем сервер через stdio, если порт не указан
		fmt.Println("Запуск сервера через stdio")
		if err := server.ServeStdio(s); err != nil {
			fmt.Printf("Ошибка сервера: %v\n", err)
		}
	}
}

func getSystemInfoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Получаем системную информацию
	sysInfo, err := getSystemInfo()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Конвертируем в JSON
	jsonData, err := json.MarshalIndent(sysInfo, "", "  ")
	if err != nil {
		return mcp.NewToolResultError("Ошибка сериализации данных: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}
