package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// MCP JSON-RPC 请求结构
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
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

type MCPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reqID  int
}

func NewMCPClient() (*MCPClient, error) {
	cmd := exec.Command("./gomcp.exe")
	cmd.Dir = "D:\\project\\gomcp-new"

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("创建stdin管道失败: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("创建stdout管道失败: %v", err)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动MCP服务器失败: %v", err)
	}

	fmt.Println("MCP服务器已启动，PID:", cmd.Process.Pid)

	return &MCPClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		reqID:  0,
	}, nil
}

func (c *MCPClient) sendRequest(method string, params interface{}) (*JSONRPCResponse, error) {
	c.reqID++
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.reqID,
		Method:  method,
		Params:  params,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %v", err)
	}

	fmt.Printf("\n>>> 发送请求 [%s]: %s\n", method, string(reqBytes))

	// 发送请求
	if _, err := c.stdin.Write(append(reqBytes, '\n')); err != nil {
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}

	// 读取响应（带超时）
	respChan := make(chan []byte, 1)
	errChan := make(chan error, 1)

	go func() {
		buf := make([]byte, 1024*1024) // 1MB buffer
		n, err := c.stdout.Read(buf)
		if err != nil {
			errChan <- err
			return
		}
		respChan <- buf[:n]
	}()

	select {
	case respBytes := <-respChan:
		fmt.Printf("<<< 收到响应: %s\n", string(respBytes))
		var resp JSONRPCResponse
		if err := json.Unmarshal(respBytes, &resp); err != nil {
			// 尝试找到JSON开始位置
			start := bytes.Index(respBytes, []byte("{"))
			if start >= 0 {
				if err := json.Unmarshal(respBytes[start:], &resp); err != nil {
					return nil, fmt.Errorf("解析响应失败: %v, 原始响应: %s", err, string(respBytes))
				}
			} else {
				return nil, fmt.Errorf("解析响应失败: %v", err)
			}
		}
		return &resp, nil
	case err := <-errChan:
		return nil, fmt.Errorf("读取响应失败: %v", err)
	case <-time.After(120 * time.Second):
		return nil, fmt.Errorf("等待响应超时")
	}
}

func (c *MCPClient) Initialize() error {
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "test-client",
			"version": "1.0.0",
		},
	}

	resp, err := c.sendRequest("initialize", params)
	if err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("初始化失败: %s", resp.Error.Message)
	}

	// 发送 initialized 通知
	c.reqID++
	notif := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	notifBytes, _ := json.Marshal(notif)
	c.stdin.Write(append(notifBytes, '\n'))

	fmt.Println("MCP初始化完成")
	return nil
}

func (c *MCPClient) StartProcess(name, command string, args []string, env map[string]string, healthCheckURL string) error {
	params := map[string]interface{}{
		"name": "tools/call",
		"arguments": map[string]interface{}{
			"name": "start_process",
			"arguments": map[string]interface{}{
				"name":             name,
				"command":          command,
				"args":             args,
				"env":              env,
				"health_check_url": healthCheckURL,
				"timeout_seconds":  60,
			},
		},
	}

	resp, err := c.sendRequest("tools/call", params)
	if err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("启动进程失败: %s", resp.Error.Message)
	}

	fmt.Println("进程启动成功")
	return nil
}

func (c *MCPClient) KillProcess(name string) error {
	params := map[string]interface{}{
		"name": "tools/call",
		"arguments": map[string]interface{}{
			"name": "kill_process",
			"arguments": map[string]interface{}{
				"name": name,
			},
		},
	}

	resp, err := c.sendRequest("tools/call", params)
	if err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("终止进程失败: %s", resp.Error.Message)
	}

	fmt.Println("进程已终止")
	return nil
}

func (c *MCPClient) Close() {
	c.stdin.Close()
	c.cmd.Process.Kill()
	c.cmd.Wait()
}

func directHTTPRequest(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	body := `{"entity_type":"mobile","topchart_name":"appstore top grossing","region_type":"market","regions":["ng","za","hk"],"genre_source":"iegg","iegg_genre_req":[],"granularity":"daily","start_date":"2025-12-29","end_date":"2025-12-29","order_source":"","order_metric":"rank","order":"desc","ratio_type":0,"page":1,"page_size":10,"search_ids":[],"search_by_name":""}`

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJkYXRhIjp7ImxvZ2ludHlwZSI6IkJhc2ljIiwib3BlbmlkIjoiZ2NfemhlbmdoYW5saWFuZyIsInVzZXJpZCI6IjkwMyIsInVzZXJuYW1lIjoiZ2NfemhlbmdoYW5saWFuZyJ9LCJleHAiOjE3NjY5NzkwNjgsImlwIjoiMTUwLjEwOS45NS4xOTkiLCJpc3MiOiJnaW4tcHJveHkiLCJ0eXBlIjoibG9naW4ifQ.8HrVW9Lb7xa8vihANuTAFd8LX8HzEWiwgQnsUqMij-I")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	fmt.Printf("HTTP响应: 状态码=%d, 长度=%d\n", resp.StatusCode, len(respBody))
	return nil
}

func main() {
	fmt.Println("=== 开始测试 MCP 卡死问题 ===")
	fmt.Println("时间:", time.Now().Format(time.RFC3339))

	// 创建MCP客户端
	client, err := NewMCPClient()
	if err != nil {
		fmt.Println("创建MCP客户端失败:", err)
		os.Exit(1)
	}
	defer client.Close()

	// 初始化
	if err := client.Initialize(); err != nil {
		fmt.Println("初始化失败:", err)
		os.Exit(1)
	}

	// 进程配置
	processName := "intelligence-pc-backend"
	command := "go"
	args := []string{"run", "."}
	env := map[string]string{
		"QB_DEV":           "1",
		"QB_IGNORE_DEVLOG": "1",
		"QB_PROFILE":       "bin2",
	}
	healthCheckURL := "http://localhost:27028/healthz"

	// 第一轮：启动 -> 请求 -> kill
	fmt.Println("\n========== 第一轮测试 ==========")

	fmt.Println("\n--- 步骤1: 启动进程 ---")
	startTime := time.Now()
	if err := client.StartProcess(processName, command, args, env, healthCheckURL); err != nil {
		fmt.Println("第一轮启动失败:", err)
		os.Exit(1)
	}
	fmt.Printf("启动耗时: %v\n", time.Since(startTime))

	fmt.Println("\n--- 步骤2: 发送HTTP请求 ---")
	if err := directHTTPRequest("http://localhost:27028/api/v1/intelligence_pc/getNewStoreRankDetailedList"); err != nil {
		fmt.Println("HTTP请求失败:", err)
		// 继续测试，不退出
	}

	fmt.Println("\n--- 步骤3: 终止进程 ---")
	if err := client.KillProcess(processName); err != nil {
		fmt.Println("终止进程失败:", err)
		os.Exit(1)
	}

	// 等待一下
	fmt.Println("\n等待2秒...")
	time.Sleep(2 * time.Second)

	// 第二轮：再次启动
	fmt.Println("\n========== 第二轮测试 ==========")

	fmt.Println("\n--- 步骤1: 再次启动进程 ---")
	startTime = time.Now()
	fmt.Println("开始时间:", startTime.Format(time.RFC3339))
	
	if err := client.StartProcess(processName, command, args, env, healthCheckURL); err != nil {
		fmt.Println("第二轮启动失败:", err)
		fmt.Println("失败时间:", time.Now().Format(time.RFC3339))
		fmt.Printf("耗时: %v\n", time.Since(startTime))
		os.Exit(1)
	}
	fmt.Printf("启动耗时: %v\n", time.Since(startTime))

	fmt.Println("\n--- 步骤2: 发送HTTP请求 ---")
	if err := directHTTPRequest("http://localhost:27028/api/v1/intelligence_pc/getNewStoreRankDetailedList"); err != nil {
		fmt.Println("HTTP请求失败:", err)
	}

	fmt.Println("\n--- 步骤3: 终止进程 ---")
	if err := client.KillProcess(processName); err != nil {
		fmt.Println("终止进程失败:", err)
	}

	fmt.Println("\n=== 测试完成 ===")
}




