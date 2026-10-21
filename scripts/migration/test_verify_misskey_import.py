"""Unit tests for verify_misskey_import.py false-pass regressions (no DB/HTTP needed)."""
import argparse
import contextlib
import io
import json
from pathlib import Path
import sqlite3
import tempfile
import types
import unittest

import verify_misskey_import as v

PEM = "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----"
IPEM = "-----BEGIN PUBLIC KEY-----\nIIII\n-----END PUBLIC KEY-----"
O = "https://maid.lat"


class FakeDB:
    def __init__(self, name, world):
        self.name, self.w = name, world

    def has_table(self, table):
        return False

    def scalar(self, sql):
        r = self.q(sql)
        return r[0][0] if r and r[0] else None

    def q(self, sql):
        w, s = self.w, sql
        if self.name == "misskey":
            if "/* 2-full */" in s:
                return [[u, "u" + u, w.get("mk_keys", {}).get(u, PEM)] for u in w["mk_users"]] + [["9ia", "instance.actor", w["mk_instance_key"]]]
            if "instance.actor" in s:
                return [["9ia", w["mk_instance_key"]]]
            if "relay.actor" in s:
                return []
            if 'count(*) FROM "user"' in s:
                return [[str(len(w["mk_users"]))]]
            if "user_keypair" in s:
                return [[u, "u" + u, PEM] for u in w["mk_users"]]
            if "FROM note" in s:
                return [["n1"]]
            if "FROM following" in s:
                return [["0", "0"]]
            return [["0"]]
        if "/* 2-full */" in s:
            rows = [[str(10 + i), O + "/users/" + u, w.get("cm_keys", {}).get(u, PEM), O + "/users/" + u + "#main-key"] for i, u in enumerate(w["cm_users"])]
            return rows + ([["7", O + "/users/9ia", w["cm_instance_key"], None]] if w["cm_users"] else [])
        if "local_actor_uri FROM accounts" in s:
            return [[O + "/users/" + u] for u in w["cm_users"]]
        if "count(*) FROM accounts" in s:
            extra = 0 if "LIKE" in s or not w["cm_users"] else 1  # instance actor is local but not /users/*
            return [[str(len(w["cm_users"]) + extra)]]
        if "system_settings" in s:
            return [['"%s/users/9ia"' % O]] if w["cm_users"] and w.get("designated", True) else []
        if "account_keys" in s:
            return [["1", w["cm_instance_key"], O + "/users/9ia#main-key"]] if w["cm_users"] else []
        if "SELECT id FROM accounts" in s:
            for u in w["cm_users"]:
                if u in s:
                    return [["7"]]
            return []
        if "FROM follows" in s:
            return [["0", "0"]]
        return [["0"]]


class FakeHTTP:
    def __init__(self, world):
        self.w, self.last_error = world, None

    def req3(self, path, method="GET", accept=None):
        if self.w.get("down"):
            return None, b"", "URLError: [Errno 111] Connection refused"
        if path.startswith("/users/") and path.endswith("/inbox"):
            return 405, b"", None
        if path in ("/actor", "/users/9ia"):
            return 200, json.dumps({"id": O + "/users/9ia", "publicKey": {"id": O + "/users/9ia#main-key", "publicKeyPem": self.w["cm_instance_key"]}}).encode(), None
        if path.startswith("/users/"):
            uid = path.split("/")[2]
            if uid not in self.w["cm_users"]:
                return 404, b"", None
            return 200, json.dumps({"id": O + path, "publicKey": {"id": O + path + "#main-key", "publicKeyPem": PEM}}).encode(), None
        if path.startswith("/.well-known/webfinger"):
            uid = path.split("acct%3Au")[-1].split("acct:u")[-1].split("@")[0]
            return 200, json.dumps({"links": [{"rel": "self", "href": O + "/users/" + uid}]}).encode(), None
        if path.startswith("/notes/"):
            return 200, json.dumps({"id": O + path}).encode(), None
        return 404, b"", None

    def req(self, path, method="GET", accept=None):
        return self.req3(path, method, accept)[:2]

    json = v.HTTP.json
    describe = v.HTTP.describe


def run(world):
    args = argparse.Namespace(host="maid.lat", misskey_dsn="x", community_dsn="y", base="http://t", forwarded_proto="https",
                              timeout=5, seed=1, sample=10, tolerance=0.02, verbose=True)
    orig_db, orig_http = v.DB, v.HTTP
    v.DB = lambda name, dsn, timeout=60: FakeDB(name, world)
    v.HTTP = lambda *a: FakeHTTP(world)
    out = io.StringIO()
    try:
        with contextlib.redirect_stdout(out):
            code = v.run(args)
    finally:
        v.DB, v.HTTP = orig_db, orig_http
    return code, out.getvalue()


def world(**kw):
    w = dict(mk_users=["a1", "b2", "c3"], cm_users=["a1", "b2", "c3"], mk_instance_key=IPEM, cm_instance_key=IPEM)
    w.update(kw)
    return w


class VerifyTests(unittest.TestCase):
    def test_complete_import_passes(self):
        code, out = run(world())
        self.assertEqual(code, 0, out)

    def test_empty_target_fails(self):
        code, out = run(world(cm_users=[]))
        self.assertEqual(code, 1)
        self.assertIn("[FAIL] 0 target database populated", out)

    def test_missing_user_fails(self):
        code, out = run(world(cm_users=["a1", "b2"]))
        self.assertEqual(code, 1)
        self.assertIn("[FAIL] 1b every local user imported", out)
        self.assertIn("c3 @uc3: no community account", out)

    def test_instance_actor_key_compared_with_misskey(self):
        code, out = run(world(cm_instance_key=PEM))
        self.assertEqual(code, 1)
        self.assertIn("[FAIL] 5 instance actor key", out)

    def test_instance_actor_must_be_designated(self):
        # Key imported but the server would still mint and sign as <base>/actor.
        code, out = run(world(designated=False))
        self.assertEqual(code, 1)
        self.assertIn("[FAIL] 5 instance actor key", out)

    def test_full_key_check_passes_for_every_user(self):
        code, out = run(world())
        self.assertIn("[PASS] 2-full actor URI/publicKey (all local users, DB): 4/4", out)
        self.assertIn("instance.actor", out)

    def test_full_key_check_fails_on_one_wrong_key(self):
        bad = "-----BEGIN PUBLIC KEY-----\nBBBB\n-----END PUBLIC KEY-----"
        code, out = run(world(cm_keys={"b2": bad}))
        self.assertEqual(code, 1)
        self.assertIn("[FAIL] 2-full actor URI/publicKey (all local users, DB): 3/4", out)
        self.assertIn("b2 @ub2: account 11 public key sha256 %s != misskey %s" % (v.key_digest(bad)[:16], v.key_digest(PEM)[:16]), out)
        self.assertNotIn("BBBB", out, "key material is never printed")

    def test_full_key_check_whitespace_normalized(self):
        code, out = run(world(cm_keys={"a1": PEM.replace("\n", "\r\n") + "\n"}))
        self.assertIn("[PASS] 2-full", out)

    def test_full_key_check_fails_on_missing_rows(self):
        code, out = run(world(cm_users=["a1", "b2"], cm_keys={"a1": None}, mk_keys={"b2": None}))
        self.assertEqual(code, 1)
        self.assertIn("[FAIL] 2-full", out)
        self.assertIn("c3 @uc3: no community account with local_actor_uri %s/users/c3" % O, out)
        self.assertIn("a1 @ua1: account 10 has no account_keys.public_key", out)
        self.assertIn("b2 @ub2: misskey user_keypair.publicKey missing", out)

    def test_full_key_check_instance_actor_key(self):
        code, out = run(world(cm_instance_key=PEM))
        self.assertIn("9ia @instance.actor (instance actor): account 7 public key sha256", out)

    def test_connection_error_reported_not_http0(self):
        code, out = run(world(down=True))
        self.assertEqual(code, 1)
        self.assertNotIn("HTTP 0", out)
        self.assertIn("Connection refused", out)


if __name__ == "__main__":
    unittest.main()


class ProfileChecksTest(unittest.TestCase):
    class Rows:
        def __init__(self, rows):
            self.rows = rows

        def q(self, sql):
            return self.rows

    def run_checks(self, src, tgt):
        rep = v.Report(False)
        with contextlib.redirect_stdout(io.StringIO()) as out:
            v.profile_checks(self.Rows(src), self.Rows(tgt), O, rep)
        return dict(rep.results), out.getvalue()

    def test_drops_fail_and_matches_pass_local_and_remote(self):
        R = "https://remote.test/users/r"
        src = [["a1", "t", None, "true", "ja-JP", "Tokyo", "2000-02-29", "hi", None, '["https://old.test/a"]'],
               ["r1", "f", R, "true", None, "x" * 300, "bad", "hello", "https://moved.test/r", None],
               ["r2", "f", "https://unimported.test/u", "true", None, None, None, None, None, None]]
        good = [["t", O + "/users/a1", None, "true", "ja-JP", "Tokyo", "2000-02-29", "hi", "", '["https://old.test/a"]'],
                ["f", None, R, "true", "", "x" * 128, "", "hello", "https://moved.test/r", None]]
        res, out = self.run_checks(src, good)
        self.assertEqual(set(res.values()), {"PASS"}, out)
        self.assertIn("misskey set local=1 remote(imported)=1", out)
        dropped = [["t", O + "/users/a1", None, "false", "", "", "", "", "", None], ["f", None, R, "false", "", "", "", "", "", None]]
        res, out = self.run_checks(src, dropped)
        self.assertEqual(res["8a profile isCat -> is_cat"], "FAIL")
        self.assertEqual(res["8f profile movedToUri -> moved_to"], "FAIL")
        self.assertEqual(res["8d profile birthday -> birthday"], "FAIL")
        self.assertIn("remote r1: is_cat differs", out)
        self.assertNotIn("Tokyo", out, "values are never printed")

    def test_also_known_as_simple_array_text(self):
        # Misskey's user.alsoKnownAs is TypeORM simple-array: comma-joined text.
        src = [["a1", "t", None, None, None, None, None, None, None, "https://old.test/a, https://older.test/a,https://old.test/a"],
               ["r1", "f", "https://remote.test/users/r", None, None, None, None, None, None, "https://alias.test/r,javascript:alert(1)"]]
        good = [["t", O + "/users/a1", None, "", "", "", "", "", "", '["https://old.test/a","https://older.test/a"]'],
                ["f", None, "https://remote.test/users/r", "", "", "", "", "", "", '["https://alias.test/r"]']]
        res, out = self.run_checks(src, good)
        self.assertEqual(res["8g profile alsoKnownAs -> also_known_as"], "PASS", out)
        self.assertIn("misskey set local=1 remote(imported)=1; matching in community local=1 remote=1", out)
        dropped = [r[:9] + [None] for r in good]
        res, out = self.run_checks(src, dropped)
        self.assertEqual(res["8g profile alsoKnownAs -> also_known_as"], "FAIL", out)
        self.assertIn("remote r1: also_known_as differs", out)

    def test_also_known_as_text_array_and_json_forms(self):
        for raw in ('["https://old.test/a"]', "{https://old.test/a}"):
            src = [["a1", "t", None, None, None, None, None, None, None, raw]]
            tgt = [["t", O + "/users/a1", None, "", "", "", "", "", "", '["https://old.test/a"]']]
            res, out = self.run_checks(src, tgt)
            self.assertEqual(res["8g profile alsoKnownAs -> also_known_as"], "PASS", out)

    def test_source_values_never_counted_fail_instead_of_0_eq_0(self):
        # Round 9: the source was set but nothing parsed, so 0 == 0 passed.
        src = [["r1", "f", "https://remote.test/users/r", None, None, None, None, None, None, "acct:r@remote.test,/users/r"]]
        tgt = [["f", None, "https://remote.test/users/r", "", "", "", "", "", "", None]]
        res, out = self.run_checks(src, tgt)
        self.assertEqual(res["8g profile alsoKnownAs -> also_known_as"], "FAIL", out)
        self.assertIn("none parsed as storable", out)

    def test_invalid_birthdays_excluded_and_reported_as_info(self):
        R = "https://remote.test/users/"
        src = [["r1", "f", R + "1", None, None, None, "0000-03-04", None, None, None],
               ["r2", "f", R + "2", None, None, None, "2999-01-01", None, None, None],
               ["r3", "f", R + "3", None, None, None, " 1990-05-06 ", None, None, None],
               ["r4", "f", R + "4", None, None, None, "2001-02-29", None, None, None]]
        tgt = [["f", None, R + str(i), "", "", "", "1990-05-06" if i == 3 else "", "", "", None] for i in range(1, 5)]
        res, out = self.run_checks(src, tgt)
        self.assertEqual(res["8d profile birthday -> birthday"], "PASS", out)
        self.assertIn("[INFO] 8d profile birthday -> birthday: 3 in-scope misskey value(s) excluded", out)
        self.assertIn("misskey set local=0 remote(imported)=1", out)
        tgt[2][6] = ""
        res, out = self.run_checks(src, tgt)
        self.assertEqual(res["8d profile birthday -> birthday"], "FAIL", out)


class RulesTest(unittest.TestCase):
    def test_shared_vectors(self):
        import datetime
        import os
        import export_misskey as e
        with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "testdata", "misskey_value_rules.json")) as f:
            vec = json.load(f)
        for c in vec["also_known_as"]:
            self.assertEqual(e.split_also_known_as(c["raw"]), c["split"], c["name"])
            self.assertEqual(e.also_known_as_values(c["raw"]), c["valid"], c["name"])
            # verify compares exactly what the importer keeps
            raw = c["raw"] if c["raw"] is None or isinstance(c["raw"], str) else json.dumps(c["raw"])
            self.assertEqual(v.profile_expected("alsoKnownAs", raw), sorted(c["valid"]), c["name"])
        now = datetime.datetime.fromisoformat(vec["birthday"]["now"].replace("Z", "+00:00"))
        for c in vec["birthday"]["cases"]:
            self.assertEqual(e.valid_birthday(c["raw"], now), c["valid"], c["raw"])


class LastActiveTest(unittest.TestCase):
    Rows = ProfileChecksTest.Rows

    def run_checks(self, src_last, tgt_last, extracted_at):
        src = [["a1", "t", None, None, None, None, None, None, src_last]]
        tgt = [["t", O + "/users/a1", None, "", "", "", "", "", tgt_last]]
        rep = v.Report(False)
        with contextlib.redirect_stdout(io.StringIO()) as out:
            v.privacy_checks(self.Rows(src), self.Rows(tgt), O, rep, extracted_at)
        return dict(rep.results)["8m account lastActiveDate -> last_active_at"], out.getvalue()

    def test_active_after_extraction_is_info(self):
        st, out = self.run_checks("2026-10-19 10:00:00+00", "2026-10-19T08:00:00Z", "2026-10-19T09:00:00Z")
        self.assertEqual(st, "PASS", out)
        self.assertIn("[INFO] 8m", out)
        self.assertIn("active after extraction)=1", out)
        # also when the account had no last-active time at extraction
        st, out = self.run_checks("2026-10-19 10:00:00+00", "", "2026-10-19T09:00:00Z")
        self.assertEqual(st, "PASS", out)

    def test_difference_before_extraction_fails(self):
        st, out = self.run_checks("2026-10-19 08:30:00+00", "2026-10-19T08:00:00Z", "2026-10-19T09:00:00Z")
        self.assertEqual(st, "FAIL", out)
        st, out = self.run_checks("2026-10-19 08:00:00+00", "2026-10-19T09:00:00Z", None)
        self.assertEqual(st, "FAIL", "a target newer than the source is never tolerated: " + out)

    def test_without_extracted_at_source_newer_is_info(self):
        st, out = self.run_checks("2026-10-19 10:00:00+00", "2026-10-19T08:00:00Z", None)
        self.assertEqual(st, "PASS", out)
        self.assertIn("pass --extracted-at", out)
        st, out = self.run_checks("2026-10-19 10:00:00+00", "", None)
        self.assertEqual(st, "FAIL", out)


class ExtractedAtDefaultTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)

    def tearDown(self):
        self.tmp.cleanup()

    def args(self, bundle, extracted_at=None):
        return types.SimpleNamespace(bundle=str(bundle), extracted_at=extracted_at)

    def test_bundle_json_report_and_spool(self):
        ts = "2026-10-19T01:02:03.456789Z"
        d = self.root / "bundle"
        d.mkdir()
        (d / "bundle.json").write_text(json.dumps({"schema": "community-transfer/v1", "scope": "site",
                                                     "source": {"platform": "misskey", "exported_at": ts},
                                                     "posts": [{"extensions": {"source": {"exported_at": "x"}}}]}))
        report = self.root / "report.json"
        report.write_text(json.dumps({"can_execute": True, "source": {"platform": "misskey", "exported_at": ts}, "counts": {}}) + "\n")
        work = self.root / "work"
        work.mkdir()
        db = sqlite3.connect(str(work / "spool.sqlite3"))
        db.execute("CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)")
        db.execute("INSERT INTO meta VALUES ('header', ?)", (json.dumps({"source": {"exported_at": ts}}),))
        db.commit()
        db.close()
        for p in (d, d / "bundle.json", report, work, work / "spool.sqlite3"):
            self.assertEqual(v.source_exported_at(p), ts, p)
            a = self.args(p)
            v.default_extracted_at(a, out=io.StringIO())
            self.assertEqual(a.extracted_at, ts, p)

    def test_zero_or_missing_exported_at_is_ignored(self):
        for src in ({"exported_at": "0001-01-01T00:00:00Z"}, {"exported_at": ""}, {}):
            p = self.root / "report.json"
            p.write_text(json.dumps({"can_execute": True, "source": src}))
            self.assertEqual(v.source_exported_at(p), "")
            a, out = self.args(p), io.StringIO()
            v.default_extracted_at(a, out=out)
            self.assertIsNone(a.extracted_at)
            self.assertIn("no source.exported_at", out.getvalue())

    def test_explicit_extracted_at_wins(self):
        p = self.root / "report.json"
        p.write_text(json.dumps({"source": {"exported_at": "2026-10-19T01:00:00Z"}}))
        a = self.args(p, "2026-10-18T00:00:00Z")
        v.default_extracted_at(a, out=io.StringIO())
        self.assertEqual(a.extracted_at, "2026-10-18T00:00:00Z")

    def test_unreadable_bundle_warns(self):
        a, out = self.args(self.root / "missing.json"), io.StringIO()
        v.default_extracted_at(a, out=out)
        self.assertIsNone(a.extracted_at)
        self.assertIn("WARNING", out.getvalue())


class PinCheckTest(unittest.TestCase):
    class DB:
        def __init__(self, rows, scalars=()):
            self.rows, self.scalars = rows, list(scalars)

        def has_table(self, t):
            return True

        def q(self, sql):
            assert sql.lstrip().upper().startswith("SELECT")
            return self.rows

        def scalar(self, sql):
            return self.scalars.pop(0)

    def run_check(self, mk_counts, cm_local, cm_remote="0"):
        rep = v.Report(False)
        with contextlib.redirect_stdout(io.StringIO()) as out:
            v.pin_check(self.DB([mk_counts]), self.DB([], [cm_local, cm_remote]), O, rep)
        return dict(rep.results)["9 pinned notes (local users)"], out.getvalue()

    def test_remote_pins_are_info_only(self):
        st, out = self.run_check(["1", "30"], "1")
        self.assertEqual(st, "PASS", out)
        self.assertIn("[INFO] 9 pinned notes (local users): remote users' pins skipped by design: misskey=30", out)

    def test_missing_local_pin_fails(self):
        st, out = self.run_check(["1", "30"], "0")
        self.assertEqual(st, "FAIL", out)


class DecorationCheckTest(unittest.TestCase):
    class DB:
        def __init__(self, tables, answers):
            self.tables, self.answers = tables, answers

        def has_table(self, t):
            return t in self.tables

        def q(self, sql):
            for key, rows in self.answers:
                if key in sql:
                    return rows
            raise AssertionError(sql)

    def run_check(self, mk_defs, mk_sel, cm_defs, receipts, cm_sel):
        mk = self.DB({"avatar_decoration"}, [("FROM avatar_decoration", [[d] for d in mk_defs]), ("avatarDecorations", mk_sel)])
        cm = self.DB({"avatar_decorations", "data_transfer_receipts"}, [("FROM avatar_decorations", [[d] for d in cm_defs]),
                                                                       ("data_transfer_receipts", receipts), ("avatar_decorations FROM accounts", cm_sel)])
        rep = v.Report(True)
        with contextlib.redirect_stdout(io.StringIO()) as out:
            v.decoration_check(mk, cm, O, rep)
        return dict(rep.results), out.getvalue()

    MK = [["a1", json.dumps([{"id": "d1", "angle": 0.125, "flipH": True, "offsetX": 0.05, "offsetY": -0.1}, {"id": "gone"}, {"id": "d2"}])],
          ["b2", json.dumps([{"id": "d2", "angle": -0.7}])]]
    REC = [["d1", "101"], ["d2", "102"]]

    def good(self):
        return [[O + "/users/a1", json.dumps([{"id": "101", "angle": 0.125, "flip_h": True, "offset_x": 0.05, "offset_y": -0.1}, {"id": "102"}])],
                [O + "/users/b2", json.dumps([{"id": "102", "angle": -0.5}])]]

    def test_matching_import_passes(self):
        res, out = self.run_check(["d1", "d2"], self.MK, ["101", "102", "5"], self.REC, self.good())
        self.assertEqual(res, {"10a avatar decoration definitions": "PASS", "10b avatar decoration selections": "PASS"}, out)
        self.assertIn("a1: gone", out, "unknown decoration is info, not a failure")

    def test_missing_definition_fails(self):
        res, out = self.run_check(["d1", "d2"], self.MK, ["101"], self.REC, self.good())
        self.assertEqual(res["10a avatar decoration definitions"], "FAIL")
        self.assertEqual(res["10b avatar decoration selections"], "FAIL")

    def test_geometry_difference_fails(self):
        cm = self.good()
        cm[0][1] = json.dumps([{"id": "101", "angle": 0.125, "offset_x": 0.05, "offset_y": -0.1}, {"id": "102"}])
        res, out = self.run_check(["d1", "d2"], self.MK, ["101", "102"], self.REC, cm)
        self.assertEqual(res["10b avatar decoration selections"], "FAIL")
        self.assertIn("a1: misskey 2 decoration(s), community 2; first difference at #0", out)

    def test_missing_selection_and_extra_selection_fail(self):
        cm = self.good()[:1] + [[O + "/users/z9", json.dumps([{"id": "101"}])]]
        res, out = self.run_check(["d1", "d2"], self.MK, ["101", "102"], self.REC, cm)
        self.assertEqual(res["10b avatar decoration selections"], "FAIL")
        self.assertIn("b2: misskey 1 decoration(s), community 0", out)
        self.assertIn("z9: community has decorations, misskey has none", out)
