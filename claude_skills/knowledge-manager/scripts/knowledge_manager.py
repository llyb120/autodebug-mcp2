#!/usr/bin/env python3
"""
知识库管理脚本 - 保存和检索可复用的知识

功能:
- save_knowledge: 保存知识到项目知识库
- search_knowledge: 检索知识库
"""

import os
import sys
import json
import uuid
import argparse
from datetime import datetime
from pathlib import Path
from typing import Optional, Dict, List, Any


def get_knowledge_dir(work_dir: str) -> Path:
    """获取知识库目录"""
    return Path(work_dir) / ".knowledge"


def save_knowledge(
    title: str,
    content: str,
    work_dir: str,
    tags: Optional[List[str]] = None,
    category: Optional[str] = None,
    knowledge_id: Optional[str] = None
) -> Dict[str, Any]:
    """
    保存知识到知识库
    
    Args:
        title: 知识标题
        content: 知识内容
        work_dir: 工作目录
        tags: 标签列表
        category: 分类
        knowledge_id: 知识ID（更新时使用）
        
    Returns:
        包含保存结果的字典
    """
    # 参数验证
    if not title:
        return {
            "success": False,
            "error": "参数错误：title 不能为空"
        }
    
    if not content:
        return {
            "success": False,
            "error": "参数错误：content 不能为空"
        }
    
    if not work_dir:
        return {
            "success": False,
            "error": "参数错误：work_dir 不能为空，请提供工作目录路径"
        }
    
    knowledge_dir = get_knowledge_dir(work_dir)
    
    # 确保目录存在
    try:
        knowledge_dir.mkdir(parents=True, exist_ok=True)
    except Exception as e:
        return {
            "success": False,
            "error": f"创建知识库目录失败: {str(e)}"
        }
    
    # 确定知识ID
    is_update = False
    created_time = datetime.now().isoformat()
    
    if knowledge_id:
        # 更新模式
        file_path = knowledge_dir / f"{knowledge_id}.md"
        if not file_path.exists():
            return {
                "success": False,
                "error": f"知识不存在: {knowledge_id}，请检查 knowledge_id 是否正确"
            }
        
        # 读取原文件获取创建时间
        try:
            old_content = file_path.read_text(encoding='utf-8')
            marker = "**创建时间**: "
            if marker in old_content:
                start = old_content.index(marker) + len(marker)
                end = old_content.index("\n", start)
                created_time = old_content[start:end].strip()
        except:
            pass
        
        is_update = True
    else:
        # 新建
        knowledge_id = str(uuid.uuid4())
        file_path = knowledge_dir / f"{knowledge_id}.md"
    
    # 设置默认分类
    if not category:
        category = "通用"
    
    # 构建标签字符串
    tags_str = ", ".join(tags) if tags else ""
    
    # 构建文件内容
    lines = [
        f"# {title}",
        "",
        f"**知识ID**: `{knowledge_id}`",
        "",
        f"**创建时间**: {created_time}",
        ""
    ]
    
    if is_update:
        lines.extend([
            f"**更新时间**: {datetime.now().isoformat()}",
            ""
        ])
    
    lines.extend([
        f"**分类**: {category}",
        ""
    ])
    
    if tags_str:
        lines.extend([
            f"**标签**: {tags_str}",
            ""
        ])
    
    lines.extend([
        "---",
        "",
        "## 内容",
        "",
        content
    ])
    
    file_content = "\n".join(lines)
    
    # 写入文件
    try:
        file_path.write_text(file_content, encoding='utf-8')
    except Exception as e:
        return {
            "success": False,
            "error": f"保存知识失败: {str(e)}"
        }
    
    action_text = "✅ 知识已更新" if is_update else "✅ 知识已保存"
    
    return {
        "success": True,
        "knowledge_id": knowledge_id,
        "title": title,
        "category": category,
        "tags": tags_str,
        "file_path": str(file_path),
        "is_update": is_update,
        "message": f"{action_text}\n\n"
                   f"**知识ID**: `{knowledge_id}`\n"
                   f"**标题**: {title}\n"
                   f"**分类**: {category}\n"
                   f"**标签**: {tags_str}\n"
                   f"**文件路径**: `{file_path}`\n\n"
                   f"💡 使用 `search_knowledge` 工具可以检索知识库。如需更新此知识，请在下次调用时传入 knowledge_id。"
    }


def search_knowledge(
    work_dir: str,
    query: Optional[str] = None,
    tags: Optional[List[str]] = None,
    category: Optional[str] = None,
    limit: int = 10
) -> Dict[str, Any]:
    """
    检索知识库
    
    Args:
        work_dir: 工作目录
        query: 搜索关键词
        tags: 标签过滤
        category: 分类过滤
        limit: 返回数量限制
        
    Returns:
        包含搜索结果的字典
    """
    if not work_dir:
        return {
            "success": False,
            "error": "参数错误：work_dir 不能为空，请提供工作目录路径"
        }
    
    knowledge_dir = get_knowledge_dir(work_dir)
    
    # 检查目录是否存在
    if not knowledge_dir.exists():
        return {
            "success": True,
            "count": 0,
            "results": [],
            "message": "知识库为空，尚未保存任何知识。使用 `save_knowledge` 工具添加知识。"
        }
    
    # 读取所有知识文件
    results = []
    query_lower = query.lower() if query else ""
    category_lower = category.lower() if category else ""
    tags_lower = [t.lower() for t in tags] if tags else []
    
    for file_path in knowledge_dir.glob("*.md"):
        try:
            content = file_path.read_text(encoding='utf-8')
            content_lower = content.lower()
            
            # 解析知识文件
            knowledge_id = file_path.stem
            title = ""
            file_category = ""
            file_tags = ""
            preview = ""
            
            # 提取标题
            lines = content.split("\n")
            if lines and lines[0].startswith("# "):
                title = lines[0][2:].strip()
            
            # 提取知识ID
            marker = "**知识ID**: `"
            if marker in content:
                start = content.index(marker) + len(marker)
                end = content.index("`", start)
                knowledge_id = content[start:end]
            
            # 提取分类
            marker = "**分类**: "
            if marker in content:
                start = content.index(marker) + len(marker)
                end = content.index("\n", start)
                file_category = content[start:end].strip()
            
            # 提取标签
            marker = "**标签**: "
            if marker in content:
                start = content.index(marker) + len(marker)
                end = content.index("\n", start)
                file_tags = content[start:end].strip()
            
            # 提取预览
            marker = "## 内容\n\n"
            if marker in content:
                start = content.index(marker) + len(marker)
                preview_content = content[start:]
                preview = preview_content[:200] + "..." if len(preview_content) > 200 else preview_content
            
            # 应用过滤条件
            match = True
            
            # 关键词搜索
            if query_lower and query_lower not in content_lower:
                match = False
            
            # 分类过滤
            if category_lower and match:
                if category_lower not in file_category.lower():
                    match = False
            
            # 标签过滤
            if tags_lower and match:
                file_tags_lower = file_tags.lower()
                tag_match = any(tag in file_tags_lower for tag in tags_lower)
                if not tag_match:
                    match = False
            
            if match:
                results.append({
                    "knowledge_id": knowledge_id,
                    "title": title,
                    "category": file_category,
                    "tags": file_tags,
                    "file_path": str(file_path),
                    "preview": preview
                })
            
            if len(results) >= limit:
                break
                
        except Exception as e:
            continue
    
    if not results:
        return {
            "success": True,
            "count": 0,
            "results": [],
            "message": "未找到匹配的知识条目。"
        }
    
    # 构建结果消息
    message_lines = [f"✅ 找到 {len(results)} 条知识", ""]
    
    for i, item in enumerate(results, 1):
        message_lines.extend([
            f"### {i}. {item['title']}",
            "",
            f"- **知识ID**: `{item['knowledge_id']}`",
            f"- **分类**: {item['category']}"
        ])
        if item['tags']:
            message_lines.append(f"- **标签**: {item['tags']}")
        message_lines.extend([
            f"- **文件**: `{item['file_path']}`",
            "",
            f"**预览**:",
            item['preview'],
            "",
            "---",
            ""
        ])
    
    message_lines.append("💡 使用文件读取工具可以查看完整知识内容。")
    
    return {
        "success": True,
        "count": len(results),
        "results": results,
        "message": "\n".join(message_lines)
    }


def main():
    parser = argparse.ArgumentParser(description='知识库管理工具')
    subparsers = parser.add_subparsers(dest='action', help='操作类型')
    
    # save 子命令
    save_parser = subparsers.add_parser('save', help='保存知识')
    save_parser.add_argument('--title', required=True, help='知识标题')
    save_parser.add_argument('--content', required=True, help='知识内容')
    save_parser.add_argument('--work-dir', required=True, help='工作目录')
    save_parser.add_argument('--tags', nargs='*', default=[], help='标签列表')
    save_parser.add_argument('--category', help='分类')
    save_parser.add_argument('--knowledge-id', help='知识ID（更新时使用）')
    
    # search 子命令
    search_parser = subparsers.add_parser('search', help='检索知识')
    search_parser.add_argument('--work-dir', required=True, help='工作目录')
    search_parser.add_argument('--query', help='搜索关键词')
    search_parser.add_argument('--tags', nargs='*', default=[], help='标签过滤')
    search_parser.add_argument('--category', help='分类过滤')
    search_parser.add_argument('--limit', type=int, default=10, help='返回数量限制')
    
    args = parser.parse_args()
    
    if args.action == 'save':
        result = save_knowledge(
            title=args.title,
            content=args.content,
            work_dir=args.work_dir,
            tags=args.tags if args.tags else None,
            category=args.category,
            knowledge_id=args.knowledge_id
        )
    elif args.action == 'search':
        result = search_knowledge(
            work_dir=args.work_dir,
            query=args.query,
            tags=args.tags if args.tags else None,
            category=args.category,
            limit=args.limit
        )
    else:
        parser.print_help()
        sys.exit(1)
    
    print(json.dumps(result, indent=2, ensure_ascii=False))
    sys.exit(0 if result.get('success', False) else 1)


if __name__ == '__main__':
    main()
