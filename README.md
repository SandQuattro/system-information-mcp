# MCP System Information Server

Model Context Protocol (MCP) сервер для получения системной информации (CPU и память).

# Спекификация

https://modelcontextprotocol.io/specification/2025-03-26/basic/transports#streamable-http

## Возможности

- Получение информации о CPU (количество ядер, модель, загрузка)
- Получение информации о памяти (общая, доступная, используемая)
- Поддержка двух режимов работы:
  - **stdio** - для интеграции с Cursor в режиме stdio и другими локальными MCP клиентами
  - **HTTP/SSE** - для веб-интеграции (n8n, браузеры)

## Установка и запуск

### Сборка из исходников

```bash
go build -o system-info-server .
```

### Запуск в режиме stdio (для Cursor и других локальных MCP клиентов)

```bash
./system-info-server
```

### Запуск в режиме HTTP сервера

```bash
PORT=8080 ./system-info-server
```

## Интеграция с Cursor

Добавьте в файл `~/.cursor/mcp.json`:

### Вариант 1
```json
{
  "mcpServers": {
    "system-info-local": {
      "command": "/path/to/system-info-server",
      "args": []
    }
  }
}
```

### Вариант 2
```json
{
  "mcpServers": {
    "system-info-remote": {
      "url": "https://your-domain.com/sse"
    }
  }
}
```

## Интеграция с n8n

При добавлении MCP сервера в n8n укажите:

- **SSE Endpoint**: `https://your-domain.com/sse`

## HTTP API

### Инициализация

```http
POST /
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-03-26",
    "capabilities": {},
    "clientInfo": {
      "name": "client-name",
      "version": "1.0.0"
    }
  }
}
```

Ответ содержит заголовок `Mcp-Session-Id`, который нужно использовать во всех последующих запросах.

### Получение списка инструментов

```http
POST /
Content-Type: application/json
Mcp-Session-Id: <session-id>

{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/list"
}
```

### Вызов инструмента

```http
POST /
Content-Type: application/json
Mcp-Session-Id: <session-id>

{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "get_system_info",
    "arguments": {}
  }
}
```

### SSE подключение

```http
GET /sse?sessionId=<session-id>
Accept: text/event-stream
```

## Docker

### Сборка образа

```bash
docker build -t mcp-system-info .
```

### Запуск контейнера

```bash
# HTTP режим
docker run -p 8080:8080 -e PORT=8080 mcp-system-info

# stdio режим
docker run -it mcp-system-info
```

### Docker Compose

```bash
docker-compose up -d
```

## Развертывание на сервере

При развертывании за nginx добавьте в конфигурацию:

```nginx
location /sse {
    proxy_pass http://localhost:8080;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_set_header X-Accel-Buffering no;
    proxy_read_timeout 86400;
}
```

## Лицензия

MIT
