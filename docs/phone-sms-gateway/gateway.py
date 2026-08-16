#!/usr/bin/env python3
"""
Reference SMS gateway for running on a spare Android phone via Termux,
instead of buying dedicated GSM modem hardware.

This implements the exact HTTP contract medistock's internal/sms.HTTPGateway
expects:

    POST /send            {"to": "...", "message": "..."} -> {"success": true}
    GET  /inbox/unread     -> [{"id": "...", "from": "...", "body": "..."}]
    POST /inbox/ack        {"id": "..."} -> {"success": true}

Setup on the phone (see README.md in this folder for full details):
    1. Install Termux and Termux:API from F-Droid (NOT the Play Store
       version, which is outdated and incompatible).
    2. In Termux: pkg install python termux-api
    3. Grant the Termux:API app SMS permission when prompted.
    4. Put a SIM card in the phone with signal.
    5. Run: python gateway.py [port] [token]
    6. Find the phone's local IP (Settings > WiFi > network details) and
       point medistock at it: -sms-gateway-url http://<phone-ip>:8090

IMPORTANT: this script has not been tested against a real Termux install in
the environment that generated it - it's written carefully against the
documented termux-api command-line tools, but Termux:API versions do drift.
Before relying on this in an actual emergency, run it once and confirm with
`termux-sms-send --help` / `termux-sms-list --help` that the flags below
still match your installed version.

Keep the phone charged (ideally on a charger) and Termux running in the
foreground or with wake-lock enabled (`termux-wake-lock`), since Android
will otherwise suspend background apps and stop receiving/sending SMS.
"""
import json
import subprocess
import sys
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ACKED_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "acked_ids.json")


def load_acked():
    if os.path.exists(ACKED_FILE):
        try:
            with open(ACKED_FILE) as f:
                return set(json.load(f))
        except Exception:
            return set()
    return set()


def save_acked(acked):
    with open(ACKED_FILE, "w") as f:
        json.dump(list(acked), f)


def send_sms(number, message):
    """Send an SMS via the termux-sms-send CLI tool (part of Termux:API)."""
    result = subprocess.run(
        ["termux-sms-send", "-n", number, message],
        capture_output=True, text=True, timeout=30,
    )
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or "termux-sms-send failed")


def list_inbox(limit=50):
    """List recent inbox SMS via termux-sms-list, returning parsed JSON."""
    result = subprocess.run(
        ["termux-sms-list", "-l", str(limit), "-t", "inbox"],
        capture_output=True, text=True, timeout=30,
    )
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or "termux-sms-list failed")
    return json.loads(result.stdout)


class Handler(BaseHTTPRequestHandler):
    token = None  # set from main()

    def _authorized(self):
        if not Handler.token:
            return True
        return self.headers.get("Authorization") == f"Bearer {Handler.token}"

    def _reply_json(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _read_json_body(self):
        length = int(self.headers.get("Content-Length", 0))
        if length == 0:
            return {}
        return json.loads(self.rfile.read(length))

    def do_GET(self):
        if not self._authorized():
            return self._reply_json(401, {"error": "unauthorized"})

        if self.path == "/inbox/unread":
            try:
                messages = list_inbox()
            except Exception as e:
                return self._reply_json(500, {"error": str(e)})

            acked = load_acked()
            out = []
            for m in messages:
                msg_id = str(m.get("_id") or m.get("id") or m.get("thread_id"))
                if msg_id in acked:
                    continue
                out.append({
                    "id": msg_id,
                    "from": m.get("number", ""),
                    "body": m.get("body", ""),
                })
            return self._reply_json(200, out)

        self._reply_json(404, {"error": "not found"})

    def do_POST(self):
        if not self._authorized():
            return self._reply_json(401, {"error": "unauthorized"})

        try:
            body = self._read_json_body()
        except Exception:
            return self._reply_json(400, {"error": "invalid JSON body"})

        if self.path == "/send":
            to = body.get("to", "")
            message = body.get("message", "")
            if not to or not message:
                return self._reply_json(400, {"success": False, "error": "to and message are required"})
            try:
                send_sms(to, message)
            except Exception as e:
                return self._reply_json(200, {"success": False, "error": str(e)})
            return self._reply_json(200, {"success": True})

        if self.path == "/inbox/ack":
            msg_id = str(body.get("id", ""))
            if not msg_id:
                return self._reply_json(400, {"success": False, "error": "id is required"})
            acked = load_acked()
            acked.add(msg_id)
            save_acked(acked)
            return self._reply_json(200, {"success": True})

        self._reply_json(404, {"error": "not found"})

    def log_message(self, fmt, *args):
        print(f"[gateway] {self.address_string()} - {fmt % args}")


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8090
    token = sys.argv[2] if len(sys.argv) > 2 else ""
    Handler.token = token

    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    print(f"Phone SMS gateway listening on 0.0.0.0:{port}"
          f"{' (token required)' if token else ' (no auth token set)'}")
    print("Point medistock at this with: -sms-gateway-url http://<this-phone-ip>:%d" % port)
    server.serve_forever()


if __name__ == "__main__":
    main()
