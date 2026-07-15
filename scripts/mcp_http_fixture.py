#!/usr/bin/env python3
import argparse
import json
import ssl
import threading
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class FixtureState:
    def __init__(self, marker, client_id, client_secret):
        self.marker = marker
        self.client_id = client_id
        self.client_secret = client_secret
        self.lock = threading.Lock()
        self.next_session = 1
        self.sessions = set()
        self.completed_listeners = set()
        self.counters = {
            "token_requests": 0,
            "initializes": 0,
            "session_headers": 0,
            "protocol_headers": 0,
            "post_sse_responses": 0,
            "listener_connections": 0,
            "listener_reconnects": 0,
            "tools_calls": 0,
            "resources_lists": 0,
            "prompts_lists": 0,
            "logging_set_level": 0,
            "deletes": 0,
        }

    def increment(self, name):
        with self.lock:
            self.counters[name] += 1

    def new_session(self):
        with self.lock:
            session_id = f"fixture-session-{self.next_session}"
            self.next_session += 1
            self.sessions.add(session_id)
            self.counters["initializes"] += 1
            return session_id

    def has_session(self, session_id):
        with self.lock:
            return session_id in self.sessions

    def delete_session(self, session_id):
        with self.lock:
            self.sessions.discard(session_id)
            self.counters["deletes"] += 1

    def complete_listener(self, session_id):
        with self.lock:
            if session_id in self.completed_listeners:
                return False
            self.completed_listeners.add(session_id)
            return True

    def snapshot(self):
        with self.lock:
            return {**self.counters, "active_sessions": len(self.sessions)}


def rpc_result(message_id, result):
    return {"jsonrpc": "2.0", "id": message_id, "result": result}


class FixtureHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "tma-mcp-http-fixture/1.0"

    @property
    def state(self):
        return self.server.fixture_state

    def log_message(self, _format, *_args):
        return

    def read_json(self):
        length = int(self.headers.get("Content-Length", "0"))
        return json.loads(self.rfile.read(length).decode("utf-8"))

    def write_json(self, status, value, headers=None):
        payload = json.dumps(value, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        for key, item in (headers or {}).items():
            self.send_header(key, item)
        self.end_headers()
        self.wfile.write(payload)

    def write_empty(self, status):
        self.send_response(status)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def write_sse(self, messages):
        payload = b"".join(
            f"id: {event_id}\ndata: {json.dumps(message, separators=(',', ':'))}\n\n".encode("utf-8")
            for event_id, message in messages
        )
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)
        self.wfile.flush()

    def authorized(self):
        return self.headers.get("Authorization") == "Bearer fixture-access-token"

    def validate_session_headers(self):
        session_id = self.headers.get("Mcp-Session-Id", "")
        if not self.state.has_session(session_id):
            self.write_empty(410)
            return None
        self.state.increment("session_headers")
        if self.headers.get("Mcp-Protocol-Version") != "2025-06-18":
            self.write_json(400, {"error": "missing protocol version"})
            return None
        self.state.increment("protocol_headers")
        return session_id

    def do_POST(self):
        if self.path == "/oauth/token":
            return self.handle_token()
        if self.path != "/mcp":
            return self.write_empty(404)
        if not self.authorized():
            return self.write_empty(401)
        message = self.read_json()
        method = message.get("method", "")
        message_id = message.get("id")
        if method == "initialize":
            session_id = self.state.new_session()
            return self.write_json(
                200,
                rpc_result(
                    message_id,
                    {
                        "protocolVersion": "2025-06-18",
                        "serverInfo": {"name": "tma-mcp-https-fixture", "version": "1.0.0"},
                        "capabilities": {
                            "tools": {"listChanged": True},
                            "resources": {"listChanged": True},
                            "prompts": {"listChanged": True},
                            "logging": {},
                        },
                    },
                ),
                {"Mcp-Session-Id": session_id},
            )
        if self.validate_session_headers() is None:
            return
        if method == "notifications/initialized":
            return self.write_empty(202)
        if method == "logging/setLevel":
            if (message.get("params") or {}).get("level") != "info":
                return self.write_json(400, {"error": "unexpected logging level"})
            self.state.increment("logging_set_level")
            return self.write_json(200, rpc_result(message_id, {}))
        if method == "tools/list":
            return self.write_json(
                200,
                rpc_result(
                    message_id,
                    {
                        "tools": [
                            {
                                "name": "readFile",
                                "title": "Read File",
                                "description": "Return the deterministic HTTPS verification marker.",
                                "inputSchema": {
                                    "type": "object",
                                    "properties": {"path": {"type": "string"}},
                                    "required": ["path"],
                                    "additionalProperties": False,
                                },
                                "annotations": {"readOnlyHint": True},
                            }
                        ]
                    },
                ),
            )
        if method == "resources/list":
            self.state.increment("resources_lists")
            return self.write_json(
                200,
                rpc_result(message_id, {"resources": [{"uri": "fixture:///readme", "name": "README", "mimeType": "text/plain"}]}),
            )
        if method == "resources/read":
            return self.write_json(
                200,
                rpc_result(message_id, {"contents": [{"uri": "fixture:///readme", "mimeType": "text/plain", "text": self.state.marker}]}),
            )
        if method == "prompts/list":
            self.state.increment("prompts_lists")
            return self.write_json(
                200,
                rpc_result(message_id, {"prompts": [{"name": "summarize", "description": "Summarize fixture content."}]}),
            )
        if method == "prompts/get":
            return self.write_json(
                200,
                rpc_result(message_id, {"messages": [{"role": "user", "content": {"type": "text", "text": self.state.marker}}]}),
            )
        if method == "tools/call":
            self.state.increment("tools_calls")
            self.state.increment("post_sse_responses")
            result = rpc_result(
                message_id,
                {
                    "content": [{"type": "text", "text": self.state.marker}],
                    "structuredContent": {"marker": self.state.marker},
                },
            )
            return self.write_sse(
                [
                    ("post-1", {"jsonrpc": "2.0", "method": "notifications/progress", "params": {"progressToken": "fixture", "progress": 1, "total": 1}}),
                    ("post-2", {"jsonrpc": "2.0", "method": "notifications/message", "params": {"level": "info", "logger": "fixture", "data": "redacted-fixture-secret"}}),
                    ("post-3", result),
                ]
            )
        return self.write_json(200, {"jsonrpc": "2.0", "id": message_id, "error": {"code": -32601, "message": "method not found"}})

    def handle_token(self):
        length = int(self.headers.get("Content-Length", "0"))
        form = urllib.parse.parse_qs(self.rfile.read(length).decode("utf-8"))
        if form.get("grant_type") != ["client_credentials"]:
            return self.write_json(400, {"error": "invalid_grant"})
        if form.get("client_id") != [self.state.client_id] or form.get("client_secret") != [self.state.client_secret]:
            return self.write_json(401, {"error": "invalid_client"})
        self.state.increment("token_requests")
        return self.write_json(200, {"access_token": "fixture-access-token", "token_type": "Bearer", "expires_in": 300})

    def do_GET(self):
        if self.path == "/health":
            return self.write_json(200, {"status": "ok"})
        if self.path == "/state":
            return self.write_json(200, self.state.snapshot())
        if self.path != "/mcp":
            return self.write_empty(404)
        if not self.authorized():
            return
        session_id = self.validate_session_headers()
        if session_id is None:
            return
        self.state.increment("listener_connections")
        last_event_id = self.headers.get("Last-Event-ID", "")
        if last_event_id:
            if not self.state.complete_listener(session_id):
                return self.write_empty(405)
            self.state.increment("listener_reconnects")
            return self.write_sse(
                [
                    ("listener-4", {"jsonrpc": "2.0", "method": "notifications/resources/list_changed"}),
                    ("listener-5", {"jsonrpc": "2.0", "method": "notifications/prompts/list_changed"}),
                ]
            )
        return self.write_sse(
            [
                ("listener-1", {"jsonrpc": "2.0", "method": "notifications/tools/list_changed"}),
                ("listener-2", {"jsonrpc": "2.0", "method": "notifications/progress", "params": {"progressToken": 7, "progress": 0.5}}),
                ("listener-3", {"jsonrpc": "2.0", "method": "notifications/message", "params": {"level": "warning", "data": {"secret": "must-not-escape"}}}),
            ]
        )

    def do_DELETE(self):
        if self.path != "/mcp" or not self.authorized():
            return self.write_empty(404)
        session_id = self.headers.get("Mcp-Session-Id", "")
        self.state.delete_session(session_id)
        return self.write_empty(204)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--addr", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=18443)
    parser.add_argument("--cert", required=True)
    parser.add_argument("--key", required=True)
    parser.add_argument("--marker", default="tma-mcp-filesystem-ok")
    parser.add_argument("--client-id", default="tma-mcp-http-client")
    parser.add_argument("--client-secret", default="tma-mcp-http-secret")
    args = parser.parse_args()

    server = ThreadingHTTPServer((args.addr, args.port), FixtureHandler)
    server.fixture_state = FixtureState(args.marker, args.client_id, args.client_secret)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(args.cert, args.key)
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    try:
        server.serve_forever(poll_interval=0.1)
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
