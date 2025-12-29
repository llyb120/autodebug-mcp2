package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ============ 复制自 process_manager.go 的核心代码 ============

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
	HealthCheckPort int

	logChan     chan string
	logLines    []string
	logTimes    []time.Time
	logMu       sync.RWMutex
	maxLogLines int
	logIndex    int
	ExitChan    chan error

	logWg         sync.WaitGroup
	logChanClosed bool
	logChanMu     sync.Mutex

	waitDone chan struct{}
	waitOnce sync.Once
}

var processManager = &ProcessManager{}

func log(format string, args ...interface{}) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func (pm *ProcessManager) GetProcess(name string) (*ProcessInfo, bool) {
	val, ok := pm.processes.Load(name)
	if !ok {
		return nil, false
	}
	return val.(*ProcessInfo), true
}

func extractPortFromURL(healthCheckURL string) (int, error) {
	parsedURL, err := url.Parse(healthCheckURL)
	if err != nil {
		return 0, fmt.Errorf("解析URL失败: %w", err)
	}

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

	switch parsedURL.Scheme {
	case "http":
		return 80, nil
	case "https":
		return 443, nil
	default:
		return 0, fmt.Errorf("不支持的协议: %s", parsedURL.Scheme)
	}
}

func isPortInUse(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func killProcessByPort(port int) error {
	log("killProcessByPort: 开始检查端口 %d", port)

	if !isPortInUse(port) {
		log("killProcessByPort: 端口 %d 未被占用", port)
		return fmt.Errorf("端口 %d 未被占用", port)
	}

	log("killProcessByPort: 端口 %d 被占用，执行 netstat", port)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "netstat", "-ano")
	output, err := cmd.Output()
	if err != nil {
		log("killProcessByPort: netstat 失败: %v", err)
		return fmt.Errorf("执行netstat失败: %w", err)
	}

	log("killProcessByPort: netstat 完成，解析输出")

	lines := strings.Split(string(output), "\n")
	seenPIDs := make(map[int]bool)
	var pids []int

	for _, line := range lines {
		if !strings.Contains(line, "LISTENING") && !strings.Contains(line, "LISTEN") {
			continue
		}
		if strings.Contains(line, fmt.Sprintf(":%d", port)) {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				pidField := fields[len(fields)-1]
				pidStr := strings.Split(pidField, "/")[0]
				pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
				if err == nil && pid > 0 && !seenPIDs[pid] {
					seenPIDs[pid] = true
					pids = append(pids, pid)
				}
			}
		}
	}

	if len(pids) == 0 {
		log("killProcessByPort: 未找到占用端口 %d 的进程", port)
		return fmt.Errorf("未找到占用端口 %d 的进程", port)
	}

	log("killProcessByPort: 找到 %d 个进程: %v", len(pids), pids)

	for _, pid := range pids {
		log("killProcessByPort: 终止进程 %d", pid)
		killCtx, killCancel := context.WithTimeout(context.Background(), 3*time.Second)
		killErr := exec.CommandContext(killCtx, "taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
		killCancel()
		if killErr != nil {
			log("killProcessByPort: 终止进程 %d 失败: %v", pid, killErr)
		} else {
			log("killProcessByPort: 成功终止进程 %d", pid)
		}
	}

	time.Sleep(500 * time.Millisecond)
	return nil
}

func setProcessGroupID(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x00000200,
		}
	}
}

func (pm *ProcessManager) StartProcess(name, command string, args []string, env map[string]string, workDir, healthCheckURL string, timeout time.Duration) (*ProcessInfo, error) {
	log("StartProcess: 开始启动进程 %s", name)

	// 如果有同名的旧进程，等待它完全清理
	if oldInfo, exists := pm.GetProcess(name); exists {
		log("StartProcess: 发现同名进程 %s，等待清理...", name)
		select {
		case <-oldInfo.waitDone:
			log("StartProcess: 旧进程 %s 已完成", name)
		case <-time.After(2 * time.Second):
			log("StartProcess: 等待旧进程 %s 超时，强制继续", name)
		}
		pm.processes.Delete(name)
	}

	port, err := extractPortFromURL(healthCheckURL)
	if err != nil {
		log("StartProcess: 从URL提取端口失败: %v", err)
		port = 0
	}
	log("StartProcess: 健康检查端口: %d", port)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, command, args...)
	setProcessGroupID(cmd)

	if workDir != "" {
		if filepath.IsAbs(workDir) {
			cmd.Dir = workDir
		} else {
			cwd, _ := os.Getwd()
			cmd.Dir = filepath.Join(cwd, workDir)
		}
	} else {
		cmd.Dir, _ = os.Getwd()
	}
	log("StartProcess: 工作目录: %s", cmd.Dir)

	// 设置环境变量
	if len(env) > 0 {
		currentEnv := os.Environ()
		envMap := make(map[string]string)
		for _, e := range currentEnv {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}
		for key, value := range env {
			envMap[key] = value
			log("StartProcess: 设置环境变量 %s=%s", key, value)
		}
		cmd.Env = make([]string, 0, len(envMap))
		for key, value := range envMap {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
	}

	log("StartProcess: 创建管道...")
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

	logBuffer := &bytes.Buffer{}
	logChan := make(chan string, 1000)
	maxLogLines := 1000

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
		ExitChan:        make(chan error, 1),
		logChanClosed:   false,
		waitDone:        make(chan struct{}),
	}

	log("StartProcess: 启动进程...")
	if err := cmd.Start(); err != nil {
		cancel()
		stdoutPipe.Close()
		stderrPipe.Close()
		close(logChan)
		return nil, fmt.Errorf("启动进程失败: %w", err)
	}

	pm.processes.Store(name, processInfo)
	log("StartProcess: 进程已启动 (PID: %d)", cmd.Process.Pid)

	processInfo.logWg.Add(2)
	go pm.collectStdout(processInfo)
	go pm.collectStderr(processInfo)
	go pm.processLogs(processInfo)
	go pm.monitorProcessExit(processInfo)

	return processInfo, nil
}

func (pm *ProcessManager) processLogs(info *ProcessInfo) {
	for line := range info.logChan {
		info.LogBuffer.WriteString(line + "\n")
		info.logMu.Lock()
		info.logLines[info.logIndex] = line
		info.logTimes[info.logIndex] = time.Now()
		info.logIndex = (info.logIndex + 1) % info.maxLogLines
		info.logMu.Unlock()
	}
}

func (pm *ProcessManager) collectStdout(info *ProcessInfo) {
	defer info.logWg.Done()
	scanner := bufio.NewReader(info.StdoutPipe)
	for {
		line, err := scanner.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			info.logChanMu.Lock()
			closed := info.logChanClosed
			info.logChanMu.Unlock()
			if closed {
				break
			}
			select {
			case info.logChan <- line:
			default:
			}
		}
	}
}

func (pm *ProcessManager) collectStderr(info *ProcessInfo) {
	defer info.logWg.Done()
	scanner := bufio.NewReader(info.StderrPipe)
	for {
		line, err := scanner.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			info.logChanMu.Lock()
			closed := info.logChanClosed
			info.logChanMu.Unlock()
			if closed {
				break
			}
			select {
			case info.logChan <- line:
			default:
			}
		}
	}
}

func (pm *ProcessManager) monitorProcessExit(info *ProcessInfo) {
	log("monitorProcessExit: 开始监控进程 %s", info.Name)

	err := info.Cmd.Wait()
	log("monitorProcessExit: Wait() 返回，err=%v", err)

	info.waitOnce.Do(func() {
		close(info.waitDone)
	})

	if info.StdoutPipe != nil {
		info.StdoutPipe.Close()
	}
	if info.StderrPipe != nil {
		info.StderrPipe.Close()
	}

	done := make(chan struct{})
	go func() {
		info.logWg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		log("monitorProcessExit: 等待日志收集协程超时")
	}

	info.logChanMu.Lock()
	if !info.logChanClosed {
		info.logChanClosed = true
		close(info.logChan)
	}
	info.logChanMu.Unlock()

	if err != nil {
		log("monitorProcessExit: 进程 %s 异常退出: %v", info.Name, err)
		select {
		case info.ExitChan <- err:
		default:
		}
	} else {
		log("monitorProcessExit: 进程 %s 正常退出", info.Name)
		select {
		case info.ExitChan <- nil:
		default:
		}
	}
}

func (pm *ProcessManager) KillProcess(name string) error {
	log("KillProcess: 开始终止进程 %s", name)

	val, ok := pm.processes.Load(name)
	if !ok {
		return fmt.Errorf("进程不存在: %s", name)
	}

	info := val.(*ProcessInfo)
	pid := info.Cmd.Process.Pid
	log("KillProcess: PID=%d, HealthCheckPort=%d", pid, info.HealthCheckPort)

	info.Cancel()

	if info.Cmd.Process != nil {
		log("KillProcess: 执行 taskkill...")
		killCmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
		output, err := killCmd.CombinedOutput()
		if err != nil {
			log("KillProcess: taskkill 失败: %v, 输出: %s", err, string(output))
			if info.HealthCheckPort > 0 {
				log("KillProcess: 尝试通过端口 %d 终止...", info.HealthCheckPort)
				killProcessByPort(info.HealthCheckPort)
			}
		} else {
			log("KillProcess: taskkill 成功")
		}

		log("KillProcess: 等待 waitDone...")
		select {
		case <-info.waitDone:
			log("KillProcess: 进程已结束")
		case <-time.After(3 * time.Second):
			log("KillProcess: 等待超时")
		}
	}

	if info.StdoutPipe != nil {
		info.StdoutPipe.Close()
	}
	if info.StderrPipe != nil {
		info.StderrPipe.Close()
	}

	logDone := make(chan struct{})
	go func() {
		info.logWg.Wait()
		close(logDone)
	}()

	select {
	case <-logDone:
	case <-time.After(1 * time.Second):
		log("KillProcess: 等待日志收集协程超时")
	}

	info.logChanMu.Lock()
	if !info.logChanClosed {
		info.logChanClosed = true
		close(info.logChan)
	}
	info.logChanMu.Unlock()

	pm.processes.Delete(name)
	log("KillProcess: 进程 %s 资源已清理", name)

	return nil
}

func waitForPortReadyWithExitCheck(port int, timeout time.Duration, exitChan <-chan error) error {
	log("waitForPortReady: 等待端口 %d 就绪...", port)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log("waitForPortReady: 超时")
			return fmt.Errorf("等待端口就绪超时")
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", fmt.Sprintf(":%d", port), 100*time.Millisecond)
			if err == nil {
				conn.Close()
				log("waitForPortReady: 端口 %d 已就绪", port)
				return nil
			}
		case exitErr := <-exitChan:
			log("waitForPortReady: 进程退出，err=%v", exitErr)
			if exitErr != nil {
				return fmt.Errorf("进程异常退出: %v", exitErr)
			}
			return fmt.Errorf("进程已退出")
		}
	}
}

// ============ 测试主函数 ============

func main() {
	log("=== 开始测试 ===")

	// 切换到项目目录
	workDir := "D:\\project\\partner-ogdb-backend-intelligence-pc-backend-master"
	if err := os.Chdir(workDir); err != nil {
		log("切换目录失败: %v", err)
		os.Exit(1)
	}
	log("工作目录: %s", workDir)

	processName := "intelligence-pc-backend"
	command := "go"
	args := []string{"run", "."}
	env := map[string]string{
		"QB_DEV":           "1",
		"QB_IGNORE_DEVLOG": "1",
		"QB_PROFILE":       "bin2",
	}
	healthCheckURL := "http://localhost:27028/healthz"
	timeout := 60 * time.Second

	// 第一轮
	log("\n========== 第一轮测试 ==========")

	log("\n--- 步骤1: 清理端口 ---")
	if err := killProcessByPort(27028); err != nil {
		log("端口清理: %v", err)
	}

	log("\n--- 步骤2: 启动进程 ---")
	processInfo, err := processManager.StartProcess(processName, command, args, env, "", healthCheckURL, timeout)
	if err != nil {
		log("启动失败: %v", err)
		os.Exit(1)
	}

	log("\n--- 步骤3: 等待健康检查 ---")
	if err := waitForPortReadyWithExitCheck(processInfo.HealthCheckPort, timeout, processInfo.ExitChan); err != nil {
		log("健康检查失败: %v", err)
		processManager.KillProcess(processName)
		os.Exit(1)
	}

	log("\n--- 步骤4: 发送HTTP请求 ---")
	resp, err := http.Get("http://localhost:27028/healthz")
	if err != nil {
		log("HTTP请求失败: %v", err)
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log("HTTP响应: %d, body=%s", resp.StatusCode, string(body))
	}

	log("\n--- 步骤5: 终止进程 ---")
	if err := processManager.KillProcess(processName); err != nil {
		log("终止失败: %v", err)
	}

	log("\n等待2秒...")
	time.Sleep(2 * time.Second)

	// 第二轮
	log("\n========== 第二轮测试 ==========")

	log("\n--- 步骤1: 启动进程 ---")
	startTime := time.Now()
	processInfo, err = processManager.StartProcess(processName, command, args, env, "", healthCheckURL, timeout)
	if err != nil {
		log("第二轮启动失败: %v", err)
		os.Exit(1)
	}
	log("启动耗时: %v", time.Since(startTime))

	log("\n--- 步骤2: 等待健康检查 ---")
	if err := waitForPortReadyWithExitCheck(processInfo.HealthCheckPort, timeout, processInfo.ExitChan); err != nil {
		log("健康检查失败: %v", err)
		processManager.KillProcess(processName)
		os.Exit(1)
	}

	log("\n--- 步骤3: 发送HTTP请求 ---")
	resp, err = http.Get("http://localhost:27028/healthz")
	if err != nil {
		log("HTTP请求失败: %v", err)
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log("HTTP响应: %d, body=%s", resp.StatusCode, string(body))
	}

	log("\n--- 步骤4: 终止进程 ---")
	if err := processManager.KillProcess(processName); err != nil {
		log("终止失败: %v", err)
	}

	log("\n=== 测试完成 ===")
}
