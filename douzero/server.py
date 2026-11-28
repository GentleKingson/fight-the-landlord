#!/usr/bin/env python3
"""MARVEL HTTP inference service using the existing DouZero wire protocol."""

import json
import os
import traceback
from http.server import BaseHTTPRequestHandler, HTTPServer

from src.inference import legal_actions_for_request
from src.marvel_backend import MarvelBackend, POSITIONS

MODEL_DIR = os.environ.get(
    "DOUZERO_MODELS", os.path.join(os.path.dirname(__file__), "models")
)
PORT = int(os.environ.get("PORT", 2021))
BACKEND = os.environ.get("AI_BACKEND", "marvel")
backend = None


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def do_GET(self):
        if self.path == "/health":
            self._json(
                200,
                {
                    "status": "ok",
                    "backend": BACKEND,
                    "agents": POSITIONS,
                    "models": backend.metadata if backend else {},
                },
            )
        else:
            self.send_error(404)

    def do_POST(self):
        if self.path != "/decide_play":
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length", 0))
            if not 0 < length <= 262144:
                self._json(400, {"error": "invalid request size"})
                return
            body = json.loads(self.rfile.read(length))
            body["position"]  # Required by the request contract, including empty hands.
            legal_actions = legal_actions_for_request(body)

            if not legal_actions:
                action = []
            else:
                action = backend.act(body)
                action = list(action) if action is not None else []

            if sorted(action) not in legal_actions:
                raise ValueError("model returned an illegal action")
            self._json(200, {"action": action})
        except Exception:
            traceback.print_exc()
            err = traceback.format_exc().splitlines()[-1]
            self._json(500, {"action": [], "error": err})

    def _json(self, code: int, obj: dict) -> None:
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    global backend
    if BACKEND != "marvel":
        raise ValueError(f"Unknown AI_BACKEND: {BACKEND}")
    backend = MarvelBackend(MODEL_DIR)
    print("Loaded MARVEL landlord.pt and farmers.pt", flush=True)
    server = HTTPServer(("0.0.0.0", PORT), Handler)
    print(f"\nDouZero service listening on port {PORT}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nDouZero service stopped.")
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
