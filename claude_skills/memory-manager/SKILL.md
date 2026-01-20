---
name: memory-manager
description: |
  记忆管理技能，用于保存和恢复 AI 对话状态。
  
  触发条件：
  - 当对话内容较长，需要保存当前进度时
  - 当上下文可能被截断，需要持久化状态时
  - 当需要恢复之前保存的工作状态时
  - 当用户明确要求保存或加载记忆时
  
  功能：
  - 保存系统提示词和任务记忆到文件
  - 通过记忆ID恢复完整工作状态
  - 支持增量更新记忆内容
  
  限制：
  - 记忆文件保存在本地，不支持云同步
  - 更新记忆时必须先读取再修改，否则会覆盖
version: 1.0.0
allowed-tools:
  - python:execute
---

# Memory Manager Skill

## Overview

记忆管理技能用于在长对话中保存和恢复工作状态。当对话内容较长、上下文可能被截断时，可以将当前的系统提示词和任务记忆保存到文件，通过记忆ID可以随时恢复完整的工作状态。

## Inputs

### 保存记忆 (save_memory)
- `system_prompt` (必需): 完整的系统提示词内容
- `content` (必需): 记忆内容，包括当前任务、调试进度、关键发现、待办事项等
- `memory_id` (可选): 记忆ID，如果提供则更新现有记忆，否则创建新记忆
- `update_mode` (可选): 更新模式，`append` 或 `replace`，默认 `append`

### 读取记忆 (read_memory)
- `memory_id` (必需): 要读取的记忆ID

## Outputs

### 保存记忆
- `memory_id`: 记忆的唯一标识符
- `file_path`: 记忆文件的完整路径
- `content_length`: 记忆内容长度
- `prompt_length`: 系统提示词长度

### 读取记忆
- `memory_id`: 记忆ID
- `file_path`: 文件路径
- `content`: 完整的记忆文件内容（包含系统提示词和任务记忆）

## Constraints / Rules

1. 保存记忆时必须提供完整的系统提示词
2. 更新记忆推荐先调用 read_memory 读取现有内容再修改保存
3. 若未进行读-改-写，必须使用 `update_mode=append`，避免覆盖丢失历史
4. 记忆文件以 Markdown 格式保存，便于人工查看
5. 记忆ID是 UUID 格式的唯一标识符

## Steps

### 保存记忆
1. 验证 system_prompt 和 content 参数不为空
2. 如果提供了 memory_id，则为更新模式，否则生成新的 UUID
3. 确保 mems 目录存在
4. 根据 update_mode 选择追加或替换
5. 构建包含元信息、系统提示词和任务记忆的 Markdown 内容
5. 写入文件
6. 返回记忆ID和文件路径

### 读取记忆
1. 验证 memory_id 参数
2. 构建文件路径
3. 检查文件是否存在
4. 读取并返回文件内容

## Examples

### 保存新记忆
```
输入:
  system_prompt: "你是一个代码调试助手..."
  content: |
    ## 当前任务
    - 调试用户登录接口返回500错误
    
    ## 进度
    - 已定位到 auth_service.py 第 45 行
    - 发现数据库连接超时问题
    
    ## 待办
    - [ ] 检查数据库连接池配置
    - [ ] 添加重试逻辑

输出:
  memory_id: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
  file_path: "/path/to/mems/a1b2c3d4-e5f6-7890-abcd-ef1234567890.md"
  content_length: 256
  prompt_length: 128
```

### 更新现有记忆
```
# 第一步：读取现有记忆
输入:
  memory_id: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

输出:
  (现有记忆内容)

# 第二步：在读取内容基础上修改后保存
输入:
  memory_id: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
  system_prompt: "你是一个代码调试助手..."
  update_mode: "append"
  content: |
    ## 当前任务
    - 调试用户登录接口返回500错误
    
    ## 进度
    - 已定位到 auth_service.py 第 45 行
    - 发现数据库连接超时问题
    - ✅ 已修复连接池配置
    
    ## 待办
    - [x] 检查数据库连接池配置
    - [ ] 添加重试逻辑
    - [ ] 测试验证

输出:
  memory_id: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
  (已更新)
```

### 读取记忆
```
输入:
  memory_id: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

输出:
  memory_id: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
  file_path: "/path/to/mems/a1b2c3d4-e5f6-7890-abcd-ef1234567890.md"
  content: |
    # 记忆文件
    
    **记忆ID**: `a1b2c3d4-e5f6-7890-abcd-ef1234567890`
    ...
```

## Error Handling / Edge Cases

- 如果 system_prompt 或 content 为空，返回参数错误
- 如果 memory_id 对应的文件不存在，返回文件不存在错误
- 如果更新时新内容比旧内容短很多，返回警告提示可能丢失了信息
- 如果更新模式为 replace，且内容明显变短，返回额外提示
- 如果无法创建 mems 目录，返回权限错误

## Limitations

- 记忆文件仅保存在本地，不支持云同步
- 不支持记忆内容的加密
- 不支持多用户隔离
- 系统提示词会完整保存，可能占用较多存储空间
- 不支持记忆的版本控制和历史记录
