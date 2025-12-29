package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
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
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"roots": map[string]bool{},
			},
			"clientInfo": map[string]string{
				"name":    "log-test",
				"version": "1.0",
			},
		},
	}
	reqData, _ := json.Marshal(initReq)
	writer.Write(reqData)
	writer.Write([]byte("\n"))
	reader.ReadString('\n')

	// 2. 发送 initialized 通知
	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	notifData, _ := json.Marshal(notif)
	writer.Write(notifData)
	writer.Write([]byte("\n"))

	fmt.Println("=== 测试请求期间日志提取 ===\n")

	// 3. 启动 phgo 项目
	fmt.Println("启动 phgo 项目（端口8080）...")
	startReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":             "phgo-server",
			"command":          "go",
			"args":             []string{"run", "main.go"},
			"work_dir":         "d:/code/phgo",
			"health_check_url": "http://localhost:8081/server",
			"timeout_seconds":  30,
		},
	}

	startReqData, _ := json.Marshal(startReq)
	writer.Write(startReqData)
	writer.Write([]byte("\n"))

	// 读取启动响应
	for i := 0; i < 3; i++ {
		line, _ := reader.ReadString('\n')
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			fmt.Println("✓ 进程启动成功")
			break
		}
	}

	// 等待进程完全启动
	time.Sleep(2 * time.Second)

	// 4. 第一次请求 - 应该包含很少的日志（只有请求日志）
	fmt.Println("\n第一次请求 /server ...")
	req1 := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "request_with_logs",
			"arguments": map[string]interface{}{
				"process_name": "phgo-server",
				"url":          "http://localhost:8080/server",
				"method":       "GET",
			},
		},
	}

	req1Data, _ := json.Marshal(req1)
	writer.Write(req1Data)
	writer.Write([]byte("\n"))

	// 读取第一次请求响应
	for i := 0; i < 5; i++ {
		line, _ := reader.ReadString('\n')
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if result, ok := resp["result"].(map[string]interface{}); ok {
			if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
				if textContent, ok := content[0].(map[string]interface{}); ok {
					if text, ok := textContent["text"].(string); ok {
						fmt.Println("\n第一次请求结果:")
						// 提取"请求期间进程日志"部分
						if idx := fmt.Sprintf("%s", text); len(idx) > 0 {
							lines := fmt.Sprintf("%s", text)
							start := strings.Index(lines, "请求期间进程日志:")
							if start >= 0 {
								logPart := lines[start:]
								fmt.Println(logPart)
							}
						}
					}
				}
			}
			break
		}
	}

	// 等待2秒
	time.Sleep(2 * time.Second)

	// 5. 第二次请求 - 应该只包含第二次请求的日志，不应该有第一次的日志
	fmt.Println("\n第二次请求 /server ...")
	req2 := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "request_with_logs",
			"arguments": map[string]interface{}{
				"process_name": "phgo-server",
				"url":          "http://localhost:8080/server",
				"method":       "GET",
			},
		},
	}

	req2Data, _ := json.Marshal(req2)
	writer.Write(req2Data)
	writer.Write([]byte("\n"))

	// 读取第二次请求响应
	for i := 0; i < 5; i++ {
		line, _ := reader.ReadString('\n')
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if result, ok := resp["result"].(map[string]interface{}); ok {
			if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
				if textContent, ok := content[0].(map[string]interface{}); ok {
					if text, ok := textContent["text"].(string); ok {
						fmt.Println("\n第二次请求结果:")
						lines := fmt.Sprintf("%s", text)
						start := strings.Index(lines, "请求期间进程日志:")
						if start >= 0 {
							logPart := lines[start:]
							fmt.Println(logPart)
						}
					}
				}
			}
			break
		}
	}

	// 6. 完成
	fmt.Println("\n测试完成，等待服务器关闭...")
	time.Sleep(1 * time.Second)

	fmt.Println("\n=== 测试完成 ===")
	fmt.Println("\n验证点:")
	fmt.Println("1. 第一次请求的日志应该只包含第一次请求的输出")
	fmt.Println("2. 第二次请求的日志应该只包含第二次请求的输出")
	fmt.Println("3. 不应该包含启动时的 [GIN-debug] 日志")

	cmd.Process.Kill()
	cmd.Wait()
}
