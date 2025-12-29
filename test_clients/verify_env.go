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
	cmd := exec.Command("d:/code/gomcp/gomcp.exe")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()

	defer stdin.Close()
	defer stdout.Close()

	writer := stdin
	reader := bufio.NewReader(stdout)

	// 初始化
	initReq := map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"roots": map[string]bool{}},
			"clientInfo":      map[string]string{"name": "env-test", "version": "1.0"},
		},
	}
	reqData, _ := json.Marshal(initReq)
	writer.Write(reqData)
	writer.Write([]byte("\n"))
	reader.ReadString('\n')

	notif := map[string]interface{}{"jsonrpc": "2.0", "method": "notifications/initialized"}
	notifData, _ := json.Marshal(notif)
	writer.Write(notifData)
	writer.Write([]byte("\n"))

	fmt.Println("=== 测试环境变量功能 ===")
	fmt.Println("\n启动进程，设置环境变量:")
	fmt.Println("  TEST_APP=my-app")
	fmt.Println("  TEST_VERSION=2.0.0")
	fmt.Println("  TEST_ENV=production")
	fmt.Println()

	// 启动进程
	toolReq := map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]interface{}{
			"name":    "start_process",
			"arguments": map[string]interface{}{
				"name":             "env-test",
				"command":          "python",
				"args":             []string{"test_env_printer.py"},
				"health_check_url": "http://localhost:8890/",
				"timeout_seconds":  10,
				"env": map[string]string{
					"TEST_APP":     "my-app",
					"TEST_VERSION": "2.0.0",
					"TEST_ENV":     "production",
				},
			},
		},
	}

	toolReqData, _ := json.Marshal(toolReq)
	writer.Write(toolReqData)
	writer.Write([]byte("\n"))

	// 读取响应
	for i := 0; i < 3; i++ {
		line, _ := reader.ReadString('\n')
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			fmt.Println("进程已启动，读取启动日志...")
			break
		}
	}

	time.Sleep(2 * time.Second)

	// 获取环境变量
	getReq := map[string]interface{}{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]interface{}{
			"name": "request_with_logs",
			"arguments": map[string]interface{}{
				"process_name": "env-test",
				"url":          "http://localhost:8890/",
				"method":       "GET",
			},
		},
	}

	fmt.Println("\n发送 GET 请求验证环境变量...")
	getReqData, _ := json.Marshal(getReq)
	writer.Write(getReqData)
	writer.Write([]byte("\n"))

	for i := 0; i < 3; i++ {
		line, _ := reader.ReadString('\n')
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if result, ok := resp["result"].(map[string]interface{}); ok {
			if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
				if textContent, ok := content[0].(map[string]interface{}); ok {
					if text, ok := textContent["text"].(string); ok {
						// 提取响应部分
						if strings.Contains(text, "test_env_vars") {
							fmt.Println("\n✓ 环境变量验证成功!")
							fmt.Println("\n响应中包含的环境变量:")
							// 简单解析 JSON
							startIdx := strings.Index(text, "{")
							if startIdx >= 0 {
								jsonPart := text[startIdx:]
								var result map[string]interface{}
								json.Unmarshal([]byte(jsonPart), &result)
								if envVars, ok := result["test_env_vars"].(map[string]interface{}); ok {
									for k, v := range envVars {
										fmt.Printf("  %s = %v\n", k, v)
									}
								}
							}
						} else {
							fmt.Println("\n响应:")
							fmt.Println(text)
						}
					}
				}
			}
			break
		}
	}

	// 清理
	killReq := map[string]interface{}{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]interface{}{
			"name": "kill_process",
			"arguments": map[string]interface{}{
				"name": "env-test",
			},
		},
	}

	fmt.Println("\n终止进程...")
	killReqData, _ := json.Marshal(killReq)
	writer.Write(killReqData)
	writer.Write([]byte("\n"))

	for i := 0; i < 3; i++ {
		line, _ := reader.ReadString('\n')
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			break
		}
	}

	cmd.Process.Kill()
	cmd.Wait()

	fmt.Println("\n=== 测试完成 ===")
}
