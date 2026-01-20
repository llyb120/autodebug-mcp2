package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
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
		} else {
			// 没有指定进程名，尝试通过 URL 自动关联
			// 首先构建完整的请求 URL
			var fullURL string
			if strings.HasPrefix(args.URL, "http://") || strings.HasPrefix(args.URL, "https://") {
				fullURL = args.URL
			} else {
				// 如果不是完整 URL，无法自动关联
				fullURL = args.URL
			}

			// 尝试通过 URL 查找匹配的进程
			if strings.HasPrefix(fullURL, "http://") || strings.HasPrefix(fullURL, "https://") {
				processInfo = processManager.FindProcessByURL(fullURL)
			}
		}

		// 构建最终的URL
		var fullURL string
		if processInfo != nil && processInfo.HealthCheckURL != "" {
			// 如果关联到了进程（通过名称或自动关联），从健康检查URL中提取scheme和host:port
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

			if args.ProcessName != "" {
				logger.Info("使用进程 %s 的地址: %s", args.ProcessName, fullURL)
			} else {
				logger.Info("使用自动关联进程 %s 的地址: %s", processInfo.Name, fullURL)
			}
		} else {
			// 没有关联进程，直接使用用户提供的URL
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
			// 使用时间窗口获取日志（包含请求前1秒到请求后500ms的日志）
			requestLogs = processInfo.GetRequestLog(requestStartTime)
			if requestLogs == "" {
				requestLogs = "(请求期间无进程日志输出)"
				logger.Debug("请求期间未捕获到进程日志")
			}
		} else {
			requestLogs = "(未关联进程)"
		}

		// 每次请求都写入日志文件（包含请求期间的进程日志）
		logFilePath := writeResponseToFile(method, fullURL, statusCode, duration, responseBody, requestLogs)

		// 计算总内容长度，决定是返回完整内容还是只返回文件路径
		totalContentLen := len(responseBody) + len(requestLogs)
		const maxInlineLen = 4000 // 超过4000字符就只返回文件路径

		var responseText string
		structuredResp := map[string]any{
			"status_code": statusCode,
			"duration_ms": duration.Milliseconds(),
		}

		if logFilePath != "" {
			structuredResp["log_file"] = logFilePath
		}

		if totalContentLen > maxInlineLen && logFilePath != "" {
			// 内容过长，只返回文件路径和摘要
			logger.Info("响应内容过长(%d字符)，完整内容见: %s", totalContentLen, logFilePath)

			// 构建简短的响应摘要
			responseSummary := responseBody
			if len(responseSummary) > 500 {
				responseSummary = responseSummary[:500] + "\n...(已截取)..."
			}

			responseText = fmt.Sprintf("请求完成\n方法: %s\nURL: %s\n状态码: %d\n耗时: %v\n\n⚠️ 内容过长，完整响应和日志已保存到:\n%s\n\n响应摘要:\n%s",
				method, fullURL, statusCode, duration, logFilePath, responseSummary)

			structuredResp["response_summary"] = truncateString(responseBody, 500)
			if processInfo != nil && requestLogs != "" {
				structuredResp["logs_summary"] = truncateString(requestLogs, 500)
			}
		} else {
			// 内容不长，返回完整内容（同时也告知日志文件位置）
			responseText = fmt.Sprintf("请求完成\n方法: %s\nURL: %s\n状态码: %d\n耗时: %v\n\n响应:\n%s",
				method, fullURL, statusCode, duration, responseBody)
			if processInfo != nil && requestLogs != "" {
				responseText += fmt.Sprintf("\n\n请求期间进程日志:\n%s", requestLogs)
			}
			// if logFilePath != "" {
			// 	responseText += fmt.Sprintf("\n\n(完整日志已保存: %s)", logFilePath)
			// }

			structuredResp["response"] = responseBody
			if processInfo != nil {
				structuredResp["logs"] = requestLogs
			}
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

	// 注册 save_memory 工具：保存记忆到文件（包含提示词）
	type saveMemoryArgs struct {
		SystemPrompt string `json:"system_prompt" jsonschema:"你的系统提示词完整内容，将被保存到记忆文件中以便恢复时使用"`
		Content      string `json:"content" jsonschema:"要保存的记忆内容，包括当前任务、调试进度、关键发现、待办事项等。注意：更新记忆时，必须先调用 read_memory 读取现有内容，在其基础上修改后再保存，否则会覆盖丢失之前的记忆！"`
		MemoryID     string `json:"memory_id,omitempty" jsonschema:"记忆ID（可选），如果不提供则新建，如果提供则更新对应记忆文件。更新时必须先 read_memory 读取现有内容！"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_memory",
		Description: "保存记忆到文件。你需要将自己的系统提示词（system prompt）完整写入 system_prompt 参数，记忆内容写入 content 参数。如果提供memory_id则更新现有记忆，否则创建新记忆。\n\n⚠️ **重要警告**：更新记忆时（提供memory_id），必须先调用 read_memory 读取现有记忆内容，在读取到的内容基础上进行修改/增量添加，然后再调用 save_memory 写回。直接覆盖写入会导致之前的记忆丢失！\n\n正确流程：read_memory → 在返回的content基础上修改 → save_memory",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args saveMemoryArgs) (*mcp.CallToolResult, any, error) {
		// 获取工具执行权限，确保工具串行执行
		acquireToolSemaphore()
		defer releaseToolSemaphore()

		logger.Info("=== 保存记忆 ===")

		if args.SystemPrompt == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "参数错误：system_prompt 不能为空，请提供你的完整系统提示词"},
				},
				IsError: true,
			}, nil, nil
		}

		if args.Content == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "参数错误：content 不能为空"},
				},
				IsError: true,
			}, nil, nil
		}

		// 获取可执行文件所在目录
		execPath, err := os.Executable()
		if err != nil {
			logger.Error("获取可执行文件路径失败: %v", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("保存记忆失败: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}
		execDir := filepath.Dir(execPath)
		memsDir := filepath.Join(execDir, "mems")

		// 确保mems目录存在
		if err := os.MkdirAll(memsDir, 0755); err != nil {
			logger.Error("创建mems目录失败: %v", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("保存记忆失败: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}

		// 确定记忆ID和文件路径
		var memoryID string
		var filePath string
		isUpdate := false
		var previousContent string

		if args.MemoryID != "" {
			// 使用提供的记忆ID（更新模式）
			memoryID = args.MemoryID
			filename := fmt.Sprintf("%s.md", memoryID)
			filePath = filepath.Join(memsDir, filename)
			isUpdate = true
			logger.Info("使用提供的记忆ID进行更新: %s", memoryID)

			// 读取现有记忆内容，用于比较和警告
			if existingContent, err := os.ReadFile(filePath); err == nil {
				// 提取现有的任务记忆部分
				contentStr := string(existingContent)
				if idx := strings.Index(contentStr, "## 任务记忆\n\n"); idx >= 0 {
					memStart := idx + len("## 任务记忆\n\n")
					if endIdx := strings.Index(contentStr[memStart:], "\n\n---\n\n"); endIdx >= 0 {
						previousContent = contentStr[memStart : memStart+endIdx]
					} else {
						previousContent = contentStr[memStart:]
					}
				}
				logger.Info("读取到现有记忆内容，长度: %d 字符", len(previousContent))
			}
		} else {
			// 创建新的记忆ID
			memoryID = uuid.New().String()
			filename := fmt.Sprintf("%s.md", memoryID)
			filePath = filepath.Join(memsDir, filename)
			logger.Info("创建新的记忆ID: %s", memoryID)
		}

		// 构建文件内容（包含提示词和记忆内容）
		var content strings.Builder
		content.WriteString("# 记忆文件\n\n")
		content.WriteString(fmt.Sprintf("**记忆ID**: `%s`\n\n", memoryID))
		content.WriteString(fmt.Sprintf("**保存时间**: %s\n\n", time.Now().Format(time.RFC3339)))
		if isUpdate {
			content.WriteString("**操作**: 更新现有记忆\n\n")
		} else {
			content.WriteString("**操作**: 创建新记忆\n\n")
		}
		content.WriteString("---\n\n")
		content.WriteString("## 系统提示词\n\n")
		content.WriteString("```markdown\n")
		content.WriteString(args.SystemPrompt)
		content.WriteString("\n```\n\n")
		content.WriteString("---\n\n")
		content.WriteString("## 任务记忆\n\n")
		content.WriteString(args.Content)
		content.WriteString("\n\n---\n\n")
		content.WriteString(fmt.Sprintf("**⚠️ 重要**: 如果上下文被截断，请读取此文件恢复状态: `%s`\n", filePath))

		// 写入文件
		if err := os.WriteFile(filePath, []byte(content.String()), 0644); err != nil {
			logger.Error("写入记忆文件失败: %v", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("保存记忆失败: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}

		// 构建返回消息
		var actionText string
		var warningText string
		if isUpdate {
			actionText = "✅ 记忆已更新"
			// 检查是否可能丢失了内容（新内容比旧内容短很多，或完全不包含旧内容的关键部分）
			if previousContent != "" && len(args.Content) < len(previousContent)/2 {
				warningText = fmt.Sprintf("\n\n⚠️ **警告**: 新记忆内容(%d字符)比之前(%d字符)短很多，请确认是否遗漏了重要信息！", len(args.Content), len(previousContent))
				logger.Info("警告：新记忆内容可能丢失了信息，旧长度=%d，新长度=%d", len(previousContent), len(args.Content))
			}
		} else {
			actionText = "✅ 记忆已保存"
		}

		logger.Info("记忆已保存到: %s", filePath)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("%s\n\n**记忆ID**: `%s`\n**文件路径**: `%s`\n**记忆内容长度**: %d 字符\n**系统提示词长度**: %d 字符%s\n\n⚠️ **请记住此记忆ID**，如果上下文被截断，使用 read_memory 读取此记忆即可恢复完整状态。\n\n💡 **提示**: 更新记忆时，请务必先调用 read_memory 读取现有内容，在其基础上修改后再调用 save_memory，避免覆盖丢失之前的记忆！", actionText, memoryID, filePath, len(args.Content), len(args.SystemPrompt), warningText)},
			},
		}, nil, nil
	})

	// 注册 read_memory 工具：根据ID读取记忆文件
	type readMemoryArgs struct {
		MemoryID string `json:"memory_id" jsonschema:"记忆ID，必须提供才能读取对应的记忆文件"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_memory",
		Description: "根据记忆ID读取记忆文件内容。必须提供memory_id参数。返回记忆文件中的系统提示词和任务记忆。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args readMemoryArgs) (*mcp.CallToolResult, any, error) {
		// 获取工具执行权限，确保工具串行执行
		acquireToolSemaphore()
		defer releaseToolSemaphore()

		logger.Info("=== 读取记忆 ===")

		// 检查记忆ID参数
		if args.MemoryID == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "参数错误：必须提供memory_id参数"},
				},
				IsError: true,
			}, nil, nil
		}

		memoryID := args.MemoryID

		// 获取可执行文件所在目录
		execPath, err := os.Executable()
		if err != nil {
			logger.Error("获取可执行文件路径失败: %v", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("读取记忆失败: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}
		execDir := filepath.Dir(execPath)
		memsDir := filepath.Join(execDir, "mems")

		// 构建文件路径
		filename := fmt.Sprintf("%s.md", memoryID)
		filePath := filepath.Join(memsDir, filename)

		// 检查文件是否存在
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("记忆文件不存在: %s", filePath)},
				},
				IsError: true,
			}, nil, nil
		}

		// 读取文件内容
		content, err := os.ReadFile(filePath)
		if err != nil {
			logger.Error("读取记忆文件失败: %v", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("读取记忆失败: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}

		logger.Info("成功读取记忆文件: %s", filePath)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("✅ 记忆读取成功\n\n**记忆ID**: `%s`\n**文件路径**: `%s`\n**文件大小**: %d 字符\n\n---\n\n%s", memoryID, filePath, len(content), string(content))},
			},
		}, nil, nil
	})

	// 注册 save_knowledge 工具：保存或更新知识到知识库
	type saveKnowledgeArgs struct {
		Title       string   `json:"title" jsonschema:"知识标题，简短描述这条知识的主题"`
		Content     string   `json:"content" jsonschema:"知识内容，详细的知识描述"`
		Tags        []string `json:"tags,omitempty" jsonschema:"标签列表，用于分类和检索"`
		Category    string   `json:"category,omitempty" jsonschema:"分类，如: 代码规范、API文档、问题解决、最佳实践等"`
		KnowledgeID string   `json:"knowledge_id,omitempty" jsonschema:"知识ID（可选），如果提供则更新现有知识，否则创建新知识"`
		WorkDir     string   `json:"work_dir" jsonschema:"工作目录，知识库将保存在该目录下的 .knowledge 文件夹中"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_knowledge",
		Description: "保存或更新知识到知识库。用于积累和保存可复用的知识，如代码规范、问题解决方案、API文档、最佳实践等。支持标签和分类，便于后续检索。知识库保存在工作目录的 .knowledge 文件夹中。如果提供 knowledge_id 则更新现有知识，否则创建新知识。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args saveKnowledgeArgs) (*mcp.CallToolResult, any, error) {
		// 获取工具执行权限，确保工具串行执行
		acquireToolSemaphore()
		defer releaseToolSemaphore()

		logger.Info("=== 保存知识 ===")

		if args.Title == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "参数错误：title 不能为空"},
				},
				IsError: true,
			}, nil, nil
		}

		if args.Content == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "参数错误：content 不能为空"},
				},
				IsError: true,
			}, nil, nil
		}

		if args.WorkDir == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "参数错误：work_dir 不能为空，请提供工作目录路径"},
				},
				IsError: true,
			}, nil, nil
		}

		// 使用工作目录下的 .knowledge 文件夹
		knowledgeDir := filepath.Join(args.WorkDir, ".knowledge")

		// 确保knowledge目录存在
		if err := os.MkdirAll(knowledgeDir, 0755); err != nil {
			logger.Error("创建knowledge目录失败: %v", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("保存知识失败: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}

		// 确定知识ID和文件路径
		var knowledgeID string
		var filePath string
		isUpdate := false
		var createdTime string

		if args.KnowledgeID != "" {
			// 使用提供的知识ID（更新模式）
			knowledgeID = args.KnowledgeID
			filename := fmt.Sprintf("%s.md", knowledgeID)
			filePath = filepath.Join(knowledgeDir, filename)

			// 检查文件是否存在
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("知识不存在: %s，请检查 knowledge_id 是否正确", knowledgeID)},
					},
					IsError: true,
				}, nil, nil
			}

			// 读取原文件获取创建时间
			oldContent, err := os.ReadFile(filePath)
			if err == nil {
				contentStr := string(oldContent)
				if idx := strings.Index(contentStr, "**创建时间**: "); idx >= 0 {
					start := idx + len("**创建时间**: ")
					if end := strings.Index(contentStr[start:], "\n"); end >= 0 {
						createdTime = strings.TrimSpace(contentStr[start : start+end])
					}
				}
			}
			if createdTime == "" {
				createdTime = time.Now().Format(time.RFC3339)
			}

			isUpdate = true
			logger.Info("使用提供的知识ID进行更新: %s", knowledgeID)
		} else {
			// 创建新的知识ID
			knowledgeID = uuid.New().String()
			filename := fmt.Sprintf("%s.md", knowledgeID)
			filePath = filepath.Join(knowledgeDir, filename)
			createdTime = time.Now().Format(time.RFC3339)
			logger.Info("创建新的知识ID: %s", knowledgeID)
		}

		// 设置默认分类
		category := args.Category
		if category == "" {
			category = "通用"
		}

		// 构建标签字符串
		tagsStr := ""
		if len(args.Tags) > 0 {
			tagsStr = strings.Join(args.Tags, ", ")
		}

		// 构建文件内容
		var content strings.Builder
		content.WriteString("# " + args.Title + "\n\n")
		content.WriteString(fmt.Sprintf("**知识ID**: `%s`\n\n", knowledgeID))
		content.WriteString(fmt.Sprintf("**创建时间**: %s\n\n", createdTime))
		if isUpdate {
			content.WriteString(fmt.Sprintf("**更新时间**: %s\n\n", time.Now().Format(time.RFC3339)))
		}
		content.WriteString(fmt.Sprintf("**分类**: %s\n\n", category))
		if tagsStr != "" {
			content.WriteString(fmt.Sprintf("**标签**: %s\n\n", tagsStr))
		}
		content.WriteString("---\n\n")
		content.WriteString("## 内容\n\n")
		content.WriteString(args.Content)
		content.WriteString("\n")

		// 写入文件
		if err := os.WriteFile(filePath, []byte(content.String()), 0644); err != nil {
			logger.Error("写入知识文件失败: %v", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("保存知识失败: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}

		// 构建返回消息
		var actionText string
		if isUpdate {
			actionText = "✅ 知识已更新"
		} else {
			actionText = "✅ 知识已保存"
		}

		logger.Info("知识已保存到: %s", filePath)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("%s\n\n**知识ID**: `%s`\n**标题**: %s\n**分类**: %s\n**标签**: %s\n**文件路径**: `%s`\n\n💡 使用 `search_knowledge` 工具可以检索知识库。如需更新此知识，请在下次调用时传入 knowledge_id。", actionText, knowledgeID, args.Title, category, tagsStr, filePath)},
			},
		}, nil, nil
	})

	// 注册 search_knowledge 工具：检索知识库
	type searchKnowledgeArgs struct {
		Query    string   `json:"query,omitempty" jsonschema:"搜索关键词，在标题和内容中搜索"`
		Tags     []string `json:"tags,omitempty" jsonschema:"按标签过滤"`
		Category string   `json:"category,omitempty" jsonschema:"按分类过滤"`
		Limit    int      `json:"limit,omitempty" jsonschema:"返回结果数量限制，默认10"`
		WorkDir  string   `json:"work_dir" jsonschema:"工作目录，知识库位于该目录下的 .knowledge 文件夹中"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_knowledge",
		Description: "检索知识库。支持按关键词搜索、按标签过滤、按分类过滤。如果不提供搜索条件，则列出所有知识条目。知识库位于工作目录的 .knowledge 文件夹中。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args searchKnowledgeArgs) (*mcp.CallToolResult, any, error) {
		// 获取工具执行权限，确保工具串行执行
		acquireToolSemaphore()
		defer releaseToolSemaphore()

		logger.Info("=== 检索知识库 ===")
		logger.Info("查询: %s, 标签: %v, 分类: %s, 工作目录: %s", args.Query, args.Tags, args.Category, args.WorkDir)

		if args.WorkDir == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "参数错误：work_dir 不能为空，请提供工作目录路径"},
				},
				IsError: true,
			}, nil, nil
		}

		// 使用工作目录下的 .knowledge 文件夹
		knowledgeDir := filepath.Join(args.WorkDir, ".knowledge")

		// 检查目录是否存在
		if _, err := os.Stat(knowledgeDir); os.IsNotExist(err) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "知识库为空，尚未保存任何知识。使用 `save_knowledge` 工具添加知识。"},
				},
			}, nil, nil
		}

		// 读取所有知识文件
		files, err := os.ReadDir(knowledgeDir)
		if err != nil {
			logger.Error("读取知识目录失败: %v", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("检索知识库失败: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}

		if len(files) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "知识库为空，尚未保存任何知识。使用 `save_knowledge` 工具添加知识。"},
				},
			}, nil, nil
		}

		// 设置默认限制
		limit := args.Limit
		if limit <= 0 {
			limit = 10
		}

		// 准备搜索条件
		queryLower := strings.ToLower(args.Query)
		categoryLower := strings.ToLower(args.Category)
		var tagsLower []string
		for _, tag := range args.Tags {
			tagsLower = append(tagsLower, strings.ToLower(tag))
		}

		type KnowledgeItem struct {
			ID       string
			Title    string
			Category string
			Tags     string
			FilePath string
			Preview  string
		}

		var results []KnowledgeItem
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".md") {
				continue
			}

			filePath := filepath.Join(knowledgeDir, file.Name())
			content, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}

			contentStr := string(content)
			contentLower := strings.ToLower(contentStr)

			// 解析知识文件
			var title, category, tags, knowledgeID, preview string

			// 提取标题
			if idx := strings.Index(contentStr, "\n"); idx > 0 {
				titleLine := strings.TrimPrefix(contentStr[:idx], "# ")
				title = strings.TrimSpace(titleLine)
			}

			// 提取知识ID
			if idx := strings.Index(contentStr, "**知识ID**: `"); idx >= 0 {
				start := idx + len("**知识ID**: `")
				if end := strings.Index(contentStr[start:], "`"); end >= 0 {
					knowledgeID = contentStr[start : start+end]
				}
			}

			// 提取分类
			if idx := strings.Index(contentStr, "**分类**: "); idx >= 0 {
				start := idx + len("**分类**: ")
				if end := strings.Index(contentStr[start:], "\n"); end >= 0 {
					category = strings.TrimSpace(contentStr[start : start+end])
				}
			}

			// 提取标签
			if idx := strings.Index(contentStr, "**标签**: "); idx >= 0 {
				start := idx + len("**标签**: ")
				if end := strings.Index(contentStr[start:], "\n"); end >= 0 {
					tags = strings.TrimSpace(contentStr[start : start+end])
				}
			}

			// 提取预览（内容部分的前200字符）
			if idx := strings.Index(contentStr, "## 内容\n\n"); idx >= 0 {
				previewStart := idx + len("## 内容\n\n")
				previewContent := contentStr[previewStart:]
				if len(previewContent) > 200 {
					preview = previewContent[:200] + "..."
				} else {
					preview = previewContent
				}
			}

			// 应用过滤条件
			match := true

			// 关键词搜索
			if queryLower != "" {
				if !strings.Contains(contentLower, queryLower) {
					match = false
				}
			}

			// 分类过滤
			if categoryLower != "" && match {
				if !strings.Contains(strings.ToLower(category), categoryLower) {
					match = false
				}
			}

			// 标签过滤
			if len(tagsLower) > 0 && match {
				tagsLowerStr := strings.ToLower(tags)
				tagMatch := false
				for _, tag := range tagsLower {
					if strings.Contains(tagsLowerStr, tag) {
						tagMatch = true
						break
					}
				}
				if !tagMatch {
					match = false
				}
			}

			if match {
				results = append(results, KnowledgeItem{
					ID:       knowledgeID,
					Title:    title,
					Category: category,
					Tags:     tags,
					FilePath: filePath,
					Preview:  preview,
				})
			}

			if len(results) >= limit {
				break
			}
		}

		if len(results) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "未找到匹配的知识条目。"},
				},
			}, nil, nil
		}

		// 构建返回结果
		var resultBuilder strings.Builder
		resultBuilder.WriteString(fmt.Sprintf("✅ 找到 %d 条知识\n\n", len(results)))

		for i, item := range results {
			resultBuilder.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, item.Title))
			resultBuilder.WriteString(fmt.Sprintf("- **知识ID**: `%s`\n", item.ID))
			resultBuilder.WriteString(fmt.Sprintf("- **分类**: %s\n", item.Category))
			if item.Tags != "" {
				resultBuilder.WriteString(fmt.Sprintf("- **标签**: %s\n", item.Tags))
			}
			resultBuilder.WriteString(fmt.Sprintf("- **文件**: `%s`\n", item.FilePath))
			resultBuilder.WriteString(fmt.Sprintf("\n**预览**:\n%s\n\n", item.Preview))
			resultBuilder.WriteString("---\n\n")
		}

		resultBuilder.WriteString("💡 使用文件读取工具可以查看完整知识内容。")

		logger.Info("检索完成，找到 %d 条知识", len(results))
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: resultBuilder.String()},
			},
		}, nil, nil
	})
}

// truncateString 截断字符串到指定长度
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// writeResponseToFile 将响应内容写入logs目录下的文件，返回文件路径
func writeResponseToFile(method, url string, statusCode int, duration time.Duration, responseBody, logs string) string {
	logger := GetLogger()

	// 获取可执行文件所在目录
	execPath, err := os.Executable()
	if err != nil {
		logger.Error("获取可执行文件路径失败: %v", err)
		return ""
	}
	execDir := filepath.Dir(execPath)
	logsDir := filepath.Join(execDir, "logs")

	// 确保logs目录存在
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		logger.Error("创建logs目录失败: %v", err)
		return ""
	}

	// 生成文件名：response_YYYYMMDD_HHMMSS_毫秒.log
	timestamp := time.Now().Format("20060102_150405")
	ms := time.Now().UnixMilli() % 1000
	filename := fmt.Sprintf("response_%s_%03d.log", timestamp, ms)
	filePath := filepath.Join(logsDir, filename)

	// 构建文件内容
	var content strings.Builder
	content.WriteString("========================================\n")
	content.WriteString("HTTP 请求响应日志\n")
	content.WriteString("========================================\n")
	content.WriteString(fmt.Sprintf("时间: %s\n", time.Now().Format(time.RFC3339)))
	content.WriteString(fmt.Sprintf("方法: %s\n", method))
	content.WriteString(fmt.Sprintf("URL: %s\n", url))
	content.WriteString(fmt.Sprintf("状态码: %d\n", statusCode))
	content.WriteString(fmt.Sprintf("耗时: %v\n", duration))
	content.WriteString("\n========================================\n")
	content.WriteString("响应内容\n")
	content.WriteString("========================================\n")
	content.WriteString(responseBody)
	content.WriteString("\n")

	// 始终写入进程日志部分（即使是空或者占位符）
	content.WriteString("\n========================================\n")
	content.WriteString("进程日志\n")
	content.WriteString("========================================\n")
	if logs != "" {
		content.WriteString(logs)
	} else {
		content.WriteString("(无进程日志)")
	}
	content.WriteString("\n")

	// 写入文件
	if err := os.WriteFile(filePath, []byte(content.String()), 0644); err != nil {
		logger.Error("写入响应日志文件失败: %v", err)
		return ""
	}

	return filePath
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
