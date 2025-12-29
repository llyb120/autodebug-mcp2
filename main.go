package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	// 初始化日志系统
	if err := InitLogger(); err != nil {
		log.Fatalf("初始化日志系统失败: %v", err)
	}
	defer GetLogger().Close()

	logger := GetLogger()
	logger.Info("=== GoMCP 服务器启动 ===")

	// 创建带信号处理的上下文
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// 创建 MCP 服务器
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "go-mcp-server",
		Version: "1.0.0",
	}, nil)

	logger.Info("MCP 服务器已创建")

	// 注册所有工具
	RegisterTools(server)
	logger.Info("所有工具已注册")

	logger.Info("服务器准备就绪，等待连接...")

	// 通过 stdio 启动服务器
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && err != context.Canceled {
		logger.Error("MCP 服务器停止时发生错误: %v", err)
		log.Fatalf("MCP server stopped with error: %v", err)
	}

	logger.Info("=== GoMCP 服务器正常关闭 ===")
}
