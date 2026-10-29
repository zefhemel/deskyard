"""Transport policy regressions; run directly without an engine or supervisor.

    .venv/bin/python -m unittest test_netchange
"""

import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from types import SimpleNamespace
from unittest.mock import Mock

from playwright.sync_api import Error, sync_playwright

import netchange
from test_ui_audio import settle_audio


def loopback_route(path, method="POST"):
    request = SimpleNamespace(url=f"http://127.0.0.1:8080{path}", method=method, resource_type="fetch")
    return Mock(request=request)


def dispatch(route):
    # Match the same two registrations that a Chromium context uses.
    if netchange.LOOPBACK.match(route.request.url) or netchange.MAPPED_API.match(route.request.url):
        netchange._request_transport(route)
    else:
        route.fallback()


class CaptureTransportTests(unittest.TestCase):
    def test_capture_reports_use_request_transport_once(self):
        route = loopback_route("/api/computers/desktop-1/mic")
        dispatch(route)
        route.fetch.assert_called_once_with(max_redirects=0, max_retries=0)
        route.fulfill.assert_called_once_with(response=route.fetch.return_value)
        route.fallback.assert_not_called()

    def test_failed_capture_is_aborted_without_replaying_it(self):
        route = loopback_route("/api/computers/desktop-1/mic")
        route.fetch.side_effect = Error("connection lost after sending request")
        dispatch(route)
        route.fetch.assert_called_once_with(max_redirects=0, max_retries=0)
        route.abort.assert_called_once_with("failed")
        route.fulfill.assert_not_called()
        route.fallback.assert_not_called()

    def test_other_mutations_and_streams_remain_native(self):
        for path, method in (
            ("/api/computers", "POST"),
            ("/api/computers/desktop-1", "PATCH"),
            ("/api/computers/desktop-1/restart", "POST"),
            ("/api/computers/desktop-1/mic", "DELETE"),
            ("/desktop-1/screen/api/websockets", "GET"),
            ("/api/events", "GET"),
        ):
            with self.subTest(path=path, method=method):
                route = loopback_route(path, method)
                dispatch(route)
                route.fallback.assert_called_once()
                route.fetch.assert_not_called()

    def test_mapped_insecure_hosts_keep_capture_reports_native(self):
        route = loopback_route("/api/computers/desktop-1/mic")
        route.request.url = route.request.url.replace("127.0.0.1", "deskyard.invalid")
        dispatch(route)
        route.fallback.assert_called_once()
        route.fetch.assert_not_called()

    def test_remote_hosts_keep_capture_reports_native(self):
        route = loopback_route("/api/computers/desktop-1/mic")
        route.request.url = route.request.url.replace("127.0.0.1", "example.com")
        dispatch(route)
        route.fallback.assert_called_once()
        route.fetch.assert_not_called()


class CaptureRequestTests(unittest.TestCase):
    def test_browser_report_preserves_body_auth_and_response(self):
        received = []

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.send_header("Content-Type", "text/html")
                self.send_header("Set-Cookie", "session=viewer; Path=/")
                self.end_headers()
                self.wfile.write(b"<body>capture transport</body>")

            def do_POST(self):
                body = self.rfile.read(int(self.headers["Content-Length"]))
                received.append((self.path, self.headers["Cookie"], self.headers["Content-Type"], json.loads(body)))
                self.send_response(409)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b'{"error":"no stream"}')

            def log_message(self, *_):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with sync_playwright() as p:
                browser = p.chromium.launch()
                try:
                    page = browser.new_page()
                    page.goto(f"http://127.0.0.1:{server.server_port}/")
                    result = page.evaluate("""async () => {
                        const r = await fetch('/api/computers/desktop-1/mic', {
                            method: 'POST', headers: {'Content-Type': 'application/json'},
                            body: JSON.stringify({on: true, tab: 'viewer-tab', seq: 7})
                        });
                        return {status: r.status, body: await r.json()};
                    }""")
                    self.assertEqual(result, {"status": 409, "body": {"error": "no stream"}})
                    self.assertEqual(received, [("/api/computers/desktop-1/mic", "session=viewer", "application/json",
                                                 {"on": True, "tab": "viewer-tab", "seq": 7})])
                    # A rapid capture toggle schedules its badge report after
                    # the click returns. Python must dispatch its route while
                    # the test waits for the browser's media state to settle.
                    page.evaluate("""() => {
                        setTimeout(() => fetch('/api/computers/desktop-1/mic', {
                            method: 'POST', headers: {'Content-Type': 'application/json'},
                            body: JSON.stringify({on: false, tab: 'viewer-tab', seq: 8})
                        }), 50);
                    }""")
                    settle_audio(page, 0.25)
                    self.assertEqual(len(received), 2, "settling blocked the pending badge report")
                    self.assertEqual(received[1][3], {"on": False, "tab": "viewer-tab", "seq": 8})
                finally:
                    browser.close()
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == "__main__":
    unittest.main()
