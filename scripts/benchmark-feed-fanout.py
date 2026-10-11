#!/usr/bin/env python3
"""Measure publication to ALL prepared Redis recipients, not HTTP fallback reads.

Run only against an isolated fixture. Receivers is a JSON array of account IDs;
key-template must contain {id}. A completion command (JSON argv) may additionally
assert durable SQL event/batch completion; it receives status ID as its last arg
and must emit {"done":true,"failed":0}. No shell command evaluation is performed.
"""
import argparse
import concurrent.futures
import http.client
import json
from pathlib import Path
import socket
import stat
import time
from urllib.parse import urlsplit


def response(stream):
    line = stream.readline()
    if not line:
        raise EOFError('Redis disconnected')
    prefix, value = line[:1], line[1:-2]
    if prefix == b'$':
        size = int(value)
        if size < 0:
            return None
        data = stream.read(size)
        if stream.read(2) != b'\r\n':
            raise ValueError('invalid Redis bulk response')
        return data
    if prefix == b':':
        return int(value)
    if prefix == b'+':
        return value
    if prefix == b'-':
        raise ValueError('Redis command failed')
    raise ValueError('unsupported Redis response')


def command(*parts):
    raw = [str(p).encode() for p in parts]
    return b'*' + str(len(raw)).encode() + b'\r\n' + b''.join(b'$' + str(len(p)).encode() + b'\r\n' + p + b'\r\n' for p in raw)


def observe(host, port, database, template, recipients, status_id, chunk=256):
    count = 0
    with socket.create_connection((host, port), timeout=3) as conn:
        conn.settimeout(3)
        stream = conn.makefile('rb')
        conn.sendall(command('SELECT', database))
        response(stream)
        for start in range(0, len(recipients), chunk):
            group = recipients[start:start + chunk]
            conn.sendall(b''.join(command('ZSCORE', template.format(id=uid), status_id) for uid in group))
            count += sum(response(stream) is not None for _ in group)
    return count


def run(args):
    import subprocess
    recipients = json.loads(args.receivers.read_text())
    if not recipients or len(recipients) != len(set(recipients)) or any(not str(i).isdigit() for i in recipients):
        raise ValueError('receivers must be a nonempty unique numeric ID array')
    if stat.S_IMODE(args.token_file.stat().st_mode) & 0o077:
        raise ValueError('token file must have mode 0600')
    token = args.token_file.read_text().strip()
    parsed = urlsplit(args.base_url)
    if parsed.scheme not in ('http', 'https') or parsed.username or parsed.password:
        raise ValueError('invalid base URL')
    completion = json.loads(args.completion_command.read_text()) if args.completion_command else None
    if completion and (not isinstance(completion, list) or not all(isinstance(x, str) for x in completion)):
        raise ValueError('completion command must be JSON argv')
    args.output.mkdir(mode=0o700, parents=True, exist_ok=False)
    results = []

    def trial(number, scheduled=None):
        klass = http.client.HTTPSConnection if parsed.scheme == 'https' else http.client.HTTPConnection
        conn = klass(parsed.hostname, parsed.port, timeout=15)
        start = time.monotonic()
        row = {'trial': number, 'started_unix': time.time(), 'recipients': len(recipients), 'redis_probe_commands': 0, 'samples': [], 'passed': False, 'scheduled_unix': scheduled, 'send_lag_ms': max(0, 1000*(time.time()-scheduled)) if scheduled else 0}
        try:
            body = json.dumps({'status': f'Feed fanout fixture {args.label} {number} {time.time_ns()}', 'visibility': 'public'})
            conn.request('POST', parsed.path.rstrip('/') + '/api/v1/statuses', body, {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json', 'Idempotency-Key': f'{args.label}-{number}'})
            reply = conn.getresponse();data = json.loads(reply.read())
            row['http_status'] = reply.status
            row['http_ms'] = 1000 * (time.monotonic() - start)
            if reply.status not in (200, 201) or not str(data.get('id', '')).isdigit():
                row['error'] = 'publish_rejected';return row
            row['status_id'] = str(data['id'])
            while time.monotonic() - start < args.timeout:
                sample = {'elapsed_ms': 1000 * (time.monotonic() - start)}
                try:
                    if completion:
                        result = subprocess.run(completion + [row['status_id']], capture_output=True, timeout=10, check=True)
                        durable = json.loads(result.stdout)
                        sample['durable'] = durable
                        if not (durable.get('done') is True and durable.get('failed') == 0):
                            row['samples'].append(sample)
                            time.sleep(args.interval)
                            continue
                    row['redis_probe_commands'] += len(recipients)
                    count = observe(args.redis_host, args.redis_port, args.redis_db, args.key_template, recipients, row['status_id'])
                    sample['received'] = count
                    if count == len(recipients):
                        row['redis_complete_ms'] = 1000 * (time.monotonic() - start)
                        row['passed'] = True
                        row['complete_ms'] = 1000 * (time.monotonic() - start)
                        row['durable_verified'] = bool(completion)
                        row['samples'].append(sample)
                        break
                except Exception as exc:
                    sample['error'] = type(exc).__name__
                row['samples'].append(sample)
                time.sleep(args.interval)
            if not row['passed']:
                row['error'] = 'full_fanout_timeout'
        except Exception as exc:
            row['error'] = type(exc).__name__
        finally:
            conn.close()
            row['elapsed_ms'] = 1000 * (time.monotonic() - start)
        return row

    start = time.monotonic()
    with (args.output / 'raw.jsonl').open('x') as raw, concurrent.futures.ThreadPoolExecutor(max_workers=args.publishers) as pool:
        def record(row):
            raw.write(json.dumps(row) + '\n');raw.flush();results.append(row)
        if args.arrival_rate:
            # No executor backlog: arrivals without a free observer slot are
            # recorded as dropped, never delayed into a hidden closed loop.
            pending = set()
            wall_start = time.time()
            for number in range(args.posts):
                scheduled = start + number / args.arrival_rate
                delay = scheduled - time.monotonic()
                if delay > 0:
                    time.sleep(delay)
                done = {f for f in pending if f.done()}
                for f in done:
                    record(f.result())
                pending -= done
                lag = time.monotonic() - scheduled
                if len(pending) >= args.publishers or lag > max(.05, 1 / args.arrival_rate):
                    record({'trial': number, 'dropped': True, 'passed': False, 'error': 'arrival_capacity_or_lag', 'send_lag_ms': lag*1000, 'scheduled_unix': wall_start+number/args.arrival_rate, 'redis_probe_commands': 0})
                else:
                    pending.add(pool.submit(trial, number, wall_start+number/args.arrival_rate))
            for f in concurrent.futures.as_completed(pending):
                record(f.result())
        else:
            for row in pool.map(trial, range(args.posts)):
                record(row)
    times = sorted(r['complete_ms'] for r in results if r['passed'])
    def pct(q):
        import math
        return times[max(0, math.ceil(q * len(times)) - 1)] if times else None
    elapsed = time.monotonic() - start
    report = {'label': args.label, 'posts': args.posts, 'arrival_rate': args.arrival_rate, 'dropped_arrivals': sum(bool(r.get('dropped')) for r in results), 'arrival_success_ratio': len(times)/args.posts, 'recipients': len(recipients), 'publishers': args.publishers, 'worker_processes': args.worker_processes, 'elapsed_s': elapsed, 'successful_posts': len(times), 'completed_posts_per_second': len(times) / elapsed, 'recipient_projections_per_second': len(times) * len(recipients) / elapsed, 'p50_ms': pct(.5), 'p95_ms': pct(.95), 'p99_ms': pct(.99), 'redis_probe_commands': sum(r['redis_probe_commands'] for r in results), 'poll_interval_seconds': args.interval, 'durable_sql_verification': bool(completion), 'passed': len(times) == args.posts, 'limitations': [('Open-arrival writes with bounded full-fanout observer slots; dropped arrivals count as failures.' if args.arrival_rate else 'Closed-loop write batches; not open-arrival capacity.'), 'Polling perturbs Redis and bounds completion-time precision by poll interval plus scan time.', 'Successful HTTP read fallback alone is never considered fanout completion.']}
    (args.output / 'report.json').write_text(json.dumps(report, indent=2))
    print(json.dumps(report))
    return 0 if report['passed'] else 1


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--base-url', required=True);p.add_argument('--token-file', required=True, type=Path)
    p.add_argument('--receivers', required=True, type=Path);p.add_argument('--key-template', required=True)
    p.add_argument('--redis-host', default='127.0.0.1');p.add_argument('--redis-port', required=True, type=int);p.add_argument('--redis-db', type=int, default=0)
    p.add_argument('--arrival-rate', type=float, help='Optional fixed open-arrival writes/sec; requires adequate --publishers observer slots')
    p.add_argument('--posts', type=int, default=20);p.add_argument('--publishers', type=int, default=1)
    p.add_argument('--worker-processes', type=int, default=1);p.add_argument('--label', required=True)
    p.add_argument('--timeout', type=float, default=120);p.add_argument('--interval', type=float, default=.25)
    p.add_argument('--completion-command', type=Path);p.add_argument('--output', required=True, type=Path)
    args = p.parse_args()
    if min(args.posts, args.publishers, args.worker_processes, args.timeout, args.interval) <= 0 or '{id}' not in args.key_template:
        p.error('positive controls and a key template containing {id} are required')
    if args.arrival_rate is not None and args.arrival_rate <= 0:
        p.error('arrival rate must be positive')
    return run(args)
if __name__ == '__main__':
    raise SystemExit(main())
