package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

func callTool(writer *bufio.Writer, reader *bufio.Reader, id int, name string, args map[string]any) (map[string]any, error) {
	// 构建请求
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      name,
			"arguments": args,
		},
	}

	reqData, _ := json.Marshal(req)
	writer.Write(reqData)
	writer.Write([]byte("\n"))
	writer.Flush()

	// 读取响应，直到找到匹配的ID
	for {
		respLine, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		var resp map[string]any
		if err := json.Unmarshal([]byte(respLine), &resp); err != nil {
			continue
		}

		if _, hasResult := resp["result"]; hasResult {
			if respID, ok := resp["id"].(float64); ok {
				if int(respID) == id {
					return resp, nil
				}
			}
		}
	}
}

func main() {
	// 启动 MCP 服务器
	cmd := exec.Command("../gomcp.exe")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()

	defer stdin.Close()
	defer stdout.Close()

	writer := bufio.NewWriter(stdin)
	reader := bufio.NewReader(stdout)

	// 初始化
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]string{
				"name":    "serial-execution-test",
				"version": "1.0",
			},
		},
	}
	reqData, _ := json.Marshal(initReq)
	writer.Write(reqData)
	writer.Write([]byte("\n"))
	writer.Flush()
	reader.ReadString('\n')

	// 发送 initialized 通知
	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	notifData, _ := json.Marshal(notif)
	writer.Write(notifData)
	writer.Write([]byte("\n"))
	writer.Flush()

	time.Sleep(1 * time.Second)

	fmt.Println("=== 测试串行执行 ===")
	fmt.Println("连续启动3个进程，观察执行时间...")
	fmt.Println("如果是串行执行，总时间约等于各进程启动时间之和")
	fmt.Println("如果是并发执行，总时间约等于单个进程启动时间")
	fmt.Println()

	startTime := time.Now()
	toolID := 2

	// 连续启动3个进程（顺序调用）
	for i := 1; i <= 3; i++ {
		processName := fmt.Sprintf("serial_test_%d", i)
		port := 18100 + i

		processStart := time.Now()
		fmt.Printf("[%s] 开始启动进程 %s (端口 %d)\n", processStart.Format("15:04:05"), processName, port)

		resp, err := callTool(writer, reader, toolID, "start_process", map[string]any{
			"name":             processName,
			"command":          "go",
			"args":             []string{"run", "../test_servers/simple_server.go"},
			"work_dir":         "..",
			"health_check_url": fmt.Sprintf("http://localhost:%d/health", port),
			"timeout_seconds":  10,
		})
		toolID++

		if err != nil {
			fmt.Printf("[%s] 进程 %s 启动失败: %v\n", time.Now().Format("15:04:05"), processName, err)
		} else {
			elapsed := time.Since(processStart)
			if result, ok := resp["result"].(map[string]any); ok {
				if isError, _ := result["isError"].(bool); isError {
					fmt.Printf("[%s] 进程 %s 启动失败 (耗时: %.2fs)\n", time.Now().Format("15:04:05"), processName, elapsed.Seconds())
				} else {
					fmt.Printf("[%s] 进程 %s 启动成功 (耗时: %.2fs)\n", time.Now().Format("15:04:05"), processName, elapsed.Seconds())
				}
			}
		}
	}

	totalElapsed := time.Since(startTime)

	fmt.Println()
	fmt.Println("=== 测试结果 ===")
	fmt.Printf("总耗时: %.2fs\n", totalElapsed.Seconds())
	fmt.Println()
	fmt.Println("分析:")
	fmt.Println("- 如果是串行执行: 总耗时应该约为15-30秒（3个进程 × 5-10秒/进程）")
	fmt.Println("- 如果是并发执行: 总耗时应该约为5-10秒（接近单个进程启动时间）")
	fmt.Printf("- 当前情况: %s\n", func() string {
		if totalElapsed.Seconds() > 20 {
			return "✓ 串行执行（工具按顺序执行，互不干扰）"
		}
		return "✗ 并发执行（工具同时执行）"
	}())

	// 清理：杀掉所有测试进程
	fmt.Println("\n清理测试进程...")
	for i := 1; i <= 3; i++ {
		port := 18100 + i
		callTool(writer, reader, toolID, "kill_process", map[string]any{
			"port": port,
		})
		toolID++
	}

	time.Sleep(2 * time.Second)
}
