# Стадия сборки
FROM golang:1.23-alpine AS builder

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.mod и go.sum для кэширования зависимостей
COPY go.mod go.sum ./

# Загружаем зависимости
RUN go mod download

# Копируем исходный код
COPY . .

# Собираем приложение
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o system-info-server .

# Финальная стадия
FROM alpine:latest

# Устанавливаем ca-certificates для HTTPS запросов
RUN apk --no-cache add ca-certificates

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

# Делаем файл исполняемым
RUN chmod +x system-info-server

# Открываем порт (если понадобится в будущем)
EXPOSE 8080

# Запускаем сервер
CMD ["./system-info-server"] 