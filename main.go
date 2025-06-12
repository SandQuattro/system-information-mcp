package main

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

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

	// Запускаем сервер через stdio
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("Ошибка сервера: %v\n", err)
	}
}

func getSystemInfoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Получаем информацию о CPU
	cpuCount := runtime.NumCPU()

	cpuInfo, err := cpu.Info()
	if err != nil {
		return mcp.NewToolResultError("Не удалось получить информацию о CPU: " + err.Error()), nil
	}

	var modelName string
	if len(cpuInfo) > 0 {
		modelName = cpuInfo[0].ModelName
	}

	// Получаем загрузку CPU
	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		return mcp.NewToolResultError("Не удалось получить загрузку CPU: " + err.Error()), nil
	}

	var usagePercent float64
	if len(cpuPercent) > 0 {
		usagePercent = cpuPercent[0]
	}

	// Получаем информацию о памяти
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return mcp.NewToolResultError("Не удалось получить информацию о памяти: " + err.Error()), nil
	}

	// Формируем структуру с системной информацией
	sysInfo := SystemInfo{
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
	}

	// Конвертируем в JSON
	jsonData, err := json.MarshalIndent(sysInfo, "", "  ")
	if err != nil {
		return mcp.NewToolResultError("Ошибка сериализации данных: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}
