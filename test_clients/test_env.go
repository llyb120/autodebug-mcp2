package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
)

func main() {
	cmd := exec.Command("d:/code/gomcp/gomcp.exe")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()

	defer stdin.Close()
	defer stdout.Close()

	writer := stdin
	reader := bufio.NewReader(stdout)

	// 1. 发送 initialize 请求
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"roots": map[string]bool{}},
			"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
		},
	}
	reqData, _ := json.Marshal(initReq)
	writer.Write(reqData)
	writer.Write([]byte("\n"))
	reader.ReadString('\n') // 读取并忽略响应

	// 2. 发送 initialized 通知
	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	notifData, _ := json.Marshal(notif)
	writer.Write(notifData)
	writer.Write([]byte("\n"))

	// 3. 调用 start_process，带环境变量
	toolReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "start_process",
			"arguments": map[string]interface{}{
				"name":             "test-env-server",
				"command":          "python",
				"args":             []string{"test_env_server.py"},
				"env": map[string]string{
					"TEST_APP":     "my-app",
					"TEST_VERSION": "1.0.0",
					"TEST_MODE":    "test",
					"TEST_DEBUG":   "true",
				},
				"health_check_url": "http://localhost:8889/",
				"timeout_seconds":  10,
			},
		},
	}

	fmt.Println("=== 测试环境变量功能 ===")
	fmt.Println("\n发送工具调用（带环境变量）...")
	toolReqData, _ := json.Marshal(toolReq)
	fmt.Printf("环境变量: TEST_APP=my-app, TEST_VERSION=1.0.0, TEST_MODE=test, TEST_DEBUG=true\n\n")
	writer.Write(toolReqData)
	writer.Write([]byte("\n"))

	// 读取响应
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		fmt.Printf("响应: %s\n", line)

		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			break
		}
	}

	// 等待一下
	fmt.Println("\n等待服务器启动...")
	// reader.ReadString('\n') // 读取任何额外的输出

	// 4. 测试 GET 请求获取环境变量
	getReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "request_with_logs",
			"arguments": map[string]interface{}{
				"process_name": "test-env-server",
				"url":          "http://localhost:8889/",
				"method":       "GET",
			},
		},
	}

	fmt.Println("\n发送 GET 请求获取环境变量...")
	getReqData, _ := json.Marshal(getReq)
	writer.Write(getReqData)
	writer.Write([]byte("\n"))

	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}

		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			// 解析结果
			if result, ok := resp["result"].(map[string]interface{}); ok {
				if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
					if textContent, ok := content[0].(map[string]interface{}); ok {
						if text, ok := textContent["text"].(string); ok {
							fmt.Printf("\n响应:\n%s\n", text)
						}
					}
				}
			}
			break
		}
	}

	// 5. 终止进程
	killReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "kill_process",
			"arguments": map[string]interface{}{
				"name": "test-env-server",
			},
		},
	}

	fmt.Println("\n终止服务器...")
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
			break
		}
	}

	cmd.Process.Kill()
	cmd.Wait()
}
