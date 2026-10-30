"""Exercise the installed keyframe handler without starting a streaming server."""
import ast
import inspect
import textwrap
import unittest
from types import SimpleNamespace
from unittest.mock import Mock, patch

from selkies import websockets_mode as streaming


class KeyframeBudget(unittest.TestCase):
    def test_coalesced_request_does_not_consume_viewer_repair_budget(self):
        # Execute the actual REQUEST_KEYFRAME branch, including its continues,
        # without negotiating unrelated audio, input and socket state.
        tree = ast.parse(textwrap.dedent(inspect.getsource(streaming.DataStreamingServer.ws_handler)))
        branch = next(node for node in ast.walk(tree) if isinstance(node, ast.If)
                      and ast.unparse(node.test) == "message == 'REQUEST_KEYFRAME'")
        wrapper = ast.parse("def request(self, websocket, client_display_id, remote_address):\n    for _ in range(1):\n        pass\n")
        wrapper.body[0].body[0].body = branch.body
        namespace = dict(vars(streaming))
        exec(compile(ast.fix_missing_locations(wrapper), "<installed keyframe handler>", "exec"), namespace)
        request = namespace["request"]
        server = streaming.DataStreamingServer.__new__(streaming.DataStreamingServer)
        viewer = object()
        capture = Mock()
        server.display_clients = {"primary": {"ws": object()}}
        server.capture_instances = {"primary": {"module": capture}}
        server._repair_times = {}
        server._last_keyframe_request = {"primary": 99.95}
        server._last_keyframe_log = {}
        server._keyframe_log_suppressed = {}
        now = SimpleNamespace(value=100.0)
        with patch.object(streaming.time, "monotonic", side_effect=lambda: now.value):
            request(server, viewer, "primary", "test-viewer")
            capture.request_idr_frame.assert_not_called()
            now.value = 100.3
            request(server, viewer, "primary", "test-viewer")
            capture.request_idr_frame.assert_called_once()
            # Accepted repairs still enforce the one-second viewer limit.
            now.value = 100.6
            request(server, viewer, "primary", "test-viewer")
            capture.request_idr_frame.assert_called_once()


if __name__ == "__main__":
    unittest.main()
