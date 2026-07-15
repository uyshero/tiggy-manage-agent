#!/usr/bin/env python3
"""Minimal ClamAV HTTP gateway fixture for Skills integration verification."""

from __future__ import annotations

import hashlib
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


HOST = os.getenv("TMA_CLAMAV_FIXTURE_HOST", "127.0.0.1")
PORT = int(os.getenv("TMA_CLAMAV_FIXTURE_PORT", "3311"))
TOKEN = os.getenv("TMA_CLAMAV_FIXTURE_TOKEN", "fixture-secret")
MAX_BODY_BYTES = 512 * 1024
SCANS: dict[str, dict[str, object]] = {}


class Handler(BaseHTTPRequestHandler):
    server_version = "TMAClamAVFixture/1.0"

    def do_GET(self) -> None:
        if self.path == "/health":
            self._json(200, {"status": "ok"})
            return
        if not self._authorized():
            return
        prefix = "/v1/scans/"
        if not self.path.startswith(prefix):
            self._json(404, {"status": "error", "message": "not found"})
            return
        scan_id = self.path[len(prefix) :]
        result = SCANS.get(scan_id)
        if result is None:
            self._json(404, {"status": "error", "message": "scan not found"})
            return
        self._json(200, result)

    def do_POST(self) -> None:
        if not self._authorized():
            return
        if self.path != "/v1/scan":
            self._json(404, {"status": "error", "message": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            length = 0
        if length <= 0 or length > MAX_BODY_BYTES:
            self._json(413, {"status": "error", "message": "invalid body size"})
            return
        content = self.rfile.read(length)
        digest = hashlib.sha256(content).hexdigest()
        expected_digest = self.headers.get("X-TMA-Content-SHA256", "")
        if expected_digest and expected_digest.lower() != digest:
            self._json(400, {"status": "error", "message": "checksum mismatch"})
            return
        scan_id = digest[:24]
        blocked = b"EICAR-STANDARD-ANTIVIRUS-TEST-FILE" in content.upper()
        result: dict[str, object] = {
            "status": "blocked" if blocked else "passed",
            "scanner": "ClamAV fixture 1.0",
            "scan_id": scan_id,
            "message": "test signature detected" if blocked else "clean",
        }
        if blocked:
            result["signature"] = "Eicar-Signature"
        SCANS[scan_id] = result
        self._json(202, {"status": "pending", "scanner": "ClamAV fixture 1.0", "scan_id": scan_id})

    def log_message(self, format_string: str, *args: object) -> None:
        print(f"clamav-fixture: {format_string % args}", flush=True)

    def _authorized(self) -> bool:
        if self.headers.get("Authorization") == f"Bearer {TOKEN}":
            return True
        self._json(401, {"status": "error", "message": "authorization required"})
        return False

    def _json(self, status: int, payload: dict[str, object]) -> None:
        encoded = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)


if __name__ == "__main__":
    server = ThreadingHTTPServer((HOST, PORT), Handler)
    print(f"clamav-fixture listening on http://{HOST}:{PORT}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
