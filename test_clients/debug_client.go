package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"
)

func main() {
	cmd := exec.Command("d:/code/gomcp/gomcp.exe")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()

	defer stdin.Close()
	defer stdout.Close()

	// 创建读写器
	writer := stdin
	reader := bufio.NewReader(stdout)

	// 发送 initialize 请求
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
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}

	reqData, _ := json.Marshal(initReq)
	writer.Write(reqData)
	writer.Write([]byte("\n"))

	// 读取响应
	line1, _ := reader.ReadString('\n')
	fmt.Printf("响应1: %s\n", line1)

	// 发送 initialized 通知
	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	notifData, _ := json.Marshal(notif)
	writer.Write(notifData)
	writer.Write([]byte("\n"))

	time.Sleep(100 * time.Millisecond)

	// 调用 start_process 工具（测试环境变量）
	toolReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "start_process",
			"arguments": map[string]interface{}{
				"name":             "test-server",
				"command":          "python",
				"args":             []string{"test_server.py"},
				"health_check_url": "http://localhost:8888/",
				"timeout_seconds":  10,
			},
		},
	}

	fmt.Printf("\n发送工具调用:\n\n")

	toolReqData, _ := json.Marshal(toolReq)
	writer.Write(toolReqData)
	writer.Write([]byte("\n"))

	// 读取所有响应（可能有多个通知）
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Printf("读取错误: %v\n", err)
			}
			break
		}
		fmt.Printf("响应%d: %s\n", i+2, line)

		// 解析响应看看是否包含 result
		var resp map[string]interface{}
		json.Unmarshal([]byte(line), &resp)
		if _, ok := resp["result"]; ok {
			fmt.Println("\n>>> 找到 result 响应！")

			// 启动成功后，测试 GET 请求
			time.Sleep(500 * time.Millisecond)

			getReq := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      3,
				"method":  "tools/call",
				"params": map[string]interface{}{
					"name": "request_with_logs",
					"arguments": map[string]interface{}{
						"process_name": "test-server",
						"url":          "http://localhost:8888/",
						"method":       "GET",
					},
				},
			}

			fmt.Println("\n发送 GET 请求...")
			getReqData, _ := json.Marshal(getReq)
			writer.Write(getReqData)
			writer.Write([]byte("\n"))

			// 读取 GET 请求响应
			for j := 0; j < 3; j++ {
				getLine, getErr := reader.ReadString('\n')
				if getErr != nil {
					break
				}
				var getResp map[string]interface{}
				json.Unmarshal([]byte(getLine), &getResp)
				if _, ok := getResp["result"]; ok {
					fmt.Printf("\nGET 响应: %s\n", getLine)
					break
				}
			}

			break
		}
	}

	time.Sleep(500 * time.Millisecond)

	// 终止进程
	killReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "kill_process",
			"arguments": map[string]interface{}{
				"name": "test-server",
			},
		},
	}

	fmt.Println("\n终止进程...")
	killReqData, _ := json.Marshal(killReq)
	writer.Write(killReqData)
	writer.Write([]byte("\n"))

	reader.ReadString('\n') // 读取 kill 响应

	cmd.Process.Kill()
	cmd.Wait()
}
