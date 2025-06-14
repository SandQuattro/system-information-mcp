package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"mcp-system-info/internal/handlers"

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

		sessionManager := handlers.NewSessionManager()

		handler := handlers.NewMCPHandler(mcpServer, sessionManager)

		addr := fmt.Sprintf(":%d", portInt)
		log.Printf("Starting HTTP server on port %s", port)
		log.Printf("SSE available at http://%s/sse", addr)

		if err = http.ListenAndServe(addr, handler); err != nil {
			log.Fatalf("Error starting HTTP server: %v", err)
		}
	} else {
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Error starting MCP server in stdio mode: %v", err)
		}
	}
}
