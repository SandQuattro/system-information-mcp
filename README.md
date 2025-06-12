# MCP System Info Server

MCP сервер на Go для получения системной информации о CPU и памяти.

## Требования

- Go 1.23 или выше

## Установка

1. Перейдите в папку проекта:

```bash
cd system-information-mcp
```

2. Установите зависимости:

```bash
go mod tidy
```

## Запуск

```bash
go run main.go
```

## Использование

Сервер предоставляет tool: `get_system_info`

Возвращает информацию о системе в формате JSON:

```json
{
  "cpu": {
    "count": 8,
    "model_name": "Apple M1",
    "usage_percent": 15.5
  },
  "memory": {
    "total_bytes": 17179869184,
    "available_bytes": 8589934592,
    "used_bytes": 8589934592,
    "used_percent": 50.0
  }
}
```

## Библиотеки

- [mcp-go](https://github.com/mark3labs/mcp-go) v0.32.0 - MCP протокол
- [gopsutil](https://github.com/shirou/gopsutil) v3.24.5 - Системная информация
