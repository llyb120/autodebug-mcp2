package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 全局串行队列，确保所有工具调用都是串行的
// 使用带缓冲的channel作为信号量，容量为1确保同时只有一个工具在执行
// 这是必需的，因为涉及到进程启动、日志读取等操作，并发执行可能导致竞态条件
var toolSemaphore = make(chan struct{}, 1)

func init() {
	// 初始化信号量：放入一个令牌，表示可以获取
	toolSemaphore <- struct{}{}
}

// acquireToolSemaphore 获取工具执行权限（阻塞直到获取到）
func acquireToolSemaphore() {
	GetLogger().Info("[Semaphore] 尝试获取信号量...")
	<-toolSemaphore // 取出令牌，获取执行权限
	GetLogger().Info("[Semaphore] 已获取信号量")
}

// releaseToolSemaphore 释放工具执行权限
func releaseToolSemaphore() {
	GetLogger().Info("[Semaphore] 释放信号量...")
	toolSemaphore <- struct{}{} // 放回令牌，释放执行权限
	GetLogger().Info("[Semaphore] 已释放信号量")
}

// RegisterTools 注册所有 MCP 工具
func RegisterTools(server *mcp.Server) {
	logger := GetLogger()

	// 注册 start_process 工具：启动进程并收集日志
	type startProcessArgs struct {
		Name              string            `json:"name" jsonschema:"进程名称，用于后续操作该进程"`
		Command           string            `json:"command" jsonschema:"要执行的命令"`
		Args              []string          `json:"args,omitempty" jsonschema:"命令参数列表"`
		WorkDir           string            `json:"work_dir,omitempty" jsonschema:"工作目录，默认为命令文件所在目录"`
		Env               map[string]string `json:"env,omitempty" jsonschema:"环境变量，键值对形式"`
		HealthCheckURL    string            `json:"health_check_url" jsonschema:"健康检查接口URL，接口返回2xx状态码视为启动成功"`
		TimeoutSeconds    int               `json:"timeout_seconds,omitempty" jsonschema:"等待启动超时时间（秒），默认60秒"`
		HealthCheckMethod string            `json:"health_check_method,omitempty" jsonschema:"健康检查请求方法，默认GET"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "start_process",
		Description: "启动一个进程并收集其所有日志，通过调用健康检查接口确认启动成功，支持设置环境变量和工作目录。注意：command 应该是可执行文件名（如 'go', 'python', 'node'），实际的命令参数应该放在 args 中（如 ['run', '.']）",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args startProcessArgs) (*mcp.CallToolResult, any, error) {
		// 获取工具执行权限，确保工具串行执行
		acquireToolSemaphore()
		defer releaseToolSemaphore()

		timeout := time.Duration(args.TimeoutSeconds) * time.Second
		if timeout == 0 {
			timeout = 60 * time.Second
		}

		logger.Info("=== 开始启动进程 ===")
		logger.Info("进程名称: %s", args.Name)
		logger.Info("命令: %s %v", args.Command, args.Args)
		logger.Info("工作目录: %s (如为空则使用脚本所在目录)", args.WorkDir)
		logger.Info("健康检查: %s", args.HealthCheckURL)

		// 检查 command 中是否包含空格（可能是用户试图传递完整命令行）
		if strings.Contains(args.Command, " ") {
			logger.Error("命令参数错误: '%s' 包含空格。请将命令拆分：command 设为可执行文件名（如 'go'），args 设为参数列表（如 ['run', '.']）", args.Command)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("命令参数错误：command '%s' 包含空格\n\n正确用法：\n- command: 可执行文件名（如 'go', 'python', 'npm'）\n- args: 参数列表（如 ['run', '.']）\n\n示例：启动 Go 项目\n  command: \"go\"\n  args: [\"run\", \".\"]\n\n示例：启动 Python 项目\n  command: \"python\"\n  args: [\"-m\", \"http.server\", \"8080\"]", args.Command)},
				},
				IsError: true,
			}, nil, nil
		}

		// 如果之前有同名进程在运行，先清理它
		if oldProcess, exists := processManager.GetProcess(args.Name); exists {
			logger.Info("发现同名进程 %s (PID: %d) 仍在运行，先清理...", args.Name, oldProcess.Cmd.Process.Pid)
			if err := processManager.KillProcess(args.Name); err != nil {
				logger.Error("清理旧进程失败: %v", err)
			}
			// 等待一下让端口释放
			time.Sleep(1 * time.Second)
		}

		// 检查端口是否被其他进程占用（非本MCP启动的进程），如果是则尝试清理
		// 注意：只有在端口确实被占用时才会执行清理
		if err := KillProcessByHealthCheckURL(args.HealthCheckURL); err != nil {
			// 端口未被占用或清理失败都不算严重错误
			logger.Debug("端口检查: %v", err)
		}

		processInfo, err := processManager.StartProcess(args.Name, args.Command, args.Args, args.Env, args.WorkDir, args.HealthCheckURL, timeout)
		if err != nil {
			logger.Error("启动进程失败: %v", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("启动进程失败: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}

		// 启动健康检查（优先使用端口检查，同时监控进程退出）
		var healthCheckErr error
		if processInfo.HealthCheckPort > 0 {
			// 使用端口检查（更快，无需 HTTP），同时监控进程退出
			logger.Info("使用端口检查: %d", processInfo.HealthCheckPort)
			healthCheckErr = waitForPortReadyWithExitCheck(processInfo.HealthCheckPort, timeout, processInfo.ExitChan)
		} else {
			// 回退到 HTTP URL 检查，同时监控进程退出
			logger.Info("使用 HTTP URL 检查: %s", args.HealthCheckURL)
			healthCheckErr = waitForHTTPReadyWithExitCheck(ctx, args.HealthCheckURL, args.HealthCheckMethod, timeout, processInfo.ExitChan)
		}

		if healthCheckErr != nil {
			// 超时后终止进程
			processManager.KillProcess(args.Name)
			logger.Error("进程 %s 启动失败: %v", args.Name, healthCheckErr)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("进程启动失败\nPID: %d\n健康检查URL: %s\n错误: %v\n\n已收集日志:\n%s",
						processInfo.Cmd.Process.Pid,
						args.HealthCheckURL,
						healthCheckErr,
						processInfo.LogBuffer.String())},
				},
				IsError: true,
			}, nil, nil
		}

		logs := processInfo.LogBuffer.String()
		logger.Info("进程 %s 启动成功", args.Name)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("进程已成功启动\nPID: %d\n启动时间: %s\n工作目录: %s\n健康检查: %s\n\n启动日志:\n%s",
					processInfo.Cmd.Process.Pid,
					processInfo.StartTime.Format(time.RFC3339),
					processInfo.Cmd.Dir,
					args.HealthCheckURL,
					logs)},
			},
		}, nil, nil
	})

	// 注册 request_with_logs 工具：发起HTTP请求并获取日志
	type requestWithLogsArgs struct {
		ProcessName string            `json:"process_name,omitempty" jsonschema:"进程名称（可选），如果提供则使用该进程的host和port替换URL中的host和port"`
		URL         string            `json:"url" jsonschema:"要请求的URL，可以是完整URL或路径"`
		Method      string            `json:"method,omitempty" jsonschema:"HTTP方法，默认GET"`
		Headers     map[string]string `json:"headers,omitempty" jsonschema:"HTTP请求头"`
		Body        string            `json:"body,omitempty" jsonschema:"请求体内容"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "request_with_logs",
		Description: "发起HTTP请求（支持GET/POST/PUT/DELETE等），如果指定了进程名称则自动使用该进程的host和port替换URL中的host和port，返回请求响应和请求期间的进程日志",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args requestWithLogsArgs) (*mcp.CallToolResult, any, error) {
		// 获取工具执行权限，确保工具串行执行
		acquireToolSemaphore()
		defer releaseToolSemaphore()

		logger.Info("=== 发起HTTP请求 ===")
		logger.Info("进程: %s", args.ProcessName)
		logger.Info("方法: %s", args.Method)
		logger.Info("URL: %s", args.URL)

		// 获取进程信息（可选）
		var processInfo *ProcessInfo
		var ok bool
		if args.ProcessName != "" {
			processInfo, ok = processManager.GetProcess(args.ProcessName)
			if !ok {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("进程不存在: %s", args.ProcessName)},
					},
					IsError: true,
				}, nil, nil
			}
		}

		// 构建最终的URL
		var fullURL string
		if args.ProcessName != "" && processInfo != nil && processInfo.HealthCheckURL != "" {
			// 如果指定了进程名，从健康检查URL中提取scheme和host:port
			parsedHealthURL, err := url.Parse(processInfo.HealthCheckURL)
			if err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("解析健康检查URL失败: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			// 解析用户提供的URL
			if strings.HasPrefix(args.URL, "http://") || strings.HasPrefix(args.URL, "https://") {
				// 完整URL，替换host和port
				parsedURL, err := url.Parse(args.URL)
				if err != nil {
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{Text: fmt.Sprintf("解析URL失败: %v", err)},
						},
						IsError: true,
					}, nil, nil
				}

				// 替换scheme和host
				parsedURL.Scheme = parsedHealthURL.Scheme
				parsedURL.Host = parsedHealthURL.Host
				fullURL = parsedURL.String()
			} else {
				// 只是路径，拼接完整URL
				baseURL := fmt.Sprintf("%s://%s", parsedHealthURL.Scheme, parsedHealthURL.Host)
				path := args.URL
				if !strings.HasPrefix(path, "/") {
					path = "/" + path
				}
				fullURL = baseURL + path
			}

			logger.Info("使用进程 %s 的地址: %s", args.ProcessName, fullURL)
		} else {
			// 没有指定进程名，直接使用用户提供的URL
			fullURL = args.URL
		}

		logger.Info("最终URL: %s", fullURL)

		// 如果有进程信息，标记请求开始时间
		var requestStartTime time.Time
		if processInfo != nil {
			requestStartTime = processInfo.StartRequestLog()
			logger.Debug("标记请求开始时间: %v", requestStartTime)
		}

		// 创建HTTP请求
		method := strings.ToUpper(args.Method)
		if method == "" {
			method = "GET"
		}

		var bodyReader io.Reader
		if args.Body != "" {
			bodyReader = strings.NewReader(args.Body)
		}

		// 使用带超时的上下文，避免请求卡死
		reqCtx, reqCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer reqCancel()

		req, err := http.NewRequestWithContext(reqCtx, method, fullURL, bodyReader)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("创建请求失败: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}

		// 设置请求头
		for key, value := range args.Headers {
			req.Header.Set(key, value)
		}

		// 如果有 body 且没有设置 Content-Type，自动设置为 application/json
		if args.Body != "" && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}

		// 使用带超时的 HTTP 客户端
		httpClient := &http.Client{
			Timeout: 60 * time.Second,
		}

		// 发起请求
		startTime := time.Now()
		resp, err := httpClient.Do(req)
		duration := time.Since(startTime)

		var responseBody string
		var statusCode int
		if err != nil {
			responseBody = fmt.Sprintf("请求失败: %v", err)
			statusCode = 0
			logger.Error("HTTP请求失败: %v", err)
		} else {
			statusCode = resp.StatusCode
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			responseBody = string(bodyBytes)
			logger.Info("HTTP请求成功: 状态码=%d, 耗时=%v", statusCode, duration)
		}

		// 获取请求期间的日志（使用时间窗口，无需等待）
		var requestLogs string
		if processInfo != nil {
			// 使用时间窗口获取日志（包含请求前100ms到请求后500ms的日志）
			requestLogs = processInfo.GetRequestLog(requestStartTime)
			logger.Debug("使用时间窗口获取请求日志，长度: %d", len(requestLogs))

			// 如果日志太长，截取
			if len(requestLogs) > 5000 {
				requestLogs = "...(日志过长，已截取)...\n" + requestLogs[len(requestLogs)-5000:]
			}
		}

		// 构建响应文本
		responseText := fmt.Sprintf("请求完成\n方法: %s\nURL: %s\n状态码: %d\n耗时: %v\n\n响应:\n%s",
			method, fullURL, statusCode, duration, responseBody)

		if processInfo != nil && requestLogs != "" {
			responseText += fmt.Sprintf("\n\n请求期间进程日志:\n%s", requestLogs)
		}

		// 构建结构化响应
		structuredResp := map[string]any{
			"status_code": statusCode,
			"duration_ms": duration.Milliseconds(),
			"response":    responseBody,
		}
		if processInfo != nil {
			structuredResp["logs"] = requestLogs
		}

		return &mcp.CallToolResult{
			StructuredContent: structuredResp,
			Content: []mcp.Content{
				&mcp.TextContent{Text: responseText},
			},
		}, nil, nil
	})

	// 注册 kill_process 工具：杀掉进程
	type killProcessArgs struct {
		Name string `json:"name,omitempty" jsonschema:"进程名称（可选），如果提供则优先匹配本mcp启动的进程"`
		Port int    `json:"port,omitempty" jsonschema:"端口号（可选），杀掉占用该端口的进程"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "kill_process",
		Description: "杀掉进程。可以通过进程名称（优先匹配本mcp启动的进程）或端口号来指定要杀掉的进程。如果同时提供name和port，优先使用name。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args killProcessArgs) (*mcp.CallToolResult, any, error) {
		// 获取工具执行权限，确保工具串行执行
		acquireToolSemaphore()
		defer releaseToolSemaphore()

		logger.Info("=== 开始终止进程 ===")

		// 检查参数
		if args.Name == "" && args.Port == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "参数错误：必须提供 name 或 port 参数中的至少一个"},
				},
				IsError: true,
			}, nil, nil
		}

		// 优先使用 name 参数
		if args.Name != "" {
			logger.Info("尝试通过进程名称终止: %s", args.Name)

			// 首先尝试从本 mcp 启动的进程中查找
			if info, ok := processManager.GetProcess(args.Name); ok {
				logger.Info("找到本 mcp 启动的进程: %s (PID: %d)", args.Name, info.Cmd.Process.Pid)

				if err := processManager.KillProcess(args.Name); err != nil {
					logger.Error("终止进程失败: %v", err)
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{Text: fmt.Sprintf("终止进程失败: %v", err)},
						},
						IsError: true,
					}, nil, nil
				}

				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("成功终止进程\n进程名称: %s\nPID: %d", args.Name, info.Cmd.Process.Pid)},
					},
				}, nil, nil
			}

			// 如果本 mcp 没有启动过这个进程，提示用户
			logger.Info("本 mcp 未启动过名为 '%s' 的进程", args.Name)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("未找到进程 '%s'\n提示：本 mcp 未启动过此进程。如果该进程正在运行，请使用 port 参数来终止它。", args.Name)},
				},
				IsError: true,
			}, nil, nil
		}

		// 使用 port 参数
		if args.Port > 0 {
			logger.Info("尝试通过端口号终止进程: %d", args.Port)

			if err := killProcessByPort(args.Port); err != nil {
				logger.Error("终止端口 %d 的进程失败: %v", args.Port, err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("终止端口 %d 的进程失败: %v", args.Port, err)},
					},
					IsError: true,
				}, nil, nil
			}

			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("成功终止占用端口 %d 的进程", args.Port)},
				},
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "未知错误"},
			},
			IsError: true,
		}, nil, nil
	})
}

// waitForHTTPReady 等待 HTTP 服务就绪（防止 channel 泄漏）
func waitForHTTPReady(ctx context.Context, url, method string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 HTTP 服务就绪超时")
		case <-ticker.C:
			// 创建健康检查请求
			reqMethod := method
			if reqMethod == "" {
				reqMethod = "GET"
			}

			req, err := http.NewRequestWithContext(ctx, reqMethod, url, nil)
			if err != nil {
				continue
			}

			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					return nil // 服务就绪
				}
			}
		}
	}
}

// waitForHTTPReadyWithExitCheck 等待 HTTP 服务就绪，同时监控进程退出
func waitForHTTPReadyWithExitCheck(ctx context.Context, url, method string, timeout time.Duration, exitChan <-chan error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 HTTP 服务就绪超时")
		case <-ticker.C:
			// 创建健康检查请求
			reqMethod := method
			if reqMethod == "" {
				reqMethod = "GET"
			}

			req, err := http.NewRequestWithContext(ctx, reqMethod, url, nil)
			if err != nil {
				continue
			}

			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					return nil // 服务就绪
				}
			}
		case exitErr := <-exitChan:
			// 进程退出
			if exitErr != nil {
				return fmt.Errorf("进程异常退出: %v", exitErr)
			}
			return fmt.Errorf("进程已退出")
		}
	}
}
