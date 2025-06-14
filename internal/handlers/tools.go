package handlers

import (
	"context"
	"fmt"
	"time"

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

	return mcp.NewToolResultText(sysInfo.FormatText()), nil
}

// SystemMonitorStreamHandler стримит системную информацию в реальном времени
func SystemMonitorStreamHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	logger.Tools.Info().
		Str("tool", "system_monitor_stream").
		Msg("Starting real-time system monitoring stream")

		// Получаем параметры из запроса
	args := request.Params.Arguments
	var durationStr, intervalStr string

	if argsMap, ok := args.(map[string]interface{}); ok {
		if dur, exists := argsMap["duration"]; exists {
			if durStr, ok := dur.(string); ok {
				durationStr = durStr
			}
		}
		if inter, exists := argsMap["interval"]; exists {
			if interStr, ok := inter.(string); ok {
				intervalStr = interStr
			}
		}
	}

	if durationStr == "" {
		durationStr = "30s" // по умолчанию 30 секунд
	}
	if intervalStr == "" {
		intervalStr = "2s" // по умолчанию каждые 2 секунды
	}

	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		logger.Tools.Error().
			Err(err).
			Str("duration", durationStr).
			Msg("Invalid duration format")
		return mcp.NewToolResultError(fmt.Sprintf("Invalid duration format: %v", err)), nil
	}

	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		logger.Tools.Error().
			Err(err).
			Str("interval", intervalStr).
			Msg("Invalid interval format")
		return mcp.NewToolResultError(fmt.Sprintf("Invalid interval format: %v", err)), nil
	}

	logger.Tools.Info().
		Dur("duration", duration).
		Dur("interval", interval).
		Msg("System monitoring stream configured")

	// Создаем буфер для накопления результатов
	var streamResults []string
	endTime := time.Now().Add(duration)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	streamResults = append(streamResults, "🔄 System Monitor Stream Started\n")
	streamResults = append(streamResults, fmt.Sprintf("⏱️  Duration: %v, Interval: %v\n", duration, interval))
	streamResults = append(streamResults, "📊 Collecting data...\n\n")

	iteration := 0
	for {
		select {
		case <-ctx.Done():
			logger.Tools.Info().Msg("Context cancelled, stopping stream")
			streamResults = append(streamResults, "❌ Stream cancelled by context\n")
			return mcp.NewToolResultText(joinResults(streamResults)), nil

		case <-ticker.C:
			if time.Now().After(endTime) {
				logger.Tools.Info().Msg("Duration expired, stopping stream")
				streamResults = append(streamResults, "✅ Stream completed successfully\n")
				return mcp.NewToolResultText(joinResults(streamResults)), nil
			}

			iteration++

			// Получаем текущую системную информацию
			sysInfo, err := sysinfo.Get()
			if err != nil {
				logger.Tools.Error().
					Err(err).
					Int("iteration", iteration).
					Msg("Failed to get system information during stream")
				streamResults = append(streamResults, fmt.Sprintf("❌ Error at iteration %d: %v\n", iteration, err))
				continue
			}

			// Форматируем данные для стрима
			timestamp := time.Now().Format("15:04:05")
			streamData := fmt.Sprintf("📈 Sample #%d at %s:\n", iteration, timestamp)
			streamData += fmt.Sprintf("  💻 CPU: %s (%d cores) - %.1f%% usage\n",
				sysInfo.CPU.ModelName, sysInfo.CPU.Count, sysInfo.CPU.UsagePercent)
			streamData += fmt.Sprintf("  🧠 Memory: %.1f GB used / %.1f GB total (%.1f%%)\n",
				float64(sysInfo.Memory.Used)/(1024*1024*1024),
				float64(sysInfo.Memory.Total)/(1024*1024*1024),
				sysInfo.Memory.UsedPercent)
			streamData += fmt.Sprintf("  💾 Available: %.1f GB\n\n",
				float64(sysInfo.Memory.Available)/(1024*1024*1024))

			streamResults = append(streamResults, streamData)

			logger.Tools.Debug().
				Int("iteration", iteration).
				Float64("cpu_usage", sysInfo.CPU.UsagePercent).
				Float64("memory_usage", sysInfo.Memory.UsedPercent).
				Msg("Stream data collected")
		}
	}
}

// joinResults объединяет результаты стрима в единый текст
func joinResults(results []string) string {
	var output string
	for _, result := range results {
		output += result
	}
	return output
}
