#!/usr/bin/env python3
import base64
import json
import os
import platform
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from urllib.parse import quote_plus


COMPUTER_APIS = [
    {
        "name": "list_windows",
        "description": "List visible desktop windows or applications.",
        "parameters": {
            "type": "object",
            "properties": {},
            "additionalProperties": False,
        },
        "capabilities": ["computer.window.read"],
        "risk": "read",
    },
    {
        "name": "get_state",
        "description": "Inspect the current desktop state. Uses CUA when available; otherwise returns an AX/UI tree from the local OS when supported.",
        "parameters": {
            "type": "object",
            "properties": {
                "capture_mode": {
                    "type": "string",
                    "enum": ["ax", "screenshot", "vision", "som"],
                    "description": "Requested CUA capture mode. AX is preferred for structured UI tree inspection.",
                },
                "window_id": {"type": "string"},
                "app": {"type": "string"},
            },
            "additionalProperties": False,
        },
        "capabilities": ["computer.state.read", "computer.ax.read"],
        "risk": "read",
    },
    {
        "name": "click",
        "description": "Click at screen coordinates or delegate a CUA click request. pid is optional; when omitted the plugin targets the resolved foreground or named app.",
        "parameters": {
            "type": "object",
            "properties": {
                "pid": {"type": "integer"},
                "app": {"type": "string"},
                "name": {"type": "string"},
                "x": {"type": "number"},
                "y": {"type": "number"},
                "button": {"type": "string", "enum": ["left", "right", "middle"]},
                "window_id": {"type": "string"},
                "element_id": {"type": "string"},
            },
            "additionalProperties": False,
        },
        "capabilities": ["computer.input.mouse"],
        "risk": "write",
    },
    {
        "name": "type_text",
        "description": "Type text into the focused desktop application. pid is optional; when omitted the plugin targets the resolved foreground or named app.",
        "parameters": {
            "type": "object",
            "properties": {
                "pid": {"type": "integer"},
                "app": {"type": "string"},
                "name": {"type": "string"},
                "text": {"type": "string"},
                "delivery_mode": {"type": "string", "enum": ["background", "foreground"]},
            },
            "required": ["text"],
            "additionalProperties": False,
        },
        "capabilities": ["computer.input.keyboard"],
        "risk": "write",
    },
    {
        "name": "hotkey",
        "description": "Press a keyboard shortcut. pid is optional; when omitted the plugin targets the resolved foreground or named app. Key aliases such as Command, Return, Esc, and Option are normalized.",
        "parameters": {
            "type": "object",
            "properties": {
                "pid": {"type": "integer"},
                "app": {"type": "string"},
                "name": {"type": "string"},
                "keys": {
                    "type": "array",
                    "items": {"type": "string"},
                    "minItems": 1,
                }
            },
            "required": ["keys"],
            "additionalProperties": False,
        },
        "capabilities": ["computer.input.keyboard"],
        "risk": "write",
    },
    {
        "name": "launch_app",
        "description": "Launch a local desktop application.",
        "parameters": {
            "type": "object",
            "properties": {"app": {"type": "string"}},
            "required": ["app"],
            "additionalProperties": False,
        },
        "capabilities": ["computer.app.launch"],
        "risk": "write",
    },
    {
        "name": "open_url",
        "description": "Open a URL in a desktop browser. This is a high-level computer action; prefer it over manually chaining launch_app, hotkey, and type_text for navigation.",
        "parameters": {
            "type": "object",
            "properties": {
                "url": {"type": "string"},
                "browser": {"type": "string"},
                "app": {"type": "string"},
            },
            "required": ["url"],
            "additionalProperties": False,
        },
        "capabilities": ["computer.app.launch", "computer.input.keyboard"],
        "risk": "write",
    },
    {
        "name": "search_web",
        "description": "Open a desktop browser search results page for a query. This is a high-level computer action, not a web API search.",
        "parameters": {
            "type": "object",
            "properties": {
                "query": {"type": "string"},
                "engine": {"type": "string", "enum": ["baidu", "google", "bing"]},
                "browser": {"type": "string"},
                "app": {"type": "string"},
            },
            "required": ["query"],
            "additionalProperties": False,
        },
        "capabilities": ["computer.app.launch", "computer.input.keyboard"],
        "risk": "write",
    },
    {
        "name": "bring_to_front",
        "description": "Bring a running application to the foreground.",
        "parameters": {
            "type": "object",
            "properties": {
                "pid": {"type": "integer"},
                "app": {"type": "string"},
                "name": {"type": "string"},
                "bundle_id": {"type": "string"},
            },
            "additionalProperties": False,
        },
        "capabilities": ["computer.window.focus"],
        "risk": "write",
    },
    {
        "name": "screenshot",
        "description": "Capture a desktop screenshot and return it as an exported artifact.",
        "parameters": {
            "type": "object",
            "properties": {},
            "additionalProperties": False,
        },
        "capabilities": ["computer.screen.capture"],
        "risk": "read",
    },
]

MANIFEST = {
    "identifier": "computer",
    "type": "process_plugin",
    "meta": {
        "title": "Computer Use",
        "description": "Desktop computer-use tools backed by CUA or local AX/UI tree inspection. OmniParser is intentionally not included.",
    },
    "system_role": "Use computer.* tools for desktop computer control. Prefer high-level computer.open_url/search_web for browser navigation. For low-level keyboard/mouse actions, pid is optional: the plugin resolves the foreground or named app and normalizes key aliases.",
    "api": [
        dict(
            api,
            namespace="computer",
            api=api["name"],
            runtime={"allowed": ["local_system"], "preferred": "local_system"},
            implementation="worker_capability",
        )
        for api in COMPUTER_APIS
    ],
}


class PluginError(Exception):
    def __init__(self, error_type, message):
        super().__init__(message)
        self.error_type = error_type
        self.message = message


def emit(payload):
    sys.stdout.write(json.dumps(payload, separators=(",", ":"), ensure_ascii=False))


def result(content, state=None, exported_files=None):
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


def run_command(args, stdin=None, timeout=15):
    completed = subprocess.run(
        args,
        input=stdin,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
        check=False,
    )
    if completed.returncode != 0:
        raise PluginError("command_failed", "command failed: " + " ".join(args) + "; " + completed.stderr.strip())
    return completed.stdout.strip()


def backend_name():
    return os.environ.get("TMA_COMPUTER_BACKEND", "auto").strip().lower() or "auto"


def execute():
    request = json.load(sys.stdin)
    call = request.get("call") or {}
    api_name = call.get("api_name") or call.get("name")
    arguments = call.get("arguments") or {}
    if isinstance(arguments, str):
        arguments = json.loads(arguments or "{}")

    backend = backend_name()
    if backend == "stub":
        return execute_stub(api_name, arguments)
    if backend == "cua":
        return execute_cua(api_name, arguments)
    if backend == "ax":
        return execute_ax(api_name, arguments)

    try:
        return execute_cua(api_name, arguments)
    except PluginError as cua_error:
        if api_name in {"list_windows", "get_state", "launch_app", "open_url", "search_web", "screenshot", "type_text", "hotkey", "click"}:
            try:
                return execute_ax(api_name, arguments)
            except PluginError as ax_error:
                raise PluginError(
                    "backend_unavailable",
                    "computer backend unavailable; cua: " + cua_error.message + "; ax: " + ax_error.message,
                )
        raise


def execute_stub(api_name, arguments):
    state = {
        "backend": "stub",
        "platform": platform.system().lower(),
        "marker": "tma-computer-plugin-ok",
        "arguments": arguments,
    }
    if api_name == "list_windows":
        state["windows"] = [{"id": "stub-window", "title": "Stub Desktop", "app": "Stub"}]
        return result("tma-computer-plugin-ok list_windows", state)
    if api_name == "get_state":
        state["ui_tree"] = {
            "role": "desktop",
            "name": "Stub Desktop",
            "children": [{"id": "stub-button", "role": "button", "name": "OK"}],
        }
        return result("tma-computer-plugin-ok get_state", state)
    if api_name in {"click", "type_text", "hotkey", "launch_app", "open_url", "search_web", "bring_to_front", "screenshot"}:
        state["action"] = api_name
        return result("tma-computer-plugin-ok " + api_name, state)
    raise PluginError("unsupported_api", "unsupported computer api: " + str(api_name))


def execute_cua(api_name, arguments):
    if api_name == "open_url":
        state = cua_open_url(arguments)
        return result("computer.open_url completed via cua", cua_state(api_name, "open_url", state))
    if api_name == "search_web":
        state = cua_search_web(arguments)
        return result("computer.search_web completed via cua", cua_state(api_name, "search_web", state))
    cua_tool, cua_arguments = cua_tool_call(api_name, arguments)
    payload = run_cua_tool(cua_tool, cua_arguments)
    state = payload if isinstance(payload, dict) else {"output": payload}
    exported_files = None
    if api_name == "screenshot":
        state, exported_files = materialize_cua_screenshot(state)
    return result("computer." + api_name + " completed via cua", cua_state(api_name, cua_tool, state), exported_files)


def cua_tool_call(api_name, arguments):
    arguments = dict(arguments or {})
    if api_name == "get_state":
        return "get_accessibility_tree", arguments
    if api_name == "launch_app":
        if "app" in arguments and "name" not in arguments and "bundle_id" not in arguments:
            arguments["name"] = arguments.pop("app")
        return "launch_app", arguments
    if api_name == "bring_to_front":
        if "pid" in arguments:
            return "bring_to_front", {"pid": int(arguments["pid"])}
        launch_arguments = dict(arguments)
        if "app" in launch_arguments and "name" not in launch_arguments and "bundle_id" not in launch_arguments:
            launch_arguments["name"] = launch_arguments.pop("app")
        launched = run_cua_tool("launch_app", launch_arguments)
        if not isinstance(launched, dict) or "pid" not in launched:
            raise PluginError("cua_call_failed", "bring_to_front requires pid or launch_app result with pid")
        return "bring_to_front", {"pid": int(launched["pid"])}
    if api_name in {"click", "type_text", "hotkey"}:
        arguments = prepare_cua_targeted_input(api_name, arguments)
    if api_name == "screenshot":
        return "get_desktop_state", arguments
    return api_name, arguments


def prepare_cua_targeted_input(api_name, arguments):
    prepared = dict(arguments or {})
    for key in ("app", "name", "browser"):
        prepared.pop(key, None)
    if api_name == "hotkey":
        prepared["keys"] = normalize_keys(prepared.get("keys") or [])
    if api_name == "type_text" and "delivery_mode" not in prepared:
        prepared["delivery_mode"] = "foreground"
    if "pid" not in prepared or prepared.get("pid") in ("", None):
        target_pid = resolve_cua_target_pid(arguments)
        if target_pid is not None:
            prepared["pid"] = int(target_pid)
    return prepared


def normalize_keys(keys):
    aliases = {
        "command": "cmd",
        "cmd": "cmd",
        "⌘": "cmd",
        "control": "ctrl",
        "ctrl": "ctrl",
        "option": "alt",
        "alt": "alt",
        "return": "enter",
        "enter": "enter",
        "escape": "escape",
        "esc": "escape",
        "spacebar": "space",
        "space": "space",
        "delete": "backspace",
    }
    normalized = []
    for key in keys:
        text = str(key).strip().lower()
        text = text.replace(" ", "")
        if not text:
            continue
        normalized.append(aliases.get(text, text))
    return normalized


def cua_open_url(arguments):
    url = str(arguments.get("url") or "").strip()
    if not url:
        raise PluginError("invalid_arguments", "open_url requires url")
    browser = preferred_browser(arguments)
    launched = cua_launch_and_focus(browser)
    pid = int(launched["pid"])
    run_cua_tool("hotkey", {"pid": pid, "keys": ["cmd", "l"]})
    typed = run_cua_tool("type_text", {"pid": pid, "text": url, "delivery_mode": "foreground"})
    submitted = run_cua_tool("hotkey", {"pid": pid, "keys": ["enter"]})
    return {
        "browser": browser,
        "pid": pid,
        "url": url,
        "launch": launched,
        "type_text": typed,
        "submit": submitted,
    }


def cua_search_web(arguments):
    query = str(arguments.get("query") or "").strip()
    if not query:
        raise PluginError("invalid_arguments", "search_web requires query")
    engine = str(arguments.get("engine") or os.environ.get("TMA_COMPUTER_SEARCH_ENGINE") or "baidu").strip().lower()
    url = search_url(engine, query)
    state = cua_open_url(dict(arguments, url=url))
    state["query"] = query
    state["engine"] = engine
    return state


def search_url(engine, query):
    encoded = quote_plus(query)
    if engine == "google":
        return "https://www.google.com/search?q=" + encoded
    if engine == "bing":
        return "https://www.bing.com/search?q=" + encoded
    return "https://www.baidu.com/s?wd=" + encoded


def preferred_browser(arguments):
    browser = str(arguments.get("browser") or arguments.get("app") or "").strip()
    if browser:
        return browser
    return os.environ.get("TMA_COMPUTER_DEFAULT_BROWSER", "Google Chrome").strip() or "Google Chrome"


def cua_launch_and_focus(app):
    launched = run_cua_tool("launch_app", {"name": app})
    if not isinstance(launched, dict) or "pid" not in launched:
        raise PluginError("cua_call_failed", "launch_app did not return pid for " + app)
    pid = int(launched["pid"])
    try:
        focused = run_cua_tool("bring_to_front", {"pid": pid})
        launched = dict(launched)
        launched["bring_to_front"] = focused
    except PluginError:
        pass
    return launched


def resolve_cua_target_pid(arguments):
    target = str(arguments.get("app") or arguments.get("name") or arguments.get("browser") or "").strip()
    if target:
        try:
            launched = cua_launch_and_focus(target)
            return int(launched["pid"])
        except PluginError:
            pass
    for tool in ("get_accessibility_tree", "list_windows"):
        try:
            state = run_cua_tool(tool, {})
        except PluginError:
            continue
        pid = pid_from_cua_state(state, target)
        if pid is not None:
            return pid
    return None


def pid_from_cua_state(state, target=""):
    if not isinstance(state, dict):
        return None
    target_lower = target.lower()
    windows = state.get("windows") or []
    apps = state.get("apps") or []
    if target_lower:
        for item in list(windows) + list(apps):
            if not isinstance(item, dict):
                continue
            haystack = " ".join(
                str(item.get(key) or "")
                for key in ("app_name", "name", "bundle_id", "title")
            ).lower()
            if target_lower in haystack and item.get("pid") is not None:
                return int(item["pid"])
    for item in windows:
        if isinstance(item, dict) and item.get("pid") is not None:
            return int(item["pid"])
    for item in apps:
        if isinstance(item, dict) and item.get("pid") is not None:
            return int(item["pid"])
    return None


def cua_state(api_name, cua_tool, state):
    payload = {"backend": "cua", "cua_tool": cua_tool, "result": state}
    if api_name == "get_state":
        payload["ui_tree"] = {
            "role": "desktop",
            "name": "desktop",
            "apps": state.get("apps", []) if isinstance(state, dict) else [],
            "windows": state.get("windows", []) if isinstance(state, dict) else [],
        }
    return payload


def materialize_cua_screenshot(state):
    if not isinstance(state, dict):
        return state, None
    screenshot_b64 = state.get("screenshot_png_b64")
    if not screenshot_b64:
        return state, None
    try:
        screenshot_bytes = base64.b64decode(str(screenshot_b64), validate=True)
    except Exception as err:
        raise PluginError("cua_call_failed", "invalid screenshot_png_b64 from CUA: " + str(err))

    output_dir = Path(os.environ.get("TMA_COMPUTER_OUTPUT_DIR", tempfile.gettempdir()))
    output_dir.mkdir(parents=True, exist_ok=True)
    screenshot_path = output_dir / ("tma-computer-screenshot-" + str(int(time.time())) + ".png")
    screenshot_path.write_bytes(screenshot_bytes)

    sanitized = dict(state)
    sanitized.pop("screenshot_png_b64", None)
    sanitized["has_screenshot"] = True
    sanitized["screenshot_path"] = str(screenshot_path)
    return sanitized, [
        {
            "path": str(screenshot_path),
            "name": "computer-screenshot.png",
            "artifact_type": "asset",
            "content_type": sanitized.get("screenshot_mime_type") or "image/png",
        }
    ]


def run_cua_tool(api_name, arguments):
    args_json = json.dumps(arguments or {}, separators=(",", ":"))
    template = os.environ.get("TMA_COMPUTER_CUA_TEMPLATE", "").strip()
    if template:
        command = template.format(tool=shlex.quote(api_name), args_json=shlex.quote(args_json))
        output = run_command(["sh", "-c", command], timeout=int(os.environ.get("TMA_COMPUTER_TIMEOUT", "30")))
        return parse_json_or_text(output)

    command_name = os.environ.get("TMA_COMPUTER_CUA_CMD", "cua-driver").strip() or "cua-driver"
    command_path = shutil.which(command_name)
    if not command_path:
        raise PluginError("cua_unavailable", "CUA command not found: " + command_name)

    attempts = [
        [command_path, "call", api_name, args_json],
        [command_path, "tools", "call", api_name, args_json],
        [command_path, api_name, args_json],
    ]
    last_error = ""
    for args in attempts:
        try:
            output = run_command(args, timeout=int(os.environ.get("TMA_COMPUTER_TIMEOUT", "30")))
            return parse_json_or_text(output)
        except PluginError as err:
            last_error = err.message
    raise PluginError("cua_call_failed", last_error or "CUA command failed")


def parse_json_or_text(output):
    try:
        return json.loads(output)
    except json.JSONDecodeError:
        return {"stdout": output}


def execute_ax(api_name, arguments):
    system = platform.system().lower()
    if api_name == "list_windows":
        windows = ax_list_windows(system)
        return result("computer.list_windows completed via ax", {"backend": "ax", "windows": windows})
    if api_name == "get_state":
        state = ax_get_state(system, arguments)
        return result("computer.get_state completed via ax", state)
    if api_name == "launch_app":
        return ax_launch_app(system, arguments)
    if api_name == "open_url":
        return ax_open_url(system, arguments)
    if api_name == "search_web":
        query = str(arguments.get("query") or "").strip()
        if not query:
            raise PluginError("invalid_arguments", "search_web requires query")
        engine = str(arguments.get("engine") or os.environ.get("TMA_COMPUTER_SEARCH_ENGINE") or "baidu").strip().lower()
        return ax_open_url(system, dict(arguments, url=search_url(engine, query), query=query, engine=engine))
    if api_name == "bring_to_front":
        return ax_bring_to_front(system, arguments)
    if api_name == "type_text":
        return ax_type_text(system, arguments)
    if api_name == "hotkey":
        return ax_hotkey(system, arguments)
    if api_name == "click":
        return ax_click(system, arguments)
    if api_name == "screenshot":
        return ax_screenshot(system)
    raise PluginError("unsupported_api", "unsupported computer api: " + str(api_name))


def ax_list_windows(system):
    if system == "darwin":
        script = '''
tell application "System Events"
  set rows to {}
  repeat with proc in application processes
    if visible of proc is true then
      set procName to name of proc
      repeat with win in windows of proc
        try
          set end of rows to procName & "\t" & name of win
        end try
      end repeat
    end if
  end repeat
  return rows as text
end tell
'''
        output = run_osascript(script)
        return [{"app": parts[0], "title": parts[1], "id": parts[0] + ":" + parts[1]} for parts in split_tsv_lines(output, 2)]
    if system == "linux" and shutil.which("wmctrl"):
        output = run_command(["wmctrl", "-l"])
        windows = []
        for line in output.splitlines():
            parts = line.split(None, 3)
            if len(parts) >= 4:
                windows.append({"id": parts[0], "desktop": parts[1], "host": parts[2], "title": parts[3]})
        return windows
    if system == "windows":
        output = run_command(
            [
                "powershell",
                "-NoProfile",
                "-Command",
                "Get-Process | Where-Object {$_.MainWindowTitle} | Select-Object Id,ProcessName,MainWindowTitle | ConvertTo-Json -Compress",
            ]
        )
        parsed = parse_json_or_text(output)
        if isinstance(parsed, list):
            return [{"id": str(item.get("Id")), "app": item.get("ProcessName"), "title": item.get("MainWindowTitle")} for item in parsed]
        if isinstance(parsed, dict) and parsed.get("Id"):
            return [{"id": str(parsed.get("Id")), "app": parsed.get("ProcessName"), "title": parsed.get("MainWindowTitle")}]
        return []
    raise PluginError("ax_unavailable", "AX window listing is not available on " + system)


def ax_get_state(system, arguments):
    if system != "darwin":
        return {
            "backend": "ax",
            "platform": system,
            "ui_tree": {"role": "desktop", "name": "unsupported", "children": []},
            "warning": "Structured AX/UI tree fallback is currently implemented for macOS System Events only.",
        }
    script = '''
tell application "System Events"
  set frontProc to first application process whose frontmost is true
  set appName to name of frontProc
  set windowName to ""
  try
    set windowName to name of front window of frontProc
  end try
  set rows to {}
  try
    repeat with itemRef in UI elements of front window of frontProc
      set itemRole to ""
      set itemName to ""
      try
        set itemRole to role of itemRef
      end try
      try
        set itemName to name of itemRef
      end try
      set end of rows to itemRole & "\t" & itemName
    end repeat
  end try
  return appName & "\n" & windowName & "\n" & (rows as text)
end tell
'''
    output = run_osascript(script)
    lines = output.splitlines()
    app_name = lines[0] if len(lines) > 0 else ""
    window_name = lines[1] if len(lines) > 1 else ""
    children = []
    for index, parts in enumerate(split_tsv_lines("\n".join(lines[2:]), 2)):
        children.append({"id": "ax-" + str(index + 1), "role": parts[0], "name": parts[1]})
    return {
        "backend": "ax",
        "platform": "darwin",
        "capture_mode": arguments.get("capture_mode") or "ax",
        "ui_tree": {
            "role": "application",
            "name": app_name,
            "children": [{"role": "window", "name": window_name, "children": children}],
        },
        "coverage": "macOS System Events top-level AX elements",
    }


def ax_launch_app(system, arguments):
    app = str(arguments.get("app") or "").strip()
    if not app:
        raise PluginError("invalid_arguments", "launch_app requires app")
    if system == "darwin":
        run_command(["open", "-a", app])
    elif system == "windows":
        run_command(["powershell", "-NoProfile", "-Command", "Start-Process " + json.dumps(app)])
    elif system == "linux":
        run_command([app])
    else:
        raise PluginError("ax_unavailable", "launch_app unsupported on " + system)
    return result("computer.launch_app completed via ax", {"backend": "ax", "app": app})


def ax_open_url(system, arguments):
    url = str(arguments.get("url") or "").strip()
    if not url:
        raise PluginError("invalid_arguments", "open_url requires url")
    if system == "darwin":
        browser = str(arguments.get("browser") or arguments.get("app") or "").strip()
        if browser:
            run_command(["open", "-a", browser, url])
        else:
            run_command(["open", url])
    elif system == "windows":
        run_command(["powershell", "-NoProfile", "-Command", "Start-Process " + json.dumps(url)])
    elif system == "linux" and shutil.which("xdg-open"):
        run_command(["xdg-open", url])
    else:
        raise PluginError("ax_unavailable", "open_url unsupported on " + system)
    state = {"backend": "ax", "url": url}
    for key in ("browser", "app", "query", "engine"):
        if arguments.get(key):
            state[key] = arguments.get(key)
    return result("computer.open_url completed via ax", state)


def ax_bring_to_front(system, arguments):
    app = str(arguments.get("app") or arguments.get("name") or "").strip()
    if system == "darwin":
        if not app:
            raise PluginError("invalid_arguments", "bring_to_front AX fallback requires app or name")
        run_osascript('tell application ' + json.dumps(app) + ' to activate')
        return result("computer.bring_to_front completed via ax", {"backend": "ax", "app": app})
    raise PluginError("ax_unavailable", "bring_to_front AX fallback is currently implemented for macOS only")


def ax_type_text(system, arguments):
    text = str(arguments.get("text") or "")
    if system == "darwin":
        run_osascript('tell application "System Events" to keystroke ' + json.dumps(text))
        return result("computer.type_text completed via ax", {"backend": "ax", "text_length": len(text)})
    raise PluginError("ax_unavailable", "type_text AX fallback is currently implemented for macOS only")


def ax_hotkey(system, arguments):
    keys = [str(key).lower() for key in arguments.get("keys") or []]
    if system == "darwin":
        key = next((item for item in keys if item not in {"cmd", "command", "ctrl", "control", "alt", "option", "shift"}), "")
        modifiers = []
        if "cmd" in keys or "command" in keys:
            modifiers.append("command down")
        if "ctrl" in keys or "control" in keys:
            modifiers.append("control down")
        if "alt" in keys or "option" in keys:
            modifiers.append("option down")
        if "shift" in keys:
            modifiers.append("shift down")
        if not key:
            raise PluginError("invalid_arguments", "hotkey requires a non-modifier key")
        using = " using {" + ", ".join(modifiers) + "}" if modifiers else ""
        run_osascript('tell application "System Events" to keystroke ' + json.dumps(key) + using)
        return result("computer.hotkey completed via ax", {"backend": "ax", "keys": keys})
    raise PluginError("ax_unavailable", "hotkey AX fallback is currently implemented for macOS only")


def ax_click(system, arguments):
    if system == "darwin" and shutil.which("cliclick"):
        x = arguments.get("x")
        y = arguments.get("y")
        if x is None or y is None:
            raise PluginError("invalid_arguments", "AX click fallback requires x and y")
        run_command(["cliclick", "c:" + str(int(x)) + "," + str(int(y))])
        return result("computer.click completed via ax", {"backend": "ax", "x": x, "y": y})
    raise PluginError("ax_unavailable", "click fallback requires CUA, or macOS with cliclick installed")


def ax_screenshot(system):
    output_dir = Path(os.environ.get("TMA_COMPUTER_OUTPUT_DIR", tempfile.gettempdir()))
    output_dir.mkdir(parents=True, exist_ok=True)
    screenshot_path = output_dir / ("tma-computer-screenshot-" + str(int(time.time())) + ".png")
    if system == "darwin":
        run_command(["screencapture", "-x", str(screenshot_path)])
    elif system == "linux" and shutil.which("gnome-screenshot"):
        run_command(["gnome-screenshot", "-f", str(screenshot_path)])
    elif system == "windows":
        raise PluginError("ax_unavailable", "screenshot fallback currently requires CUA on Windows")
    else:
        raise PluginError("ax_unavailable", "no screenshot backend available on " + system)
    return result(
        "computer.screenshot completed via ax",
        {"backend": "ax", "screenshot_path": str(screenshot_path)},
        [{"path": str(screenshot_path), "name": "computer-screenshot.png", "artifact_type": "asset", "content_type": "image/png"}],
    )


def run_osascript(script):
    if not shutil.which("osascript"):
        raise PluginError("ax_unavailable", "osascript not found")
    return run_command(["osascript", "-e", script])


def split_tsv_lines(output, expected):
    rows = []
    for line in output.splitlines():
        parts = line.split("\t")
        if len(parts) >= expected:
            rows.append(parts[:expected])
    return rows


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    if mode == "manifest":
        emit(MANIFEST)
        return 0
    if mode == "execute":
        try:
            execute()
        except PluginError as err:
            failure(err.error_type, err.message)
        except Exception as err:
            failure("plugin_exception", str(err))
        return 0
    sys.stderr.write("usage: computer-plugin.py manifest|execute\n")
    return 64


if __name__ == "__main__":
    raise SystemExit(main())
