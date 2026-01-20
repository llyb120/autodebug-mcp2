#!/usr/bin/env python3
"""
记忆管理脚本 - 保存和读取 AI 对话状态

功能:
- save_memory: 保存系统提示词和任务记忆到文件
- read_memory: 根据记忆ID读取记忆文件
"""

import os
import sys
import json
import uuid
import argparse
from datetime import datetime
from pathlib import Path
from typing import Optional, Dict, Any


# 记忆目录
MEMS_DIR = Path(__file__).parent.parent.parent / "mems"


def ensure_mems_dir():
    """确保记忆目录存在"""
    MEMS_DIR.mkdir(parents=True, exist_ok=True)


def save_memory(
    system_prompt: str,
    content: str,
    memory_id: Optional[str] = None,
    update_mode: str = "append"
) -> Dict[str, Any]:
    """
    保存记忆到文件
    
    Args:
        system_prompt: 系统提示词完整内容
        content: 记忆内容
        memory_id: 记忆ID（可选），如果提供则更新，否则创建新记忆
        
    Returns:
        包含保存结果的字典
    """
    # 参数验证
    if not system_prompt:
        return {
            "success": False,
            "error": "参数错误：system_prompt 不能为空，请提供完整的系统提示词"
        }
    
    if not content:
        return {
            "success": False,
            "error": "参数错误：content 不能为空"
        }
    
    ensure_mems_dir()
    
    # 确定记忆ID和文件路径
    is_update = False
    previous_content = ""
    
    if memory_id:
        # 更新模式
        is_update = True
        file_path = MEMS_DIR / f"{memory_id}.md"
        
        # 读取现有记忆内容用于比较
        if file_path.exists():
            old_content = file_path.read_text(encoding='utf-8')
            # 提取现有的任务记忆部分
            marker = "## 任务记忆\n\n"
            if marker in old_content:
                start = old_content.index(marker) + len(marker)
                end_marker = "\n\n---\n\n"
                if end_marker in old_content[start:]:
                    end = old_content.index(end_marker, start)
                    previous_content = old_content[start:end]
                else:
                    previous_content = old_content[start:]
    else:
        # 创建新记忆
        memory_id = str(uuid.uuid4())
        file_path = MEMS_DIR / f"{memory_id}.md"
    
    # 根据更新模式处理内容
    if is_update:
        normalized_mode = (update_mode or "append").strip().lower()
        if normalized_mode not in ("append", "replace"):
            return {
                "success": False,
                "error": "参数错误：update_mode 必须是 append 或 replace"
            }
        if normalized_mode == "append" and previous_content:
            # 如果新内容未包含历史内容，自动追加，避免覆盖
            if previous_content not in content:
                content = f"{previous_content}\n\n{content}".strip()

    # 构建文件内容
    now = datetime.now().isoformat()
    
    lines = [
        "# 记忆文件",
        "",
        f"**记忆ID**: `{memory_id}`",
        "",
        f"**保存时间**: {now}",
        "",
        f"**操作**: {'更新现有记忆' if is_update else '创建新记忆'}",
        "",
        "---",
        "",
        "## 系统提示词",
        "",
        "```markdown",
        system_prompt,
        "```",
        "",
        "---",
        "",
        "## 任务记忆",
        "",
        content,
        "",
        "---",
        "",
        f"**⚠️ 重要**: 如果上下文被截断，请读取此文件恢复状态: `{file_path}`"
    ]
    
    file_content = "\n".join(lines)
    
    # 写入文件
    try:
        file_path.write_text(file_content, encoding='utf-8')
    except Exception as e:
        return {
            "success": False,
            "error": f"保存记忆失败: {str(e)}"
        }
    
    # 检查是否可能丢失了内容
    warning = ""
    if is_update and previous_content and len(content) < len(previous_content) / 2:
        warning = f"\n\n⚠️ **警告**: 新记忆内容({len(content)}字符)比之前({len(previous_content)}字符)短很多，请确认是否遗漏了重要信息！"
    
    action_text = "✅ 记忆已更新" if is_update else "✅ 记忆已保存"
    
    return {
        "success": True,
        "memory_id": memory_id,
        "file_path": str(file_path),
        "content_length": len(content),
        "prompt_length": len(system_prompt),
        "is_update": is_update,
        "message": f"{action_text}\n\n"
                   f"**记忆ID**: `{memory_id}`\n"
                   f"**文件路径**: `{file_path}`\n"
                   f"**记忆内容长度**: {len(content)} 字符\n"
                   f"**系统提示词长度**: {len(system_prompt)} 字符"
                   f"{warning}\n\n"
                   f"⚠️ **请记住此记忆ID**，如果上下文被截断，使用 read_memory 读取此记忆即可恢复完整状态。\n\n"
                   f"💡 **提示**: 更新记忆时，推荐先调用 read_memory 读取现有内容，再修改保存；如无法读取，请使用 update_mode='append' 追加保存，避免覆盖丢失历史。"
    }


def read_memory(memory_id: str) -> Dict[str, Any]:
    """
    读取记忆文件
    
    Args:
        memory_id: 记忆ID
        
    Returns:
        包含记忆内容的字典
    """
    if not memory_id:
        return {
            "success": False,
            "error": "参数错误：必须提供 memory_id 参数"
        }
    
    file_path = MEMS_DIR / f"{memory_id}.md"
    
    if not file_path.exists():
        return {
            "success": False,
            "error": f"记忆文件不存在: {file_path}"
        }
    
    try:
        content = file_path.read_text(encoding='utf-8')
    except Exception as e:
        return {
            "success": False,
            "error": f"读取记忆失败: {str(e)}"
        }
    
    return {
        "success": True,
        "memory_id": memory_id,
        "file_path": str(file_path),
        "file_size": len(content),
        "content": content,
        "message": f"✅ 记忆读取成功\n\n"
                   f"**记忆ID**: `{memory_id}`\n"
                   f"**文件路径**: `{file_path}`\n"
                   f"**文件大小**: {len(content)} 字符\n\n"
                   f"---\n\n{content}"
    }


def list_memories() -> Dict[str, Any]:
    """列出所有记忆文件"""
    if not MEMS_DIR.exists():
        return {
            "success": True,
            "memories": [],
            "message": "记忆目录为空，尚未保存任何记忆。"
        }
    
    memories = []
    for file_path in MEMS_DIR.glob("*.md"):
        memory_id = file_path.stem
        stat = file_path.stat()
        memories.append({
            "memory_id": memory_id,
            "file_path": str(file_path),
            "size": stat.st_size,
            "modified": datetime.fromtimestamp(stat.st_mtime).isoformat()
        })
    
    # 按修改时间倒序排列
    memories.sort(key=lambda x: x["modified"], reverse=True)
    
    return {
        "success": True,
        "count": len(memories),
        "memories": memories
    }


def main():
    parser = argparse.ArgumentParser(description='记忆管理工具')
    subparsers = parser.add_subparsers(dest='action', help='操作类型')
    
    # save 子命令
    save_parser = subparsers.add_parser('save', help='保存记忆')
    save_parser.add_argument('--system-prompt', required=True, help='系统提示词')
    save_parser.add_argument('--content', required=True, help='记忆内容')
    save_parser.add_argument('--memory-id', help='记忆ID（更新时使用）')
    save_parser.add_argument('--update-mode', default='append', help='更新模式: append 或 replace')
    
    # read 子命令
    read_parser = subparsers.add_parser('read', help='读取记忆')
    read_parser.add_argument('--memory-id', required=True, help='记忆ID')
    
    # list 子命令
    list_parser = subparsers.add_parser('list', help='列出所有记忆')
    
    args = parser.parse_args()
    
    if args.action == 'save':
        result = save_memory(
            system_prompt=args.system_prompt,
            content=args.content,
            memory_id=args.memory_id,
            update_mode=args.update_mode
        )
    elif args.action == 'read':
        result = read_memory(args.memory_id)
    elif args.action == 'list':
        result = list_memories()
    else:
        parser.print_help()
        sys.exit(1)
    
    print(json.dumps(result, indent=2, ensure_ascii=False))
    sys.exit(0 if result.get('success', False) else 1)


if __name__ == '__main__':
    main()
