# Стадия сборки
FROM golang:1.24.2-alpine AS builder

# Принудительно устанавливаем переменные окружения для Go modules
ENV GO111MODULE=on
ENV GOPATH=""
ENV GOPROXY=https://proxy.golang.org,direct

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.mod и go.sum для кэширования зависимостей
COPY go.mod go.sum ./

# Отладочная информация - проверяем что файлы скопировались
RUN ls -la && cat go.mod

# Загружаем зависимости
RUN go mod download

# Копируем исходный код
COPY . .

# Отладочная информация - проверяем Go environment и структуру
RUN echo "=== GO ENVIRONMENT ===" && \
    go env GO111MODULE && \
    go env GOMOD && \
    go env GOPATH && \
    go env PWD && \
    echo "=== FILE STRUCTURE ===" && \
    find . -name "*.go" | head -10 && \
    echo "=== GO MOD STATUS ===" && \
    go mod verify

# Собираем приложение с принудительным включением modules и очисткой GOPATH
RUN GO111MODULE=on GOPATH="" CGO_ENABLED=0 GOOS=linux go build -a -mod=mod -installsuffix cgo -o system-info-server .
RUN chmod +x system-info-server

# Финальная стадия
FROM alpine:latest

# Устанавливаем ca-certificates для HTTPS запросов и curl для healthcheck
RUN apk --no-cache add ca-certificates curl

# Создаем пользователя для безопасности
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

# Устанавливаем рабочую директорию
WORKDIR /root/

# Копируем бинарный файл из стадии сборки
COPY --from=builder /app/system-info-server .

# Меняем владельца файла
RUN chown appuser:appgroup system-info-server

# Переключаемся на пользователя appuser
USER appuser

# Устанавливаем переменные окружения
ENV PORT=8080

# Открываем порт
EXPOSE 8080

# Запускаем сервер
CMD ["./system-info-server"] 