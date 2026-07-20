#!/usr/bin/env python3
import hashlib
import hmac
import json
import os
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


BASE_PROPERTIES = {
    "browser_session_id": {
        "type": "string",
        "description": "Browser session to reuse. Defaults to the current TMA session.",
    }
}


def schema(properties=None, required=None, any_of=None):
    value = {
        "type": "object",
        "properties": {**BASE_PROPERTIES, **(properties or {})},
        "additionalProperties": False,
    }
    if required:
        value["required"] = required
    if any_of:
        value["anyOf"] = any_of
    return value


APIS = [
    {
        "name": "open",
        "description": "Open a URL in an isolated browser extension session.",
        "parameters": schema({"url": {"type": "string"}}, ["url"]),
        "capabilities": ["browser.open", "browser.read"],
        "risk": "read",
    },
    {
        "name": "read",
        "description": "Read the current page and list visible interactive elements.",
        "parameters": schema(),
        "capabilities": ["browser.read"],
        "risk": "read",
    },
    {
        "name": "click",
        "description": "Click an element using a selector or ref returned by browser.read/open.",
        "parameters": schema(
            {"selector": {"type": "string"}, "ref": {"type": "string"}},
            any_of=[{"required": ["selector"]}, {"required": ["ref"]}],
        ),
        "capabilities": ["browser.read", "browser.interact"],
        "risk": "write",
    },
    {
        "name": "type",
        "description": "Fill text into an element using a selector or ref returned by browser.read/open.",
        "parameters": schema(
            {
                "selector": {"type": "string"},
                "ref": {"type": "string"},
                "text": {"type": "string"},
                "clear": {"type": "boolean"},
            },
            required=["text"],
            any_of=[{"required": ["selector"]}, {"required": ["ref"]}],
        ),
        "capabilities": ["browser.read", "browser.interact"],
        "risk": "write",
    },
    {
        "name": "screenshot",
        "description": "Capture the current page as a screenshot artifact.",
        "parameters": schema({"full_page": {"type": "boolean"}}),
        "capabilities": ["browser.read", "browser.capture"],
        "risk": "read",
    },
    {
        "name": "takeover",
        "description": "Return the Workbench browser extension URL for manual control of this session.",
        "parameters": schema(),
        "capabilities": ["browser.read", "browser.takeover"],
        "risk": "write",
    },
    {
        "name": "close",
        "description": "Close the isolated browser extension session.",
        "parameters": schema(),
        "capabilities": ["browser.close"],
        "risk": "write",
    },
]


MANIFEST = {
    "identifier": "browser",
    "type": "process_plugin",
    "meta": {
        "title": "Browser Extension",
        "description": "Tenant-scoped browser tools provided by the external TMA Browser Gateway.",
    },
    "system_role": (
        "Use browser.* tools when JavaScript rendering or page interaction is required. "
        "Reuse browser_session_id across related calls. Use refs returned by browser.read/open. "
        "Use browser.takeover when the user needs to log in or control the page manually."
    ),
    "api": [
        {
            **api,
            "namespace": "browser",
            "api": api["name"],
            "runtime": {"allowed": ["local_system"], "preferred": "local_system"},
            "implementation": "worker_capability",
        }
        for api in APIS
    ],
}


def emit(payload):
    sys.stdout.write(json.dumps(payload, separators=(",", ":"), ensure_ascii=False))


def success(content, state=None, exported_files=None):
    payload = {
        "protocol_version": "tma.plugin_result.v1",
        "success": True,
        "content": content,
        "state": state or {},
    }
    if exported_files:
        payload["exported_files"] = exported_files
    emit(payload)


def failure(error_type, message, state=None):
    emit(
        {
            "protocol_version": "tma.plugin_result.v1",
            "success": False,
            "content": message,
            "state": state or {},
            "error": {"type": error_type, "message": message},
        }
    )


def gateway_base_url():
    return os.environ.get("TMA_BROWSER_GATEWAY_URL", "http://127.0.0.1:8090/v2/extensions/browser").rstrip("/")


def signed_headers(method, path, workspace_id, session_id, body):
    secret = os.environ.get("TMA_BROWSER_GATEWAY_SERVICE_SECRET", "")
    if not secret:
        raise RuntimeError("TMA_BROWSER_GATEWAY_SERVICE_SECRET is required")
    timestamp = str(int(time.time()))
    body_hash = hashlib.sha256(body).hexdigest()
    canonical = "\n".join([timestamp, method.upper(), path, workspace_id, session_id, body_hash])
    signature = hmac.new(secret.encode(), canonical.encode(), hashlib.sha256).hexdigest()
    return {
        "Content-Type": "application/json",
        "X-TMA-Browser-Timestamp": timestamp,
        "X-TMA-Workspace-ID": workspace_id,
        "X-TMA-Session-ID": session_id,
        "X-TMA-Browser-Signature": signature,
    }


def gateway_request(method, suffix, context, payload=None, binary=False):
    base = gateway_base_url()
    parsed = urllib.parse.urlparse(base)
    path = parsed.path.rstrip("/") + suffix
    url = urllib.parse.urlunparse(parsed._replace(path=path, query=""))
    body = b"" if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    workspace_id = str(context.get("workspace_id") or "wksp_default")
    session_id = str(context.get("session_id") or "")
    if not session_id:
        raise RuntimeError("browser plugin requires a TMA session id")
    request = urllib.request.Request(
        url,
        data=body if method in {"POST", "PUT", "PATCH"} else None,
        method=method,
        headers=signed_headers(method, path, workspace_id, session_id, body),
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            content = response.read()
            return content if binary else json.loads(content or b"{}")
    except urllib.error.HTTPError as error:
        detail = error.read().decode("utf-8", errors="replace")
        try:
            detail = json.loads(detail).get("error", detail)
        except json.JSONDecodeError:
            pass
        raise RuntimeError(f"browser gateway returned HTTP {error.code}: {detail}") from error


def ensure_session(context, browser_session_id, url=None):
    payload = {
        "browser_session_id": browser_session_id,
        "tma_session_id": context.get("session_id"),
    }
    if url:
        payload["url"] = url
    return gateway_request("POST", "/sessions", context, payload)


def content_for(state):
    lines = [
        f"Browser session: {state.get('browser_session_id', '')}",
        f"URL: {state.get('url', '')}",
        f"Title: {state.get('title', '')}",
    ]
    if state.get("text"):
        lines.extend(["", str(state["text"])])
    elements = state.get("elements") or []
    if elements:
        lines.extend(["", "Interactive elements:"])
        lines.extend(
            f"{item.get('ref')} [{item.get('role')}] {item.get('text') or item.get('selector')}"
            for item in elements[:80]
        )
    return "\n".join(lines)


def execute():
    request = json.load(sys.stdin)
    call = request.get("call") or {}
    context = request.get("context") or {}
    arguments = call.get("arguments") or {}
    if isinstance(arguments, str):
        arguments = json.loads(arguments or "{}")
    api_name = call.get("api_name") or call.get("name")
    browser_session_id = str(arguments.get("browser_session_id") or context.get("session_id") or "")
    if not browser_session_id:
        raise RuntimeError("browser_session_id or TMA session context is required")

    if api_name == "open":
        ensure_session(context, browser_session_id)
        encoded_id = urllib.parse.quote(browser_session_id, safe="")
        state = gateway_request(
            "POST",
            f"/sessions/{encoded_id}/open",
            context,
            {"url": str(arguments.get("url") or "")},
        )
        success(content_for(state), state)
        return

    state = ensure_session(context, browser_session_id)
    encoded_id = urllib.parse.quote(browser_session_id, safe="")
    if api_name == "read":
        state = gateway_request("GET", f"/sessions/{encoded_id}", context)
    elif api_name in {"click", "type"}:
        state = gateway_request("POST", f"/sessions/{encoded_id}/{api_name}", context, arguments)
    elif api_name == "screenshot":
        frame = gateway_request("GET", f"/sessions/{encoded_id}/frame", context, binary=True)
        handle, filename = tempfile.mkstemp(prefix="tma-browser-", suffix=".jpg")
        with os.fdopen(handle, "wb") as output:
            output.write(frame)
        state = gateway_request("GET", f"/sessions/{encoded_id}", context)
        success(
            content_for(state),
            state,
            [{"path": filename, "name": "browser-screenshot.jpg", "artifact_type": "asset", "content_type": "image/jpeg"}],
        )
        return
    elif api_name == "takeover":
        route = f"/plugins/com.tma.enterprise-browser/browser?session={urllib.parse.quote(browser_session_id)}"
        state = {**state, "takeover_route": route}
        success(f"Open the Workbench Browser extension to control this session: {route}", state)
        return
    elif api_name == "close":
        state = gateway_request("DELETE", f"/sessions/{encoded_id}", context)
        success("Browser extension session closed.", state)
        return
    else:
        raise RuntimeError(f"unsupported browser API: {api_name}")

    success(content_for(state), state)


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    if mode == "manifest":
        emit(MANIFEST)
        return 0
    if mode == "execute":
        try:
            execute()
        except Exception as error:
            failure("browser_gateway_error", str(error))
        return 0
    sys.stderr.write("usage: browser-plugin.py manifest|execute\n")
    return 64


if __name__ == "__main__":
    raise SystemExit(main())
