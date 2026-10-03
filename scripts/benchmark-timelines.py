#!/usr/bin/env python3
"""Read-only, closed-loop HTTP timeline benchmark using Python's standard library."""
import argparse
import concurrent.futures
import http.client
import json
import math
import os
from pathlib import Path
import stat
import time
from urllib.parse import urlencode, urlsplit, quote


def validate_page(data, limit, max_id):
    """Structural checks only: this is deliberately not an authorization oracle."""
    if not isinstance(data, list):
        return ['response_not_array'], -1
    errors = set()
    if len(data) > limit:
        errors.add('page_exceeds_limit')
    ids = []
    for item in data:
        if not isinstance(item, dict) or not isinstance(item.get('id'), str) or not item['id'].isdigit():
            errors.add('invalid_status_id')
            continue
        ids.append(int(item['id']))
    if len(ids) != len(set(ids)):
        errors.add('duplicate_status_id')
    if max_id is not None and any(i >= max_id for i in ids):
        errors.add('max_id_not_exclusive')
    if any(left <= right for left, right in zip(ids, ids[1:])):
        errors.add('ids_not_descending')
    return sorted(errors), len(data)


def benchmark(base, token, path, concurrency, duration, timeout, limit, max_id):
    parsed = urlsplit(base)
    connection = http.client.HTTPSConnection if parsed.scheme == 'https' else http.client.HTTPConnection
    started = time.perf_counter()
    deadline = started + duration

    def worker():
        client = connection(parsed.hostname, parsed.port, timeout=timeout)
        rows = []
        while time.perf_counter() < deadline:
            tick = time.perf_counter()
            status, size, count, errors = 0, 0, -1, []
            try:
                headers = {'Accept': 'application/json'}
                if token:
                    headers['Authorization'] = 'Bearer ' + token
                client.request('GET', path, headers=headers)
                response = client.getresponse()
                status = response.status
                body = response.read()
                size = len(body)
                elapsed = (time.perf_counter() - tick) * 1000
                if status == 200:
                    try:
                        errors, count = validate_page(json.loads(body), limit, max_id)
                    except (ValueError, UnicodeError):
                        errors = ['invalid_json']
            except (OSError, http.client.HTTPException) as exc:
                elapsed = (time.perf_counter() - tick) * 1000
                errors = [type(exc).__name__]
                client.close()
                client = connection(parsed.hostname, parsed.port, timeout=timeout)
            rows.append((elapsed, status, size, count, errors))
        client.close()
        return rows

    rows = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
        for batch in pool.map(lambda _: worker(), range(concurrency)):
            rows.extend(batch)
    elapsed = time.perf_counter() - started
    latencies = sorted(row[0] for row in rows)

    def percentile(p):
        return round(latencies[max(0, math.ceil(len(latencies) * p) - 1)], 3) if latencies else None

    codes = {}
    for row in rows:
        codes[str(row[1])] = codes.get(str(row[1]), 0) + 1
    return dict(path=path, concurrency=concurrency, limit=limit, requests=len(rows),
                elapsed_seconds=round(elapsed, 3), requests_per_second=round(len(rows) / elapsed, 2),
                p50_ms=percentile(.50), p95_ms=percentile(.95), p99_ms=percentile(.99),
                http_status_counts=codes, http_errors=sum(row[1] != 200 for row in rows),
                validation_errors=sum(bool(row[4]) for row in rows) + (not rows),
                error_kinds=sorted({e for row in rows for e in row[4]} | ({'no_requests'} if not rows else set())),
                item_counts=sorted({row[3] for row in rows}), response_bytes=sum(row[2] for row in rows))


def positive_int(value):
    result = int(value)
    if result < 1:
        raise argparse.ArgumentTypeError('must be positive')
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', default=os.environ.get('TIMELINE_BENCH_URL', 'http://localhost:8080'))
    parser.add_argument('--fixture', type=Path, help='private JSON object: token, user_id, list_id, tag')
    parser.add_argument('--token-file', type=Path, help='private plain-text bearer token file')
    parser.add_argument('--cases', nargs='+', choices=['home', 'public', 'media', 'user', 'list', 'tag'], default=['public'])
    parser.add_argument('--user-id')
    parser.add_argument('--list-id')
    parser.add_argument('--tag')
    parser.add_argument('--concurrency', nargs='+', type=positive_int, default=[1])
    parser.add_argument('--limit', nargs='+', type=positive_int, default=[20])
    parser.add_argument('--duration', type=float, default=3)
    parser.add_argument('--timeout', type=float, default=20)
    parser.add_argument('--max-id', type=positive_int)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if not math.isfinite(args.duration) or not math.isfinite(args.timeout) or args.duration <= 0 or args.timeout <= 0:
        parser.error('duration and timeout must be finite and positive')
    parsed = urlsplit(args.url)
    if parsed.scheme not in ['http', 'https'] or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        parser.error('URL must be an HTTP(S) base without credentials, query or fragment')

    def private_file(path):
        if not path.is_file() or stat.S_IMODE(path.stat().st_mode) & 0o077:
            parser.error('credential files must exist and have no group/other permissions (use chmod 600)')
        return path.read_text()

    fixture = json.loads(private_file(args.fixture)) if args.fixture else {}
    if not isinstance(fixture, dict):
        parser.error('fixture must be a JSON object')
    token = private_file(args.token_file).strip() if args.token_file else os.environ.get('TIMELINE_BENCH_TOKEN', fixture.get('token', ''))
    if not isinstance(token, str) or '\n' in token or '\r' in token:
        parser.error('invalid token')
    user = args.user_id or fixture.get('user_id')
    listing = args.list_id or fixture.get('list_id')
    tag = args.tag or fixture.get('tag')
    paths = {'home': '/api/v1/timelines/home', 'public': '/api/v1/timelines/public',
             'media': '/api/v1/timelines/public', 'user': '/api/v1/accounts/' + quote(str(user), safe='') + '/statuses',
             'list': '/api/v1/timelines/list/' + quote(str(listing), safe=''),
             'tag': '/api/v1/timelines/tag/' + quote(str(tag), safe='')}
    for case, required in [('user', user), ('list', listing), ('tag', tag)]:
        if case in args.cases and not required:
            parser.error(case + ' case requires its ID/tag argument or fixture field')
    results = []
    for case in args.cases:
        for limit in args.limit:
            query = {'limit': limit}
            if case == 'media':
                query['only_media'] = 'true'
            if args.max_id is not None:
                query['max_id'] = args.max_id
            path = parsed.path.rstrip('/') + paths[case] + '?' + urlencode(query)
            for concurrency in args.concurrency:
                result = benchmark(args.url, token, path, concurrency, args.duration, args.timeout, limit, args.max_id)
                result['case'] = case
                results.append(result)
                print(json.dumps(result), flush=True)
    report = dict(base_url=args.url, method='closed-loop; persistent HTTP connection per worker',
                  note='No cache flush or warmup. Empty pages are reported, not automatically invalid. Structural checks do not prove privacy or authorization. In-flight requests can extend duration up to timeout.', results=results)
    if args.output:
        args.output.write_text(json.dumps(report, indent=2) + '\n')
    return 1 if any(r['http_errors'] or r['validation_errors'] for r in results) else 0


if __name__ == '__main__':
    raise SystemExit(main())
