import importlib.util
import io
from pathlib import Path
import unittest
import json
import socket
import threading
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

spec = importlib.util.spec_from_file_location('feed_benchmark', Path(__file__).with_name('benchmark-feed-open.py'))
bench = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bench)

class FeedBenchmarkTests(unittest.TestCase):
    def test_exact_oracle_and_metadata(self):
        page = [{'id': '2', 'visibility': 'public', 'account': {'id': '7'}, 'content': '<p>test</p>'}]
        case = {'limit': 20, 'expected_ids': ['2'], 'expected_signature': bench.page_signature(page)}
        self.assertEqual([], bench.validate(page, case)[0])
        changed = [{'id': '2', 'visibility': 'direct', 'account': {'id': '7'}, 'content': '<p>test</p>'}]
        self.assertIn('oracle_metadata_mismatch', bench.validate(changed, case)[0])
        self.assertIn('oracle_ids_mismatch', bench.validate([], case)[0])

    def test_embedded_author_and_media_are_in_signature(self):
        page = [{'id': '2', 'account': {'id': '1'}, 'reblog': {'id': '1', 'account': {'id': '3'}}}]
        before = bench.page_signature(page)
        page[0]['reblog']['account']['id'] = '4'
        self.assertNotEqual(before, bench.page_signature(page))
        before = bench.page_signature(page)
        page[0]['reblog']['media_attachments'] = [{'id': '100', 'type': 'image'}]
        self.assertNotEqual(before, bench.page_signature(page))

    def test_drops_count_against_offered_work_not_hidden_by_success_latency(self):
        rows = [{'status': 200, 'errors': [], 'service_ms': 2, 'end_to_end_ms': 12, 'send_lag_ms': 10}, {'dropped': True}, {'status': 503, 'errors': [], 'service_ms': 20, 'end_to_end_ms': 30, 'send_lag_ms': 10}]
        result = bench.summarize(rows, 1, {'name': 'test', 'rate': 3})
        self.assertEqual(1, result['dropped'])
        self.assertEqual(1 / 3, result['arrival_success_ratio'])
        self.assertEqual(1, result['valid_rps'])
        self.assertEqual(1, result['http_errors'])
        self.assertFalse(result['correctness_qualified'])
        self.assertEqual(30, result['scheduled_to_complete_ms']['0.99'])

    def test_exclusive_cursor_and_duplicate_rejection(self):
        page = [{'id': '2'}, {'id': '2'}]
        errors, _ = bench.validate(page, {'limit': 1, 'max_id': 2})
        self.assertEqual({'oversized_page', 'duplicate_id', 'not_descending', 'cursor_not_exclusive'}, set(errors))

class FeedRedisObserverTests(unittest.TestCase):
    def test_resp_nil_and_scores_are_distinct(self):
        spec = importlib.util.spec_from_file_location('fanout_benchmark', Path(__file__).with_name('benchmark-feed-fanout.py'))
        fanout = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(fanout)
        stream = io.BytesIO(b'$-1\r\n$3\r\n123\r\n:1\r\n+OK\r\n')
        self.assertIsNone(fanout.response(stream))
        self.assertEqual(b'123', fanout.response(stream))
        self.assertEqual(1, fanout.response(stream))
        self.assertEqual(b'OK', fanout.response(stream))
        with self.assertRaises(ValueError):
            fanout.response(io.BytesIO(b'-ERR simulated\r\n'))
        self.assertEqual(b'*2\r\n$6\r\nSELECT\r\n$1\r\n1\r\n', fanout.command('SELECT', 1))

@contextmanager
def idle_test_server(disconnect=False):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'
        def setup(self):
            super().setup()
            self.connection.settimeout(.1)
        def log_message(self, *_):
            pass
        def do_GET(self):
            self.server.requests += 1
            if disconnect:
                self.close_connection = True
                self.connection.shutdown(socket.SHUT_RDWR)
                return
            body = b'[]'
            self.send_response(200)
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    server.daemon_threads = True
    server.requests = 0
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server, f'http://127.0.0.1:{server.server_port}'
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


class FeedConnectionIdleTests(unittest.TestCase):
    case = {'name': 'idle', 'path': '/', 'rate': 2, 'expected_ids': [],
            'expected_signature': bench.page_signature([])}

    def test_short_server_idle_reproduces_failure_without_early_recycle(self):
        with idle_test_server() as (server, base):
            raw = io.StringIO()
            result = bench.run_case(base, '', self.case, .6, 1, 1, raw, connection_max_idle=30)
            rows = [json.loads(line) for line in raw.getvalue().splitlines()]
            self.assertEqual(1, result['valid'])
            self.assertEqual(1, result['http_errors'])
            self.assertIn('RemoteDisconnected', rows[1]['errors'])
            self.assertEqual(0, result['connections_recycled'])
            self.assertEqual(1, server.requests)

    def test_expired_idle_connection_recycled_before_next_request(self):
        with idle_test_server() as (server, base):
            raw = io.StringIO()
            result = bench.run_case(base, '', self.case, .6, 1, 1, raw, connection_max_idle=.05)
            rows = [json.loads(line) for line in raw.getvalue().splitlines()]
            self.assertEqual(2, result['valid'])
            self.assertEqual(0, result['http_errors'])
            self.assertEqual(1, result['connections_recycled'])
            self.assertTrue(rows[1]['connection_recycled'])
            self.assertGreater(rows[1]['connection_idle_s'], .05)
            self.assertGreater(rows[1]['service_ms'], 0)
            self.assertEqual(2, server.requests)

    def test_active_disconnect_remains_failure_without_retry(self):
        with idle_test_server(disconnect=True) as (server, base):
            raw = io.StringIO()
            result = bench.run_case(base, '', self.case, .1, 1, 1, raw, connection_max_idle=.05)
            rows = [json.loads(line) for line in raw.getvalue().splitlines()]
            self.assertEqual(0, result['valid'])
            self.assertEqual(1, result['http_errors'])
            self.assertEqual(1, server.requests)
            self.assertIn('RemoteDisconnected', rows[0]['errors'])
            self.assertEqual(0, rows[0]['status'])

    def test_idle_budget_must_be_positive_and_finite(self):
        for budget in [0, -1, float('nan'), float('inf')]:
            with self.assertRaises(ValueError):
                bench.run_case('http://127.0.0.1:1', '', self.case, .1, 1, 1, io.StringIO(), budget)

if __name__ == '__main__':
    unittest.main()
