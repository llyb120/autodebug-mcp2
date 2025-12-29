#!/usr/bin/env python3
import os
import sys
import json
from http.server import HTTPServer, BaseHTTPRequestHandler

class EnvHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        test_env_vars = {k: v for k, v in os.environ.items() if k.startswith('TEST_')}

        response = {
            "message": "Environment variables received",
            "test_env_vars": test_env_vars,
            "count": len(test_env_vars)
        }

        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps(response, indent=2).encode())

    def log_message(self, format, *args):
        pass

if __name__ == '__main__':
    port = 8890
    server = HTTPServer(('localhost', port), EnvHandler)
    server.serve_forever()
