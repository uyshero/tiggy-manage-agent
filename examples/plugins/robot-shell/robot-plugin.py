#!/usr/bin/env python3
import json
import sys
import time


MANIFEST = {
    "identifier": "robot",
    "type": "process_plugin",
    "meta": {
        "title": "Robot Shell Example",
        "description": "Example process plugin for robot-like worker capabilities.",
    },
    "system_role": "Use robot.* tools only for robot control or robot status tasks.",
    "api": [
        {
            "name": "get_state",
            "description": "Read the current robot state.",
            "parameters": {
                "type": "object",
                "properties": {},
                "additionalProperties": False,
            },
            "capabilities": ["robot.state"],
            "risk": "read",
            "runtime": {"allowed": ["local_system"], "preferred": "local_system"},
            "implementation": "worker_capability",
        },
        {
            "name": "stop",
            "description": "Request an immediate robot stop.",
            "parameters": {
                "type": "object",
                "properties": {
                    "reason": {
                        "type": "string",
                        "description": "Human-readable stop reason.",
                    }
                },
                "additionalProperties": False,
            },
            "capabilities": ["robot.stop"],
            "risk": "write",
            "runtime": {"allowed": ["local_system"], "preferred": "local_system"},
            "implementation": "worker_capability",
        },
    ],
}


def emit(payload):
    sys.stdout.write(json.dumps(payload, separators=(",", ":")))


def execute():
    request = json.load(sys.stdin)
    call = request.get("call") or {}
    api_name = call.get("api_name") or call.get("name")
    arguments = call.get("arguments") or {}
    if isinstance(arguments, str):
        arguments = json.loads(arguments or "{}")

    if api_name == "get_state":
        emit(
            {
                "protocol_version": "tma.plugin_result.v1",
                "success": True,
                "content": "robot state: idle",
                "state": {
                    "status": "idle",
                    "battery_percent": 87,
                    "updated_at": int(time.time()),
                },
            }
        )
        return

    if api_name == "stop":
        reason = arguments.get("reason") or "requested by operator"
        emit(
            {
                "protocol_version": "tma.plugin_result.v1",
                "success": True,
                "content": "robot stop requested",
                "state": {
                    "status": "stopped",
                    "reason": reason,
                    "updated_at": int(time.time()),
                },
            }
        )
        return

    emit(
        {
            "protocol_version": "tma.plugin_result.v1",
            "success": False,
            "content": "unsupported robot api",
            "error": {
                "type": "unsupported_api",
                "message": "unsupported robot api: " + str(api_name),
            },
        }
    )


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    if mode == "manifest":
        emit(MANIFEST)
        return 0
    if mode == "execute":
        execute()
        return 0
    sys.stderr.write("usage: robot-plugin.py manifest|execute\n")
    return 64


if __name__ == "__main__":
    raise SystemExit(main())
