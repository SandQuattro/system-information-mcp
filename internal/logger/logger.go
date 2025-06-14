package logger

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	// Глобальные логгеры для разных компонентов
	Main       zerolog.Logger
	HTTP       zerolog.Logger
	Session    zerolog.Logger
	MCP        zerolog.Logger
	Tools      zerolog.Logger
	SysInfo    zerolog.Logger
	SSE        zerolog.Logger
	Streamable zerolog.Logger
)

// InitLogger инициализирует логгеры на основе переменных окружения
func InitLogger() {
	// Настраиваем глобальные параметры zerolog
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		return file + ":" + strconv.Itoa(line)
	}

	// Определяем уровень логгирования
	level := getLogLevel()
	zerolog.SetGlobalLevel(level)

	// Настраиваем вывод в зависимости от окружения
	var writer zerolog.ConsoleWriter
	if isDevelopmentMode() {
		// Красивый консольный вывод для разработки
		writer = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "15:04:05",
			NoColor:    false,
		}
		writer.FormatLevel = func(i interface{}) string {
			return strings.ToUpper(i.(string))
		}
		log.Logger = zerolog.New(writer).With().Timestamp().Caller().Logger()
	} else {
		// JSON вывод для продакшена
		log.Logger = zerolog.New(os.Stdout).With().Timestamp().Caller().Logger()
	}

	// Инициализируем компонентные логгеры с контекстом
	Main = log.Logger.With().Str("component", "main").Logger()
	HTTP = log.Logger.With().Str("component", "http").Logger()
	Session = log.Logger.With().Str("component", "session").Logger()
	MCP = log.Logger.With().Str("component", "mcp").Logger()
	Tools = log.Logger.With().Str("component", "tools").Logger()
	SysInfo = log.Logger.With().Str("component", "sysinfo").Logger()
	SSE = log.Logger.With().Str("component", "sse").Logger()
	Streamable = log.Logger.With().Str("component", "streamable").Logger()

	Main.Info().
		Str("level", level.String()).
		Bool("development", isDevelopmentMode()).
		Msg("Logger initialized")
}

// getLogLevel определяет уровень логгирования из переменной окружения
func getLogLevel() zerolog.Level {
	levelStr := strings.ToLower(os.Getenv("LOG_LEVEL"))
	switch levelStr {
	case "trace":
		return zerolog.TraceLevel
	case "debug":
		return zerolog.DebugLevel
	case "info", "":
		return zerolog.InfoLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	case "panic":
		return zerolog.PanicLevel
	case "disabled":
		return zerolog.Disabled
	default:
		return zerolog.InfoLevel
	}
}

// isDevelopmentMode проверяет режим разработки
func isDevelopmentMode() bool {
	env := strings.ToLower(os.Getenv("ENVIRONMENT"))
	if env == "" {
		env = strings.ToLower(os.Getenv("ENV"))
	}
	return env == "development" || env == "dev" || env == ""
}

// GetLoggerWithContext создает логгер с дополнительным контекстом
func GetLoggerWithContext(component string, fields map[string]interface{}) zerolog.Logger {
	logger := log.Logger.With().Str("component", component)

	for key, value := range fields {
		logger = logger.Interface(key, value)
	}

	return logger.Logger()
}

// GetHTTPLogger создает логгер для HTTP запросов с контекстными полями
func GetHTTPLogger(method, path, sessionID string) zerolog.Logger {
	return HTTP.With().
		Str("method", method).
		Str("path", path).
		Str("session_id", sessionID).
		Logger()
}

// GetSessionLogger создает логгер для сессий
func GetSessionLogger(sessionID string) zerolog.Logger {
	return Session.With().
		Str("session_id", sessionID).
		Logger()
}

// GetMCPLogger создает логгер для MCP операций
func GetMCPLogger(method, sessionID string) zerolog.Logger {
	return MCP.With().
		Str("method", method).
		Str("session_id", sessionID).
		Logger()
}
