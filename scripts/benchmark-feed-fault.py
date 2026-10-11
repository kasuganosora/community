#!/usr/bin/env python3
"""Apply a recorded pause/crash to explicitly identified isolated lab processes.

Run concurrently with a workload and recovery oracle. This tool alone proves
neither convergence nor absence of leaks. Pause always resumes in finally.
"""
import argparse
import json
import os
from pathlib import Path
import signal
import time


def identity(pid):
    if pid <= 1 or pid == os.getpid():
        raise ValueError('refusing unsafe PID')
    raw = Path(f'/proc/{pid}/stat').read_text()
    start = raw[raw.rfind(')') + 2:].split()[19]
    argv = Path(f'/proc/{pid}/cmdline').read_bytes().replace(b'\0', b' ').decode(errors='replace')
    return start, argv


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--pid', type=int, action='append', required=True)
    p.add_argument('--expected-lab-path', required=True, type=Path)
    p.add_argument('--kind', choices=['pause', 'crash'], required=True)
    p.add_argument('--delay', type=float, default=2)
    p.add_argument('--seconds', type=float, default=60)
    p.add_argument('--output', required=True, type=Path)
    args = p.parse_args()
    expected = str(args.expected_lab_path.resolve())
    if not expected.startswith('/tmp/') or len(args.pid) > 8 or args.seconds <= 0 or args.delay < 0:
        p.error('requires an explicit /tmp lab executable path, at most 8 PIDs, positive interval')
    identities = {}
    for pid in args.pid:
        start, argv = identity(pid)
        if expected not in argv:
            p.error('process does not match the declared lab executable')
        identities[pid] = start
    args.output.parent.mkdir(parents=True, exist_ok=True)
    events = []
    def record(event, pid=None):
        events.append({'event': event, 'pid': pid, 'unix_ns': time.time_ns(), 'monotonic_ns': time.monotonic_ns()})
        args.output.write_text(json.dumps({'kind': args.kind, 'expected_lab_path': expected, 'events': events}, indent=2))
    record('armed')
    time.sleep(args.delay)
    paused = []
    try:
        for pid, start in identities.items():
            if identity(pid)[0] != start:
                raise RuntimeError('PID was reused; refusing to signal')
            os.kill(pid, signal.SIGSTOP if args.kind == 'pause' else signal.SIGKILL)
            if args.kind == 'pause':
                paused.append(pid)
            record('paused' if args.kind == 'pause' else 'killed', pid)
        if paused:
            time.sleep(args.seconds)
    finally:
        for pid in paused:
            try:
                if identity(pid)[0] == identities[pid]:
                    os.kill(pid, signal.SIGCONT)
                    record('resumed', pid)
            except OSError:
                record('process_exited', pid)
        record('finished')
    print(json.dumps({'kind': args.kind, 'processes': len(identities), 'evidence': str(args.output), 'recovery_verified': False}))

if __name__ == '__main__':
    main()
