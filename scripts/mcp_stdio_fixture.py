#!/usr/bin/env python3
import json
import os
import sys
import time


def read_message():
    content_length = None
    while True:
        line = sys.stdin.buffer.readline()
        if not line:
            return None
        line = line.rstrip(b"\r\n")
        if not line:
            break
        lower = line.lower()
        if lower.startswith(b"content-length:"):
            content_length = int(line.split(b":", 1)[1].strip())
    if content_length is None:
        return None
    payload = sys.stdin.buffer.read(content_length)
    if not payload:
        return None
    return json.loads(payload.decode("utf-8"))


def write_message(message):
    payload = json.dumps(message, separators=(",", ":")).encode("utf-8")
    sys.stdout.buffer.write(b"Content-Length: " + str(len(payload)).encode("ascii") + b"\r\n\r\n")
    sys.stdout.buffer.write(payload)
    sys.stdout.buffer.flush()


def handle_initialize(message_id):
    write_message(
        {
            "jsonrpc": "2.0",
            "id": message_id,
            "result": {
                "serverInfo": {
                    "name": "tma-mcp-fixture",
                    "version": "1.0.0",
                },
                "capabilities": {},
            },
        }
    )


def handle_tools_list(message_id):
    write_message(
        {
            "jsonrpc": "2.0",
            "id": message_id,
            "result": {
                "tools": [
                    {
                        "name": "readFile",
                        "title": "Read File",
                        "description": "Return a deterministic filesystem verification marker.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "path": {"type": "string"}
                            },
                            "required": ["path"],
                            "additionalProperties": False,
                        },
                        "annotations": {"readOnlyHint": True},
                    }
                ]
            },
        }
    )


def fault_mode():
    mode_file = os.environ.get("TMA_MCP_FAULT_MODE_FILE", "").strip()
    if not mode_file:
        return "success"
    try:
        with open(mode_file, "r", encoding="utf-8") as source:
            return source.read().strip().lower() or "success"
    except FileNotFoundError:
        return "success"


def record_tool_call(mode):
    call_file = os.environ.get("TMA_MCP_FAULT_CALL_FILE", "").strip()
    if not call_file:
        return
    with open(call_file, "a", encoding="utf-8") as calls:
        calls.write(f"{mode}\n")


def handle_tools_call(message_id, params):
    name = (params or {}).get("name", "")
    arguments = (params or {}).get("arguments") or {}
    path = arguments.get("path", "")
    marker = os.environ.get("TMA_MCP_FIXTURE_MARKER", "tma-mcp-filesystem-ok")
    if name != "readFile":
        write_message(
            {
                "jsonrpc": "2.0",
                "id": message_id,
                "result": {
                    "content": [{"type": "text", "text": f"unsupported tool: {name}"}],
                    "isError": True,
                },
            }
        )
        return
    mode = fault_mode()
    record_tool_call(mode)
    if mode == "timeout":
        delay = float(os.environ.get("TMA_MCP_FAULT_DELAY_SECONDS", "5"))
        time.sleep(max(delay, 0))
    elif mode == "transport":
        raise SystemExit(75)
    elif mode == "rpc_unavailable":
        write_message(
            {
                "jsonrpc": "2.0",
                "id": message_id,
                "error": {"code": -32000, "message": "fixture unavailable"},
            }
        )
        return
    elif mode == "protocol":
        write_message(
            {
                "jsonrpc": "2.0",
                "id": message_id,
                "result": "invalid tools/call result",
            }
        )
        return
    elif mode != "success":
        write_message(
            {
                "jsonrpc": "2.0",
                "id": message_id,
                "error": {"code": -32602, "message": "unsupported fixture fault mode"},
            }
        )
        return
    text = f"{marker}\npath={path}"
    write_message(
        {
            "jsonrpc": "2.0",
            "id": message_id,
            "result": {
                "content": [{"type": "text", "text": text}],
                "structuredContent": {
                    "marker": marker,
                    "path": path,
                },
            },
        }
    )


def main():
    start_file = os.environ.get("TMA_MCP_FIXTURE_START_FILE", "").strip()
    if start_file:
        with open(start_file, "a", encoding="utf-8") as marker:
            marker.write(f"{os.getpid()}\n")
    while True:
        message = read_message()
        if message is None:
            return 0
        method = message.get("method", "")
        if method == "notifications/initialized":
            continue
        message_id = message.get("id")
        if method == "initialize":
            handle_initialize(message_id)
        elif method == "tools/list":
            handle_tools_list(message_id)
        elif method == "tools/call":
            handle_tools_call(message_id, message.get("params"))
        else:
            write_message(
                {
                    "jsonrpc": "2.0",
                    "id": message_id,
                    "error": {"code": -32601, "message": f"method not found: {method}"},
                }
            )


if __name__ == "__main__":
    raise SystemExit(main())
