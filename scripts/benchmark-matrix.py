#!/usr/bin/env python3
"""Benchmark Matrix client APIs with dedicated test accounts (sync has side effects)."""
import argparse
import concurrent.futures
import hashlib
import http.client
import json
import math
import os
from pathlib import Path
import stat
import time
from urllib.parse import quote, urlencode, urlsplit


def timeline_events(data):
    return [event for section in ('join', 'leave')
            for room in data.get('rooms', {}).get(section, {}).values()
            for event in room.get('timeline', {}).get('events', [])]


def signature(data):
    """Exclude transport cursors/ages, retaining event contents and room state."""
    value = json.loads(json.dumps(data))
    for key in ('next_batch', 'start', 'end'):
        value.pop(key, None)
    for section in ('join', 'leave'):
        for room in value.get('rooms', {}).get(section, {}).values():
            room.get('timeline', {}).pop('prev_batch', None)

    def clean_event(event):
        if isinstance(event, dict) and isinstance(event.get('unsigned'), dict):
            event['unsigned'].pop('age', None)
            if not event['unsigned']:
                event.pop('unsigned')

    for event in value.get('chunk', []):
        clean_event(event)
    for event in timeline_events(value):
        clean_event(event)
    for section, state_key in (('join', 'state'), ('leave', 'state'), ('invite', 'invite_state')):
        for room in value.get('rooms', {}).get(section, {}).values():
            for event in room.get(state_key, {}).get('events', []):
                clean_event(event)
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def validate(case, data, page_limit, minimum_events):
    errors = set()
    if not isinstance(data, dict):
        return ['response_not_object'], -1, -1
    if case.startswith('messages'):
        items = data.get('chunk')
        if not isinstance(items, list):
            return ['missing_message_chunk'], -1, 0
        if len(items) > page_limit:
            errors.add('page_exceeds_limit')
    else:
        if not isinstance(data.get('next_batch'), str) or not data['next_batch']:
            errors.add('missing_sync_token')
        if not isinstance(data.get('rooms'), dict):
            return sorted(errors | {'missing_rooms'}), -1, -1
        items = timeline_events(data)
        if case == 'idle' and items:
            errors.add('idle_has_timeline_events')
        if case == 'changed':
            changed = bool(data.get('to_device', {}).get('events'))
            changed |= any(data.get('device_lists', {}).get(k) for k in ('changed', 'left'))
            changed |= bool(data.get('account_data', {}).get('events'))
            for section in ('join', 'leave', 'invite', 'knock'):
                for room in data['rooms'].get(section, {}).values():
                    changed |= any(room.get(k, {}).get('events') for k in
                                   ('timeline', 'state', 'ephemeral', 'account_data', 'invite_state', 'knock_state'))
            if not changed:
                errors.add('changed_response_empty')
    ids = []
    for item in items:
        if not isinstance(item, dict) or not isinstance(item.get('event_id'), str) or not item['event_id']:
            errors.add('invalid_event_id')
        else:
            ids.append(item['event_id'])
    if len(ids) != len(set(ids)):
        errors.add('duplicate_event_id')
    if case != 'idle' and len(items) < minimum_events:
        errors.add('too_few_events')
    return sorted(errors), len(items), len(data.get('rooms', {}).get('join', {}))


def positive(value):
    number = int(value)
    if number < 1:
        raise argparse.ArgumentTypeError('must be positive')
    return number


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', default=os.environ.get('MATRIX_BENCH_URL', 'http://localhost:8080'))
    parser.add_argument('--fixture', type=Path, help='private JSON: token, room_id, since, changed_since, from')
    parser.add_argument('--token-file', type=Path)
    parser.add_argument('--room-id')
    parser.add_argument('--since', help='complete opaque token for idle sync; never strip a suffix')
    parser.add_argument('--changed-since', help='complete opaque token with known pending changes')
    parser.add_argument('--from-token', help='complete messages pagination cursor')
    parser.add_argument('--cases', nargs='+', choices=['initial', 'idle', 'changed', 'messages', 'messages-deep'], default=['initial'])
    parser.add_argument('--concurrency', nargs='+', type=positive, default=[1])
    parser.add_argument('--duration', type=float, default=3)
    parser.add_argument('--requests-per-worker', type=positive, help='fixed count instead of duration')
    parser.add_argument('--timeline-limit', type=positive, default=20)
    parser.add_argument('--limit', type=positive, default=20, help='messages page limit')
    parser.add_argument('--min-events', type=int, default=0, help='minimum timeline/page events, except idle')
    parser.add_argument('--timeout', type=float, default=60)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if not math.isfinite(args.duration) or not math.isfinite(args.timeout) or args.duration <= 0 or args.timeout <= 0 or args.min_events < 0:
        parser.error('duration/timeout must be finite and positive; min-events must be nonnegative')
    base = urlsplit(args.url)
    if base.scheme not in ('http', 'https') or not base.hostname or base.username or base.password or base.query or base.fragment:
        parser.error('use an HTTP(S) base URL without credentials, query or fragment')

    def private_file(path):
        if not path.is_file() or stat.S_IMODE(path.stat().st_mode) & 0o077:
            parser.error('credential files must be private (chmod 600)')
        return path.read_text()

    fixture = json.loads(private_file(args.fixture)) if args.fixture else {}
    if not isinstance(fixture, dict):
        parser.error('fixture must be a JSON object')
    token = private_file(args.token_file).strip() if args.token_file else os.environ.get('MATRIX_BENCH_TOKEN', fixture.get('token', ''))
    if not isinstance(token, str) or not token or '\r' in token or '\n' in token:
        parser.error('a valid Matrix access token is required via private file/fixture or MATRIX_BENCH_TOKEN')
    room = args.room_id or fixture.get('room_id')
    since = args.since or fixture.get('since')
    changed_since = args.changed_since or fixture.get('changed_since')
    page_from = args.from_token or fixture.get('from')
    if any(c.startswith('messages') for c in args.cases) and not room:
        parser.error('messages cases require a room ID')
    for case, cursor in [('idle', since), ('changed', changed_since), ('messages-deep', page_from)]:
        if case in args.cases and (not isinstance(cursor, str) or not cursor):
            parser.error(case + ' requires its complete cursor argument/fixture field')

    def path(case):
        if case.startswith('messages'):
            query = {'dir': 'b', 'limit': args.limit}
            if case == 'messages-deep':
                query['from'] = page_from
            route = '/_matrix/client/v3/rooms/' + quote(str(room), safe='') + '/messages'
        else:
            query = {'timeout': 0, 'set_presence': 'offline', 'filter': json.dumps({'room': {'timeline': {'limit': args.timeline_limit}}}, separators=(',', ':'))}
            if case == 'idle':
                query['since'] = since
            elif case == 'changed':
                query['since'] = changed_since
            route = '/_matrix/client/v3/sync'
        return base.path.rstrip('/') + route + '?' + urlencode(query)

    connection = http.client.HTTPSConnection if base.scheme == 'https' else http.client.HTTPConnection
    results = []
    for case in args.cases:
        for concurrency in args.concurrency:
            started = time.perf_counter()
            deadline = started + args.duration
            cpu_started = time.process_time()

            def worker(_):
                client = connection(base.hostname, base.port, timeout=args.timeout)
                rows = []
                while (len(rows) < args.requests_per_worker if args.requests_per_worker else time.perf_counter() < deadline):
                    tick = time.perf_counter()
                    code, size, count, rooms, errors, digest = 0, 0, -1, -1, [], ''
                    try:
                        client.request('GET', path(case), headers={'Authorization': 'Bearer ' + token, 'Accept': 'application/json'})
                        response = client.getresponse()
                        code = response.status
                        raw = response.read()
                        elapsed = (time.perf_counter() - tick) * 1000
                        size = len(raw)
                        if code == 200:
                            try:
                                data = json.loads(raw)
                                errors, count, rooms = validate(case, data, args.limit, args.min_events)
                                digest = signature(data)
                            except (ValueError, TypeError, KeyError, AttributeError):
                                errors = ['invalid_matrix_response']
                    except (OSError, http.client.HTTPException) as exc:
                        elapsed = (time.perf_counter() - tick) * 1000
                        errors = [type(exc).__name__]
                        client.close()
                        client = connection(base.hostname, base.port, timeout=args.timeout)
                    rows.append((elapsed, code, size, count, rooms, errors, digest))
                client.close()
                return rows

            rows = []
            with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
                for batch in pool.map(worker, range(concurrency)):
                    rows.extend(batch)
            wall = time.perf_counter() - started
            cpu = time.process_time() - cpu_started
            latencies = sorted(row[0] for row in rows)
            def pct(p):
                return round(latencies[max(0, math.ceil(len(latencies) * p) - 1)], 3) if latencies else None
            result = dict(case=case, concurrency=concurrency, requests=len(rows), elapsed_seconds=round(wall, 3),
                          rps=round(len(rows) / wall, 3), p50_ms=pct(.5), p95_ms=pct(.95), p99_ms=pct(.99),
                          http_errors=sum(row[1] != 200 for row in rows), validation_errors=sum(bool(row[5]) for row in rows) + (not rows),
                          error_kinds=sorted({error for row in rows for error in row[5]} | ({'no_requests'} if not rows else set())),
                          event_counts=sorted({row[3] for row in rows}), joined_room_counts=sorted({row[4] for row in rows}),
                          client_cpu_seconds=round(cpu, 3), client_cpu_percent=round(100 * cpu / wall, 2),
                          response_bytes=sum(row[2] for row in rows), normalized_signatures=sorted({row[6] for row in rows if row[6]}))
            results.append(result)
            print(json.dumps(result), flush=True)
    if args.output:
        args.output.write_text(json.dumps(dict(base_url=args.url, warning='Sync GET can update presence and consume/acknowledge to-device messages. Use dedicated test accounts only.',
                                               method='closed-loop; persistent connections; nearest-rank latency percentiles; no warmup; client CPU measured', results=results), indent=2) + '\n')
    return 1 if any(r['http_errors'] or r['validation_errors'] for r in results) else 0


if __name__ == '__main__':
    raise SystemExit(main())
