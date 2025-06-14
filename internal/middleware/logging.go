package middleware

import (
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
