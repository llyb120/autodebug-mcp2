package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// MCP 请求和响应结构
type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int        `json:"id,omitempty"`
	Method  string      `json:"method,omitempty"`
	Params  interface{} `json:"params,omitempty"`
}

type MCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

type MCPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reqID  int
}

func NewMCPClient() (*MCPClient, error) {
	cmd := exec.Command("../gomcp.exe")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 stdin 管道失败: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("创建 stdout 管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("启动 MCP 服务器失败: %w", err)
	}

	client := &MCPClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		reqID:  1,
	}

	// 初始化：发送 initialize 请求
	initParams := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"roots": map[string]bool{},
		},
		"clientInfo": map[string]string{
			"name":    "test-client",
			"version": "1.0.0",
		},
	}

	_, err = client.Call("initialize", initParams)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("初始化失败: %w", err)
	}

	// 发送 initialized 通知
	err = client.Notify("notifications/initialized", nil)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("发送 initialized 通知失败: %w", err)
	}

	return client, nil
}

func (c *MCPClient) Call(method string, params interface{}) (json.RawMessage, error) {
	reqID := c.reqID
	c.reqID++

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      &reqID,
		Method:  method,
		Params:  params,
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 发送请求
	_, err = c.stdin.Write(reqData)
	_, err = c.stdin.Write([]byte("\n"))
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}

	// 读取响应（可能跳过通知）
	decoder := json.NewDecoder(c.stdout)
	for {
		var rawResp map[string]json.RawMessage
		err = decoder.Decode(&rawResp)
		if err != nil {
			return nil, fmt.Errorf("解析响应失败: %w", err)
		}

		// 检查是否是通知（没有 id 字段）
		if _, hasID := rawResp["id"]; !hasID {
			// 这是一个通知，跳过并继续读取
			continue
		}

		// 检查是否有错误
		if errRaw, hasError := rawResp["error"]; hasError {
			var mcpErr MCPError
			json.Unmarshal(errRaw, &mcpErr)
			return nil, fmt.Errorf("MCP 错误: %s", mcpErr.Message)
		}

		// 返回 result
		if result, ok := rawResp["result"]; ok {
			return result, nil
		}

		return nil, fmt.Errorf("响应中没有 result 字段")
	}
}

func (c *MCPClient) Notify(method string, params interface{}) error {
	req := MCPRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化通知失败: %w", err)
	}

	_, err = c.stdin.Write(reqData)
	_, err = c.stdin.Write([]byte("\n"))
	return err
}

func (c *MCPClient) CallTool(name string, args map[string]interface{}) (*ToolResult, error) {
	params := ToolCallParams{
		Name:      name,
		Arguments: args,
	}

	result, err := c.Call("tools/call", params)
	if err != nil {
		return nil, err
	}

	var toolResult ToolResult
	err = json.Unmarshal(result, &toolResult)
	if err != nil {
		return nil, fmt.Errorf("解析工具结果失败: %w", err)
	}

	return &toolResult, nil
}

func (c *MCPClient) Close() error {
	c.stdin.Close()
	c.stdout.Close()
	return c.cmd.Wait()
}

func main() {
	fmt.Println("=== MCP 测试客户端 ===\n")

	// 创建 MCP 客户端
	client, err := NewMCPClient()
	if err != nil {
		fmt.Printf("创建客户端失败: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	fmt.Println("✓ MCP 客户端已连接\n")

	// 测试场景：启动一个简单的 HTTP 服务器并测试
	fmt.Println("--- 测试场景：启动 HTTP 服务器（带环境变量） ---\n")

	// 1. 启动进程
	fmt.Println("1. 启动测试 HTTP 服务器（设置环境变量）...")
	startResult, err := client.CallTool("start_process", map[string]interface{}{
		"name":             "test-server",
		"command":          "py",
		"args":             []string{"../test_server.py"},
		"env": map[string]string{
			"TEST_APP":     "gomcp-test",
			"TEST_VERSION": "v1.0.0",
			"TEST_MODE":    "demo",
		},
		"health_check_url": "http://localhost:8888/",
		"timeout_seconds":  10,
	})

	if err != nil {
		fmt.Printf("   ✗ 启动失败: %v\n", err)
		os.Exit(1)
	}

	if len(startResult.Content) > 0 {
		fmt.Printf("   ✓ 启动成功:\n%s\n\n", startResult.Content[0].Text)
	}

	if startResult.IsError {
		fmt.Println("   注意：启动标记为错误，进程可能已终止")
		os.Exit(1)
	}

	// 等待一下确保服务器完全启动
	time.Sleep(1 * time.Second)

	// 2. 发起 GET 请求
	fmt.Println("2. 发起 GET 请求...")
	getResult, err := client.CallTool("request_with_logs", map[string]interface{}{
		"process_name": "test-server",
		"url":          "http://localhost:8888/",
		"method":       "GET",
	})

	if err != nil {
		fmt.Printf("   ✗ GET 请求失败: %v\n", err)
	} else if len(getResult.Content) > 0 {
		fmt.Printf("   ✓ GET 请求成功:\n%s\n\n", getResult.Content[0].Text)
	}

	// 3. 发起 POST 请求（测试一个 404 路径）
	fmt.Println("3. 发起 POST 请求...")
	postResult, err := client.CallTool("request_with_logs", map[string]interface{}{
		"process_name": "test-server",
		"url":          "http://localhost:8888/test",
		"method":       "POST",
		"body":         `{"test": "data"}`,
		"headers": map[string]string{
			"X-Custom-Header": "test-value",
		},
	})

	if err != nil {
		fmt.Printf("   ✗ POST 请求失败: %v\n", err)
	} else if len(postResult.Content) > 0 {
		fmt.Printf("   ✓ POST 请求完成:\n%s\n\n", postResult.Content[0].Text)
	}

	// 4. 终止进程
	fmt.Println("4. 终止服务器...")
	killResult, err := client.CallTool("kill_process", map[string]interface{}{
		"name": "test-server",
	})

	if err != nil {
		fmt.Printf("   ✗ 终止失败: %v\n", err)
	} else if len(killResult.Content) > 0 {
		fmt.Printf("   ✓ %s\n\n", killResult.Content[0].Text)
	}

	fmt.Println("--- 测试完成 ---")

	// 额外演示：测试 echo 工具
	fmt.Println("\n--- 额外演示：echo 工具 ---")
	echoResult, err := client.CallTool("echo", map[string]interface{}{
		"text": "Hello from MCP Client!",
	})

	if err != nil {
		fmt.Printf("   ✗ echo 失败: %v\n", err)
	} else if len(echoResult.Content) > 0 {
		fmt.Printf("   ✓ echo 响应: %s\n", echoResult.Content[0].Text)
	}
}
