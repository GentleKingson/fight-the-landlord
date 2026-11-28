import http.client
import json
import os
from pathlib import Path
import subprocess
import sys
import threading
import unittest
from http.server import HTTPServer
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import server


class HTTPTests(unittest.TestCase):
    def setUp(self):
        self.backend = mock.Mock(metadata={"landlord": {"architecture": "v1.3"}})
        patch = mock.patch.object(server, "backend", self.backend)
        patch.start()
        self.addCleanup(patch.stop)
        self.httpd = HTTPServer(("127.0.0.1", 0), server.Handler)
        thread = threading.Thread(target=self.httpd.serve_forever)
        thread.start()
        self.addCleanup(self.httpd.server_close)
        self.addCleanup(thread.join)
        self.addCleanup(self.httpd.shutdown)

    def request(self, method, path, data=None, headers=None):
        connection = http.client.HTTPConnection(*self.httpd.server_address)
        try:
            connection.request(method, path, data, headers or {})
            response = connection.getresponse()
            body = response.read()
            return response.status, json.loads(body) if response.getheader(
                "Content-Type"
            ) == "application/json" else body
        finally:
            connection.close()

    def test_health_and_unknown_routes(self):
        status, body = self.request("GET", "/health")
        self.assertEqual(status, 200)
        self.assertEqual(
            body,
            {
                "status": "ok",
                "backend": "marvel",
                "agents": list(server.POSITIONS),
                "models": self.backend.metadata,
            },
        )
        for method in ("GET", "POST"):
            with self.subTest(method=method):
                self.assertEqual(self.request(method, "/missing")[0], 404)

    def test_play_pass_and_empty_hand(self):
        for hand, must_play, last_move, action in [
            ([3], True, [], [3]),
            ([3], False, [4], []),
        ]:
            with self.subTest(hand=hand, must_play=must_play):
                self.backend.reset_mock()
                self.backend.act.return_value = action
                payload = {
                    "position": "landlord",
                    "hand": hand,
                    "must_play": must_play,
                    "last_move": last_move,
                }
                self.assertEqual(
                    self.request("POST", "/decide_play", json.dumps(payload)),
                    (200, {"action": action}),
                )
                self.backend.act.assert_called_once_with(payload)
        with mock.patch.object(server.traceback, "print_exc"):
            self.backend.reset_mock()
            self.assertEqual(
                self.request(
                    "POST",
                    "/decide_play",
                    json.dumps({"position": "landlord", "hand": []}),
                ),
                (
                    500,
                    {
                        "action": [],
                        "error": "ValueError: model returned an illegal action",
                    },
                ),
            )
            self.backend.act.assert_not_called()

    def test_illegal_action_and_backend_error(self):
        payload = json.dumps({"position": "landlord", "hand": [3]})
        self.backend.act.return_value = [4]
        with mock.patch.object(server.traceback, "print_exc"):
            self.assertEqual(
                self.request("POST", "/decide_play", payload),
                (
                    500,
                    {
                        "action": [],
                        "error": "ValueError: model returned an illegal action",
                    },
                ),
            )
            self.backend.act.side_effect = ValueError("inconsistent history")
            self.assertEqual(
                self.request("POST", "/decide_play", payload),
                (500, {"action": [], "error": "ValueError: inconsistent history"}),
            )

    def test_invalid_request_size_and_json(self):
        for length in (0, 262145):
            with self.subTest(length=length):
                self.assertEqual(
                    self.request(
                        "POST", "/decide_play", headers={"Content-Length": str(length)}
                    ),
                    (400, {"error": "invalid request size"}),
                )
        with mock.patch.object(server.traceback, "print_exc"):
            status, body = self.request("POST", "/decide_play", "{")
        self.assertEqual(status, 500)
        self.assertEqual(body["action"], [])
        self.assertTrue(body["error"].startswith("json.decoder.JSONDecodeError:"))

    def test_backend_configuration(self):
        for value in (None, "marvel", "onnx", "unknown"):
            with self.subTest(value=value):
                env = os.environ.copy()
                env.pop("AI_BACKEND", None)
                if value is not None:
                    env["AI_BACKEND"] = value
                result = subprocess.run(
                    [
                        sys.executable,
                        "-c",
                        "from unittest.mock import patch; import server; print(server.BACKEND)\nwith patch.object(server, 'MarvelBackend'), patch.object(server, 'HTTPServer'): server.main()",
                    ],
                    cwd=Path(server.__file__).parent,
                    env=env,
                    capture_output=True,
                    text=True,
                )
                if value in (None, "marvel"):
                    self.assertEqual(result.stdout.splitlines()[0], "marvel")
                    self.assertEqual(result.returncode, 0, result.stderr)
                else:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(f"Unknown AI_BACKEND: {value}", result.stderr)
