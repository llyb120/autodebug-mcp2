# Claude Skills - 开发调试工具集

这是一套 Claude AI Skills，将原 Go MCP 的功能改造为 Claude Skills 格式，使用 Python 脚本实现。

## 技能列表

| 技能 | 描述 | 主要功能 |
|------|------|----------|
| [process-manager](./process-manager/) | 进程管理 | 启动进程、健康检查、日志收集、终止进程 |
| [http-request](./http-request/) | HTTP 请求 | 发起请求、获取响应、关联进程日志 |
| [memory-manager](./memory-manager/) | 记忆管理 | 保存/读取 AI 对话状态和系统提示词 |
| [knowledge-manager](./knowledge-manager/) | 知识库管理 | 保存/检索可复用的知识 |

## 目录结构

```
claude_skills/
├── README.md                          # 本文件
├── process-manager/
│   ├── SKILL.md                       # 技能规范
│   └── scripts/
│       └── process_manager.py         # Python 实现
├── http-request/
│   ├── SKILL.md
│   └── scripts/
│       └── http_request.py
├── memory-manager/
│   ├── SKILL.md
│   └── scripts/
│       └── memory_manager.py
└── knowledge-manager/
    ├── SKILL.md
    └── scripts/
        └── knowledge_manager.py
```

## 使用方法

### 1. 进程管理 (process-manager)

#### 启动进程

```bash
python scripts/process_manager.py start \
  --name my-server \
  --command python \
  --args -m http.server 8080 \
  --work-dir /path/to/project \
  --health-check-url http://localhost:8080/ \
  --timeout 30
```

#### 终止进程

```bash
# 通过名称
python scripts/process_manager.py kill --name my-server

# 通过端口
python scripts/process_manager.py kill --port 8080
```

#### 获取日志

```bash
python scripts/process_manager.py logs --name my-server
```

### 2. HTTP 请求 (http-request)

```bash
# GET 请求
python scripts/http_request.py \
  --url http://localhost:8080/api/users \
  --method GET

# POST 请求
python scripts/http_request.py \
  --url http://localhost:8080/api/users \
  --method POST \
  --headers '{"Content-Type": "application/json"}' \
  --body '{"name": "Alice"}'

# 关联进程
python scripts/http_request.py \
  --url /api/users \
  --process-name my-server
```

### 3. 记忆管理 (memory-manager)

#### 保存记忆

```bash
python scripts/memory_manager.py save \
  --system-prompt "你是一个代码助手..." \
  --content "## 当前任务\n- 调试登录接口"
```

#### 读取记忆

```bash
python scripts/memory_manager.py read --memory-id <UUID>
```

#### 列出所有记忆

```bash
python scripts/memory_manager.py list
```

#### 更新记忆（推荐追加）

```bash
python scripts/memory_manager.py save \
  --memory-id <UUID> \
  --update-mode append \
  --system-prompt "你是一个代码助手..." \
  --content "## 新进度\n- 已完成修复"
```

### 4. 知识库管理 (knowledge-manager)

#### 保存知识

```bash
python scripts/knowledge_manager.py save \
  --title "Go 错误处理规范" \
  --content "## 规范说明\n..." \
  --work-dir /path/to/project \
  --tags go error-handling \
  --category "代码规范"
```

#### 检索知识

```bash
# 关键词搜索
python scripts/knowledge_manager.py search \
  --work-dir /path/to/project \
  --query "error handling"

# 按标签搜索
python scripts/knowledge_manager.py search \
  --work-dir /path/to/project \
  --tags go

# 按分类搜索
python scripts/knowledge_manager.py search \
  --work-dir /path/to/project \
  --category "代码规范"
```

## SKILL.md 格式说明

每个技能的 SKILL.md 文件遵循 Claude Skills 标准格式：

```markdown
---
name: skill-name              # 技能名称（小写，连字符）
description: |                # 技能描述
  触发条件、功能、限制
version: x.y.z               # 版本号
allowed-tools:               # 允许使用的工具
  - python:execute
---

# 技能名称

## Overview
技能概述

## Inputs
输入参数说明

## Outputs
输出结果说明

## Constraints / Rules
约束和规则

## Steps
执行步骤

## Examples
使用示例

## Error Handling / Edge Cases
错误处理

## Limitations
限制说明
```

## 与原 Go MCP 功能对比

| 原 MCP 工具 | Claude Skill | 状态 |
|-------------|--------------|------|
| start_process | process-manager | ✅ |
| kill_process | process-manager | ✅ |
| request_with_logs | http-request | ✅ |
| save_memory | memory-manager | ✅ |
| read_memory | memory-manager | ✅ |
| save_knowledge | knowledge-manager | ✅ |
| search_knowledge | knowledge-manager | ✅ |

## 数据存储

- **进程日志**: `./logs/` 目录
- **记忆文件**: `./mems/` 目录
- **知识库**: `<work_dir>/.knowledge/` 目录

## 依赖

- Python 3.8+
- 标准库（无需额外安装）

## 注意事项

1. **进程管理**: 脚本需要保持运行才能管理进程，重启脚本后进程信息会丢失
2. **记忆更新**: 更新记忆时务必先读取再修改，避免覆盖丢失
3. **知识库**: 知识库是项目级别的，存储在工作目录的 `.knowledge` 文件夹

## License

MIT
