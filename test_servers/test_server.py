#!/usr/bin/env python3
"""
简单的测试 HTTP 服务器
用于演示 MCP 客户端的功能
"""
from http.server import HTTPServer, SimpleHTTPRequestHandler
import sys
import json

class TestHandler(SimpleHTTPRequestHandler):
    def log_message(self, format, *args):
        """自定义日志格式，便于观察"""
        print(f"[{self.log_date_time_string()}] {format % args}")

    def do_GET(self):
        """处理 GET 请求"""
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        response = {
            "message": "Hello from test server!",
            "path": self.path,
            "method": "GET"
        }
        self.wfile.write(json.dumps(response).encode())

    def do_POST(self):
        """处理 POST 请求"""
        content_length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(content_length)

        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()

        response = {
            "message": "POST received",
            "path": self.path,
            "body": body.decode('utf-8', errors='ignore'),
            "headers": dict(self.headers)
        }
        self.wfile.write(json.dumps(response, indent=2).encode())

    def do_PUT(self):
        """处理 PUT 请求"""
        content_length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(content_length)

        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()

        response = {
            "message": "PUT received",
            "path": self.path,
            "body": body.decode('utf-8', errors='ignore')
        }
        self.wfile.write(json.dumps(response).encode())

if __name__ == '__main__':
    port = 8888
    print(f"启动测试服务器在端口 {port}...")
    print(f"健康检查: http://localhost:{port}/")
    print("按 Ctrl+C 停止服务器")

    server = HTTPServer(('localhost', port), TestHandler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\n服务器已停止")
        server.shutdown()
