#!/usr/bin/env python3
"""Simple Mastodon-API load generator for Community benchmarks.
Usage: load.py BASE_URL CLIENT_ID CLIENT_SECRET USERS DURATION_S CONCURRENCY
Users must exist as u1..uN with email uN@bench.test and password benchpassN."""
import sys, json, time, random, threading, urllib.request, urllib.parse, statistics, uuid, os
base, cid, cs, n, dur, conc = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5]), int(sys.argv[6])
def req(method, path, tok=None, data=None, body=None, ctype=None):
    h = {}
    if tok: h['Authorization'] = 'Bearer ' + tok
    if data is not None: body = urllib.parse.urlencode(data, doseq=True).encode(); ctype = 'application/x-www-form-urlencoded'
    if ctype: h['Content-Type'] = ctype
    r = urllib.request.Request(base + path, data=body, headers=h, method=method)
    with urllib.request.urlopen(r, timeout=60) as resp: return resp.status, resp.read()
toks = []
for i in range(1, n + 1):
    _, b = req('POST', '/oauth/token', data={'grant_type': 'password', 'client_id': cid, 'client_secret': cs, 'username': f'u{i}@bench.test', 'password': f'benchpass{i}', 'scope': 'read write follow'})
    toks.append(json.loads(b)['access_token'])
ids = [json.loads(req('GET', '/api/v1/accounts/verify_credentials', t)[1])['id'] for t in toks]
for t in toks:
    for a in random.sample(ids, min(5, len(ids))):
        try: req('POST', f'/api/v1/accounts/{a}/follow', t, data={})
        except Exception: pass
img = open(os.path.join(os.path.dirname(os.path.abspath(__file__)), 'sample.jpg'), 'rb').read()
lat, errs, recent, lock = {}, {}, [], threading.Lock()
def media(t):
    bd = uuid.uuid4().hex
    body = (f'--{bd}\r\nContent-Disposition: form-data; name="file"; filename="a.jpg"\r\nContent-Type: image/jpeg\r\n\r\n').encode() + img + f'\r\n--{bd}--\r\n'.encode()
    _, b = req('POST', '/api/v2/media', t, body=body, ctype='multipart/form-data; boundary=' + bd)
    mid = json.loads(b)['id']
    return req('POST', '/api/v1/statuses', t, data={'visibility': 'public', 'status': 'media ' + uuid.uuid4().hex[:6], 'media_ids[]': [mid]})
ops = [
 ('home', 45, lambda t: req('GET', '/api/v1/timelines/home?limit=20', t)),
 ('public', 15, lambda t: req('GET', '/api/v1/timelines/public?limit=20', t)),
 ('post', 15, lambda t: req('POST', '/api/v1/statuses', t, data={'visibility': 'public', 'status': 'bench post #tag%d %s' % (random.randint(1, 20), uuid.uuid4().hex)})),
 ('poll', 5, lambda t: req('POST', '/api/v1/statuses', t, data={'visibility': 'public', 'status': 'poll?', 'poll[options][]': ['a', 'b', 'c'], 'poll[expires_in]': 3600})),
 ('media', 5, media),
 ('favourite', 10, lambda t: req('POST', f'/api/v1/statuses/{random.choice(recent)}/favourite', t, data={}) if recent else req('GET', '/api/v1/timelines/public', t)),
 ('notifications', 5, lambda t: req('GET', '/api/v1/notifications?limit=20', t)),
]
weights = [o[1] for o in ops]
stop = time.time() + dur
def worker():
    while time.time() < stop:
        name, _, fn = random.choices(ops, weights)[0]; t = random.choice(toks); s = time.time()
        try:
            st, b = fn(t)
            if name in ('post', 'poll', 'media'):
                with lock: recent.append(json.loads(b)['id']); recent[:] = recent[-200:]
            ok = True
        except Exception as e:
            ok = False
        d = (time.time() - s) * 1000
        with lock:
            (lat if ok else errs).setdefault(name, []).append(d)
th = [threading.Thread(target=worker) for _ in range(conc)]
[x.start() for x in th]; [x.join() for x in th]
def pct(v, p): v = sorted(v); return v[min(len(v) - 1, int(len(v) * p))]
allv = [x for v in lat.values() for x in v]
print(f'{"op":14}{"ok":>7}{"err":>6}{"p50ms":>9}{"p95ms":>9}')
for name, *_ in ops:
    v = lat.get(name, [])
    if v or errs.get(name): print(f'{name:14}{len(v):7}{len(errs.get(name, [])):6}{pct(v,.5) if v else 0:9.0f}{pct(v,.95) if v else 0:9.0f}')
print(f'{"ALL":14}{len(allv):7}{sum(len(v) for v in errs.values()):6}{pct(allv,.5):9.0f}{pct(allv,.95):9.0f}  rps={len(allv)/dur:.1f}')
