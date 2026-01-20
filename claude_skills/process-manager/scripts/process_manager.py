#!/usr/bin/env python3
"""
进程管理脚本 - 启动、监控和终止进程

功能:
- start_process: 启动进程并收集日志，通过健康检查确认启动成功
- kill_process: 终止进程（通过名称或端口）
"""

import os
import sys
import json
import time
import signal
import socket
import subprocess
import threading
import argparse
from datetime import datetime
from pathlib import Path
from typing import Optional, Dict, List, Any
from urllib.request import urlopen, Request
from urllib.error import URLError, HTTPError
import re


# 全局进程存储
_processes: Dict[str, Dict[str, Any]] = {}
_lock = threading.Lock()


class ProcessInfo:
    """进程信息类"""
    def __init__(self, name: str, process: subprocess.Popen, health_check_url: str, work_dir: str):
        self.name = name
        self.process = process
        self.health_check_url = health_check_url
        self.work_dir = work_dir
        self.start_time = datetime.now()
        self.logs: List[str] = []
        self.log_lock = threading.Lock()
        self._stop_logging = threading.Event()
        self._log_thread: Optional[threading.Thread] = None
        
    def start_log_collection(self):
        """启动日志收集线程"""
        def collect_stdout():
            try:
                for line in iter(self.process.stdout.readline, ''):
                    if self._stop_logging.is_set():
                        break
                    if line:
                        with self.log_lock:
                            timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S.%f")[:-3]
                            self.logs.append(f"[{timestamp}] [STDOUT] {line.rstrip()}")
            except Exception as e:
                with self.log_lock:
                    self.logs.append(f"[ERROR] 读取stdout失败: {e}")
                    
        def collect_stderr():
            try:
                for line in iter(self.process.stderr.readline, ''):
                    if self._stop_logging.is_set():
                        break
                    if line:
                        with self.log_lock:
                            timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S.%f")[:-3]
                            self.logs.append(f"[{timestamp}] [STDERR] {line.rstrip()}")
            except Exception as e:
                with self.log_lock:
                    self.logs.append(f"[ERROR] 读取stderr失败: {e}")
        
        stdout_thread = threading.Thread(target=collect_stdout, daemon=True)
        stderr_thread = threading.Thread(target=collect_stderr, daemon=True)
        stdout_thread.start()
        stderr_thread.start()
        
    def stop_log_collection(self):
        """停止日志收集"""
        self._stop_logging.set()
        
    def get_logs(self) -> str:
        """获取收集的日志"""
        with self.log_lock:
            return "\n".join(self.logs[-500:])  # 最多返回最近500行


def parse_port_from_url(url: str) -> Optional[int]:
    """从URL解析端口号"""
    match = re.search(r':(\d+)', url)
    if match:
        return int(match.group(1))
    if url.startswith('https://'):
        return 443
    if url.startswith('http://'):
        return 80
    return None


def check_port_in_use(port: int) -> bool:
    """检查端口是否被占用"""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        return s.connect_ex(('localhost', port)) == 0


def get_pid_by_port(port: int) -> Optional[int]:
    """获取占用指定端口的进程PID"""
    try:
        if sys.platform == 'win32':
            result = subprocess.run(
                ['netstat', '-ano', '-p', 'TCP'],
                capture_output=True, text=True
            )
            for line in result.stdout.split('\n'):
                if f':{port}' in line and 'LISTENING' in line:
                    parts = line.split()
                    if parts:
                        return int(parts[-1])
        else:
            result = subprocess.run(
                ['lsof', '-ti', f':{port}'],
                capture_output=True, text=True
            )
            if result.stdout.strip():
                return int(result.stdout.strip().split('\n')[0])
    except Exception as e:
        print(f"获取端口{port}的PID失败: {e}", file=sys.stderr)
    return None


def kill_process_by_pid(pid: int):
    """通过PID终止进程"""
    try:
        if sys.platform == 'win32':
            subprocess.run(['taskkill', '/F', '/PID', str(pid)], check=True)
        else:
            os.kill(pid, signal.SIGTERM)
            time.sleep(0.5)
            try:
                os.kill(pid, 0)  # 检查进程是否还在
                os.kill(pid, signal.SIGKILL)  # 如果还在，强制杀死
            except ProcessLookupError:
                pass
    except Exception as e:
        print(f"终止进程 {pid} 失败: {e}", file=sys.stderr)
        raise


def wait_for_health_check(url: str, method: str = 'GET', timeout: int = 60) -> tuple[bool, str]:
    """等待健康检查通过"""
    start_time = time.time()
    last_error = ""
    
    while time.time() - start_time < timeout:
        try:
            req = Request(url, method=method)
            with urlopen(req, timeout=5) as response:
                if 200 <= response.status < 300:
                    return True, ""
                last_error = f"HTTP {response.status}"
        except HTTPError as e:
            if 200 <= e.code < 300:
                return True, ""
            last_error = f"HTTP {e.code}: {e.reason}"
        except URLError as e:
            last_error = str(e.reason)
        except Exception as e:
            last_error = str(e)
        
        time.sleep(0.5)
    
    return False, f"健康检查超时 ({timeout}秒): {last_error}"


def start_process(
    name: str,
    command: str,
    args: Optional[List[str]] = None,
    work_dir: Optional[str] = None,
    env: Optional[Dict[str, str]] = None,
    health_check_url: str = "",
    health_check_method: str = "GET",
    timeout_seconds: int = 60
) -> Dict[str, Any]:
    """
    启动进程并收集日志
    
    Args:
        name: 进程名称
        command: 可执行文件名
        args: 命令参数列表
        work_dir: 工作目录
        env: 环境变量
        health_check_url: 健康检查URL
        health_check_method: 健康检查HTTP方法
        timeout_seconds: 启动超时时间
        
    Returns:
        包含启动结果的字典
    """
    global _processes
    
    # 参数验证
    if ' ' in command:
        return {
            "success": False,
            "error": f"命令参数错误：command '{command}' 包含空格\n\n"
                    "正确用法：\n"
                    "- command: 可执行文件名（如 'python', 'node', 'go'）\n"
                    "- args: 参数列表（如 ['run', '.']）\n\n"
                    "示例：启动 Python HTTP 服务器\n"
                    "  command: \"python\"\n"
                    "  args: [\"-m\", \"http.server\", \"8080\"]"
        }
    
    if not health_check_url:
        return {
            "success": False,
            "error": "必须提供 health_check_url 参数"
        }
    
    # 清理同名旧进程
    with _lock:
        if name in _processes:
            old_info = _processes[name]
            old_info["process_info"].stop_log_collection()
            try:
                old_info["process_info"].process.terminate()
                old_info["process_info"].process.wait(timeout=5)
            except:
                try:
                    old_info["process_info"].process.kill()
                except:
                    pass
            del _processes[name]
            time.sleep(1)
    
    # 检查端口占用
    port = parse_port_from_url(health_check_url)
    if port and check_port_in_use(port):
        pid = get_pid_by_port(port)
        if pid:
            try:
                kill_process_by_pid(pid)
                time.sleep(1)
            except:
                pass
    
    # 构建命令
    full_command = [command] + (args or [])
    
    # 设置环境变量
    process_env = os.environ.copy()
    if env:
        process_env.update(env)
    
    # 设置工作目录
    cwd = work_dir if work_dir else None
    
    try:
        # 启动进程
        process = subprocess.Popen(
            full_command,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=cwd,
            env=process_env,
            text=True,
            bufsize=1  # 行缓冲
        )
        
        # 创建进程信息
        process_info = ProcessInfo(name, process, health_check_url, cwd or os.getcwd())
        process_info.start_log_collection()
        
        # 存储进程信息
        with _lock:
            _processes[name] = {
                "process_info": process_info,
                "pid": process.pid
            }
        
        # 等待健康检查
        success, error_msg = wait_for_health_check(
            health_check_url, 
            health_check_method, 
            timeout_seconds
        )
        
        if not success:
            # 启动失败，收集日志并清理
            logs = process_info.get_logs()
            process_info.stop_log_collection()
            
            try:
                process.terminate()
                process.wait(timeout=5)
            except:
                try:
                    process.kill()
                except:
                    pass
            
            with _lock:
                if name in _processes:
                    del _processes[name]
            
            return {
                "success": False,
                "error": error_msg,
                "pid": process.pid,
                "health_check_url": health_check_url,
                "logs": logs
            }
        
        # 启动成功
        return {
            "success": True,
            "pid": process.pid,
            "start_time": process_info.start_time.isoformat(),
            "work_dir": process_info.work_dir,
            "health_check_url": health_check_url,
            "logs": process_info.get_logs()
        }
        
    except Exception as e:
        return {
            "success": False,
            "error": f"启动进程失败: {str(e)}"
        }


def kill_process(name: Optional[str] = None, port: Optional[int] = None) -> Dict[str, Any]:
    """
    终止进程
    
    Args:
        name: 进程名称（优先）
        port: 端口号
        
    Returns:
        包含终止结果的字典
    """
    global _processes
    
    if not name and not port:
        return {
            "success": False,
            "error": "必须提供 name 或 port 参数中的至少一个"
        }
    
    # 优先使用 name
    if name:
        with _lock:
            if name in _processes:
                process_data = _processes[name]
                process_info = process_data["process_info"]
                pid = process_data["pid"]
                
                process_info.stop_log_collection()
                
                try:
                    process_info.process.terminate()
                    process_info.process.wait(timeout=5)
                except:
                    try:
                        process_info.process.kill()
                    except:
                        pass
                
                del _processes[name]
                
                return {
                    "success": True,
                    "name": name,
                    "pid": pid,
                    "message": f"成功终止进程 {name} (PID: {pid})"
                }
            else:
                return {
                    "success": False,
                    "error": f"未找到进程 '{name}'\n提示：本技能未启动过此进程。如果该进程正在运行，请使用 port 参数来终止它。"
                }
    
    # 使用 port
    if port:
        if not check_port_in_use(port):
            return {
                "success": False,
                "error": f"端口 {port} 未被占用"
            }
        
        pid = get_pid_by_port(port)
        if not pid:
            return {
                "success": False,
                "error": f"无法获取占用端口 {port} 的进程PID"
            }
        
        try:
            kill_process_by_pid(pid)
            
            # 从管理的进程中移除（如果存在）
            with _lock:
                for proc_name, proc_data in list(_processes.items()):
                    if proc_data["pid"] == pid:
                        proc_data["process_info"].stop_log_collection()
                        del _processes[proc_name]
                        break
            
            return {
                "success": True,
                "port": port,
                "pid": pid,
                "message": f"成功终止占用端口 {port} 的进程 (PID: {pid})"
            }
        except Exception as e:
            return {
                "success": False,
                "error": f"终止端口 {port} 的进程失败: {str(e)}"
            }
    
    return {
        "success": False,
        "error": "未知错误"
    }


def get_process_logs(name: str) -> Dict[str, Any]:
    """获取进程日志"""
    with _lock:
        if name in _processes:
            process_info = _processes[name]["process_info"]
            return {
                "success": True,
                "name": name,
                "logs": process_info.get_logs()
            }
    return {
        "success": False,
        "error": f"进程 '{name}' 不存在"
    }


def main():
    parser = argparse.ArgumentParser(description='进程管理工具')
    subparsers = parser.add_subparsers(dest='action', help='操作类型')
    
    # start 子命令
    start_parser = subparsers.add_parser('start', help='启动进程')
    start_parser.add_argument('--name', required=True, help='进程名称')
    start_parser.add_argument('--command', required=True, help='可执行文件名')
    start_parser.add_argument('--args', nargs='*', default=[], help='命令参数')
    start_parser.add_argument('--work-dir', help='工作目录')
    start_parser.add_argument('--env', type=json.loads, default={}, help='环境变量(JSON格式)')
    start_parser.add_argument('--health-check-url', required=True, help='健康检查URL')
    start_parser.add_argument('--health-check-method', default='GET', help='健康检查方法')
    start_parser.add_argument('--timeout', type=int, default=60, help='超时时间(秒)')
    
    # kill 子命令
    kill_parser = subparsers.add_parser('kill', help='终止进程')
    kill_parser.add_argument('--name', help='进程名称')
    kill_parser.add_argument('--port', type=int, help='端口号')
    
    # logs 子命令
    logs_parser = subparsers.add_parser('logs', help='获取进程日志')
    logs_parser.add_argument('--name', required=True, help='进程名称')
    
    args = parser.parse_args()
    
    if args.action == 'start':
        result = start_process(
            name=args.name,
            command=args.command,
            args=args.args,
            work_dir=args.work_dir,
            env=args.env,
            health_check_url=args.health_check_url,
            health_check_method=args.health_check_method,
            timeout_seconds=args.timeout
        )
    elif args.action == 'kill':
        result = kill_process(name=args.name, port=args.port)
    elif args.action == 'logs':
        result = get_process_logs(args.name)
    else:
        parser.print_help()
        sys.exit(1)
    
    print(json.dumps(result, indent=2, ensure_ascii=False))
    sys.exit(0 if result.get('success', False) else 1)


if __name__ == '__main__':
    main()
