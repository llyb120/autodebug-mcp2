#!/usr/bin/env python3
"""
HTTP 请求脚本 - 发起 HTTP 请求并获取日志

功能:
- 发起各种 HTTP 方法的请求
- 支持自定义请求头和请求体
- 可关联进程获取请求期间的日志
"""

import os
import sys
import json
import time
import argparse
from datetime import datetime
from pathlib import Path
from typing import Optional, Dict, Any
from urllib.request import urlopen, Request
from urllib.error import URLError, HTTPError
from urllib.parse import urlparse, urlunparse
import re


# 日志目录
LOGS_DIR = Path(__file__).parent.parent.parent / "logs"


def ensure_logs_dir():
    """确保日志目录存在"""
    LOGS_DIR.mkdir(parents=True, exist_ok=True)


def write_response_to_file(
    method: str,
    url: str,
    status_code: int,
    duration_ms: int,
    response_body: str,
    logs: str = ""
) -> str:
    """将响应写入日志文件"""
    ensure_logs_dir()
    
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    ms = int(datetime.now().timestamp() * 1000) % 1000
    filename = f"response_{timestamp}_{ms:03d}.log"
    filepath = LOGS_DIR / filename
    
    content = [
        "=" * 40,
        "HTTP 请求响应日志",
        "=" * 40,
        f"时间: {datetime.now().isoformat()}",
        f"方法: {method}",
        f"URL: {url}",
        f"状态码: {status_code}",
        f"耗时: {duration_ms}ms",
        "",
        "=" * 40,
        "响应内容",
        "=" * 40,
        response_body,
        ""
    ]
    
    if logs:
        content.extend([
            "=" * 40,
            "进程日志",
            "=" * 40,
            logs,
            ""
        ])
    
    filepath.write_text("\n".join(content), encoding="utf-8")
    return str(filepath)


def request_with_logs(
    url: str,
    method: str = "GET",
    headers: Optional[Dict[str, str]] = None,
    body: Optional[str] = None,
    process_name: Optional[str] = None,
    process_registry: Optional[Dict[str, Any]] = None
) -> Dict[str, Any]:
    """
    发起 HTTP 请求并获取日志
    
    Args:
        url: 请求 URL
        method: HTTP 方法
        headers: 请求头
        body: 请求体
        process_name: 进程名称（用于获取地址和日志）
        process_registry: 进程注册表（外部传入）
        
    Returns:
        包含请求结果的字典
    """
    headers = headers or {}
    process_registry = process_registry or {}
    
    # 处理进程关联
    process_info = None
    if process_name and process_name in process_registry:
        process_info = process_registry[process_name]
    elif not process_name:
        # 尝试通过 URL 自动关联进程
        parsed = urlparse(url)
        if parsed.netloc:
            for name, info in process_registry.items():
                health_url = info.get("health_check_url", "")
                if health_url:
                    health_parsed = urlparse(health_url)
                    if health_parsed.netloc == parsed.netloc:
                        process_info = info
                        break
    
    # 构建完整 URL
    full_url = url
    if process_info and process_info.get("health_check_url"):
        health_parsed = urlparse(process_info["health_check_url"])
        
        if url.startswith(('http://', 'https://')):
            # 完整 URL，替换 host 和 port
            parsed = urlparse(url)
            full_url = urlunparse((
                health_parsed.scheme,
                health_parsed.netloc,
                parsed.path,
                parsed.params,
                parsed.query,
                parsed.fragment
            ))
        else:
            # 只是路径
            path = url if url.startswith('/') else '/' + url
            full_url = f"{health_parsed.scheme}://{health_parsed.netloc}{path}"
    
    # 标记请求开始时间
    request_start_time = datetime.now()
    
    # 构建请求
    method = method.upper()
    req_headers = dict(headers)
    
    # 如果有 body 且没有设置 Content-Type，默认设置为 application/json
    if body and "Content-Type" not in req_headers and "content-type" not in req_headers:
        req_headers["Content-Type"] = "application/json"
    
    # 准备请求体
    data = body.encode('utf-8') if body else None
    
    # 发起请求
    start_time = time.time()
    status_code = 0
    response_body = ""
    error_msg = None
    
    try:
        req = Request(full_url, data=data, headers=req_headers, method=method)
        with urlopen(req, timeout=60) as response:
            status_code = response.status
            response_body = response.read().decode('utf-8', errors='replace')
    except HTTPError as e:
        status_code = e.code
        try:
            response_body = e.read().decode('utf-8', errors='replace')
        except:
            response_body = str(e)
    except URLError as e:
        error_msg = f"请求失败: {e.reason}"
    except Exception as e:
        error_msg = f"请求失败: {str(e)}"
    
    duration_ms = int((time.time() - start_time) * 1000)
    
    if error_msg:
        response_body = error_msg
    
    # 获取进程日志（这里是模拟，实际需要与 process_manager 集成）
    request_logs = ""
    if process_info:
        # 实际使用时需要从 process_manager 获取日志
        request_logs = "(请求期间的进程日志需要与 process_manager 集成)"
    
    # 判断是否需要写入文件
    total_len = len(response_body) + len(request_logs)
    log_file = ""
    
    if total_len > 4000:
        log_file = write_response_to_file(
            method, full_url, status_code, duration_ms, response_body, request_logs
        )
    
    # 构建结果
    result: Dict[str, Any] = {
        "success": error_msg is None,
        "status_code": status_code,
        "duration_ms": duration_ms,
        "url": full_url,
        "method": method
    }
    
    if log_file:
        result["log_file"] = log_file
        result["response_summary"] = response_body[:500] + "..." if len(response_body) > 500 else response_body
        if request_logs:
            result["logs_summary"] = request_logs[:500] + "..." if len(request_logs) > 500 else request_logs
    else:
        result["response"] = response_body
        if request_logs:
            result["logs"] = request_logs
    
    if error_msg:
        result["error"] = error_msg
    
    return result


def main():
    parser = argparse.ArgumentParser(description='HTTP 请求工具')
    parser.add_argument('--url', required=True, help='请求 URL')
    parser.add_argument('--method', default='GET', help='HTTP 方法')
    parser.add_argument('--headers', type=json.loads, default={}, help='请求头(JSON格式)')
    parser.add_argument('--body', help='请求体')
    parser.add_argument('--process-name', help='关联的进程名称')
    
    args = parser.parse_args()
    
    result = request_with_logs(
        url=args.url,
        method=args.method,
        headers=args.headers,
        body=args.body,
        process_name=args.process_name
    )
    
    print(json.dumps(result, indent=2, ensure_ascii=False))
    sys.exit(0 if result.get('success', False) else 1)


if __name__ == '__main__':
    main()
