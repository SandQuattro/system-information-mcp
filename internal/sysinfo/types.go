package sysinfo

// SystemInfo представляет полную информацию о системе
type SystemInfo struct {
	CPU    CPUInfo    `json:"cpu"`
	Memory MemoryInfo `json:"memory"`
}

// CPUInfo содержит информацию о процессоре
type CPUInfo struct {
	Count        int     `json:"count"`
	ModelName    string  `json:"model_name"`
	UsagePercent float64 `json:"usage_percent"`
}

// MemoryInfo содержит информацию о памяти
type MemoryInfo struct {
	Total       uint64  `json:"total_bytes"`
	Available   uint64  `json:"available_bytes"`
	Used        uint64  `json:"used_bytes"`
	UsedPercent float64 `json:"used_percent"`
}
