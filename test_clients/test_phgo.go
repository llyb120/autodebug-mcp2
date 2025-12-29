package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

func main() {
	// 启动 MCP 服务器
	cmd := exec.Command("../gomcp.exe")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()

	defer stdin.Close()
	defer stdout.Close()

	writer := stdin
	reader := bufio.NewReader(stdout)

	// 1. 初始化
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"roots": map[string]bool{},
			},
			"clientInfo": map[string]string{
				"name": "phgo-test",
				"version": "1.0",
			},
		},
	}
	reqData, _ := json.Marshal(initReq)
	writer.Write(reqData)
	writer.Write([]byte("\n"))
	reader.ReadString('\n') // 读取响应

	// 2. 发送 initialized 通知
	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method": "notifications/initialized",
	}
	notifData, _ := json.Marshal(notif)
	writer.Write(notifData)
	writer.Write([]byte("\n"))

	fmt.Println("=== 测试启动 phgo 项目 ===\n")

	// 3. 启动 phgo 项目
	fmt.Println("正在启动 phgo 项目...")
	fmt.Println("工作目录: d:/code/phgo")
	fmt.Println("健康检查: http://localhost:8081/server")
	fmt.Println()

	startReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id": 2,
		"method": "tools/call",
		"params": map[string]interface{}{
			"name": "start_process",
			"arguments": map[string]interface{}{
				"name":             "phgo-server",
				"command":          "go",
				"args":             []string{"run", "main.go"},
				"work_dir":         "d:/code/phgo",
				"health_check_url": "http://localhost:8081/server",
				"timeout_seconds":  30,
			},
		},
	}

	startReqData, _ := json.Marshal(startReq)
	writer.Write(startReqData)
	writer.Write([]byte("\n"))

	// 读取响应（可能有多个通知）
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		fmt.Printf("响应: %s\n", line)

		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			// 解析结果
			if result, ok := resp["result"].(map[string]interface{}); ok {
				if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
					if textContent, ok := content[0].(map[string]interface{}); ok {
						if text, ok := textContent["text"].(string); ok {
							fmt.Printf("\n启动结果:\n%s\n", text)
						}
					}
				}
			}
			break
		}
	}

	// 等待足够长时间让所有启动日志输出完毕
	fmt.Println("\n等待 10 秒让所有启动日志输出完毕...")
	time.Sleep(10 * time.Second)

	// 4. 测试 API 接口（使用进程名自动替换host和port）
	fmt.Println("\n正在测试 API 接口...")
	apiReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id": 3,
		"method": "tools/call",
		"params": map[string]interface{}{
			"name": "request_with_logs",
			"arguments": map[string]interface{}{
				"process_name": "phgo-server",
				"url":          "/server",
				"method":       "GET",
			},
		},
	}

	apiReqData, _ := json.Marshal(apiReq)
	writer.Write(apiReqData)
	writer.Write([]byte("\n"))

	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			fmt.Printf("API 测试响应: %s\n", line)
			break
		}
	}

	// 5. 终止进程
	fmt.Println("\n正在终止进程...")
	killReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id": 4,
		"method": "tools/call",
		"params": map[string]interface{}{
			"name": "kill_process",
			"arguments": map[string]interface{}{
				"name": "phgo-server",
			},
		},
	}

	killReqData, _ := json.Marshal(killReq)
	writer.Write(killReqData)
	writer.Write([]byte("\n"))

	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			fmt.Printf("终止响应: %s\n", line)
			break
		}
	}

	fmt.Println("\n=== 测试完成 ===")
	cmd.Process.Kill()
	cmd.Wait()
}
