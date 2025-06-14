package handlers

import (
	"context"
	"fmt"

	"mcp-system-info/internal/sysinfo"

	"github.com/mark3labs/mcp-go/mcp"
)

func GetSystemInfoHandler(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sysInfo, err := sysinfo.Get()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error getting system information: %v", err)), nil
	}

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
