# MCP System Information Server

Model Context Protocol (MCP) сервер для получения системной информации (CPU и память).

## Спецификация

<https://modelcontextprotocol.io/specification/2025-03-26/basic/transports#streamable-http>

## Reference

https://levelup.gitconnected.com/mcp-server-and-client-with-sse-the-new-streamable-http-d860850d9d9d

## Возможности сервера

- Получение информации о CPU (количество ядер, модель, загрузка)
- Получение информации о памяти (общая, доступная, используемая)
- Структурированное логгирование с помощью zerolog
- Поддержка двух режимов работы:
  - **stdio** - для интеграции с Cursor в режиме stdio и другими локальными MCP клиентами
  - **Streamable HTTP** - новый протокол согласно спецификации 2025-03-26 на корневом маршруте `/`
  - **Legacy HTTP/SSE** - для обратной совместимости на маршруте `/sse`

## Конфигурация логгирования

Сервер использует структурированное логгирование с помощью [zerolog](https://github.com/rs/zerolog) с поддержкой следующих переменных окружения:

### Переменные окружения

- **`LOG_LEVEL`** - уровень логгирования: `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`, `disabled` (по умолчанию: `info`)
- **`ENVIRONMENT`** или **`ENV`** - режим окружения: `development`/`dev` или `production`/`prod` (по умолчанию: `development`)

### Режимы логгирования

#### Режим разработки (development)

```bash
ENVIRONMENT=development LOG_LEVEL=debug ./system-info-server
```

- Красивый цветной вывод в консоль
- Читаемые временные метки (HH:MM:SS)
- Подробная информация о файлах и строках кода

#### Режим продакшена (production)

```bash
ENVIRONMENT=production LOG_LEVEL=info ./system-info-server
```

- JSON формат для парсинга логами агрегаторами
- RFC3339 временные метки
- Оптимизирован для производительности

### Примеры конфигурации

```bash
# Минимальное логгирование для продакшена
ENVIRONMENT=production LOG_LEVEL=error PORT=8080 ./system-info-server

# Максимальная детализация для отладки
ENVIRONMENT=development LOG_LEVEL=trace ./system-info-server

# Стандартная конфигурация для разработки
LOG_LEVEL=debug ./system-info-server
```

### Структура логов

Каждое логируемое событие содержит контекстные поля:

- **`component`** - компонент системы (main, http, session, mcp, tools, sysinfo, sse, streamable)
- **`session_id`** - идентификатор сессии для отслеживания запросов
- **`method`** - HTTP метод или RPC метод
- **`duration`** - время выполнения операций
- **`status`** - HTTP статус код
- **`error`** - детали ошибок с контекстом

Пример лога в режиме разработки:

```
14:30:25 INF Starting Fiber server component=main port=8080 addr=:8080
14:30:30 INF Request started component=http method=POST path=/ session_id=session_20240614_143030_abc12345
14:30:30 DBG Processing JSON-RPC request component=mcp method=initialize session_id=session_20240614_143030_abc12345
```

Пример лога в JSON формате (продакшен):

```json
{"level":"info","time":"2024-06-14T14:30:25+03:00","caller":"main.go:65","component":"main","port":"8080","addr":":8080","message":"Starting Fiber server"}
{"level":"info","time":"2024-06-14T14:30:30+03:00","caller":"middleware/logging.go:35","component":"http","method":"POST","path":"/","session_id":"session_20240614_143030_abc12345","message":"Request started"}
```

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

### Вариант 1: Локальный stdio сервер

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

### Вариант 2: Remote Streamable HTTP (новая спецификация 2025-03-26)

```json
{
  "mcpServers": {
    "system-info-remote": {
      "url": "https://your-domain.com/"
    }
  }
}
```

### Вариант 3: Legacy HTTP+SSE (для обратной совместимости)

```json
{
  "mcpServers": {
    "system-info-legacy": {
      "url": "https://your-domain.com/sse"
    }
  }
}
```

## Интеграция с n8n

### Новый формат (Streamable HTTP)

При добавлении MCP сервера в n8n укажите:

- **MCP Endpoint**: `https://your-domain.com/`

### Legacy формат (для обратной совместимости)

При добавлении MCP сервера в n8n укажите:

- **SSE Endpoint**: `https://your-domain.com/sse`

## Streamable HTTP API (новая спецификация 2025-03-26)

### Инициализация

```http
POST /
Content-Type: application/json
Accept: application/json, text/event-stream

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
Accept: application/json
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
Accept: application/json
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

### SSE поток (Streamable HTTP)

```http
GET /
Accept: text/event-stream
Mcp-Session-Id: <session-id>
```

### POST с SSE ответом

```http
POST /
Content-Type: application/json
Accept: text/event-stream
Mcp-Session-Id: <session-id>

{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "tools/call",
  "params": {
    "name": "get_system_info",
    "arguments": {}
  }
}
```

### Завершение сессии

```http
DELETE /
Mcp-Session-Id: <session-id>
```

## Legacy HTTP API (для обратной совместимости)

### Инициализация

```http
POST /sse
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2024-11-05",
    "capabilities": {},
    "clientInfo": {
      "name": "client-name",
      "version": "1.0.0"
    }
  }
}
```

### SSE подключение (Legacy)

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
# Для нового Streamable HTTP endpoint
location / {
    proxy_pass http://localhost:8080;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_set_header X-Accel-Buffering no;
    proxy_read_timeout 86400;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}

# Для Legacy SSE endpoint (обратная совместимость)
location /sse {
    proxy_pass http://localhost:8080/sse;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_set_header X-Accel-Buffering no;
    proxy_read_timeout 86400;
}
```

## Протокольные изменения

### Streamable HTTP (2025-03-26) vs Legacy HTTP+SSE (2024-11-05)

**Новая спецификация (/):**

- Единый endpoint для всех операций
- POST и GET методы на одном маршруте
- Поддержка `Accept: application/json, text/event-stream`
- Session Management через `Mcp-Session-Id` заголовок
- Resumable streams с `Last-Event-Id`
- DELETE для явного завершения сессии

**Legacy спецификация (/sse):**

- Отдельные endpoints для POST и SSE
- Событие `endpoint` при подключении к SSE
- Параметр `sessionId` в query string
- Старый формат событий SSE

## Лицензия

MIT
