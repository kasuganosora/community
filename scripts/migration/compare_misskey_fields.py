#!/usr/bin/env python3
"""Compare note-level and other non-account Misskey data with a Community import.

Read-only: connects to the Misskey source database and the Community target
database with read-only sessions (SELECT only) and never writes to either.
Prints counts per field and up to N sample source ids per mismatch; never
prints text, URLs, credentials or other content (only ids, booleans, counts).

Checks (local notes are always in scope; remote notes only where imported):
  N1  every in-scope note is a status or a private archive record
  N2  visibility (public/home/followers/specified -> 0/1/2/3; narrowed replies counted apart)
  N3  local-only
  N4  CW (spoiler text, compared by hash)
  N5  MFM source text of local notes (by hash; remote notes are stored rendered)
  N6  sensitive (any attached file isSensitive)
  N7  pure renotes -> reblog of the same note
  N8  quotes -> quote_id of the same note, or import_quote_uri when unlinked
  N9  reply parent
  N10 note language (only when the source has a note lang column)
  R1  reactions per note (unicode, local custom, remote custom emoji)
  P1  polls: choices, vote counts, multiple, expiry; P2 poll vote rows
  PIN pinned notes
  CH  channel notes are archived, never published
  E1  local custom emoji: category, aliases, license, sensitive, local-only
  M1  instance meta assets (WARN only: meta import is a separate decision)

Exit codes: 0 all checks pass, 1 a check failed, 2 usage/connection error.

  MISSKEY_DB_DSN='postgresql://ro@10.0.0.5/misskey?sslmode=require' \\
  COMMUNITY_DB_DSN='postgresql://ro@127.0.0.1/community_rehearsal_20261019f' \\
  python3 scripts/migration/compare_misskey_fields.py --origin https://maid.lat --samples 10 --seed 1
"""
import argparse
import json
import os
import random
import re
import sys
from collections import Counter, defaultdict

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from verify_misskey_import import DB, Report, SAFE_ID, lit  # noqa: E402

VIS = {"public": 0, "home": 1, "followers": 2, "specified": 3}
TRUE = ("t", "true", "True", "1")
BATCH = 2000


def b(v):
    return v in TRUE


def safe(i):
    return i if i and SAFE_ID.match(i) else "?"


class Compare:
    def __init__(self, mk, cm, origin, rep, samples, rnd):
        self.mk, self.cm, self.origin, self.rep, self.samples, self.rnd = mk, cm, origin.rstrip("/"), rep, samples, rnd
        self.pg = cm.kind == "pg"

    # ---------------------------------------------------------- dialect
    def prop(self, col, key):
        if self.pg:
            return "(%s::jsonb ->> %s)" % (col, lit(key))
        return "JSON_UNQUOTE(JSON_EXTRACT(%s, %s))" % (col, lit("$." + key))

    def note_key(self, alias):
        return "CASE WHEN %s.\"userHost\" IS NULL THEN %s || %s.id ELSE %s.uri END" % (alias, lit(self.origin + "/notes/"), alias, alias)

    def status_key(self, alias):
        return "COALESCE(NULLIF(%s.local_object_uri, ''), %s.origin_uri)" % (alias, alias)

    def add(self, name, ok, detail, diffs=()):
        diffs = list(diffs)
        shown = diffs if len(diffs) <= self.samples else self.rnd.sample(diffs, self.samples)
        if len(diffs) > len(shown):
            detail += "; showing %d random of %d mismatches" % (len(shown), len(diffs))
        self.rep.add(name, ok, detail, sorted(shown))

    # ---------------------------------------------------------- loading
    def load_statuses(self):
        s = self.status_key("s")
        rows = self.cm.q(
            "SELECT %s, s.visibility, s.local_only, s.sensitive, s.language, %s, %s, %s, md5(COALESCE(c.content, '')), md5(COALESCE(c.spoiler_text, '')), "
            "%s, %s, s.id FROM statuses s LEFT JOIN status_contents c ON c.status_id = s.id "
            "LEFT JOIN statuses r ON r.id = s.reblog_id LEFT JOIN statuses q ON q.id = s.quote_id LEFT JOIN statuses p ON p.id = s.in_reply_to_id "
            "WHERE s.deleted_at IS NULL" % (s, self.status_key("r"), self.status_key("q"), self.status_key("p"),
                                            self.prop("s.properties", "import_visibility_narrowed_from"), self.prop("s.properties", "import_quote_uri")))
        self.st = {}
        for r in rows:
            if r[0]:
                self.st[r[0]] = {"vis": int(r[1]), "local_only": b(r[2]), "sensitive": b(r[3]), "lang": r[4] or "", "reblog": r[5], "quote": r[6],
                                 "reply": r[7], "text": r[8], "cw": r[9], "narrowed": bool(r[10]), "quote_uri": bool(r[11]), "id": r[12]}
        self.archived = {r[0] for r in self.cm.q("SELECT source_id FROM data_transfer_preserved_records WHERE kind = 'post'")}

    def note_select(self):
        quote = '(n.text IS NOT NULL OR n.cw IS NOT NULL OR cardinality(n."fileIds") > 0 OR COALESCE(n."hasPoll", false))'
        return ('SELECT n.id, n."userHost" IS NULL, %s, n.visibility, n."localOnly", md5(COALESCE(n.text, \'\')), md5(COALESCE(n.cw, \'\')), n.cw IS NOT NULL, '
                'n."renoteId" IS NOT NULL, %s, %s, %s, n."replyId" IS NOT NULL, n."channelId" IS NOT NULL, '
                'EXISTS (SELECT 1 FROM drive_file f WHERE f.id = ANY(n."fileIds") AND f."isSensitive"), to_jsonb(n)->>\'lang\' '
                'FROM note n LEFT JOIN note t ON t.id = n."renoteId" LEFT JOIN note pr ON pr.id = n."replyId" ' % (
                    self.note_key("n"), quote, self.note_key("t"), self.note_key("pr")))

    def load_notes(self):
        rows = self.mk.q(self.note_select() + 'WHERE n."userHost" IS NULL')
        remote_keys = [k for k in self.st if not k.startswith(self.origin + "/")]
        for i in range(0, len(remote_keys), BATCH):
            arr = "ARRAY[%s]::varchar[]" % ",".join(lit(k) for k in remote_keys[i:i + BATCH])
            rows += self.mk.q(self.note_select() + 'WHERE n."userHost" IS NOT NULL AND n.uri = ANY(%s)' % arr)
        self.notes = {}
        for r in rows:
            self.notes[r[0]] = {"local": b(r[1]), "key": r[2], "vis": r[3], "local_only": b(r[4]), "text": r[5], "cw": r[6], "has_cw": b(r[7]),
                                "renote": b(r[8]), "is_quote": b(r[8]) and b(r[9]), "target": r[10], "parent": r[11], "reply": b(r[12]),
                                "channel": b(r[13]), "sensitive": b(r[14]), "lang": r[15] or ""}

    # ---------------------------------------------------------- notes
    def notes_checks(self):
        self.load_statuses()
        self.load_notes()
        present, archived, missing = 0, 0, []
        for nid, n in self.notes.items():
            if n["key"] in self.st:
                present += 1
            elif nid in self.archived:
                archived += 1
            elif n["local"]:
                missing.append("%s local note: no status and no archive record" % safe(nid))
        local = sum(1 for n in self.notes.values() if n["local"])
        self.add("N1 notes present", not missing, "misskey in scope=%d (local=%d, imported remote=%d); statuses=%d archived=%d missing=%d"
                 % (len(self.notes), local, len(self.notes) - local, present, archived, len(missing)), missing)

        pairs = [(nid, n, self.st[n["key"]]) for nid, n in self.notes.items() if n["key"] in self.st]

        def field(name, want, got, applies=lambda n, s: True, ok_if=lambda n, s: False, extra=""):
            total, match, diffs = 0, 0, []
            for nid, n, s in pairs:
                if not applies(n, s):
                    continue
                total += 1
                if want(n) == got(s) or ok_if(n, s):
                    match += 1
                else:
                    diffs.append("%s %s note: %s differs" % (safe(nid), "local" if n["local"] else "remote", name))
            self.add(name, not diffs, "compared=%d matching=%d%s" % (total, match, extra), diffs)

        narrowed = sum(1 for _, n, s in pairs if s["narrowed"])
        field("N2 visibility", lambda n: VIS.get(n["vis"], -1), lambda s: s["vis"], ok_if=lambda n, s: s["narrowed"] and s["vis"] > VIS.get(n["vis"], 9),
              extra="; replies narrowed on import=%d; misskey public=%d home=%d followers=%d specified=%d" % (
                  narrowed, *(sum(1 for _, n, _ in pairs if n["vis"] == v) for v in ("public", "home", "followers", "specified"))))
        field("N3 local-only", lambda n: n["local_only"], lambda s: s["local_only"],
              extra="; misskey localOnly=%d community local_only=%d" % (sum(n["local_only"] for _, n, _ in pairs), sum(s["local_only"] for _, _, s in pairs)))
        field("N4 CW", lambda n: n["cw"], lambda s: s["cw"],
              extra="; misskey cw=%d community spoiler=%d" % (sum(n["has_cw"] for _, n, _ in pairs), sum(s["cw"] != "d41d8cd98f00b204e9800998ecf8427e" for _, _, s in pairs)))
        field("N5 MFM source text (local)", lambda n: n["text"], lambda s: s["text"], applies=lambda n, s: n["local"] and not (n["renote"] and not n["is_quote"]))
        field("N6 sensitive", lambda n: n["sensitive"], lambda s: s["sensitive"],
              extra="; misskey with sensitive file=%d community sensitive=%d" % (sum(n["sensitive"] for _, n, _ in pairs), sum(s["sensitive"] for _, _, s in pairs)))
        field("N7 pure renotes", lambda n: n["target"], lambda s: s["reblog"], applies=lambda n, s: n["renote"] and not n["is_quote"])
        unlinked = sum(1 for _, n, s in pairs if n["is_quote"] and not s["quote"] and s["quote_uri"])
        field("N8 quotes", lambda n: n["target"], lambda s: s["quote"], applies=lambda n, s: n["is_quote"],
              ok_if=lambda n, s: not s["quote"] and s["quote_uri"], extra="; unlinked with import_quote_uri=%d (see quote_unlinked in the import report)" % unlinked)
        field("N9 reply parent", lambda n: n["parent"], lambda s: s["reply"], applies=lambda n, s: n["reply"] and n["parent"] is not None,
              ok_if=lambda n, s: s["reply"] is None and n["parent"] not in self.st)
        with_lang = [p for p in pairs if p[1]["lang"]]
        if with_lang:
            field("N10 note language", lambda n: n["lang"].lower(), lambda s: s["lang"].lower(), applies=lambda n, s: bool(n["lang"]))
        else:
            self.rep.add("N10 note language", True, "source notes carry no language (upstream Misskey has no note lang column)")
        chan = [(nid, n) for nid, n in self.notes.items() if n["channel"]]
        leaked = ["%s channel note published as a status" % safe(nid) for nid, n in chan if n["key"] in self.st]
        self.add("CH channel notes archived", not leaked, "misskey channel notes in scope=%d archived=%d published=%d"
                 % (len(chan), sum(1 for nid, _ in chan if nid in self.archived), len(leaked)), leaked)

    # ---------------------------------------------------------- reactions
    def reaction_checks(self):
        # Exporter scope: reactions by local users or on local notes.
        src = defaultdict(Counter)
        for nid, reaction, count in self.mk.q(
                'SELECT r."noteId", r.reaction, count(*) FROM note_reaction r JOIN "user" u ON u.id = r."userId" JOIN note n ON n.id = r."noteId" '
                'WHERE u.host IS NULL OR n."userHost" IS NULL GROUP BY r."noteId", r.reaction'):
            n = self.notes.get(nid)
            if n and n["key"] in self.st:
                src[nid][re.sub(r"@\.:$", ":", reaction)] += int(count)
        by_status = {s["id"]: k for k, s in self.st.items()}
        tgt = defaultdict(Counter)
        for sid_, name, count in self.cm.q("SELECT status_id, name, count(*) FROM status_reactions GROUP BY status_id, name"):
            if sid_ in by_status:
                tgt[by_status[sid_]][name] += int(count)
        emojis = {(r[0], r[1] or "") for r in self.cm.q("SELECT shortcode, domain FROM emojis WHERE deleted_at IS NULL")}

        def kind(name):
            if not name.startswith(":"):
                return "unicode"
            return "remote custom" if "@" in name else "local custom"

        totals = {k: [0, 0] for k in ("unicode", "local custom", "remote custom")}
        diffs, archived = [], 0
        for nid, want in src.items():
            got = tgt.get(self.notes[nid]["key"], Counter())
            for name, c in want.items():
                totals[kind(name)][0] += c
                totals[kind(name)][1] += min(c, got.get(name, 0))
            for name in set(want) | set(got):
                if want.get(name, 0) == got.get(name, 0):
                    continue
                parts = name.strip(":").split("@", 1)
                if kind(name) != "unicode" and (parts[0], parts[1] if len(parts) == 2 else "") not in emojis and got.get(name, 0) == 0:
                    archived += want[name]  # emoji not on the target (E1 checks local emoji): archived by design
                    continue
                diffs.append("%s: %s reaction count misskey=%d community=%d" % (safe(nid), kind(name), want.get(name, 0), got.get(name, 0)))
        detail = "; ".join("%s misskey=%d community=%d" % (k, v[0], v[1]) for k, v in totals.items())
        self.add("R1 reactions per note", not diffs, "notes with reactions misskey=%d community=%d; %s; custom-emoji reactions archived (emoji not on the target)=%d"
                 % (len(src), len(tgt), detail, archived), diffs)

    # ---------------------------------------------------------- polls
    def poll_checks(self):
        src = {}
        for nid, choices, votes, multiple, expires in self.mk.q(
                'SELECT p."noteId", array_to_json(p.choices), array_to_json(p.votes), p.multiple, '
                "to_char(p.\"expiresAt\" AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') FROM poll p"):
            n = self.notes.get(nid)
            if n and n["key"] in self.st:
                src[nid] = (json.loads(choices or "[]"), json.loads(votes or "[]"), b(multiple), expires)
        exp = "to_char(p.expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')" if self.pg else "DATE_FORMAT(p.expires_at, '%Y-%m-%d %H:%i:%s')"
        by_status = {s["id"]: k for k, s in self.st.items()}
        tgt, poll_of = {}, {}
        for pid, status_id, multiple, expires in self.cm.q("SELECT p.id, p.status_id, p.multiple, %s FROM polls p WHERE p.deleted_at IS NULL" % exp):
            if status_id in by_status:
                tgt[by_status[status_id]] = [[], [], b(multiple), expires]
                poll_of[pid] = by_status[status_id]
        for pid, _, content, votes in self.cm.q('SELECT poll_id, %s, content, votes FROM poll_options ORDER BY poll_id, %s' % (("\"index\"",) * 2 if self.pg else ("`index`",) * 2)):
            if pid in poll_of:
                tgt[poll_of[pid]][0].append(content)
                tgt[poll_of[pid]][1].append(int(votes))
        diffs = []
        for nid, (choices, votes, multiple, expires) in src.items():
            t = tgt.get(self.notes[nid]["key"])
            if t is None:
                diffs.append("%s: poll missing" % safe(nid)); continue
            for what, a, c in (("choices", choices, t[0]), ("votes", votes, t[1]), ("multiple", multiple, t[2]), ("expiry", expires, t[3])):
                if a != c:
                    diffs.append("%s: poll %s differs" % (safe(nid), what))
        matched = [tgt[self.notes[n]["key"]] for n in src if self.notes[n]["key"] in tgt]
        self.add("P1 polls", not diffs, "misskey polls in scope=%d community=%d; vote counts misskey=%d community=%d; multiple misskey=%d community=%d"
                 % (len(src), len(matched), sum(sum(v[1]) for v in src.values()), sum(sum(t[1]) for t in matched),
                    sum(v[2] for v in src.values()), sum(t[2] for t in matched)), diffs)
        mk_votes = Counter()
        for nid, count in self.mk.q('SELECT v."noteId", count(*) FROM poll_vote v JOIN "user" u ON u.id = v."userId" JOIN note n ON n.id = v."noteId" '
                                    'WHERE u.host IS NULL OR n."userHost" IS NULL GROUP BY v."noteId"'):
            if nid in src:
                mk_votes[self.notes[nid]["key"]] += int(count)
        cm_votes = Counter()
        for pid, count in self.cm.q("SELECT poll_id, count(*) FROM poll_option_votes GROUP BY poll_id"):
            if pid in poll_of:
                cm_votes[poll_of[pid]] += int(count)
        vd = ["%s: vote rows misskey=%d community=%d" % (k.rsplit("/", 1)[-1] if k.startswith(self.origin) else "remote", mk_votes[k], cm_votes[k])
              for k in set(mk_votes) | set(cm_votes) if mk_votes[k] != cm_votes[k]]
        self.add("P2 poll vote rows", not vd, "misskey in-scope vote rows=%d community=%d" % (sum(mk_votes.values()), sum(cm_votes.values())), vd)

    # ---------------------------------------------------------- pins
    def pin_checks(self):
        src = {self.notes[r[0]]["key"]: r[0] for r in self.mk.q('SELECT "noteId" FROM user_note_pining') if r[0] in self.notes}
        by_status = {s["id"]: k for k, s in self.st.items()}
        tgt = {by_status[r[0]] for r in self.cm.q("SELECT status_id FROM pin_statuses") if r[0] in by_status}
        diffs = ["%s: pinned in misskey, not in community" % safe(src[k]) for k in src if k in self.st and k not in tgt]
        self.add("PIN pinned notes", not diffs, "misskey pinned (published)=%d community pinned=%d" % (sum(1 for k in src if k in self.st), len(tgt)), diffs)

    # ---------------------------------------------------------- emoji
    def emoji_checks(self):
        src = {r[1]: r for r in self.mk.q("SELECT id, name, to_jsonb(e)->>'category', to_jsonb(e)->'aliases', to_jsonb(e)->>'license', to_jsonb(e)->>'isSensitive', "
                                                    "to_jsonb(e)->>'localOnly' FROM emoji e WHERE host IS NULL")}
        tgt = {r[0]: r for r in self.cm.q("SELECT shortcode, category, aliases, license, is_sensitive, local_only FROM emojis WHERE (domain = '' OR domain IS NULL) AND deleted_at IS NULL")}
        diffs = []
        counts = Counter()
        for name, r in src.items():
            t = tgt.get(name)
            if t is None:
                diffs.append("%s: local emoji missing" % safe(r[0])); continue
            want = {"category": r[2] or "", "aliases": sorted(x for x in json.loads(r[3] or "[]") if x), "license": r[4] or "",
                    "sensitive": b(r[5]), "local_only": b(r[6])}
            try:
                got_aliases = sorted(x for x in (json.loads(t[2]) if t[2] else []) or [] if x)
            except ValueError:
                got_aliases = None
            got = {"category": t[1] or "", "aliases": got_aliases, "license": t[3] or "", "sensitive": b(t[4]), "local_only": b(t[5])}
            for k in want:
                if want[k]:
                    counts[k] += 1
                if want[k] != got[k]:
                    diffs.append("%s: emoji %s differs" % (safe(r[0]), k))
        self.add("E1 local emoji metadata", not diffs, "misskey local emoji=%d community=%d; misskey with category=%d aliases=%d license=%d sensitive=%d localOnly=%d"
                 % (len(src), len(tgt), counts["category"], counts["aliases"], counts["license"], counts["sensitive"], counts["local_only"]), diffs)

    # ---------------------------------------------------------- meta
    def meta_checks(self):
        cols = {"iconUrl": "branding.icon_url", "app192IconUrl": "branding.app192_icon_url", "app512IconUrl": "branding.app512_icon_url",
                "bannerUrl": "branding.banner_url", "backgroundImageUrl": "branding.background_image_url", "infoImageUrl": "branding.info_image_url",
                "serverErrorImageUrl": "branding.server_error_image_url", "notFoundImageUrl": "branding.not_found_image_url"}
        meta = self.mk.q("SELECT %s FROM meta m LIMIT 1" % ", ".join("to_jsonb(m)->>%s" % lit(c) for c in cols))
        meta = meta[0] if meta else [None] * len(cols)
        settings = {r[0]: (r[1] or "").strip().strip('"') for r in self.cm.q("SELECT `key`, val FROM system_settings" if not self.pg else "SELECT key, val FROM system_settings")}
        rows, unset = [], []
        for (col, key), v in zip(cols.items(), meta):
            if v:
                rows.append("%s=%s" % (col, "set" if settings.get(key) else "unset"))
                if not settings.get(key):
                    unset.append("meta.%s set in misskey, %s empty in community (file not exported, or meta import pending)" % (col, key))
        self.add("M1 instance meta assets", None if unset else True, "misskey meta assets: %s" % (", ".join(rows) or "none"), unset)


def run(args):
    rep = Report(args.verbose)
    mk = DB("misskey", args.misskey_dsn, args.timeout)
    cm = DB("community", args.community_dsn, args.timeout)
    c = Compare(mk, cm, args.origin, rep, args.samples, random.Random(args.seed))
    c.notes_checks()
    c.reaction_checks()
    c.poll_checks()
    c.pin_checks()
    c.emoji_checks()
    c.meta_checks()
    failed = [n for n, s in rep.results if s == "FAIL"]
    print("\nSUMMARY: %d checks, %d PASS, %d WARN, %d FAIL" % (len(rep.results), sum(s == "PASS" for _, s in rep.results), sum(s == "WARN" for _, s in rep.results), len(failed)))
    return 1 if failed else 0


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0], formatter_class=argparse.RawDescriptionHelpFormatter, epilog=__doc__.split("\n\n", 1)[1])
    ap.add_argument("--misskey-dsn", default=os.environ.get("MISSKEY_DB_DSN"), help="read-only Misskey PostgreSQL DSN (env MISSKEY_DB_DSN)")
    ap.add_argument("--community-dsn", default=os.environ.get("COMMUNITY_DB_DSN"), help="Community DB DSN (env COMMUNITY_DB_DSN)")
    ap.add_argument("--origin", default=os.environ.get("ORIGIN", "https://maid.lat"), help="source origin, default https://maid.lat")
    ap.add_argument("--samples", type=int, default=10, help="random mismatch ids shown per check (default 10; -v shows all)")
    ap.add_argument("--seed", type=int, default=None, help="random seed for reproducible samples")
    ap.add_argument("--timeout", type=int, default=600, help="per query timeout seconds (default 600)")
    ap.add_argument("-v", "--verbose", action="store_true", help="list every mismatch id")
    args = ap.parse_args()
    if args.verbose:
        args.samples = 1 << 30
    missing = [n for n, v in (("MISSKEY_DB_DSN", args.misskey_dsn), ("COMMUNITY_DB_DSN", args.community_dsn)) if not v]
    if missing:
        ap.error("missing: " + ", ".join(missing))
    try:
        sys.exit(run(args))
    except Exception as e:  # no traceback: it could contain DSNs
        print("ERROR: %s" % e, file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    main()
