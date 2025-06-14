package tools

import (
	"context"
	"fmt"

	"mcp-system-info/internal/logger"
	"mcp-system-info/internal/sysinfo"

	"github.com/mark3labs/mcp-go/mcp"
)

// GetSystemInfoHandler возвращает текущую информацию о системе
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

	return mcp.NewToolResultText(sysInfo.FormatText()), nil
}
