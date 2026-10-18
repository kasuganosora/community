"""Unit tests for verify_misskey_import.py false-pass regressions (no DB/HTTP needed)."""
import argparse
import contextlib
import io
import json
import unittest

import verify_misskey_import as v

PEM = "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----"
IPEM = "-----BEGIN PUBLIC KEY-----\nIIII\n-----END PUBLIC KEY-----"
O = "https://maid.lat"


class FakeDB:
    def __init__(self, name, world):
        self.name, self.w = name, world

    def scalar(self, sql):
        r = self.q(sql)
        return r[0][0] if r and r[0] else None

    def q(self, sql):
        w, s = self.w, sql
        if self.name == "misskey":
            if "instance.actor" in s:
                return [[w["mk_instance_key"]]]
            if 'count(*) FROM "user"' in s:
                return [[str(len(w["mk_users"]))]]
            if "user_keypair" in s:
                return [[u, "u" + u, PEM] for u in w["mk_users"]]
            if "FROM note" in s:
                return [["n1"]]
            if "FROM following" in s:
                return [["0", "0"]]
            return [["0"]]
        if "local_actor_uri FROM accounts" in s:
            return [[O + "/users/" + u] for u in w["cm_users"]]
        if "count(*) FROM accounts" in s:
            extra = 0 if "LIKE" in s or not w["cm_users"] else 1  # instance actor is local but not /users/*
            return [[str(len(w["cm_users"]) + extra)]]
        if "account_keys" in s:
            return [["1", w["cm_instance_key"], O + "/actor#main-key"]] if w["cm_users"] else []
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
        if path == "/actor":
            return 200, json.dumps({"id": O + "/actor", "publicKey": {"publicKeyPem": self.w["cm_instance_key"]}}).encode(), None
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

    def test_connection_error_reported_not_http0(self):
        code, out = run(world(down=True))
        self.assertEqual(code, 1)
        self.assertNotIn("HTTP 0", out)
        self.assertIn("Connection refused", out)


if __name__ == "__main__":
    unittest.main()
