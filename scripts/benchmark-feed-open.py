#!/usr/bin/env python3
"""Bounded open-arrival HTTP benchmark. Raw failures and missed arrivals are retained.

Scenario JSON: {base_url, cases:[{name,path,rate,limit,expected_ids?,expected_signature?}]}.
Tokens are read separately from a mode-0600 file; request bodies/tokens are never logged.
An exact frozen oracle is required for a correctness-qualified read result.
"""
import argparse
import concurrent.futures
import hashlib
import http.client
import json
import math
import re
from pathlib import Path
import stat
import threading
import time
from urllib.parse import urlsplit


def percentile(values, q):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(q * len(ordered)) - 1)] if ordered else None


def page_signature(page):
    # Preserve order and permission-sensitive embedded entities, but omit volatile
    # rendering URLs and timestamps. Hashes do not substitute for SQL ACL oracles.
    rows = []
    for item in page:
        def entity(status):
            if not isinstance(status, dict):
                return None
            return {k: status.get(k) for k in ('id', 'visibility', 'in_reply_to_id', 'sensitive')} | {
                'account': (status.get('account') or {}).get('id'),
                'content_sha256': hashlib.sha256(status.get('content', '').encode()).hexdigest(),
                'media': [(m.get('id'), m.get('type')) for m in status.get('media_attachments', [])],
                'poll': status.get('poll'),
                'emojis': sorted(e.get('shortcode', '') for e in status.get('emojis', [])),
            }
        rows.append({'status': entity(item), 'reblog': entity(item.get('reblog')), 'quote': entity(item.get('quote'))})
    return hashlib.sha256(json.dumps(rows, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def validate(page, case):
    errors = []
    if not isinstance(page, list):
        return ['not_array'], None
    try:
        ids = [int(item['id']) for item in page]
    except (KeyError, TypeError, ValueError):
        return ['invalid_status_id'], None
    if len(ids) > case.get('limit', 40):
        errors.append('oversized_page')
    if len(ids) != len(set(ids)):
        errors.append('duplicate_id')
    if any(a <= b for a, b in zip(ids, ids[1:])):
        errors.append('not_descending')
    if case.get('max_id') is not None and any(i >= int(case['max_id']) for i in ids):
        errors.append('cursor_not_exclusive')
    if 'expected_ids' in case and ids != [int(i) for i in case['expected_ids']]:
        errors.append('oracle_ids_mismatch')
    signature = page_signature(page)
    if case.get('expected_signature') and signature != case['expected_signature']:
        errors.append('oracle_metadata_mismatch')
    return errors, signature


def summarize(rows, duration, case):
    sent = [r for r in rows if not r.get('dropped')]
    valid = [r for r in sent if r['status'] == 200 and not r['errors']]
    return {
        'name': case['name'], 'scheduled': len(rows), 'sent': len(sent),
        'dropped': len(rows) - len(sent), 'valid': len(valid),
        'offered_rps': case['rate'], 'sent_rps': len(sent) / duration,
        'valid_rps': len(valid) / duration,
        'arrival_success_ratio': len(valid) / len(rows) if rows else 0,
        'http_errors': sum(r['status'] != 200 for r in sent),
        'semantic_errors': sum(bool(r['errors']) for r in sent),
        'connections_recycled': sum(bool(r.get('connection_recycled')) for r in sent),
        'correctness_qualified': 'expected_ids' in case and bool(case.get('expected_signature')),
        'service_ms': {str(q): percentile([r['service_ms'] for r in sent], q) for q in (.5, .95, .99)},
        'scheduled_to_complete_ms': {str(q): percentile([r['end_to_end_ms'] for r in sent], q) for q in (.5, .95, .99)},
        'send_lag_ms': {str(q): percentile([r['send_lag_ms'] for r in sent], q) for q in (.5, .95, .99)},
        'error_kinds': sorted({e for r in sent for e in r['errors']}),
    }


def run_case(base, token, case, seconds, concurrency, timeout, output, connection_max_idle=30):
    if not math.isfinite(connection_max_idle) or connection_max_idle <= 0:
        raise ValueError("connection max idle must be finite and positive")
    parsed = urlsplit(base)
    if parsed.scheme not in ('http', 'https') or parsed.username or parsed.password:
        raise ValueError('base must be a credential-free HTTP(S) origin')
    local = threading.local()
    slots = threading.BoundedSemaphore(concurrency)
    lock = threading.Lock()
    rows = []
    connections = {}
    start = time.monotonic() + .1

    def save(row):
        with lock:
            rows.append(row)
            output.write(json.dumps(row, separators=(',', ':')) + '\n')

    def request(seq, scheduled):
        sent = time.monotonic()
        row = {'case': case['name'], 'sequence': seq, 'scheduled_s': scheduled - start,
               'sent_s': sent - start, 'send_lag_ms': 1000 * (sent - scheduled), 'status': 0, 'errors': [],
               'connection_idle_s': None, 'connection_recycled': False}
        try:
            if getattr(local, 'connection', None) and getattr(local, 'response_completed', None) is not None:
                row['connection_idle_s'] = sent - local.response_completed
                if row['connection_idle_s'] > connection_max_idle:
                    local.connection.close()
                    local.connection = None
                    row['connection_recycled'] = True
            if not getattr(local, 'connection', None):
                klass = http.client.HTTPSConnection if parsed.scheme == 'https' else http.client.HTTPConnection
                local.connection = klass(parsed.hostname, parsed.port, timeout=timeout)
                with lock:
                    connections[threading.get_ident()] = local.connection
            headers = {'Accept': 'application/json'}
            if token:
                headers['Authorization'] = 'Bearer ' + token
            local.connection.request('GET', parsed.path.rstrip('/') + case['path'], headers=headers)
            response = local.connection.getresponse()
            body = response.read(16 * 1024 * 1024 + 1)
            local.response_completed = time.monotonic()
            row['status'], row['bytes'] = response.status, len(body)
            if len(body) > 16 * 1024 * 1024:
                row['errors'] = ['response_size_limit']
                local.connection.close(); local.connection = None
            elif response.status == 200:
                page = json.loads(body)
                row['errors'], row['signature'] = validate(page, case)
                row['items'] = len(page) if isinstance(page, list) else None
        except Exception as exc:
            row['errors'] = [type(exc).__name__]
            if getattr(local, 'connection', None):
                local.connection.close(); local.connection = None
        finally:
            ended = time.monotonic()
            row['service_ms'] = 1000 * (ended - sent)
            row['end_to_end_ms'] = 1000 * (ended - scheduled)
            save(row)
            slots.release()

    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
        for seq in range(math.ceil(case['rate'] * seconds)):
            scheduled = start + seq / case['rate']
            delay = scheduled - time.monotonic()
            if delay > 0:
                time.sleep(delay)
            if time.monotonic() - scheduled > max(.05, 1 / case['rate']):
                save({'case': case['name'], 'sequence': seq, 'scheduled_s': scheduled - start,
                      'dropped': True, 'reason': 'scheduler_late'})
            elif slots.acquire(blocking=False):
                pool.submit(request, seq, scheduled)
            else:
                save({'case': case['name'], 'sequence': seq, 'scheduled_s': scheduled - start,
                      'dropped': True, 'reason': 'inflight_limit'})
    for connection in connections.values():
        connection.close()
    output.flush()
    return summarize(rows, seconds, case)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--scenario', required=True, type=Path)
    parser.add_argument('--structural-only', action='store_true', help='Explicitly waive the independent frozen oracle; results are not correctness qualified')
    parser.add_argument('--token-file', type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--seconds', type=float, default=600)
    parser.add_argument('--warmup-seconds', type=float, default=120)
    parser.add_argument('--repeats', type=int, default=3)
    parser.add_argument('--max-inflight', type=int, default=32)
    parser.add_argument('--timeout', type=float, default=10)
    parser.add_argument('--connection-max-idle', type=float, default=30, help='Recycle an idle connection before its next request, in seconds; never retry failed requests')
    args = parser.parse_args()
    if not math.isfinite(args.connection_max_idle) or args.connection_max_idle <= 0:
        parser.error('connection max idle must be finite and positive')
    if min(args.seconds, args.repeats, args.max_inflight, args.timeout) <= 0 or args.warmup_seconds < 0:
        parser.error('durations, repeats, inflight and timeout must be positive')
    token = ''
    if args.token_file:
        if stat.S_IMODE(args.token_file.stat().st_mode) & 0o077:
            parser.error('token file must be private (0600)')
        token = args.token_file.read_text().strip()
    scenario = json.loads(args.scenario.read_text())
    for case in scenario['cases']:
        if not re.fullmatch(r'[A-Za-z0-9_-]+', case['name']):
            parser.error('case names must be safe alphanumeric labels')
        if not args.structural_only and ('expected_ids' not in case or not case.get('expected_signature')):
            parser.error('each read case needs frozen oracle IDs and signature; use --structural-only for smoke checks only')
        if not case['path'].startswith('/') or case['rate'] <= 0:
            parser.error('each case requires an absolute path and positive rate')
    args.output.mkdir(mode=0o700, parents=True, exist_ok=False)
    summaries = []
    for repetition in range(args.repeats):
        for case in scenario['cases']:
            if args.warmup_seconds:
                with (args.output / f'{repetition}-{case["name"]}-warmup.jsonl').open('x') as raw:
                    run_case(scenario['base_url'], token, case, args.warmup_seconds, args.max_inflight, args.timeout, raw, args.connection_max_idle)
            with (args.output / f'{repetition}-{case["name"]}.jsonl').open('x') as raw:
                result = run_case(scenario['base_url'], token, case, args.seconds, args.max_inflight, args.timeout, raw, args.connection_max_idle)
            result['repetition'] = repetition
            summaries.append(result)
            print(json.dumps(result), flush=True)
    report = {'scenario': scenario, 'settings': {k: str(v) if isinstance(v, Path) else v for k, v in vars(args).items() if k != 'token_file'}, 'results': summaries}
    (args.output / 'report.json').write_text(json.dumps(report, indent=2))
    return 1 if any(r['http_errors'] or r['semantic_errors'] or r['dropped'] for r in summaries) else 0

if __name__ == '__main__':
    raise SystemExit(main())
