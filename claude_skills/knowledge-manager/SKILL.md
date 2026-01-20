---
name: knowledge-manager
description: |
  知识库管理技能，用于积累和检索可复用的知识。
  
  触发条件：
  - 当发现值得记录的代码规范、最佳实践时
  - 当解决了一个典型问题需要保存解决方案时
  - 当需要记录 API 文档或技术细节时
  - 当需要查找之前保存的知识时
  
  功能：
  - 保存知识到项目知识库（.knowledge 目录）
  - 支持标签和分类便于组织
  - 支持关键词搜索和标签过滤
  - 支持更新现有知识
  
  限制：
  - 知识库保存在工作目录下，项目相关
  - 仅支持文本内容
version: 1.0.0
allowed-tools:
  - python:execute
---

# Knowledge Manager Skill

## Overview

知识库管理技能用于在项目开发过程中积累和检索可复用的知识。支持保存代码规范、问题解决方案、API文档、最佳实践等，通过标签和分类组织知识，便于后续快速检索。

## Inputs

### 保存知识 (save_knowledge)
- `title` (必需): 知识标题，简短描述主题
- `content` (必需): 知识内容，详细描述
- `work_dir` (必需): 工作目录，知识库将保存在该目录下的 .knowledge 文件夹
- `tags` (可选): 标签列表，用于分类和检索
- `category` (可选): 分类，如"代码规范"、"API文档"、"问题解决"、"最佳实践"
- `knowledge_id` (可选): 知识ID，如果提供则更新现有知识

### 检索知识 (search_knowledge)
- `work_dir` (必需): 工作目录
- `query` (可选): 搜索关键词，在标题和内容中搜索
- `tags` (可选): 按标签过滤
- `category` (可选): 按分类过滤
- `limit` (可选): 返回结果数量限制，默认10

## Outputs

### 保存知识
- `knowledge_id`: 知识的唯一标识符
- `title`: 知识标题
- `category`: 分类
- `tags`: 标签
- `file_path`: 知识文件路径

### 检索知识
- `count`: 找到的知识条目数量
- `results`: 知识列表，每条包含 ID、标题、分类、标签、预览

## Constraints / Rules

1. work_dir 参数必须提供，知识库相对于项目存储
2. 知识以 Markdown 格式保存在 `.knowledge` 目录下
3. 每条知识有唯一的 UUID 标识符
4. 搜索时关键词不区分大小写
5. 更新知识时必须提供正确的 knowledge_id

## Steps

### 保存知识
1. 验证必需参数（title, content, work_dir）
2. 确定知识ID（新建或更新）
3. 确保 .knowledge 目录存在
4. 构建包含元信息和内容的 Markdown 文件
5. 写入文件
6. 返回知识ID和文件路径

### 检索知识
1. 验证 work_dir 参数
2. 读取 .knowledge 目录下所有 .md 文件
3. 解析每个文件的元信息和内容
4. 应用搜索条件过滤
5. 返回匹配的知识列表

## Examples

### 保存代码规范
```
输入:
  title: "Go 错误处理规范"
  content: |
    ## 规范说明
    
    1. 错误应该总是被处理，不能忽略
    2. 使用 errors.Wrap 添加上下文
    3. 在日志中记录完整错误栈
    
    ## 示例
    
    ```go
    if err != nil {
        return errors.Wrap(err, "failed to process request")
    }
    ```
  work_dir: "/path/to/project"
  tags: ["go", "error-handling", "best-practice"]
  category: "代码规范"

输出:
  knowledge_id: "abc123-..."
  title: "Go 错误处理规范"
  category: "代码规范"
  tags: "go, error-handling, best-practice"
  file_path: "/path/to/project/.knowledge/abc123-....md"
```

### 保存问题解决方案
```
输入:
  title: "MySQL 连接池超时问题解决"
  content: |
    ## 问题描述
    生产环境出现 "too many connections" 错误
    
    ## 原因分析
    连接池配置不当，空闲连接未及时释放
    
    ## 解决方案
    1. 设置 MaxIdleConns = 10
    2. 设置 MaxOpenConns = 100
    3. 设置 ConnMaxLifetime = 5 * time.Minute
  work_dir: "/path/to/project"
  tags: ["mysql", "connection-pool", "production"]
  category: "问题解决"

输出:
  (保存成功)
```

### 搜索知识
```
输入:
  work_dir: "/path/to/project"
  query: "connection pool"
  category: "问题解决"

输出:
  count: 1
  results:
    - knowledge_id: "xyz789-..."
      title: "MySQL 连接池超时问题解决"
      category: "问题解决"
      tags: "mysql, connection-pool, production"
      preview: "问题描述 生产环境出现..."
```

### 按标签搜索
```
输入:
  work_dir: "/path/to/project"
  tags: ["go"]

输出:
  count: 2
  results:
    - title: "Go 错误处理规范"
      ...
    - title: "Go 并发模式"
      ...
```

## Error Handling / Edge Cases

- 如果 work_dir 为空，返回参数错误
- 如果 .knowledge 目录不存在，搜索时返回空结果
- 如果 knowledge_id 不存在（更新时），返回知识不存在错误
- 如果无法创建目录或写入文件，返回权限错误

## Limitations

- 知识库是项目级别的，不支持跨项目共享
- 仅支持纯文本内容，不支持图片或附件
- 不支持知识的版本控制
- 搜索是简单的字符串匹配，不支持语义搜索
- 不支持知识的导入导出
