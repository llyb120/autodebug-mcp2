package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 进程管理器：存储和管理运行的进程
type ProcessManager struct {
	processes sync.Map
}

type ProcessInfo struct {
	Cmd             *exec.Cmd
	StdoutPipe      io.ReadCloser
	StderrPipe      io.ReadCloser
	LogBuffer       *bytes.Buffer
	StartTime       time.Time
	Cancel          context.CancelFunc
	Name            string
	HealthCheckURL  string
	HealthCheckPort int // 从URL中提取的端口，用于端口检查

	// 完全无锁结构：使用时间窗口捕获日志
	logChan     chan string  // 主日志通道
	logLines    []string     // 环形缓冲区，存储最近的日志行（带时间戳）
	logTimes    []time.Time  // 每行的时间戳
	logMu       sync.RWMutex // 仅保护 logLines/logTimes 的读写
	maxLogLines int          // 最大日志行数
	logIndex    int          // 当前写入位置（环形）
	ExitChan    chan error   // 进程退出时发送错误（nil表示正常退出，非nil表示异常）

	// 用于协调日志收集goroutine的关闭
	logWg         sync.WaitGroup // 等待日志收集goroutine完成
	logChanClosed bool           // 标记logChan是否已关闭
	logChanMu     sync.Mutex     // 保护logChanClosed的并发访问

	// 进程退出同步 - 确保 Wait() 只被调用一次
	waitDone chan struct{} // 标记Wait()已完成
	waitOnce sync.Once     // 确保只关闭一次waitDone
}

var processManager = &ProcessManager{}

// StartProcess 启动进程并收集日志
func (pm *ProcessManager) StartProcess(name, command string, args []string, env map[string]string, workDir, healthCheckURL string, timeout time.Duration) (*ProcessInfo, error) {
	logger := GetLogger()

	// 如果有同名的旧进程，等待它完全清理
	if oldInfo, exists := pm.GetProcess(name); exists {
		logger.Info("发现同名进程 %s 仍在管理器中，等待清理...", name)
		// 等待旧进程的 Wait() 完成
		select {
		case <-oldInfo.waitDone:
			logger.Info("旧进程 %s 已完成", name)
		case <-time.After(2 * time.Second):
			logger.Info("等待旧进程 %s 超时，强制继续", name)
		}
		// 从管理器中移除旧进程
		pm.processes.Delete(name)
	}

	// 从URL中提取端口用于健康检查
	port, err := extractPortFromURL(healthCheckURL)
	if err != nil {
		logger.Info("从URL提取端口失败: %v，将使用URL健康检查", err)
		port = 0
	}

	// 创建带取消功能的上下文
	ctx, cancel := context.WithCancel(context.Background())

	// 解析命令
	cmd := exec.CommandContext(ctx, command, args...)

	// 设置进程组标志（Windows），确保 taskkill /T 能正确终止子进程
	setProcessGroupID(cmd)

	// 设置工作目录
	if workDir != "" {
		// 如果指定了工作目录，将其转换为绝对路径
		if filepath.IsAbs(workDir) {
			cmd.Dir = workDir
		} else {
			// 相对路径，基于当前工作目录
			cwd, _ := os.Getwd()
			cmd.Dir = filepath.Join(cwd, workDir)
		}
		logger.Info("进程 %s 使用工作目录: %s", name, cmd.Dir)
	} else {
		// 如果没有指定工作目录，尝试从 go -C 参数中提取
		if command == "go" && len(args) >= 2 && args[0] == "-C" {
			// go -C <dir> 命令，提取工作目录
			extractedDir := args[1]
			if filepath.IsAbs(extractedDir) {
				cmd.Dir = extractedDir
			} else {
				cwd, _ := os.Getwd()
				cmd.Dir = filepath.Join(cwd, extractedDir)
			}
			logger.Info("进程 %s 从 go -C 参数提取工作目录: %s", name, cmd.Dir)
		} else {
			// 使用当前目录
			cmd.Dir, _ = os.Getwd()
			logger.Info("进程 %s 使用当前工作目录: %s", name, cmd.Dir)
		}
	}

	// 记录原始参数
	logger.Debug("原始参数: %s %v", command, args)

	// 设置环境变量（合并现有环境变量和新环境变量）
	// 注意：必须正确处理，否则可能导致进程启动卡死
	if len(env) > 0 {
		currentEnv := os.Environ()
		// 创建一个map来存储环境变量，方便覆盖
		envMap := make(map[string]string)
		for _, e := range currentEnv {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}

		// 添加/覆盖用户指定的环境变量
		for key, value := range env {
			envMap[key] = value
			logger.Debug("设置环境变量: %s=%s", key, value)
		}

		// 转换回切片
		cmd.Env = make([]string, 0, len(envMap))
		for key, value := range envMap {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
		logger.Info("进程 %s 设置环境变量: %d 个自定义 + %d 个系统 = %d 个总计",
			name, len(env), len(currentEnv), len(cmd.Env))
	}

	// 创建管道
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("创建 stdout 管道失败: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		stdoutPipe.Close()
		return nil, fmt.Errorf("创建 stderr 管道失败: %w", err)
	}

	// 创建日志缓冲区和环形日志缓冲区
	logBuffer := &bytes.Buffer{}
	logChan := make(chan string, 1000) // 带缓冲的通道，避免阻塞
	maxLogLines := 1000                // 保留最近1000行日志

	processInfo := &ProcessInfo{
		Cmd:             cmd,
		StdoutPipe:      stdoutPipe,
		StderrPipe:      stderrPipe,
		LogBuffer:       logBuffer,
		StartTime:       time.Now(),
		Cancel:          cancel,
		Name:            name,
		HealthCheckURL:  healthCheckURL,
		HealthCheckPort: port,
		logChan:         logChan,
		logLines:        make([]string, maxLogLines),
		logTimes:        make([]time.Time, maxLogLines),
		maxLogLines:     maxLogLines,
		logIndex:        0,
		ExitChan:        make(chan error, 1), // 带缓冲，确保goroutine不会阻塞
		logChanClosed:   false,
		waitDone:        make(chan struct{}), // 用于等待进程退出
	}

	// 启动进程
	if err := cmd.Start(); err != nil {
		cancel()
		stdoutPipe.Close()
		stderrPipe.Close()
		close(logChan)

		// 提供更详细的错误信息
		if strings.Contains(err.Error(), "executable file not found") {
			return nil, fmt.Errorf("启动进程失败: %v\n\n提示：找不到可执行文件 '%s'\n请确保：\n1. 该命令已安装并在 PATH 中\n2. 或使用完整路径（如 'C:\\Go\\bin\\go.exe'）\n\n常见示例：\n- Go: command='go', args=['run', '.']\n- Python: command='python', args=['app.py']\n- Node: command='node', args=['app.js']", err, command)
		}
		return nil, fmt.Errorf("启动进程失败: %w", err)
	}

	// 存储进程信息
	pm.processes.Store(name, processInfo)

	logger.Info("进程 %s 已启动 (PID: %d)", name, cmd.Process.Pid)

	// 标记有2个日志收集协程需要等待
	processInfo.logWg.Add(2)

	// 启动日志收集协程（stdout 和 stderr 都写入同一个 channel）
	go pm.collectStdout(processInfo)
	go pm.collectStderr(processInfo)

	// 启动无锁日志处理协程
	go pm.processLogs(processInfo)

	// 启动进程退出监控协程
	go pm.monitorProcessExit(processInfo)

	return processInfo, nil
}

// processLogs 无锁日志处理器：从 channel 读取日志并处理
func (pm *ProcessManager) processLogs(info *ProcessInfo) {
	logger := GetLogger()

	for line := range info.logChan {
		// 写入主日志 buffer（无锁，单一写入者）
		info.LogBuffer.WriteString(line + "\n")

		// 写入环形缓冲区（使用写锁，快速操作）
		info.logMu.Lock()
		info.logLines[info.logIndex] = line
		info.logTimes[info.logIndex] = time.Now()
		info.logIndex = (info.logIndex + 1) % info.maxLogLines
		info.logMu.Unlock()

		// 输出到日志文件
		logger.ProcessLog(info.Name, line)
	}
}

// collectStdout 收集 stdout 日志到 channel
func (pm *ProcessManager) collectStdout(info *ProcessInfo) {
	defer info.logWg.Done()

	scanner := bufio.NewReader(info.StdoutPipe)
	for {
		line, err := scanner.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				GetLogger().Error("读取 stdout 错误: %v", err)
			}
			break
		}
		// 去掉换行符
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			// 使用非阻塞写入，如果channel已满或已关闭则跳过
			info.logChanMu.Lock()
			closed := info.logChanClosed
			info.logChanMu.Unlock()
			if closed {
				break
			}
			select {
			case info.logChan <- line:
			default:
				// channel满了，丢弃日志避免阻塞
				GetLogger().Debug("日志channel已满，丢弃stdout日志")
			}
		}
	}
}

// collectStderr 收集 stderr 日志到 channel
func (pm *ProcessManager) collectStderr(info *ProcessInfo) {
	defer info.logWg.Done()

	scanner := bufio.NewReader(info.StderrPipe)
	for {
		line, err := scanner.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				GetLogger().Error("读取 stderr 错误: %v", err)
			}
			break
		}
		// 去掉换行符
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			// 使用非阻塞写入，如果channel已满或已关闭则跳过
			info.logChanMu.Lock()
			closed := info.logChanClosed
			info.logChanMu.Unlock()
			if closed {
				break
			}
			select {
			case info.logChan <- line:
			default:
				// channel满了，丢弃日志避免阻塞
				GetLogger().Debug("日志channel已满，丢弃stderr日志")
			}
		}
	}
}

// monitorProcessExit 监控进程退出，如果进程异常退出则通知
func (pm *ProcessManager) monitorProcessExit(info *ProcessInfo) {
	logger := GetLogger()

	// 等待进程退出（Wait() 只能调用一次）
	err := info.Cmd.Wait()

	// 标记 Wait() 已完成，通知其他等待者
	info.waitOnce.Do(func() {
		close(info.waitDone)
	})

	// 进程退出后，关闭管道以让日志收集协程退出
	if info.StdoutPipe != nil {
		info.StdoutPipe.Close()
	}
	if info.StderrPipe != nil {
		info.StderrPipe.Close()
	}

	// 等待日志收集协程完成（带超时）
	done := make(chan struct{})
	go func() {
		info.logWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 日志收集协程已完成
	case <-time.After(2 * time.Second):
		logger.Info("等待日志收集协程超时，继续处理")
	}

	// 安全关闭日志channel
	info.logChanMu.Lock()
	if !info.logChanClosed {
		info.logChanClosed = true
		close(info.logChan)
	}
	info.logChanMu.Unlock()

	// 检查进程是否异常退出（非0退出码）
	if err != nil {
		logger.Error("进程 %s (PID: %d) 异常退出: %v", info.Name, info.Cmd.Process.Pid, err)
		// 发送退出错误到channel
		select {
		case info.ExitChan <- err:
		default:
			// channel已满或已关闭，忽略
		}
	} else {
		logger.Info("进程 %s (PID: %d) 正常退出", info.Name, info.Cmd.Process.Pid)
		// 发送nil表示正常退出
		select {
		case info.ExitChan <- nil:
		default:
		}
	}
}

// GetProcess 获取进程
func (pm *ProcessManager) GetProcess(name string) (*ProcessInfo, bool) {
	val, ok := pm.processes.Load(name)
	if !ok {
		return nil, false
	}
	return val.(*ProcessInfo), true
}

// FindProcessByURL 根据 URL 自动查找匹配的进程
// 通过 host:端口匹配进程的 HealthCheckURL 或 HealthCheckPort
// 支持 localhost/127.0.0.1/::1 等价匹配
func (pm *ProcessManager) FindProcessByURL(requestURL string) *ProcessInfo {
	logger := GetLogger()

	// 解析请求 URL
	parsedURL, err := url.Parse(requestURL)
	if err != nil {
		logger.Debug("解析请求URL失败: %v", err)
		return nil
	}

	// 提取请求的 host 和端口
	requestHost := parsedURL.Hostname()
	requestPort := parsedURL.Port()

	// 标准化请求的 host（处理 localhost/127.0.0.1 等价）
	normalizedRequestHost := normalizeHost(requestHost)

	// 如果没有显式指定端口，使用默认端口
	if requestPort == "" {
		if parsedURL.Scheme == "http" {
			requestPort = "80"
		} else if parsedURL.Scheme == "https" {
			requestPort = "443"
		} else {
			logger.Debug("无法确定默认端口，scheme: %s", parsedURL.Scheme)
			return nil
		}
	}

	logger.Debug("查找进程: requestHost=%s (normalized=%s), requestPort=%s", requestHost, normalizedRequestHost, requestPort)

	var matchedProcess *ProcessInfo

	// 遍历所有进程，查找匹配的
	pm.processes.Range(func(key, value any) bool {
		info := value.(*ProcessInfo)

		// 方法1: 直接比较 HealthCheckURL
		if info.HealthCheckURL != "" {
			healthURL, err := url.Parse(info.HealthCheckURL)
			if err == nil {
				// 标准化健康检查的 host
				normalizedHealthHost := normalizeHost(healthURL.Hostname())
				healthPort := healthURL.Port()

				// 使用默认端口
				if healthPort == "" {
					if healthURL.Scheme == "http" {
						healthPort = "80"
					} else if healthURL.Scheme == "https" {
						healthPort = "443"
					}
				}

				// 比较 host 和端口
				if normalizedRequestHost == normalizedHealthHost && requestPort == healthPort {
					logger.Debug("通过 HealthCheckURL 匹配到进程 %s", info.Name)
					matchedProcess = info
					return false // 停止遍历
				}
			}
		}

		// 方法2: 比较 HealthCheckPort（将请求的端口转为整数）
		if info.HealthCheckPort > 0 {
			requestPortInt, _ := strconv.Atoi(requestPort)
			if requestPortInt == info.HealthCheckPort {
				logger.Debug("通过 HealthCheckPort 匹配到进程 %s", info.Name)
				matchedProcess = info
				return false // 停止遍历
			}
		}

		return true // 继续遍历
	})

	if matchedProcess != nil {
		logger.Info("自动关联进程: %s <- %s", matchedProcess.Name, requestURL)
	}

	return matchedProcess
}

// normalizeHost 标准化主机名，处理 localhost/127.0.0.1/::1 等价
func normalizeHost(host string) string {
	if host == "" {
		return ""
	}

	// 转为小写
	host = strings.ToLower(host)

	// 处理各种 localhost 变体
	switch host {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return "localhost"
	}

	return host
}

// StartRequestLog 标记请求开始时间（完全无锁）
func (pi *ProcessInfo) StartRequestLog() time.Time {
	return time.Now()
}

// GetRequestLog 获取请求期间的日志（使用时间窗口，完全无死锁）
func (pi *ProcessInfo) GetRequestLog(startTime time.Time) string {
	// 使用读锁，允许多个并发读取
	pi.logMu.RLock()
	defer pi.logMu.RUnlock()

	endTime := time.Now().Add(500 * time.Millisecond) // 包含请求后500ms的日志

	logger := GetLogger()
	logger.Debug("GetRequestLog: 开始时间=%v, 结束时间=%v", startTime.Format("15:04:05.000"), endTime.Format("15:04:05.000"))

	var logs []string
	matchCount := 0
	// 遍历环形缓冲区
	for i := 0; i < pi.maxLogLines; i++ {
		idx := (pi.logIndex - 1 - i + pi.maxLogLines) % pi.maxLogLines
		logTime := pi.logTimes[idx]

		// 如果时间戳为零值，说明这个位置还没写入过
		if logTime.IsZero() {
			continue
		}

		// 检查是否在时间窗口内（扩大窗口：请求前1秒到请求后500ms）
		if logTime.After(startTime.Add(-1*time.Second)) && logTime.Before(endTime) {
			logs = append([]string{pi.logLines[idx]}, logs...) // 保持时间顺序
			matchCount++
		}

		// 如果日志时间早于开始时间太多，停止遍历
		if logTime.Before(startTime.Add(-2 * time.Second)) {
			break
		}
	}

	result := strings.Join(logs, "\n")
	logger.Debug("GetRequestLog: 找到 %d 条匹配日志，总长度 %d", matchCount, len(result))
	return result
}

// KillProcess 终止进程
func (pm *ProcessManager) KillProcess(name string) error {
	logger := GetLogger()

	val, ok := pm.processes.Load(name)
	if !ok {
		return fmt.Errorf("进程不存在: %s", name)
	}

	info := val.(*ProcessInfo)

	pid := info.Cmd.Process.Pid
	logger.Info("正在终止进程 %s (PID: %d, HealthCheckPort=%d)...", name, pid, info.HealthCheckPort)

	// 取消上下文
	info.Cancel()

	// 终止进程 - 在Windows上使用taskkill命令，更可靠
	if info.Cmd.Process != nil {
		if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
			// Windows: 使用taskkill命令强制终止进程及其子进程（带超时）
			killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
			killCmd := exec.CommandContext(killCtx, "taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
			output, err := killCmd.CombinedOutput()
			killCancel()
			if err != nil {
				logger.Error("使用taskkill终止进程 %s (PID: %d) 失败: %v, 输出: %s", name, pid, err, string(output))

				// 如果 taskkill 失败（进程已退出），尝试通过端口终止子进程
				// 这对于 `go run` 等会创建子进程的命令特别有用
				if info.HealthCheckPort > 0 {
					logger.Info("进程可能已退出，尝试终止端口 %d 的子进程...", info.HealthCheckPort)
					if portErr := killProcessByPort(info.HealthCheckPort); portErr != nil {
						logger.Error("终止端口 %d 的进程失败: %v", info.HealthCheckPort, portErr)
					} else {
						logger.Info("成功终止端口 %d 的进程", info.HealthCheckPort)
					}
				}
			} else {
				logger.Info("使用taskkill成功终止进程 %s (PID: %d)", name, pid)
			}
		} else {
			// Unix/Linux: 使用Process.Kill()
			if err := info.Cmd.Process.Kill(); err != nil {
				logger.Error("终止进程 %s 失败: %v", name, err)
			} else {
				logger.Info("成功终止进程 %s (PID: %d)", name, pid)
			}
		}

		// 等待 monitorProcessExit 协程中的 Wait() 完成，而不是重复调用 Wait()
		// Wait() 只能调用一次，重复调用会导致死锁
		select {
		case <-info.waitDone:
			logger.Info("进程 %s 已结束", name)
		case <-time.After(3 * time.Second):
			logger.Info("进程 %s 等待结束超时，继续清理", name)
		}
	}

	// 关闭管道（使用 defer 确保执行，忽略错误）
	if info.StdoutPipe != nil {
		info.StdoutPipe.Close()
	}
	if info.StderrPipe != nil {
		info.StderrPipe.Close()
	}

	// 等待日志收集协程完成（带超时）
	logDone := make(chan struct{})
	go func() {
		info.logWg.Wait()
		close(logDone)
	}()

	select {
	case <-logDone:
		// 日志收集协程已完成
	case <-time.After(1 * time.Second):
		logger.Info("等待日志收集协程超时")
	}

	// 安全关闭日志 channel
	info.logChanMu.Lock()
	if !info.logChanClosed {
		info.logChanClosed = true
		close(info.logChan)
	}
	info.logChanMu.Unlock()

	// 从管理器中移除
	pm.processes.Delete(name)

	logger.Info("进程 %s 资源已清理", name)

	return nil
}

// GetLogsInRange 获取指定时间段内的日志
func (pm *ProcessManager) GetLogsInRange(name string, startTime, endTime time.Time) string {
	val, ok := pm.processes.Load(name)
	if !ok {
		return ""
	}

	info := val.(*ProcessInfo)
	logs := info.LogBuffer.String()

	// 这里简化处理，返回所有日志
	// 实际应用中可以添加时间戳过滤
	return logs
}

// extractPortFromURL 从URL中提取端口号
func extractPortFromURL(healthCheckURL string) (int, error) {
	parsedURL, err := url.Parse(healthCheckURL)
	if err != nil {
		return 0, fmt.Errorf("解析URL失败: %w", err)
	}

	// 如果URL中没有指定端口，使用默认端口
	host := parsedURL.Host
	if strings.Contains(host, ":") {
		_, portStr, err := net.SplitHostPort(host)
		if err != nil {
			return 0, fmt.Errorf("分离主机和端口失败: %w", err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return 0, fmt.Errorf("端口号无效: %w", err)
		}
		return port, nil
	}

	// 使用默认端口
	switch parsedURL.Scheme {
	case "http":
		return 80, nil
	case "https":
		return 443, nil
	default:
		return 0, fmt.Errorf("不支持的协议: %s", parsedURL.Scheme)
	}
}

// isPortInUse 检查端口是否被占用
func isPortInUse(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err != nil {
		return false // 端口未被占用
	}
	conn.Close()
	return true // 端口被占用
}

// killProcessByPort 根据端口号杀掉占用该端口的进程
func killProcessByPort(port int) error {
	logger := GetLogger()

	// 先检查端口是否真的被占用
	if !isPortInUse(port) {
		return fmt.Errorf("端口 %d 未被占用", port)
	}

	// 使用 netstat 查找占用端口的进程，带超时
	// Windows: netstat -ano | findstr :PORT
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
		cmd = exec.CommandContext(ctx, "netstat", "-ano")
	} else {
		cmd = exec.CommandContext(ctx, "netstat", "-tulnp")
	}

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("执行netstat失败: %w", err)
	}

	// 解析netstat输出，查找占用端口的PID（只匹配 LISTENING 状态）
	lines := strings.Split(string(output), "\n")
	seenPIDs := make(map[int]bool) // 去重
	var pids []int

	for _, line := range lines {
		// 只匹配 LISTENING 状态的连接
		if !strings.Contains(line, "LISTENING") && !strings.Contains(line, "LISTEN") {
			continue
		}
		if strings.Contains(line, fmt.Sprintf(":%d", port)) {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				// Windows netstat格式：最后一列是PID
				// Linux netstat格式：最后一列是PID/进程名
				pidField := fields[len(fields)-1]
				pidStr := strings.Split(pidField, "/")[0] // 去掉进程名部分

				pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
				if err == nil && pid > 0 && !seenPIDs[pid] {
					seenPIDs[pid] = true
					pids = append(pids, pid)
				}
			}
		}
	}

	if len(pids) == 0 {
		return fmt.Errorf("未找到占用端口 %d 的进程", port)
	}

	// 杀掉找到的所有进程
	logger.Info("找到 %d 个占用端口 %d 的进程", len(pids), port)
	for _, pid := range pids {
		logger.Info("发现端口 %d 被进程 %d 占用，正在终止...", port, pid)

		// 使用超时上下文执行 kill 命令，避免卡死
		killCtx, killCancel := context.WithTimeout(context.Background(), 3*time.Second)

		var killErr error
		if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
			killErr = exec.CommandContext(killCtx, "taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
		} else {
			killErr = exec.CommandContext(killCtx, "kill", "-9", strconv.Itoa(pid)).Run()
		}
		killCancel() // 立即释放，不用 defer

		if killErr != nil {
			logger.Error("终止进程 %d 失败: %v", pid, killErr)
		} else {
			logger.Info("成功终止进程 %d", pid)
		}
	}

	// 等待端口释放
	time.Sleep(500 * time.Millisecond)
	return nil
}

// KillProcessByHealthCheckURL 根据健康检查URL杀掉占用端口的进程
// 只有在端口被占用时才会执行kill操作
func KillProcessByHealthCheckURL(healthCheckURL string) error {
	port, err := extractPortFromURL(healthCheckURL)
	if err != nil {
		return err
	}

	logger := GetLogger()

	// 先检查端口是否被占用
	if !isPortInUse(port) {
		logger.Info("端口 %d 未被占用，无需清理", port)
		return nil
	}

	logger.Info("端口 %d 被占用，尝试清理...", port)
	return killProcessByPort(port)
}

// waitForPortReady 等待端口就绪（使用TCP连接检查，无需HTTP）
func waitForPortReady(port int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待端口就绪超时")
		case <-ticker.C:
			// 尝试连接端口
			conn, err := net.DialTimeout("tcp", fmt.Sprintf(":%d", port), 100*time.Millisecond)
			if err == nil {
				conn.Close()
				return nil // 端口就绪
			}
		}
	}
}

// waitForPortReadyWithExitCheck 等待端口就绪，同时监控进程退出
func waitForPortReadyWithExitCheck(port int, timeout time.Duration, exitChan <-chan error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待端口就绪超时")
		case <-ticker.C:
			// 尝试连接端口
			conn, err := net.DialTimeout("tcp", fmt.Sprintf(":%d", port), 100*time.Millisecond)
			if err == nil {
				conn.Close()
				return nil // 端口就绪
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
