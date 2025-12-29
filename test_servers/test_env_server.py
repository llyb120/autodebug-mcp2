#!/usr/bin/env python3
"""
测试环境变量的 HTTP 服务器
"""
from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import os

class EnvTestHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        """打印所有环境变量"""
        env_vars = {k: v for k, v in os.environ.items() if k.startswith('TEST_')}

        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()

        response = {
            "message": "Environment variables received",
            "test_vars": env_vars
        }
        self.wfile.write(json.dumps(response, indent=2).encode())

    def log_message(self, format, *args):
        print(f"[{self.log_date_time_string()}] {format % args}")

if __name__ == '__main__':
    port = 8889
    print(f"启动环境变量测试服务器在端口 {port}")
    print("健康检查: GET http://localhost:{port}/")
    print("查看 TEST_ 开头的环境变量")

    server = HTTPServer(('localhost', port), EnvTestHandler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\n服务器已停止")
        server.shutdown()
