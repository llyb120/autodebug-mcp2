#!/usr/bin/env python3
import os
import sys
import json
from http.server import HTTPServer, SimpleHTTPRequestHandler

class EnvHandler(SimpleHTTPRequestHandler):
    def do_GET(self):
        # 获取所有 TEST_ 开头的环境变量
        test_env_vars = {k: v for k, v in os.environ.items() if k.startswith('TEST_')}

        response = {
            "message": "Environment test",
            "test_env_vars": test_env_vars,
            "path": self.path,
            "count": len(test_env_vars)
        }
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps(response, indent=2).encode())

    def log_message(self, format, *args):
        # 减少日志输出
        pass

if __name__ == '__main__':
    port = 8890
    sys.stderr.write(f"Starting env test server on port {port}\n")
    sys.stderr.flush()
    server = HTTPServer(('localhost', port), EnvHandler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        sys.stderr.write("\nServer stopped\n")
        server.shutdown()
