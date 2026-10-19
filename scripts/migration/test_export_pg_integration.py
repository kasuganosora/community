"""Exporter integration tests against a real, disposable PostgreSQL database.

Opt-in and destructive for the named database (its public schema is dropped):
  EXPORT_TEST_PG_DB=scratch PGHOST=... PGUSER=... python3 -m unittest test_export_pg_integration
"""
import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import export_mastodon_db  # noqa: E402
import export_misskey  # noqa: E402

DB = os.environ.get("EXPORT_TEST_PG_DB", "")


def psql(*args, stdin=None):
    env = dict(os.environ, PGDATABASE=DB)
    return subprocess.run(["psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", *args], env=env, input=stdin, check=True, capture_output=True, text=True).stdout


@unittest.skipUnless(DB, "set EXPORT_TEST_PG_DB to a disposable database")
class MisskeyPG(unittest.TestCase):
    def setUp(self):
        psql("-v", "remote_notes=3000", "-v", "local_notes=200", "-f", str(HERE / "testdata" / "synthetic_misskey.sql"))
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        os.environ["PGDATABASE"] = DB

    def tearDown(self):
        self.tmp.cleanup()

    def run_export(self, out, *extra):
        argv = ["--base-url", "https://src.test", "--id-method", "aidx", "--source-version", "t", "--output", str(self.root / out),
                "--object-storage-mode", "reference", "--object-base-url", "https://h", "--batch-size", "97", "--progress-interval", "0", *extra]
        err = io.StringIO()
        with contextlib.redirect_stdout(io.StringIO()) as out_, contextlib.redirect_stderr(err):
            code = export_misskey.main(argv)
        self.assertIn(code, (0, 2), err.getvalue())
        return out_.getvalue(), err.getvalue()

    def bundle(self, out):
        return json.loads((self.root / out / "bundle.json").read_text())

    def assert_closed(self, b):
        posts = {p["id"] for p in b["posts"]}
        accounts = {a["id"] for a in b["accounts"]}
        media = {m["id"] for m in b["media"]}
        for p in b["posts"]:
            self.assertIn(p["author_id"], accounts)
            for ref in (p.get("reply_id"), p.get("reblog_id")):
                self.assertTrue(not ref or ref in posts, ref)
            self.assertTrue(set(p.get("media_ids") or []) <= media)
        for r in b["reactions"]:
            self.assertIn(r["post_id"], posts)
            self.assertIn(r["account_id"], accounts)
        for r in b["relations"]:
            self.assertIn(r["target_id"], accounts)

    def test_local_only_selection_is_closed_and_excludes_remote_cache(self):
        _, err = self.run_export("out")
        b = self.bundle("out")
        self.assert_closed(b)
        local = [p for p in b["posts"] if p["id"].startswith("00000002")]
        self.assertEqual(len(local), 200)
        self.assertEqual(len([p for p in b["posts"] if p["id"].startswith("00000003")]), 20)  # remote replies to local notes
        self.assertLess(len(b["posts"]), 600)  # not the 3000-note remote cache
        self.assertEqual(len(b["reactions"]), 5000)  # reactions on local notes only
        self.assertIn("progress stage=notes", err)
        polls = sorted(r["id"] for r in b["records"] if r["kind"] == "misskey:poll")
        self.assertEqual(polls, ["0000000200000050", "0000000200000100", "0000000200000150"])  # noteId, not the shared userId
        self.assertEqual(sorted(v["option"] for v in b["poll_votes"]), [0, 1])
        drive_only = [m for m in b["media"] if m["id"].startswith("00000009500")]
        self.assertEqual(len(drive_only), 5, "local drive-only files are exported")
        self.assertTrue(all(m.get("unattached") for m in drive_only))
        roles = {a.get("system_role"): a for a in b["accounts"] if a.get("system_role")}
        self.assertEqual(roles["instance_actor"]["actor_uri"], "https://src.test/users/0000000000000060")
        self.assertEqual(roles["relay_actor"]["actor_uri"], "https://src.test/users/0000000000000061")
        self.assertEqual([(r["inbox"], r["state"], r["actor_uri"]) for r in b["relays"]], [("https://relay.test/inbox", "accepted", "https://src.test/users/0000000000000061")])
        quotes = [p for p in b["posts"] if p.get("quote_id")]
        ids = {p["id"] for p in b["posts"]}
        self.assertTrue(all(p["quote_id"] in ids for p in quotes), "quote targets two hops away are fetched")
        self.assertTrue(any(not p["id"].startswith("00000002") for p in quotes), "remote quotes in scope")
        self.assertTrue(quotes)
        self.assertFalse(any(p.get("archive_only") for p in quotes))
        ids = [(r["kind"], r["id"]) for r in b["records"]]
        self.assertEqual(len(ids), len(set(ids)))
        _, err = self.run_export("full", "--include-remote-cache", "--state-dir", str(self.root / "full.state"))
        full = self.bundle("full")
        self.assertEqual(len(full["posts"]), 3220)
        self.assertEqual(len(full["reactions"]), 55000)
        self.assert_closed(full)

    def test_two_phase_final_sync_handles_new_updated_and_deleted_rows(self):
        out, _ = self.run_export("out", "--fetch-only")
        self.assertEqual(json.loads(out)["status"], "fetched")
        psql("-c", "UPDATE note SET text='EDITED', \"updatedAt\"=now() WHERE id=mkid(200000001); DELETE FROM note WHERE id=mkid(200000002);"
                   "INSERT INTO note(id, \"userId\", \"userHost\", text, \"replyId\") VALUES (mkid(200009999), mkid(1), NULL, 'NEW', mkid(100000777));")
        _, err = self.run_export("out", "--final-sync")
        self.assertIn("final sync: 1 staged rows deleted", err)
        posts = {p["id"]: p for p in self.bundle("out")["posts"]}
        self.assertEqual(posts["0000000200000001"]["text"], "EDITED")
        self.assertNotIn("0000000200000002", posts)
        self.assertEqual(posts["0000000200009999"]["reply_id"], "0000000100000777")
        self.assertIn("0000000100000777", posts)
        self.assert_closed(self.bundle("out"))


@unittest.skipUnless(DB, "set EXPORT_TEST_PG_DB to a disposable database")
class MastodonPG(unittest.TestCase):
    def test_local_only_selection(self):
        psql("-f", str(HERE / "testdata" / "synthetic_mastodon.sql"))
        os.environ["PGDATABASE"] = DB
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "out"
            with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                code = export_mastodon_db.main(["--base-url", "https://masto.test", "--output", str(out), "--object-storage-mode", "reference",
                                                "--object-base-url", "https://cdn.test", "--progress-interval", "0"])
            self.assertIn(code, (0, 2))
            b = json.loads((out / "bundle.json").read_text())
        posts = {p["id"] for p in b["posts"]}
        self.assertEqual(len([p for p in posts if p.startswith("5000")]), 50)
        self.assertEqual(len([p for p in posts if p.startswith("6000")]), 10)
        self.assertLess(len(posts), 200)
        self.assertEqual(len([r for r in b["relations"] if r["kind"] == "follow"]), 60)  # remote<->remote follows skipped
        self.assertNotIn("mastodon.preview_cards", {r["kind"] for r in b["records"]})
        for p in b["posts"]:
            self.assertTrue(not p.get("reply_id") or p["reply_id"] in posts)


if __name__ == "__main__":
    unittest.main()
