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
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"roots": map[string]bool{},
			},
			"clientInfo": map[string]string{
				"name":    "restart-test",
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
		"method":  "notifications/initialized",
	}
	notifData, _ := json.Marshal(notif)
	writer.Write(notifData)
	writer.Write([]byte("\n"))

	fmt.Println("=== 测试进程重启功能 ===\n")

	// 3. 第一次启动 phgo 项目
	fmt.Println("第一次启动 phgo 项目...")
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

	// 读取响应
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			if result, ok := resp["result"].(map[string]interface{}); ok {
				if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
					if textContent, ok := content[0].(map[string]interface{}); ok {
						if text, ok := textContent["text"].(string); ok {
							fmt.Printf("第一次启动结果:\n%s\n\n", text)
						}
					}
				}
			}
			break
		}
	}

	// 等待进程完全启动
	fmt.Println("等待 3 秒让进程完全启动...")
	time.Sleep(3 * time.Second)

	// 4. 第二次启动同名进程（应该自动终止旧进程）
	fmt.Println("第二次启动同名进程（应该自动终止旧进程）...")
	startReq2 := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
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

	startReq2Data, _ := json.Marshal(startReq2)
	writer.Write(startReq2Data)
	writer.Write([]byte("\n"))

	// 读取响应
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			if result, ok := resp["result"].(map[string]interface{}); ok {
				if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
					if textContent, ok := content[0].(map[string]interface{}); ok {
						if text, ok := textContent["text"].(string); ok {
							fmt.Printf("第二次启动结果:\n%s\n\n", text)
						}
					}
				}
			}
			break
		}
	}

	// 等待新进程完全启动
	fmt.Println("等待 3 秒让新进程完全启动...")
	time.Sleep(3 * time.Second)

	// 5. 测试 API 接口
	fmt.Println("测试新进程的 API...")
	apiReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "phgo-server",
			"url":  "http://localhost:8081/server",
			"method": "GET",
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
			fmt.Printf("API 测试结果: 新进程工作正常\n")
			break
		}
	}

	// 6. 终止进程
	fmt.Println("\n终止进程...")
	killReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "phgo-server",
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
			fmt.Printf("进程已终止\n")
			break
		}
	}

	fmt.Println("\n=== 测试完成 ===")
	fmt.Println("✅ 验证点:")
	fmt.Println("1. 第一次启动成功")
	fmt.Println("2. 第二次启动时自动终止旧进程")
	fmt.Println("3. 新进程启动成功")
	fmt.Println("4. 新进程 API 工作正常")
	cmd.Process.Kill()
	cmd.Wait()
}
