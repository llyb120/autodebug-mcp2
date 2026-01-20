---
name: http-request
description: |
  HTTP 请求技能，用于发起 HTTP 请求并获取进程日志。
  
  触发条件：
  - 当用户需要测试 API 接口时
  - 当用户需要调试 HTTP 服务时
  - 当用户需要发起 GET/POST/PUT/DELETE 等请求时
  
  功能：
  - 发起各种 HTTP 方法的请求
  - 支持自定义请求头和请求体
  - 可关联进程自动使用其地址
  - 返回响应内容和请求期间的进程日志
  
  限制：
  - 请求超时时间为 60 秒
  - 不支持文件上传
version: 1.0.0
allowed-tools:
  - python:execute
---

# HTTP Request Skill

## Overview

HTTP 请求技能用于在开发调试过程中发起 HTTP 请求，测试 API 接口。支持关联已启动的进程，自动使用进程的地址，并返回请求期间产生的进程日志，便于问题排查。

## Inputs

- `url` (必需): 请求 URL，可以是完整 URL 或路径
- `method` (可选): HTTP 方法，默认 GET。支持 GET/POST/PUT/DELETE/PATCH/HEAD/OPTIONS
- `headers` (可选): HTTP 请求头字典
- `body` (可选): 请求体内容
- `process_name` (可选): 进程名称，如果提供则使用该进程的 host 和 port 替换 URL 中的地址

## Outputs

- `status_code`: HTTP 状态码
- `duration_ms`: 请求耗时（毫秒）
- `response`: 响应内容
- `logs`: 请求期间的进程日志（如果关联了进程）
- `log_file`: 日志文件路径（如果响应过长）

## Constraints / Rules

1. 如果同时提供 `process_name` 和完整 URL，会使用进程的 host:port 替换 URL 中的地址
2. 如果只提供路径（如 `/api/users`），必须提供 `process_name` 或确保路径可以自动匹配到某个运行中的进程
3. 请求超时时间固定为 60 秒
4. 如果响应内容过长（超过 4000 字符），会保存到文件并返回文件路径

## Steps

1. 解析 URL 参数
2. 如果提供了 process_name，从进程信息中获取地址并替换 URL
3. 如果没有提供 process_name 但 URL 可以匹配到某个进程，自动关联
4. 标记请求开始时间（用于截取日志）
5. 构建 HTTP 请求并发送
6. 获取响应内容
7. 截取请求期间的进程日志
8. 如果内容过长，保存到文件
9. 返回结果

## Examples

### GET 请求
```
输入:
  url: "http://localhost:8080/api/users"
  method: "GET"

输出:
  status_code: 200
  duration_ms: 45
  response: "[{\"id\": 1, \"name\": \"Alice\"}]"
```

### POST 请求
```
输入:
  url: "/api/users"
  process_name: "my-server"
  method: "POST"
  headers: {"Content-Type": "application/json"}
  body: "{\"name\": \"Bob\", \"email\": \"bob@example.com\"}"

输出:
  status_code: 201
  duration_ms: 120
  response: "{\"id\": 2, \"name\": \"Bob\"}"
  logs: "[2026-01-20 10:00:00.123] [INFO] Creating user: Bob"
```

### 关联进程请求
```
输入:
  url: "/health"
  process_name: "api-server"

输出:
  status_code: 200
  duration_ms: 5
  response: "{\"status\": \"healthy\"}"
```

## Error Handling / Edge Cases

- 如果进程不存在，返回错误提示
- 如果请求超时，返回超时错误
- 如果 URL 解析失败，返回格式错误提示
- 如果连接被拒绝，返回连接错误和可能的原因

## Limitations

- 不支持文件上传（multipart/form-data）
- 不支持 WebSocket 连接
- 不支持 HTTP/2 特有功能
- 不支持客户端证书认证
- 不支持代理配置
