package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Logger struct {
	mu       sync.Mutex
	file     *os.File
	filePath string
}

var globalLogger *Logger
var loggerOnce sync.Once

// InitLogger 初始化日志文件
func InitLogger() error {
	var initErr error
	loggerOnce.Do(func() {
		// 获取可执行文件所在目录
		execPath, err := os.Executable()
		if err != nil {
			initErr = fmt.Errorf("获取可执行文件路径失败: %w", err)
			return
		}

		// 获取可执行文件所在目录
		execDir := filepath.Dir(execPath)

		// 创建 logs 目录
		logsDir := filepath.Join(execDir, "logs")
		if err := os.MkdirAll(logsDir, 0755); err != nil {
			initErr = fmt.Errorf("创建logs目录失败: %w", err)
			return
		}

		// 在 logs 目录中创建日志文件
		logFileName := fmt.Sprintf("gomcp_%s.log", time.Now().Format("20060102_150405"))
		logPath := filepath.Join(logsDir, logFileName)

		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			initErr = fmt.Errorf("创建日志文件失败: %w", err)
			return
		}

		globalLogger = &Logger{
			file:     file,
			filePath: logPath,
		}

		// 写入日志文件头部
		globalLogger.file.WriteString(fmt.Sprintf("=== GoMCP Log Started at %s ===\n", time.Now().Format(time.RFC3339)))
		globalLogger.file.WriteString(fmt.Sprintf("可执行文件: %s\n", execPath))
		globalLogger.file.WriteString(fmt.Sprintf("日志目录: %s\n\n", logsDir))
	})

	return initErr
}

// GetLogger 获取日志实例
func GetLogger() *Logger {
	if globalLogger == nil {
		InitLogger()
	}
	return globalLogger
}

// Close 关闭日志文件
func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.file.WriteString(fmt.Sprintf("\n=== GoMCP Log Ended at %s ===\n", time.Now().Format(time.RFC3339)))
	return l.file.Close()
}

// Info 写入信息日志
func (l *Logger) Info(format string, args ...interface{}) {
	message := fmt.Sprintf("[%s] [INFO] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))

	// 只输出到文件，不输出到 stderr（避免阻塞 MCP 通信）
	if l != nil && l.file != nil {
		l.mu.Lock()
		l.file.WriteString(message)
		l.file.Sync() // 立即刷新到磁盘
		l.mu.Unlock()
	}
}

// Error 写入错误日志
func (l *Logger) Error(format string, args ...interface{}) {
	message := fmt.Sprintf("[%s] [ERROR] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))

	// 只输出到文件
	if l != nil && l.file != nil {
		l.mu.Lock()
		l.file.WriteString(message)
		l.file.Sync()
		l.mu.Unlock()
	}
}

// Debug 写入调试日志
func (l *Logger) Debug(format string, args ...interface{}) {
	message := fmt.Sprintf("[%s] [DEBUG] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))

	// 只输出到文件
	if l != nil && l.file != nil {
		l.mu.Lock()
		l.file.WriteString(message)
		l.file.Sync()
		l.mu.Unlock()
	}
}

// ProcessLog 写入进程日志
func (l *Logger) ProcessLog(processName string, line string) {
	message := fmt.Sprintf("[%s] [PROCESS:%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), processName, line)

	// 输出到文件
	if l != nil && l.file != nil {
		l.mu.Lock()
		l.file.WriteString(message)
		l.mu.Unlock()
	}
}

// GetLogPath 获取日志文件路径
func (l *Logger) GetLogPath() string {
	if l != nil {
		return l.filePath
	}
	return ""
}
