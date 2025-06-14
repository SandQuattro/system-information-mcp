package sysinfo

import (
	"fmt"
	"runtime"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

func Get() (*SystemInfo, error) {
	cpuCount := runtime.NumCPU()

	cpuInfo, err := cpu.Info()
	if err != nil {
		return nil, fmt.Errorf("failed to get CPU information: %v", err)
	}

	var modelName string
	if len(cpuInfo) > 0 {
		modelName = cpuInfo[0].ModelName
	}

	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get CPU usage: %v", err)
	}

	var usagePercent float64
	if len(cpuPercent) > 0 {
		usagePercent = cpuPercent[0]
	}

	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory information: %v", err)
	}

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
