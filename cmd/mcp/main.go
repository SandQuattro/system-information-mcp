package main

import (
	"fmt"
	"os"
	"strconv"

	"mcp-system-info/internal/handlers"
	"mcp-system-info/internal/logger"
	"mcp-system-info/internal/middleware"
	"mcp-system-info/internal/tools"
	"mcp-system-info/internal/types"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	// Инициализируем логгер в самом начале
	logger.InitLogger()

	systemInfoTool := mcp.NewTool("get_system_info",
		mcp.WithDescription("Gets system information: CPU and memory"),
		mcp.WithString("random_string",
			mcp.Required(),
			mcp.Description("Dummy parameter for no-parameter tools"),
		),
	)

	systemMonitorStreamTool := mcp.NewTool("system_monitor_stream",
		mcp.WithDescription("Streams real-time system information: CPU and memory monitoring"),
		mcp.WithString("duration",
			mcp.Description("Monitoring duration (e.g., '30s', '5m')"),
		),
		mcp.WithString("interval",
			mcp.Description("Update interval (e.g., '1s', '2s')"),
		),
	)

	mcpServer := server.NewMCPServer("mcp-system-info", "1.0.0")
	mcpServer.AddTool(systemInfoTool, tools.GetSystemInfoHandler)
	mcpServer.AddTool(systemMonitorStreamTool, tools.SystemMonitorStreamHandler)

	// Добавляем отладочную информацию
	logger.Main.Info().
		Str("tool1", "get_system_info").
		Str("tool2", "system_monitor_stream").
		Msg("Registered MCP tools")

	if port := os.Getenv("PORT"); port != "" {
		portInt, err := strconv.Atoi(port)
		if err != nil || portInt <= 0 {
			logger.Main.Fatal().
				Str("port", port).
				Msg("Invalid PORT value")
		}

		// Создаем Fiber приложение
		app := fiber.New(fiber.Config{
			DisableStartupMessage: false,
			AppName:               "MCP System Info Server",
		})

		// Добавляем middleware для логгирования HTTP запросов с расширенной информацией о клиентах
		app.Use(middleware.RequestLoggingMiddleware())

		// Добавляем CORS middleware
		app.Use(cors.New(cors.Config{
			AllowOrigins:     "*",
			AllowMethods:     "GET,POST,OPTIONS",
			AllowHeaders:     "Content-Type,Mcp-Session-Id",
			ExposeHeaders:    "Mcp-Session-Id",
			AllowCredentials: false,
		}))

		sessionManager := types.NewSessionManager()
		mcpHandler := handlers.NewFiberMCPHandler(mcpServer, sessionManager)

		// Регистрируем маршруты
		mcpHandler.RegisterRoutes(app)

		addr := fmt.Sprintf(":%d", portInt)
		logger.Main.Info().
			Str("port", port).
			Str("addr", addr).
			Msg("Starting Fiber server")

		if err = app.Listen(addr); err != nil {
			logger.Main.Fatal().
				Err(err).
				Str("addr", addr).
				Msg("Error starting Fiber server")
		}
	} else {
		logger.Main.Info().Msg("Starting MCP server in stdio mode")
		if err := server.ServeStdio(mcpServer); err != nil {
			logger.Main.Fatal().
				Err(err).
				Msg("Error starting MCP server in stdio mode")
		}
	}
}
