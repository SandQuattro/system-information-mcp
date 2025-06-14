package middleware

import (
	"encoding/json"
	"strings"
	"time"

	"mcp-system-info/internal/logger"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// LoggingMiddleware создает middleware для логгирования HTTP запросов
func LoggingMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		// Получаем базовую информацию о запросе
		method := c.Method()
		path := c.Path()
		userAgent := c.Get("User-Agent")
		sessionID := c.Get("Mcp-Session-Id")
		if sessionID == "" {
			sessionID = "unknown"
		}

		// Создаем контекстный логгер для запроса
		requestLogger := logger.GetHTTPLogger(method, path, sessionID).With().
			Str("user_agent", userAgent).
			Str("remote_ip", c.IP()).
			Logger()

		requestLogger.Info().
			Msg("Request started")

		// Выполняем запрос
		err := c.Next()

		// Логгируем результат
		duration := time.Since(start)
		status := c.Response().StatusCode()
		responseSize := len(c.Response().Body())

		logEvent := requestLogger.Info()
		if err != nil {
			logEvent = requestLogger.Error().Err(err)
		} else if status >= 400 {
			logEvent = requestLogger.Warn()
		}

		logEvent.
			Int("status", status).
			Dur("duration", duration).
			Int("response_size", responseSize).
			Msg("Request completed")

		// Детальное логгирование для ошибок
		if status >= 500 {
			requestLogger.Error().
				Int("status", status).
				Str("method", method).
				Str("path", path).
				Dur("duration", duration).
				Int("response_size", responseSize).
				Msg("Server error occurred")
		}

		return err
	}
}

// LoggingConfig конфигурация для логгирования
type LoggingConfig struct {
	// SkipPaths пути которые нужно пропустить при логгировании
	SkipPaths []string
	// LogSlowRequests логгировать медленные запросы
	LogSlowRequests bool
	// SlowRequestThreshold порог для медленных запросов
	SlowRequestThreshold time.Duration
}

// CustomLoggingMiddleware создает настраиваемый middleware для логгирования
func CustomLoggingMiddleware(config LoggingConfig) fiber.Handler {
	if config.SlowRequestThreshold == 0 {
		config.SlowRequestThreshold = 2 * time.Second
	}

	return func(c *fiber.Ctx) error {
		start := time.Now()
		path := c.Path()

		// Пропускаем определенные пути
		for _, skipPath := range config.SkipPaths {
			if path == skipPath {
				return c.Next()
			}
		}

		method := c.Method()
		userAgent := c.Get("User-Agent")
		sessionID := c.Get("Mcp-Session-Id")
		if sessionID == "" {
			sessionID = "unknown"
		}

		requestLogger := logger.GetHTTPLogger(method, path, sessionID).With().
			Str("user_agent", userAgent).
			Str("remote_ip", c.IP()).
			Logger()

		// Логгируем начало запроса только для debug уровня
		requestLogger.Debug().
			Msg("Request started")

		err := c.Next()

		duration := time.Since(start)
		status := c.Response().StatusCode()
		responseSize := len(c.Response().Body())

		// Определяем уровень логгирования
		var logEvent *zerolog.Event
		if err != nil {
			logEvent = requestLogger.Error().Err(err)
		} else if status >= 500 {
			logEvent = requestLogger.Error()
		} else if status >= 400 {
			logEvent = requestLogger.Warn()
		} else if config.LogSlowRequests && duration > config.SlowRequestThreshold {
			logEvent = requestLogger.Warn()
		} else {
			logEvent = requestLogger.Info()
		}

		logEvent.
			Int("status", status).
			Dur("duration", duration).
			Int("response_size", responseSize).
			Msg("Request completed")

		return err
	}
}

func RequestLoggingMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		// Извлекаем информацию о клиенте из заголовков
		userAgent := c.Get("User-Agent")
		clientName := c.Get("X-Client-Name")
		clientVersion := c.Get("X-Client-Version")
		acceptHeader := c.Get("Accept")
		contentType := c.Get("Content-Type")

		// Определяем тип клиента по User-Agent и дополнительным признакам
		var clientType string
		var detectedClientName string
		var detectedClientVersion string

		// Анализируем JSON payload для более точной идентификации клиентов
		if c.Method() == "POST" && strings.Contains(contentType, "application/json") {
			body := c.Body()
			if len(body) > 0 {
				var jsonData map[string]interface{}
				if err := json.Unmarshal(body, &jsonData); err == nil {
					// Проверяем clientInfo в params для initialize запросов
					if params, ok := jsonData["params"].(map[string]interface{}); ok {
						if clientInfo, ok := params["clientInfo"].(map[string]interface{}); ok {
							if name, ok := clientInfo["name"].(string); ok {
								detectedClientName = name
								if version, ok := clientInfo["version"].(string); ok {
									detectedClientVersion = version
								}
							}
						}
					}
				}
			}
		}

		// Определяем тип клиента с учетом JSON payload
		switch {
		case strings.Contains(detectedClientName, "cursor"):
			clientType = "cursor"
		case strings.Contains(userAgent, "cursor"):
			clientType = "cursor"
		case strings.Contains(userAgent, "node") && detectedClientName == "":
			// node User-Agent без clientInfo обычно означает n8n
			clientType = "n8n"
		case strings.Contains(userAgent, "n8n"):
			clientType = "n8n"
		case strings.Contains(detectedClientName, "McpClient") || strings.Contains(userAgent, "McpClient"):
			clientType = "mcp-client"
		case strings.Contains(userAgent, "curl"):
			clientType = "curl"
		case strings.Contains(userAgent, "Postman"):
			clientType = "postman"
		default:
			clientType = "unknown"
		}

		sessionID := c.Get("Mcp-Session-Id")
		if sessionID == "" {
			sessionID = "unknown"
		}

		httpLogger := logger.HTTP.With().
			Str("session_id", sessionID).
			Str("method", c.Method()).
			Str("path", c.Path()).
			Str("remote_ip", c.IP()).
			Str("user_agent", userAgent).
			Str("client_type", clientType).
			Logger()

		// Добавляем дополнительные поля если они есть
		if clientName != "" {
			httpLogger = httpLogger.With().Str("client_name", clientName).Logger()
		}
		if clientVersion != "" {
			httpLogger = httpLogger.With().Str("client_version", clientVersion).Logger()
		}
		if detectedClientName != "" {
			httpLogger = httpLogger.With().Str("detected_client_name", detectedClientName).Logger()
		}
		if detectedClientVersion != "" {
			httpLogger = httpLogger.With().Str("detected_client_version", detectedClientVersion).Logger()
		}
		if acceptHeader != "" {
			httpLogger = httpLogger.With().Str("accept", acceptHeader).Logger()
		}
		if contentType != "" {
			httpLogger = httpLogger.With().Str("content_type", contentType).Logger()
		}

		httpLogger.Info().Msg("Request started")

		// Обрабатываем запрос
		err := c.Next()

		// Логируем завершение запроса
		duration := time.Since(start)
		status := c.Response().StatusCode()
		responseSize := len(c.Response().Body())

		logEvent := httpLogger.With().
			Dur("duration", duration).
			Int("status", status).
			Int("response_size", responseSize).
			Logger()

		if err != nil {
			logEvent.Error().
				Err(err).
				Msg("Request failed")
		} else if status >= 400 {
			logEvent.Warn().Msg("Request completed")
		} else {
			logEvent.Info().Msg("Request completed")
		}

		return err
	}
}
