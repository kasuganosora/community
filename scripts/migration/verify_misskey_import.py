#!/usr/bin/env python3
"""Read-only verification of a Misskey -> Community migration rehearsal.

Compares the source Misskey PostgreSQL database with an isolated Community
instance (database + HTTP endpoint) after import. Never writes: every SQL
session is opened read-only, only SELECT statements are issued, and HTTP uses
GET/OPTIONS only. Key material is compared by SHA-256 of the normalized PEM;
private keys, tokens and passwords are never selected or printed.

Python 3 stdlib only. Uses psycopg2 for PostgreSQL if importable, otherwise the
`psql` CLI. A MySQL Community database (mysql://...) is read via the `mysql`
CLI. See docs/migration-verify.md.
"""
import argparse
import hashlib
import json
import os
import random
import re
import shutil
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

AP_ACCEPT = 'application/activity+json, application/ld+json; profile="https://www.w3.org/ns/activitystreams"'
SAFE_ID = re.compile(r"^[A-Za-z0-9_-]{1,64}$")


# ---------------------------------------------------------------- database
class DB:
    """Minimal read-only SQL runner returning rows as lists of strings/None."""

    def __init__(self, name, dsn, timeout=60):
        self.name, self.dsn, self.timeout = name, dsn, timeout
        scheme = urllib.parse.urlsplit(dsn).scheme.lower() if "://" in dsn else "postgresql"
        self.kind = "mysql" if scheme.startswith("mysql") else "pg"
        self._conn = None
        if self.kind == "pg":
            try:
                import psycopg2  # type: ignore
                self._conn = psycopg2.connect(dsn, options="-c default_transaction_read_only=on -c statement_timeout=%d" % (timeout * 1000))
                self._conn.set_session(readonly=True, autocommit=True)
            except ImportError:
                if not shutil.which("psql"):
                    raise SystemExit("neither psycopg2 nor psql is available")
        elif not shutil.which("mysql"):
            raise SystemExit("mysql CLI is required for a mysql:// COMMUNITY_DB_DSN")

    def q(self, sql):
        if not re.match(r"^\s*(SELECT|WITH)\b", sql, re.I):
            raise RuntimeError("refusing non-SELECT statement")
        if self._conn is not None:
            cur = self._conn.cursor()
            cur.execute(sql)
            return [[None if v is None else str(v) for v in r] for r in cur.fetchall()]
        if self.kind == "pg":
            env = dict(os.environ, PGOPTIONS="-c default_transaction_read_only=on -c statement_timeout=%d" % (self.timeout * 1000))
            cmd = ["psql", "-X", "-w", "-q", "-A", "-t", "-F", "\x1f", "-R", "\x1e", "-P", "null=\\N", "-v", "ON_ERROR_STOP=1",
                   "-d", self.dsn, "-c", sql]
        else:
            u = urllib.parse.urlsplit(self.dsn)
            env = dict(os.environ)
            if u.password:
                env["MYSQL_PWD"] = urllib.parse.unquote(u.password)
            cmd = ["mysql", "--no-defaults", "-N", "-B", "-h", u.hostname or "127.0.0.1", "-P", str(u.port or 3306)]
            if u.username:
                cmd += ["-u", urllib.parse.unquote(u.username)]
            cmd += [u.path.lstrip("/").split("?")[0], "-e", "SET SESSION TRANSACTION READ ONLY; START TRANSACTION READ ONLY; " + sql]
        p = subprocess.run(cmd, env=env, capture_output=True, text=True, timeout=self.timeout + 10)
        if p.returncode != 0:
            # stderr may echo connection details; keep only the first line, scrub URLs.
            err = (p.stderr.strip().splitlines() or ["?"])[0]
            raise RuntimeError("%s query failed: %s" % (self.name, re.sub(r"\w+://\S+", "<dsn>", err)))
        sep = "\x1f" if self.kind == "pg" else "\t"
        rows = []
        if self.kind == "pg":
            records = [r.strip("\n") for r in p.stdout.split("\x1e")]
        else:
            records = p.stdout.splitlines()
        for line in records:
            if line == "":
                continue
            vals = [None if v in ("\\N", "NULL") else v for v in line.split(sep)]
            if self.kind == "mysql":  # mysql -B escapes newline/tab/backslash
                vals = [None if v is None else v.replace("\\n", "\n").replace("\\t", "\t").replace("\\\\", "\\") for v in vals]
            rows.append(vals)
        return rows

    def scalar(self, sql):
        r = self.q(sql)
        return r[0][0] if r and r[0] else None

    def has_table(self, table):
        if self.kind == "pg":
            return self.scalar("SELECT to_regclass('public.%s') IS NOT NULL" % table.replace('"', '')) in ("t", "True", "true", "1")
        return self.scalar("SELECT count(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = '%s'" % table) not in (None, "0")


def lit(s):
    return "'" + str(s).replace("'", "''") + "'"


def key_hash(pem):
    if not pem:
        return None
    norm = re.sub(r"\s+", "", pem)
    return hashlib.sha256(norm.encode()).hexdigest()[:16]


# ---------------------------------------------------------------- http
class HTTP:
    def __init__(self, base, host, proto, timeout):
        self.base, self.host, self.proto, self.timeout = base.rstrip("/"), host, proto, timeout

    def req(self, path, method="GET", accept=AP_ACCEPT):
        r = urllib.request.Request(self.base + path, method=method)
        r.add_header("Host", self.host)
        r.add_header("Accept", accept)
        if self.proto:
            r.add_header("X-Forwarded-Proto", self.proto)
        r.add_header("User-Agent", "community-migration-verify/1")
        opener = urllib.request.build_opener(NoRedirect)
        try:
            with opener.open(r, timeout=self.timeout) as resp:
                return resp.status, resp.read(4 << 20)
        except urllib.error.HTTPError as e:
            return e.code, e.read(65536) if e.fp else b""
        except Exception as e:  # connection error
            return 0, str(e).encode()

    def json(self, path, accept=AP_ACCEPT):
        st, body = self.req(path, accept=accept)
        try:
            return st, json.loads(body.decode("utf-8")) if st == 200 else None
        except ValueError:
            return st, None


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None  # report 3xx as-is; never follow off the isolated instance


# ---------------------------------------------------------------- checks
class Report:
    def __init__(self, verbose):
        self.results, self.verbose = [], verbose

    def add(self, name, ok, detail, failures=()):
        status = "PASS" if ok is True else ("WARN" if ok is None else "FAIL")
        self.results.append((name, status))
        print("[%s] %s: %s" % (status, name, detail))
        for f in list(failures)[: (None if self.verbose else 10)]:
            print("       - " + f)
        if failures and not self.verbose and len(failures) > 10:
            print("       ... %d more (use -v)" % (len(failures) - 10))


def within(a, b, tol):
    if a == b:
        return True
    return abs(a - b) <= max(1, tol * max(a, b))


def run(args):
    origin = "https://" + args.host
    rep = Report(args.verbose)
    mk = DB("misskey", args.misskey_dsn, args.timeout)
    cm = DB("community", args.community_dsn, args.timeout)
    http = HTTP(args.base, args.host, args.forwarded_proto, args.timeout)
    rnd = random.Random(args.seed)

    # (1) local users
    mk_total = int(mk.scalar('SELECT count(*) FROM "user" WHERE host IS NULL'))
    mk_live = int(mk.scalar('SELECT count(*) FROM "user" WHERE host IS NULL AND "isDeleted" = false'))
    cm_imported = int(cm.scalar("SELECT count(*) FROM accounts WHERE domain IS NULL AND deleted_at IS NULL AND local_actor_uri LIKE %s" % lit(origin + "/users/%")))
    cm_local = int(cm.scalar("SELECT count(*) FROM accounts WHERE domain IS NULL AND deleted_at IS NULL"))
    rep.add("1 local user count", cm_imported in (mk_total, mk_live),
            "misskey local=%d (not deleted=%d); community accounts with %s/users/* identity=%d (all local=%d)" % (mk_total, mk_live, origin, cm_imported, cm_local))

    # sample users (exclude system actors such as instance.actor / relay.actor whose usernames contain dots)
    users = mk.q('SELECT u.id, u.username, k."publicKey" FROM "user" u LEFT JOIN user_keypair k ON k."userId" = u.id '
                 'WHERE u.host IS NULL AND u."isDeleted" = false AND u.username NOT LIKE \'%.%\'')
    users = [u for u in users if SAFE_ID.match(u[0])]
    sample = rnd.sample(users, min(args.sample, len(users)))

    # (2) actor + key + webfinger
    fails, ok_n = [], 0
    for uid, uname, pem in sample:
        want = "%s/users/%s" % (origin, uid)
        st, actor = http.json("/users/" + uid)
        if st != 200 or not isinstance(actor, dict):
            fails.append("%s @%s: GET /users/%s -> HTTP %s" % (uid, uname, uid, st)); continue
        if actor.get("id") != want:
            fails.append("%s @%s: actor id %r != %s" % (uid, uname, actor.get("id"), want)); continue
        pk = actor.get("publicKey") or {}
        if isinstance(pk, list):
            pk = pk[0] if pk else {}
        got_h, want_h = key_hash(pk.get("publicKeyPem")), key_hash(pem)
        if not want_h or got_h != want_h:
            fails.append("%s @%s: publicKey sha256 %s != misskey %s" % (uid, uname, got_h, want_h)); continue
        if pk.get("id") and pk.get("id") != want + "#main-key":
            fails.append("%s @%s: key id %r != %s#main-key" % (uid, uname, pk.get("id"), want)); continue
        res = urllib.parse.quote("acct:%s@%s" % (uname, args.host), safe=":@")
        st, wf = http.json("/.well-known/webfinger?resource=" + res, accept="application/jrd+json, application/json")
        hrefs = [l.get("href") for l in (wf or {}).get("links", []) if l.get("rel") == "self"]
        if st != 200 or want not in hrefs:
            fails.append("%s @%s: webfinger HTTP %s self=%s" % (uid, uname, st, hrefs)); continue
        ok_n += 1
    rep.add("2 actor id/publicKey/webfinger", bool(sample) and not fails, "%d/%d sampled users OK" % (ok_n, len(sample)), fails)

    # (3) notes
    notes = mk.q('SELECT id FROM note WHERE "userHost" IS NULL AND visibility IN (\'public\', \'home\') ORDER BY random() LIMIT %d' % args.sample)
    fails, ok_n = [], 0
    for (nid,) in notes:
        if not SAFE_ID.match(nid):
            continue
        st, obj = http.json("/notes/" + nid)
        want = "%s/notes/%s" % (origin, nid)
        if st == 200 and isinstance(obj, dict) and obj.get("id") == want:
            ok_n += 1
        else:
            fails.append("%s: HTTP %s id=%r" % (nid, st, (obj or {}).get("id") if isinstance(obj, dict) else None))
    rep.add("3 public/home note objects", bool(notes) and not fails, "%d/%d sampled notes OK" % (ok_n, len(notes)), fails)

    # (4) follow counts
    fails, ok_n = [], 0
    for uid, uname, _ in sample:
        mf = mk.q('SELECT (SELECT count(*) FROM following WHERE "followeeId" = %s), (SELECT count(*) FROM following WHERE "followerId" = %s)' % (lit(uid), lit(uid)))[0]
        acc = cm.scalar("SELECT id FROM accounts WHERE domain IS NULL AND deleted_at IS NULL AND local_actor_uri = %s ORDER BY id LIMIT 1" % lit("%s/users/%s" % (origin, uid)))
        if acc is None:
            fails.append("%s @%s: no community account" % (uid, uname)); continue
        cf = cm.q("SELECT (SELECT count(*) FROM follows WHERE follow_id = %s AND status = 0), (SELECT count(*) FROM follows WHERE account_id = %s AND status = 0)" % (int(acc), int(acc)))[0]
        if [int(x) for x in mf] != [int(x) for x in cf]:
            fails.append("%s @%s: followers misskey=%s community=%s; following misskey=%s community=%s" % (uid, uname, mf[0], cf[0], mf[1], cf[1]))
        else:
            ok_n += 1
    rep.add("4 follower/following counts", bool(sample) and not fails, "%d/%d sampled users match" % (ok_n, len(sample)), fails)

    # (5) instance actor
    row = cm.q("SELECT a.id, k.public_key, k.key_id FROM accounts a LEFT JOIN account_keys k ON k.account_id = a.id "
               "WHERE a.domain IS NULL AND a.deleted_at IS NULL AND a.local_actor_uri = %s ORDER BY a.id LIMIT 1" % lit(origin + "/actor"))
    db_h = key_hash(row[0][1]) if row else None
    st, actor = http.json("/actor")
    pk = (actor or {}).get("publicKey") or {} if isinstance(actor, dict) else {}
    http_h = key_hash(pk.get("publicKeyPem")) if isinstance(pk, dict) else None
    ok = bool(db_h) and (http_h == db_h) and (actor or {}).get("id") == origin + "/actor"
    rep.add("5 instance actor key", ok, "db account=%s key sha256=%s; GET /actor HTTP %s id=%r key sha256=%s" %
            (row[0][0] if row else None, db_h, st, (actor or {}).get("id") if isinstance(actor, dict) else None, http_h))

    # (6) emoji + drive files
    mk_emoji = int(mk.scalar("SELECT count(*) FROM emoji WHERE host IS NULL"))
    cm_emoji = int(cm.scalar("SELECT count(*) FROM emojis WHERE (domain = '' OR domain IS NULL) AND deleted_at IS NULL"))
    rep.add("6a local custom emoji count", within(mk_emoji, cm_emoji, args.tolerance), "misskey=%d community=%d (tolerance %.0f%%)" % (mk_emoji, cm_emoji, args.tolerance * 100))
    mk_files = int(mk.scalar('SELECT count(*) FROM drive_file WHERE "userHost" IS NULL'))
    mk_files_all = int(mk.scalar("SELECT count(*) FROM drive_file"))
    cm_files = int(cm.scalar("SELECT count(*) FROM media_attachments m JOIN accounts a ON a.id = m.account_id WHERE a.domain IS NULL"))
    cm_files_all = int(cm.scalar("SELECT count(*) FROM media_attachments"))
    rep.add("6b drive file / media count", within(mk_files, cm_files, args.tolerance) or within(mk_files_all, cm_files_all, args.tolerance),
            "misskey local=%d all=%d; community local-owner media=%d all=%d (tolerance %.0f%%)" % (mk_files, mk_files_all, cm_files, cm_files_all, args.tolerance * 100))

    # (7) inbox routes (POST-only route: GET/OPTIONS should yield 405/400/401, never 404)
    fails, ok_n = [], 0
    for uid, uname, _ in sample:
        codes = [http.req("/users/%s/inbox" % uid, method=m)[0] for m in ("OPTIONS", "GET")]
        if all(c in (0, 404) for c in codes):
            fails.append("%s @%s: OPTIONS/GET -> %s" % (uid, uname, codes))
        else:
            ok_n += 1
    rep.add("7 inbox route exists", bool(sample) and not fails, "%d/%d sampled inbox paths routed" % (ok_n, len(sample)), fails)

    failed = [n for n, s in rep.results if s == "FAIL"]
    print("\nSUMMARY: %d checks, %d PASS, %d FAIL" % (len(rep.results), len(rep.results) - len(failed), len(failed)))
    return 1 if failed else 0


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0], formatter_class=argparse.RawDescriptionHelpFormatter,
                                 epilog="Exit codes: 0 all checks pass, 1 a check failed, 2 usage/connection error.")
    ap.add_argument("--misskey-dsn", default=os.environ.get("MISSKEY_DB_DSN"), help="read-only Misskey PostgreSQL DSN (env MISSKEY_DB_DSN; prefer PGPASSFILE over inline passwords)")
    ap.add_argument("--community-dsn", default=os.environ.get("COMMUNITY_DB_DSN"), help="Community DB DSN, postgres:// or mysql:// (env COMMUNITY_DB_DSN)")
    ap.add_argument("--base", default=os.environ.get("COMMUNITY_BASE"), help="Community HTTP base, e.g. http://127.0.0.1:8080 (env COMMUNITY_BASE)")
    ap.add_argument("--host", default=os.environ.get("HOST_HEADER", "maid.lat"), help="Host header / original domain (env HOST_HEADER, default maid.lat)")
    ap.add_argument("--sample", type=int, default=int(os.environ.get("SAMPLE", "50")), help="sample size for users and notes (env SAMPLE, default 50)")
    ap.add_argument("--seed", type=int, default=None, help="random seed for reproducible user sampling")
    ap.add_argument("--tolerance", type=float, default=0.02, help="relative tolerance for emoji/file counts (default 0.02)")
    ap.add_argument("--forwarded-proto", default="https", help="X-Forwarded-Proto to send ('' to omit; default https)")
    ap.add_argument("--timeout", type=int, default=30, help="per query/request timeout seconds")
    ap.add_argument("-v", "--verbose", action="store_true", help="list every failure")
    args = ap.parse_args()
    missing = [n for n, v in (("MISSKEY_DB_DSN", args.misskey_dsn), ("COMMUNITY_DB_DSN", args.community_dsn), ("COMMUNITY_BASE", args.base)) if not v]
    if missing:
        ap.error("missing: " + ", ".join(missing))
    try:
        sys.exit(run(args))
    except Exception as e:  # report without traceback (could contain DSNs)
        print("ERROR: %s" % e, file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    main()
