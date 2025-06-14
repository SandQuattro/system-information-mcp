package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"mcp-system-info/internal/handlers"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	systemInfoTool := mcp.NewTool("get_system_info",
		mcp.WithDescription("Gets system information: CPU and memory"),
		mcp.WithString("random_string",
			mcp.Required(),
			mcp.Description("Dummy parameter for no-parameter tools"),
		),
	)

	mcpServer := server.NewMCPServer("mcp-system-info", "1.0.0")
	mcpServer.AddTool(systemInfoTool, handlers.GetSystemInfoHandler)

	if port := os.Getenv("PORT"); port != "" {
		portInt, err := strconv.Atoi(port)
		if err != nil || portInt <= 0 {
			log.Fatal("Invalid PORT value")
		}

		// Создаем Fiber приложение
		app := fiber.New(fiber.Config{
			DisableStartupMessage: false,
			AppName:               "MCP System Info Server",
		})

		// Добавляем CORS middleware
		app.Use(cors.New(cors.Config{
			AllowOrigins:     "*",
			AllowMethods:     "GET,POST,OPTIONS,DELETE",
			AllowHeaders:     "Content-Type,Mcp-Session-Id,Accept,Last-Event-Id",
			ExposeHeaders:    "Mcp-Session-Id",
			AllowCredentials: false,
		}))

		sessionManager := handlers.NewSessionManager()
		mcpHandler := handlers.NewFiberMCPHandler(mcpServer, sessionManager)

		// Регистрируем маршруты
		mcpHandler.RegisterRoutes(app)

		addr := fmt.Sprintf(":%d", portInt)
		log.Printf("Starting Fiber server on port %s", port)

		if err = app.Listen(addr); err != nil {
			log.Fatalf("Error starting Fiber server: %v", err)
		}
	} else {
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Error starting MCP server in stdio mode: %v", err)
		}
	}
}
