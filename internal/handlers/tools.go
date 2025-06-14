package handlers

import (
	"context"
	"fmt"

	"mcp-system-info/internal/logger"
	"mcp-system-info/internal/sysinfo"

	"github.com/mark3labs/mcp-go/mcp"
)

func GetSystemInfoHandler(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	logger.Tools.Debug().Msg("Getting system information")

	sysInfo, err := sysinfo.Get()
	if err != nil {
		logger.Tools.Error().
			Err(err).
			Msg("Failed to get system information")
		return mcp.NewToolResultError(fmt.Sprintf("Error getting system information: %v", err)), nil
	}

	logger.Tools.Debug().
		Int("cpu_count", sysInfo.CPU.Count).
		Str("cpu_model", sysInfo.CPU.ModelName).
		Float64("cpu_usage", sysInfo.CPU.UsagePercent).
		Uint64("memory_total", sysInfo.Memory.Total).
		Uint64("memory_available", sysInfo.Memory.Available).
		Uint64("memory_used", sysInfo.Memory.Used).
		Float64("memory_used_percent", sysInfo.Memory.UsedPercent).
		Msg("System information retrieved successfully")

	content := fmt.Sprintf("System Information:\n\nCPU:\n- Core count: %d\n- Model: %s\n- Usage: %.2f%%\n\nMemory:\n- Total: %.2f GB\n- Available: %.2f GB\n- Used: %.2f GB (%.2f%%)",
		sysInfo.CPU.Count,
		sysInfo.CPU.ModelName,
		sysInfo.CPU.UsagePercent,
		float64(sysInfo.Memory.Total)/(1024*1024*1024),
		float64(sysInfo.Memory.Available)/(1024*1024*1024),
		float64(sysInfo.Memory.Used)/(1024*1024*1024),
		sysInfo.Memory.UsedPercent)

	return mcp.NewToolResultText(content), nil
}
