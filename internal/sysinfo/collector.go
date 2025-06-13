package sysinfo

import (
	"fmt"
	"runtime"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

// Get возвращает текущую информацию о системе (CPU и память)
func Get() (*SystemInfo, error) {
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
