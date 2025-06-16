package middleware

import (
	"strings"

	"mcp-system-info/internal/logger"

	"github.com/gofiber/fiber/v2"
)

// AuthConfig конфигурация для middleware авторизации
type AuthConfig struct {
	// APIKey API ключ для доступа к MCP endpoints
	APIKey string
	// AllowedUserAgents список разрешенных User-Agent префиксов
	AllowedUserAgents []string
	// SkipPaths пути которые нужно пропустить при проверке авторизации
	SkipPaths []string
}

// AuthMiddleware создает middleware для проверки авторизации MCP запросов
func AuthMiddleware() fiber.Handler {
	// Дефолтная конфигурация
	config := AuthConfig{
		APIKey: "mcp-secret-key-2025", // хардкодное значение как запросил пользователь
		AllowedUserAgents: []string{
			"Cursor/", // Cursor клиент
		},
		SkipPaths: []string{
			"/", // Health check
		},
	}

	return AuthMiddlewareWithConfig(config)
}

// AuthMiddlewareWithConfig создает middleware для авторизации с настраиваемой конфигурацией
func AuthMiddlewareWithConfig(config AuthConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		path := c.Path()
		method := c.Method()
		userAgent := c.Get("User-Agent")
		apiKey := c.Get("X-API-Key")
		sessionID := c.Get("Mcp-Session-Id", "unknown")

		authLogger := logger.HTTP.With().
			Str("session_id", sessionID).
			Str("method", method).
			Str("path", path).
			Str("remote_ip", c.IP()).
			Str("user_agent", userAgent).
			Logger()

		// Пропускаем проверку для определенных путей
		for _, skipPath := range config.SkipPaths {
			if path == skipPath {
				authLogger.Debug().
					Str("skip_reason", "path_in_skip_list").
					Msg("Auth check skipped")
				return c.Next()
			}
		}

		// Проверяем если это Cursor - пропускаем без проверки API ключа
		isCursorClient := false
		for _, allowedUA := range config.AllowedUserAgents {
			if strings.HasPrefix(userAgent, allowedUA) {
				isCursorClient = true
				break
			}
		}

		if isCursorClient {
			authLogger.Debug().
				Msg("Cursor client detected - skipping API key check")
			return c.Next()
		}

		// Для всех остальных клиентов проверяем API ключ
		if apiKey != config.APIKey {
			authLogger.Warn().
				Str("provided_api_key", maskAPIKey(apiKey)).
				Str("expected_api_key", maskAPIKey(config.APIKey)).
				Msg("Non-Cursor client with invalid API key")

			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Unauthorized",
				"message": "API key required",
				"code":    "AUTH_INVALID_API_KEY",
			})
		}

		authLogger.Debug().
			Msg("Non-Cursor client authorized with valid API key")

		return c.Next()
	}
}

// maskAPIKey маскирует API ключ для безопасного логгирования
func maskAPIKey(key string) string {
	if key == "" {
		return "empty"
	}
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "***" + key[len(key)-4:]
}
