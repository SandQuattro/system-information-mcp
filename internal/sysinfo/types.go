package sysinfo

import "fmt"

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

// FormatText formats system information as human-readable text
func (s *SystemInfo) FormatText() string {
	return fmt.Sprintf("System Information:\n\nCPU:\n- Core count: %d\n- Model: %s\n- Usage: %.2f%%\n\nMemory:\n- Total: %.2f GB\n- Available: %.2f GB\n- Used: %.2f GB (%.2f%%)",
		s.CPU.Count,
		s.CPU.ModelName,
		s.CPU.UsagePercent,
		float64(s.Memory.Total)/(1024*1024*1024),
		float64(s.Memory.Available)/(1024*1024*1024),
		float64(s.Memory.Used)/(1024*1024*1024),
		s.Memory.UsedPercent)
}
