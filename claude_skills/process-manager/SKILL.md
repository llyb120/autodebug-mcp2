---
name: process-manager
description: |
  进程管理技能，用于启动、监控和终止进程。
  
  触发条件：
  - 当用户需要启动开发服务器、后端服务或任何需要持续运行的进程时
  - 当用户需要终止正在运行的进程时
  - 当用户需要通过端口号杀掉占用端口的进程时
  
  功能：
  - 启动进程并持续收集日志
  - 通过健康检查URL确认进程启动成功
  - 支持设置环境变量和工作目录
  - 通过进程名称或端口号终止进程
  
  限制：
  - 不能管理系统进程
  - 健康检查需要HTTP接口
version: 1.0.0
allowed-tools:
  - python:execute
  - bash:execute
---

# Process Manager Skill

## Overview

进程管理技能用于在开发调试过程中启动、监控和终止进程。支持启动开发服务器并通过健康检查确认启动成功，同时持续收集进程输出日志用于问题排查。

## Inputs

### 启动进程 (start_process)
- `name` (必需): 进程名称，用于后续操作该进程的标识符
- `command` (必需): 要执行的命令，应该是可执行文件名（如 'python', 'node', 'go'）
- `args` (可选): 命令参数列表，如 ['run', '.'] 或 ['-m', 'http.server', '8080']
- `work_dir` (可选): 工作目录路径
- `env` (可选): 环境变量字典
- `health_check_url` (必需): 健康检查URL，返回2xx状态码视为启动成功
- `health_check_method` (可选): 健康检查HTTP方法，默认GET
- `timeout_seconds` (可选): 启动超时时间，默认60秒

### 终止进程 (kill_process)
- `name` (可选): 进程名称，优先匹配本技能启动的进程
- `port` (可选): 端口号，终止占用该端口的进程

## Outputs

### 启动进程
- 成功: 返回进程PID、启动时间、工作目录、健康检查状态和启动日志
- 失败: 返回错误信息和已收集的日志

### 终止进程
- 成功: 返回已终止进程的名称和PID
- 失败: 返回错误信息

## Constraints / Rules

1. `command` 参数必须是纯可执行文件名，不能包含空格或命令参数
2. 命令参数应放在 `args` 列表中
3. 启动进程前会自动清理同名的旧进程
4. 健康检查URL必须返回2xx状态码才视为启动成功
5. 终止进程时必须提供 `name` 或 `port` 中的至少一个

## Steps

### 启动进程
1. 验证参数，检查 command 是否包含空格（如有则报错）
2. 检查是否有同名进程运行，如有则先终止
3. 检查健康检查URL的端口是否被占用，如有则尝试清理
4. 启动进程并开始收集输出日志
5. 定期轮询健康检查URL，等待返回2xx状态码
6. 返回启动结果和收集的日志

### 终止进程
1. 如果提供了 name，查找并终止本技能启动的同名进程
2. 如果提供了 port，查找并终止占用该端口的进程
3. 返回操作结果

## Examples

### 启动 Python HTTP 服务器
```
输入:
  name: "my-server"
  command: "python"
  args: ["-m", "http.server", "8080"]
  work_dir: "/path/to/project"
  health_check_url: "http://localhost:8080/"
  timeout_seconds: 30

输出:
  进程已成功启动
  PID: 12345
  启动时间: 2026-01-20T10:00:00Z
  工作目录: /path/to/project
  健康检查: http://localhost:8080/
```

### 启动 Go 项目
```
输入:
  name: "go-api"
  command: "go"
  args: ["run", "."]
  work_dir: "/path/to/go-project"
  env: {"PORT": "3000", "DEBUG": "true"}
  health_check_url: "http://localhost:3000/health"

输出:
  进程已成功启动
  PID: 12346
  ...
```

### 终止进程
```
输入:
  name: "my-server"

输出:
  成功终止进程
  进程名称: my-server
  PID: 12345
```

### 通过端口终止
```
输入:
  port: 8080

输出:
  成功终止占用端口 8080 的进程
```

## Error Handling / Edge Cases

- 如果 command 包含空格，返回错误提示用户正确拆分命令和参数
- 如果进程启动后健康检查超时，返回已收集的日志帮助排查问题
- 如果进程在启动过程中意外退出，立即返回错误和日志
- 如果指定的进程名不存在，提示用户使用端口方式终止
- 如果端口未被占用，返回相应提示

## Limitations

- 不支持交互式命令（需要用户输入的程序）
- 健康检查仅支持HTTP协议
- 进程日志是内存中收集的，重启技能后会丢失
- 不支持Windows服务和系统级进程管理
