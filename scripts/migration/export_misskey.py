#!/usr/bin/env python3
"""Read-only Misskey PostgreSQL -> private community-transfer/v1 directory.

The export runs in two steps that share a resumable state directory:

  fetch  pages every needed table with keyset pagination
         (WHERE pk > last ORDER BY pk LIMIT --batch-size), each page in its own
         short read-only transaction (default_transaction_read_only=on), and
         upserts the rows into a local SQLite staging file. Progress is
         checkpointed per table, so an interrupted run resumes where it stopped.
  build  streams the staged rows into bundle.json (written incrementally) and
         copies media bytes from the trusted files snapshot.

By default only data relevant to the local instance is exported: local users,
notes, files and everything they own, plus the remote accounts, notes, files and
emojis those reference (follows, replies/quotes/renote targets, remote replies
to local notes, reactions on local notes, ...). --include-remote-cache exports
every row of every table instead.

Two-phase migration: run with --fetch-only while the source is live (bulk
pass), put the source in maintenance / stop writes, then run --final-sync,
which fetches new rows, re-fetches rows whose updatedAt changed, drops staged
rows that were deleted at the source, and builds the bundle.

Use standard PG* environment/PGPASSFILE (or --dsn without a password), never a
password argument. No remote URL is fetched. See README-misskey.md.
Exit codes: 0 exported, 2 exported with blocking issues, 1 failed.
"""
import argparse
from collections import defaultdict
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import sqlite3
import stat
import subprocess
import sys
import tempfile
import time
import traceback
from urllib.parse import unquote, urlsplit

SCHEMA = "community-transfer/v1"
METHODS = ("aid", "aidx", "meid", "meidg", "objectid", "ulid")
SECTIONS = ("accounts", "credentials", "keys", "posts", "media", "relations", "lists", "reactions", "bookmarks",
            "polls", "poll_votes", "emojis", "records", "issues")
IDENT = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


class ExportError(Exception):
    """A diagnostic that is safe to print (secrets are redacted)."""


_REDACT = [(re.compile(r"(password\s*=\s*)('[^']*'|\S+)", re.I), r"\1***"),
           (re.compile(r"(postgres(?:ql)?://[^:/@\s]*:)[^@\s]*@", re.I), r"\1***@"),
           (re.compile(r"\$2[aby]\$\d\d\$[./A-Za-z0-9]{20,}"), "***"),
           (re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----", re.S), "***")]


def redact(text):
    text = str(text)
    for pattern, repl in _REDACT:
        text = pattern.sub(repl, text)
    return text


def dim(value):
    """Image dimension as a non-negative int; Misskey stores floats such as 1665.517."""
    try:
        v = float(value or 0)
    except (TypeError, ValueError):
        return 0
    return int(round(v)) if v == v and 0 < v < 1e9 else 0


def sid(value):
    return "" if value is None else str(value)


def qi(name):
    """Quote a catalog identifier; only plain identifiers are accepted."""
    if not IDENT.match(name):
        raise ExportError("unsupported identifier in source catalog: %r" % name[:64])
    return '"%s"' % name


def origin(value):
    u = urlsplit(value)
    if u.scheme not in ("https", "http") or not u.hostname or u.username or u.password or u.query or u.fragment or u.path not in ("", "/"):
        raise ExportError("base-url must be an explicit HTTP(S) origin without credentials/path")
    return value.rstrip("/")


def _decode_id(value, method):
    if method in ("aid", "aidx"):
        if not re.fullmatch(r"[0-9a-z]{%d}" % (10 if method == "aid" else 16), value):
            raise ValueError("invalid ID")
        ms = int(value[:8], 36) + 946684800000
    elif method in ("meid", "objectid"):
        if not re.fullmatch(r"[0-9a-f]{24}", value):
            raise ValueError("invalid ID")
        ms = int(value[:12], 16) - 0x800000000000 if method == "meid" else int(value[:8], 16) * 1000
    elif method == "meidg":
        if not re.fullmatch(r"g[0-9a-f]{23}", value):
            raise ValueError("invalid ID")
        ms = int(value[1:12], 16)
    elif method == "ulid":
        chars = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
        if len(value) != 26 or any(c not in chars for c in value) or value[0] > "7":
            raise ValueError("invalid ID")
        ms = 0
        for c in value[:10]:
            ms = ms * 32 + chars.index(c)
    else:
        raise ExportError("unsupported source ID method")
    return ms


# Plausible creation window for a decoded ID: 2000-01-01 .. now + 1 day.
_MIN_MS = 946684800000


def detect_id_method(value, preferred=None):
    """Return the ID format of a single value.

    Misskey instances that changed config.id over their lifetime (e.g. early
    10-char `aid` notes followed by `aidx`) hold mixed formats, so the format is
    detected per value. The configured method wins when it decodes plausibly;
    ambiguous 24-hex IDs (meid vs objectid) fall back to the one that yields a
    plausible timestamp.
    """
    value = str(value)
    max_ms = int(time.time() * 1000) + 86400000
    candidates = []
    if preferred in METHODS:
        candidates.append(preferred)
    if re.fullmatch(r"[0-9a-z]{10}", value):
        candidates.append("aid")
    if re.fullmatch(r"[0-9a-z]{16}", value):
        candidates.append("aidx")
    if re.fullmatch(r"g[0-9a-f]{23}", value):
        candidates.append("meidg")
    if re.fullmatch(r"[0-9a-f]{24}", value):
        candidates += ["meid", "objectid"]
    if len(value) == 26:
        candidates.append("ulid")
    for m in candidates:
        try:
            ms = _decode_id(value, m)
        except (ValueError, OverflowError):
            continue
        if _MIN_MS <= ms <= max_ms:
            return m, ms
    raise ValueError("invalid ID")


def id_time(value, method=None):
    _, ms = detect_id_method(value, method)
    return datetime.fromtimestamp(ms / 1000, timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


# --------------------------------------------------------------------------- progress
class Progress:
    """Periodic one-line progress on stderr (stdout carries the JSON summary)."""

    def __init__(self, interval=5.0, stream=None):
        self.interval = interval
        self.stream = stream or sys.stderr
        self.label = ""
        self.done = self.total = 0
        self.started = self.last = time.monotonic()
        self.start_done = 0

    def stage(self, label, total=0, done=0):
        self.finish()
        self.label, self.total, self.done, self.start_done = label, total, done, done
        self.started = self.last = time.monotonic()
        if total:
            self.emit(force=True)

    def advance(self, n=1):
        self.done += n
        self.emit()

    def line(self):
        elapsed = max(time.monotonic() - self.started, 1e-6)
        rate = (self.done - self.start_done) / elapsed
        parts = ["progress", "stage=%s" % self.label]
        if self.total:
            total = max(self.total, self.done)
            parts.append("done=%d/%d (%.1f%%)" % (self.done, total, 100.0 * self.done / total))
            if rate > 0 and total > self.done:
                parts.append("eta=%s" % fmt_duration((total - self.done) / rate))
        else:
            parts.append("done=%d" % self.done)
        parts.append("speed=%.0f/s" % rate)
        return " ".join(parts)

    def emit(self, force=False):
        now = time.monotonic()
        if force or now - self.last >= self.interval:
            self.last = now
            if self.interval >= 0:
                print(self.line(), file=self.stream, flush=True)

    def finish(self):
        if self.label:
            print(self.line() + " finished", file=self.stream, flush=True)
            self.label = ""


def fmt_duration(seconds):
    seconds = int(seconds)
    h, rem = divmod(seconds, 3600)
    m, s = divmod(rem, 60)
    return "%dh%02dm%02ds" % (h, m, s) if h else ("%dm%02ds" % (m, s) if m else "%ds" % s)


QUIET = Progress(interval=-1, stream=open(os.devnull, "w"))


# --------------------------------------------------------------------------- staging
def sort_key(k):
    k = sid(k)
    return k.rjust(24, "0") if k.isdigit() else k


class Staging:
    """Disk-backed row store: one SQLite file, upserted by (table, key)."""

    def __init__(self, path=":memory:"):
        self.db = sqlite3.connect(str(path), isolation_level=None)
        self.db.execute("PRAGMA journal_mode=WAL" if path != ":memory:" else "PRAGMA journal_mode=MEMORY")
        self.db.execute("PRAGMA synchronous=NORMAL")
        self.db.execute("PRAGMA cache_size=-65536")  # 64 MiB page cache cap
        self.db.executescript("""
            CREATE TABLE IF NOT EXISTS rows(tbl TEXT NOT NULL, k TEXT NOT NULL, sk TEXT NOT NULL, data TEXT NOT NULL, PRIMARY KEY(tbl, k)) WITHOUT ROWID;
            CREATE INDEX IF NOT EXISTS rows_order ON rows(tbl, sk);
            CREATE TABLE IF NOT EXISTS progress(task TEXT PRIMARY KEY, last TEXT, done INTEGER NOT NULL DEFAULT 0, finished INTEGER NOT NULL DEFAULT 0);
            CREATE TABLE IF NOT EXISTS refs_done(task TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY(task, value)) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT);
            CREATE TABLE IF NOT EXISTS pk(tbl TEXT PRIMARY KEY, col TEXT);
        """)
        self.indexes = set()
        self.pk_cache = None

    @classmethod
    def from_dict(cls, tables):
        s = cls()
        s.db.execute("BEGIN")
        for table, rows in tables.items():
            col = next((c for c in ("id", "userId", "noteId", "account_id") if rows and all(c in r for r in rows)), None)
            s.set_pk(table, col)
            s.upsert(table, rows, col)
        s.db.execute("COMMIT")
        return s

    # meta / pk
    def get_meta(self, key, default=None):
        r = self.db.execute("SELECT value FROM meta WHERE key=?", (key,)).fetchone()
        return r[0] if r else default

    def set_meta(self, key, value):
        self.db.execute("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", (key, value))

    def set_pk(self, table, col):
        self.db.execute("INSERT INTO pk(tbl,col) VALUES(?,?) ON CONFLICT(tbl) DO UPDATE SET col=excluded.col", (table, col))
        self.pk_cache = None

    def pk(self, table):
        if self.pk_cache is None:
            self.pk_cache = dict(self.db.execute("SELECT tbl, col FROM pk"))
        return self.pk_cache.get(table)

    # rows
    def upsert(self, table, rows, col=None):
        col = col if col is not None else self.pk(table)
        batch = []
        for r in rows:
            data = r if isinstance(r, str) else json.dumps(r, ensure_ascii=False, separators=(",", ":"))
            if col:
                obj = r if isinstance(r, dict) else json.loads(r)
                k = sid(obj.get(col))
            else:
                k = hashlib.sha256(data.encode()).hexdigest()
            batch.append((table, k, sort_key(k), data))
        self.db.executemany("INSERT INTO rows(tbl,k,sk,data) VALUES(?,?,?,?) ON CONFLICT(tbl,k) DO UPDATE SET data=excluded.data", batch)
        return len(batch)

    def delete(self, table, keys):
        self.db.executemany("DELETE FROM rows WHERE tbl=? AND k=?", [(table, k) for k in keys])

    def tables(self):
        return [r[0] for r in self.db.execute("SELECT DISTINCT tbl FROM rows ORDER BY tbl")]

    def count(self, table):
        return self.db.execute("SELECT count(*) FROM rows WHERE tbl=?", (table,)).fetchone()[0]

    def has(self, table, k):
        return self.db.execute("SELECT 1 FROM rows WHERE tbl=? AND k=?", (table, sid(k))).fetchone() is not None

    def get(self, table, k, default=None):
        r = self.db.execute("SELECT data FROM rows WHERE tbl=? AND k=?", (table, sid(k))).fetchone()
        return json.loads(r[0]) if r else default

    def rows(self, table):
        cur = self.db.execute("SELECT data FROM rows WHERE tbl=? ORDER BY sk", (table,))
        while True:
            chunk = cur.fetchmany(1000)
            if not chunk:
                return
            for (data,) in chunk:
                yield json.loads(data)

    def keys(self, table):
        """Key batches by keyset, safe while rows of the table are deleted."""
        last = ""
        while True:
            chunk = self.db.execute("SELECT k, sk FROM rows WHERE tbl=? AND sk > ? ORDER BY sk LIMIT 5000", (table, last)).fetchall()
            if not chunk:
                return
            last = chunk[-1][1]
            yield [k for (k, _) in chunk]

    def by(self, table, col, value):
        """Rows whose JSON column equals value (expression index created on demand)."""
        if not IDENT.match(col):
            raise ExportError("bad column")
        if (table, col) not in self.indexes:
            name = "ix_" + hashlib.sha1((table + "." + col).encode()).hexdigest()[:16]
            self.db.execute("CREATE INDEX IF NOT EXISTS %s ON rows(tbl, json_extract(data, '$.%s'), sk)" % (name, col))
            self.indexes.add((table, col))
        value = int(value) if isinstance(value, int) or (isinstance(value, str) and value.isdigit() and len(value) < 19) else value
        out = [json.loads(d) for (d,) in self.db.execute(
            "SELECT data FROM rows WHERE tbl=? AND json_extract(data, '$.%s') IN (?, ?) ORDER BY sk" % col, (table, value, sid(value)))]
        return out

    def values(self, table, path, array=False, where=""):
        """Distinct non-null scalar values at a JSON path of staged rows."""
        if array:
            sql = "SELECT DISTINCT CAST(j.value AS TEXT) FROM rows, json_each(rows.data, '$.%s') j WHERE rows.tbl=? %s" % (path, where)
        else:
            sql = "SELECT DISTINCT CAST(json_extract(data, '$.%s') AS TEXT) FROM rows WHERE tbl=? AND json_extract(data, '$.%s') IS NOT NULL %s" % (path, path, where)
        cur = self.db.execute(sql, (table,))
        while True:
            chunk = cur.fetchmany(5000)
            if not chunk:
                return
            for (v,) in chunk:
                if v is not None and v != "":
                    yield v

    # progress
    def task(self, name):
        r = self.db.execute("SELECT last, done, finished FROM progress WHERE task=?", (name,)).fetchone()
        return r if r else (None, 0, 0)

    def save_task(self, name, last, done, finished=0):
        self.db.execute("INSERT INTO progress(task,last,done,finished) VALUES(?,?,?,?) ON CONFLICT(task) DO UPDATE SET last=excluded.last, done=excluded.done, finished=excluded.finished",
                        (name, last, done, finished))

    def ref_done(self, task, value):
        return self.db.execute("SELECT 1 FROM refs_done WHERE task=? AND value=?", (task, value)).fetchone() is not None

    def mark_refs(self, task, values):
        self.db.executemany("INSERT OR IGNORE INTO refs_done(task,value) VALUES(?,?)", [(task, v) for v in values])


class StagingTables:
    """dict-like read view: tables.get(name, []) -> iterable of rows."""

    def __init__(self, staging):
        self.s = staging

    def get(self, table, default=None):
        return _Rows(self.s, table)

    def __contains__(self, table):
        return self.s.count(table) > 0

    def __iter__(self):
        return iter(self.s.tables())


class _Rows:
    def __init__(self, s, table):
        self.s, self.table = s, table

    def __iter__(self):
        return self.s.rows(self.table)

    def __bool__(self):
        return self.s.db.execute("SELECT 1 FROM rows WHERE tbl=? LIMIT 1", (self.table,)).fetchone() is not None

    def __len__(self):
        return self.s.count(self.table)


class StagingMap:
    """Mapping view of one staged table keyed by its primary key."""

    def __init__(self, staging, table):
        self.s, self.table = staging, table

    def get(self, k, default=None):
        return self.s.get(self.table, k, default)

    def __contains__(self, k):
        return self.s.has(self.table, k)

    def items(self):
        col = self.s.pk(self.table) or "id"
        for r in self.s.rows(self.table):
            yield sid(r.get(col)), r

    def values(self):
        return self.s.rows(self.table)

    def __len__(self):
        return self.s.count(self.table)


# --------------------------------------------------------------------------- output
class FileSink:
    """Streams each bundle section into its own spool file, then assembles bundle.json."""

    def __init__(self, output, header):
        self.output = Path(output)
        self.header = header
        self.files, self.counts = {}, defaultdict(int)
        self.severity = defaultdict(int)
        spool = self.output / ".spool"
        spool.mkdir(mode=0o700, exist_ok=True)
        self.spool = spool
        self.bundle = dict(header)
        for key in SECTIONS:
            self.bundle[key] = _Section(self, key)

    def append(self, key, obj):
        f = self.files.get(key)
        if f is None:
            f = self.files[key] = (self.spool / key).open("w", encoding="utf-8")
        f.write("," if self.counts[key] else "")
        f.write(json.dumps(obj, ensure_ascii=False, separators=(",", ":")))
        self.counts[key] += 1
        if key == "issues":
            self.severity[obj.get("severity")] += 1

    def finish(self):
        for f in self.files.values():
            f.close()
        tmp = self.output / "bundle.json.partial"
        with tmp.open("x", encoding="utf-8") as out:
            head = json.dumps(self.header, ensure_ascii=False, separators=(",", ":"))
            out.write(head[:-1])
            for key in SECTIONS:
                out.write(',"%s":[' % key)
                p = self.spool / key
                if p.exists():
                    with p.open(encoding="utf-8") as src:
                        shutil.copyfileobj(src, out, 1 << 20)
                    p.unlink()
                out.write("]")
            out.write("}")
            out.flush()
            os.fsync(out.fileno())
        os.replace(tmp, self.output / "bundle.json")
        shutil.rmtree(self.spool, ignore_errors=True)


class StreamSink(FileSink):
    """Direct mode: write NDJSON records to a stream instead of assembling bundle.json.

    Line 1 is {"header": {...}}; every following line is {"s": <section>, "v": <record>}.
    `community-transfer --source-dsn` reads this from the exporter's stdout and
    spools it into its own SQLite staging file, so no bundle.json is ever built.
    Media bytes (copy mode) are still written under --output.
    """

    def __init__(self, output, header, stream=None):
        self.output = Path(output)
        self.header = header
        self.stream = stream or sys.stdout
        self.files, self.counts = {}, defaultdict(int)
        self.severity = defaultdict(int)
        self.bundle = dict(header)
        for key in SECTIONS:
            self.bundle[key] = _Section(self, key)
        self.stream.write(json.dumps({"header": header}, ensure_ascii=False, separators=(",", ":")) + "\n")

    def append(self, key, obj):
        self.stream.write(json.dumps({"s": key, "v": obj}, ensure_ascii=False, separators=(",", ":")) + "\n")
        self.counts[key] += 1
        if key == "issues":
            self.severity[obj.get("severity")] += 1

    def finish(self):
        self.stream.flush()


def make_sink(args, output, header):
    return StreamSink(output, header) if getattr(args, "emit", "bundle") == "ndjson" else FileSink(output, header)


class _Section:
    def __init__(self, sink, key):
        self.sink, self.key = sink, key

    def append(self, obj):
        self.sink.append(self.key, obj)

    def __len__(self):
        return self.sink.counts[self.key]


def pg_array(values):
    return "{" + ",".join('"%s"' % sid(v).replace("\\", "\\\\").replace('"', '\\"') for v in values) + "}"


# --------------------------------------------------------------------------- source
class PsqlSource:
    """Keyset-paged reads through psql; every call is its own read-only transaction."""

    def __init__(self, workdir, dsn="", batch_size=5000, max_rows_per_sec=0.0, batch_sleep=0.0, statement_timeout="300s"):
        if dsn and re.search(r"password\s*=|://[^/@]*:[^/@]*@", dsn, re.I):
            raise ExportError("--dsn must not contain a password; use PGPASSFILE")
        self.workdir = Path(workdir)
        self.dsn = dsn
        self.batch_size = batch_size
        self.max_rows_per_sec = max_rows_per_sec
        self.batch_sleep = batch_sleep
        self.statement_timeout = statement_timeout
        self.rows_read = 0
        self.t0 = time.monotonic()
        self.catalog = None
        self.warned = set()

    def run(self, sql, variables=None):
        """Run one statement; yield one stripped output line per row."""
        env = dict(os.environ)
        env["PGOPTIONS"] = (env.get("PGOPTIONS", "") + " -c default_transaction_read_only=on -c statement_timeout=%s -c lock_timeout=5s -c TimeZone=UTC" % self.statement_timeout).strip()
        cmd = ["psql", "-X", "-q", "-A", "-t", "-w", "-v", "ON_ERROR_STOP=1"]
        for k, v in (variables or {}).items():
            cmd += ["-v", "%s=%s" % (k, v)]
        if self.dsn:
            cmd += ["-d", self.dsn]
        script = "BEGIN TRANSACTION ISOLATION LEVEL READ COMMITTED READ ONLY;\n" + sql.rstrip().rstrip(";") + ";\nCOMMIT;\n"
        with tempfile.TemporaryFile(dir=self.workdir) as errors:
            proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=errors, env=env)
            try:
                proc.stdin.write(script.encode())
                proc.stdin.close()
                for line in proc.stdout:
                    line = line.rstrip(b"\n")
                    if line:
                        yield line
                code = proc.wait()
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.wait()
                proc.stdout.close()
            if code == 0:
                errors.seek(0)
                for warn in errors.read()[-4000:].decode("utf-8", "replace").splitlines():
                    warn = redact(warn.strip())
                    if warn and warn not in self.warned:
                        self.warned.add(warn)
                        print("psql: " + warn, file=sys.stderr, flush=True)
            if code != 0:
                errors.seek(0)
                detail = errors.read()[-4000:].decode("utf-8", "replace").strip()
                raise ExportError("psql exited with code %d: %s" % (code, redact(detail) or "(no message)"))

    def throttle(self, n):
        self.rows_read += n
        if self.batch_sleep:
            time.sleep(self.batch_sleep)
        if self.max_rows_per_sec > 0:
            ahead = self.rows_read / self.max_rows_per_sec - (time.monotonic() - self.t0)
            if ahead > 0:
                time.sleep(ahead)

    def load_catalog(self):
        if self.catalog is None:
            sql = r"""SELECT json_build_object('t', c.relname, 'cols', (SELECT json_agg(a.attname ORDER BY a.attnum) FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped),
                'pk', (SELECT CASE WHEN array_length(i.indkey::int2[],1)=1 THEN (SELECT attname FROM pg_attribute WHERE attrelid=c.oid AND attnum=i.indkey[0]) END FROM pg_index i WHERE i.indrelid=c.oid AND i.indisprimary),
                'est', GREATEST(c.reltuples,0)::bigint)
                FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p') ORDER BY c.relname"""
            self.catalog = {}
            for line in self.run(sql):
                r = json.loads(line)
                self.catalog[r["t"]] = {"cols": set(r["cols"] or []), "pk": r["pk"], "est": r["est"]}
        return self.catalog

    def scalar(self, sql, variables=None):
        out = list(self.run(sql, variables))
        return out[0].decode() if out else ""


# --------------------------------------------------------------------------- fetch plan
class Fetcher:
    """Executes a plan of scan/ref steps against the source into staging."""

    def __init__(self, source, staging, progress=QUIET, final_sync=False, labels=None):
        self.src, self.s, self.p = source, staging, progress
        self.final_sync = final_sync
        self.labels = labels or {}
        self.cat = source.load_catalog()

    def label(self, table, suffix=""):
        return self.labels.get(table, table) + (suffix and "/" + suffix)

    def cols(self, table):
        return self.cat.get(table, {}).get("cols", set())

    def exists(self, table, *cols):
        return table in self.cat and all(c in self.cols(table) for c in cols)

    def select_expr(self, table):
        return "row_to_json(t)::text"

    def scan(self, table, where="TRUE", name=None, extra=""):
        """Keyset scan of a filtered table. Resumable via progress[name]."""
        if table not in self.cat:
            return
        pk = self.cat[table]["pk"]
        name = name or "scan:" + table
        self.s.set_pk(table, pk)
        last, done, finished = self.s.task(name)
        if finished and not self.final_sync:
            return
        total = self.count(table, where) + (0 if not finished else 0)
        self.p.stage(self.label(table, "" if name == "scan:" + table else name.split(":", 2)[-1]), total, done)
        base = "SELECT %s FROM public.%s t WHERE (%s)%s" % (self.select_expr(table), qi(table), where, extra)
        if not pk:
            # No single-column primary key: re-read whole (streamed in batches; keyed by row hash).
            self.s.db.execute("BEGIN")
            self.s.db.execute("DELETE FROM rows WHERE tbl=?", (table,))
            done, batch = 0, []
            for line in self.src.run(base):
                batch.append(line.decode())
                if len(batch) >= self.src.batch_size:
                    done += self.s.upsert(table, batch, None)
                    self.p.advance(len(batch))
                    self.src.throttle(len(batch))
                    batch = []
            done += self.s.upsert(table, batch, None)
            self.p.advance(len(batch))
            self.s.save_task(name, None, done, 1)
            self.s.db.execute("COMMIT")
            return
        while True:
            variables = {}
            sql = base
            if last is not None:
                sql += " AND t.%s > :'last'" % qi(pk)
                variables["last"] = last
            sql += " ORDER BY t.%s LIMIT %d" % (qi(pk), self.src.batch_size)
            rows = [r.decode() for r in self.src.run(sql, variables)]
            if rows:
                last = sid(json.loads(rows[-1]).get(pk))
            done += len(rows)
            self.s.db.execute("BEGIN")
            self.s.upsert(table, rows, pk)
            self.s.save_task(name, last, done, int(len(rows) < self.src.batch_size))
            self.s.db.execute("COMMIT")
            self.p.advance(len(rows))
            self.src.throttle(len(rows))
            if len(rows) < self.src.batch_size:
                return

    def count(self, table, where="TRUE"):
        if where == "TRUE":
            return int(self.cat[table]["est"] or 0)
        try:
            return int(self.src.scalar("SELECT count(*) FROM public.%s t WHERE (%s)" % (qi(table), where)) or 0)
        except ExportError:
            return 0

    def refs(self, table, col, sources, name=None, where="TRUE"):
        """Fetch rows of table whose col is in values gathered from staging."""
        if not self.exists(table, col):
            return
        pk = self.cat[table]["pk"]
        self.s.set_pk(table, pk)
        name = name or "ref:%s.%s" % (table, col)
        pending = []
        for (stbl, path, array, swhere) in sources:
            for v in self.s.values(stbl, path, array, swhere):
                if (col == pk and self.s.has(table, v)) or self.s.ref_done(name, v):
                    continue
                pending.append(v)
        pending = sorted(set(pending))
        if not pending:
            return
        self.p.stage(self.label(table, "referenced"), len(pending), 0)
        chunk = max(1, min(1000, self.src.batch_size))
        for i in range(0, len(pending), chunk):
            values = pending[i:i + chunk]
            sql = "SELECT %s FROM public.%s t WHERE t.%s = ANY(:'vals') AND (%s)" % (self.select_expr(table), qi(table), qi(col), where)
            batch = []
            self.s.db.execute("BEGIN")
            for line in self.src.run(sql, {"vals": pg_array(values)}):
                batch.append(line.decode())
                if len(batch) >= self.src.batch_size:
                    self.s.upsert(table, batch, pk)
                    self.src.throttle(len(batch))
                    batch = []
            self.s.upsert(table, batch, pk)
            self.s.mark_refs(name, values)
            self.s.db.execute("COMMIT")
            self.p.advance(len(values))
            self.src.throttle(len(batch))

    def updated_since(self, table, where, since):
        """Final sync: re-fetch rows changed after the bulk pass started."""
        col = next((c for c in ("updatedAt", "updated_at") if c in self.cols(table)), None)
        if not col or not self.cat[table]["pk"]:
            return
        self.scan(table, where, name="updated:%s:%s" % (table, since), extra=" AND t.%s > '%s'::timestamptz" % (qi(col), since.replace("'", "")))

    def prune_deleted(self, table):
        """Final sync: drop staged rows whose primary key no longer exists at the source."""
        pk = self.cat.get(table, {}).get("pk")
        if not pk or self.s.count(table) == 0:
            return 0
        self.p.stage(self.label(table, "deletions"), self.s.count(table), 0)
        removed = 0
        for keys in self.s.keys(table):
            for i in range(0, len(keys), 1000):
                part = keys[i:i + 1000]
                arr = pg_array(part)
                present = {l.decode() for l in self.src.run("SELECT t.%s::text FROM public.%s t WHERE t.%s = ANY(:'vals')" % (qi(pk), qi(table), qi(pk)), {"vals": arr})}
                gone = [k for k in part if k not in present]
                if gone:
                    self.s.delete(table, gone)
                    removed += len(gone)
                self.p.advance(len(part))
                self.src.throttle(len(part))
        return removed


class Converter:
    def __init__(self, tables, base_url, method, source_dir, output, version="", snapshot="", max_media_bytes=8 << 30,
                 object_mode="error", object_dir=None, object_base_url="", sink=None, progress=QUIET):
        # tables: a Staging store (streaming export) or a {table: [rows]} dict (tests).
        self.staging = tables if isinstance(tables, Staging) else Staging.from_dict(tables)
        self.tables = StagingTables(self.staging)
        self.progress = progress
        self.base = origin(base_url)
        if method not in METHODS:
            raise ExportError("unsupported source ID method")
        self.method = method
        self.source_dir = Path(source_dir).resolve(strict=True) if source_dir else None
        # Object storage (storedInternal=false): "error" (default) refuses a
        # metadata-only backup; "copy" reads bytes from object_dir, a local
        # mirror of the bucket keyed by accessKey; "reference" keeps the
        # original object URLs, for a target that keeps serving the same
        # bucket (no bytes are copied).
        if object_mode not in ("error", "copy", "reference"):
            raise ExportError("unsupported object storage mode")
        self.object_mode = object_mode
        self.object_dir = Path(object_dir).resolve(strict=True) if object_dir else None
        if object_mode == "copy" and not self.object_dir:
            raise ExportError("object storage copy mode requires a bucket mirror directory")
        self.object_base = (object_base_url or "").rstrip("/")
        self.output = Path(output)
        self.remaining = max_media_bytes
        self.copied = {}
        header = {"schema": SCHEMA, "scope": "site", "source": {
            "platform": "misskey", "version": version, "base_url": self.base,
            "snapshot_id": snapshot, "exported_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")}}
        self.sink = sink
        if sink is None:
            self.bundle = dict(header, **{key: [] for key in SECTIONS})
        else:
            self.bundle = sink.bundle
        # Lookups stay disk-backed; only small local sets live in memory.
        self.users = StagingMap(self.staging, "user")
        self.profiles = StagingMap(self.staging, "user_profile")
        self.local_users = {sid(r["id"]) for r in self.tables.get("user") if r.get("host") is None}
        self.post_ids = None
        self.media_urls = {}
        self.emoji_urls = {r.get("originalUrl") or r.get("publicUrl") for r in self.tables.get("emoji")} - {None, ""}
        self.emoji_ids = {(r.get("name"), r.get("host") or ""): sid(r["id"]) for r in self.tables.get("emoji")}
        self.system_users = {sid(r.get("userId")) for r in self.tables.get("system_account")}
        # Streaming exports drop dangling references (deleted rows, remote cache
        # outside the local-only selection) so the importer sees a closed graph.
        self.trim_references = sink is not None

    def is_local(self, user_id):
        return sid(user_id) in self.local_users

    def known(self, table, id_):
        return not self.trim_references or not id_ or self.staging.has(table, id_)

    def trimmed(self, kind, id_, what):
        self.issue("info", "remote_reference_trimmed", kind, id_, "A reference to %s outside the local-only export was dropped; the raw source row keeps it." % what)

    def stage(self, label, table=None, total=None):
        self.progress.stage("build/" + label, total if total is not None else (self.staging.count(table) if table else 0))

    def issue(self, severity, code, kind, id_, message):
        self.bundle["issues"].append(dict(severity=severity, code=code, kind=kind, source_id=sid(id_), message=message))

    def time(self, row, key="id"):
        if row.get("createdAt"):
            return row["createdAt"]
        try:
            return id_time(row[key], self.method)
        except (ValueError, KeyError, OverflowError, OSError):
            self.issue("error", "invalid_source_time", "id", row.get(key), "Source ID does not match any known Misskey ID format (aid, aidx, meid, meidg, ulid, objectid).")
            return "0001-01-01T00:00:00Z"

    def actor(self, user):
        return self.base + "/users/" + sid(user["id"]) if user.get("host") is None else (user.get("uri") or "")

    def preserve(self):
        for table in sorted(self.tables):
            rows = self.tables.get(table)
            self.stage("records:" + table, table)
            for i, row in enumerate(rows):
                self.bundle["records"].append({"kind": "misskey:" + table,
                    "id": sid(row.get("id") or row.get("userId") or row.get("noteId") or i),
                    "owner_id": sid(row.get("userId") or row.get("fromUserId")), "data": row})
                self.progress.advance()
            if rows:
                self.issue("warning", "source_records_preserved", table, "", "Complete source rows are preserved privately; preservation does not imply equivalent live support.")
        self.issue("warning", "source_auth_material", "credentials", "", "Private raw records contain source authentication/security material. Do not publish this bundle; sessions, MFA and unsupported settings require explicit migration policy.")
        self.issue("info", "source_id_method", "source", "", "Source ID timestamp method: " + self.method)

    def accounts(self):
        self.stage("accounts", "user")
        for id_, u in self.users.items():
            self.progress.advance()
            p = self.profiles.get(id_, {})
            a = {"id": id_, "username": u.get("username") or "", "local": u.get("host") is None,
                 "domain": u.get("host") or "", "actor_uri": self.actor(u), "system": id_ in self.system_users,
                 "display_name": u.get("name") or "", "note": p.get("description") or "", "note_html": False,
                 "fields": [{"name": f.get("name", ""), "value": f.get("value", "")} for f in p.get("fields", [])],
                 "avatar_id": self.media_ref(u.get("avatarId"), id_), "header_id": self.media_ref(u.get("bannerId"), id_),
                 "avatar_url": u.get("avatarUrl") or "", "header_url": u.get("bannerUrl") or "",
                 "emoji_ids": self.note_emoji_ids({"id": id_, "emojis": u.get("emojis") or [], "userHost": u.get("host")}, kind="account"),
                 "locked": bool(u.get("isLocked")), "bot": bool(u.get("isBot")), "discoverable": bool(u.get("isExplorable")),
                 "suspended": bool(u.get("isSuspended")), "noindex": bool(p.get("noCrawle")),
                 "hide_collections": any(p.get(k) not in (None, "public") for k in ("followingVisibility", "followersVisibility")), "created_at": self.time(u),
                 "settings": {k: v for k, v in u.items() if k in ("isDeleted", "chatScope", "requireSigninToViewContents", "makeNotesFollowersOnlyBefore", "makeNotesHiddenBefore", "movedToUri", "alsoKnownAs", "movedAt")}}
            for src, dst in (("url", "url"), ("inbox", "inbox"), ("sharedInbox", "shared_inbox"), ("updatedAt", "updated_at")):
                if u.get(src):
                    a[dst] = u[src]
            self.bundle["accounts"].append(a)
            if not a["actor_uri"]:
                self.issue("error", "missing_actor_uri", "account", id_, "Remote account has no canonical actor URI.")
            if a["local"] and not a["system"]:
                if not p:
                    self.issue("error", "missing_local_profile", "account", id_, "Local account has no profile/credential source row.")
                self.bundle["credentials"].append({"account_id": id_, "email": p.get("email") or "", "email_verified": bool(p.get("emailVerified")),
                    "password_hash": p.get("password") or "", "password_scheme": "bcrypt", "disabled": id_ in self.system_users or not bool(p.get("password")),
                    "two_factor": bool(p.get("twoFactorEnabled") or p.get("securityKeysAvailable") or p.get("usePasswordLessLogin")),
                    "extensions": ({k: p[k] for k in ("twoFactorEnabled", "twoFactorSecret", "twoFactorBackupSecret", "usePasswordLessLogin", "securityKeysAvailable") if p.get(k)}
                                   if p.get("twoFactorEnabled") or p.get("usePasswordLessLogin") or p.get("securityKeysAvailable") else {})})
        key_users = set()
        self.stage("keys", "user_keypair")
        for k in self.tables.get("user_keypair", []):
            id_ = sid(k["userId"])
            u = self.users.get(id_)
            if not u or u.get("host") is not None:
                self.issue("error", "nonlocal_private_key", "key", id_, "Private key is not owned by a known local account.")
                continue
            key_users.add(id_)
            self.bundle["keys"].append({"account_id": id_, "key_id": self.actor(u) + "#main-key", "public_key": k.get("publicKey") or "",
                                        "private_key": k.get("privateKey") or "", "algorithm": "rsa", "primary": True})
        for id_ in sorted(self.local_users):
            if id_ not in key_users:
                self.issue("error", "missing_local_key", "account", id_, "Local account has no AP keypair; do not silently regenerate its identity.")
        for k in self.tables.get("user_publickey", []):
            if not self.known("user", sid(k["userId"])):
                continue
            self.bundle["keys"].append({"account_id": sid(k["userId"]), "key_id": k.get("keyId") or "", "public_key": k.get("keyPem") or "", "algorithm": "rsa"})

    def copy_asset(self, key, expected_size=None, root=None):
        root = root or self.source_dir
        if not root:
            raise ExportError("Local media source directory was not supplied.")
        if not isinstance(key, str) or not key or "\\" in key or "\x00" in key:
            raise ExportError("Invalid local media key.")
        parts = PurePosixPath(key).parts
        if key.startswith("/") or any(p in ("..", ".") for p in parts) or not parts:
            raise ExportError("Local media key escapes source directory.")
        # openat + O_NOFOLLOW at EVERY level rejects symlink traversal/races.
        root_fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        fd = None
        try:
            for part in parts[:-1]:
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=root_fd)
                os.close(root_fd)
                root_fd = child
            fd = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=root_fd)
            before = os.fstat(fd)
            if not stat.S_ISREG(before.st_mode):
                raise ExportError("Local media is not a regular file.")
            if expected_size is not None and before.st_size != expected_size:
                raise ExportError("Source media size differs from database snapshot.")
            if key in self.copied:
                return self.copied[key]
            if before.st_size > self.remaining:
                raise ExportError("Media copy budget exceeded.")
            if shutil.disk_usage(self.output).free < before.st_size + (16 << 20):
                raise ExportError("Insufficient free disk space for media snapshot.")
            media_dir = self.output / "media"
            media_dir.mkdir(mode=0o700, exist_ok=True)
            digest = hashlib.sha256()
            size = 0
            with tempfile.NamedTemporaryFile(dir=media_dir, delete=False) as target:
                temp = Path(target.name)
                try:
                    with os.fdopen(fd, "rb") as source:
                        fd = None
                        while chunk := source.read(1 << 20):
                            size += len(chunk)
                            if size > before.st_size or size > self.remaining:
                                raise ExportError("Source media changed or exceeded copy budget.")
                            digest.update(chunk)
                            target.write(chunk)
                        after = os.fstat(source.fileno())
                    if (before.st_size, before.st_mtime_ns, before.st_ino) != (after.st_size, after.st_mtime_ns, after.st_ino) or size != before.st_size:
                        raise ExportError("Source media changed during snapshot copy.")
                    target.flush()
                    os.fsync(target.fileno())
                except BaseException:
                    temp.unlink(missing_ok=True)
                    raise
            sha = digest.hexdigest()
            final = media_dir / sha
            os.replace(temp, final)
            self.remaining -= size
            result = {"path": "media/" + sha, "sha256": sha, "size": size}
            self.copied[key] = result
            return result
        finally:
            if fd is not None:
                os.close(fd)
            os.close(root_fd)

    def copied_image_type(self, asset):
        # Derivative files have separate MIME from their originals. Inspect only
        # a fixed header of our verified private copy; do not fetch/decode URLs.
        with (self.output / asset["path"]).open("rb") as source:
            head = source.read(16)
        if head.startswith(b"\x89PNG\r\n\x1a\n"):
            return "image/png"
        if head.startswith(b"\xff\xd8\xff"):
            return "image/jpeg"
        if head[:6] in (b"GIF87a", b"GIF89a"):
            return "image/gif"
        if head.startswith(b"RIFF") and head[8:12] == b"WEBP":
            return "image/webp"
        if head[4:8] == b"ftyp" and head[8:12] in (b"avif", b"avis"):
            return "image/avif"
        return "application/octet-stream"

    def is_source_asset_url(self, url):
        u = urlsplit(url or "")
        return bool(u.scheme + "://" + u.netloc == self.base and u.path.startswith("/files/") and not u.query and not u.fragment)

    def object_key(self, url):
        """Bucket key of an object-storage URL under --object-base-url, or None."""
        if not self.object_base or not url or not url.startswith(self.object_base + "/"):
            return None
        rest = urlsplit(url)
        if rest.query or rest.fragment:
            return None
        return unquote(url[len(self.object_base) + 1:])

    def local_url_key(self, url):
        u = urlsplit(url or "")
        if u.scheme + "://" + u.netloc != self.base or not u.path.startswith("/files/") or u.query or u.fragment:
            raise ExportError("Local asset URL requires an explicit storage snapshot mapping.")
        return unquote(u.path[len("/files/"):])

    def media(self):
        self.stage("files", "drive_file")
        for r in self.tables.get("drive_file", []):
            self.progress.advance()
            id_ = sid(r["id"])
            props = r.get("properties") or {}
            m = {"id": id_, "owner_id": sid(r.get("userId")), "name": r.get("name") or "", "content_type": r.get("type") or "application/octet-stream",
                 "size": r.get("size") or 0, "remote_url": r.get("url") or "", "description": r.get("comment") or "", "blurhash": r.get("blurhash") or "",
                 "width": dim(props.get("width")), "height": dim(props.get("height")), "created_at": self.time(r)}
            local_owner = r.get("userId") and self.is_local(r["userId"])
            if r.get("userId") and not self.known("user", sid(r["userId"])):
                m["owner_id"] = ""
            if r.get("storedInternal"):
                if self.is_source_asset_url(r.get("url")):
                    m["original_url"] = r["url"]
                try:
                    m.update(self.copy_asset(r.get("accessKey"), int(r.get("size") or 0)))
                except (ExportError, OSError, ValueError):
                    self.issue("error", "media_bytes_unavailable", "media", id_, "Internal media bytes are missing, unsafe, changed, over budget, or inconsistent with the database snapshot.")
                for name, key_col, url_col in (("thumbnail", "thumbnailAccessKey", "thumbnailUrl"), ("webpublic", "webpublicAccessKey", "webpublicUrl")):
                    # Misskey reserves keys even when no derivative was generated.
                    # A populated URL, not merely its reserved key, declares a variant.
                    if r.get(url_col):
                        variant = {"id": id_ + ":" + name, "owner_id": m["owner_id"], "name": name, "content_type": "application/octet-stream", "remote_url": r.get(url_col) or ""}
                        if self.is_source_asset_url(r.get(url_col)):
                            variant["original_url"] = r[url_col]
                        try:
                            variant.update(self.copy_asset(r[key_col]))
                            variant["content_type"] = self.copied_image_type(variant)
                        except (ExportError, OSError):
                            self.issue("error", "media_variant_unavailable", "media", variant["id"], "Derived asset bytes could not be safely copied from the snapshot.")
                        self.bundle["media"].append(variant)
            elif not r.get("isLink") and self.object_mode == "reference":
                self.issue("info", "object_storage_reference", "media", id_, "Object-storage media keeps its original URL; the bucket must stay available to the target.")
                m["storage_reference"] = True
            elif not r.get("isLink") and self.object_mode == "copy":
                if r.get("url"):
                    m["original_url"] = r["url"]
                try:
                    m.update(self.copy_asset(r.get("accessKey"), int(r.get("size") or 0), root=self.object_dir))
                except (ExportError, OSError, ValueError):
                    self.issue("error", "media_bytes_unavailable", "media", id_, "Object-storage media bytes are missing from the bucket mirror or inconsistent with the database snapshot.")
            elif local_owner or not r.get("isLink"):
                self.issue("error", "external_storage_required", "media", id_, "This asset requires an object-storage/remote byte snapshot; metadata alone is not a complete site backup.")
            else:
                self.issue("warning", "remote_media_reference", "media", id_, "Remote linked media is preserved as a reference; bytes are not fetched or guaranteed available.")
            self.bundle["media"].append(m)
            for col in ("url", "webpublicUrl", "thumbnailUrl"):
                if r.get(col) and r[col] in self.emoji_urls:
                    self.media_urls[r[col]] = id_

    def note_emoji_ids(self, row, kind="note"):
        result = []
        for raw in row.get("emojis") or []:
            name = str(raw).strip(":")
            domain = row.get("userHost") or ""
            if "@" in name:
                name, domain = name.rsplit("@", 1)
                if domain == ".":
                    domain = ""
            id_ = self.emoji_ids.get((name, domain))
            if id_:
                if id_ not in result:
                    result.append(id_)
            else:
                self.issue("warning", "missing_" + kind + "_emoji", kind, row.get("id"), "Referenced custom emoji was not found in the source emoji table; shortcode and source data are retained.")
        return result

    def posts(self):
        visibility = {"public": "public", "home": "unlisted", "followers": "private", "specified": "direct"}
        pinned = {sid(r["noteId"]) for r in self.tables.get("user_note_pining", [])}
        self.stage("notes", "note")
        for r in self.tables.get("note", []):
            self.progress.advance()
            id_ = sid(r["id"])
            if not self.known("user", sid(r["userId"])):
                self.trimmed("note", id_, "an unexported author")
                continue
            local = self.is_local(r["userId"])
            files = [sid(x) for x in r.get("fileIds") or []]
            file_rows = [self.staging.get("drive_file", f) for f in files]
            reply_id, renote_id = sid(r.get("replyId")), sid(r.get("renoteId"))
            recipients = [sid(x) for x in r.get("visibleUserIds") or []]
            if self.trim_references:
                for ref, what in ((reply_id, "a reply target"), (renote_id, "a renote/quote target")):
                    if ref and not self.known("note", ref):
                        self.trimmed("note", id_, what)
                if not all(file_rows) or not all(self.known("user", x) for x in recipients):
                    self.trimmed("note", id_, "files or recipients")
                reply_id = reply_id if self.known("note", reply_id) else ""
                renote_id = renote_id if self.known("note", renote_id) else ""
                recipients = [x for x in recipients if self.known("user", x)]
                files = [f for f, row in zip(files, file_rows) if row]
            p = {"id": id_, "author_id": sid(r["userId"]), "uri": r.get("uri") or (self.base + "/notes/" + id_ if local else ""),
                 "url": r.get("url") or "", "text": r.get("text") or "", "spoiler_text": r.get("cw") or "",
                 # Official NoteCreateService: reply.threadId ?? reply.id; roots have null threadId.
                 "conversation_id": "note-thread:" + sid(r.get("threadId") or id_),
                 "visibility": visibility.get(r.get("visibility"), "direct"), "local_only": bool(r.get("localOnly")),
                 "recipient_ids": recipients, "reply_id": reply_id, "reblog_id": renote_id,
                 "media_ids": files, "tags": r.get("tags") or [], "emoji_ids": self.note_emoji_ids(r), "pinned": id_ in pinned,
                 "created_at": self.time(r), "sensitive": any(f and f.get("isSensitive") for f in file_rows),
                 "extensions": {"format": "mfm", "source": r}}
            if r.get("visibility") not in visibility:
                p["archive_only"] = True
                self.issue("error", "unknown_visibility", "note", id_, "Unknown source visibility must not be published.")
            if r.get("channelId"):
                p["archive_only"] = True
                self.issue("warning", "channel_note_archive", "note", id_, "Channel semantics need an explicit adapter; note remains archive-only.")
            if r.get("renoteId") and (r.get("text") or r.get("cw") or r.get("fileIds") or r.get("hasPoll")):
                p["archive_only"] = True
                self.issue("warning", "quote_note_archive", "note", id_, "Quote-note semantics must not silently become a pure boost.")
            self.bundle["posts"].append(p)
        rooms = {sid(r["id"]): r for r in self.tables.get("chat_room", [])}
        memberships = defaultdict(set)
        for r in self.tables.get("chat_room_membership", []):
            memberships[sid(r["roomId"])].add(sid(r["userId"]))
        # Official 2026.10 roomTimeline permits owner/current members to read all
        # history. Roles can additionally disable chat; do not approximate their
        # dynamic condition evaluator when a non-default read restriction exists.
        restricted_chat = any((r.get("policies") or {}).get("chatAvailability", "available") == "unavailable" for r in self.tables.get("meta", []))
        for role in self.tables.get("role", []):
            policy = (role.get("policies") or {}).get("chatAvailability")
            if policy and not policy.get("useDefault", True) and policy.get("value") == "unavailable":
                restricted_chat = True
        for table, author, recipient in (("chat_message", "fromUserId", "toUserId"), ("messaging_message", "userId", "recipientId")):
            self.stage(table, table)
            for r in self.tables.get(table, []):
                self.progress.advance()
                sender = sid(r.get(author))
                target = sid(r.get(recipient))
                recipients = [target] if target and self.known("user", target) else []
                if not self.known("user", sender):
                    continue
                archived = not bool(target)
                conversation = table + ":pair:" + ":".join(sorted((sender, target)))
                room_id = sid(r.get("toRoomId"))
                if table == "chat_message" and room_id:
                    room = rooms.get(room_id)
                    if room:
                        recipients = sorted(x for x in memberships[room_id] | {sid(room["ownerId"])} if self.known("user", x))
                        archived = False
                    conversation = table + ":room:" + room_id
                elif r.get("groupId"):
                    conversation = table + ":group:" + sid(r["groupId"])
                # Author access is intrinsic to target direct posts. A departed
                # room sender is still permitted their own messages by source
                # searchMessages(fromUserId), without granting other old senders.
                if table == "chat_message" and restricted_chat:
                    archived = True
                post = {"id": table + ":" + sid(r["id"]), "author_id": sender, "text": r.get("text") or "",
                    "uri": r.get("uri") or "", "visibility": "direct", "recipient_ids": recipients,
                    "media_ids": [sid(r["fileId"])] if r.get("fileId") and self.known("drive_file", sid(r["fileId"])) else [], "conversation_id": conversation,
                    "created_at": self.time(r), "archive_only": archived, "local_only": not bool(r.get("uri")), "extensions": r}
                self.bundle["posts"].append(post)
                if archived:
                    self.issue("warning", "chat_archive_only", table, r["id"], "Chat target or dynamic role read permissions require an explicit adapter; source data is retained privately.")
            if self.tables.get(table):
                self.issue("warning", "chat_policy_review", table, "", "Direct messages preserve participants and conversation identity; read receipts, reactions, moderator access and future membership/role policy need explicit target support. No new federation is authorized.")

    def relationships(self):
        for table, kind, source, target in (("following", "follow", "followerId", "followeeId"), ("follow_request", "follow_request", "followerId", "followeeId"),
                                             ("blocking", "block", "blockerId", "blockeeId"), ("muting", "mute", "muterId", "muteeId")):
            self.stage("relations:" + table, table)
            for r in self.tables.get(table, []):
                self.progress.advance()
                if not (self.known("user", sid(r[source])) and self.known("user", sid(r[target]))):
                    self.trimmed(kind, r.get("id"), "an unexported account")
                    continue
                relation = {"kind": kind, "account_id": sid(r[source]), "target_id": sid(r[target]), "show_reblogs": True,
                            "notify": r.get("notify") not in (None, False, "none"), "activity_uri": r.get("requestId") or "", "created_at": self.time(r)}
                if kind in ("follow", "follow_request") and not relation["activity_uri"] and self.is_local(r[source]):
                    relation["activity_uri"] = self.base + "/follows/" + sid(r[source]) + "/" + sid(r[target])
                if kind == "mute" and r.get("expiresAt"):
                    relation["expires_at"] = r["expiresAt"]
                self.bundle["relations"].append(relation)

        members = defaultdict(list)
        for r in self.tables.get("user_list_membership", []):
            if self.known("user", sid(r["userId"])):
                members[sid(r["userListId"])].append(sid(r["userId"]))
        for r in self.tables.get("user_list", []):
            self.bundle["lists"].append({"id": sid(r["id"]), "owner_id": sid(r["userId"]), "title": r.get("name") or "", "member_ids": members[sid(r["id"])]})
        for r in self.tables.get("note_favorite", []):
            if self.post_ok(r["noteId"]) and self.known("user", sid(r["userId"])):
                self.bundle["bookmarks"].append({"account_id": sid(r["userId"]), "post_id": sid(r["noteId"])})
        self.stage("reactions", "note_reaction")
        for r in self.tables.get("note_reaction", []):
            self.progress.advance()
            if self.post_ok(r["noteId"]) and self.known("user", sid(r["userId"])):
                self.bundle["reactions"].append({"id": sid(r["id"]), "account_id": sid(r["userId"]), "post_id": sid(r["noteId"]), "emoji": r["reaction"], "created_at": self.time(r)})
        polls = set()
        for r in self.tables.get("poll", []):
            if not self.post_ok(r["noteId"]):
                continue
            votes = r.get("votes") or []
            choices = r.get("choices") or []
            if len(votes) != len(choices) or any(not isinstance(v, int) or v < 0 for v in votes):
                self.issue("error", "invalid_poll_counts", "poll", r["noteId"], "Poll option/count arrays are not aligned nonnegative counts.")
                continue
            p = {"id": sid(r["noteId"]), "post_id": sid(r["noteId"]), "multiple": bool(r.get("multiple")), "options": [{"title": title, "votes": votes[i]} for i, title in enumerate(choices)]}
            if r.get("expiresAt"):
                p["expires_at"] = r["expiresAt"]
            self.bundle["polls"].append(p)
            polls.add(p["id"])
        for r in self.tables.get("poll_vote", []):
            if sid(r["noteId"]) in polls and self.known("user", sid(r["userId"])):
                self.bundle["poll_votes"].append({"poll_id": sid(r["noteId"]), "account_id": sid(r["userId"]), "option": r["choice"], "created_at": self.time(r)})

    def emojis(self):
        for r in self.tables.get("emoji", []):
            id_ = sid(r["id"])
            url = r.get("originalUrl") or r.get("publicUrl") or ""
            e = {"id": id_, "shortcode": r.get("name") or "", "domain": r.get("host") or "", "url": r.get("publicUrl") or url,
                 "media_id": self.media_urls.get(url, ""), "category": r.get("category") or "", "aliases": r.get("aliases") or [],
                 "sensitive": bool(r.get("isSensitive")), "license": r.get("license") or ""}
            object_key = self.object_key(url)
            if r.get("host") is None and not e["media_id"] and object_key and self.object_mode == "reference":
                self.issue("info", "object_storage_reference", "emoji", id_, "Object-storage emoji keeps its original URL.")
            elif r.get("host") is None and not e["media_id"] and object_key and self.object_mode == "copy":
                asset_id = "emoji:" + id_
                m = {"id": asset_id, "owner_id": "", "name": r.get("name") or "", "content_type": r.get("type") or "application/octet-stream", "remote_url": url}
                try:
                    m.update(self.copy_asset(object_key, root=self.object_dir))
                    m["original_url"] = url
                    e["media_id"] = asset_id
                    self.bundle["media"].append(m)
                except (ExportError, OSError):
                    self.issue("error", "emoji_bytes_unavailable", "emoji", id_, "Object-storage emoji bytes are missing from the bucket mirror.")
            elif r.get("host") is None and not e["media_id"]:
                asset_id = "emoji:" + id_
                m = {"id": asset_id, "owner_id": "", "name": r.get("name") or "", "content_type": r.get("type") or "application/octet-stream", "remote_url": url}
                try:
                    m.update(self.copy_asset(self.local_url_key(url)))
                    m["original_url"] = url
                    e["media_id"] = asset_id
                    self.bundle["media"].append(m)
                except (ExportError, OSError):
                    self.issue("error", "emoji_bytes_unavailable", "emoji", id_, "Local emoji bytes need a safe local/object-storage snapshot mapping.")
            self.bundle["emojis"].append(e)

    def media_ref(self, file_id, owner):
        file_id = sid(file_id)
        if file_id and self.trim_references and not self.known("drive_file", file_id):
            self.trimmed("account", owner, "a profile image")
            return ""
        return file_id

    def post_ok(self, note_id):
        """Whether a note was converted (exists and its author was exported)."""
        if not self.trim_references:
            return True
        note = self.staging.get("note", sid(note_id))
        return bool(note) and self.known("user", sid(note.get("userId")))

    def convert(self):
        self.preserve()
        self.accounts()
        self.media()
        self.posts()
        self.relationships()
        self.emojis()
        self.progress.finish()
        return self.bundle




# --------------------------------------------------------------------------- Misskey plan
LABELS = {"user": "accounts", "user_profile": "profiles", "user_keypair": "keys", "user_publickey": "keys", "note": "notes",
          "drive_file": "files", "following": "follows", "follow_request": "follow-requests", "blocking": "blocks", "muting": "mutes",
          "note_reaction": "reactions", "note_favorite": "bookmarks", "poll": "polls", "poll_vote": "poll-votes", "emoji": "emojis",
          "chat_message": "chat", "messaging_message": "chat", "user_list": "lists", "user_list_membership": "list-members"}
# Derived statistics are rebuilt by the target; they are skipped in local-only mode.
SKIP_LOCAL = re.compile(r"^(__chart|_chart|migrations$|typeorm_metadata$)")


def misskey_plan(f, include_remote_cache=False):
    """Fetch steps in dependency order. Returns the scan filters (for final sync)."""
    scans = []

    def scan(table, where="TRUE"):
        if table in f.cat:
            scans.append((table, where))
            f.scan(table, where)

    if include_remote_cache:
        for table in sorted(f.cat):
            scan(table)
        return scans
    LU = '(SELECT u.id FROM public."user" u WHERE u.host IS NULL)'
    LN = '(SELECT n.id FROM public.note n WHERE n."userHost" IS NULL)'
    handled = set()

    def explicit(table, where, *cols):
        handled.add(table)
        if f.exists(table, *cols):
            scan(table, where)

    explicit("user", "t.host IS NULL", "host")
    explicit("note", 't."userHost" IS NULL', "userHost")
    for table, a, b in (("following", "followerId", "followeeId"), ("follow_request", "followerId", "followeeId"),
                        ("blocking", "blockerId", "blockeeId"), ("muting", "muterId", "muteeId")):
        explicit(table, 't."%s" IN %s OR t."%s" IN %s' % (a, LU, b, LU), a, b)
    rooms = '(SELECT r.id FROM public.chat_room r WHERE r."ownerId" IN %s UNION SELECT m."roomId" FROM public.chat_room_membership m WHERE m."userId" IN %s)' % (LU, LU)
    explicit("chat_room", "t.id IN " + rooms, "ownerId")
    explicit("chat_room_membership", 't."roomId" IN ' + rooms, "roomId")
    explicit("chat_message", 't."fromUserId" IN %s OR t."toUserId" IN %s OR t."toRoomId" IN %s' % (LU, LU, rooms), "fromUserId", "toUserId", "toRoomId")
    explicit("messaging_message", 't."userId" IN %s OR t."recipientId" IN %s' % (LU, LU), "userId", "recipientId")
    explicit("user_list_membership", 't."userListId" IN (SELECT l.id FROM public.user_list l WHERE l."userId" IN %s)' % LU, "userListId")
    explicit("note_reaction", 't."userId" IN %s OR t."noteId" IN %s' % (LU, LN), "userId", "noteId")
    explicit("poll_vote", 't."userId" IN %s OR t."noteId" IN %s' % (LU, LN), "userId", "noteId")
    explicit("emoji", "t.host IS NULL", "host")
    for name in ("user_profile", "user_publickey", "drive_file", "poll", "instance"):
        handled.add(name)
    # Everything else: rows owned by local users, rows about local notes, or whole small tables.
    for table in sorted(f.cat):
        if table in handled or SKIP_LOCAL.match(table):
            continue
        cols = f.cols(table)
        if "userId" in cols:
            scan(table, 't."userId" IN ' + LU)
        elif "noteId" in cols:
            scan(table, 't."noteId" IN ' + LN)
        else:
            scan(table)
    local_notes = "AND json_extract(data, '$.userHost') IS NULL"
    # Remote replies / quotes of local notes (pure renotes are skipped).
    f.refs("note", "replyId", [("note", "id", False, local_notes)], name="ref:note.replies")
    f.refs("note", "renoteId", [("note", "id", False, local_notes)], name="ref:note.quotes",
           where="t.text IS NOT NULL OR t.cw IS NOT NULL OR cardinality(t.\"fileIds\") > 0")
    # Notes the local instance points at (one hop).
    f.refs("note", "id", [("note", "replyId", False, local_notes), ("note", "renoteId", False, local_notes),
                          ("note_reaction", "noteId", False, ""), ("note_favorite", "noteId", False, ""),
                          ("user_note_pining", "noteId", False, ""), ("poll_vote", "noteId", False, ""),
                          ("clip_note", "noteId", False, "")])
    f.refs("poll", "noteId", [("note", "id", False, "")])
    f.refs("user", "id", [("note", "userId", False, ""), ("note", "visibleUserIds", True, ""),
                          ("following", "followerId", False, ""), ("following", "followeeId", False, ""),
                          ("follow_request", "followerId", False, ""), ("follow_request", "followeeId", False, ""),
                          ("blocking", "blockeeId", False, ""), ("muting", "muteeId", False, ""),
                          ("note_reaction", "userId", False, ""), ("poll_vote", "userId", False, ""),
                          ("user_list_membership", "userId", False, ""), ("chat_message", "fromUserId", False, ""),
                          ("chat_message", "toUserId", False, ""), ("chat_room", "ownerId", False, ""),
                          ("chat_room_membership", "userId", False, ""), ("messaging_message", "recipientId", False, "")])
    f.refs("user_profile", "userId", [("user", "id", False, "")])
    f.refs("user_publickey", "userId", [("user", "id", False, "")])
    f.refs("drive_file", "userId", [("user", "id", False, "AND json_extract(data, '$.host') IS NULL")], name="ref:drive_file.local")
    f.refs("drive_file", "id", [("note", "fileIds", True, ""), ("user", "avatarId", False, ""), ("user", "bannerId", False, ""),
                                ("chat_message", "fileId", False, ""), ("messaging_message", "fileId", False, "")])
    f.refs("emoji", "host", [("user", "host", False, "")])
    f.refs("instance", "host", [("user", "host", False, "")])
    return scans


# --------------------------------------------------------------------------- CLI
def add_common_args(ap):
    ap.add_argument("--state-dir", type=Path, help="resumable fetch state (default: <output>.state); reuse it to resume or for --final-sync")
    ap.add_argument("--include-remote-cache", action="store_true", help="export every row of every table, including the remote-content cache")
    ap.add_argument("--fetch-only", action="store_true", help="bulk pass: fetch into the state directory and stop (source may stay live)")
    ap.add_argument("--final-sync", action="store_true", help="after writes are stopped: fetch new/updated rows, drop deleted rows, then build")
    ap.add_argument("--batch-size", type=int, default=5000, help="rows per keyset page / read-only transaction")
    ap.add_argument("--max-rows-per-sec", type=float, default=0, help="read rate limit for a live source (0 = unlimited)")
    ap.add_argument("--batch-sleep", type=float, default=0, help="seconds to sleep after every page")
    ap.add_argument("--dsn", default="", help="libpq connection string without password, e.g. 'host=127.0.0.1 port=15432 dbname=misskey user=export sslmode=require'")
    ap.add_argument("--progress-interval", type=float, default=5, help="seconds between progress lines on stderr")
    ap.add_argument("--max-records", type=int, default=0, help="fail if more rows than this are staged (0 = no limit)")
    ap.add_argument("--max-snapshot-bytes", type=int, default=0, help=argparse.SUPPRESS)  # accepted for compatibility; memory no longer scales with it
    ap.add_argument("--max-media-bytes", type=int, default=8 << 30)
    ap.add_argument("--verbose", action="store_true", help="print the full traceback on failure")
    ap.add_argument("--emit", choices=("bundle", "ndjson"), default="bundle",
                    help="bundle: write <output>/bundle.json; ndjson: stream records to stdout for community-transfer direct mode")


class Run:
    """Shared fetch driver and error reporting for the PostgreSQL exporters."""

    def __init__(self, args, platform):
        self.args, self.platform, self.stage = args, platform, "setup"
        self.progress = Progress(args.progress_interval)

    def open_state(self):
        a = self.args
        if a.batch_size <= 0 or a.max_rows_per_sec < 0 or a.batch_sleep < 0 or a.max_media_bytes <= 0 or a.max_records < 0:
            raise ExportError("batch size, rate limits and media budget must be positive")
        if a.fetch_only and a.final_sync:
            raise ExportError("--fetch-only and --final-sync are mutually exclusive")
        os.umask(0o077)
        state = a.state_dir or a.output.with_name(a.output.name + ".state")
        self.stage = "opening state directory %s" % state
        state.mkdir(mode=0o700, parents=False, exist_ok=True)
        staging = Staging(state / "staging.sqlite3")
        mode = "full" if a.include_remote_cache else "local"
        stored = staging.get_meta("mode")
        if stored and stored != mode:
            raise ExportError("state directory was fetched in %s mode; use a new --state-dir for %s mode" % (stored, mode))
        if staging.get_meta("platform", platform_name := self.platform) != platform_name:
            raise ExportError("state directory belongs to another source platform")
        staging.set_meta("mode", mode)
        staging.set_meta("platform", self.platform)
        if not staging.get_meta("bulk_started"):
            staging.set_meta("bulk_started", datetime.now(timezone.utc).isoformat())
        elif not a.final_sync:
            print("resuming from checkpoint in %s" % state, file=sys.stderr, flush=True)
        self.state, self.staging = state, staging
        return staging

    def fetch(self, plan, labels):
        a, staging = self.args, self.staging
        self.stage = "connecting to source (catalog)"
        source = PsqlSource(self.state, a.dsn, a.batch_size, a.max_rows_per_sec, a.batch_sleep)
        fetcher = Fetcher(source, staging, self.progress, final_sync=a.final_sync, labels=labels)
        self.stage = "fetch"
        scans = plan(fetcher, a.include_remote_cache)
        if a.final_sync:
            since = (datetime.fromisoformat(staging.get_meta("bulk_started")) - timedelta(minutes=5)).isoformat()
            self.stage = "final sync: updated rows"
            for table, where in scans:
                fetcher.updated_since(table, where, since)
            self.stage = "final sync: deletions"
            removed = sum(fetcher.prune_deleted(t) for t in staging.tables())
            print("final sync: %d staged rows deleted at the source were dropped" % removed, file=sys.stderr, flush=True)
            self.stage = "final sync: references"
            plan(fetcher, a.include_remote_cache)
            staging.set_meta("final_sync", datetime.now(timezone.utc).isoformat())
        staging.set_meta("fetched", datetime.now(timezone.utc).isoformat())
        self.progress.finish()
        total = staging.db.execute("SELECT count(*) FROM rows").fetchone()[0]
        if a.max_records and total > a.max_records:
            raise ExportError("%d staged rows exceed --max-records %d" % (total, a.max_records))
        return total

    def snapshot_id(self):
        s = self.staging
        return "keyset:%s/%s%s" % (s.get_meta("bulk_started"), s.get_meta("fetched"), "/final" if s.get_meta("final_sync") else "")

    def fail(self, exc):
        if isinstance(exc, KeyboardInterrupt):
            print("Export interrupted during %s; rerun the same command to resume from the checkpoint." % self.stage, file=sys.stderr)
            return 130
        print("Export failed during %s: %s: %s" % (self.stage, type(exc).__name__, redact(exc)), file=sys.stderr)
        if self.args.verbose:
            print(redact("".join(traceback.format_exception(exc))), file=sys.stderr)
        else:
            print("(rerun with --verbose for the traceback; fetched data is kept in the state directory)", file=sys.stderr)
        return 1


def summary(sink):
    errors = sink.severity["error"]
    # In NDJSON mode stdout carries the records, so the summary goes to stderr.
    out = sys.stderr if isinstance(sink, StreamSink) else sys.stdout
    print(json.dumps({"status": "blocked" if errors else "exported_with_review_required", "source_records": sink.counts["records"],
                      "errors": errors, "warnings": sink.severity["warning"]}), file=out, flush=True)
    return 2 if errors else 0


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base-url", required=True)
    ap.add_argument("--id-method", required=True, choices=METHODS, help="exact config.id from source Misskey")
    ap.add_argument("--source-version", required=True)
    ap.add_argument("--source-dir", type=Path, help="trusted consistent Misskey files snapshot; never an untrusted upload")
    ap.add_argument("--output", required=True, type=Path, help="new private directory for bundle.json; must not already exist")
    ap.add_argument("--object-storage-mode", choices=("error", "copy", "reference"), default="error",
                    help="media stored in object storage: refuse (default), copy from --object-storage-dir, or keep original URLs")
    ap.add_argument("--object-storage-dir", type=Path, help="local mirror of the source bucket, keyed by object key (copy mode)")
    ap.add_argument("--object-base-url", default="", help="public URL prefix of the source bucket (Misskey objectStorageBaseUrl)")
    add_common_args(ap)
    args = ap.parse_args(argv)
    run = Run(args, "misskey")
    try:
        origin(args.base_url)
        if not args.fetch_only and args.output.exists():
            raise ExportError("output %s already exists; choose a new directory (the state directory is reused)" % args.output)
        run.open_state()
        rows = run.fetch(misskey_plan, LABELS)
        if args.fetch_only:
            print(json.dumps({"status": "fetched", "staged_rows": rows, "state_dir": str(run.state)}), flush=True)
            return 0
        run.stage = "build: creating output"
        args.output.mkdir(mode=0o700, parents=False, exist_ok=False)
        run.stage = "build"
        conv = Converter(run.staging, args.base_url, args.id_method, args.source_dir, args.output, args.source_version, run.snapshot_id(),
                         args.max_media_bytes, args.object_storage_mode, args.object_storage_dir, args.object_base_url,
                         sink=None, progress=run.progress)
        sink = make_sink(args, args.output, {k: v for k, v in conv.bundle.items() if k not in SECTIONS})
        conv.sink, conv.bundle, conv.trim_references = sink, sink.bundle, True
        conv.convert()
        run.stage = "build: writing bundle.json"
        sink.finish()
        return summary(sink)
    except (Exception, KeyboardInterrupt) as exc:  # noqa: BLE001 - every failure is reported with its stage
        return run.fail(exc)


if __name__ == "__main__":
    sys.exit(main())
