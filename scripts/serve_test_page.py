# A tiny page server for the CDP/BiDi checks. Usage: serve_test_page.py [port]
import http.server, socketserver, sys

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8765


class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = (b"<html><head><title>Puppeteer Check</title></head>"
                b"<body><h1 id='h'>hello cdp</h1></body></html>")
        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass


socketserver.TCPServer.allow_reuse_address = True
socketserver.TCPServer(("127.0.0.1", PORT), H).serve_forever()
