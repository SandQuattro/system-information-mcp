package sysinfo

import (
	"fmt"
	"runtime"
	"time"

	"mcp-system-info/internal/logger"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

func Get() (*SystemInfo, error) {
	start := time.Now()
	logger.SysInfo.Debug().Msg("Starting system information collection")

	cpuCount := runtime.NumCPU()
	logger.SysInfo.Debug().Int("cpu_count", cpuCount).Msg("Got CPU count from runtime")

	cpuInfo, err := cpu.Info()
	if err != nil {
		logger.SysInfo.Error().
			Err(err).
			Msg("Failed to get CPU information")
		return nil, fmt.Errorf("failed to get CPU information: %v", err)
	}

	var modelName string
	if len(cpuInfo) > 0 {
		modelName = cpuInfo[0].ModelName
		logger.SysInfo.Debug().
			Int("cpu_info_count", len(cpuInfo)).
			Str("model_name", modelName).
			Msg("Got CPU model information")
	} else {
		logger.SysInfo.Warn().Msg("No CPU information available")
	}

	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		logger.SysInfo.Error().
			Err(err).
			Msg("Failed to get CPU usage")
		return nil, fmt.Errorf("failed to get CPU usage: %v", err)
	}

	var usagePercent float64
	if len(cpuPercent) > 0 {
		usagePercent = cpuPercent[0]
		logger.SysInfo.Debug().
			Float64("cpu_usage_percent", usagePercent).
			Msg("Got CPU usage percentage")
	} else {
		logger.SysInfo.Warn().Msg("No CPU usage data available")
	}

	memInfo, err := mem.VirtualMemory()
	if err != nil {
		logger.SysInfo.Error().
			Err(err).
			Msg("Failed to get memory information")
		return nil, fmt.Errorf("failed to get memory information: %v", err)
	}

	logger.SysInfo.Debug().
		Uint64("memory_total", memInfo.Total).
		Uint64("memory_available", memInfo.Available).
		Uint64("memory_used", memInfo.Used).
		Float64("memory_used_percent", memInfo.UsedPercent).
		Msg("Got memory information")

	sysInfo := &SystemInfo{
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

	duration := time.Since(start)
	logger.SysInfo.Info().
		Dur("duration", duration).
		Int("cpu_count", cpuCount).
		Str("cpu_model", modelName).
		Float64("cpu_usage", usagePercent).
		Float64("memory_total_gb", float64(memInfo.Total)/(1024*1024*1024)).
		Float64("memory_used_percent", memInfo.UsedPercent).
		Msg("System information collection completed")

	return sysInfo, nil
}
