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

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
# The value rules the importer applies (single definition, shared with the
# exporter and the Go importer; see docs/misskey-field-mapping.md).
from export_misskey import also_known_as_values, split_also_known_as, valid_birthday  # noqa: E402

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
        st, body, _ = self.req3(path, method, accept)
        return st, body

    def req3(self, path, method="GET", accept=AP_ACCEPT):
        """Return (status, body, error). status is None when no HTTP response arrived."""
        r = urllib.request.Request(self.base + path, method=method)
        r.add_header("Host", self.host)
        r.add_header("Accept", accept)
        if self.proto:
            r.add_header("X-Forwarded-Proto", self.proto)
        r.add_header("User-Agent", "community-migration-verify/1")
        opener = urllib.request.build_opener(NoRedirect)
        try:
            with opener.open(r, timeout=self.timeout) as resp:
                return resp.status, resp.read(4 << 20), None
        except urllib.error.HTTPError as e:
            return e.code, (e.read(65536) if e.fp else b""), None
        except Exception as e:  # connection error: no HTTP status exists
            reason = getattr(e, "reason", None) or e
            return None, b"", "%s: %s" % (type(e).__name__, re.sub(r"\w+://\S+", "<url>", str(reason))[:200])

    def json(self, path, accept=AP_ACCEPT):
        st, body, err = self.req3(path, accept=accept)
        self.last_error = err
        try:
            return st, json.loads(body.decode("utf-8")) if st == 200 else None
        except ValueError:
            return st, None

    def describe(self, st):
        """Human status: real HTTP code, or the connection error instead of a fake `HTTP 0`."""
        if st is None:
            return "no response (%s)" % (getattr(self, "last_error", None) or "connection error")
        return "HTTP %s" % st


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
        self.show(failures)

    def info(self, name, detail, items=()):
        """An informational line: not a check, never changes the exit code."""
        print("[INFO] %s: %s" % (name, detail))
        self.show(items)

    def show(self, failures):
        for f in list(failures)[: (None if self.verbose else 10)]:
            print("       - " + f)
        if failures and not self.verbose and len(failures) > 10:
            print("       ... %d more (use -v)" % (len(failures) - 10))


def within(a, b, tol):
    if a == b:
        return True
    if a == 0 or b == 0:
        return False
    return abs(a - b) <= max(1, tol * max(a, b))


# ---------------------------------------------------------------- profile extensions
# Misskey source expression -> Community accounts column. to_jsonb(row)->>'col'
# yields NULL (not an error) when a Misskey version lacks the column.
PROFILE_FIELDS = (
    ("isCat", "is_cat", "to_jsonb(u)->>'isCat'"),
    ("lang", "language", "to_jsonb(p)->>'lang'"),
    ("location", "location", "to_jsonb(p)->>'location'"),
    ("birthday", "birthday", "to_jsonb(p)->>'birthday'"),
    ("followedMessage", "followed_message", "to_jsonb(p)->>'followedMessage'"),
    ("movedToUri", "moved_to", "to_jsonb(u)->>'movedToUri'"),
    # ->> : upstream's simple-array text ('a,b'), or a text[] / json column as JSON text.
    ("alsoKnownAs", "also_known_as", "to_jsonb(u)->>'alsoKnownAs'"),
)


def profile_expected(field, raw):
    """The value the importer stores for a Misskey value, normalised for
    comparison ('' / False / [] when it is unset or not storable)."""
    if field == "isCat":
        return raw in ("true", "t", "True", "1")
    if field == "alsoKnownAs":
        return sorted(also_known_as_values(raw))
    if field == "birthday":
        return valid_birthday(raw)
    raw = (raw or "")
    if field in ("location", "lang"):
        raw = raw.strip()
    if field == "location":
        raw = raw[:128]
    if field == "followedMessage":
        raw = raw[:200]
    if field == "lang":
        raw = raw.lower()
    return raw


def profile_raw_set(field, raw):
    """The Misskey value is set (before the importer's validity rules)."""
    if field == "isCat":
        return raw in ("true", "t", "True", "1")
    if field == "alsoKnownAs":
        return bool(split_also_known_as(raw))
    return bool((raw or "").strip())


# Fields whose source values are parsed: a source with set values of which
# none were counted means the check parsed them differently from the importer
# (round 9: 8g read alsoKnownAs text as JSON and compared 0 == 0).
PARSED_FIELDS = ("alsoKnownAs", "birthday")


def profile_actual(column, raw):
    if column == "is_cat":
        return raw in ("true", "t", "True", "1")
    if column == "also_known_as":
        try:
            v = json.loads(raw) if raw else []
        except ValueError:
            v = []
        return sorted(x for x in (v or []) if isinstance(x, str))
    raw = raw or ""
    return raw.lower() if column == "language" else raw


def profile_checks(mk, cm, origin, rep):
    """8a-8g: every Misskey profile extension Community stores, compared per
    account (local by <origin>/users/<id>, remote by actor URI; remote
    accounts only where imported). Prints counts and ids, never values."""
    exprs = ", ".join(e for _, _, e in PROFILE_FIELDS)
    any_set = " OR ".join("(%s) IS NOT NULL" % e for _, _, e in PROFILE_FIELDS)
    src = mk.q('SELECT u.id, u.host IS NULL, u.uri, %s FROM "user" u LEFT JOIN user_profile p ON p."userId" = u.id '
               "WHERE (to_jsonb(u)->>'isDeleted') IS DISTINCT FROM 'true' AND (%s)" % (exprs, any_set))
    cols = ", ".join(c for _, c, _ in PROFILE_FIELDS)
    tgt = {}
    for r in cm.q("SELECT domain IS NULL, local_actor_uri, origin_id, %s FROM accounts WHERE deleted_at IS NULL" % cols):
        if len(r) < 3 + len(PROFILE_FIELDS):
            continue
        local = r[0] in ("t", "true", "True", "1")
        key = r[1] if local else r[2]
        if key:
            tgt[key] = (local, r[3:])
    for n, (field, column, _) in enumerate(PROFILE_FIELDS):
        counts = {"local": [0, 0], "remote": [0, 0]}
        diffs, raw_set, excluded = [], 0, []
        for r in src:
            if len(r) < 3 + len(PROFILE_FIELDS):
                continue
            local = r[1] in ("t", "true", "True", "1")
            key = "%s/users/%s" % (origin, r[0]) if local else r[2]
            if not key or (not local and key not in tgt):
                continue
            want = profile_expected(field, r[3 + n])
            if profile_raw_set(field, r[3 + n]):
                raw_set += 1
                if not want:
                    excluded.append("%s %s" % ("local" if local else "remote", r[0] if SAFE_ID.match(r[0] or "") else "?"))
            if not want:
                continue
            side = "local" if local else "remote"
            counts[side][0] += 1
            got = profile_actual(column, tgt[key][1][n]) if key in tgt else None
            if got == want:
                counts[side][1] += 1
            else:
                diffs.append("%s %s: %s %s" % (side, r[0] if SAFE_ID.match(r[0] or "") else "?", column, "missing account" if got is None else "differs"))
        target_total = {"local": 0, "remote": 0}
        for local, vals in tgt.values():
            if profile_actual(column, vals[n]):
                target_total["local" if local else "remote"] += 1
        name = "8%s profile %s -> %s" % ("abcdefg"[n], field, column)
        expected = counts["local"][0] + counts["remote"][0]
        if field in PARSED_FIELDS and raw_set and not expected:
            diffs.append("misskey has %d in-scope account(s) with %s set but none parsed as storable: the check does not parse the source like the importer" % (raw_set, field))
        rep.add(name, not diffs,
                "misskey set local=%d remote(imported)=%d; matching in community local=%d remote=%d; community set local=%d remote=%d; "
                "misskey set but not storable (excluded)=%d"
                % (counts["local"][0], counts["remote"][0], counts["local"][1], counts["remote"][1], target_total["local"], target_total["remote"], len(excluded)), diffs)
        if excluded:
            rep.info(name, "%d in-scope misskey value(s) excluded from the expected set: the importer drops them by rule (profile_value_dropped)%s"
                     % (len(excluded), {"birthday": ": year 0000, impossible or future dates",
                                        "alsoKnownAs": ": no alias is an absolute http(s) URL"}.get(field, "")), sorted(excluded))


TRUE = ("t", "true", "True", "1")


def norm_ts(raw):
    """A timestamp as UTC seconds text, '' when unset or unparsable."""
    import datetime
    raw = (raw or "").strip().replace(" ", "T", 1)
    if not raw:
        return ""
    raw = re.sub(r"\.\d+", "", raw)
    raw = re.sub(r"([+-]\d\d)$", r"\1:00", raw).replace("Z", "+00:00")
    try:
        d = datetime.datetime.fromisoformat(raw)
    except ValueError:
        return ""
    if d.tzinfo is None:
        d = d.replace(tzinfo=datetime.timezone.utc)
    return d.astimezone(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
# (misskey field, community column, misskey expr, expected(raw), actual(raw)):
# only accounts whose Misskey value differs from the default are compared.
PRIVACY_FIELDS = (
    ("followingVisibility", "following_visibility", "to_jsonb(p)->>'followingVisibility'",
     lambda r: r if r in ("followers", "private") else "", lambda r: r if r in ("followers", "private") else ""),
    ("followersVisibility", "followers_visibility", "to_jsonb(p)->>'followersVisibility'",
     lambda r: r if r in ("followers", "private") else "", lambda r: r if r in ("followers", "private") else ""),
    ("autoAcceptFollowed", "auto_accept_followed", "to_jsonb(p)->>'autoAcceptFollowed'", lambda r: r in TRUE, lambda r: r in TRUE),
    ("hideOnlineStatus", "hide_online_status", "to_jsonb(u)->>'hideOnlineStatus'", lambda r: r in TRUE, lambda r: r in TRUE),
    ("speakAsCat=false", "speak_as_cat", "to_jsonb(u)->>'speakAsCat'", lambda r: r == "false", lambda r: r in ("f", "false", "False", "0")),
    ("lastActiveDate", "last_active_at", "to_jsonb(u)->>'lastActiveDate'",
     lambda r: norm_ts(r), lambda r: norm_ts(r)),
)


def privacy_checks(mk, cm, origin, rep, extracted_at=None):
    """8h-8m: Misskey account privacy settings, compared per account like
    profile_checks. Prints counts and ids, never values.

    8m lastActiveDate: the live source keeps moving after the export read it.
    A source value newer than the target's is reported as info, not a
    failure, when it is later than `extracted_at` (the bundle's
    source.exported_at), or, without `extracted_at`, whenever the target holds
    an older value."""
    extracted = norm_ts(extracted_at) if extracted_at else ""
    exprs = ", ".join(f[2] for f in PRIVACY_FIELDS)
    src = mk.q('SELECT u.id, u.host IS NULL, u.uri, %s FROM "user" u LEFT JOIN user_profile p ON p."userId" = u.id '
               "WHERE (to_jsonb(u)->>'isDeleted') IS DISTINCT FROM 'true'" % exprs)
    cols = ", ".join(f[1] for f in PRIVACY_FIELDS)
    tgt = {}
    for r in cm.q("SELECT domain IS NULL, local_actor_uri, origin_id, %s FROM accounts WHERE deleted_at IS NULL" % cols):
        if len(r) < 3 + len(PRIVACY_FIELDS):
            continue
        local = r[0] in TRUE
        key = r[1] if local else r[2]
        if key:
            tgt[key] = (local, r[3:])
    for n, (field, column, _, expected, actual) in enumerate(PRIVACY_FIELDS):
        counts = {"local": [0, 0], "remote": [0, 0]}
        diffs, moved = [], []
        for r in src:
            if len(r) < 3 + len(PRIVACY_FIELDS):
                continue
            local = r[1] in TRUE
            key = "%s/users/%s" % (origin, r[0]) if local else r[2]
            want = expected(r[3 + n])
            if not want or not key or (not local and key not in tgt):
                continue
            side = "local" if local else "remote"
            counts[side][0] += 1
            got = actual(tgt[key][1][n]) if key in tgt else None
            rid = r[0] if SAFE_ID.match(r[0] or "") else "?"
            if got == want:
                counts[side][1] += 1
            elif column == "last_active_at" and got is not None and active_after_extraction(want, got, extracted):
                moved.append("%s %s" % (side, rid))
            else:
                diffs.append("%s %s: %s %s" % (side, rid, column, "missing account" if got is None else "differs"))
        name = "8%s account %s -> %s" % ("hijklm"[n], field, column)
        rep.add(name, not diffs,
                "misskey set local=%d remote(imported)=%d; matching in community local=%d remote=%d%s"
                % (counts["local"][0], counts["remote"][0], counts["local"][1], counts["remote"][1],
                   "; newer on the source (active after extraction)=%d" % len(moved) if column == "last_active_at" else ""), diffs)
        if moved:
            rep.info(name, "%d account(s) were active on the source after it was read (%s); not a failure"
                     % (len(moved), "source lastActiveDate > --extracted-at " + extracted if extracted else "source newer than the imported value; pass --extracted-at for a strict bound"), sorted(moved))


def source_exported_at(path):
    """source.exported_at of a migration bundle, '' when unset or zero.

    path may be bundle.json, a bundle directory, the community-transfer
    --report JSON, a direct-mode --work-dir or its spool.sqlite3. Only the
    header is read (bundle.json writes it before the record sections), so a
    multi-GB bundle is not loaded."""
    import sqlite3
    from pathlib import Path
    p = Path(path)
    if p.is_dir():
        for name in ("bundle.json", "spool.sqlite3"):
            if (p / name).is_file():
                p = p / name
                break
        else:
            raise ValueError("%s has no bundle.json or spool.sqlite3" % path)
    with p.open("rb") as f:
        magic = f.read(16)
    source = None
    if magic.startswith(b"SQLite format 3"):
        db = sqlite3.connect("file:%s?mode=ro" % p, uri=True)
        try:
            row = db.execute("SELECT v FROM meta WHERE k = 'header'").fetchone()
        finally:
            db.close()
        source = (json.loads(row[0]) if row else {}).get("source")
    else:
        with p.open("r", encoding="utf-8", errors="replace") as f:
            head = f.read(1 << 20)
        m = re.search(r'"source"\s*:\s*', head)
        if m:
            try:
                source, _ = json.JSONDecoder().raw_decode(head, m.end())
            except ValueError:
                source = None
    value = (source or {}).get("exported_at") if isinstance(source, dict) else ""
    value = value if isinstance(value, str) else ""
    # Go encodes an unset time.Time as 0001-01-01T00:00:00Z.
    if value.startswith("0001-01-01") or not norm_ts(value):
        return ""
    return value


def active_after_extraction(want, got, extracted):
    """8m: the source last-active time moved on after extraction."""
    if not want:
        return False
    if extracted:
        return want > extracted and (not got or got <= want)
    return bool(got) and got < want


def pin_check(mk, cm, origin, rep):
    if not mk.has_table("user_note_pining"):
        rep.add("9 pinned notes (local users)", None, "misskey has no user_note_pining table")
        return
    r = mk.q('SELECT count(*) FILTER (WHERE u.host IS NULL), count(*) FILTER (WHERE u.host IS NOT NULL) '
             'FROM user_note_pining p JOIN "user" u ON u.id = p."userId" WHERE (to_jsonb(u)->>\'isDeleted\') IS DISTINCT FROM \'true\'')
    mk_local, mk_remote = (int(x or 0) for x in (r[0] if r and len(r[0]) >= 2 else (0, 0)))
    cm_local = int(cm.scalar("SELECT count(*) FROM pin_statuses p JOIN accounts a ON a.id = p.account_id "
                             "WHERE a.domain IS NULL AND a.deleted_at IS NULL AND a.local_actor_uri LIKE %s" % lit(origin + "/users/%")) or 0)
    cm_remote = int(cm.scalar("SELECT count(*) FROM pin_statuses p JOIN accounts a ON a.id = p.account_id WHERE a.domain IS NOT NULL") or 0)
    rep.add("9 pinned notes (local users)", cm_local == mk_local and not (mk_local and not cm_local),
            "misskey local users' pins=%d; community pins of imported local users=%d" % (mk_local, cm_local))
    if mk_remote or cm_remote:
        rep.info("9 pinned notes (local users)", "remote users' pins skipped by design: misskey=%d community=%d (fetched from their servers' featured collections by federation)"
                 % (mk_remote, cm_remote))


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
    rep.add("0 target database populated", cm_local > 0 and (mk_live == 0 or cm_imported > 0),
            "community local accounts=%d, with %s/users/* identity=%d" % (cm_local, origin, cm_imported))
    rep.add("1 local user count", mk_total > 0 and cm_imported > 0 and cm_imported in (mk_total, mk_live),
            "misskey local=%d (not deleted=%d); community accounts with %s/users/* identity=%d (all local=%d)" % (mk_total, mk_live, origin, cm_imported, cm_local))

    # sample users (exclude system actors such as instance.actor / relay.actor whose usernames contain dots)
    users = mk.q('SELECT u.id, u.username, k."publicKey" FROM "user" u LEFT JOIN user_keypair k ON k."userId" = u.id '
                 'WHERE u.host IS NULL AND u."isDeleted" = false AND u.username NOT LIKE \'%.%\'')
    users = [u for u in users if SAFE_ID.match(u[0])]
    # (1b) every live local Misskey user must exist in Community (not just the count)
    want_uris = {"%s/users/%s" % (origin, u[0]): u for u in users}
    have = {r[0] for r in cm.q("SELECT local_actor_uri FROM accounts WHERE domain IS NULL AND deleted_at IS NULL AND local_actor_uri LIKE %s" % lit(origin + "/users/%"))}
    missing = sorted(set(want_uris) - have)
    rep.add("1b every local user imported", bool(users) and not missing, "%d/%d misskey local users present" % (len(want_uris) - len(missing), len(want_uris)),
            ["%s @%s: no community account" % (want_uris[u][0], want_uris[u][1]) for u in missing])
    sample = rnd.sample(users, min(args.sample, len(users)))

    # (2) actor + key + webfinger
    fails, ok_n = [], 0
    for uid, uname, pem in sample:
        want = "%s/users/%s" % (origin, uid)
        st, actor = http.json("/users/" + uid)
        if st != 200 or not isinstance(actor, dict):
            fails.append("%s @%s: GET /users/%s -> %s" % (uid, uname, uid, http.describe(st))); continue
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
            fails.append("%s @%s: webfinger %s self=%s" % (uid, uname, http.describe(st), hrefs)); continue
        ok_n += 1
    rep.add("2 actor id/publicKey/webfinger", bool(sample) and ok_n == len(sample) and not fails, "%d/%d sampled users OK" % (ok_n, len(sample)), fails)

    # (3) notes
    notes = mk.q('SELECT id FROM note WHERE "userHost" IS NULL AND visibility IN (\'public\', \'home\') ORDER BY random() LIMIT %d' % args.sample)
    fails, ok_n = [], 0
    for (nid,) in notes:
        if not SAFE_ID.match(nid):
            fails.append("%r: unsafe note id, cannot verify" % nid[:40]); continue
        st, obj = http.json("/notes/" + nid)
        want = "%s/notes/%s" % (origin, nid)
        # Pure renotes are served as the Announce Misskey published (id <uri>/activity).
        if st == 200 and isinstance(obj, dict) and (obj.get("id") == want or (obj.get("type") == "Announce" and obj.get("id") == want + "/activity")):
            ok_n += 1
        else:
            fails.append("%s: %s id=%r" % (nid, http.describe(st), (obj or {}).get("id") if isinstance(obj, dict) else None))
    rep.add("3 public/home note objects", bool(notes) and ok_n == len(notes) and not fails, "%d/%d sampled notes OK" % (ok_n, len(notes)), fails)

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

    # (5) instance / relay actor. Misskey's system users instance.actor and
    # relay.actor are imported keeping their actor URI (<origin>/users/<id>),
    # keyId (<uri>#main-key) and keypair, and are designated through
    # system_settings, so remote caches and relay subscriptions stay valid.
    def system_actor(username, setting, name, http_paths):
        mk_row = mk.q('SELECT u.id, k."publicKey" FROM "user" u LEFT JOIN user_keypair k ON k."userId" = u.id '
                      "WHERE u.host IS NULL AND u.username = %s LIMIT 1" % lit(username))
        if not mk_row:
            rep.add(name, None if username == "relay.actor" else False, "misskey has no local %s user" % username)
            return None
        mk_id, mk_pem = mk_row[0][0], mk_row[0][1]
        uri = origin + "/users/" + mk_id
        mk_h = key_hash(mk_pem)
        row = cm.q("SELECT a.id, k.public_key, k.key_id FROM accounts a LEFT JOIN account_keys k ON k.account_id = a.id "
                   "WHERE a.domain IS NULL AND a.deleted_at IS NULL AND a.local_actor_uri = %s ORDER BY a.id LIMIT 1" % lit(uri))
        db_h = key_hash(row[0][1]) if row else None
        key_id = (row[0][2] or uri + "#main-key") if row else None
        designated = cm.scalar("SELECT s.val FROM system_settings s WHERE s.key = %s" % lit(setting))
        designated = (designated or "").strip().strip('"')
        fails, seen = [], []
        for path in http_paths(mk_id):
            st, actor = http.json(path)
            actor = actor if isinstance(actor, dict) else {}
            pk = actor.get("publicKey") if isinstance(actor.get("publicKey"), dict) else {}
            seen.append("GET %s %s id=%r keyId=%r" % (path, http.describe(st), actor.get("id"), pk.get("id")))
            if actor.get("id") != uri or pk.get("id") != uri + "#main-key" or key_hash(pk.get("publicKeyPem")) != mk_h:
                fails.append("%s: id=%r keyId=%r key sha256=%s" % (path, actor.get("id"), pk.get("id"), key_hash(pk.get("publicKeyPem"))))
        ok = bool(mk_h) and db_h == mk_h and key_id == uri + "#main-key" and designated == uri and not fails
        rep.add(name, ok, "misskey %s uri=%s key sha256=%s; db account=%s key sha256=%s keyId=%s; %s=%r; %s" %
                (username, uri, mk_h, row[0][0] if row else None, db_h, key_id, setting, designated or None, "; ".join(seen)), fails)
        return uri

    system_actor("instance.actor", "federation.instance_actor_uri", "5 instance actor key",
                 lambda mk_id: ["/users/" + mk_id, "/actor"])
    relay_uri = system_actor("relay.actor", "federation.relay_actor_uri", "5b relay actor key", lambda mk_id: ["/users/" + mk_id])
    mk_relays = int(mk.scalar("SELECT count(*) FROM relay") or 0) if mk.has_table("relay") else 0
    if mk_relays:
        cm_relays = int(cm.scalar("SELECT count(*) FROM relays WHERE deleted_at IS NULL AND actor_uri = %s" % lit(relay_uri or "")) or 0)
        rep.add("5c relay subscriptions", cm_relays == mk_relays, "misskey relays=%d community relays followed by relay.actor=%d" % (mk_relays, cm_relays))

    # (6) emoji + drive files
    mk_emoji = int(mk.scalar("SELECT count(*) FROM emoji WHERE host IS NULL"))
    cm_emoji = int(cm.scalar("SELECT count(*) FROM emojis WHERE (domain = '' OR domain IS NULL) AND deleted_at IS NULL"))
    rep.add("6a local custom emoji count", within(mk_emoji, cm_emoji, args.tolerance) and not (mk_emoji and not cm_emoji), "misskey=%d community=%d (tolerance %.0f%%)" % (mk_emoji, cm_emoji, args.tolerance * 100))
    if getattr(args, "skip_unattached_drive_files", False):
        # Drive-only files were not migrated: compare referenced local files only.
        mk_files = int(mk.scalar('SELECT count(*) FROM drive_file f WHERE f."userHost" IS NULL AND f."userId" IS NOT NULL AND (EXISTS (SELECT 1 FROM note n WHERE f.id = ANY(n."fileIds")) '
                                 'OR EXISTS (SELECT 1 FROM "user" u WHERE u."avatarId" = f.id OR u."bannerId" = f.id))'))
    else:
        # Ownerless files (userId NULL: custom emoji images, instance assets)
        # are not user media; emoji images are checked in 6c.
        mk_files = int(mk.scalar('SELECT count(*) FROM drive_file WHERE "userHost" IS NULL AND "userId" IS NOT NULL'))
    mk_files_all = int(mk.scalar('SELECT count(*) FROM drive_file WHERE "userId" IS NOT NULL'))
    cm_files = int(cm.scalar("SELECT count(*) FROM media_attachments m JOIN accounts a ON a.id = m.account_id WHERE a.domain IS NULL"))
    cm_files_all = int(cm.scalar("SELECT count(*) FROM media_attachments"))
    rep.add("6b drive file / media count", (within(mk_files, cm_files, args.tolerance) or within(mk_files_all, cm_files_all, args.tolerance)) and not (mk_files and not cm_files_all),
            "misskey local=%d all=%d; community local-owner media=%d all=%d (tolerance %.0f%%)" % (mk_files, mk_files_all, cm_files, cm_files_all, args.tolerance * 100))
    # 6c: ownerless local drive files used as custom emoji images become emoji
    # images, not media; each must have produced a local emoji with an image.
    mk_emoji_files = int(mk.scalar('SELECT count(DISTINCT f.id) FROM drive_file f JOIN emoji e ON e.host IS NULL AND (e."originalUrl" = f.url OR e."publicUrl" = f.url) '
                                   'WHERE f."userHost" IS NULL AND f."userId" IS NULL') or 0)
    mk_ownerless = int(mk.scalar('SELECT count(*) FROM drive_file WHERE "userHost" IS NULL AND "userId" IS NULL') or 0)
    cm_emoji_images = int(cm.scalar("SELECT count(*) FROM emojis WHERE (domain = '' OR domain IS NULL) AND deleted_at IS NULL AND url <> ''") or 0)
    rep.add("6c emoji image files", cm_emoji_images >= mk_emoji_files - int(mk_emoji_files * args.tolerance),
            "misskey ownerless emoji image files=%d; community local emojis with an image=%d; other ownerless files=%d (instance assets: see the import report's misskey_meta_asset issues)"
            % (mk_emoji_files, cm_emoji_images, mk_ownerless - mk_emoji_files))

    # (7) inbox routes (POST-only route: GET/OPTIONS should yield 405/400/401, never 404)
    fails, ok_n = [], 0
    for uid, uname, _ in sample:
        res = [http.req3("/users/%s/inbox" % uid, method=m) for m in ("OPTIONS", "GET")]
        codes = [r[0] for r in res]
        if all(c in (None, 404) for c in codes):
            fails.append("%s @%s: OPTIONS/GET -> %s" % (uid, uname, ", ".join("HTTP %s" % r[0] if r[0] is not None else "no response (%s)" % r[2] for r in res)))
        else:
            ok_n += 1
    rep.add("7 inbox route exists", bool(sample) and not fails, "%d/%d sampled inbox paths routed" % (ok_n, len(sample)), fails)

    # (8) Misskey profile extensions stored in accounts columns
    profile_checks(mk, cm, origin, rep)
    privacy_checks(mk, cm, origin, rep, getattr(args, "extracted_at", None))

    # (9) pinned notes: only local users' pins are imported; a remote user's
    # pins are its server's featured collection, refreshed by federation.
    pin_check(mk, cm, origin, rep)

    failed = [n for n, s in rep.results if s == "FAIL"]
    print("\nSUMMARY: %d checks, %d PASS, %d FAIL" % (len(rep.results), len(rep.results) - len(failed), len(failed)))
    return 1 if failed else 0


def default_extracted_at(args, out=sys.stderr):
    """--extracted-at falls back to the bundle's source.exported_at (non-zero)."""
    if args.extracted_at or not getattr(args, "bundle", None):
        return
    try:
        value = source_exported_at(args.bundle)
    except (OSError, ValueError) as e:
        print("WARNING: could not read source.exported_at from --bundle (%s); 8m runs without --extracted-at" % e, file=out)
        return
    if value:
        args.extracted_at = value
        print("using --extracted-at %s (source.exported_at of --bundle)" % value, file=out)
    else:
        print("NOTE: --bundle has no source.exported_at; 8m runs without --extracted-at", file=out)


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
    ap.add_argument("--skip-unattached-drive-files", action="store_true", help="the import ran with --skip-unattached-drive-files: 6b compares only files a note/avatar/banner references")
    ap.add_argument("--extracted-at", default=os.environ.get("EXTRACTED_AT"),
                    help="when the source was read (the bundle's source.exported_at, ISO 8601; env EXTRACTED_AT): 8m reports accounts active on the source after it as info")
    ap.add_argument("--bundle", default=os.environ.get("BUNDLE"),
                    help="bundle.json / bundle dir, community-transfer --report JSON, or direct-mode --work-dir (env BUNDLE): "
                         "--extracted-at defaults to its source.exported_at when that is set")
    ap.add_argument("--forwarded-proto", default="https", help="X-Forwarded-Proto to send ('' to omit; default https)")
    ap.add_argument("--timeout", type=int, default=30, help="per query/request timeout seconds")
    ap.add_argument("-v", "--verbose", action="store_true", help="list every failure")
    args = ap.parse_args()
    missing = [n for n, v in (("MISSKEY_DB_DSN", args.misskey_dsn), ("COMMUNITY_DB_DSN", args.community_dsn), ("COMMUNITY_BASE", args.base)) if not v]
    if missing:
        ap.error("missing: " + ", ".join(missing))
    default_extracted_at(args)
    try:
        sys.exit(run(args))
    except Exception as e:  # report without traceback (could contain DSNs)
        print("ERROR: %s" % e, file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    main()
