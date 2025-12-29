package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func log(format string, args ...interface{}) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func main() {
	log("=== 测试 MCP 卡死问题 ===")

	// 启动 MCP 服务器
	cmd := exec.Command("D:\\project\\gomcp-new\\gomcp.exe")
	cmd.Dir = "D:\\project\\partner-ogdb-backend-intelligence-pc-backend-master"

	stdin, err := cmd.StdinPipe()
	if err != nil {
		log("创建stdin管道失败: %v", err)
		os.Exit(1)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log("创建stdout管道失败: %v", err)
		os.Exit(1)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		log("创建stderr管道失败: %v", err)
		os.Exit(1)
	}

	// 收集 stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log("[STDERR] %s", scanner.Text())
		}
	}()

	if err := cmd.Start(); err != nil {
		log("启动MCP服务器失败: %v", err)
		os.Exit(1)
	}
	log("MCP服务器已启动，PID: %d", cmd.Process.Pid)

	reader := bufio.NewReader(stdout)
	reqID := 0

	sendRequest := func(method string, params interface{}) (json.RawMessage, error) {
		reqID++
		currentReqID := reqID
		req := JSONRPCRequest{
			JSONRPC: "2.0",
			ID:      currentReqID,
			Method:  method,
			Params:  params,
		}

		reqBytes, _ := json.Marshal(req)
		log(">>> 发送请求 [%s] ID=%d", method, currentReqID)

		if _, err := stdin.Write(append(reqBytes, '\n')); err != nil {
			return nil, fmt.Errorf("发送请求失败: %v", err)
		}

		// 读取响应，跳过通知，匹配请求ID
		timeout := time.After(120 * time.Second)
		for {
			respChan := make(chan string, 1)
			errChan := make(chan error, 1)

			go func() {
				line, err := reader.ReadString('\n')
				if err != nil {
					errChan <- err
					return
				}
				respChan <- line
			}()

			select {
			case line := <-respChan:
				var resp JSONRPCResponse
				if err := json.Unmarshal([]byte(line), &resp); err != nil {
					log("<<< 解析失败: %v, 内容: %s", err, line)
					continue // 跳过无法解析的消息
				}
				
				// 检查是否是通知（没有ID）
				if resp.ID == 0 {
					log("<<< 收到通知，跳过: %s", line)
					continue
				}
				
				// 检查ID是否匹配
				if resp.ID != currentReqID {
					log("<<< 收到响应 ID=%d (期望 ID=%d)，跳过: %s", resp.ID, currentReqID, line)
					continue
				}
				
				log("<<< 收到响应 ID=%d: %s", resp.ID, line)
				if resp.Error != nil {
					return nil, fmt.Errorf("RPC错误: %s", resp.Error.Message)
				}
				return resp.Result, nil
			case err := <-errChan:
				return nil, fmt.Errorf("读取响应失败: %v", err)
			case <-timeout:
				return nil, fmt.Errorf("等待响应超时 (120s)")
			}
		}
	}

	// 初始化
	log("\n--- 初始化 MCP ---")
	_, err = sendRequest("initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "test-client",
			"version": "1.0.0",
		},
	})
	if err != nil {
		log("初始化失败: %v", err)
		cmd.Process.Kill()
		os.Exit(1)
	}
	log("初始化成功")

	// 发送 initialized 通知
	notif := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	notifBytes, _ := json.Marshal(notif)
	stdin.Write(append(notifBytes, '\n'))

	// 进程配置
	processName := "intelligence-pc-backend"
	startArgs := map[string]interface{}{
		"name":    processName,
		"command": "go",
		"args":    []string{"run", "."},
		"env": map[string]string{
			"QB_DEV":           "1",
			"QB_IGNORE_DEVLOG": "1",
			"QB_PROFILE":       "bin2",
		},
		"health_check_url": "http://localhost:27028/healthz",
		"timeout_seconds":  60,
	}

	killArgs := map[string]interface{}{
		"name": processName,
	}

	// request_with_logs 参数
	requestArgs := map[string]interface{}{
		"process_name": processName,
		"url":          "/api/v1/intelligence_pc/getNewStoreRankDetailedList",
		"method":       "POST",
		"headers": map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJkYXRhIjp7ImxvZ2ludHlwZSI6IkJhc2ljIiwib3BlbmlkIjoiZ2NfemhlbmdoYW5saWFuZyIsInVzZXJpZCI6IjkwMyIsInVzZXJuYW1lIjoiZ2NfemhlbmdoYW5saWFuZyJ9LCJleHAiOjE3NjY5NzkwNjgsImlwIjoiMTUwLjEwOS45NS4xOTkiLCJpc3MiOiJnaW4tcHJveHkiLCJ0eXBlIjoibG9naW4ifQ.8HrVW9Lb7xa8vihANuTAFd8LX8HzEWiwgQnsUqMij-I",
		},
		"body": `{"entity_type":"mobile","topchart_name":"appstore top grossing","region_type":"market","regions":["hk"],"genre_source":"iegg","iegg_genre_req":[],"granularity":"daily","start_date":"2025-12-29","end_date":"2025-12-29","order_source":"","order_metric":"rank","order":"desc","ratio_type":0,"page":1,"page_size":10,"search_ids":[],"search_by_name":""}`,
	}

	// 第一轮
	log("\n========== 第一轮测试 ==========")

	log("\n--- 启动进程 ---")
	startTime := time.Now()
	_, err = sendRequest("tools/call", map[string]interface{}{
		"name":      "start_process",
		"arguments": startArgs,
	})
	if err != nil {
		log("启动失败: %v", err)
	} else {
		log("启动成功，耗时: %v", time.Since(startTime))
	}

	log("\n--- 发送HTTP请求 (request_with_logs) ---")
	startTime = time.Now()
	_, err = sendRequest("tools/call", map[string]interface{}{
		"name":      "request_with_logs",
		"arguments": requestArgs,
	})
	if err != nil {
		log("HTTP请求失败: %v", err)
	} else {
		log("HTTP请求成功，耗时: %v", time.Since(startTime))
	}

	log("\n--- 终止进程 ---")
	_, err = sendRequest("tools/call", map[string]interface{}{
		"name":      "kill_process",
		"arguments": killArgs,
	})
	if err != nil {
		log("终止失败: %v", err)
	} else {
		log("终止成功")
	}

	log("\n等待2秒...")
	time.Sleep(2 * time.Second)

	// 第二轮
	log("\n========== 第二轮测试 ==========")

	log("\n--- 再次启动进程 ---")
	startTime = time.Now()
	_, err = sendRequest("tools/call", map[string]interface{}{
		"name":      "start_process",
		"arguments": startArgs,
	})
	if err != nil {
		log("第二轮启动失败: %v", err)
		log("失败耗时: %v", time.Since(startTime))
	} else {
		log("启动成功，耗时: %v", time.Since(startTime))
	}

	log("\n--- 发送HTTP请求 (request_with_logs) ---")
	startTime = time.Now()
	_, err = sendRequest("tools/call", map[string]interface{}{
		"name":      "request_with_logs",
		"arguments": requestArgs,
	})
	if err != nil {
		log("HTTP请求失败: %v", err)
	} else {
		log("HTTP请求成功，耗时: %v", time.Since(startTime))
	}

	log("\n--- 终止进程 ---")
	_, err = sendRequest("tools/call", map[string]interface{}{
		"name":      "kill_process",
		"arguments": killArgs,
	})
	if err != nil {
		log("终止失败: %v", err)
	} else {
		log("终止成功")
	}

	log("\n=== 测试完成 ===")

	// 关闭
	stdin.Close()
	cmd.Process.Kill()
	cmd.Wait()
}

