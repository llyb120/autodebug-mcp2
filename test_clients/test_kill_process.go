package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// 检查端口是否被占用，返回占用的PID
func getPortPID(port int) int {
	cmd := exec.Command("netstat", "-ano")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	lines := strings.Split(string(output), "\n")
	portStr := fmt.Sprintf(":%d", port)
	for _, line := range lines {
		if strings.Contains(line, portStr) && strings.Contains(line, "LISTENING") {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				pidStr := strings.TrimSpace(fields[len(fields)-1])
				var pid int
				fmt.Sscanf(pidStr, "%d", &pid)
				return pid
			}
		}
	}
	return 0
}

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
			continue // 忽略无法解析的行
		}

		// 检查是否是响应（有result或error字段）
		if _, hasResult := resp["result"]; hasResult {
			// 检查ID是否匹配
			if respID, ok := resp["id"].(float64); ok {
				if int(respID) == id {
					return resp, nil
				}
				// 如果ID不匹配，继续读取
			}
		}
	}
}

func checkServer(url string) bool {
	resp, err := http.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func main() {
	// 获取当前目录的父目录
	parentDir := ".."

	// 启动 MCP 服务器
	cmd := exec.Command("../gomcp.exe")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()

	defer stdin.Close()
	defer stdout.Close()

	writer := bufio.NewWriter(stdin)
	reader := bufio.NewReader(stdout)

	// 1. 初始化
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"roots": map[string]bool{},
			},
			"clientInfo": map[string]string{
				"name":    "kill-process-test",
				"version": "1.0",
			},
		},
	}
	reqData, _ := json.Marshal(initReq)
	writer.Write(reqData)
	writer.Write([]byte("\n"))
	writer.Flush()
	reader.ReadString('\n') // 读取响应

	// 2. 发送 initialized 通知
	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	notifData, _ := json.Marshal(notif)
	writer.Write(notifData)
	writer.Write([]byte("\n"))
	writer.Flush()

	// 等待服务器初始化
	time.Sleep(1 * time.Second)

	toolID := 2

	// 测试1: 启动一个测试服务器
	fmt.Println("=== 测试1: 启动测试服务器 ===")
	startResp, err := callTool(writer, reader, toolID, "start_process", map[string]any{
		"name":             "test_server",
		"command":          "go",
		"args":             []string{"run", "test_servers/simple_server.go"},
		"work_dir":         parentDir,
		"health_check_url": "http://localhost:18080/health",
		"timeout_seconds":  30,
	})
	toolID++

	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}

	if result, ok := startResp["result"].(map[string]any); ok {
		if content, ok := result["content"].([]any); ok && len(content) > 0 {
			if textContent, ok := content[0].(map[string]any); ok {
				if text, ok := textContent["text"].(string); ok {
					fmt.Printf("启动结果:\n%s\n\n", text)
				}
			}
		}
	}

	// 等待服务器完全启动
	time.Sleep(2 * time.Second)

	// 验证服务器是否正常运行
	if checkServer("http://localhost:18080/health") {
		fmt.Printf("✓ 服务器运行正常\n\n")
	} else {
		fmt.Printf("✗ 服务器访问失败\n\n")
	}

	// 测试2: 通过 name 杀掉进程
	fmt.Println("=== 测试2: 通过 name 杀掉进程 ===")
	killResp1, err := callTool(writer, reader, toolID, "kill_process", map[string]any{
		"name": "test_server",
	})
	toolID++

	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}

	if result, ok := killResp1["result"].(map[string]any); ok {
		if content, ok := result["content"].([]any); ok && len(content) > 0 {
			if textContent, ok := content[0].(map[string]any); ok {
				if text, ok := textContent["text"].(string); ok {
					fmt.Printf("杀掉结果:\n%s\n\n", text)
				}
			}
		}
	}

	// 验证服务器是否已关闭
	fmt.Printf("检查端口状态...\n")
	for i := 0; i < 5; i++ {
		time.Sleep(1 * time.Second)
		portPID := getPortPID(18080)
		serverRunning := checkServer("http://localhost:18080/health")
		fmt.Printf("  第%d秒: 端口PID=%d, HTTP响应=%v\n", i+1, portPID, serverRunning)

		if !serverRunning {
			fmt.Printf("✓ 服务器已关闭\n\n")
			break
		}
		if i == 4 {
			fmt.Printf("✗ 服务器仍在运行（等待5秒后仍无法关闭）\n\n")
		}
	}

	// 测试3: 再次启动服务器
	fmt.Println("=== 测试3: 再次启动服务器 ===")
	startResp2, err := callTool(writer, reader, toolID, "start_process", map[string]any{
		"name":             "test_server2",
		"command":          "go",
		"args":             []string{"run", "test_servers/simple_server.go"},
		"work_dir":         parentDir,
		"health_check_url": "http://localhost:18080/health",
		"timeout_seconds":  30,
	})
	toolID++

	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}

	if result, ok := startResp2["result"].(map[string]any); ok {
		if content, ok := result["content"].([]any); ok && len(content) > 0 {
			if textContent, ok := content[0].(map[string]any); ok {
				if text, ok := textContent["text"].(string); ok {
					fmt.Printf("启动结果:\n%s\n\n", text)
				}
			}
		}
	}

	// 等待服务器启动
	time.Sleep(2 * time.Second)

	// 测试4: 通过 port 杀掉进程
	fmt.Println("=== 测试4: 通过 port 杀掉进程 ===")
	killResp2, err := callTool(writer, reader, toolID, "kill_process", map[string]any{
		"port": 18080,
	})
	toolID++

	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}

	// 打印完整响应用于调试
	respJSON, _ := json.MarshalIndent(killResp2, "", "  ")
	fmt.Printf("完整响应: %s\n\n", respJSON)

	if result, ok := killResp2["result"].(map[string]any); ok {
		if content, ok := result["content"].([]any); ok && len(content) > 0 {
			if textContent, ok := content[0].(map[string]any); ok {
				if text, ok := textContent["text"].(string); ok {
					fmt.Printf("杀掉结果:\n%s\n\n", text)
				}
			}
		}
	}

	// 验证服务器是否已关闭
	time.Sleep(3 * time.Second)
	if !checkServer("http://localhost:18080/health") {
		fmt.Printf("✓ 服务器已关闭\n\n")
	} else {
		fmt.Printf("✗ 服务器仍在运行\n\n")
	}

	// 测试5: 测试不存在的 name
	fmt.Println("=== 测试5: 测试不存在的 name ===")
	killResp3, err := callTool(writer, reader, toolID, "kill_process", map[string]any{
		"name": "nonexistent",
	})
	toolID++

	if err != nil {
		fmt.Printf("错误: %v\n", err)
	} else if result, ok := killResp3["result"].(map[string]any); ok {
		if content, ok := result["content"].([]any); ok && len(content) > 0 {
			if textContent, ok := content[0].(map[string]any); ok {
				if text, ok := textContent["text"].(string); ok {
					fmt.Printf("结果:\n%s\n\n", text)
				}
			}
		}
	}

	fmt.Println("=== 所有测试完成 ===")

	// 等待一下让服务器输出日志
	time.Sleep(1 * time.Second)
}
