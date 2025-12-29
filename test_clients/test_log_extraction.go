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
	cmd := exec.Command("../gomcp.exe")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()
	defer stdin.Close()
	defer stdout.Close()

	writer := stdin
	reader := bufio.NewReader(stdout)

	// 初始化
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
	reader.ReadString('\n')

	notif := map[string]interface{}{"jsonrpc": "2.0", "method": "notifications/initialized"}
	notifData, _ := json.Marshal(notif)
	writer.Write(notifData)
	writer.Write([]byte("\n"))

	fmt.Println("=== 测试：请求期间日志提取 ===\n")

	// 启动进程
	fmt.Println("1. 启动进程...")
	startReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":             "test-server",
			"command":          "go",
			"args":             []string{"run", "main.go"},
			"work_dir":         "d:/code/phgo",
			"health_check_url": "http://localhost:8080/server",
			"timeout_seconds":  30,
		},
	}
	startReqData, _ := json.Marshal(startReq)
	writer.Write(startReqData)
	writer.Write([]byte("\n"))

	// 读取启动响应
	for i := 0; i < 5; i++ {
		line, _ := reader.ReadString('\n')
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			fmt.Println("   ✓ 进程启动成功")
			break
		}
	}

	// 等待启动日志全部收集完毕
	fmt.Println("\n2. 等待3秒让所有启动日志被收集...")
	time.Sleep(3 * time.Second)

	// 第一次请求
	fmt.Println("\n3. 发起第一次请求到 /server")
	req1 := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "request_with_logs",
			"arguments": map[string]interface{}{
				"process_name": "test-server",
				"url":          "http://localhost:8080/server",
				"method":       "GET",
			},
		},
	}
	req1Data, _ := json.Marshal(req1)
	writer.Write(req1Data)
	writer.Write([]byte("\n"))

	var log1 string
	for i := 0; i < 5; i++ {
		line, _ := reader.ReadString('\n')
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if result, ok := resp["result"].(map[string]interface{}); ok {
			if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
				if textContent, ok := content[0].(map[string]interface{}); ok {
					if text, ok := textContent["text"].(string); ok {
						// 提取日志部分
						if idx := strings.Index(text, "请求期间进程日志:"); idx >= 0 {
							log1 = text[idx+21:]
							fmt.Println("   ✓ 第一次请求完成")
						}
					}
				}
			}
			break
		}
	}

	// 等待2秒
	time.Sleep(2 * time.Second)

	// 第二次请求
	fmt.Println("\n4. 发起第二次请求到 /server")
	req2 := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "request_with_logs",
			"arguments": map[string]interface{}{
				"process_name": "test-server",
				"url":          "http://localhost:8080/server",
				"method":       "GET",
			},
		},
	}
	req2Data, _ := json.Marshal(req2)
	writer.Write(req2Data)
	writer.Write([]byte("\n"))

	var log2 string
	for i := 0; i < 5; i++ {
		line, _ := reader.ReadString('\n')
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if result, ok := resp["result"].(map[string]interface{}); ok {
			if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
				if textContent, ok := content[0].(map[string]interface{}); ok {
					if text, ok := textContent["text"].(string); ok {
						if idx := strings.Index(text, "请求期间进程日志:"); idx >= 0 {
							log2 = text[idx+21:]
							fmt.Println("   ✓ 第二次请求完成")
						}
					}
				}
			}
			break
		}
	}

	// 分析结果
	fmt.Println("\n=== 分析结果 ===\n")

	// 检查是否包含启动日志
	hasStartupLog1 := strings.Contains(log1, "[GIN-debug]")
	hasStartupLog2 := strings.Contains(log2, "[GIN-debug]")

	fmt.Printf("第一次请求日志长度: %d 字节\n", len(log1))
	fmt.Printf("  - 包含启动日志 [GIN-debug]: %v\n\n", hasStartupLog1)

	fmt.Printf("第二次请求日志长度: %d 字节\n", len(log2))
	fmt.Printf("  - 包含启动日志 [GIN-debug]: %v\n\n", hasStartupLog2)

	if !hasStartupLog1 && !hasStartupLog2 {
		fmt.Println("✅ 测试通过！两次请求都不包含启动日志")
	} else {
		fmt.Println("❌ 测试失败！请求日志中仍然包含启动日志")
		fmt.Println("\n第一次请求的日志内容:")
		fmt.Println(log1)
	}

	fmt.Println("\n=== 测试完成 ===")
	time.Sleep(1 * time.Second)
	cmd.Process.Kill()
	cmd.Wait()
}
