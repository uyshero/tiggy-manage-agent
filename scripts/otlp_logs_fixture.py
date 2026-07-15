#!/usr/bin/env python3

import argparse
import json
from http.server import BaseHTTPRequestHandler, HTTPServer


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--token", required=True)
    args = parser.parse_args()

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            if self.path != "/health":
                self.send_error(404)
                return
            self.send_response(200)
            self.end_headers()

        def do_POST(self) -> None:
            authorized = self.headers.get("Authorization") == f"Bearer {args.token}"
            if not authorized:
                self.send_error(401)
                return
            try:
                length = int(self.headers.get("Content-Length", "0"))
                payload = json.loads(self.rfile.read(length))
            except (ValueError, json.JSONDecodeError):
                self.send_error(400)
                return
            with open(args.output, "a", encoding="utf-8") as stream:
                stream.write(json.dumps({
                    "path": self.path,
                    "authorization_valid": authorized,
                    "payload": payload,
                }, separators=(",", ":")) + "\n")
            self.send_response(202)
            self.end_headers()

        def log_message(self, _format: str, *_args: object) -> None:
            return

    HTTPServer((args.host, args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
